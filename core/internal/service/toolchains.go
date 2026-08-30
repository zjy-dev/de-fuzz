package service

import (
	"bytes"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"

	"github.com/zjy-dev/de-fuzz/internal/oracle"
	executor "github.com/zjy-dev/de-fuzz/internal/seed_executor"
	"gopkg.in/yaml.v3"
)

// Toolchain describes how to build and run binaries for a single ISA.
//
// The ISA→toolchain mapping is config-driven (decision: configs/toolchains.yaml)
// so the deterministic build node never hard-codes cross-compiler paths. A
// missing ISA is not a crash: BuildService emits an error cell (R8).
type Toolchain struct {
	// GCCPath is the (cross-)gcc executable for this ISA.
	// It remains the default used by the legacy BuildService and MCP paths.
	GCCPath string `yaml:"gcc_path"`
	// ClangPath is the (cross-)clang executable for this ISA. Candidate
	// dispatch selects it only when the trusted compiler family is LLVM.
	ClangPath string `yaml:"clang_path"`
	// Prefix is the -B prefix path for compiler components (cc1, as, ld).
	Prefix string `yaml:"prefix"`
	// DriverArgs are trusted wrapper arguments inserted before compiler flags.
	// This supports containerized compiler drivers on non-Linux hosts.
	DriverArgs []string `yaml:"driver_args"`
	// Sysroot is the --sysroot for cross-compilation; empty for native.
	Sysroot string `yaml:"sysroot"`
	// CFlags are mechanism/ISA flags injected into every compile (e.g.
	// -fstack-protector-strong, -O0).
	CFlags []string `yaml:"cflags"`
	// QEMUPath is the user-mode emulator for dynamic checkers on non-native
	// ISAs. It may also name a trusted wrapper that invokes QEMU in a
	// container. A non-native toolchain without QEMUPath is invalid whenever
	// a dynamic checker needs to execute the target binary.
	QEMUPath string `yaml:"qemu_path"`
	// QEMUArgs are trusted arguments inserted before -L and the target binary.
	// They are useful for settings such as `-cpu max`.
	QEMUArgs []string `yaml:"qemu_args"`
	// QEMUSysroot is the -L sysroot passed to QEMU.
	QEMUSysroot string `yaml:"qemu_sysroot"`
	// Native explicitly marks a target whose binaries run directly on the host.
	Native bool `yaml:"native"`
}

// oracleExecutor returns the execution backend for a dynamic checker. It is
// deliberately fail-closed: a cross-ISA binary must never be executed
// directly just because qemu_path was omitted from configuration.
func (t Toolchain) oracleExecutor(timeoutSec int) (oracle.Executor, string, error) {
	if t.Native {
		return executor.NewOracleExecutorAdapter(timeoutSec), "", nil
	}
	if strings.TrimSpace(t.QEMUPath) == "" {
		return nil, "", fmt.Errorf("non-native toolchain requires qemu_path for dynamic checkers")
	}
	resolved, err := osexec.LookPath(t.QEMUPath)
	if err != nil {
		return nil, "", fmt.Errorf("resolve qemu_path %q: %w", t.QEMUPath, err)
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return nil, "", fmt.Errorf("resolve absolute qemu_path %q: %w", t.QEMUPath, err)
	}
	resolved, err = filepath.EvalSymlinks(resolved)
	if err != nil {
		return nil, "", fmt.Errorf("resolve qemu_path symlinks %q: %w", t.QEMUPath, err)
	}
	return executor.NewQEMUOracleExecutorAdapterWithArgs(
		resolved, t.QEMUSysroot, t.QEMUArgs, timeoutSec,
	), resolved, nil
}

// Toolchains is the parsed configs/toolchains.yaml document, keyed by ISA.
type Toolchains struct {
	Toolchains map[string]Toolchain `yaml:"toolchains"`
}

// Lookup returns the toolchain for an ISA, if configured.
func (t *Toolchains) Lookup(isa string) (Toolchain, bool) {
	if t == nil {
		return Toolchain{}, false
	}
	tc, ok := t.Toolchains[isa]
	return tc, ok
}

// LoadToolchains parses an ISA→toolchain YAML config from path.
func LoadToolchains(path string) (*Toolchains, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read toolchains config %s: %w", path, err)
	}
	var tc Toolchains
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&tc); err != nil {
		return nil, fmt.Errorf("parse toolchains config %s: %w", path, err)
	}
	if tc.Toolchains == nil {
		tc.Toolchains = map[string]Toolchain{}
	}
	return &tc, nil
}
