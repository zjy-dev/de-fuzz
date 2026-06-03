// Command fortify-repro drives the FORTIFY oracle
// (`internal/oracle.FortifyOracle`) directly, without going through the
// LLM / fuzz-loop / corpus manager. Its purpose is a focused, fast
// harness for any of the eight invariants screened in
// `docs/tech-docs/invariants/fortify-source.md` §6, and a working
// example of the "direct repro" pattern documented in
// `docs/tech-docs/guides/oracle-e2e-testing.md`.
//
// Usage (defaults target an x86_64 host GCC + the in-tree fortify
// initial seed):
//
//	go run ./cmd/fortify-repro
//
// Override `--source` to point at a different C trigger, `--cc` to use
// a specific GCC, or `--no-exec` to skip the dynamic checkers.
//
// Static checkers run unconditionally (they only need a compiled
// binary). Dynamic checkers (R01/R02/C01) run iff `--no-exec` is not
// passed; on a host that lacks an executor we fall back to NA.
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
	seedexec "github.com/zjy-dev/de-fuzz/internal/seed_executor"
)

const (
	defaultSource   = "initial_seeds/x64/fortify/id-000001-src-000000-cov-00000-fortify1/source.c"
	defaultTemplate = "initial_seeds/x64/fortify/function_template.c"
	defaultCC       = "/usr/bin/gcc"
)

// reproCFlags is the canonical flag set: -O2 makes glibc emit fortify
// wrappers; -D_FORTIFY_SOURCE=2 turns them on. The repro defaults to
// linking a full executable so the dynamic checkers can run; pass
// `--no-exec` to drop into static-only mode.
var reproCFlags = []string{
	"-O2",
	"-D_FORTIFY_SOURCE=2",
}

func main() {
	var (
		sourcePath   = flag.String("source", defaultSource, "Path to the C trigger source")
		compilerPath = flag.String("cc", defaultCC, "Path to the host x86 GCC")
		outDir       = flag.String("out", "", "Output dir for the compiled binary (defaults to a temp dir)")
		keep         = flag.Bool("keep", false, "Keep the compiled binary on exit")
		noExec       = flag.Bool("no-exec", false, "Skip dynamic checkers (static-only)")
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

	binDir, cleanup := makeOutDir(*outDir, *keep)
	defer cleanup()
	binPath := filepath.Join(binDir, "fortify_repro")

	flags := append([]string(nil), reproCFlags...)
	flags = append(flags, extraCFlags...)
	flags = append(flags, "-o", binPath, src)

	fmt.Println("$", cc, strings.Join(flags, " "))
	cmd := exec.Command(cc, flags...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		die("compilation failed: %v", err)
	}
	fmt.Printf("compiled %s -> %s\n", src, binPath)

	content, _ := os.ReadFile(src)
	s := &seed.Seed{
		Meta:    seed.Metadata{ID: 1, FilePath: src, ContentPath: src},
		Content: string(content),
	}

	o := &oracle.FortifyOracle{}
	ctx := &oracle.AnalyzeContext{BinaryPath: binPath}
	if !*noExec {
		ctx.Executor = seedexec.NewOracleExecutorAdapter(10)
	}

	fmt.Println("\n=== oracle: FortifyOracle.Analyze ===")
	bug, err := o.Analyze(s, ctx, nil)
	if err != nil {
		die("oracle returned error: %v", err)
	}
	if bug == nil {
		fmt.Println("verdict: NO BUG (all invariants Pass / NA)")
		return
	}

	fmt.Println("verdict: BUG DETECTED")
	fmt.Println(strings.Repeat("-", 72))
	fmt.Println(bug.Description)
	fmt.Println(strings.Repeat("-", 72))
}

func makeOutDir(out string, keep bool) (string, func()) {
	if out != "" {
		if err := os.MkdirAll(out, 0o755); err != nil {
			die("failed to create out dir: %v", err)
		}
		return out, func() {}
	}
	d, err := os.MkdirTemp("", "fortify-repro-*")
	if err != nil {
		die("failed to create temp dir: %v", err)
	}
	if keep {
		return d, func() { fmt.Printf("(kept binary at %s)\n", d) }
	}
	return d, func() { _ = os.RemoveAll(d) }
}

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
	fmt.Fprintf(os.Stderr, "fortify-repro: "+format+"\n", args...)
	os.Exit(1)
}
