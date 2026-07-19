package toolhome

import (
	"path/filepath"
	"testing"
)

func TestDir_EnvVarSet_ReturnsCleaned(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv(EnvVar, tmp+string(filepath.Separator)+".")

	got, err := Dir()
	if err != nil {
		t.Fatalf("Dir() returned error: %v", err)
	}
	want := filepath.Clean(tmp)
	if got != want {
		t.Fatalf("Dir() = %q, want %q", got, want)
	}
}

func TestDir_EnvVarUnset_PanicsUnderTest(t *testing.T) {
	t.Setenv(EnvVar, "")

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected Dir() to panic when ATTN_TOOL_HOME is unset under go test, but it did not")
		}
	}()

	_, _ = Dir()
	t.Fatal("unreachable: Dir() should have panicked before returning")
}
