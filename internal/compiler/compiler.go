package compiler

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/zjy-dev/de-fuzz/internal/exec"
	"github.com/zjy-dev/de-fuzz/internal/seed"
)

// CompileResult holds the outcome of a compilation.
type CompileResult struct {
	BinaryPath string // Path to the compiled binary
	Success    bool   // Whether compilation succeeded
	Stdout     string // Compiler stdout
	Stderr     string // Compiler stderr (warnings, errors)
}

// Compiler defines the interface for compiling C code.
type Compiler interface {
	// Compile compiles the seed's C source code and returns the path to the binary.
	Compile(s *seed.Seed) (*CompileResult, error)

	// GetWorkDir returns the working directory for compilation.
	GetWorkDir() string
}

// GCCCompiler implements the Compiler interface using GCC.
type GCCCompiler struct {
	executor   exec.Executor
	gccPath    string   // Path to gcc executable (e.g., "gcc" or "/usr/bin/aarch64-linux-gnu-gcc")
	workDir    string   // Working directory for compilation
	prefixPath string   // -B prefix path for compiler components (cc1, as, ld, etc.)
	cflags     []string // Additional compiler flags as a slice
}

// GCCCompilerConfig holds the configuration for GCCCompiler.
type GCCCompilerConfig struct {
	GCCPath    string   // Path to GCC executable
	WorkDir    string   // Working directory
	PrefixPath string   // -B prefix path for finding compiler components (cc1, as, ld)
	CFlags     []string // Additional compiler flags as a slice
}

// NewGCCCompiler creates a new GCC compiler.
func NewGCCCompiler(cfg GCCCompilerConfig) *GCCCompiler {
	return &GCCCompiler{
		executor:   exec.NewCommandExecutor(),
		gccPath:    cfg.GCCPath,
		workDir:    cfg.WorkDir,
		prefixPath: cfg.PrefixPath,
		cflags:     cfg.CFlags,
	}
}

// Compile compiles the seed's C source code.
func (c *GCCCompiler) Compile(s *seed.Seed) (*CompileResult, error) {
	return c.compile(s)
}

// GetWorkDir returns the working directory.
func (c *GCCCompiler) GetWorkDir() string {
	return c.workDir
}

func (c *GCCCompiler) compile(s *seed.Seed) (*CompileResult, error) {
	// Ensure work directory exists
	if err := os.MkdirAll(c.workDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create work directory: %w", err)
	}

	// Write source file
	sourceFile := filepath.Join(c.workDir, fmt.Sprintf("seed_%d.c", s.Meta.ID))
	if err := os.WriteFile(sourceFile, []byte(s.Content), 0644); err != nil {
		return nil, fmt.Errorf("failed to write source file: %w", err)
	}

	// Determine output binary path
	binaryPath := filepath.Join(c.workDir, fmt.Sprintf("seed_%d", s.Meta.ID))

	// Build compile command arguments
	var args []string

	// Add -B prefix path first if specified (for finding cc1, as, ld, etc.)
	if c.prefixPath != "" {
		args = append(args, "-B"+c.prefixPath)
	}

	// Add user-specified flags
	args = append(args, c.cflags...)

	// Add source file and output
	args = append(args, sourceFile, "-o", binaryPath)

	// Run GCC
	result, err := c.executor.Run(c.gccPath, args...)
	if err != nil {
		return &CompileResult{
			BinaryPath: "",
			Success:    false,
			Stdout:     "",
			Stderr:     fmt.Sprintf("failed to run compiler: %v", err),
		}, nil
	}

	success := result.ExitCode == 0

	return &CompileResult{
		BinaryPath: binaryPath,
		Success:    success,
		Stdout:     result.Stdout,
		Stderr:     result.Stderr,
	}, nil
}

// CrossGCCCompiler extends GCCCompiler for cross-compilation.
type CrossGCCCompiler struct {
	*GCCCompiler
	targetArch string // Target architecture (e.g., "aarch64", "riscv64")
	sysroot    string // Sysroot for cross-compilation
}

// CrossGCCCompilerConfig holds configuration for cross-compilation.
type CrossGCCCompilerConfig struct {
	GCCCompilerConfig
	TargetArch string
	Sysroot    string
}

// NewCrossGCCCompiler creates a cross-compiler.
func NewCrossGCCCompiler(cfg CrossGCCCompilerConfig) *CrossGCCCompiler {
	base := NewGCCCompiler(cfg.GCCCompilerConfig)
	return &CrossGCCCompiler{
		GCCCompiler: base,
		targetArch:  cfg.TargetArch,
		sysroot:     cfg.Sysroot,
	}
}

// GetTargetArch returns the target architecture.
func (c *CrossGCCCompiler) GetTargetArch() string {
	return c.targetArch
}
