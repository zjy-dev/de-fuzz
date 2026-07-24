package seed

import (
	"testing"
)

func TestFindDefenseDisablingFlags_Canary(t *testing.T) {
	tests := []struct {
		name       string
		cflags     []string
		wantViolation bool
	}{
		{
			name:          "clean positive flags",
			cflags:        []string{"-fstack-protector-strong", "-O2"},
			wantViolation: false,
		},
		{
			name:          "fno-stack-protector",
			cflags:        []string{"-fno-stack-protector"},
			wantViolation: true,
		},
		{
			name:          "fno-stack-protector-all",
			cflags:        []string{"-O2", "-fno-stack-protector-all"},
			wantViolation: true,
		},
		{
			name:          "fno-stack-protector-strong",
			cflags:        []string{"-fno-stack-protector-strong"},
			wantViolation: true,
		},
		{
			name:          "fno-stack-protector-explicit",
			cflags:        []string{"-fno-stack-protector-explicit"},
			wantViolation: true,
		},
		{
			name:          "ssp-buffer-size=0 prefix",
			cflags:        []string{"--param=ssp-buffer-size=0"},
			wantViolation: true,
		},
		{
			name:          "nil cflags",
			cflags:        nil,
			wantViolation: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FindDefenseDisablingFlags("canary", tt.cflags)
			if (len(got) > 0) != tt.wantViolation {
				t.Errorf("FindDefenseDisablingFlags(canary, %v) = %v, wantViolation=%v",
					tt.cflags, got, tt.wantViolation)
			}
		})
	}
}

func TestFindDefenseDisablingFlags_IBT(t *testing.T) {
	tests := []struct {
		name          string
		cflags        []string
		wantViolation bool
	}{
		{
			name:          "clean ibt flags",
			cflags:        []string{"-fcf-protection=branch", "-O2"},
			wantViolation: false,
		},
		{
			name:          "fcf-protection=none",
			cflags:        []string{"-fcf-protection=none"},
			wantViolation: true,
		},
		{
			name:          "fno-cf-protection",
			cflags:        []string{"-fno-cf-protection"},
			wantViolation: true,
		},
		{
			name:          "mbranch-protection=none",
			cflags:        []string{"-mbranch-protection=none"},
			wantViolation: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FindDefenseDisablingFlags("ibt", tt.cflags)
			if (len(got) > 0) != tt.wantViolation {
				t.Errorf("FindDefenseDisablingFlags(ibt, %v) = %v, wantViolation=%v",
					tt.cflags, got, tt.wantViolation)
			}
		})
	}
}

func TestFindDefenseDisablingFlags_UnknownOracleType(t *testing.T) {
	got := FindDefenseDisablingFlags("unknown", []string{"-fno-stack-protector", "-fcf-protection=none"})
	if len(got) != 0 {
		t.Errorf("unknown oracle type must return empty, got %v", got)
	}
}
