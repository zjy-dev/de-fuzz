package compiler

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zjy-dev/de-fuzz/internal/exec"
	"github.com/zjy-dev/de-fuzz/internal/logger"
	"github.com/zjy-dev/de-fuzz/internal/seed"
)

// CompileResult holds the outcome of a compilation.
type CompileResult struct {
	BinaryPath     string   // Path to the compiled binary
	Success        bool     // Whether compilation succeeded
	Stdout         string   // Compiler stdout
	Stderr         string   // Compiler stderr (warnings, errors)
	Command        string   // Shell-safe command string for reproduction
	CompilerPath   string   // Compiler executable path
	Args           []string // Complete argv excluding argv[0]
	PrefixFlags    []string // Automatically injected flags (e.g. -B prefix)
	ConfigCFlags   []string // Flags from compiler config
	SeedCFlags     []string // Flags from the seed/LLM
	EffectiveFlags []string // Full flag list excluding source file and output path
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

	command, args, prefixFlags, effectiveFlags := c.buildCompileCommand(s, sourceFile, binaryPath)
	commandString := shellJoin(command, args)

	logger.Info("Compile seed %d compiler=%s", s.Meta.ID, command)
	logger.Info("Compile seed %d command=%s", s.Meta.ID, commandString)
	logger.Info("Compile seed %d prefix_flags=%v", s.Meta.ID, prefixFlags)
	logger.Info("Compile seed %d config_cflags=%v", s.Meta.ID, c.cflags)
	logger.Info("Compile seed %d seed_cflags=%v", s.Meta.ID, s.CFlags)
	logger.Info("Compile seed %d effective_flags=%v", s.Meta.ID, effectiveFlags)

	// Run GCC
	result, err := c.executor.Run(command, args...)
	if err != nil {
		return &CompileResult{
			BinaryPath:     binaryPath,
			Success:        false,
			Stdout:         "",
			Stderr:         fmt.Sprintf("failed to run compiler: %v", err),
			Command:        commandString,
			CompilerPath:   command,
			Args:           append([]string(nil), args...),
			PrefixFlags:    append([]string(nil), prefixFlags...),
			ConfigCFlags:   append([]string(nil), c.cflags...),
			SeedCFlags:     append([]string(nil), s.CFlags...),
			EffectiveFlags: append([]string(nil), effectiveFlags...),
		}, nil
	}

	success := result.ExitCode == 0

	return &CompileResult{
		BinaryPath:     binaryPath,
		Success:        success,
		Stdout:         result.Stdout,
		Stderr:         result.Stderr,
		Command:        commandString,
		CompilerPath:   command,
		Args:           append([]string(nil), args...),
		PrefixFlags:    append([]string(nil), prefixFlags...),
		ConfigCFlags:   append([]string(nil), c.cflags...),
		SeedCFlags:     append([]string(nil), s.CFlags...),
		EffectiveFlags: append([]string(nil), effectiveFlags...),
	}, nil
}

func (c *GCCCompiler) buildCompileCommand(s *seed.Seed, sourceFile, binaryPath string) (string, []string, []string, []string) {
	prefixFlags := make([]string, 0, 1)
	if c.prefixPath != "" {
		prefixFlags = append(prefixFlags, "-B"+c.prefixPath)
	}

	configFlags := append([]string(nil), c.cflags...)
	seedFlags := append([]string(nil), s.CFlags...)
	if len(seedFlags) > 0 {
		logger.Debug("Seed %d has CFlags from LLM: %v", s.Meta.ID, seedFlags)
	}

	effectiveFlags := make([]string, 0, len(prefixFlags)+len(configFlags)+len(seedFlags))
	effectiveFlags = append(effectiveFlags, prefixFlags...)
	effectiveFlags = append(effectiveFlags, configFlags...)
	effectiveFlags = append(effectiveFlags, seedFlags...)

	args := make([]string, 0, len(effectiveFlags)+3)
	args = append(args, effectiveFlags...)
	args = append(args, sourceFile, "-o", binaryPath)

	return c.gccPath, args, prefixFlags, effectiveFlags
}

// ToCompilationRecord converts a compile result into a seed-level record for persistence.
func (r *CompileResult) ToCompilationRecord(seedID uint64, sourcePath string) *seed.CompilationRecord {
	if r == nil {
		return nil
	}

	return &seed.CompilationRecord{
		SeedID:         seedID,
		RecordedAt:     time.Now(),
		SourcePath:     sourcePath,
		BinaryPath:     r.BinaryPath,
		Success:        r.Success,
		CompilerPath:   r.CompilerPath,
		Command:        r.Command,
		Args:           append([]string(nil), r.Args...),
		PrefixFlags:    append([]string(nil), r.PrefixFlags...),
		ConfigCFlags:   append([]string(nil), r.ConfigCFlags...),
		SeedCFlags:     append([]string(nil), r.SeedCFlags...),
		EffectiveFlags: append([]string(nil), r.EffectiveFlags...),
		Stdout:         r.Stdout,
		Stderr:         r.Stderr,
	}
}

func shellJoin(command string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, shellQuote(command))
	for _, arg := range args {
		parts = append(parts, shellQuote(arg))
	}
	return strings.Join(parts, " ")
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}

	safe := true
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			continue
		}
		switch r {
		case '/', '.', '_', '-', ':', '=', '+', ',':
			continue
		default:
			safe = false
		}
		if !safe {
			break
		}
	}
	if safe {
		return s
	}

	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
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
