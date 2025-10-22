package executor

import (
	"fmt"
	"os"
	"path/filepath"

	"defuzz/internal/compiler"
	"defuzz/internal/seed"
	"defuzz/internal/vm"
)

// QemuExecutor implements the Executor interface using QEMU.
type QemuExecutor struct {
	compiler    compiler.Compiler
	commandPath string // Path to compile_command.txt
}

// NewQemuExecutor creates a new QemuExecutor.
func NewQemuExecutor(compiler compiler.Compiler, commandPath string) *QemuExecutor {
	return &QemuExecutor{
		compiler:    compiler,
		commandPath: commandPath,
	}
}

// Execute uses the provided VM to execute all test cases for the seed.
func (e *QemuExecutor) Execute(s *seed.Seed, v vm.VM) ([]ExecutionResult, error) {
	binaryPath, err := e.compiler.Compile(s, e.commandPath)
	if err != nil {
		return nil, fmt.Errorf("failed to compile seed %s: %w", s.ID, err)
	}
	defer os.Remove(filepath.Dir(binaryPath)) // Clean up the temp directory

	var results []ExecutionResult

	// Execute each test case
	for _, testCase := range s.TestCases {
		// Create a temporary script for this test case
		scriptPath := filepath.Join(filepath.Dir(binaryPath), "test_script.sh")
		scriptContent := fmt.Sprintf("#!/bin/bash\n%s\n", testCase.RunningCommand)

		if err := os.WriteFile(scriptPath, []byte(scriptContent), 0755); err != nil {
			return nil, fmt.Errorf("failed to write test script: %w", err)
		}

		// Execute the test case using VM
		vmResult, err := v.Run(binaryPath, scriptPath)
		if err != nil {
			return nil, fmt.Errorf("failed to execute test case: %w", err)
		}

		// Convert VM result to ExecutionResult
		result := ExecutionResult{
			Stdout:   vmResult.Stdout,
			Stderr:   vmResult.Stderr,
			ExitCode: vmResult.ExitCode,
		}

		results = append(results, result)

		// Clean up the script file
		os.Remove(scriptPath)
	}

	return results, nil
}
