package oracle

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestRegister tests oracle factory registration.
func TestRegister(t *testing.T) {
	// Create a test factory with correct signature
	called := false
	testFactory := func(options map[string]interface{}) (Oracle, error) {
		called = true
		return nil, nil
	}

	// Register the test oracle
	Register("test_oracle", testFactory)

	// Verify it's in the registry
	_, err := New("test_oracle", nil)
	assert.NoError(t, err)
	assert.True(t, called, "factory should have been called")
}

// TestNew_UnknownOracle tests error handling for unknown oracle types.
func TestNew_UnknownOracle(t *testing.T) {
	_, err := New("nonexistent_oracle", nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "oracle plugin not found")
}

// TestCrashOracleRegistered tests that Crash oracle is properly registered.
func TestCrashOracleRegistered(t *testing.T) {
	orc, err := New("crash", nil)
	assert.NoError(t, err)
	assert.NotNil(t, orc)
}

// TestCanaryOracleRegistered tests that Canary oracle is properly registered.
func TestCanaryOracleRegistered(t *testing.T) {
	orc, err := New("canary", nil)
	assert.NoError(t, err)
	assert.NotNil(t, orc)
}

// TestRegistryIsolation tests that the registry properly isolates different oracle types.
func TestRegistryIsolation(t *testing.T) {
	crash, err1 := New("crash", nil)
	canary, err2 := New("canary", nil)

	assert.NoError(t, err1)
	assert.NoError(t, err2)

	assert.NotNil(t, crash)
	assert.NotNil(t, canary)

	_, isCrash := crash.(*CrashOracle)
	_, isCanary := canary.(*CanaryOracle)
	assert.True(t, isCrash, "crash should be CrashOracle type")
	assert.True(t, isCanary, "canary should be CanaryOracle type")
}
