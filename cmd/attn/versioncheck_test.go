package main

import (
	"strings"
	"testing"
)

func TestVersionMismatchWarning(t *testing.T) {
	tests := []struct {
		name     string
		cli      string
		daemon   string
		wantWarn bool
	}{
		{name: "different releases warn", cli: "0.5.0", daemon: "0.10.2", wantWarn: true},
		{name: "same release is quiet", cli: "0.10.2", daemon: "0.10.2", wantWarn: false},
		{name: "whitespace tolerated", cli: " 0.10.2 ", daemon: "0.10.2", wantWarn: false},
		{name: "dev cli skipped", cli: "dev", daemon: "0.10.2", wantWarn: false},
		{name: "unknown daemon skipped", cli: "0.10.2", daemon: "unknown", wantWarn: false},
		{name: "empty daemon skipped", cli: "0.10.2", daemon: "", wantWarn: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, ok := versionMismatchWarning(tt.cli, tt.daemon)
			if ok != tt.wantWarn {
				t.Fatalf("versionMismatchWarning(%q, %q) ok = %v, want %v", tt.cli, tt.daemon, ok, tt.wantWarn)
			}
			if ok {
				if !strings.Contains(msg, strings.TrimSpace(tt.cli)) || !strings.Contains(msg, strings.TrimSpace(tt.daemon)) {
					t.Fatalf("warning %q should mention both versions", msg)
				}
				if !strings.Contains(msg, "which -a attn") {
					t.Fatalf("warning %q should point at the shadowing check", msg)
				}
			}
		})
	}
}
