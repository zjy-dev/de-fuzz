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
	BinaryPath        string            // Path to the compiled binary
	Success           bool              // Whether compilation succeeded
	Stdout            string            // Compiler stdout
	Stderr            string            // Compiler stderr (warnings, errors)
	Command           string            // Shell-safe command string for reproduction
	CompilerPath      string            // Compiler executable path
	Args              []string          // Complete argv excluding argv[0]
	PrefixFlags       []string          // Automatically injected flags (e.g. -B prefix)
	ConfigCFlags      []string          // Flags from compiler config
	ProfileName       string            // Selected deterministic flag profile name
	ProfileFlags      []string          // Flags from the selected profile
	ProfileAxes       map[string]string // Axis/value mapping for the selected profile
	IsNegativeControl bool              // Whether the selected profile is a negative control
	SeedCFlags        []string          // Flags requested by the seed/LLM
	AppliedLLMCFlags  []string          // LLM flags that survived conflict filtering
	DroppedLLMCFlags  []string          // LLM flags dropped due to profile conflicts
	LLMCFlagsApplied  bool              // Whether seed-provided flags were applied
	EffectiveFlags    []string          // Full flag list excluding source file and output path
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
	allowLLM   bool     // Whether LLM-provided seed flags are applied
}

// GCCCompilerConfig holds the configuration for GCCCompiler.
type GCCCompilerConfig struct {
	GCCPath          string   // Path to GCC executable
	WorkDir          string   // Working directory
	PrefixPath       string   // -B prefix path for finding compiler components (cc1, as, ld)
	CFlags           []string // Additional compiler flags as a slice
	DisableLLMCFlags bool     // Disable LLM-provided seed flags for deterministic strategy profiles
}

// NewGCCCompiler creates a new GCC compiler.
func NewGCCCompiler(cfg GCCCompilerConfig) *GCCCompiler {
	return &GCCCompiler{
		executor:   exec.NewCommandExecutor(),
		gccPath:    cfg.GCCPath,
		workDir:    cfg.WorkDir,
		prefixPath: cfg.PrefixPath,
		cflags:     cfg.CFlags,
		allowLLM:   !cfg.DisableLLMCFlags,
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

	command, args, prefixFlags, effectiveFlags, appliedLLMCFlags, droppedLLMCFlags := c.buildCompileCommand(s, sourceFile, binaryPath)
	commandString := shellJoin(command, args)

	logger.Info("Compile seed %d compiler=%s", s.Meta.ID, command)
	logger.Info("Compile seed %d command=%s", s.Meta.ID, commandString)
	logger.Info("Compile seed %d prefix_flags=%v", s.Meta.ID, prefixFlags)
	logger.Info("Compile seed %d config_cflags=%v", s.Meta.ID, c.cflags)
	logger.Info("Compile seed %d profile=%s profile_flags=%v", s.Meta.ID, profileName(s.FlagProfile), profileFlags(s.FlagProfile))
	logger.Info("Compile seed %d seed_cflags=%v", s.Meta.ID, s.CFlags)
	logger.Info("Compile seed %d applied_llm_cflags=%v dropped_llm_cflags=%v", s.Meta.ID, appliedLLMCFlags, droppedLLMCFlags)
	logger.Info("Compile seed %d llm_cflags_applied=%t", s.Meta.ID, s.LLMCFlagsApplied)
	logger.Info("Compile seed %d effective_flags=%v", s.Meta.ID, effectiveFlags)

	// Run GCC
	result, err := c.executor.Run(command, args...)
	if err != nil {
		return &CompileResult{
			BinaryPath:        binaryPath,
			Success:           false,
			Stdout:            "",
			Stderr:            fmt.Sprintf("failed to run compiler: %v", err),
			Command:           commandString,
			CompilerPath:      command,
			Args:              append([]string(nil), args...),
			PrefixFlags:       append([]string(nil), prefixFlags...),
			ConfigCFlags:      append([]string(nil), c.cflags...),
			ProfileName:       profileName(s.FlagProfile),
			ProfileFlags:      profileFlags(s.FlagProfile),
			ProfileAxes:       profileAxes(s.FlagProfile),
			IsNegativeControl: isNegativeProfile(s.FlagProfile),
			SeedCFlags:        append([]string(nil), s.CFlags...),
			AppliedLLMCFlags:  append([]string(nil), appliedLLMCFlags...),
			DroppedLLMCFlags:  append([]string(nil), droppedLLMCFlags...),
			LLMCFlagsApplied:  s.LLMCFlagsApplied,
			EffectiveFlags:    append([]string(nil), effectiveFlags...),
		}, nil
	}

	success := result.ExitCode == 0

	return &CompileResult{
		BinaryPath:        binaryPath,
		Success:           success,
		Stdout:            result.Stdout,
		Stderr:            result.Stderr,
		Command:           commandString,
		CompilerPath:      command,
		Args:              append([]string(nil), args...),
		PrefixFlags:       append([]string(nil), prefixFlags...),
		ConfigCFlags:      append([]string(nil), c.cflags...),
		ProfileName:       profileName(s.FlagProfile),
		ProfileFlags:      profileFlags(s.FlagProfile),
		ProfileAxes:       profileAxes(s.FlagProfile),
		IsNegativeControl: isNegativeProfile(s.FlagProfile),
		SeedCFlags:        append([]string(nil), s.CFlags...),
		AppliedLLMCFlags:  append([]string(nil), appliedLLMCFlags...),
		DroppedLLMCFlags:  append([]string(nil), droppedLLMCFlags...),
		LLMCFlagsApplied:  s.LLMCFlagsApplied,
		EffectiveFlags:    append([]string(nil), effectiveFlags...),
	}, nil
}

func (c *GCCCompiler) buildCompileCommand(s *seed.Seed, sourceFile, binaryPath string) (string, []string, []string, []string, []string, []string) {
	prefixFlags := make([]string, 0, 1)
	if c.prefixPath != "" {
		prefixFlags = append(prefixFlags, "-B"+c.prefixPath)
	}

	configFlags := append([]string(nil), c.cflags...)
	profileFlags := profileFlags(s.FlagProfile)
	seedFlags := append([]string(nil), s.CFlags...)
	if len(seedFlags) > 0 {
		logger.Debug("Seed %d has CFlags from LLM: %v", s.Meta.ID, seedFlags)
	}
	appliedLLMCFlags := []string(nil)
	droppedLLMCFlags := []string(nil)
	if c.allowLLM {
		appliedLLMCFlags, droppedLLMCFlags = filterLLMCFlags(seedFlags, s.FlagProfile)
	} else if len(seedFlags) > 0 {
		droppedLLMCFlags = append([]string(nil), seedFlags...)
	}
	s.AppliedLLMCFlags = append([]string(nil), appliedLLMCFlags...)
	s.DroppedLLMCFlags = append([]string(nil), droppedLLMCFlags...)
	s.LLMCFlagsApplied = c.allowLLM && len(appliedLLMCFlags) > 0

	effectiveFlags := make([]string, 0, len(prefixFlags)+len(configFlags)+len(profileFlags)+len(appliedLLMCFlags))
	effectiveFlags = append(effectiveFlags, prefixFlags...)
	effectiveFlags = append(effectiveFlags, configFlags...)
	effectiveFlags = append(effectiveFlags, profileFlags...)
	if c.allowLLM {
		effectiveFlags = append(effectiveFlags, appliedLLMCFlags...)
	}

	args := make([]string, 0, len(effectiveFlags)+3)
	args = append(args, effectiveFlags...)
	args = append(args, sourceFile, "-o", binaryPath)

	return c.gccPath, args, prefixFlags, effectiveFlags, appliedLLMCFlags, droppedLLMCFlags
}

// ToCompilationRecord converts a compile result into a seed-level record for persistence.
func (r *CompileResult) ToCompilationRecord(seedID uint64, sourcePath string) *seed.CompilationRecord {
	if r == nil {
		return nil
	}

	return &seed.CompilationRecord{
		SeedID:            seedID,
		RecordedAt:        time.Now(),
		SourcePath:        sourcePath,
		BinaryPath:        r.BinaryPath,
		Success:           r.Success,
		CompilerPath:      r.CompilerPath,
		Command:           r.Command,
		Args:              append([]string(nil), r.Args...),
		PrefixFlags:       append([]string(nil), r.PrefixFlags...),
		ConfigCFlags:      append([]string(nil), r.ConfigCFlags...),
		ProfileName:       r.ProfileName,
		ProfileFlags:      append([]string(nil), r.ProfileFlags...),
		ProfileAxes:       cloneAxes(r.ProfileAxes),
		IsNegativeControl: r.IsNegativeControl,
		SeedCFlags:        append([]string(nil), r.SeedCFlags...),
		AppliedLLMCFlags:  append([]string(nil), r.AppliedLLMCFlags...),
		DroppedLLMCFlags:  append([]string(nil), r.DroppedLLMCFlags...),
		LLMCFlags:         append([]string(nil), r.SeedCFlags...),
		LLMCFlagsApplied:  r.LLMCFlagsApplied,
		EffectiveFlags:    append([]string(nil), r.EffectiveFlags...),
		Stdout:            r.Stdout,
		Stderr:            r.Stderr,
	}
}

func profileName(profile *seed.FlagProfile) string {
	if profile == nil {
		return ""
	}
	return profile.Name
}

func profileFlags(profile *seed.FlagProfile) []string {
	if profile == nil {
		return nil
	}
	return append([]string(nil), profile.Flags...)
}

func profileAxes(profile *seed.FlagProfile) map[string]string {
	if profile == nil || len(profile.AxisValues) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(profile.AxisValues))
	for key, value := range profile.AxisValues {
		cloned[key] = value
	}
	return cloned
}

func isNegativeProfile(profile *seed.FlagProfile) bool {
	return profile != nil && profile.IsNegativeControl
}

func cloneAxes(axes map[string]string) map[string]string {
	if len(axes) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(axes))
	for key, value := range axes {
		cloned[key] = value
	}
	return cloned
}

var canaryLLMConflictPrefixes = []string{
	"-fstack-protector",
	"-fno-stack-protector",
	"--param=ssp-buffer-size=",
	"-mstack-protector-guard",
}

var canaryLLMConflictFlags = []string{
	"-fpic",
	"-fPIC",
	"-fpie",
	"-fPIE",
	"-fhardened",
}

func filterLLMCFlags(seedFlags []string, profile *seed.FlagProfile) ([]string, []string) {
	if len(seedFlags) == 0 {
		return nil, nil
	}

	applied := make([]string, 0, len(seedFlags))
	dropped := make([]string, 0)
	for _, flag := range seedFlags {
		if shouldDropLLMFlag(flag, profile) {
			dropped = append(dropped, flag)
			continue
		}
		applied = append(applied, flag)
	}
	return applied, dropped
}

func shouldDropLLMFlag(flag string, profile *seed.FlagProfile) bool {
	if profile == nil || len(profile.AxisValues) == 0 {
		return false
	}

	for _, blocked := range canaryLLMConflictFlags {
		if flag == blocked {
			return true
		}
	}
	for _, prefix := range canaryLLMConflictPrefixes {
		if strings.HasPrefix(flag, prefix) {
			return true
		}
	}
	return false
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
