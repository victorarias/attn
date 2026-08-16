package ghosttyvt

import (
	"bytes"
	"testing"
)

func TestSetColorThemeSerializesEmbedderANSIPalette(t *testing.T) {
	term, err := New(20, 4, Options{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(term.Close)

	theme := ColorTheme{HasANSIPalette: true}
	theme.ANSIPalette[2] = 0x0dbc79
	if err := term.SetColorTheme(theme); err != nil {
		t.Fatalf("SetColorTheme() error = %v", err)
	}

	dump := restoredViewportDump(t, term)
	if !bytes.Contains(dump, []byte("\x1b]4;2;rgb:0d/bc/79\x1b\\")) {
		t.Fatalf("restored palette does not contain attn green: %q", dump)
	}
}

func TestSetColorThemePreservesProgramPaletteOverride(t *testing.T) {
	term, err := New(20, 4, Options{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(term.Close)

	term.Write([]byte("\x1b]4;2;rgb:12/34/56\x1b\\"))
	theme := ColorTheme{HasANSIPalette: true}
	theme.ANSIPalette[2] = 0x0dbc79
	if err := term.SetColorTheme(theme); err != nil {
		t.Fatalf("SetColorTheme() error = %v", err)
	}

	dump := restoredViewportDump(t, term)
	if !bytes.Contains(dump, []byte("\x1b]4;2;rgb:12/34/56\x1b\\")) {
		t.Fatalf("restored palette lost the OSC 4 override: %q", dump)
	}
}

// restoredViewportDump round-trips a terminal through a snapshot and returns
// the restored terminal's viewport serialization. The palette is not readable
// as bytes on the snapshot itself — it is a binary record stream — so what it
// carried is asserted through what the restored terminal emits.
func restoredViewportDump(t *testing.T, src *Terminal) []byte {
	t.Helper()
	restored, err := Restore(src.Serialize().Payload, Options{})
	if err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	t.Cleanup(restored.Close)
	return restored.SerializeViewport().VTDump
}
