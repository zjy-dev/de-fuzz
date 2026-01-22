package seed

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseCFlagsFromResponse(t *testing.T) {
	tests := []struct {
		name     string
		response string
		want     []string
	}{
		{
			name: "basic cflags",
			response: `void seed(int buf_size, int fill_size) {
    char buffer[64];
}
// ||||| CFLAGS_START |||||
-fstack-protector-all
-O2
// ||||| CFLAGS_END |||||`,
			want: []string{"-fstack-protector-all", "-O2"},
		},
		{
			name: "no cflags section",
			response: `void seed(int buf_size, int fill_size) {
    char buffer[64];
}`,
			want: nil,
		},
		{
			name: "empty cflags section",
			response: `void seed() {}
// ||||| CFLAGS_START |||||
// ||||| CFLAGS_END |||||`,
			want: nil,
		},
		{
			name: "cflags with comments",
			response: `void seed() {}
// ||||| CFLAGS_START |||||
# This is a comment
-fstack-protector-all
// another comment
-O3
// ||||| CFLAGS_END |||||`,
			want: []string{"-fstack-protector-all", "-O3"},
		},
		{
			name: "cflags with empty lines",
			response: `void seed() {}
// ||||| CFLAGS_START |||||

-fstack-protector

-fPIC

// ||||| CFLAGS_END |||||`,
			want: []string{"-fstack-protector", "-fPIC"},
		},
		{
			name: "malformed - no end marker",
			response: `void seed() {}
// ||||| CFLAGS_START |||||
-fstack-protector-all`,
			want: nil,
		},
		{
			name: "single flag",
			response: `code
// ||||| CFLAGS_START |||||
-fno-stack-protector
// ||||| CFLAGS_END |||||`,
			want: []string{"-fno-stack-protector"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseCFlagsFromResponse(tt.response)
			if len(got) != len(tt.want) {
				t.Errorf("ParseCFlagsFromResponse() = %v, want %v", got, tt.want)
				return
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("ParseCFlagsFromResponse()[%d] = %v, want %v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestExtractCodeWithoutCFlags(t *testing.T) {
	tests := []struct {
		name     string
		response string
		want     string
	}{
		{
			name: "remove cflags section",
			response: `void seed() {
}
// ||||| CFLAGS_START |||||
-fstack-protector-all
// ||||| CFLAGS_END |||||
// ||||| JSON_TESTCASES_START |||||`,
			want: `void seed() {
}
// ||||| JSON_TESTCASES_START |||||`,
		},
		{
			name: "no cflags section",
			response: `void seed() {
}`,
			want: `void seed() {
}`,
		},
		{
			name: "cflags at end",
			response: `void seed() {}
// ||||| CFLAGS_START |||||
-O2
// ||||| CFLAGS_END |||||`,
			want: `void seed() {}
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractCodeWithoutCFlags(tt.response)
			if got != tt.want {
				t.Errorf("ExtractCodeWithoutCFlags() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCFlagsPersistence(t *testing.T) {
	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "cflags_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a seed with CFlags
	seed := &Seed{
		Meta: Metadata{
			ID:       1,
			ParentID: 0,
		},
		Content: "void seed() {}",
		CFlags:  []string{"-fstack-protector-all", "-O2"},
	}

	// Save the seed
	namer := NewDefaultNamingStrategy()
	dirName, err := SaveSeedWithMetadata(tmpDir, seed, namer)
	if err != nil {
		t.Fatalf("Failed to save seed: %v", err)
	}

	// Verify cflags.json was created
	cflagsFile := filepath.Join(tmpDir, dirName, "cflags.json")
	if _, err := os.Stat(cflagsFile); os.IsNotExist(err) {
		t.Errorf("cflags.json was not created")
	}

	// Load the seed back
	seedDir := filepath.Join(tmpDir, dirName)
	loadedSeed, err := LoadSeedWithMetadata(seedDir, namer)
	if err != nil {
		t.Fatalf("Failed to load seed: %v", err)
	}

	// Verify CFlags were loaded correctly
	if len(loadedSeed.CFlags) != 2 {
		t.Errorf("Expected 2 CFlags, got %d", len(loadedSeed.CFlags))
	}
	if loadedSeed.CFlags[0] != "-fstack-protector-all" {
		t.Errorf("Expected first cflag to be '-fstack-protector-all', got '%s'", loadedSeed.CFlags[0])
	}
	if loadedSeed.CFlags[1] != "-O2" {
		t.Errorf("Expected second cflag to be '-O2', got '%s'", loadedSeed.CFlags[1])
	}
}

func TestCFlagsPersistence_EmptyCFlags(t *testing.T) {
	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "cflags_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a seed without CFlags
	seed := &Seed{
		Meta: Metadata{
			ID:       1,
			ParentID: 0,
		},
		Content: "void seed() {}",
		CFlags:  nil, // No CFlags
	}

	// Save the seed
	namer := NewDefaultNamingStrategy()
	dirName, err := SaveSeedWithMetadata(tmpDir, seed, namer)
	if err != nil {
		t.Fatalf("Failed to save seed: %v", err)
	}

	// Verify cflags.json was NOT created (empty CFlags)
	cflagsFile := filepath.Join(tmpDir, dirName, "cflags.json")
	if _, err := os.Stat(cflagsFile); !os.IsNotExist(err) {
		t.Errorf("cflags.json should not be created for empty CFlags")
	}

	// Load the seed back - should have nil/empty CFlags
	seedDir := filepath.Join(tmpDir, dirName)
	loadedSeed, err := LoadSeedWithMetadata(seedDir, namer)
	if err != nil {
		t.Fatalf("Failed to load seed: %v", err)
	}

	if len(loadedSeed.CFlags) != 0 {
		t.Errorf("Expected 0 CFlags, got %d", len(loadedSeed.CFlags))
	}
}
