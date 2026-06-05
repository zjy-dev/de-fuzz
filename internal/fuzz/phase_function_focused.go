// Package fuzz provides the fuzzing engine for constraint solving based fuzzing.
package fuzz

import (
	"github.com/zjy-dev/de-fuzz/internal/coverage"
	"github.com/zjy-dev/de-fuzz/internal/logger"
)

// FunctionFocusScheduler implements function-level focused fuzzing on top of the
// CFG-guided BB-level constraint solving loop.
//
// It picks one focus function (lowest BB coverage among target functions) and
// restricts target-BB selection to that function. Every windowSize iterations it
// measures the GLOBAL BB-coverage increment; if the increment is below minDeltaBP
// the focus function is declared a bottleneck and the next lowest-coverage function
// is chosen. When all functions are bottlenecked, the threshold is relaxed (multiplied
// by relaxFactor) and the rotation restarts.
type FunctionFocusScheduler struct {
	analyzer    *coverage.Analyzer
	windowSize  int     // iterations per measurement window
	minDeltaBP  uint64  // minimum acceptable global coverage increment per window (basis points)
	relaxFactor float64 // threshold relax multiplier applied when all functions bottleneck

	focusFunc     string
	bottlenecked  map[string]bool
	windowStartBP uint64
	iterInWindow  int
}

// NewFunctionFocusScheduler creates a scheduler. windowSize<=0 defaults to 10;
// relaxFactor outside (0,1] defaults to 0.5.
func NewFunctionFocusScheduler(a *coverage.Analyzer, windowSize int, minDeltaBP uint64, relaxFactor float64) *FunctionFocusScheduler {
	if windowSize <= 0 {
		windowSize = 10
	}
	if relaxFactor <= 0 || relaxFactor > 1 {
		relaxFactor = 0.5
	}
	return &FunctionFocusScheduler{
		analyzer:     a,
		windowSize:   windowSize,
		minDeltaBP:   minDeltaBP,
		relaxFactor:  relaxFactor,
		bottlenecked: make(map[string]bool),
	}
}

// NextTarget returns the next target BB restricted to the current focus function.
// It returns nil when no focus function with selectable uncovered BBs remains
// (the caller should then fall back to global selection).
func (s *FunctionFocusScheduler) NextTarget() *coverage.TargetInfo {
	for {
		if s.focusFunc == "" {
			if !s.pickNextFunction() {
				return nil
			}
		}

		t := s.analyzer.SelectTargetInFunction(s.focusFunc)
		if t == nil {
			// Focus function exhausted all selectable uncovered BBs: bottleneck it.
			logger.Info("[FunctionFocus] Function %s has no selectable BB, rotating", s.focusFunc)
			s.bottlenecked[s.focusFunc] = true
			s.focusFunc = ""
			continue
		}

		s.iterInWindow++
		return t
	}
}

// OnIterationEnd settles the measurement window once windowSize iterations elapse.
func (s *FunctionFocusScheduler) OnIterationEnd() {
	if s.focusFunc == "" || s.iterInWindow < s.windowSize {
		return
	}

	nowBP := s.analyzer.GetBBCoverageBasisPoints()
	delta := uint64(0)
	if nowBP > s.windowStartBP {
		delta = nowBP - s.windowStartBP
	}

	if delta < s.minDeltaBP {
		logger.Info("[FunctionFocus] Bottleneck on %s: delta=%d bp < threshold=%d bp, rotating",
			s.focusFunc, delta, s.minDeltaBP)
		s.bottlenecked[s.focusFunc] = true
		s.focusFunc = ""
		return
	}

	// Still making progress: keep focus, reset the window.
	logger.Debug("[FunctionFocus] %s window progress delta=%d bp >= threshold=%d bp, continuing",
		s.focusFunc, delta, s.minDeltaBP)
	s.windowStartBP = nowBP
	s.iterInWindow = 0
}

// pickNextFunction selects the next focus function and resets the window.
// On exhaustion it relaxes the threshold once and retries; returns false only
// when no eligible function remains even after relaxation.
func (s *FunctionFocusScheduler) pickNextFunction() bool {
	fn, ok := s.analyzer.LowestCoverageFunction(s.bottlenecked)
	if !ok {
		s.relax()
		fn, ok = s.analyzer.LowestCoverageFunction(s.bottlenecked)
		if !ok {
			return false
		}
	}

	s.focusFunc = fn
	s.windowStartBP = s.analyzer.GetBBCoverageBasisPoints()
	s.iterInWindow = 0
	logger.Info("[FunctionFocus] Focusing on function %s (threshold=%d bp)", fn, s.minDeltaBP)
	return true
}

// relax lowers the bottleneck threshold and clears the bottleneck set so all
// functions become eligible again.
func (s *FunctionFocusScheduler) relax() {
	old := s.minDeltaBP
	s.minDeltaBP = uint64(float64(s.minDeltaBP) * s.relaxFactor)
	s.bottlenecked = make(map[string]bool)
	logger.Info("[FunctionFocus] All functions bottlenecked, relaxing threshold %d -> %d bp",
		old, s.minDeltaBP)
}
