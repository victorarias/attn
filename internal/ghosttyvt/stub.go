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

	// KittyImageStorageLimit is the kitty graphics image storage cap in
	// bytes, applied at construction. The zero value disables the kitty
	// graphics protocol entirely — deliberate: the library's own default is
	// 10MB, and a silently-live worker-side parser desyncs the grid from the
	// client model, which never parses kitty.
	KittyImageStorageLimit uint64
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

func (t *Terminal) SetColorTheme(_ ColorTheme) error { return nil }

func (t *Terminal) Resize(cols, rows int) {
	if cols > 0 && rows > 0 {
		t.cols, t.rows = cols, rows
	}
}

// ResizeNoReflow mirrors the real build's no-reflow resize. The stub parses
// nothing, so there is no grid to reflow and no mode to toggle.
func (t *Terminal) ResizeNoReflow(cols, rows int) { t.Resize(cols, rows) }

// SetCellPixelSize mirrors the real build's cell-geometry setter. The stub
// answers no size report, so there is nothing for a cell size to scale.
func (t *Terminal) SetCellPixelSize(_, _ int) {}

func (t *Terminal) DrainResponses() []byte { return nil }

func (t *Terminal) Size() (cols, rows int) { return t.cols, t.rows }

func (t *Terminal) PlainText() string { return "" }

func (t *Terminal) Serialize() Snapshot { return Snapshot{Cols: t.cols, Rows: t.rows} }

func (t *Terminal) CursorPos() (x, y int) { return 0, 0 }

func (t *Terminal) CursorVisible() bool { return false }

func (t *Terminal) LeftRightMarginMode() bool { return false }

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

// KittyImageFormat mirrors the real build's stored-image pixel layouts.
type KittyImageFormat uint8

const (
	KittyImageRGB KittyImageFormat = iota
	KittyImageRGBA
	KittyImageGrayAlpha
	KittyImageGray
)

// KittyPlacement mirrors the real build's observed placement.
type KittyPlacement struct {
	ImageID         uint32
	PlacementID     uint32
	Virtual         bool
	Z               int32
	PixelWidth      uint32
	PixelHeight     uint32
	GridCols        uint32
	GridRows        uint32
	ViewportCol     int32
	ViewportRow     int32
	ViewportVisible bool
	SourceX         uint32
	SourceY         uint32
	SourceWidth     uint32
	SourceHeight    uint32
	ImageGeneration uint64
}

// KittyImage mirrors the real build's copied-out image.
type KittyImage struct {
	ID         uint32
	Width      uint32
	Height     uint32
	Format     KittyImageFormat
	Generation uint64
	Data       []byte
}

// KittyGeneration is always zero on the stub: no parser, so nothing ever
// mutates the storage — the same value a live terminal reports while empty.
func (t *Terminal) KittyGeneration() uint64 { return 0 }

// KittyPlacements is always empty on the stub.
func (t *Terminal) KittyPlacements() []KittyPlacement { return nil }

// KittyImage never finds an image on the stub.
func (t *Terminal) KittyImage(_ uint32) (KittyImage, bool) { return KittyImage{}, false }
