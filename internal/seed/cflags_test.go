package seed

import (
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
