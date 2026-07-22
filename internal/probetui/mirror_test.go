package probetui

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/victorarias/attn/internal/probetui/vtvocab"
)

// fixture mirrors the agent-mirror capture tool's analysis.json shape:
// real VT vocabulary recorded from a live agent, split at the
// startup+idle / resize+teardown boundary.
type fixture struct {
	BoundaryOffset int64         `json:"boundaryOffset"`
	Phase1         vtvocab.Stats `json:"phase1"`
	Phase2         vtvocab.Stats `json:"phase2"`
}

func loadFixture(t *testing.T, path string) fixture {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	var f fixture
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("unmarshal fixture %s: %v", path, err)
	}
	return f
}

// pin ties one VT feature to two independent checks: does the probe emit
// it (or correctly withhold it), and does the recorded real-agent fixture
// exhibit the same behavior somewhere in its capture. A failure on either
// side names the feature, so drift (probe regression, or the mirror going
// stale against an agent update) is diagnosable without re-deriving which
// assertion caught it.
type pin struct {
	name    string
	probe   func(t *testing.T, combined vtvocab.Stats)
	fixture func(t *testing.T, phase1, phase2 vtvocab.Stats)
}

func runPins(t *testing.T, pins []pin, combined vtvocab.Stats, f fixture) {
	t.Helper()
	for _, p := range pins {
		p := p
		t.Run(p.name, func(t *testing.T) {
			t.Run("probe", func(t *testing.T) { p.probe(t, combined) })
			t.Run("fixture", func(t *testing.T) { p.fixture(t, f.Phase1, f.Phase2) })
		})
	}
}

func mirrorTranscript(style Style) []byte {
	var out []byte
	out = append(out, Startup(style, 80, 24)...)
	out = append(out, Frame(style, 80, 24, 1)...)
	out = append(out, OnResize(style, 62, 27)...)
	out = append(out, Frame(style, 62, 27, 2)...)
	out = append(out, Teardown(style)...)
	return out
}

func TestMirrorCodex(t *testing.T) {
	combined := vtvocab.Analyze(mirrorTranscript(StyleCodex))
	f := loadFixture(t, "testdata/agent-vocab-codex.json")

	pins := []pin{
		{
			name: "synchronized-update (2026 set + reset)",
			probe: func(t *testing.T, s vtvocab.Stats) {
				if s.PrivateModeSet["2026"] == 0 || s.PrivateModeReset["2026"] == 0 {
					t.Errorf("want ?2026h and ?2026l both present, got set=%d reset=%d", s.PrivateModeSet["2026"], s.PrivateModeReset["2026"])
				}
			},
			fixture: func(t *testing.T, p1, p2 vtvocab.Stats) {
				if p1.PrivateModeSet["2026"] == 0 {
					t.Errorf("fixture phase1 never sets mode 2026")
				}
			},
		},
		{
			name: "no alt-screen",
			probe: func(t *testing.T, s vtvocab.Stats) {
				if s.AltScreenEnter != 0 || s.AltScreenExit != 0 {
					t.Errorf("codex probe must never touch the alt screen, got enter=%d exit=%d", s.AltScreenEnter, s.AltScreenExit)
				}
			},
			fixture: func(t *testing.T, p1, p2 vtvocab.Stats) {
				if p1.AltScreenEnter != 0 || p1.AltScreenExit != 0 || p2.AltScreenEnter != 0 || p2.AltScreenExit != 0 {
					t.Errorf("codex fixture unexpectedly touches the alt screen")
				}
			},
		},
		{
			name: "OSC 8 hyperlinks",
			probe: func(t *testing.T, s vtvocab.Stats) {
				if s.OSCByCode["8"] == 0 {
					t.Errorf("want at least one OSC 8 hyperlink")
				}
			},
			fixture: func(t *testing.T, p1, p2 vtvocab.Stats) {
				if p1.OSCByCode["8"] == 0 && p2.OSCByCode["8"] == 0 {
					t.Errorf("fixture never emits an OSC 8 hyperlink")
				}
			},
		},
		{
			name: "DA1 query on startup",
			probe: func(t *testing.T, s vtvocab.Stats) {
				if s.DA1QueryFromChild == 0 {
					t.Errorf("want a DA1 query (ESC[c)")
				}
			},
			fixture: func(t *testing.T, p1, p2 vtvocab.Stats) {
				if p1.DA1QueryFromChild == 0 {
					t.Errorf("fixture phase1 never issues a DA1 query")
				}
			},
		},
		{
			name: "CPR query on startup",
			probe: func(t *testing.T, s vtvocab.Stats) {
				if s.CPRQueryFromChild == 0 {
					t.Errorf("want a CPR query (ESC[6n) on startup")
				}
			},
			fixture: func(t *testing.T, p1, p2 vtvocab.Stats) {
				if p1.CPRQueryFromChild == 0 {
					t.Errorf("fixture phase1 never issues a CPR query")
				}
			},
		},
		{
			name: "CPR query re-asserted on resize",
			probe: func(t *testing.T, s vtvocab.Stats) {
				if s.CPRQueryFromChild < 2 {
					t.Errorf("want CPR queried again on resize, got %d total", s.CPRQueryFromChild)
				}
			},
			fixture: func(t *testing.T, p1, p2 vtvocab.Stats) {
				if p2.CPRQueryFromChild == 0 {
					t.Errorf("fixture phase2 never re-issues a CPR query after resize")
				}
			},
		},
		{
			name: "OSC 10/11 color queries",
			probe: func(t *testing.T, s vtvocab.Stats) {
				if s.OSCColorQueryFromChild["10"] == 0 || s.OSCColorQueryFromChild["11"] == 0 {
					t.Errorf("want OSC 10 and 11 color queries, got %v", s.OSCColorQueryFromChild)
				}
			},
			fixture: func(t *testing.T, p1, p2 vtvocab.Stats) {
				if p1.OSCColorQueryFromChild["10"] == 0 || p1.OSCColorQueryFromChild["11"] == 0 {
					t.Errorf("fixture phase1 missing OSC 10/11 color queries, got %v", p1.OSCColorQueryFromChild)
				}
			},
		},
		{
			name: "DECSTBM scroll region",
			probe: func(t *testing.T, s vtvocab.Stats) {
				if s.DECSTBM == 0 {
					t.Errorf("want at least one DECSTBM (CSI r)")
				}
			},
			fixture: func(t *testing.T, p1, p2 vtvocab.Stats) {
				if p1.DECSTBM == 0 && p2.DECSTBM == 0 {
					t.Errorf("fixture never sets a scroll region")
				}
			},
		},
		{
			name: "startup modes 1004 + 2004",
			probe: func(t *testing.T, s vtvocab.Stats) {
				if s.PrivateModeSet["1004"] == 0 || s.PrivateModeSet["2004"] == 0 {
					t.Errorf("want ?1004h and ?2004h on startup, got %v", s.PrivateModeSet)
				}
			},
			fixture: func(t *testing.T, p1, p2 vtvocab.Stats) {
				if p1.PrivateModeSet["1004"] == 0 || p1.PrivateModeSet["2004"] == 0 {
					t.Errorf("fixture phase1 missing ?1004h/?2004h, got %v", p1.PrivateModeSet)
				}
			},
		},
		{
			name: "cursor hidden during paint (mode 25 reset)",
			probe: func(t *testing.T, s vtvocab.Stats) {
				if s.PrivateModeReset["25"] == 0 {
					t.Errorf("want ?25l (hide cursor) during frame paint")
				}
			},
			fixture: func(t *testing.T, p1, p2 vtvocab.Stats) {
				if p1.PrivateModeReset["25"] == 0 {
					t.Errorf("fixture phase1 never hides the cursor")
				}
			},
		},
		{
			name: "no newlines",
			probe: func(t *testing.T, s vtvocab.Stats) {
				if s.Newlines != 0 {
					t.Errorf("codex probe must never emit \\n, got %d", s.Newlines)
				}
			},
			fixture: func(t *testing.T, p1, p2 vtvocab.Stats) {
				if p1.Newlines != 0 || p2.Newlines != 0 {
					t.Errorf("codex fixture unexpectedly contains \\n (phase1=%d phase2=%d)", p1.Newlines, p2.Newlines)
				}
			},
		},
		{
			name: "no carriage returns",
			probe: func(t *testing.T, s vtvocab.Stats) {
				if s.CarriageReturns != 0 {
					t.Errorf("codex probe must never emit \\r, got %d", s.CarriageReturns)
				}
			},
			fixture: func(t *testing.T, p1, p2 vtvocab.Stats) {
				if p1.CarriageReturns != 0 || p2.CarriageReturns != 0 {
					t.Errorf("codex fixture unexpectedly contains \\r (phase1=%d phase2=%d)", p1.CarriageReturns, p2.CarriageReturns)
				}
			},
		},
	}

	runPins(t, pins, combined, f)
}

func TestMirrorClaude(t *testing.T) {
	combined := vtvocab.Analyze(mirrorTranscript(StyleClaude))
	f := loadFixture(t, "testdata/agent-vocab-claude.json")

	pins := []pin{
		{
			name: "alt-screen enter on startup",
			probe: func(t *testing.T, s vtvocab.Stats) {
				if s.AltScreenEnter == 0 {
					t.Errorf("want ?1049h on startup")
				}
			},
			fixture: func(t *testing.T, p1, p2 vtvocab.Stats) {
				if p1.AltScreenEnter == 0 {
					t.Errorf("fixture phase1 never enters the alt screen")
				}
			},
		},
		{
			name: "alt-screen exit on teardown",
			probe: func(t *testing.T, s vtvocab.Stats) {
				if s.AltScreenExit == 0 {
					t.Errorf("want ?1049l on teardown")
				}
			},
			fixture: func(t *testing.T, p1, p2 vtvocab.Stats) {
				if p2.AltScreenExit == 0 {
					t.Errorf("fixture phase2 never exits the alt screen")
				}
			},
		},
		{
			name: "mouse modes asserted on startup",
			probe: func(t *testing.T, s vtvocab.Stats) {
				for _, m := range claudeMouseModes {
					if s.PrivateModeSet[m] == 0 {
						t.Errorf("want mouse mode %s set, got %v", m, s.PrivateModeSet)
					}
				}
			},
			fixture: func(t *testing.T, p1, p2 vtvocab.Stats) {
				for _, m := range claudeMouseModes {
					if p1.PrivateModeSet[m] == 0 {
						t.Errorf("fixture phase1 missing mouse mode %s set", m)
					}
				}
			},
		},
		{
			name: "mouse modes re-asserted on resize",
			probe: func(t *testing.T, s vtvocab.Stats) {
				for _, m := range claudeMouseModes {
					if s.PrivateModeSet[m] < 2 {
						t.Errorf("want mouse mode %s set twice (startup + resize), got %d", m, s.PrivateModeSet[m])
					}
				}
			},
			fixture: func(t *testing.T, p1, p2 vtvocab.Stats) {
				for _, m := range claudeMouseModes {
					if p2.PrivateModeSet[m] == 0 {
						t.Errorf("fixture phase2 never re-asserts mouse mode %s after resize", m)
					}
				}
			},
		},
		{
			name: "mouse modes reset on teardown",
			probe: func(t *testing.T, s vtvocab.Stats) {
				for _, m := range claudeMouseModes {
					if s.PrivateModeReset[m] == 0 {
						t.Errorf("want mouse mode %s reset on teardown, got %v", m, s.PrivateModeReset)
					}
				}
			},
			fixture: func(t *testing.T, p1, p2 vtvocab.Stats) {
				for _, m := range claudeMouseModes {
					if p2.PrivateModeReset[m] == 0 {
						t.Errorf("fixture phase2 never resets mouse mode %s", m)
					}
				}
			},
		},
		{
			name: "OSC 0 title",
			probe: func(t *testing.T, s vtvocab.Stats) {
				if s.OSCByCode["0"] == 0 {
					t.Errorf("want an OSC 0 title sequence")
				}
			},
			fixture: func(t *testing.T, p1, p2 vtvocab.Stats) {
				if p1.OSCByCode["0"] == 0 {
					t.Errorf("fixture phase1 never sets an OSC 0 title")
				}
			},
		},
		{
			name: "DECSCUSR (final 'q')",
			probe: func(t *testing.T, s vtvocab.Stats) {
				if s.OtherCSIFinal["q"] == 0 {
					t.Errorf("want a DECSCUSR sequence (CSI ... q)")
				}
			},
			fixture: func(t *testing.T, p1, p2 vtvocab.Stats) {
				if p1.OtherCSIFinal["q"] == 0 {
					t.Errorf("fixture phase1 never emits a final 'q' CSI sequence")
				}
			},
		},
		{
			name: "DA1 query on startup",
			probe: func(t *testing.T, s vtvocab.Stats) {
				if s.DA1QueryFromChild == 0 {
					t.Errorf("want a DA1 query (ESC[c)")
				}
			},
			fixture: func(t *testing.T, p1, p2 vtvocab.Stats) {
				if p1.DA1QueryFromChild == 0 {
					t.Errorf("fixture phase1 never issues a DA1 query")
				}
			},
		},
		{
			name: "no CPR query",
			probe: func(t *testing.T, s vtvocab.Stats) {
				if s.CPRQueryFromChild != 0 {
					t.Errorf("claude probe must never query CPR, got %d", s.CPRQueryFromChild)
				}
			},
			fixture: func(t *testing.T, p1, p2 vtvocab.Stats) {
				if p1.CPRQueryFromChild != 0 || p2.CPRQueryFromChild != 0 {
					t.Errorf("claude fixture unexpectedly queries CPR (phase1=%d phase2=%d)", p1.CPRQueryFromChild, p2.CPRQueryFromChild)
				}
			},
		},
		{
			name: "no OSC color queries",
			probe: func(t *testing.T, s vtvocab.Stats) {
				if len(s.OSCColorQueryFromChild) != 0 {
					t.Errorf("claude probe must never query OSC 10/11/12 colors, got %v", s.OSCColorQueryFromChild)
				}
			},
			fixture: func(t *testing.T, p1, p2 vtvocab.Stats) {
				if len(p1.OSCColorQueryFromChild) != 0 || len(p2.OSCColorQueryFromChild) != 0 {
					t.Errorf("claude fixture unexpectedly queries OSC colors (phase1=%v phase2=%v)", p1.OSCColorQueryFromChild, p2.OSCColorQueryFromChild)
				}
			},
		},
		{
			name: "DECSC/DECRC cursor save-restore",
			probe: func(t *testing.T, s vtvocab.Stats) {
				if s.CursorSaveDECSC == 0 || s.CursorRestoreDECRC == 0 {
					t.Errorf("want ESC 7 / ESC 8 at least once per frame, got save=%d restore=%d", s.CursorSaveDECSC, s.CursorRestoreDECRC)
				}
			},
			fixture: func(t *testing.T, p1, p2 vtvocab.Stats) {
				if p1.CursorSaveDECSC == 0 || p1.CursorRestoreDECRC == 0 {
					t.Errorf("fixture phase1 missing DECSC/DECRC, save=%d restore=%d", p1.CursorSaveDECSC, p1.CursorRestoreDECRC)
				}
			},
		},
		{
			name: "OSC 8 hyperlinks",
			probe: func(t *testing.T, s vtvocab.Stats) {
				if s.OSCByCode["8"] == 0 {
					t.Errorf("want at least one OSC 8 hyperlink")
				}
			},
			fixture: func(t *testing.T, p1, p2 vtvocab.Stats) {
				if p1.OSCByCode["8"] == 0 {
					t.Errorf("fixture phase1 never emits an OSC 8 hyperlink")
				}
			},
		},
		{
			name: "relative cursor moves (CSI B/C/G)",
			probe: func(t *testing.T, s vtvocab.Stats) {
				if s.OtherCSIFinal["B"] == 0 || s.OtherCSIFinal["C"] == 0 || s.OtherCSIFinal["G"] == 0 {
					t.Errorf("want CSI B (down), C (forward) and G (column) all present, got %v", s.OtherCSIFinal)
				}
			},
			fixture: func(t *testing.T, p1, p2 vtvocab.Stats) {
				if p1.OtherCSIFinal["B"] == 0 || p1.OtherCSIFinal["C"] == 0 || p1.OtherCSIFinal["G"] == 0 {
					t.Errorf("fixture phase1 missing one of CSI B/C/G, got %v", p1.OtherCSIFinal)
				}
			},
		},
		{
			name: "carriage-return driven layout, not newlines",
			probe: func(t *testing.T, s vtvocab.Stats) {
				if s.CarriageReturns == 0 {
					t.Errorf("want \\r used for row positioning")
				}
				if s.Newlines != 0 {
					t.Errorf("claude probe must stay deterministic: no \\n, got %d", s.Newlines)
				}
			},
			fixture: func(t *testing.T, p1, p2 vtvocab.Stats) {
				if p1.CarriageReturns <= p1.Newlines {
					t.Errorf("fixture phase1 does not predominantly use \\r for layout (cr=%d nl=%d)", p1.CarriageReturns, p1.Newlines)
				}
			},
		},
		{
			name: "bracketed paste + focus + mode 2031 reset on teardown",
			probe: func(t *testing.T, s vtvocab.Stats) {
				if s.PrivateModeReset["1004"] == 0 || s.PrivateModeReset["2004"] == 0 || s.PrivateModeReset["2031"] == 0 {
					t.Errorf("want ?1004l ?2004l ?2031l on teardown, got %v", s.PrivateModeReset)
				}
			},
			fixture: func(t *testing.T, p1, p2 vtvocab.Stats) {
				if p2.PrivateModeReset["1004"] == 0 || p2.PrivateModeReset["2004"] == 0 || p2.PrivateModeReset["2031"] == 0 {
					t.Errorf("fixture phase2 missing ?1004l/?2004l/?2031l, got %v", p2.PrivateModeReset)
				}
			},
		},
	}

	runPins(t, pins, combined, f)
}
