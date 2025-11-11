package executor

import (
	"fmt"
	"os"
	"path/filepath"

	"defuzz/internal/compiler"
	"defuzz/internal/exec"
	"defuzz/internal/seed"
)

// QemuExecutor implements the Executor interface using QEMU on the host machine.
type QemuExecutor struct {
	compiler    compiler.Compiler
	commandPath string        // Path to compile_command.txt
	executor    exec.Executor // For running commands on host
}

// NewQemuExecutor creates a new QemuExecutor.
func NewQemuExecutor(compiler compiler.Compiler, commandPath string, executor exec.Executor) *QemuExecutor {
	return &QemuExecutor{
		compiler:    compiler,
		commandPath: commandPath,
		executor:    executor,
	}
}

// Execute runs all test cases for the seed on the host machine.
func (e *QemuExecutor) Execute(s *seed.Seed) ([]ExecutionResult, error) {
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
		scriptContent := fmt.Sprintf("#!/bin/bash\ncd %s\n%s\n", filepath.Dir(binaryPath), testCase.RunningCommand)

		if err := os.WriteFile(scriptPath, []byte(scriptContent), 0755); err != nil {
			return nil, fmt.Errorf("failed to write test script: %w", err)
		}

		// Execute the test case directly on the host using bash
		execResult, err := e.executor.Run("bash", scriptPath)
		if err != nil {
			return nil, fmt.Errorf("failed to execute test case: %w", err)
		}

		// Convert exec result to ExecutionResult
		result := ExecutionResult{
			Stdout:   execResult.Stdout,
			Stderr:   execResult.Stderr,
			ExitCode: execResult.ExitCode,
		}

		results = append(results, result)

		// Clean up the script file
		os.Remove(scriptPath)
	}

	return results, nil
}
