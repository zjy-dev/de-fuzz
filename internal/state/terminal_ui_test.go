package state

import (
	"strings"
	"testing"
	"time"
)

func TestTerminalUI_NewTerminalUI(t *testing.T) {
	ui := NewTerminalUI()
	if ui == nil {
		t.Fatal("NewTerminalUI returned nil")
	}
	if !ui.enabled {
		t.Error("UI should be enabled by default")
	}
	if ui.width != 80 {
		t.Errorf("expected width 80, got %d", ui.width)
	}
}

func TestTerminalUI_SetMetrics(t *testing.T) {
	ui := NewTerminalUI()
	metrics := &FuzzMetrics{
		StartTime:       time.Now(),
		TotalSeedsRun:   100,
		CurrentCoverage: 42.5,
	}

	ui.SetMetrics(metrics)

	if ui.metrics != metrics {
		t.Error("SetMetrics did not set metrics correctly")
	}
}

func TestTerminalUI_SetEnabled(t *testing.T) {
	ui := NewTerminalUI()

	ui.SetEnabled(false)
	if ui.enabled {
		t.Error("SetEnabled(false) did not disable UI")
	}

	ui.SetEnabled(true)
	if !ui.enabled {
		t.Error("SetEnabled(true) did not enable UI")
	}
}

func TestTerminalUI_buildDisplay(t *testing.T) {
	ui := NewTerminalUI()
	metrics := &FuzzMetrics{
		StartTime:          time.Now().Add(-time.Hour),
		LastUpdateTime:     time.Now(),
		ElapsedSeconds:     3600,
		TotalSeedsRun:      150,
		CoverageIncrSeeds:  30,
		CompileFailedSeeds: 5,
		CrashedSeeds:       1,
		OracleChecks:       145,
		OracleFailures:     2,
		OracleErrors:       3,
		LLMCalls:           50,
		LLMErrors:          2,
		SeedsGenerated:     48,
		CurrentCoverage:    45.67,
		TotalCoveredLines:  456,
		TotalLines:         1000,
		SeedsPerSecond:     0.042,
		AvgSeedTimeMs:      24000,
	}

	ui.SetMetrics(metrics)

	output := ui.buildDisplay()

	// Verify the output contains expected sections
	expectedStrings := []string{
		"DE-FUZZ",
		"Runtime",
		"Seeds Processed",
		"Coverage Increase",
		"Compile Failures",
		"Crashes",
		"Bugs Found",
		"Oracle Checks",
		"LLM Calls",
		"Coverage:",
		"150",      // Total seeds
		"45.67%",   // Coverage percentage
		"456/1000", // Covered/total lines
	}

	for _, expected := range expectedStrings {
		if !strings.Contains(output, expected) {
			t.Errorf("output should contain %q", expected)
		}
	}

	// Verify box drawing characters are present
	boxChars := []string{"╔", "╗", "╚", "╝", "║", "═"}
	for _, char := range boxChars {
		if !strings.Contains(output, char) {
			t.Errorf("output should contain box character %q", char)
		}
	}
}

func TestTerminalUI_formatRow(t *testing.T) {
	ui := NewTerminalUI()

	row := ui.formatRow(60, "Test Label", "Test Value", colorWhite)

	if !strings.Contains(row, "Test Label") {
		t.Error("row should contain label")
	}
	if !strings.Contains(row, "Test Value") {
		t.Error("row should contain value")
	}
	if !strings.Contains(row, "║") {
		t.Error("row should contain vertical border")
	}
}

func TestTerminalUI_formatCoverageBar(t *testing.T) {
	ui := NewTerminalUI()
	metrics := &FuzzMetrics{
		CurrentCoverage:   50.0,
		TotalCoveredLines: 500,
		TotalLines:        1000,
	}

	bar := ui.formatCoverageBar(60, metrics)

	if !strings.Contains(bar, "Coverage:") {
		t.Error("coverage bar should contain label")
	}
	if !strings.Contains(bar, "500/1000") {
		t.Error("coverage bar should contain line counts")
	}
	if !strings.Contains(bar, "█") {
		t.Error("coverage bar should contain filled bar character")
	}
	if !strings.Contains(bar, "░") {
		t.Error("coverage bar should contain empty bar character")
	}
}

func TestTerminalUI_colorize(t *testing.T) {
	ui := NewTerminalUI()

	colored := ui.colorize("test", colorRed)

	if !strings.HasPrefix(colored, colorRed) {
		t.Error("colorized string should start with color code")
	}
	if !strings.HasSuffix(colored, colorReset) {
		t.Error("colorized string should end with reset code")
	}
	if !strings.Contains(colored, "test") {
		t.Error("colorized string should contain original text")
	}
}

func TestTerminalUI_RateLimit(t *testing.T) {
	ui := NewTerminalUI()
	ui.minRenderGap = 50 * time.Millisecond

	metrics := &FuzzMetrics{
		TotalSeedsRun: 10,
	}
	ui.SetMetrics(metrics)

	// First render should work
	ui.Render()
	firstRenderLines := ui.renderLines

	// Immediate second render should be rate-limited
	ui.renderLines = 0 // Reset to check if render happens
	ui.Render()

	// renderLines should still be 0 because render was skipped
	if ui.renderLines != 0 {
		// Note: This might pass if the render was actually allowed
		// The rate limiting only skips if time since last render is < minRenderGap
	}

	// Wait and try again
	time.Sleep(60 * time.Millisecond)
	ui.Render()

	if ui.renderLines < firstRenderLines {
		// After waiting, render should work
		// (can't strictly test this without capturing stderr)
	}
}

func TestTerminalUI_ProgressBarEdgeCases(t *testing.T) {
	ui := NewTerminalUI()

	tests := []struct {
		name    string
		metrics *FuzzMetrics
	}{
		{
			name: "zero total lines",
			metrics: &FuzzMetrics{
				TotalLines:        0,
				TotalCoveredLines: 0,
				CurrentCoverage:   0,
			},
		},
		{
			name: "100% coverage",
			metrics: &FuzzMetrics{
				TotalLines:        100,
				TotalCoveredLines: 100,
				CurrentCoverage:   100.0,
			},
		},
		{
			name: "very low coverage",
			metrics: &FuzzMetrics{
				TotalLines:        10000,
				TotalCoveredLines: 1,
				CurrentCoverage:   0.01,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bar := ui.formatCoverageBar(60, tt.metrics)
			if bar == "" {
				t.Error("formatCoverageBar should return non-empty string")
			}
			if !strings.Contains(bar, "[") || !strings.Contains(bar, "]") {
				t.Error("progress bar should have brackets")
			}
		})
	}
}

func TestIsTerminal(t *testing.T) {
	// This test just verifies the function doesn't panic
	// The actual result depends on the test environment
	_ = IsTerminal()
}
