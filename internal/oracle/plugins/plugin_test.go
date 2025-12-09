package plugins

import (
	"testing"

	"github.com/zjy-dev/de-fuzz/internal/oracle"
)

func TestLLMOracleRegistration(t *testing.T) {
	// Test that LLM oracle is registered
	_, err := oracle.New("llm", nil, nil, nil, "")
	if err == nil {
		t.Error("Expected error when creating LLM oracle without required dependencies")
	}
}

func TestCrashOracleRegistration(t *testing.T) {
	// Test that Crash oracle is registered
	orc, err := oracle.New("crash", nil, nil, nil, "")
	if err != nil {
		t.Fatalf("Failed to create crash oracle: %v", err)
	}
	if orc == nil {
		t.Error("Crash oracle should not be nil")
	}
}

func TestUnknownOracle(t *testing.T) {
	_, err := oracle.New("unknown", nil, nil, nil, "")
	if err == nil {
		t.Error("Expected error when requesting unknown oracle")
	}
}
