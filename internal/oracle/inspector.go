package oracle

import (
	"debug/elf"
	"errors"
	"fmt"
	"os"
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

	once   sync.Once
	exists bool
	isELF  bool
	syms   []string
	imps   []string
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

		symSet := make(map[string]struct{})
		// Static symbols: may be absent in stripped binaries.
		if syms, err := f.Symbols(); err == nil {
			for _, s := range syms {
				if s.Name != "" {
					symSet[s.Name] = struct{}{}
				}
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
			}
		} else if !errors.Is(err, elf.ErrNoSymbols) && e.parseErr == nil {
			e.parseErr = fmt.Errorf("read .dynsym: %w", err)
		}

		e.syms = make([]string, 0, len(symSet))
		for s := range symSet {
			e.syms = append(e.syms, s)
		}
	})
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
