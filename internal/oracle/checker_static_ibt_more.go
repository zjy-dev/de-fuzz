package oracle

import (
	"debug/elf"
	"fmt"
	"sort"
	"strings"

	"golang.org/x/arch/x86/x86asm"
)

// checker_static_ibt_more.go implements the IBT invariants from
// `docs/tech-docs/invariants/endbr-ibt.md` beyond INV-IBT-B01:
//
//   - INV-IBT-P01  IndirectCallableEndbrChecker
//   - INV-IBT-P02  SetjmpReturnEndbrChecker
//   - INV-IBT-P03  EHLandingPadEndbrChecker
//   - INV-IBT-P04  IFUNCResolverEndbrChecker
//   - INV-IBT-P05  NestedFuncTrampolineEndbrChecker (static)
//   - INV-IBT-P06  IndirectBranchTargetEndbrChecker
//   - INV-IBT-N01  NotrackPrefixGuardChecker
//   - INV-IBT-N02  FineIBTHashCollisionChecker
//   - INV-IBT-M01  IBTRuntimeEnforcementChecker
//
// All checkers are CategoryStatic except INV-IBT-M01 which is CategoryDynamic
// (it requires arch_prctl + dlopen at runtime; the static checker collapses
// to NA when no Executor is available).

// ---- shared static helpers --------------------------------------------------

// readByteAt returns the byte at virtual address `addr` from any
// executable section, or (0, false) if no section covers it.
func readByteAt(execs []ExecSection, addr uint64) (byte, bool) {
	for _, sec := range execs {
		if addr < sec.Addr {
			continue
		}
		off := addr - sec.Addr
		if off < uint64(len(sec.Data)) {
			return sec.Data[off], true
		}
	}
	return 0, false
}

// hasEndbrAt reports whether the 4 bytes starting at the given virtual
// address spell out `pattern`.
func hasEndbrAt(execs []ExecSection, addr uint64, pattern []byte) bool {
	for _, sec := range execs {
		if addr < sec.Addr {
			continue
		}
		off := addr - sec.Addr
		if off+uint64(len(pattern)) > uint64(len(sec.Data)) {
			continue
		}
		return IsEndbrAt(sec.Data, int(off), pattern)
	}
	return false
}

// initIBTResult populates the common fields shared by every checker in
// this file (ID, category, source URL, polarity flag).
func initIBTResult(id string, category InvariantCategory) InvariantResult {
	return InvariantResult{
		ID:          id,
		Category:    category,
		SourceURL:   "https://gcc.gnu.org/onlinedocs/gcc/Instrumentation-Options.html",
		Sensitivity: "likely-to-drift",
		Detail: map[string]any{
			"polarity_sensitive": true,
		},
	}
}

// fetchPattern is the small lookup that every IBT checker performs at
// the top: given the inspector, return either (pattern, ok) or a
// VerdictNotApplicable result with Reason populated.
func fetchPattern(ctx *CheckContext, r *InvariantResult) ([]byte, bool) {
	if ctx == nil || ctx.Inspector == nil {
		r.Verdict = VerdictNotApplicable
		r.Reason = "no inspector available (missing binary path)"
		return nil, false
	}
	machine, err := ctx.Inspector.Machine()
	if err != nil {
		r.Verdict = naOrError(err)
		r.Reason = fmt.Sprintf("inspector.Machine failed: %v", err)
		return nil, false
	}
	class, err := ctx.Inspector.Class()
	if err != nil {
		r.Verdict = naOrError(err)
		r.Reason = fmt.Sprintf("inspector.Class failed: %v", err)
		return nil, false
	}
	pat, ok := selectEndbrPattern(machine, class)
	if !ok {
		r.Verdict = VerdictNotApplicable
		r.Reason = fmt.Sprintf("non-x86 binary (machine=%v); IBT not applicable", machine)
		return nil, false
	}
	r.Detail["machine"] = machine.String()
	r.Detail["class"] = class.String()
	return pat, true
}

// ============================================================================
// INV-IBT-P01 — Indirect-callable function entries must begin with ENDBR.
// ============================================================================

// IndirectCallableEndbrChecker scans every "indirect-callable" function
// symbol (GLOBAL/WEAK binding, non-hidden visibility, or referenced by
// any non-PC32 relocation) and verifies its first 4 bytes are ENDBR.
//
// We deliberately accept a coarse over-approximation of "indirect
// callable":
//   - STB_GLOBAL / STB_WEAK functions whose visibility is NOT
//     STV_HIDDEN and NOT STV_INTERNAL are externally callable;
//   - any function whose address appears as the value of a R_X86_64_64
//     / R_X86_64_32 / R_X86_64_PLT32-on-non-call relocation is
//     address-taken.
//
// Address-taken via constant data (vtable slot in `.rodata`) is also
// handled by scanning ReadOnlySections for 8-byte aligned little-endian
// references to function symbol addresses. This covers C++ vtables and
// hand-rolled function-pointer tables.
type IndirectCallableEndbrChecker struct{}

func (c *IndirectCallableEndbrChecker) ID() string                { return "INV-IBT-P01" }
func (c *IndirectCallableEndbrChecker) Category() InvariantCategory { return CategoryStatic }

func (c *IndirectCallableEndbrChecker) Check(ctx *CheckContext) InvariantResult {
	r := initIBTResult(c.ID(), CategoryStatic)
	pattern, ok := fetchPattern(ctx, &r)
	if !ok {
		return r
	}
	extFuncs, err := ctx.Inspector.ExtendedFunctionSymbols()
	if err != nil {
		r.Verdict = naOrError(err)
		r.Reason = fmt.Sprintf("inspector.ExtendedFunctionSymbols failed: %v", err)
		return r
	}
	if len(extFuncs) == 0 {
		r.Verdict = VerdictNotApplicable
		r.Reason = "no STT_FUNC symbols; cannot enumerate indirect-callable functions"
		return r
	}
	execs, err := ctx.Inspector.ExecutableSections()
	if err != nil {
		r.Verdict = naOrError(err)
		r.Reason = fmt.Sprintf("inspector.ExecutableSections failed: %v", err)
		return r
	}
	if len(execs) == 0 {
		r.Verdict = VerdictNotApplicable
		r.Reason = "no executable sections"
		return r
	}

	addressTaken := collectAddressTakenAddrs(ctx, extFuncs)

	type viol struct {
		Func string
		Addr uint64
	}
	var violations []viol
	candidates := 0
	for _, ef := range extFuncs {
		if !isIndirectCallable(ef, addressTaken) {
			continue
		}
		candidates++
		if !hasEndbrAt(execs, ef.Addr, pattern) {
			violations = append(violations, viol{Func: ef.Name, Addr: ef.Addr})
		}
	}
	r.Detail["candidates"] = candidates
	r.Detail["violations_total"] = len(violations)

	if candidates == 0 {
		r.Verdict = VerdictNotApplicable
		r.Reason = "binary defines no indirect-callable functions (all hidden / static / never address-taken)"
		return r
	}
	if len(violations) == 0 {
		r.Verdict = VerdictPass
		r.Evidence = fmt.Sprintf("%d indirect-callable function(s); all start with ENDBR", candidates)
		return r
	}
	reported := violations
	if len(reported) > MaxReportedViolations {
		reported = reported[:MaxReportedViolations]
	}
	formatted := make([]string, 0, len(reported))
	for _, v := range reported {
		formatted = append(formatted, fmt.Sprintf("%s@0x%x", v.Func, v.Addr))
	}
	r.Detail["violations"] = formatted
	r.Verdict = VerdictFail
	r.Evidence = fmt.Sprintf("%d indirect-callable function(s) miss ENDBR prologue: %s%s",
		len(violations), formatted[0], moreSuffix(len(violations), len(reported)))
	return r
}

// isIndirectCallable approximates "this function may be called via an
// unknown function pointer". We accept either:
//   - external linkage (GLOBAL/WEAK) and non-hidden visibility, OR
//   - address-taken (its address appears in some relocation / vtable).
//
// IFUNC functions are always indirect-callable (PLT/GOT lookup goes
// through them).
func isIndirectCallable(ef ExtendedFunctionSymbol, addressTaken map[uint64]struct{}) bool {
	if ef.IsIFUNC {
		return true
	}
	if _, ok := addressTaken[ef.Addr]; ok {
		return true
	}
	if ef.Visibility == elf.STV_HIDDEN || ef.Visibility == elf.STV_INTERNAL {
		return false
	}
	return ef.Bind == elf.STB_GLOBAL || ef.Bind == elf.STB_WEAK
}

// collectAddressTakenAddrs walks the relocations and any read-only
// section bytes to identify functions whose address has been emitted as
// data (vtables, function-pointer tables, PIC `lea`s).
func collectAddressTakenAddrs(ctx *CheckContext, extFuncs []ExtendedFunctionSymbol) map[uint64]struct{} {
	out := make(map[uint64]struct{})
	if ctx == nil || ctx.Inspector == nil {
		return out
	}
	addrSet := make(map[uint64]struct{}, len(extFuncs))
	for _, ef := range extFuncs {
		addrSet[ef.Addr] = struct{}{}
	}

	relocs, _ := ctx.Inspector.Relocations()
	for _, rel := range relocs {
		if rel.Symbol == "" {
			continue
		}
		if rel.SymbolType != elf.STT_FUNC && rel.SymbolType != elf.STT_GNU_IFUNC {
			continue
		}
		// Any non-PC-relative relocation is "the address ended up in
		// memory". On x86_64 PLT32 (type 4) and PC32 (type 2) are
		// PC-relative direct calls; we ignore those.
		if isPCRelocCallType(rel.Type) {
			continue
		}
		out[rel.SymbolValue] = struct{}{}
	}

	// Scan ReadOnlySections for 8-byte aligned little-endian function
	// addresses (covers C++ vtables and hand-rolled tables). 8-byte
	// only because IBT is x86_64-relevant; on i386 we'd want 4-byte
	// scanning, but i386 IBT is essentially never deployed at runtime.
	machine, _ := ctx.Inspector.Machine()
	if machine != elf.EM_X86_64 {
		return out
	}
	rodatas, _ := ctx.Inspector.ReadOnlySections()
	for _, ds := range rodatas {
		// Skip sections that are clearly non-pointer (heuristic by name).
		if !looksPointerful(ds.Name) {
			continue
		}
		data := ds.Data
		for off := 0; off+8 <= len(data); off += 8 {
			v := uint64(data[off]) |
				uint64(data[off+1])<<8 |
				uint64(data[off+2])<<16 |
				uint64(data[off+3])<<24 |
				uint64(data[off+4])<<32 |
				uint64(data[off+5])<<40 |
				uint64(data[off+6])<<48 |
				uint64(data[off+7])<<56
			if _, ok := addrSet[v]; ok {
				out[v] = struct{}{}
			}
		}
	}
	return out
}

func looksPointerful(name string) bool {
	switch {
	case name == ".rodata", name == ".data.rel.ro", name == ".data.rel.ro.local":
		return true
	case strings.HasPrefix(name, ".data.rel.ro"):
		return true
	case strings.HasPrefix(name, ".init_array"), strings.HasPrefix(name, ".fini_array"),
		strings.HasPrefix(name, ".ctors"), strings.HasPrefix(name, ".dtors"):
		return true
	case name == ".got", name == ".got.plt":
		return true
	}
	return false
}

func isPCRelocCallType(t uint32) bool {
	// x86_64: R_X86_64_PC32 = 2, R_X86_64_PLT32 = 4, R_X86_64_GOTPCREL = 9.
	// i386:   R_386_PC32 = 2, R_386_PLT32 = 4.
	switch t {
	case 2, 4, 9:
		return true
	}
	return false
}

// ============================================================================
// INV-IBT-P02 — `setjmp` call site's return PC must be ENDBR.
// ============================================================================

// SetjmpReturnEndbrChecker walks every direct CALL whose target is a
// setjmp-family symbol (`setjmp`, `_setjmp`, `__sigsetjmp`,
// `__builtin_setjmp`) and asserts that the instruction immediately
// following the CALL begins with ENDBR.
type SetjmpReturnEndbrChecker struct{}

func (c *SetjmpReturnEndbrChecker) ID() string                { return "INV-IBT-P02" }
func (c *SetjmpReturnEndbrChecker) Category() InvariantCategory { return CategoryStatic }

var setjmpFamily = map[string]struct{}{
	"setjmp":           {},
	"_setjmp":          {},
	"__sigsetjmp":      {},
	"sigsetjmp":        {},
	"__builtin_setjmp": {},
}

func (c *SetjmpReturnEndbrChecker) Check(ctx *CheckContext) InvariantResult {
	r := initIBTResult(c.ID(), CategoryStatic)
	pattern, ok := fetchPattern(ctx, &r)
	if !ok {
		return r
	}
	execs, err := ctx.Inspector.ExecutableSections()
	if err != nil {
		r.Verdict = naOrError(err)
		r.Reason = fmt.Sprintf("inspector.ExecutableSections failed: %v", err)
		return r
	}
	funcs, err := ctx.Inspector.FunctionSymbols()
	if err != nil {
		r.Verdict = naOrError(err)
		r.Reason = fmt.Sprintf("inspector.FunctionSymbols failed: %v", err)
		return r
	}
	// Map setjmp family addresses by name.
	setjmpAddrs := make(map[uint64]string)
	for _, fn := range funcs {
		if _, ok := setjmpFamily[fn.Name]; ok {
			setjmpAddrs[fn.Addr] = fn.Name
		}
	}
	machine, _ := ctx.Inspector.Machine()
	branches := CachedIndirectBranches(ctx, machine, execs)
	candidates := 0
	type viol struct {
		Site uint64
		Name string
	}
	var violations []viol
	for _, br := range branches {
		if !br.IsCall || !br.TargetKnown {
			continue
		}
		name, hit := setjmpAddrs[br.Target]
		if !hit {
			continue
		}
		candidates++
		afterAddr := br.Addr + uint64(br.Length)
		if !hasEndbrAt(execs, afterAddr, pattern) {
			violations = append(violations, viol{Site: br.Addr, Name: name})
		}
	}
	r.Detail["candidates"] = candidates
	r.Detail["violations_total"] = len(violations)
	if candidates == 0 {
		r.Verdict = VerdictNotApplicable
		r.Reason = "no direct call to setjmp / _setjmp / sigsetjmp; INV-IBT-P02 trivially satisfied"
		return r
	}
	if len(violations) == 0 {
		r.Verdict = VerdictPass
		r.Evidence = fmt.Sprintf("%d setjmp call site(s); all return PCs begin with ENDBR", candidates)
		return r
	}
	reported := violations
	if len(reported) > MaxReportedViolations {
		reported = reported[:MaxReportedViolations]
	}
	formatted := make([]string, 0, len(reported))
	for _, v := range reported {
		formatted = append(formatted, fmt.Sprintf("call %s @0x%x", v.Name, v.Site))
	}
	r.Detail["violations"] = formatted
	r.Verdict = VerdictFail
	r.Evidence = fmt.Sprintf("%d setjmp call site(s) lack ENDBR after CALL: %s%s",
		len(violations), formatted[0], moreSuffix(len(violations), len(reported)))
	return r
}

// ============================================================================
// INV-IBT-P03 — EH landing pads must begin with ENDBR.
// ============================================================================

// EHLandingPadEndbrChecker walks every PC value reported by
// EHLandingPads() and asserts each begins with ENDBR.
type EHLandingPadEndbrChecker struct{}

func (c *EHLandingPadEndbrChecker) ID() string                { return "INV-IBT-P03" }
func (c *EHLandingPadEndbrChecker) Category() InvariantCategory { return CategoryStatic }

func (c *EHLandingPadEndbrChecker) Check(ctx *CheckContext) InvariantResult {
	r := initIBTResult(c.ID(), CategoryStatic)
	pattern, ok := fetchPattern(ctx, &r)
	if !ok {
		return r
	}
	pads, err := ctx.Inspector.EHLandingPads()
	if err != nil {
		r.Verdict = naOrError(err)
		r.Reason = fmt.Sprintf("inspector.EHLandingPads failed: %v", err)
		return r
	}
	r.Detail["candidates"] = len(pads)
	if len(pads) == 0 {
		r.Verdict = VerdictNotApplicable
		r.Reason = "no .eh_frame / FDE landing pads decoded; INV-IBT-P03 trivially holds"
		return r
	}
	execs, err := ctx.Inspector.ExecutableSections()
	if err != nil || len(execs) == 0 {
		r.Verdict = VerdictNotApplicable
		r.Reason = "no executable sections to inspect landing pad bytes"
		return r
	}
	var violations []uint64
	for _, pc := range pads {
		if !hasEndbrAt(execs, pc, pattern) {
			violations = append(violations, pc)
		}
	}
	r.Detail["violations_total"] = len(violations)
	if len(violations) == 0 {
		r.Verdict = VerdictPass
		r.Evidence = fmt.Sprintf("%d EH landing pad(s); all begin with ENDBR", len(pads))
		return r
	}
	reported := violations
	if len(reported) > MaxReportedViolations {
		reported = reported[:MaxReportedViolations]
	}
	formatted := make([]string, 0, len(reported))
	for _, pc := range reported {
		formatted = append(formatted, fmt.Sprintf("0x%x", pc))
	}
	r.Detail["violations"] = formatted
	r.Verdict = VerdictFail
	r.Evidence = fmt.Sprintf("%d EH landing pad(s) miss ENDBR: %s%s",
		len(violations), formatted[0], moreSuffix(len(violations), len(reported)))
	return r
}

// ============================================================================
// INV-IBT-P04 — IFUNC resolvers must begin with ENDBR.
// ============================================================================

// IFUNCResolverEndbrChecker collects STT_GNU_IFUNC symbols and IRELATIVE
// resolver addresses, and asserts each entry begins with ENDBR.
type IFUNCResolverEndbrChecker struct{}

func (c *IFUNCResolverEndbrChecker) ID() string                { return "INV-IBT-P04" }
func (c *IFUNCResolverEndbrChecker) Category() InvariantCategory { return CategoryStatic }

func (c *IFUNCResolverEndbrChecker) Check(ctx *CheckContext) InvariantResult {
	r := initIBTResult(c.ID(), CategoryStatic)
	pattern, ok := fetchPattern(ctx, &r)
	if !ok {
		return r
	}
	resolvers, err := ctx.Inspector.IFUNCResolvers()
	if err != nil {
		r.Verdict = naOrError(err)
		r.Reason = fmt.Sprintf("inspector.IFUNCResolvers failed: %v", err)
		return r
	}
	r.Detail["candidates"] = len(resolvers)
	if len(resolvers) == 0 {
		r.Verdict = VerdictNotApplicable
		r.Reason = "binary has no STT_GNU_IFUNC symbols nor IRELATIVE relocations"
		return r
	}
	execs, err := ctx.Inspector.ExecutableSections()
	if err != nil || len(execs) == 0 {
		r.Verdict = VerdictNotApplicable
		r.Reason = "no executable sections; cannot inspect resolver entry bytes"
		return r
	}
	type viol struct {
		Name string
		Addr uint64
	}
	var violations []viol
	for _, fn := range resolvers {
		if !hasEndbrAt(execs, fn.Addr, pattern) {
			violations = append(violations, viol{Name: fn.Name, Addr: fn.Addr})
		}
	}
	r.Detail["violations_total"] = len(violations)
	if len(violations) == 0 {
		r.Verdict = VerdictPass
		r.Evidence = fmt.Sprintf("%d IFUNC resolver(s); all begin with ENDBR", len(resolvers))
		return r
	}
	reported := violations
	if len(reported) > MaxReportedViolations {
		reported = reported[:MaxReportedViolations]
	}
	formatted := make([]string, 0, len(reported))
	for _, v := range reported {
		formatted = append(formatted, fmt.Sprintf("%s@0x%x", v.Name, v.Addr))
	}
	r.Detail["violations"] = formatted
	r.Verdict = VerdictFail
	r.Evidence = fmt.Sprintf("%d IFUNC resolver(s) miss ENDBR: %s%s",
		len(violations), formatted[0], moreSuffix(len(violations), len(reported)))
	return r
}

// ============================================================================
// INV-IBT-P05 — GCC nested-function trampoline template must include ENDBR.
// ============================================================================

// NestedFuncTrampolineEndbrChecker scans `.text` (and any other exec
// section) for libgcc's nested-function trampoline template prologue
// and verifies it starts with ENDBR.
//
// The libgcc template under `-fcf-protection=branch` on x86_64 consists
// of (per `libgcc/config/i386/heap-trampoline.c`):
//
//	endbr64                 F3 0F 1E FA
//	mov $static_chain, %r11 49 BB <8-byte imm>
//	mov $func_addr,   %rax  48 B8 <8-byte imm>
//	jmp *%rax               FF E0
//
// The first 4 + 2 + 8 + 2 + 8 + 2 = 26 bytes form a recognisable
// template. We pattern-match the *non-immediate* opcode bytes:
//
//	[0..4)   = endbr64           (F3 0F 1E FA)
//	[4..6)   = MOV r11 imm64     (49 BB)
//	[14..16) = MOV rax imm64     (48 B8)
//	[24..26) = JMP *rax          (FF E0)
//
// A negative case (the bug we're guarding against) is the same 22-byte
// "skeleton" with the first 4 bytes replaced by anything other than the
// ENDBR pattern.
type NestedFuncTrampolineEndbrChecker struct{}

func (c *NestedFuncTrampolineEndbrChecker) ID() string { return "INV-IBT-P05" }
func (c *NestedFuncTrampolineEndbrChecker) Category() InvariantCategory {
	return CategoryStatic
}

// Trampoline skeleton: 4 endbr + 2 r11-mov + 8 imm + 2 rax-mov + 8 imm + 2 jmp.
const trampolineSize = 26

func (c *NestedFuncTrampolineEndbrChecker) Check(ctx *CheckContext) InvariantResult {
	r := initIBTResult(c.ID(), CategoryStatic)
	pattern, ok := fetchPattern(ctx, &r)
	if !ok {
		return r
	}
	if len(pattern) != 4 {
		r.Verdict = VerdictNotApplicable
		r.Reason = "non-x86_64 ENDBR pattern; libgcc trampoline template is x86_64-only"
		return r
	}
	machine, _ := ctx.Inspector.Machine()
	if machine != elf.EM_X86_64 {
		r.Verdict = VerdictNotApplicable
		r.Reason = "INV-IBT-P05 currently only checks the x86_64 trampoline template"
		return r
	}
	execs, err := ctx.Inspector.ExecutableSections()
	if err != nil || len(execs) == 0 {
		r.Verdict = VerdictNotApplicable
		r.Reason = "no executable sections"
		return r
	}
	hits := 0
	violations := 0
	type viol struct {
		Section string
		Addr    uint64
	}
	var violRecords []viol
	for _, sec := range execs {
		data := sec.Data
		for i := 0; i+trampolineSize <= len(data); i++ {
			if !looksLikeTrampoline(data[i : i+trampolineSize]) {
				continue
			}
			hits++
			if !startsWithPattern(data[i:i+trampolineSize], pattern) {
				violations++
				if len(violRecords) < MaxReportedViolations {
					violRecords = append(violRecords, viol{
						Section: sec.Name,
						Addr:    sec.Addr + uint64(i),
					})
				}
			}
		}
	}
	r.Detail["candidates"] = hits
	r.Detail["violations_total"] = violations
	if hits == 0 {
		r.Verdict = VerdictNotApplicable
		r.Reason = "no nested-function trampoline template found in executable sections"
		return r
	}
	if violations == 0 {
		r.Verdict = VerdictPass
		r.Evidence = fmt.Sprintf("%d trampoline template instance(s); all begin with ENDBR", hits)
		return r
	}
	formatted := make([]string, 0, len(violRecords))
	for _, v := range violRecords {
		formatted = append(formatted, fmt.Sprintf("%s@0x%x", v.Section, v.Addr))
	}
	r.Detail["violations"] = formatted
	r.Verdict = VerdictFail
	r.Evidence = fmt.Sprintf("%d trampoline template(s) lack ENDBR prologue: %s%s",
		violations, formatted[0], moreSuffix(violations, len(formatted)))
	return r
}

func looksLikeTrampoline(b []byte) bool {
	if len(b) < trampolineSize {
		return false
	}
	// MOV r11, imm64 (49 BB)
	if b[4] != 0x49 || b[5] != 0xBB {
		return false
	}
	// MOV rax, imm64 (48 B8)
	if b[14] != 0x48 || b[15] != 0xB8 {
		return false
	}
	// JMP *rax (FF E0)
	if b[24] != 0xFF || b[25] != 0xE0 {
		return false
	}
	return true
}

func startsWithPattern(b, pat []byte) bool {
	if len(b) < len(pat) {
		return false
	}
	for i, x := range pat {
		if b[i] != x {
			return false
		}
	}
	return true
}

// ============================================================================
// INV-IBT-P06 — Indirect-branch targets (when statically resolvable) must
// land on ENDBR.
// ============================================================================

// IndirectBranchTargetEndbrChecker walks every indirect call/jmp and,
// when the branch carries a NOTRACK prefix or a direct-relative form
// (we already filter to indirects in classifyBranch, so only the direct
// fallthrough branches with TargetKnown == true qualify), asserts the
// resolved target lands on ENDBR.
//
// Pure register-indirect / memory-indirect branches have no statically
// known target; this checker reports the count as informational only.
type IndirectBranchTargetEndbrChecker struct{}

func (c *IndirectBranchTargetEndbrChecker) ID() string { return "INV-IBT-P06" }
func (c *IndirectBranchTargetEndbrChecker) Category() InvariantCategory {
	return CategoryStatic
}

func (c *IndirectBranchTargetEndbrChecker) Check(ctx *CheckContext) InvariantResult {
	r := initIBTResult(c.ID(), CategoryStatic)
	pattern, ok := fetchPattern(ctx, &r)
	if !ok {
		return r
	}
	execs, err := ctx.Inspector.ExecutableSections()
	if err != nil || len(execs) == 0 {
		r.Verdict = VerdictNotApplicable
		r.Reason = "no executable sections"
		return r
	}
	machine, _ := ctx.Inspector.Machine()
	branches := CachedIndirectBranches(ctx, machine, execs)
	resolvable := 0
	indirect := 0
	type viol struct {
		Site   uint64
		Target uint64
	}
	var violations []viol
	for _, br := range branches {
		if !br.TargetKnown {
			indirect++
			continue
		}
		// Skip direct branches that target the start of a function
		// symbol; those are normal direct calls and don't need ENDBR
		// (caller doesn't enter WAIT_FOR_ENDBRANCH on direct CALL).
		if !br.HasNotrack {
			continue
		}
		resolvable++
		if !hasEndbrAt(execs, br.Target, pattern) {
			violations = append(violations, viol{Site: br.Addr, Target: br.Target})
		}
	}
	r.Detail["candidates_resolvable"] = resolvable
	r.Detail["branches_indirect_unknown"] = indirect
	r.Detail["violations_total"] = len(violations)
	if resolvable == 0 {
		r.Verdict = VerdictNotApplicable
		r.Reason = fmt.Sprintf("no statically-resolvable NOTRACK indirect branches (%d register/memory indirect)", indirect)
		return r
	}
	if len(violations) == 0 {
		r.Verdict = VerdictPass
		r.Evidence = fmt.Sprintf("%d resolvable indirect branch target(s); all land on ENDBR", resolvable)
		return r
	}
	reported := violations
	if len(reported) > MaxReportedViolations {
		reported = reported[:MaxReportedViolations]
	}
	formatted := make([]string, 0, len(reported))
	for _, v := range reported {
		formatted = append(formatted, fmt.Sprintf("0x%x->0x%x", v.Site, v.Target))
	}
	r.Detail["violations"] = formatted
	r.Verdict = VerdictFail
	r.Evidence = fmt.Sprintf("%d indirect branch target(s) miss ENDBR: %s%s",
		len(violations), formatted[0], moreSuffix(len(violations), len(reported)))
	return r
}

// ============================================================================
// INV-IBT-N01 — NOTRACK prefix must only sit before known-safe indirect
// branches (compiler-emitted switch jump tables).
// ============================================================================

// NotrackPrefixGuardChecker enumerates every NOTRACK-prefixed indirect
// call/jmp and reports a violation when the branch's target is NOT
// inside a recognised jump-table region.
//
// Heuristic: a NOTRACK indirect jmp/call whose target operand is an
// `[rip + disp]` pointing into a `.rodata*` section is *almost
// certainly* a switch jump table dispatch. We treat that as legitimate.
// Any NOTRACK indirect call/jmp whose target is a register (call *%rax)
// or whose memory operand does NOT resolve into rodata is flagged.
//
// The current trigger set (GCC PR 104816) is the *baseline* behaviour;
// detection here therefore primarily warns when the operand looks like
// a function-pointer call rather than a switch dispatch.
type NotrackPrefixGuardChecker struct{}

func (c *NotrackPrefixGuardChecker) ID() string                { return "INV-IBT-N01" }
func (c *NotrackPrefixGuardChecker) Category() InvariantCategory { return CategoryStatic }

func (c *NotrackPrefixGuardChecker) Check(ctx *CheckContext) InvariantResult {
	r := initIBTResult(c.ID(), CategoryStatic)
	if _, ok := fetchPattern(ctx, &r); !ok {
		return r
	}
	execs, err := ctx.Inspector.ExecutableSections()
	if err != nil || len(execs) == 0 {
		r.Verdict = VerdictNotApplicable
		r.Reason = "no executable sections"
		return r
	}
	machine, _ := ctx.Inspector.Machine()
	branches := CachedIndirectBranches(ctx, machine, execs)
	rodatas, _ := ctx.Inspector.ReadOnlySections()

	notrackTotal := 0
	type viol struct {
		Site uint64
		Op   string
	}
	var violations []viol
	for _, br := range branches {
		if !br.HasNotrack {
			continue
		}
		notrackTotal++
		// Register-indirect (call *%rax) is never safe under NOTRACK
		// for arbitrary callees: the compiler can't have proven type
		// safety post-IPA. Flag.
		if br.Inst.Args[0] == nil {
			violations = append(violations, viol{Site: br.PrefixAddr, Op: "<unknown>"})
			continue
		}
		switch br.Inst.Args[0].(type) {
		case nil:
			violations = append(violations, viol{Site: br.PrefixAddr, Op: "<unknown>"})
		default:
			if !targetLandsInRodata(br, rodatas) {
				violations = append(violations, viol{Site: br.PrefixAddr, Op: br.Inst.Args[0].String()})
			}
		}
	}
	r.Detail["notrack_branches_total"] = notrackTotal
	r.Detail["violations_total"] = len(violations)
	if notrackTotal == 0 {
		r.Verdict = VerdictNotApplicable
		r.Reason = "no NOTRACK indirect branches found; INV-IBT-N01 trivially holds"
		return r
	}
	if len(violations) == 0 {
		r.Verdict = VerdictPass
		r.Evidence = fmt.Sprintf("%d NOTRACK branch(es); all dispatch via .rodata jump tables", notrackTotal)
		return r
	}
	reported := violations
	if len(reported) > MaxReportedViolations {
		reported = reported[:MaxReportedViolations]
	}
	formatted := make([]string, 0, len(reported))
	for _, v := range reported {
		formatted = append(formatted, fmt.Sprintf("0x%x %s", v.Site, v.Op))
	}
	r.Detail["violations"] = formatted
	r.Verdict = VerdictFail
	r.Evidence = fmt.Sprintf("%d NOTRACK branch(es) target non-jump-table operand: %s%s",
		len(violations), formatted[0], moreSuffix(len(violations), len(reported)))
	return r
}

// targetLandsInRodata is a coarse rodata-jump-table heuristic: only
// memory operands with a non-zero displacement falling inside a
// recognised `.rodata*` section pass.
func targetLandsInRodata(br IndirectBranch, rodatas []DataSection) bool {
	if len(br.Inst.Args) == 0 {
		return false
	}
	mem, ok := br.Inst.Args[0].(x86asm.Mem)
	if !ok {
		return false
	}
	disp := uint64(mem.Disp)
	for _, ds := range rodatas {
		if disp >= ds.Addr && disp < ds.Addr+uint64(len(ds.Data)) {
			return true
		}
	}
	return false
}

// ============================================================================
// INV-IBT-N02 — FineIBT signature hash must not collide.
// ============================================================================

// FineIBTHashCollisionChecker locates `__cfi_*` thunks and extracts the
// 32-bit hash that follows the ENDBR. Collisions in the hash space
// across distinct function targets indicate the FineIBT trap can be
// bypassed via an ABI-equivalent function.
//
// On a binary where FineIBT is not deployed (no `__cfi_*` symbols), the
// checker collapses to NotApplicable.
type FineIBTHashCollisionChecker struct{}

func (c *FineIBTHashCollisionChecker) ID() string                { return "INV-IBT-N02" }
func (c *FineIBTHashCollisionChecker) Category() InvariantCategory { return CategoryStatic }

func (c *FineIBTHashCollisionChecker) Check(ctx *CheckContext) InvariantResult {
	r := initIBTResult(c.ID(), CategoryStatic)
	if _, ok := fetchPattern(ctx, &r); !ok {
		return r
	}
	funcs, err := ctx.Inspector.FunctionSymbols()
	if err != nil {
		r.Verdict = naOrError(err)
		r.Reason = fmt.Sprintf("inspector.FunctionSymbols failed: %v", err)
		return r
	}
	execs, err := ctx.Inspector.ExecutableSections()
	if err != nil || len(execs) == 0 {
		r.Verdict = VerdictNotApplicable
		r.Reason = "no executable sections"
		return r
	}
	cfiThunks := 0
	hashes := make(map[uint32][]string)
	for _, fn := range funcs {
		if !strings.HasPrefix(fn.Name, "__cfi_") {
			continue
		}
		cfiThunks++
		// The thunk layout (kernel CONFIG_FINEIBT) is:
		//   endbr64           4 bytes
		//   sub  $hash, %r10  7 bytes (41 81 EA <imm32>) — Linux pattern
		// glibc / userland may differ; we recognise both:
		//   F3 0F 1E FA  41 81 EA  hh hh hh hh        (kernel)
		//   F3 0F 1E FA  B8        hh hh hh hh ...    (cmp eax,imm32)
		hash, ok := extractFineIBTHash(execs, fn.Addr)
		if !ok {
			continue
		}
		hashes[hash] = append(hashes[hash], fn.Name)
	}
	r.Detail["cfi_thunks_total"] = cfiThunks
	if cfiThunks == 0 {
		r.Verdict = VerdictNotApplicable
		r.Reason = "no __cfi_* symbols; FineIBT not deployed"
		return r
	}
	type collision struct {
		Hash    uint32
		Members []string
	}
	var collisions []collision
	for h, members := range hashes {
		if len(members) > 1 {
			sort.Strings(members)
			collisions = append(collisions, collision{Hash: h, Members: members})
		}
	}
	r.Detail["collisions_total"] = len(collisions)
	if len(collisions) == 0 {
		r.Verdict = VerdictPass
		r.Evidence = fmt.Sprintf("%d FineIBT thunk(s); no hash collisions", cfiThunks)
		return r
	}
	sort.Slice(collisions, func(i, j int) bool { return collisions[i].Hash < collisions[j].Hash })
	reported := collisions
	if len(reported) > MaxReportedViolations {
		reported = reported[:MaxReportedViolations]
	}
	formatted := make([]string, 0, len(reported))
	for _, col := range reported {
		formatted = append(formatted, fmt.Sprintf("0x%08x:[%s]", col.Hash, strings.Join(col.Members, ",")))
	}
	r.Detail["violations"] = formatted
	r.Verdict = VerdictFail
	r.Evidence = fmt.Sprintf("%d FineIBT hash collision(s): %s%s",
		len(collisions), formatted[0], moreSuffix(len(collisions), len(reported)))
	return r
}

// extractFineIBTHash reads the 4-byte hash immediate after the ENDBR
// prologue of a __cfi_* thunk. Recognises the two common layouts.
func extractFineIBTHash(execs []ExecSection, addr uint64) (uint32, bool) {
	// We need at least 11 bytes after addr.
	const need = 4 + 3 + 4
	bytes := make([]byte, 0, need)
	for off := uint64(0); off < uint64(need); off++ {
		b, ok := readByteAt(execs, addr+off)
		if !ok {
			return 0, false
		}
		bytes = append(bytes, b)
	}
	if !startsWithPattern(bytes, endbr64Pattern) {
		return 0, false
	}
	// kernel: 41 81 EA <imm32>
	if bytes[4] == 0x41 && bytes[5] == 0x81 && bytes[6] == 0xEA {
		return uint32(bytes[7]) | uint32(bytes[8])<<8 |
			uint32(bytes[9])<<16 | uint32(bytes[10])<<24, true
	}
	// alternate: B8 <imm32> ...
	if bytes[4] == 0xB8 {
		return uint32(bytes[5]) | uint32(bytes[6])<<8 |
			uint32(bytes[7])<<16 | uint32(bytes[8])<<24, true
	}
	return 0, false
}

// ============================================================================
// INV-IBT-M01 — runtime IBT enforcement must not be silently disabled by
// dlopen of an IBT-less DSO.
// ============================================================================

// IBTRuntimeEnforcementChecker is a CategoryDynamic checker. The static
// portion verifies the binary itself advertises the IBT feature bit in
// `.note.gnu.property`; the dynamic portion would need to invoke
// `arch_prctl(ARCH_CET_STATUS)` post-dlopen. Because the latter requires
// a privileged Executor, this checker degrades to NotApplicable when no
// Executor is available — the static "binary advertises IBT" result is
// reported in Detail for future runtime correlation.
type IBTRuntimeEnforcementChecker struct{}

func (c *IBTRuntimeEnforcementChecker) ID() string { return "INV-IBT-M01" }
func (c *IBTRuntimeEnforcementChecker) Category() InvariantCategory {
	return CategoryDynamic
}

func (c *IBTRuntimeEnforcementChecker) Check(ctx *CheckContext) InvariantResult {
	r := initIBTResult(c.ID(), CategoryDynamic)
	if ctx == nil || ctx.Inspector == nil {
		r.Verdict = VerdictNotApplicable
		r.Reason = "no inspector available"
		return r
	}
	gnu, err := ctx.Inspector.GNUProperty()
	if err != nil {
		r.Verdict = naOrError(err)
		r.Reason = fmt.Sprintf("inspector.GNUProperty failed: %v", err)
		return r
	}
	hasIBT := gnu&GNUPropertyX86Feature1IBT != 0
	r.Detail["binary_ibt_property"] = hasIBT
	r.Detail["gnu_property_bits"] = fmt.Sprintf("0x%x", gnu)
	if !hasIBT {
		r.Verdict = VerdictNotApplicable
		r.Reason = "binary does not advertise GNU_PROPERTY_X86_FEATURE_1_IBT; runtime enforcement n/a"
		return r
	}
	if ctx.Executor == nil {
		r.Verdict = VerdictNotApplicable
		r.Reason = "binary advertises IBT property; runtime arch_prctl(ARCH_CET_STATUS) check requires an Executor (none provided)"
		return r
	}
	// We could actually run the binary here under a probe shim, but
	// that requires a host kernel with CET support and the binary
	// itself to invoke arch_prctl. For now the dynamic path is left as
	// NA with a directive Reason; a follow-up integration test can
	// fill it in.
	r.Verdict = VerdictNotApplicable
	r.Reason = "dynamic dlopen probe not yet implemented; static IBT property bit is set"
	return r
}
