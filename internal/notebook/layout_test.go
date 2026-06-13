package notebook

import "testing"

func TestDefaultRoot(t *testing.T) {
	tests := []struct {
		home, profile, want string
	}{
		{"/Users/x", "", "/Users/x/attn-notebook"},
		{"/Users/x", "default", "/Users/x/attn-notebook"},
		{"/Users/x", "dev", "/Users/x/attn-notebook-dev"},
		{"/Users/x", "Agent7", "/Users/x/attn-notebook-agent7"},
		{"/Users/x", "  dev  ", "/Users/x/attn-notebook-dev"},
	}
	for _, tc := range tests {
		if got := DefaultRoot(tc.home, tc.profile); got != tc.want {
			t.Errorf("DefaultRoot(%q,%q) = %q, want %q", tc.home, tc.profile, got, tc.want)
		}
	}
}

func TestCleanPath(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"memory/decisions/foo.md", "memory/decisions/foo.md", false},
		{"/memory/decisions/foo.md", "memory/decisions/foo.md", false}, // root-absolute normalized
		{"  /index.md  ", "index.md", false},
		{"memory/./foo.md", "memory/foo.md", false},
		// escapes are neutralized to within the root, never outside
		{"../../etc/passwd.md", "etc/passwd.md", false},
		{"memory/../journal/x.md", "journal/x.md", false},
		// rejections
		{"", "", true},
		{"/", "", true},
		{"memory/foo.txt", "", true},     // not .md
		{"memory/foo", "", true},         // no extension
		{".attn/dreams/x.md", "", true},  // dotdir segment
		{"memory/.hidden.md", "", true},  // dotfile segment
		{"memory//foo.md", "memory/foo.md", false}, // doubled slash collapses to a valid path
	}
	for _, tc := range tests {
		got, err := CleanPath(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("CleanPath(%q) = %q, want error", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("CleanPath(%q) unexpected error: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("CleanPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
