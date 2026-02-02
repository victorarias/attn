package pathutil

import "testing"

func TestMergePaths(t *testing.T) {
	tests := []struct {
		name      string
		primary   string
		secondary string
		want      string
	}{
		{
			name:      "empty paths",
			primary:   "",
			secondary: "",
			want:      "",
		},
		{
			name:      "primary only",
			primary:   "/usr/bin:/bin",
			secondary: "",
			want:      "/usr/bin:/bin",
		},
		{
			name:      "secondary only",
			primary:   "",
			secondary: "/usr/bin:/bin",
			want:      "/usr/bin:/bin",
		},
		{
			name:      "no duplicates",
			primary:   "/usr/bin:/bin",
			secondary: "/usr/local/bin:/opt/bin",
			want:      "/usr/bin:/bin:/usr/local/bin:/opt/bin",
		},
		{
			name:      "with duplicates",
			primary:   "/usr/bin:/bin:/usr/local/bin",
			secondary: "/usr/local/bin:/opt/bin:/bin",
			want:      "/usr/bin:/bin:/usr/local/bin:/opt/bin",
		},
		{
			name:      "empty segments ignored",
			primary:   "/usr/bin::/bin",
			secondary: ":/opt/bin:",
			want:      "/usr/bin:/bin:/opt/bin",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mergePaths(tt.primary, tt.secondary)
			if got != tt.want {
				t.Errorf("mergePaths(%q, %q) = %q, want %q", tt.primary, tt.secondary, got, tt.want)
			}
		})
	}
}
