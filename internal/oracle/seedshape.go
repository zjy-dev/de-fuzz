package oracle

import (
	"regexp"
	"strings"

	"github.com/zjy-dev/de-fuzz/internal/seed"
)

// SeedShape summarizes the stack-allocation flavors a seed's C source
// contains. Multiple flags may be true simultaneously (a seed can mix
// fixed-size buffers with a VLA and an alloca call).
//
// This is a lossy textual heuristic, not a full C parser. It is good
// enough for routing checkers (e.g., INV-SP-H01 only cares whether the
// seed has any VLA/alloca; INV-SP-L02 needs the same precondition; the
// future INV-SP-L03 needs "mixed" semantics). Callers that need
// stronger guarantees should defer to a real frontend.
type SeedShape struct {
	// HasFixedBuffer is true if the source contains a `char NAME[INT]`
	// (or unsigned char / int8_t variant). This is the bread-and-butter
	// canary trigger.
	HasFixedBuffer bool
	// HasVLA is true if the source contains a `TYPE NAME[EXPR]` whose
	// EXPR is not a plain integer literal. Detection is approximate:
	// any local array declaration whose size token contains a
	// non-digit, non-whitespace, non-underscore character is treated
	// as a VLA. Catches `char buf[n]`, `char buf[n+1]`, `int xs[get_n()]`.
	HasVLA bool
	// HasAlloca is true if the source mentions `alloca(` or
	// `__builtin_alloca(`. We do not try to filter out false hits
	// inside string literals or comments — those are vanishingly rare
	// in our seed corpus.
	HasAlloca bool
}

// IsMixed reports whether the seed contains more than one flavor of
// vulnerable object. INV-SP-L03 is specifically about this case
// (multiple vulnerable objects sharing the same canary protection plane).
func (s SeedShape) IsMixed() bool {
	n := 0
	if s.HasFixedBuffer {
		n++
	}
	if s.HasVLA {
		n++
	}
	if s.HasAlloca {
		n++
	}
	return n >= 2
}

// HasDynamicAlloc reports whether the seed has any dynamic stack
// allocation (VLA or alloca). INV-SP-H01 / INV-SP-L02 rely on this.
func (s SeedShape) HasDynamicAlloc() bool {
	return s.HasVLA || s.HasAlloca
}

// classifySeedShape inspects a Seed's C source and returns a SeedShape.
// A nil seed or an empty Content yields the zero value (everything false).
func classifySeedShape(s *seed.Seed) SeedShape {
	if s == nil || s.Content == "" {
		return SeedShape{}
	}
	return classifySeedShapeText(s.Content)
}

// fixedBufferRe matches `char NAME[INT_LITERAL]` and similar fixed-size
// byte arrays. We restrict to char-family element types because the
// canary heuristics in GCC/LLVM key on these.
var fixedBufferRe = regexp.MustCompile(
	`\b(?:unsigned\s+char|signed\s+char|char|uint8_t|int8_t|u_char)\s+\w+\s*\[\s*\d+\s*\]`,
)

// vlaRe matches a local array declaration whose size expression contains
// at least one non-digit token. The element type is left loose
// (`\w[\w\s\*]*`) so we catch `char`, `int`, `struct foo *`, etc.
//
// Excluded by construction: `[]` (no size — function parameters),
// pure integer literals (caught by fixedBufferRe), and anything where
// the brackets enclose only digits / whitespace.
var vlaRe = regexp.MustCompile(
	`\b\w[\w\s\*]*\s+\w+\s*\[\s*[A-Za-z_][^\]]*\]`,
)

// classifySeedShapeText is the text-only entry point, exposed separately
// so tests can drive it without constructing a Seed.
func classifySeedShapeText(src string) SeedShape {
	s := SeedShape{}
	if fixedBufferRe.MatchString(src) {
		s.HasFixedBuffer = true
	}
	if vlaRe.MatchString(src) {
		s.HasVLA = true
	}
	if strings.Contains(src, "alloca(") || strings.Contains(src, "__builtin_alloca(") {
		s.HasAlloca = true
	}
	return s
}
