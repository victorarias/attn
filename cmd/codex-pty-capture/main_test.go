package main

import "testing"

func TestPreviewBytesEscapesControlSequences(t *testing.T) {
	got := previewBytes([]byte("\x1b[?2026hhello\r\n"), 80)
	want := `\x1b[?2026hhello\r\n`
	if got != want {
		t.Fatalf("previewBytes() = %q, want %q", got, want)
	}
}

func TestControlFlagsDetectsSynchronizedOutput(t *testing.T) {
	flags := controlFlags([]byte("a\x1b[?2026hb\x1b[?2026lc"))
	if !flags["sync_start"] || !flags["sync_end"] {
		t.Fatalf("controlFlags() = %#v, want sync start and end", flags)
	}
}

func TestCommandFromArgsDefaultsToCodex(t *testing.T) {
	got := commandFromArgs(nil)
	if len(got) != 1 || got[0] != "codex" {
		t.Fatalf("commandFromArgs(nil) = %#v, want codex", got)
	}
}
