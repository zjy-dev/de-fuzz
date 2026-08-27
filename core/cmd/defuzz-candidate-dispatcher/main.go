package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/zjy-dev/de-fuzz/internal/logger"
	_ "github.com/zjy-dev/de-fuzz/internal/oracle"
	"github.com/zjy-dev/de-fuzz/internal/service"
)

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	return runWithIO(args, os.Stdout, os.Stderr)
}

func runWithIO(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("defuzz-candidate-dispatcher", flag.ContinueOnError)
	flags.SetOutput(stderr)
	mode := flags.String("mode", "online", "online, verify, or catalog")
	compiler := flags.String("compiler", "", "trusted compiler family: gcc or llvm")
	candidatePath := flags.String("candidate-json", "", "path to raw candidate JSON")
	fingerprint := flags.String("candidate-fingerprint", "", "expected SHA-256 of raw candidate bytes")
	toolchainsPath := flags.String("toolchains", "configs/toolchains.yaml", "path to toolchains YAML")
	bundleManifest := flags.String("bundle-manifest", "", "optional checker bundle manifest provenance path")
	catalogPath := flags.String("catalog", "", "checker catalog referenced by the bundle manifest")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	logger.SetOutput(stderr)
	logger.SetColorEnable(false)

	dispatchMode := service.CandidateMode(*mode)
	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	if dispatchMode == service.CandidateModeCatalog {
		if err := encoder.Encode(service.RuntimeCheckerCatalog()); err != nil {
			fmt.Fprintf(stderr, "encode catalog: %v\n", err)
			return 2
		}
		return 0
	}

	response := service.CandidateDispatchResponse{
		CandidateFingerprint:       *fingerprint,
		EchoedCandidateFingerprint: *fingerprint,
		Verdict:                    "ERROR",
		Evidence:                   []string{},
		Results:                    []service.CandidateResult{},
		Builds:                     []service.CandidateBuild{},
		BundleManifest:             *bundleManifest,
	}
	fail := func(err error) int {
		response.Feedback = err.Error()
		_ = encoder.Encode(response)
		return 2
	}
	if dispatchMode != service.CandidateModeOnline && dispatchMode != service.CandidateModeVerify {
		return fail(fmt.Errorf("unsupported dispatcher mode %q", dispatchMode))
	}
	if *candidatePath == "" {
		return fail(fmt.Errorf("--candidate-json is required"))
	}
	if *compiler == "" {
		return fail(fmt.Errorf("--compiler is required"))
	}
	resolved, err := service.ResolveCandidatePath(*candidatePath)
	if err != nil {
		return fail(fmt.Errorf("candidate JSON path: %w", err))
	}
	raw, err := os.ReadFile(resolved)
	if err != nil {
		return fail(fmt.Errorf("read candidate JSON: %w", err))
	}
	toolchains, err := service.LoadToolchains(*toolchainsPath)
	if err != nil {
		return fail(err)
	}
	dispatcher := service.NewCandidateDispatcher(toolchains)
	if (*bundleManifest == "") != (*catalogPath == "") {
		return fail(fmt.Errorf("--bundle-manifest and --catalog must be provided together"))
	}
	if *bundleManifest != "" {
		allowlist, catalogErr := service.LoadBundleCatalog(*bundleManifest, *catalogPath)
		if catalogErr != nil {
			return fail(catalogErr)
		}
		dispatcher.CatalogAllowlist = allowlist
	}
	response, err = dispatcher.Dispatch(service.CandidateDispatchRequest{
		Mode: dispatchMode, CandidateJSON: raw, ExpectedFingerprint: *fingerprint,
		Compiler:       *compiler,
		BundleManifest: *bundleManifest,
	})
	if err != nil {
		response.Verdict = "ERROR"
		response.Feedback = err.Error()
	}
	if encodeErr := encoder.Encode(response); encodeErr != nil {
		fmt.Fprintf(stderr, "encode response: %v\n", encodeErr)
		return 2
	}
	return service.ExitCode(dispatchMode, response.Verdict, err != nil)
}
