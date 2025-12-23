package state

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// ANSI color codes
const (
	colorReset   = "\033[0m"
	colorRed     = "\033[31m"
	colorGreen   = "\033[32m"
	colorYellow  = "\033[33m"
	colorBlue    = "\033[34m"
	colorMagenta = "\033[35m"
	colorCyan    = "\033[36m"
	colorWhite   = "\033[37m"
	colorBold    = "\033[1m"
	colorDim     = "\033[2m"

	// Background colors
	bgRed    = "\033[41m"
	bgGreen  = "\033[42m"
	bgYellow = "\033[43m"
	bgBlue   = "\033[44m"

	// Cursor control
	cursorHide    = "\033[?25l"
	cursorShow    = "\033[?25h"
	clearScreen   = "\033[2J"
	cursorHome    = "\033[H"
	clearLine     = "\033[K"
	cursorUp      = "\033[%dA"
	cursorDown    = "\033[%dB"
	saveCursor    = "\033[s"
	restoreCursor = "\033[u"
)

// Box drawing characters (Unicode)
const (
	boxTopLeft     = "╔"
	boxTopRight    = "╗"
	boxBottomLeft  = "╚"
	boxBottomRight = "╝"
	boxHorizontal  = "═"
	boxVertical    = "║"
	boxTeeRight    = "╠"
	boxTeeLeft     = "╣"
	boxTeeDown     = "╦"
	boxTeeUp       = "╩"
	boxCross       = "╬"
)

// TerminalUI handles real-time terminal display with colors and progress bars.
type TerminalUI struct {
	mu           sync.Mutex
	metrics      *FuzzMetrics
	width        int
	height       int
	lastRender   time.Time
	renderLines  int // Number of lines rendered (for clearing)
	enabled      bool
	minRenderGap time.Duration
	suppressLogs bool // Whether to suppress regular log output during render
}

// NewTerminalUI creates a new terminal UI.
func NewTerminalUI() *TerminalUI {
	return &TerminalUI{
		width:        80,
		height:       24,
		enabled:      true,
		minRenderGap: 100 * time.Millisecond, // Minimum time between renders
		suppressLogs: false,
	}
}

// SetMetrics sets the metrics to display.
func (t *TerminalUI) SetMetrics(m *FuzzMetrics) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.metrics = m
}

// SetEnabled enables or disables the UI.
func (t *TerminalUI) SetEnabled(enabled bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.enabled = enabled
}

// Render draws the current state to the terminal.
func (t *TerminalUI) Render() {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.enabled || t.metrics == nil {
		return
	}

	// Rate limit rendering
	if time.Since(t.lastRender) < t.minRenderGap {
		return
	}
	t.lastRender = time.Now()

	// Build the display
	output := t.buildDisplay()

	// Count lines in output
	newRenderLines := strings.Count(output, "\n")

	// Move cursor up to overwrite previous render
	if t.renderLines > 0 {
		// Clear each line as we go up
		for i := 0; i < t.renderLines; i++ {
			fmt.Fprint(os.Stderr, "\033[A\033[2K") // Move up and clear line
		}
	}

	// Print the output
	fmt.Fprint(os.Stderr, output)

	// Update line count for next render
	t.renderLines = newRenderLines
}

// Clear clears the UI from the terminal.
func (t *TerminalUI) Clear() {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.renderLines > 0 {
		// Move cursor up and clear each line
		for i := 0; i < t.renderLines; i++ {
			fmt.Fprintf(os.Stderr, "\033[A\033[K")
		}
		t.renderLines = 0
	}
}

// buildDisplay constructs the display string.
func (t *TerminalUI) buildDisplay() string {
	m := t.metrics
	var sb strings.Builder

	// Calculate width (default 60, adjustable)
	width := 60

	// Title bar
	sb.WriteString(t.colorize(boxTopLeft, colorCyan))
	sb.WriteString(t.colorize(strings.Repeat(boxHorizontal, width-2), colorCyan))
	sb.WriteString(t.colorize(boxTopRight, colorCyan))
	sb.WriteString("\n")

	// Title
	title := " DE-FUZZ - Compiler Fuzzer "
	padding := (width - 2 - len(title)) / 2
	sb.WriteString(t.colorize(boxVertical, colorCyan))
	sb.WriteString(strings.Repeat(" ", padding))
	sb.WriteString(t.colorize(title, colorBold+colorYellow))
	sb.WriteString(strings.Repeat(" ", width-2-padding-len(title)))
	sb.WriteString(t.colorize(boxVertical, colorCyan))
	sb.WriteString("\n")

	// Separator
	sb.WriteString(t.colorize(boxTeeRight, colorCyan))
	sb.WriteString(t.colorize(strings.Repeat(boxHorizontal, width-2), colorCyan))
	sb.WriteString(t.colorize(boxTeeLeft, colorCyan))
	sb.WriteString("\n")

	// Runtime
	runtime := formatDuration(m.ElapsedSeconds)
	sb.WriteString(t.formatRow(width, "Runtime", runtime, colorWhite))

	// Seeds section
	sb.WriteString(t.formatRow(width, "Seeds Processed", fmt.Sprintf("%d", m.TotalSeedsRun), colorWhite))
	sb.WriteString(t.formatRow(width, "Coverage Increase", fmt.Sprintf("%d (%.1f%%)", m.CoverageIncrSeeds, safePercent(m.CoverageIncrSeeds, m.TotalSeedsRun)), colorGreen))
	sb.WriteString(t.formatRow(width, "Compile Failures", fmt.Sprintf("%d", m.CompileFailedSeeds), colorYellow))
	sb.WriteString(t.formatRow(width, "Crashes", fmt.Sprintf("%d", m.CrashedSeeds), colorRed))

	// Separator
	sb.WriteString(t.colorize(boxTeeRight, colorCyan))
	sb.WriteString(t.colorize(strings.Repeat(boxHorizontal, width-2), colorCyan))
	sb.WriteString(t.colorize(boxTeeLeft, colorCyan))
	sb.WriteString("\n")

	// Coverage progress bar
	sb.WriteString(t.formatCoverageBar(width, m))

	// Separator
	sb.WriteString(t.colorize(boxTeeRight, colorCyan))
	sb.WriteString(t.colorize(strings.Repeat(boxHorizontal, width-2), colorCyan))
	sb.WriteString(t.colorize(boxTeeLeft, colorCyan))
	sb.WriteString("\n")

	// Oracle section
	bugsColor := colorGreen
	if m.OracleFailures > 0 {
		bugsColor = colorRed + colorBold
	}
	sb.WriteString(t.formatRow(width, "Bugs Found", fmt.Sprintf("%d", m.OracleFailures), bugsColor))
	sb.WriteString(t.formatRow(width, "Oracle Checks", fmt.Sprintf("%d", m.OracleChecks), colorWhite))
	sb.WriteString(t.formatRow(width, "Oracle Errors", fmt.Sprintf("%d", m.OracleErrors), colorYellow))

	// Separator
	sb.WriteString(t.colorize(boxTeeRight, colorCyan))
	sb.WriteString(t.colorize(strings.Repeat(boxHorizontal, width-2), colorCyan))
	sb.WriteString(t.colorize(boxTeeLeft, colorCyan))
	sb.WriteString("\n")

	// LLM section
	sb.WriteString(t.formatRow(width, "LLM Calls", fmt.Sprintf("%d", m.LLMCalls), colorWhite))
	sb.WriteString(t.formatRow(width, "Seeds Generated", fmt.Sprintf("%d", m.SeedsGenerated), colorGreen))
	sb.WriteString(t.formatRow(width, "LLM Errors", fmt.Sprintf("%d", m.LLMErrors), colorYellow))

	// Separator
	sb.WriteString(t.colorize(boxTeeRight, colorCyan))
	sb.WriteString(t.colorize(strings.Repeat(boxHorizontal, width-2), colorCyan))
	sb.WriteString(t.colorize(boxTeeLeft, colorCyan))
	sb.WriteString("\n")

	// Performance
	sb.WriteString(t.formatRow(width, "Speed", fmt.Sprintf("%.2f seeds/sec", m.SeedsPerSecond), colorWhite))
	sb.WriteString(t.formatRow(width, "Avg Time/Seed", fmt.Sprintf("%.1f ms", m.AvgSeedTimeMs), colorWhite))

	// Bottom border
	sb.WriteString(t.colorize(boxBottomLeft, colorCyan))
	sb.WriteString(t.colorize(strings.Repeat(boxHorizontal, width-2), colorCyan))
	sb.WriteString(t.colorize(boxBottomRight, colorCyan))
	sb.WriteString("\n")

	return sb.String()
}

// formatRow formats a single row with label and value.
func (t *TerminalUI) formatRow(width int, label, value string, valueColor string) string {
	var sb strings.Builder

	// Left border
	sb.WriteString(t.colorize(boxVertical, colorCyan))
	sb.WriteString(" ")

	// Label (left aligned)
	labelWidth := 18
	sb.WriteString(t.colorize(label, colorDim))
	sb.WriteString(strings.Repeat(" ", labelWidth-len(label)))

	// Value (right aligned)
	valueWidth := width - labelWidth - 4
	padding := valueWidth - len(value)
	if padding > 0 {
		sb.WriteString(strings.Repeat(" ", padding))
	}
	sb.WriteString(t.colorize(value, valueColor))

	// Right border
	sb.WriteString(" ")
	sb.WriteString(t.colorize(boxVertical, colorCyan))
	sb.WriteString("\n")

	return sb.String()
}

// formatCoverageBar formats a coverage progress bar.
func (t *TerminalUI) formatCoverageBar(width int, m *FuzzMetrics) string {
	var sb strings.Builder

	// Label row
	sb.WriteString(t.colorize(boxVertical, colorCyan))
	sb.WriteString(" ")

	coverageLabel := fmt.Sprintf("Coverage: %.2f%% (%d/%d lines)", m.CurrentCoverage, m.TotalCoveredLines, m.TotalLines)
	sb.WriteString(t.colorize(coverageLabel, colorWhite))
	sb.WriteString(strings.Repeat(" ", width-3-len(coverageLabel)))
	sb.WriteString(t.colorize(boxVertical, colorCyan))
	sb.WriteString("\n")

	// Progress bar row
	sb.WriteString(t.colorize(boxVertical, colorCyan))
	sb.WriteString(" ")

	barWidth := width - 6
	filledWidth := 0
	if m.TotalLines > 0 {
		filledWidth = int(float64(barWidth) * float64(m.TotalCoveredLines) / float64(m.TotalLines))
	}
	if filledWidth > barWidth {
		filledWidth = barWidth
	}
	emptyWidth := barWidth - filledWidth

	// Build the bar
	sb.WriteString("[")
	if filledWidth > 0 {
		sb.WriteString(t.colorize(strings.Repeat("█", filledWidth), colorGreen))
	}
	if emptyWidth > 0 {
		sb.WriteString(t.colorize(strings.Repeat("░", emptyWidth), colorDim))
	}
	sb.WriteString("]")

	sb.WriteString(" ")
	sb.WriteString(t.colorize(boxVertical, colorCyan))
	sb.WriteString("\n")

	return sb.String()
}

// colorize wraps text with ANSI color codes.
func (t *TerminalUI) colorize(text, color string) string {
	return color + text + colorReset
}

// IsTerminal checks if stdout is a terminal.
func IsTerminal() bool {
	fileInfo, _ := os.Stdout.Stat()
	return (fileInfo.Mode() & os.ModeCharDevice) != 0
}
