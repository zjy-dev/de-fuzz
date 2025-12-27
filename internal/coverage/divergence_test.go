package coverage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseReplayOutput(t *testing.T) {
	analyzer := &UftraceAnalyzer{contextSize: 5}

	// Sample uftrace replay output
	output := `            [852229] | main() {
   2.709 us [852229] |   getrlimit();
            [852229] |   toplev::main() {
            [852229] |     pretty_printer::pretty_printer() {
   1.234 us [852229] |       file_cache::file_cache();
            [852229] |     } /* pretty_printer::pretty_printer */
            [852229] |     c_parser_peek_token() {
   0.567 us [852229] |       c_lex_one_token();
            [852229] |     } /* c_parser_peek_token */
            [852229] |     gen_addsi3() {
   0.123 us [852229] |       start_sequence();
            [852229] |     } /* gen_addsi3 */
            [852229] |   } /* toplev::main */
            [852229] | } /* main */
            [852230] | linux:schedule();`

	calls, err := analyzer.parseReplayOutput(output, "852229")
	if err != nil {
		t.Fatalf("parseReplayOutput failed: %v", err)
	}

	// Should have extracted function entries only (not exits)
	// The regex matches: main, getrlimit, toplev::main, pretty_printer::pretty_printer,
	// file_cache::file_cache, c_parser_peek_token, c_lex_one_token, gen_addsi3, start_sequence
	expectedFuncs := []string{
		"main",
		"getrlimit",
		"toplev",
		"pretty_printer",
		"file_cache",
		"c_parser_peek_token",
		"c_lex_one_token",
		"gen_addsi3",
		"start_sequence",
	}

	if len(calls) != len(expectedFuncs) {
		t.Errorf("Expected %d calls, got %d", len(expectedFuncs), len(calls))
		for i, c := range calls {
			t.Logf("  [%d] %s (depth=%d)", i, c.Name, c.Depth)
		}
	}

	// Check that scheduler calls from other PIDs are filtered
	for _, c := range calls {
		if strings.Contains(c.Name, "schedule") {
			t.Errorf("Should have filtered out schedule call: %s", c.Name)
		}
	}
}

func TestFindParserStart(t *testing.T) {
	analyzer := &UftraceAnalyzer{contextSize: 5}

	calls := []FunctionCall{
		{Name: "main", Depth: 0},
		{Name: "toplev::main", Depth: 1},
		{Name: "pretty_printer", Depth: 2},
		{Name: "file_cache", Depth: 2},
		{Name: "c_parser_peek_token", Depth: 2}, // Parser starts here
		{Name: "c_lex_one_token", Depth: 3},
		{Name: "gen_addsi3", Depth: 2},
	}

	start := analyzer.findParserStart(calls)
	if start != 4 {
		t.Errorf("Expected parser start at index 4, got %d", start)
	}

	// Test with "parse" in lowercase
	calls2 := []FunctionCall{
		{Name: "main", Depth: 0},
		{Name: "init_parse_context", Depth: 1}, // Should match
		{Name: "do_stuff", Depth: 2},
	}

	start2 := analyzer.findParserStart(calls2)
	if start2 != 1 {
		t.Errorf("Expected parser start at index 1, got %d", start2)
	}
}

func TestFindDivergence(t *testing.T) {
	analyzer := &UftraceAnalyzer{contextSize: 3}

	calls1 := []FunctionCall{
		{Name: "common1", Depth: 0},
		{Name: "common2", Depth: 1},
		{Name: "common3", Depth: 1},
		{Name: "gen_addsi3", Depth: 2}, // Divergence here
		{Name: "start_sequence", Depth: 3},
		{Name: "gen_movsi", Depth: 3},
	}

	calls2 := []FunctionCall{
		{Name: "common1", Depth: 0},
		{Name: "common2", Depth: 1},
		{Name: "common3", Depth: 1},
		{Name: "optimize_insn_for_speed_p", Depth: 2}, // Divergence here
		{Name: "register_operand", Depth: 2},
		{Name: "gen_movsi", Depth: 2},
	}

	div := analyzer.findDivergence(calls1, calls2)
	if div == nil {
		t.Fatal("Expected divergence point, got nil")
	}

	if div.Index != 3 {
		t.Errorf("Expected divergence at index 3, got %d", div.Index)
	}

	if div.Function1 != "gen_addsi3" {
		t.Errorf("Expected Function1='gen_addsi3', got '%s'", div.Function1)
	}

	if div.Function2 != "optimize_insn_for_speed_p" {
		t.Errorf("Expected Function2='optimize_insn_for_speed_p', got '%s'", div.Function2)
	}

	// Check common prefix (last 3 before divergence)
	expectedPrefix := []string{"common1", "common2", "common3"}
	if len(div.CommonPrefix) != len(expectedPrefix) {
		t.Errorf("Expected common prefix length %d, got %d", len(expectedPrefix), len(div.CommonPrefix))
	}
	for i, p := range expectedPrefix {
		if i < len(div.CommonPrefix) && div.CommonPrefix[i] != p {
			t.Errorf("CommonPrefix[%d]: expected '%s', got '%s'", i, p, div.CommonPrefix[i])
		}
	}

	// Check paths after divergence
	if len(div.Path1) != 3 {
		t.Errorf("Expected Path1 length 3, got %d", len(div.Path1))
	}
	if len(div.Path2) != 3 {
		t.Errorf("Expected Path2 length 3, got %d", len(div.Path2))
	}
}

func TestFindDivergenceIdenticalTraces(t *testing.T) {
	analyzer := &UftraceAnalyzer{contextSize: 3}

	calls := []FunctionCall{
		{Name: "func1", Depth: 0},
		{Name: "func2", Depth: 1},
		{Name: "func3", Depth: 1},
	}

	div := analyzer.findDivergence(calls, calls)
	if div != nil {
		t.Errorf("Expected nil for identical traces, got: %v", div)
	}
}

func TestFindDivergenceDifferentLengths(t *testing.T) {
	analyzer := &UftraceAnalyzer{contextSize: 3}

	calls1 := []FunctionCall{
		{Name: "func1", Depth: 0},
		{Name: "func2", Depth: 1},
		{Name: "func3", Depth: 1},
	}

	calls2 := []FunctionCall{
		{Name: "func1", Depth: 0},
		{Name: "func2", Depth: 1},
	}

	div := analyzer.findDivergence(calls1, calls2)
	if div == nil {
		t.Fatal("Expected divergence for different lengths")
	}

	if div.Index != 2 {
		t.Errorf("Expected divergence at index 2, got %d", div.Index)
	}

	if div.Function1 != "func3" {
		t.Errorf("Expected Function1='func3', got '%s'", div.Function1)
	}

	if div.Function2 != "" {
		t.Errorf("Expected Function2='', got '%s'", div.Function2)
	}
}

func TestDivergencePointString(t *testing.T) {
	div := &DivergencePoint{
		Index:        42,
		Function1:    "gen_addsi3",
		Function2:    "optimize_insn_for_speed_p",
		CommonPrefix: []string{"common1", "common2"},
		Path1:        []string{"gen_addsi3", "start_sequence"},
		Path2:        []string{"optimize_insn_for_speed_p", "register_operand"},
	}

	str := div.String()
	if !strings.Contains(str, "42") {
		t.Error("String() should contain index")
	}
	if !strings.Contains(str, "gen_addsi3") {
		t.Error("String() should contain Function1")
	}
	if !strings.Contains(str, "optimize_insn_for_speed_p") {
		t.Error("String() should contain Function2")
	}
}

func TestDivergencePointForLLM(t *testing.T) {
	div := &DivergencePoint{
		Index:        42,
		Function1:    "gen_addsi3",
		Function2:    "optimize_insn_for_speed_p",
		CommonPrefix: []string{"update_bb_for_insn"},
		Path1:        []string{"gen_addsi3", "start_sequence"},
		Path2:        []string{"optimize_insn_for_speed_p", "register_operand"},
	}

	llmStr := div.ForLLM()
	if !strings.Contains(llmStr, "## Divergence Analysis") {
		t.Error("ForLLM() should contain markdown header")
	}
	if !strings.Contains(llmStr, "`gen_addsi3`") {
		t.Error("ForLLM() should format function names as code")
	}
	if !strings.Contains(llmStr, "Context Before Divergence") {
		t.Error("ForLLM() should contain context section")
	}
}

func TestExtractCC1PID(t *testing.T) {
	// Create a temporary task.txt file
	tmpDir := t.TempDir()
	taskContent := `1234567890 gcc pid=12345 ppid=12344
1234567891 cc1 pid=12346 ppid=12345
1234567892 as pid=12347 ppid=12345
1234567893 collect2 pid=12348 ppid=12345`

	taskFile := filepath.Join(tmpDir, "task.txt")
	if err := os.WriteFile(taskFile, []byte(taskContent), 0644); err != nil {
		t.Fatalf("Failed to write task.txt: %v", err)
	}

	analyzer := &UftraceAnalyzer{}
	pid, err := analyzer.extractCC1PID(tmpDir)
	if err != nil {
		t.Fatalf("extractCC1PID failed: %v", err)
	}

	if pid != "12346" {
		t.Errorf("Expected PID '12346', got '%s'", pid)
	}
}

func TestExtractCC1PIDNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	taskContent := `1234567890 gcc pid=12345 ppid=12344
1234567892 as pid=12347 ppid=12345`

	taskFile := filepath.Join(tmpDir, "task.txt")
	if err := os.WriteFile(taskFile, []byte(taskContent), 0644); err != nil {
		t.Fatalf("Failed to write task.txt: %v", err)
	}

	analyzer := &UftraceAnalyzer{}
	_, err := analyzer.extractCC1PID(tmpDir)
	if err == nil {
		t.Error("Expected error when cc1 not found")
	}
	if !strings.Contains(err.Error(), "cc1 process not found") {
		t.Errorf("Unexpected error message: %v", err)
	}
}

func TestNilDivergencePoint(t *testing.T) {
	var div *DivergencePoint

	str := div.String()
	if str != "no divergence" {
		t.Errorf("Expected 'no divergence', got '%s'", str)
	}

	llmStr := div.ForLLM()
	if llmStr != "" {
		t.Errorf("Expected empty string for nil, got '%s'", llmStr)
	}
}
