package main

import (
	"strings"
	"testing"
)

func TestWriteProfileEnvClearsRoutingOverridesBeforeSelectingProfile(t *testing.T) {
	var output strings.Builder
	writeProfileEnv(&output, "dev", false)

	got := output.String()
	for _, name := range profileRoutingOverrides {
		if !strings.Contains(got, "unset "+name+"\n") {
			t.Fatalf("profile env output missing %s cleanup: %q", name, got)
		}
	}
	if !strings.HasSuffix(got, "export ATTN_PROFILE=dev\n") {
		t.Fatalf("profile env output does not select dev last: %q", got)
	}
}

func TestWriteProfileEnvFishClearsRoutingOverridesWhenReturningToDefault(t *testing.T) {
	var output strings.Builder
	writeProfileEnv(&output, "", true)

	got := output.String()
	for _, name := range profileRoutingOverrides {
		if !strings.Contains(got, "set -e "+name+"\n") {
			t.Fatalf("fish profile env output missing %s cleanup: %q", name, got)
		}
	}
	if !strings.HasSuffix(got, "set -e ATTN_PROFILE\n") {
		t.Fatalf("fish profile env output does not clear profile last: %q", got)
	}
}
