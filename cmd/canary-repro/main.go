// Command canary-repro drives the canary oracle (`internal/oracle.CanaryOracle`)
// directly, without going through the LLM / fuzz-loop / corpus manager. It is
// intended as a focused harness for the LoongArch64 stack-canary leak case
// (INV-SP-R03 / DREV-2026-001), but it is generic enough to point at any
// freestanding (or glibc-linked) seed that follows the canary template's
// argv contract:
//
//	./prog scrub          - emits CANARY_SCRUB_OK, GUARD_LEAKED, or CANARY_SCRUB_NA
//	./prog <bs> <fs>      - allocates `bs`-byte buffer, memsets `fs` bytes
//
// Usage (LoongArch64 default paths baked in):
//
//	go run ./cmd/canary-repro
//
// The default flags target the freestanding repro at
// `repro/loongarch64/canary_leak/source.c` compiled with the cross-toolchain
// at `target_compilers/gcc-v16.1.0-loongarch64-cross-compile/...`. Override
// any of `--source`, `--cc`, `--qemu` to point at a different setup.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/zjy-dev/de-fuzz/internal/oracle"
	"github.com/zjy-dev/de-fuzz/internal/seed"
	executor "github.com/zjy-dev/de-fuzz/internal/seed_executor"
)

const (
	defaultSource = "repro/loongarch64/canary_leak/source.c"
	defaultCC     = "/home/yall/project/de-fuzz/target_compilers/" +
		"gcc-v16.1.0-loongarch64-cross-compile/" +
		"install-loongarch64-unknown-linux-gnu/bin/" +
		"loongarch64-unknown-linux-gnu-gcc-16.1.0"
	defaultQEMU = "qemu-loongarch64"
)

// freestandingCFlags is the minimal flag set that lets us link the repro
// without a usable glibc — we provide our own __stack_chk_guard,
// __stack_chk_fail, memset, and _start. -fstack-protector-strong is left on
// so the seed function gets the canary instrumentation we are testing.
var freestandingCFlags = []string{
	"-nostdlib",
	"-nostartfiles",
	"-ffreestanding",
	"-static",
	"-no-pie",
	"-O0",
	"-fstack-protector-strong",
	"-fno-asynchronous-unwind-tables",
	"-fno-unwind-tables",
}

func main() {
	var (
		sourcePath   = flag.String("source", defaultSource, "Path to the seed C source file")
		compilerPath = flag.String("cc", defaultCC, "Path to the LoongArch64 cross-compiler (gcc)")
		qemuPath     = flag.String("qemu", defaultQEMU, "Path or name of qemu-loongarch64 binary")
		sysrootPath  = flag.String("sysroot", "", "QEMU -L sysroot (leave empty for static binaries)")
		outDir       = flag.String("out", "", "Output dir for the compiled binary (defaults to a temp dir)")
		timeoutSec   = flag.Int("timeout", 30, "Per-execution timeout in seconds")
		maxBufSize   = flag.Int("max-buf", 1024, "CanaryOracle.MaxBufferSize for the binary search")
		bufSize      = flag.Int("buf-size", 64, "CanaryOracle.DefaultBufSize (passed to seed as argv[1])")
		extraCFlags  arrayFlag
		freestanding = flag.Bool("freestanding", true, "Use freestanding flags (-nostdlib -static ...)")
		keep         = flag.Bool("keep", false, "Keep the compiled binary on exit")
		showProbes   = flag.Bool("probes", true, "Run smoke probes (scrub, safe, overflow) before the oracle")
	)
	flag.Var(&extraCFlags, "cflag", "Extra compiler flag (repeatable)")
	flag.Parse()

	src := absOrDie(*sourcePath)
	cc := absOrDie(*compilerPath)
	if _, err := os.Stat(src); err != nil {
		die("source not found: %v", err)
	}
	if _, err := os.Stat(cc); err != nil {
		die("compiler not found: %v", err)
	}
	if _, err := exec.LookPath(*qemuPath); err != nil {
		die("qemu not found in PATH: %v", err)
	}

	// 1) Compile the seed.
	binDir, cleanup := makeBinDir(*outDir, *keep)
	defer cleanup()
	binaryPath := filepath.Join(binDir, "canary_leak")
	if err := compileSeed(cc, src, binaryPath, *freestanding, extraCFlags); err != nil {
		die("compilation failed: %v", err)
	}
	fmt.Printf("compiled %s -> %s\n", src, binaryPath)

	// 2) Optional smoke probes — useful as a quick sanity check before
	// invoking the oracle. Output goes straight to stdout/stderr so the
	// user sees the raw GUARD_LEAKED / SEED_RETURNED / canary-fail lines.
	if *showProbes {
		runProbes(*qemuPath, *sysrootPath, binaryPath, *bufSize)
	}

	// 3) Build the oracle execution context. The QEMU adapter implements
	// `oracle.Executor`, satisfying the contract that
	// `CanaryOracle.Analyze` requires (see `internal/oracle/canary_oracle.go`).
	adapter := executor.NewQEMUOracleExecutorAdapter(*qemuPath, *sysrootPath, *timeoutSec)
	canary := &oracle.CanaryOracle{
		MaxBufferSize:  *maxBufSize,
		DefaultBufSize: *bufSize,
	}

	content, _ := os.ReadFile(src)
	s := &seed.Seed{
		Meta:    seed.Metadata{ID: 1, FilePath: src, ContentPath: src},
		Content: string(content),
	}

	fmt.Println("\n=== oracle: CanaryOracle.Analyze ===")
	bug, err := canary.Analyze(s, &oracle.AnalyzeContext{
		BinaryPath: binaryPath,
		Executor:   adapter,
	}, nil)
	if err != nil {
		die("oracle returned error: %v", err)
	}
	if bug == nil {
		fmt.Println("oracle verdict: NO BUG (all invariants Pass / NA)")
		return
	}

	fmt.Println("oracle verdict: BUG DETECTED")
	fmt.Println(strings.Repeat("-", 72))
	fmt.Println(bug.Description)
	fmt.Println(strings.Repeat("-", 72))
}

// runProbes runs the seed in scrub mode and two binary-search points and
// prints the raw output. This is independent of the oracle and only there
// to give the operator a quick sanity check.
func runProbes(qemu, sysroot, binary string, bufSize int) {
	probes := []struct {
		label string
		args  []string
	}{
		{"scrub (INV-SP-R03)", []string{"scrub"}},
		{fmt.Sprintf("safe   (%d, %d)", bufSize, bufSize/2), []string{itoa(bufSize), itoa(bufSize / 2)}},
		{fmt.Sprintf("smash  (%d, %d)", bufSize, bufSize*4), []string{itoa(bufSize), itoa(bufSize * 4)}},
	}
	fmt.Println("=== smoke probes ===")
	for _, p := range probes {
		fmt.Printf("--- %s ---\n", p.label)
		args := []string{}
		if sysroot != "" {
			args = append(args, "-L", sysroot)
		}
		args = append(args, binary)
		args = append(args, p.args...)
		cmd := exec.Command(qemu, args...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		err := cmd.Run()
		fmt.Printf("(exit=%d, err=%v)\n", cmd.ProcessState.ExitCode(), err)
	}
}

// compileSeed invokes the cross-compiler with the appropriate flag set.
// In freestanding mode we use a fixed flag list; otherwise we let the
// caller drive the compile via --cflag.
func compileSeed(cc, src, dst string, freestanding bool, extra []string) error {
	args := []string{}
	if freestanding {
		args = append(args, freestandingCFlags...)
	}
	args = append(args, extra...)
	args = append(args, "-o", dst, src)

	fmt.Println("$", cc, strings.Join(args, " "))
	cmd := exec.Command(cc, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// makeBinDir picks a directory to drop the compiled binary in. If --out is
// empty we mkdir in os.TempDir(); otherwise we use --out as-is. The
// returned cleanup is a no-op when --keep is true OR --out was provided.
func makeBinDir(out string, keep bool) (string, func()) {
	if out != "" {
		if err := os.MkdirAll(out, 0o755); err != nil {
			die("failed to create out dir: %v", err)
		}
		return out, func() {}
	}
	d, err := os.MkdirTemp("", "canary-repro-*")
	if err != nil {
		die("failed to create temp dir: %v", err)
	}
	if keep {
		return d, func() { fmt.Printf("(kept binary at %s)\n", d) }
	}
	return d, func() { _ = os.RemoveAll(d) }
}

// arrayFlag is a `flag.Value` that accumulates string values across
// repeated `--cflag x --cflag y` invocations.
type arrayFlag []string

func (a *arrayFlag) String() string     { return strings.Join(*a, " ") }
func (a *arrayFlag) Set(v string) error { *a = append(*a, v); return nil }

func absOrDie(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		die("failed to resolve path %q: %v", p, err)
	}
	return abs
}

func itoa(n int) string { return fmt.Sprintf("%d", n) }

func die(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "canary-repro: "+format+"\n", args...)
	os.Exit(1)
}

// drain is a small helper that copies an io.Reader to the given writer,
// useful when a future caller wants to capture compile output instead of
// streaming it to the parent stdout. Currently unused but kept here so
// `go vet` doesn't complain about an imported but unused `io` package.
var _ = io.Copy
