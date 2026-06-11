package coverage

import (
	"path/filepath"
	"testing"
)

func TestParseLLVMCovExport_TypeValidation(t *testing.T) {
	// Valid sample parses and has correct type.
	export, err := parseLLVMCovExport(filepath.Join("artifacts", "llvm_cov_sample.json"))
	if err != nil {
		t.Fatalf("parseLLVMCovExport() error = %v", err)
	}
	if export.Type != llvmCovExportType {
		t.Errorf("export.Type = %q, want %q", export.Type, llvmCovExportType)
	}

	// A gcovr-style JSON must be rejected.
	tmp := t.TempDir()
	bad := filepath.Join(tmp, "gcovr.json")
	if err := writeFile(bad, `{"gcovr/format_version":"0.5","files":[]}`); err != nil {
		t.Fatal(err)
	}
	if _, err := parseLLVMCovExport(bad); err == nil {
		t.Error("parseLLVMCovExport() should reject non-llvm JSON type")
	}
}

func TestCoveredLinesFromExport(t *testing.T) {
	export, err := parseLLVMCovExport(filepath.Join("artifacts", "llvm_cov_sample.json"))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	covered := coveredLinesFromExport(export)
	set := coveredLineSet(covered)

	// From the fixture: StackProtector.cpp lines 100,101 (count>0, regionEntry),
	// 130 (count>0 but NOT regionEntry -> excluded), 102 (count==0 -> excluded),
	// 120 (hasCount==false -> excluded). Other.cpp line 10 (count>0), 11 (count==0).
	wantCovered := []string{
		"/src/llvm/lib/CodeGen/StackProtector.cpp:100",
		"/src/llvm/lib/CodeGen/StackProtector.cpp:101",
		"/src/llvm/lib/CodeGen/Other.cpp:10",
	}
	for _, w := range wantCovered {
		if !set[w] {
			t.Errorf("expected covered line %q missing", w)
		}
	}
	wantExcluded := []string{
		"/src/llvm/lib/CodeGen/StackProtector.cpp:102", // count==0
		"/src/llvm/lib/CodeGen/StackProtector.cpp:120", // hasCount==false
		"/src/llvm/lib/CodeGen/StackProtector.cpp:130", // not region entry
		"/src/llvm/lib/CodeGen/Other.cpp:11",           // count==0
	}
	for _, w := range wantExcluded {
		if set[w] {
			t.Errorf("line %q should NOT be covered", w)
		}
	}
}

func TestJSONToBool_NumericAndBool(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"true", true},
		{"false", false},
		{"1", true},
		{"0", false},
	}
	for _, c := range cases {
		if got := jsonToBool([]byte(c.in)); got != c.want {
			t.Errorf("jsonToBool(%s) = %v, want %v", c.in, got, c.want)
		}
	}
}
