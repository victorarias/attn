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

// The list is shared with the fence, so iterating it cannot catch a variable
// missing from both. ATTN_DATA_DIR was exactly that gap: `profile-env` left it
// behind, and an inherited one silently outranked the profile it selected.
func TestWriteProfileEnvClearsTheDataDir(t *testing.T) {
	var output strings.Builder
	writeProfileEnv(&output, "dev", false)

	if !strings.Contains(output.String(), "unset ATTN_DATA_DIR\n") {
		t.Fatalf("profile env output must clear ATTN_DATA_DIR: %q", output.String())
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
