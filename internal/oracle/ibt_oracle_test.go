package oracle

import (
	"testing"

	"github.com/zjy-dev/de-fuzz/internal/seed"
)

// ---- NewIBTOracle ----

func TestNewIBTOracle_NoOptions(t *testing.T) {
	o, err := NewIBTOracle(nil, nil, nil, "")
	if err != nil {
		t.Fatalf("NewIBTOracle(nil): %v", err)
	}
	if _, ok := o.(*IBTOracle); !ok {
		t.Fatalf("NewIBTOracle must return *IBTOracle, got %T", o)
	}
}

// ---- Analyze error paths ----

func TestIBTOracle_Analyze_NilContext(t *testing.T) {
	o := &IBTOracle{}
	_, err := o.Analyze(&seed.Seed{}, nil, nil)
	if err == nil {
		t.Error("nil AnalyzeContext must return error")
	}
}

func TestIBTOracle_Analyze_EmptyBinaryPath(t *testing.T) {
	o := &IBTOracle{}
	_, err := o.Analyze(&seed.Seed{}, &AnalyzeContext{BinaryPath: ""}, nil)
	if err == nil {
		t.Error("empty BinaryPath must return error")
	}
}

// ---- registry ----

func TestIBTOracle_RegisteredAsIBT(t *testing.T) {
	o, err := New("ibt", nil, nil, nil, "")
	if err != nil {
		t.Fatalf("oracle 'ibt' not found in registry: %v", err)
	}
	if _, ok := o.(*IBTOracle); !ok {
		t.Errorf("registry 'ibt' returned %T, want *IBTOracle", o)
	}
}
