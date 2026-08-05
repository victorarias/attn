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

	dump := term.Serialize().VTDump
	if !bytes.Contains(dump, []byte("\x1b]4;2;rgb:0d/bc/79\x1b\\")) {
		t.Fatalf("serialized palette does not contain attn green: %q", dump)
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

	dump := term.Serialize().VTDump
	if !bytes.Contains(dump, []byte("\x1b]4;2;rgb:12/34/56\x1b\\")) {
		t.Fatalf("serialized palette lost the OSC 4 override: %q", dump)
	}
}
