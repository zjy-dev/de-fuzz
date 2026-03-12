package seed

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompilationRecordPersistence(t *testing.T) {
	seedDir := filepath.Join(t.TempDir(), "id-000001-src-000000-cov-00000-aaaaaaaa")
	record := &CompilationRecord{
		SeedID:         1,
		RecordedAt:     time.Now().UTC().Truncate(time.Second),
		SourcePath:     filepath.Join(seedDir, "source.c"),
		BinaryPath:     "/tmp/build/seed_1",
		Success:        true,
		CompilerPath:   "/usr/bin/gcc",
		Command:        "gcc source.c -o seed_1",
		Args:           []string{"source.c", "-o", "seed_1"},
		PrefixFlags:    []string{"-B/tmp/gcc"},
		ConfigCFlags:   []string{"-Wall", "-O0"},
		SeedCFlags:     []string{"-fstack-protector-all"},
		EffectiveFlags: []string{"-B/tmp/gcc", "-Wall", "-O0", "-fstack-protector-all"},
		Stdout:         "stdout",
		Stderr:         "stderr",
	}

	err := SaveCompilationRecord(seedDir, record)
	require.NoError(t, err)

	loaded, err := LoadCompilationRecord(seedDir)
	require.NoError(t, err)
	assert.Equal(t, record, loaded)
}

func TestGetCompilationRecordPath(t *testing.T) {
	seedDir := "/tmp/corpus/id-000001-src-000000-cov-00000-aaaaaaaa"
	assert.Equal(t, filepath.Join(seedDir, "compile_command.json"), GetCompilationRecordPath(seedDir))
}
