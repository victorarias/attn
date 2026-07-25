//go:build !cgo || !((darwin && arm64) || (linux && amd64) || (linux && arm64))

// Package ghosttyvt provides a buildability shim for unsupported GOOS/GOARCH
// combinations and cgo-disabled cross-builds. attn supports darwin/arm64,
// linux/amd64, and linux/arm64; product builds for those platforms link the
// native libghostty-vt archive. This stub is never a product path.
//
// A stub Terminal serializes to nothing and renders nothing.
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

func (t *Terminal) CursorPos() (x, y int) { return 0, 0 }

func (t *Terminal) CursorVisible() bool { return false }

func (t *Terminal) ViewportText() string { return "" }

func (t *Terminal) SerializeViewport() Snapshot { return Snapshot{Cols: t.cols, Rows: t.rows} }

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
