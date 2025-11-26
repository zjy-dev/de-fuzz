package seed

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseSeedFromLLMResponse(t *testing.T) {
	t.Run("should parse valid response", func(t *testing.T) {
		response := `int main() { return 0; }
// ||||| JSON_TESTCASES_START |||||
[
  {
    "running command": "./prog",
    "expected result": "success"
  }
]`

		source, testCases, err := ParseSeedFromLLMResponse(response)
		require.NoError(t, err)
		assert.Equal(t, "int main() { return 0; }", source)
		assert.Len(t, testCases, 1)
		assert.Equal(t, "./prog", testCases[0].RunningCommand)
		assert.Equal(t, "success", testCases[0].ExpectedResult)
	})

	t.Run("should parse response with multiple test cases", func(t *testing.T) {
		response := `#include <stdio.h>
int main() {
    printf("Hello\n");
    return 0;
}
// ||||| JSON_TESTCASES_START |||||
[
  {
    "running command": "./prog",
    "expected result": "Hello"
  },
  {
    "running command": "./prog arg1",
    "expected result": "with arg"
  }
]`

		source, testCases, err := ParseSeedFromLLMResponse(response)
		require.NoError(t, err)
		assert.Contains(t, source, "printf")
		assert.Len(t, testCases, 2)
	})

	t.Run("should fail when separator is missing", func(t *testing.T) {
		response := `Some random text without proper format`

		_, _, err := ParseSeedFromLLMResponse(response)
		require.Error(t, err)
		assert.IsType(t, &ValidationError{}, err)
		assert.Contains(t, err.Error(), "separator")
	})

	t.Run("should fail when source is empty", func(t *testing.T) {
		response := `
// ||||| JSON_TESTCASES_START |||||
[{"running command": "./prog", "expected result": "ok"}]`

		_, _, err := ParseSeedFromLLMResponse(response)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "source code is empty")
	})

	t.Run("should fail when test cases array is empty", func(t *testing.T) {
		response := `int main() {}
// ||||| JSON_TESTCASES_START |||||
[]`

		_, _, err := ParseSeedFromLLMResponse(response)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "at least one test case")
	})

	t.Run("should fail when test case has empty running command", func(t *testing.T) {
		response := `int main() {}
// ||||| JSON_TESTCASES_START |||||
[{"running command": "", "expected result": "ok"}]`

		_, _, err := ParseSeedFromLLMResponse(response)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "running command is empty")
	})

	t.Run("should fail when test cases JSON is invalid", func(t *testing.T) {
		response := `int main() {}
// ||||| JSON_TESTCASES_START |||||
invalid json here`

		_, _, err := ParseSeedFromLLMResponse(response)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to parse test cases JSON")
	})
}

func TestValidateSeed(t *testing.T) {
	t.Run("should pass for valid seed", func(t *testing.T) {
		s := &Seed{
			Content: "int main() {}",
			TestCases: []TestCase{
				{RunningCommand: "./prog", ExpectedResult: "ok"},
			},
		}
		err := ValidateSeed(s)
		assert.NoError(t, err)
	})

	t.Run("should fail for nil seed", func(t *testing.T) {
		err := ValidateSeed(nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "seed is nil")
	})

	t.Run("should fail for empty content", func(t *testing.T) {
		s := &Seed{
			Content: "",
			TestCases: []TestCase{
				{RunningCommand: "./prog", ExpectedResult: "ok"},
			},
		}
		err := ValidateSeed(s)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "content is empty")
	})

	t.Run("should fail for empty test cases", func(t *testing.T) {
		s := &Seed{
			Content:   "int main() {}",
			TestCases: []TestCase{},
		}
		err := ValidateSeed(s)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "at least one test case")
	})

	t.Run("should fail for test case with empty running command", func(t *testing.T) {
		s := &Seed{
			Content: "int main() {}",
			TestCases: []TestCase{
				{RunningCommand: "", ExpectedResult: "ok"},
			},
		}
		err := ValidateSeed(s)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "running command is empty")
	})
}

func TestValidationError(t *testing.T) {
	err := &ValidationError{
		Field:   "test_field",
		Message: "test message",
	}
	assert.Equal(t, "validation error in test_field: test message", err.Error())
}
