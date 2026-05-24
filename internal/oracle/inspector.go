package oracle

import (
	"debug/elf"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
)

// BinaryInspector is the read-only view of a compiled binary used by static
// invariant checkers. It is intentionally minimal so multiple ELF / Mach-O /
// PE backends can implement the same surface; today only ELF is supported.
//
// All methods are safe for concurrent use; underlying ELF parsing is done
// once on the first call and cached.
type BinaryInspector interface {
	// Path returns the binary file path the inspector was constructed with.
	Path() string
	// Exists reports whether the binary file exists and is readable. A
	// false result short-circuits all other methods to (zero, ErrBinaryMissing).
	Exists() bool
	// IsELF reports whether the file is a valid ELF object. False for
	// non-ELF formats (Mach-O on macOS hosts, PE, scripts).
	IsELF() bool
	// Symbols returns the union of static (.symtab) and dynamic (.dynsym)
	// symbol names. Empty list with nil error if the binary is stripped.
	Symbols() ([]string, error)
	// HasSymbol reports whether `name` appears in any symbol table
	// (static or dynamic, defined or undefined). Useful for asking
	// "does this binary reference __stack_chk_guard?".
	HasSymbol(name string) (bool, error)
	// ImportedFunctions returns the names of dynamic, undefined function
	// symbols (i.e., functions resolved at load time from shared libs).
	// Distinguishes "this binary calls __stack_chk_fail" from
	// "this binary defines __stack_chk_fail".
	ImportedFunctions() ([]string, error)
	// FunctionSymbols returns all defined STT_FUNC entries with non-zero
	// size from `.symtab` (preferred) merged with `.dynsym`. Used by
	// static checkers that need precise function address ranges
	// (e.g., scanning `.text` for unintended ENDBR landing pads inside
	// function bodies — INV-IBT-B03).
	FunctionSymbols() ([]FunctionSymbol, error)
	// ExecutableSections returns the raw bytes plus base address of every
	// SHF_ALLOC | SHF_EXECINSTR section. For relocatable objects (`.o`)
	// sh_addr is usually 0, so addresses are section-relative and the
	// caller must reconcile with FunctionSymbol.Addr which uses the same
	// reference frame.
	ExecutableSections() ([]ExecSection, error)
	// Machine returns the ELF e_machine value (EM_386, EM_X86_64, ...).
	Machine() (elf.Machine, error)
	// Class returns the ELF EI_CLASS (ELFCLASS32 / ELFCLASS64).
	Class() (elf.Class, error)
}

// FunctionSymbol describes a defined function symbol with its absolute (or
// section-relative for relocatable objects) start address and byte size.
//
// Only symbols with STT_FUNC and Size > 0 are returned by
// `BinaryInspector.FunctionSymbols`. Aliases (multiple symbols at the same
// address with the same size) are deduplicated by `(Addr, Size)` with the
// first encountered name kept; this is OK for the current consumers which
// only need address ranges, not name multiplicity.
type FunctionSymbol struct {
	Name string
	Addr uint64
	Size uint64
	// SectionIdx is the ELF section index (0 == SHN_UNDEF; sane symbols
	// have idx > 0). Useful for cross-checking against ExecSection ordering.
	SectionIdx int
}

// ExecSection is a snapshot of an executable section's bytes plus the
// address its first byte occupies. For ET_REL objects (relocatable `.o`)
// Addr is usually 0; for ET_EXEC / ET_DYN it is the virtual address.
type ExecSection struct {
	Name       string
	Addr       uint64
	Data       []byte
	SectionIdx int
}

// ErrBinaryMissing is returned by inspector methods when the binary path
// does not exist or could not be opened. Static checkers should treat this
// as VerdictNotApplicable, not VerdictError, because it commonly happens in
// unit tests with mock paths.
var ErrBinaryMissing = errors.New("binary file not present or not readable")

// ErrNotELF is returned by inspector methods when the binary is not in ELF
// format. Static checkers that are ELF-specific should map this to
// VerdictNotApplicable with a clear Reason.
var ErrNotELF = errors.New("binary is not in ELF format")

// NewBinaryInspector returns an inspector for the given path. The file is
// not opened until the first method that needs ELF data is called; this
// keeps construction cheap and lets tests pass mock paths without I/O.
func NewBinaryInspector(path string) BinaryInspector {
	return &elfInspector{path: path}
}

// elfInspector is the default implementation backed by Go's stdlib
// `debug/elf` package. We deliberately avoid shelling out to nm / objdump /
// readelf so that:
//   - cross-host portability is preserved (no GNU binutils required);
//   - tests can run hermetically with a temp ELF file;
//   - error semantics are explicit Go errors instead of stderr scraping.
type elfInspector struct {
	path string

	once     sync.Once
	exists   bool
	isELF    bool
	syms     []string
	imps     []string
	funcs    []FunctionSymbol
	execs    []ExecSection
	machine  elf.Machine
	class    elf.Class
	parseErr error
}

func (e *elfInspector) Path() string { return e.path }

func (e *elfInspector) Exists() bool {
	e.parseOnce()
	return e.exists
}

func (e *elfInspector) IsELF() bool {
	e.parseOnce()
	return e.isELF
}

func (e *elfInspector) Symbols() ([]string, error) {
	e.parseOnce()
	if !e.exists {
		return nil, ErrBinaryMissing
	}
	if !e.isELF {
		return nil, ErrNotELF
	}
	if e.parseErr != nil {
		return nil, e.parseErr
	}
	// Defensive copy so callers can't mutate the cached slice.
	out := make([]string, len(e.syms))
	copy(out, e.syms)
	return out, nil
}

func (e *elfInspector) HasSymbol(name string) (bool, error) {
	syms, err := e.Symbols()
	if err != nil {
		return false, err
	}
	for _, s := range syms {
		if s == name {
			return true, nil
		}
	}
	return false, nil
}

func (e *elfInspector) ImportedFunctions() ([]string, error) {
	e.parseOnce()
	if !e.exists {
		return nil, ErrBinaryMissing
	}
	if !e.isELF {
		return nil, ErrNotELF
	}
	if e.parseErr != nil {
		return nil, e.parseErr
	}
	out := make([]string, len(e.imps))
	copy(out, e.imps)
	return out, nil
}

func (e *elfInspector) FunctionSymbols() ([]FunctionSymbol, error) {
	e.parseOnce()
	if !e.exists {
		return nil, ErrBinaryMissing
	}
	if !e.isELF {
		return nil, ErrNotELF
	}
	if e.parseErr != nil {
		return nil, e.parseErr
	}
	out := make([]FunctionSymbol, len(e.funcs))
	copy(out, e.funcs)
	return out, nil
}

func (e *elfInspector) ExecutableSections() ([]ExecSection, error) {
	e.parseOnce()
	if !e.exists {
		return nil, ErrBinaryMissing
	}
	if !e.isELF {
		return nil, ErrNotELF
	}
	if e.parseErr != nil {
		return nil, e.parseErr
	}
	// Defensive copy of the slice header; the underlying byte buffers are
	// shared (immutable in practice — debug/elf returned freshly-allocated
	// slices in parseOnce, and callers must treat them as read-only).
	out := make([]ExecSection, len(e.execs))
	copy(out, e.execs)
	return out, nil
}

func (e *elfInspector) Machine() (elf.Machine, error) {
	e.parseOnce()
	if !e.exists {
		return 0, ErrBinaryMissing
	}
	if !e.isELF {
		return 0, ErrNotELF
	}
	return e.machine, nil
}

func (e *elfInspector) Class() (elf.Class, error) {
	e.parseOnce()
	if !e.exists {
		return 0, ErrBinaryMissing
	}
	if !e.isELF {
		return 0, ErrNotELF
	}
	return e.class, nil
}

// parseOnce loads the ELF symbol tables exactly once per inspector instance.
// All errors are stored on the receiver and surfaced via the typed methods;
// this keeps the BinaryInspector interface clean of "init / lazy" knobs.
func (e *elfInspector) parseOnce() {
	e.once.Do(func() {
		if e.path == "" {
			return
		}
		fi, err := os.Stat(e.path)
		if err != nil || fi.IsDir() {
			return
		}
		e.exists = true

		f, err := elf.Open(e.path)
		if err != nil {
			// Not ELF (or unreadable as ELF). Distinguish the two by
			// peeking at the magic; for now, report not-ELF, since
			// elf.Open returns a similar error for both.
			return
		}
		defer f.Close()
		e.isELF = true
		e.machine = f.Machine
		e.class = f.Class

		// Collect SHF_EXECINSTR sections (typically `.text`, `.plt`, `.init`,
		// `.fini`). We snapshot the raw bytes here so callers don't need to
		// re-open the ELF; sections are usually small relative to the binary.
		for i, sec := range f.Sections {
			if sec.Type != elf.SHT_PROGBITS {
				continue
			}
			if sec.Flags&elf.SHF_EXECINSTR == 0 {
				continue
			}
			data, derr := sec.Data()
			if derr != nil {
				if e.parseErr == nil {
					e.parseErr = fmt.Errorf("read section %q: %w", sec.Name, derr)
				}
				continue
			}
			e.execs = append(e.execs, ExecSection{
				Name:       sec.Name,
				Addr:       sec.Addr,
				Data:       data,
				SectionIdx: i,
			})
		}

		symSet := make(map[string]struct{})
		funcSet := make(map[uint64]FunctionSymbol)
		// Static symbols: may be absent in stripped binaries.
		if syms, err := f.Symbols(); err == nil {
			for _, s := range syms {
				if s.Name != "" {
					symSet[s.Name] = struct{}{}
				}
				collectFunctionSymbol(funcSet, s)
			}
		} else if !errors.Is(err, elf.ErrNoSymbols) {
			e.parseErr = fmt.Errorf("read .symtab: %w", err)
		}
		// Dynamic symbols: present in dynamically linked binaries; this is
		// where we'll see __stack_chk_fail / __stack_chk_guard imports.
		if dyn, err := f.DynamicSymbols(); err == nil {
			for _, s := range dyn {
				if s.Name == "" {
					continue
				}
				symSet[s.Name] = struct{}{}
				// Imported function: undefined section + STT_FUNC.
				if s.Section == elf.SHN_UNDEF && elf.ST_TYPE(s.Info) == elf.STT_FUNC {
					e.imps = append(e.imps, s.Name)
				}
				collectFunctionSymbol(funcSet, s)
			}
		} else if !errors.Is(err, elf.ErrNoSymbols) && e.parseErr == nil {
			e.parseErr = fmt.Errorf("read .dynsym: %w", err)
		}

		e.syms = make([]string, 0, len(symSet))
		for s := range symSet {
			e.syms = append(e.syms, s)
		}

		e.funcs = make([]FunctionSymbol, 0, len(funcSet))
		for _, fs := range funcSet {
			e.funcs = append(e.funcs, fs)
		}
		sort.Slice(e.funcs, func(i, j int) bool {
			if e.funcs[i].Addr != e.funcs[j].Addr {
				return e.funcs[i].Addr < e.funcs[j].Addr
			}
			return e.funcs[i].Size < e.funcs[j].Size
		})
	})
}

// collectFunctionSymbol records a defined STT_FUNC symbol with non-zero
// size. Aliases (same Addr+Size) are deduplicated by keeping the first
// encountered name; this matches the consumer contract of returning
// address ranges, not name multiplicity.
//
// Symbols with section index SHN_UNDEF (imports) and Size == 0 are
// rejected because they don't bound an actual function body.
func collectFunctionSymbol(set map[uint64]FunctionSymbol, s elf.Symbol) {
	if elf.ST_TYPE(s.Info) != elf.STT_FUNC {
		return
	}
	if s.Section == elf.SHN_UNDEF {
		return
	}
	if s.Size == 0 {
		return
	}
	if _, exists := set[s.Value]; exists {
		return
	}
	set[s.Value] = FunctionSymbol{
		Name:       s.Name,
		Addr:       s.Value,
		Size:       s.Size,
		SectionIdx: int(s.Section),
	}
}

// hasAnyPrefix is a tiny helper used by checkers to match a family of
// related symbols (e.g., "__stack_chk_*").
func hasAnyPrefix(s string, prefixes ...string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}
