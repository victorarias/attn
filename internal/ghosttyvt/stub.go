//go:build !(darwin && arm64)

// Package ghosttyvt's non-macOS-arm64 build is a portable no-op. The real
// implementation links a native libghostty-vt static library that only exists
// for aarch64-macos (see scripts/build-libghostty-vt.sh). attn is a macOS-only
// product, but its Go code stays buildable on Linux for CI (vet + unit tests),
// so this stub provides the same API surface with inert behavior.
//
// A stub Terminal serializes to nothing and renders nothing; callers already
// treat the terminal as best-effort (every use in internal/pty is nil-safe and
// tolerant of empty output), so nothing observes the difference off macOS.
package ghosttyvt

// DefaultMaxScrollback mirrors the real build's constant.
const DefaultMaxScrollback = 10000

// Options mirrors the real build's construction options.
type Options struct {
	MaxScrollback int
}

// Snapshot mirrors the real build's serialization result.
type Snapshot struct {
	Cols, Rows int
	VTDump     []byte
}

// Terminal is the no-op stand-in for the native terminal off macOS/arm64.
type Terminal struct {
	cols, rows int
}

// New returns an inert terminal. It never fails so callers exercise the same
// code path they do on macOS; every method is a no-op.
func New(cols, rows int, _ Options) (*Terminal, error) {
	return &Terminal{cols: cols, rows: rows}, nil
}

func (t *Terminal) Write(_ []byte) {}

func (t *Terminal) Resize(cols, rows int) {
	if cols > 0 && rows > 0 {
		t.cols, t.rows = cols, rows
	}
}

func (t *Terminal) DrainResponses() []byte { return nil }

func (t *Terminal) Size() (cols, rows int) { return t.cols, t.rows }

func (t *Terminal) PlainText() string { return "" }

func (t *Terminal) Serialize() Snapshot { return Snapshot{Cols: t.cols, Rows: t.rows} }

func (t *Terminal) Close() {}

// TrackedRef mirrors the real build's tracked grid reference. The stub cannot
// pin cells, so TrackCursor always returns nil and instances never exist; the
// type only keeps cross-platform callers compiling.
type TrackedRef struct{}

func (r *TrackedRef) ScreenPoint() (x, y int, ok bool) { return 0, 0, false }

func (r *TrackedRef) Free() {}

// TrackCursor always fails on the stub; callers already treat a nil ref as
// "position unpinnable" and degrade to serving no blocks.
func (t *Terminal) TrackCursor() *TrackedRef { return nil }

// AltScreenActive is always false on the stub.
func (t *Terminal) AltScreenActive() bool { return false }

// LiveTrackedRefs mirrors the real build's leak accounting; always zero here.
func LiveTrackedRefs() int { return 0 }
