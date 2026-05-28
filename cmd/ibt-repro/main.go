// Command ibt-repro drives the IBT oracle (`internal/oracle.IBTOracle`)
// directly, without going through the LLM / fuzz-loop / corpus manager.
// Its purpose is to provide a focused, fast harness for DREV-2026-004
// (the GCC `ix86_endbr_immediate_operand` shift-scan bypass) and to
// serve as a working example of the "direct repro" pattern documented in
// `@/home/yall/project/de-fuzz/docs/tech-docs/guides/oracle-e2e-testing.md`.
//
// Usage (defaults target the in-tree DREV-2026-004 trigger):
//
//	go run ./cmd/ibt-repro
//
// Override `--source` to point at a different C trigger, or `--cc` to
// use a specific GCC build (e.g. `/home/yall/opt/gcc-17-20260426/bin/gcc`,
// the runtime-verified-affected build per DREV-2026-004's timeline).
//
// The IBT oracle is purely static, so this driver does NOT require QEMU
// or any Executor — only a host x86_64 GCC that accepts
// `-fcf-protection=branch`.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/zjy-dev/de-fuzz/internal/oracle"
	"github.com/zjy-dev/de-fuzz/internal/seed"
)

const (
	defaultSource = "repro/x64/ibt_endbr_imm/source.c"
	defaultCC     = "/usr/bin/gcc"
)

// reproCFlags is the flag set that triggers the bug. `-O2` is the
// canonical optimisation level used by the upstream GCC test suite for
// `ix86_endbr_immediate_operand` regression coverage; `-c` keeps us at
// the relocatable object stage so we don't need a usable libc / linker
// configuration.
var reproCFlags = []string{
	"-O2",
	"-fcf-protection=branch",
	"-c",
}

func main() {
	var (
		sourcePath   = flag.String("source", defaultSource, "Path to the C trigger source")
		compilerPath = flag.String("cc", defaultCC, "Path to the host x86 GCC")
		outDir       = flag.String("out", "", "Output dir for the compiled .o (defaults to a temp dir)")
		keep         = flag.Bool("keep", false, "Keep the compiled object on exit")
		extraCFlags  arrayFlag
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

	objDir, cleanup := makeOutDir(*outDir, *keep)
	defer cleanup()
	objPath := filepath.Join(objDir, "ibt_repro.o")

	flags := append([]string(nil), reproCFlags...)
	flags = append(flags, extraCFlags...)
	flags = append(flags, "-o", objPath, src)

	fmt.Println("$", cc, strings.Join(flags, " "))
	cmd := exec.Command(cc, flags...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		die("compilation failed: %v", err)
	}
	fmt.Printf("compiled %s -> %s\n", src, objPath)

	content, _ := os.ReadFile(src)
	s := &seed.Seed{
		Meta:    seed.Metadata{ID: 1, FilePath: src, ContentPath: src},
		Content: string(content),
	}

	o := &oracle.IBTOracle{}
	ctx := &oracle.AnalyzeContext{BinaryPath: objPath}

	fmt.Println("\n=== oracle: IBTOracle.Analyze ===")
	bug, err := o.Analyze(s, ctx, nil)
	if err != nil {
		die("oracle returned error: %v", err)
	}
	if bug == nil {
		fmt.Println("verdict: NO BUG (all invariants Pass / NA)")
		fmt.Println("note: if you expected DREV-2026-004 to trigger, your GCC may have the upstream fix backported.")
		return
	}

	fmt.Println("verdict: BUG DETECTED")
	fmt.Println(strings.Repeat("-", 72))
	fmt.Println(bug.Description)
	fmt.Println(strings.Repeat("-", 72))
}

// makeOutDir picks a directory to drop the compiled object in. If --out
// is empty we mkdir in os.TempDir(); otherwise we use --out as-is. The
// returned cleanup is a no-op when --keep is true OR --out was provided.
func makeOutDir(out string, keep bool) (string, func()) {
	if out != "" {
		if err := os.MkdirAll(out, 0o755); err != nil {
			die("failed to create out dir: %v", err)
		}
		return out, func() {}
	}
	d, err := os.MkdirTemp("", "ibt-repro-*")
	if err != nil {
		die("failed to create temp dir: %v", err)
	}
	if keep {
		return d, func() { fmt.Printf("(kept object at %s)\n", d) }
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

func die(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "ibt-repro: "+format+"\n", args...)
	os.Exit(1)
}
