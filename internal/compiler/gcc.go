package compiler

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"defuzz/internal/seed"
)

// GccCompiler implements the Compiler interface using gcc.
type GccCompiler struct{}

// NewGccCompiler creates a new GccCompiler.
func NewGccCompiler() *GccCompiler {
	return &GccCompiler{}
}

// Compile compiles the given seed using a compile command template and returns the path to the compiled binary.
func (c *GccCompiler) Compile(s *seed.Seed, commandPath string) (string, error) {
	// Read the compile command template
	commandTemplate, err := os.ReadFile(commandPath)
	if err != nil {
		return "", fmt.Errorf("failed to read compile command from %s: %w", commandPath, err)
	}

	tempDir, err := os.MkdirTemp("", "defuzz-compiler-")
	if err != nil {
		return "", fmt.Errorf("failed to create temp dir: %w", err)
	}

	sourcePath := filepath.Join(tempDir, "source.c")
	if err := os.WriteFile(sourcePath, []byte(s.Content), 0644); err != nil {
		return "", fmt.Errorf("failed to write source file: %w", err)
	}

	binaryPath := filepath.Join(tempDir, "prog")

	// Replace placeholders in the command template
	command := strings.ReplaceAll(string(commandTemplate), "{input_path}", sourcePath)
	command = strings.ReplaceAll(command, "{output_path}", binaryPath)
	command = strings.TrimSpace(command)

	// Split command into parts for exec.Command
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return "", fmt.Errorf("empty compile command")
	}

	cmd := exec.Command(parts[0], parts[1:]...)
	cmd.Dir = tempDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to compile seed %s: %w\nOutput:\n%s", s.ID, err, string(output))
	}

	return binaryPath, nil
}
