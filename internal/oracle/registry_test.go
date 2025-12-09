package oracle

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/zjy-dev/de-fuzz/internal/llm"
	"github.com/zjy-dev/de-fuzz/internal/prompt"
)

// TestRegister tests oracle factory registration.
func TestRegister(t *testing.T) {
	// Create a test factory with correct signature
	called := false
	testFactory := func(options map[string]interface{}, l llm.LLM, prompter *prompt.Builder, context string) (Oracle, error) {
		called = true
		return nil, nil
	}

	// Register the test oracle
	Register("test_oracle", testFactory)

	// Verify it's in the registry
	_, err := New("test_oracle", nil, nil, nil, "")
	assert.NoError(t, err)
	assert.True(t, called, "factory should have been called")
}

// TestNew_UnknownOracle tests error handling for unknown oracle types.
func TestNew_UnknownOracle(t *testing.T) {
	_, err := New("nonexistent_oracle", nil, nil, nil, "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "oracle plugin not found")
}

// TestLLMOracleRegistered tests that LLM oracle is properly registered.
func TestLLMOracleRegistered(t *testing.T) {
	// Test that LLM oracle is registered but requires dependencies
	_, err := New("llm", nil, nil, nil, "")
	assert.Error(t, err, "Expected error when creating LLM oracle without required dependencies")
}

// TestCrashOracleRegistered tests that Crash oracle is properly registered.
func TestCrashOracleRegistered(t *testing.T) {
	// Test that Crash oracle is registered and can be created
	orc, err := New("crash", nil, nil, nil, "")
	assert.NoError(t, err)
	assert.NotNil(t, orc)
}

// TestCanaryOracleRegistered tests that Canary oracle is properly registered.
func TestCanaryOracleRegistered(t *testing.T) {
	// Test that Canary oracle is registered and can be created
	orc, err := New("canary", nil, nil, nil, "")
	assert.NoError(t, err)
	assert.NotNil(t, orc)
}

// TestRegistryIsolation tests that the registry properly isolates different oracle types.
func TestRegistryIsolation(t *testing.T) {
	// Create oracles of different types
	crash, err1 := New("crash", nil, nil, nil, "")
	canary, err2 := New("canary", nil, nil, nil, "")

	assert.NoError(t, err1)
	assert.NoError(t, err2)

	// Verify both were created successfully
	assert.NotNil(t, crash)
	assert.NotNil(t, canary)

	// Verify they are different types (not the same oracle)
	// Note: CrashOracle is stateless and may return the same instance,
	// but CanaryOracle creates new instances with state
	_, isCrash := crash.(*CrashOracle)
	_, isCanary := canary.(*CanaryOracle)
	assert.True(t, isCrash, "crash should be CrashOracle type")
	assert.True(t, isCanary, "canary should be CanaryOracle type")
}
