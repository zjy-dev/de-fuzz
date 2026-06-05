package fuzz

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zjy-dev/de-fuzz/internal/coverage"
)

// writeFocusCFG writes a two-function CFG dump with line annotations so the
// analyzer can track BB coverage and basis-point increments.
func newFocusScheduler(t *testing.T, minDeltaBP uint64, windowSize int) (*FunctionFocusScheduler, *coverage.Analyzer) {
	t.Helper()

	cfg := `;; Function funcA (funcA, funcdef_no=0, decl_uid=2)
;;   with 3 basic blocks.

funcA (int x)
{
;; 2 succs { 3 4 }
<bb 2>:
x_1 = 1; [a.c:10:5]

;; 1 succs { 4 }
<bb 3>:
x_2 = 2; [a.c:11:5]

;; 1 succs { }
<bb 4>:
return; [a.c:12:5]

}

;; Function funcB (funcB, funcdef_no=1, decl_uid=3)
;;   with 3 basic blocks.

funcB (int y)
{
;; 2 succs { 3 4 }
<bb 2>:
y_1 = 2; [b.c:20:5]

;; 1 succs { 4 }
<bb 3>:
y_2 = 3; [b.c:21:5]

;; 1 succs { }
<bb 4>:
return; [b.c:22:5]

}
`
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "test.cfg")
	require.NoError(t, os.WriteFile(cfgPath, []byte(cfg), 0644))

	mappingPath := filepath.Join(dir, "mapping.json")
	a, err := coverage.NewAnalyzer([]string{cfgPath}, []string{"funcA", "funcB"}, "", mappingPath, 0.8)
	require.NoError(t, err)

	s := NewFunctionFocusScheduler(a, windowSize, minDeltaBP, 0.5)
	return s, a
}

func TestFocusScheduler_KeepsFocusWhenProgressing(t *testing.T) {
	s, a := newFocusScheduler(t, 1 /*minDeltaBP*/, 2 /*windowSize*/)

	t1 := s.NextTarget()
	require.NotNil(t, t1)
	focus := s.focusFunc
	require.NotEmpty(t, focus)

	// Simulate coverage progress on the focus function before the window closes.
	// Cover only BB2's line so the function still has selectable BBs (BB3/BB4).
	switch focus {
	case "funcA":
		a.RecordCoverage(1, []string{"a.c:10"})
	case "funcB":
		a.RecordCoverage(1, []string{"b.c:20"})
	}

	// Second iteration closes the window (windowSize=2).
	_ = s.NextTarget()
	s.OnIterationEnd()

	// Progress >= threshold => focus unchanged, not bottlenecked.
	require.Equal(t, focus, s.focusFunc)
	require.False(t, s.bottlenecked[focus])
}

func TestFocusScheduler_BottleneckRotatesFunction(t *testing.T) {
	s, _ := newFocusScheduler(t, 5 /*high threshold*/, 1 /*windowSize*/)

	t1 := s.NextTarget()
	require.NotNil(t, t1)
	first := s.focusFunc

	// No coverage recorded => delta=0 < threshold => bottleneck after window.
	s.OnIterationEnd()
	require.True(t, s.bottlenecked[first])
	require.Empty(t, s.focusFunc)

	// Next selection picks the other (non-bottlenecked) function.
	t2 := s.NextTarget()
	require.NotNil(t, t2)
	require.NotEqual(t, first, s.focusFunc)
}

func TestFocusScheduler_RelaxesWhenAllBottlenecked(t *testing.T) {
	s, _ := newFocusScheduler(t, 4 /*threshold*/, 1 /*windowSize*/)

	// Bottleneck both functions with zero progress.
	require.NotNil(t, s.NextTarget())
	s.OnIterationEnd()
	require.NotNil(t, s.NextTarget())
	s.OnIterationEnd()

	// Both functions are now bottlenecked.
	require.Len(t, s.bottlenecked, 2)

	// Next selection must relax: threshold halved (4 -> 2) and set cleared.
	require.NotNil(t, s.NextTarget())
	require.Equal(t, uint64(2), s.minDeltaBP)
	require.NotEmpty(t, s.focusFunc)
}

func TestFocusScheduler_ExhaustedFunctionRotatesEarly(t *testing.T) {
	s, a := newFocusScheduler(t, 1, 10 /*large window*/)

	// Fully cover funcA so it has no selectable BB.
	a.RecordCoverage(1, []string{"a.c:10", "a.c:11", "a.c:12"})

	// funcA has lowest selectable? It is fully covered (100%), funcB is 0%,
	// so funcB is chosen first; force funcA focus to exercise early rotation.
	s.focusFunc = "funcA"
	target := s.NextTarget()
	require.NotNil(t, target)
	// funcA exhausted => scheduler should have rotated to funcB.
	require.Equal(t, "funcB", target.Function)
	require.True(t, s.bottlenecked["funcA"])
}
