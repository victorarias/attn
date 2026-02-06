package github

import "testing"

func TestParseGHVersionOutput(t *testing.T) {
	output := "gh version 2.81.0 (2025-01-01)\nhttps://github.com/cli/cli/releases\n"
	version, err := parseGHVersionOutput(output)
	if err != nil {
		t.Fatalf("parseGHVersionOutput error: %v", err)
	}
	if version != "2.81.0" {
		t.Fatalf("version = %q, want 2.81.0", version)
	}
}

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		a    string
		b    string
		want int
	}{
		{"2.81.0", "2.81.0", 0},
		{"2.81.1", "2.81.0", 1},
		{"2.80.9", "2.81.0", -1},
		{"2.9.0", "2.10.0", -1},
		{"2.10.0", "2.9.0", 1},
		{"2.81.0-rc1", "2.81.0", 0},
		{"v2.81.0", "2.81.0", 0},
	}
	for _, tt := range tests {
		got, err := compareVersions(tt.a, tt.b)
		if err != nil {
			t.Fatalf("compareVersions(%q,%q) error: %v", tt.a, tt.b, err)
		}
		if got != tt.want {
			t.Fatalf("compareVersions(%q,%q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}
