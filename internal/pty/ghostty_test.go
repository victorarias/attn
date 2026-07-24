package pty

import (
	"testing"

	"github.com/victorarias/attn/internal/ghosttyvt"
)

func newTestGhostty(t *testing.T, cols, rows int) *ghosttyvt.Terminal {
	t.Helper()
	term, err := ghosttyvt.New(cols, rows, ghosttyvt.Options{})
	if err != nil {
		t.Fatalf("ghosttyvt.New(%d, %d): %v", cols, rows, err)
	}
	t.Cleanup(term.Close)
	if term.Serialize().VTDump == nil {
		t.Skip("ghosttyvt returned a nil VT dump; skipping on the non-native stub")
	}
	return term
}
