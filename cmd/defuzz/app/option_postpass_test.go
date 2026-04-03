package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolveRunRootAndCorpusDirFromRunRoot(t *testing.T) {
	root := t.TempDir()
	corpusDir := filepath.Join(root, "corpus")
	require.NoError(t, os.MkdirAll(corpusDir, 0755))

	runRoot, resolvedCorpusDir, err := resolveRunRootAndCorpusDir(root)
	require.NoError(t, err)
	require.Equal(t, filepath.Clean(root), runRoot)
	require.Equal(t, filepath.Clean(corpusDir), resolvedCorpusDir)
}

func TestResolveRunRootAndCorpusDirFromCorpusDir(t *testing.T) {
	root := t.TempDir()
	corpusDir := filepath.Join(root, "corpus")
	require.NoError(t, os.MkdirAll(corpusDir, 0755))

	runRoot, resolvedCorpusDir, err := resolveRunRootAndCorpusDir(corpusDir)
	require.NoError(t, err)
	require.Equal(t, filepath.Clean(root), runRoot)
	require.Equal(t, filepath.Clean(corpusDir), resolvedCorpusDir)
}

func TestResolveRunRootAndCorpusDirRejectsInvalidDir(t *testing.T) {
	root := t.TempDir()

	_, _, err := resolveRunRootAndCorpusDir(root)
	require.Error(t, err)
}
