package crew

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func filedAt(t *testing.T, stamp string) time.Time {
	t.Helper()
	at, err := time.Parse(time.RFC3339, stamp)
	if err != nil {
		t.Fatalf("parse %s: %v", stamp, err)
	}
	return at
}

func TestHandoff_LandsUnderTheNameTheLineAlreadyUses(t *testing.T) {
	home := t.TempDir()
	path, err := FileHandoff(home, "trellis", "Where I left off.", filedAt(t, "2026-08-14T19:30:12Z"))
	if err != nil {
		t.Fatalf("file: %v", err)
	}
	want := filepath.Join(home, HandoffsDirName, "2026-08-14T19-30Z-trellis.md")
	if path != want {
		t.Fatalf("filed at %s, want %s", path, want)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(body) != "Where I left off.\n" {
		t.Fatalf("the letter on disk is %q; attn must not edit the prose", string(body))
	}
}

func TestHandoff_TheStampIsUTCHoweverTheClockIsSet(t *testing.T) {
	home := t.TempDir()
	zone := time.FixedZone("UTC+9", 9*3600)
	path, err := FileHandoff(home, "keel", "Filed from Tokyo.", filedAt(t, "2026-08-14T19:30:00Z").In(zone))
	if err != nil {
		t.Fatalf("file: %v", err)
	}
	if filepath.Base(path) != "2026-08-14T19-30Z-keel.md" {
		t.Fatalf("filed as %s; the line is UTC so names sort chronologically", filepath.Base(path))
	}
}

func TestHandoff_ALetterAlreadyFiledIsNeverOverwritten(t *testing.T) {
	home := t.TempDir()
	at := filedAt(t, "2026-08-14T19:30:00Z")
	first, err := FileHandoff(home, "trellis", "The honest closure.", at)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	_, refusal := FileHandoff(home, "trellis", "A rewrite of it.", at)
	if !errors.Is(refusal, ErrHandoffExists) {
		t.Fatalf("second filing returned %v, want the append-only refusal", refusal)
	}
	if !strings.Contains(refusal.Error(), first) {
		t.Fatalf("the refusal %q does not name the letter that is already there", refusal)
	}
	body, err := os.ReadFile(first)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(body) != "The honest closure.\n" {
		t.Fatalf("the filed letter became %q", string(body))
	}
}

func TestHandoff_ThereIsNothingToFileWithoutALetter(t *testing.T) {
	home := t.TempDir()
	for _, note := range []string{"", "   \n\t "} {
		if _, err := FileHandoff(home, "trellis", note, time.Now()); err == nil {
			t.Fatalf("filing %q was accepted", note)
		}
	}
	if entries, err := os.ReadDir(filepath.Join(home, HandoffsDirName)); err == nil && len(entries) > 0 {
		t.Fatalf("a refused filing left %d files behind", len(entries))
	}
}

func TestHandoff_SomethingThatIsNotALetterIsRefusedWithBothNumbers(t *testing.T) {
	home := t.TempDir()
	spill := strings.Repeat("x", MaxHandoffBytes+1)
	_, err := FileHandoff(home, "trellis", spill, time.Now())
	if err == nil {
		t.Fatal("a note past the limit was filed")
	}
	for _, want := range []string{"64001", "64000"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the refusal %q does not name %s — an agent cannot fix a limit it cannot see", err, want)
		}
	}
}

func TestHandoff_TheFreshestLetterIsTheOneJustFiled(t *testing.T) {
	home := t.TempDir()
	for _, stamp := range []string{"2026-08-12T09:00:00Z", "2026-08-13T22:20:00Z", "2026-08-14T19:30:00Z"} {
		if _, err := FileHandoff(home, "trellis", "day "+stamp, filedAt(t, stamp)); err != nil {
			t.Fatalf("file %s: %v", stamp, err)
		}
	}
	entries, err := os.ReadDir(filepath.Join(home, HandoffsDirName))
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	SortHandoffNames(names)
	if names[0] != "2026-08-14T19-30Z-trellis.md" {
		t.Fatalf("the freshest letter is %s; the wake would prime from the wrong day", names[0])
	}
}
