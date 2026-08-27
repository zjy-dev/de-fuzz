package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zjy-dev/de-fuzz/internal/service"
)

func TestRunRequiresTrustedCompiler(t *testing.T) {
	candidatePath, fingerprint := writeCandidateFixture(t, "gcc")
	toolchainsPath := writeToolchainsFixture(t)
	var stdout, stderr bytes.Buffer

	exitCode := runWithIO([]string{
		"--candidate-json", candidatePath,
		"--candidate-fingerprint", fingerprint,
		"--toolchains", toolchainsPath,
	}, &stdout, &stderr)

	assert.Equal(t, 2, exitCode)
	var response service.CandidateDispatchResponse
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &response))
	assert.Equal(t, "ERROR", response.Verdict)
	assert.Equal(t, "--compiler is required", response.Feedback)
}

func TestRunPassesTrustedCompilerToDispatcher(t *testing.T) {
	candidatePath, fingerprint := writeCandidateFixture(t, "clang")
	toolchainsPath := writeToolchainsFixture(t)
	var stdout, stderr bytes.Buffer

	exitCode := runWithIO([]string{
		"--compiler", "gcc",
		"--candidate-json", candidatePath,
		"--candidate-fingerprint", fingerprint,
		"--toolchains", toolchainsPath,
	}, &stdout, &stderr)

	assert.Equal(t, 2, exitCode)
	var response service.CandidateDispatchResponse
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &response))
	assert.Equal(t, "ERROR", response.Verdict)
	assert.Contains(t, response.Feedback, `candidate.toolchain "llvm" does not match trusted compiler "gcc"`)
}

func TestRunRejectsUnknownTrustedCompiler(t *testing.T) {
	candidatePath, fingerprint := writeCandidateFixture(t, "gcc")
	toolchainsPath := writeToolchainsFixture(t)
	var stdout, stderr bytes.Buffer

	exitCode := runWithIO([]string{
		"--compiler", "msvc",
		"--candidate-json", candidatePath,
		"--candidate-fingerprint", fingerprint,
		"--toolchains", toolchainsPath,
	}, &stdout, &stderr)

	assert.Equal(t, 2, exitCode)
	var response service.CandidateDispatchResponse
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &response))
	assert.Equal(t, "ERROR", response.Verdict)
	assert.Contains(t, response.Feedback, `trusted compiler: unknown compiler "msvc"`)
}

func writeCandidateFixture(t *testing.T, toolchain string) (string, string) {
	t.Helper()
	raw := []byte(fmt.Sprintf(`{"toolchain":%q,"mechanism":"ibt","isa":["x86_64"],"checker_ids":["INV-IBT-B01"],"minimal_trigger":{"source":"int x;","flags":["-c"]}}`, toolchain))
	path := filepath.Join(t.TempDir(), "candidate.json")
	require.NoError(t, os.WriteFile(path, raw, 0o600))
	return path, fmt.Sprintf("%x", sha256.Sum256(raw))
}

func writeToolchainsFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "toolchains.yaml")
	require.NoError(t, os.WriteFile(path, []byte("toolchains: {}\n"), 0o600))
	return path
}
