package config

import (
	"bytes"
	"strings"
	"testing"
)

func TestPrintProfileBanner_NoopForDefault(t *testing.T) {
	t.Setenv("ATTN_PROFILE", "")
	var buf bytes.Buffer
	PrintProfileBanner(&buf)
	if buf.Len() != 0 {
		t.Errorf("expected no output for default profile, got %q", buf.String())
	}
}

func TestPrintProfileBanner_MentionsProfileSocketAndPort(t *testing.T) {
	t.Setenv("ATTN_PROFILE", "dev")
	// Let ATTN_WS_PORT fall through to the profile default.
	t.Setenv("ATTN_WS_PORT", "")
	var buf bytes.Buffer
	PrintProfileBanner(&buf)
	got := buf.String()
	if !strings.Contains(got, "profile=dev") {
		t.Errorf("banner missing profile= field: %q", got)
	}
	if !strings.Contains(got, "socket=") {
		t.Errorf("banner missing socket= field: %q", got)
	}
	if !strings.Contains(got, "port=29849") {
		t.Errorf("banner missing port=29849: %q", got)
	}
	if !strings.HasSuffix(got, "\n") {
		t.Errorf("banner should end with newline, got %q", got)
	}
}

func TestCollapseHome(t *testing.T) {
	t.Setenv("HOME", "/Users/victor")
	cases := map[string]string{
		"/Users/victor":                     "~",
		"/Users/victor/.attn-dev":           "~/.attn-dev",
		"/Users/victor/.attn-dev/attn.sock": "~/.attn-dev/attn.sock",
		"/tmp/other":                        "/tmp/other",
	}
	for in, want := range cases {
		if got := collapseHome(in); got != want {
			t.Errorf("collapseHome(%q) = %q, want %q", in, got, want)
		}
	}
}
