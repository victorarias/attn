package ptyworker

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/victorarias/attn/internal/ghosttyvt"
	"github.com/victorarias/attn/internal/pty"
)

const (
	RPCMajor = 1
	RPCMinor = 1
)

// MinCompatibleRPCMinor defines the oldest peer minor version that is
// compatible with this worker RPC major line.
const MinCompatibleRPCMinor = 0

const (
	MethodHello = "hello"
	MethodInfo  = "info"
	// MethodSnapshot returns the current rendered screen + LastSeq without
	// attaching. Added without an RPC version bump: older workers reject it
	// with ErrBadRequest ("unknown method"), and the daemon degrades to an
	// unseeded observer rather than failing.
	MethodSnapshot = "snapshot"
	MethodAttach   = "attach"
	MethodWatch    = "watch"
	MethodDetach   = "detach"
	MethodInput    = "input"
	MethodResize   = "resize"
	// MethodSetTheme updates the colors the session answers OSC 10/11/12 color
	// queries with. Added without an RPC version bump, following the
	// MethodSnapshot precedent: older workers reject it with ErrBadRequest
	// ("unknown method"), and callers treat that as non-fatal.
	MethodSetTheme = "set_theme"
	MethodSignal   = "signal"
	MethodRemove   = "remove"
	MethodHealth   = "health"
	// MethodKittyImage returns one stored image's decoded pixels by ghostty
	// image id. Pulled on demand rather than pushed with the placement that
	// references it: an image is megabytes of raw pixels and a client usually
	// already has it. Added without an RPC version bump, following the
	// MethodSnapshot precedent: an older worker rejects it with ErrBadRequest
	// ("unknown method"), which reads the same as an image it cannot serve.
	MethodKittyImage = "kitty_image"
)

const (
	EventOutput       = "output"
	EventDesync       = "desync"
	EventStateChanged = "state_changed"
	EventExit         = "exit"
	// EventKittyPlacements carries the FULL kitty placement set of the session's
	// active screen as of the chunk stamped Seq, the empty set included — that
	// is how a client learns the last image is gone. Sent on the attach
	// connection, after the EventOutput carrying the same Seq, because the
	// positions were measured on the grid those bytes produce. Additive like the
	// methods above: a worker that never sends it leaves its client drawing no
	// images, which is also what a client that ignores it does.
	EventKittyPlacements = "kitty_placements"
)

const (
	ErrBadRequest         = "bad_request"
	ErrUnsupportedVersion = "unsupported_version"
	ErrUnauthorized       = "unauthorized"
	ErrSessionNotFound    = "session_not_found"
	ErrSessionNotRunning  = "session_not_running"
	ErrIO                 = "io_error"
	ErrInternal           = "internal_error"
	// ErrImageNotFound answers MethodKittyImage for an id the terminal does not
	// hold — evicted at the storage limit, deleted by the program, or never
	// transmitted. Its own code because it is an ordinary answer: the caller
	// drops that placement's render instead of treating the session as broken.
	ErrImageNotFound = "image_not_found"
)

type RPCError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type RequestEnvelope struct {
	Type   string          `json:"type"`
	ID     string          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

type ResponseEnvelope struct {
	Type   string          `json:"type"`
	ID     string          `json:"id"`
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *RPCError       `json:"error,omitempty"`
}

type EventEnvelope struct {
	Type       string  `json:"type"`
	Event      string  `json:"event"`
	SessionID  string  `json:"session_id"`
	Seq        *uint32 `json:"seq,omitempty"`
	Data       *string `json:"data,omitempty"`
	Reason     *string `json:"reason,omitempty"`
	State      *string `json:"state,omitempty"`
	ExitCode   *int    `json:"exit_code,omitempty"`
	ExitSignal *string `json:"exit_signal,omitempty"`

	// StateSource / StateDetail / StateObservedAt qualify State on
	// EventStateChanged: which observer spoke, why, and when it observed. The
	// daemon arbitrates between observers, which a bare state name cannot
	// support. Added without an RPC version bump, following the MethodSnapshot
	// precedent: an older worker simply omits them and the daemon treats the
	// state as source-unknown, observed on arrival.
	StateSource     *string `json:"state_source,omitempty"`
	StateDetail     *string `json:"state_detail,omitempty"`
	StateObservedAt *string `json:"state_observed_at,omitempty"`

	// Placements is the whole placement set on EventKittyPlacements, stamped by
	// Seq. An absent array on that event is the empty set — the event name
	// already says what the field is about, so there is nothing to distinguish
	// it from.
	Placements []KittyPlacement `json:"placements,omitempty"`
}

// KittyPlacement is the wire form of one observed placement (see
// pty.KittyPlacement for field semantics). Viewport row and column are
// screen-relative on the worker's grid, which the client's grid equals, so a
// client maps them by adding its own scrollback length.
type KittyPlacement struct {
	ImageID         uint32 `json:"image_id"`
	PlacementID     uint32 `json:"placement_id"`
	Virtual         bool   `json:"virtual,omitempty"`
	Z               int32  `json:"z,omitempty"`
	PixelWidth      uint32 `json:"pixel_width"`
	PixelHeight     uint32 `json:"pixel_height"`
	GridCols        uint32 `json:"grid_cols"`
	GridRows        uint32 `json:"grid_rows"`
	ViewportCol     int32  `json:"viewport_col"`
	ViewportRow     int32  `json:"viewport_row"`
	ViewportVisible bool   `json:"viewport_visible,omitempty"`
	SourceX         uint32 `json:"source_x,omitempty"`
	SourceY         uint32 `json:"source_y,omitempty"`
	SourceWidth     uint32 `json:"source_width,omitempty"`
	SourceHeight    uint32 `json:"source_height,omitempty"`
	ImageGeneration uint64 `json:"image_generation"`
}

// placementsToWire converts an observed placement set to its wire form (1:1
// fields). An empty set converts to nil, which the event carries as an absent
// array and means exactly that.
func placementsToWire(placements []pty.KittyPlacement) []KittyPlacement {
	if len(placements) == 0 {
		return nil
	}
	out := make([]KittyPlacement, len(placements))
	for i, p := range placements {
		out[i] = KittyPlacement{
			ImageID:         p.ImageID,
			PlacementID:     p.PlacementID,
			Virtual:         p.Virtual,
			Z:               p.Z,
			PixelWidth:      p.PixelWidth,
			PixelHeight:     p.PixelHeight,
			GridCols:        p.GridCols,
			GridRows:        p.GridRows,
			ViewportCol:     p.ViewportCol,
			ViewportRow:     p.ViewportRow,
			ViewportVisible: p.ViewportVisible,
			SourceX:         p.SourceX,
			SourceY:         p.SourceY,
			SourceWidth:     p.SourceWidth,
			SourceHeight:    p.SourceHeight,
			ImageGeneration: p.ImageGeneration,
		}
	}
	return out
}

// PlacementsFromWire reads a wire placement set back into the observation form
// the rest of the daemon speaks. Both legs live here so the pair stays one
// contract: a field added on one side and forgotten on the other is a placement
// that silently loses its geometry.
func PlacementsFromWire(placements []KittyPlacement) []pty.KittyPlacement {
	if len(placements) == 0 {
		return nil
	}
	out := make([]pty.KittyPlacement, len(placements))
	for i, p := range placements {
		out[i] = pty.KittyPlacement{
			ImageID:         p.ImageID,
			PlacementID:     p.PlacementID,
			Virtual:         p.Virtual,
			Z:               p.Z,
			PixelWidth:      p.PixelWidth,
			PixelHeight:     p.PixelHeight,
			GridCols:        p.GridCols,
			GridRows:        p.GridRows,
			ViewportCol:     p.ViewportCol,
			ViewportRow:     p.ViewportRow,
			ViewportVisible: p.ViewportVisible,
			SourceX:         p.SourceX,
			SourceY:         p.SourceY,
			SourceWidth:     p.SourceWidth,
			SourceHeight:    p.SourceHeight,
			ImageGeneration: p.ImageGeneration,
		}
	}
	return out
}

type HelloParams struct {
	RPCMajor         int    `json:"rpc_major"`
	RPCMinor         int    `json:"rpc_minor"`
	DaemonInstanceID string `json:"daemon_instance_id"`
	ControlToken     string `json:"control_token"`
}

type HelloResult struct {
	WorkerVersion    string `json:"worker_version"`
	RPCMajor         int    `json:"rpc_major"`
	RPCMinor         int    `json:"rpc_minor"`
	DaemonInstanceID string `json:"daemon_instance_id"`
	SessionID        string `json:"session_id"`
}

type InfoResult struct {
	Running   bool   `json:"running"`
	Agent     string `json:"agent"`
	CWD       string `json:"cwd"`
	Cols      uint16 `json:"cols"`
	Rows      uint16 `json:"rows"`
	WorkerPID int    `json:"worker_pid"`
	ChildPID  int    `json:"child_pid"`
	LastSeq   uint32 `json:"last_seq"`
	State     string `json:"state"`

	// LastSignal* is the newest level the session's signal observers emitted —
	// the agent's own title heartbeat, or a shell pane's foreground level. It is
	// evidence, not a state claim, and it is here because the worker outlives the
	// daemon: an agent parked at its prompt paints nothing, so a daemon that
	// restarted has no other way to learn what the level currently says. Empty
	// claim means the session has never produced one, which is also what a worker
	// predating these fields reports.
	LastSignalClaim  string `json:"last_signal_claim,omitempty"`
	LastSignalDetail string `json:"last_signal_detail,omitempty"`
	LastSignalSource string `json:"last_signal_source,omitempty"`
	LastSignalAt     string `json:"last_signal_at,omitempty"`

	ExitCode   *int    `json:"exit_code,omitempty"`
	ExitSignal *string `json:"exit_signal,omitempty"`
}

type AttachResult struct {
	LastSeq uint32 `json:"last_seq"`
	Cols    uint16 `json:"cols"`
	Rows    uint16 `json:"rows"`
	PID     int    `json:"pid"`
	Running bool   `json:"running"`

	ExitCode   *int    `json:"exit_code,omitempty"`
	ExitSignal *string `json:"exit_signal,omitempty"`

	// GhosttySnapshot is the server-authoritative VT serialization of the whole
	// terminal from libghostty-vt (geometry is Cols/Rows). Omitted when absent.
	GhosttySnapshot []byte `json:"ghostty_snapshot,omitempty"`
	// GhosttyBlocks are the worker's OSC 133 command blocks resolved to
	// SCREEN-space rows of GhosttySnapshot, captured atomically with it and
	// LastSeq (Phase 3a). Mirrors pty.AttachBlockData. Omitted when absent;
	// additive and skew-safe like GhosttySnapshot.
	GhosttyBlocks []AttachBlock `json:"ghostty_blocks,omitempty"`
	// GhosttyPlacements is the kitty placement set of the screen
	// GhosttySnapshot serializes, captured in the same hold as it, the blocks,
	// and LastSeq. Omitted when the session holds no images; additive and
	// skew-safe like the fields above.
	GhosttyPlacements []KittyPlacement `json:"ghostty_placements,omitempty"`
	// GhosttyScrollbackTruncated reports whether the ghostty terminal dropped
	// scrollback lines at its cap before GhosttySnapshot was serialized.
	GhosttyScrollbackTruncated bool `json:"ghostty_scrollback_truncated,omitempty"`
}

// SnapshotResult is the lean read-only viewport seed returned by MethodSnapshot.
// An absent ScreenSnapshot leaves observers unseeded for graceful worker-version
// skew and sessions that have not yet produced a frame.
type SnapshotResult struct {
	LastSeq        uint32 `json:"last_seq"`
	Cols           uint16 `json:"cols"`
	Rows           uint16 `json:"rows"`
	Running        bool   `json:"running"`
	ScreenSnapshot []byte `json:"screen_snapshot,omitempty"`
	// ScreenText is a pointer so an old worker omitting the additive field is
	// distinguishable from a genuinely blank viewport.
	ScreenText *string `json:"screen_text,omitempty"`
	ScreenCols uint16  `json:"screen_cols,omitempty"`
	ScreenRows uint16  `json:"screen_rows,omitempty"`
}

// AttachBlock is the wire form of one resolved command block (see
// pty.AttachBlockData for field semantics; EndRow is exclusive, Pending marks
// the single open block).
type AttachBlock struct {
	ID             uint64  `json:"id"`
	Pending        bool    `json:"pending,omitempty"`
	PromptRow      int32   `json:"prompt_row"`
	InputRow       *int32  `json:"input_row,omitempty"`
	InputCol       *int32  `json:"input_col,omitempty"`
	OutputStartRow *int32  `json:"output_start_row,omitempty"`
	EndRow         *int32  `json:"end_row,omitempty"`
	Command        *string `json:"command,omitempty"`
	ExitCode       *int32  `json:"exit_code,omitempty"`
}

type AttachParams struct {
	SubscriberID string `json:"subscriber_id,omitempty"`
}

type InputParams struct {
	Data string `json:"data"`
}

// XPixel/YPixel are the pane's total size in device pixels. Omitted (0) by a
// caller with no pixel geometry, and by every worker predating them.
type ResizeParams struct {
	Cols   uint16 `json:"cols"`
	Rows   uint16 `json:"rows"`
	XPixel uint16 `json:"xpixel,omitempty"`
	YPixel uint16 `json:"ypixel,omitempty"`
}

type SignalParams struct {
	Signal string `json:"signal"`
}

type SetThemeParams struct {
	Foreground  string     `json:"foreground"`
	Background  string     `json:"background"`
	Cursor      string     `json:"cursor"`
	ANSIPalette [16]string `json:"ansi_palette"`
}

type KittyImageParams struct {
	ImageID uint32 `json:"image_id"`
}

// KittyImageResult is one stored image. Data is base64'd RAW PIXELS in Format's
// layout — ghostty decodes PNG and inflates zlib before storing, so there is no
// compressed form to pass through — which makes the payload Width*Height*bpp,
// bounded by the session's storage limit rather than by what the program sent.
//
// Generation pairs with ImageID: a program that retransmits an id replaces the
// pixels behind every placement of it, and a cache keyed on the id alone would
// serve the old ones forever.
type KittyImageResult struct {
	ImageID    uint32 `json:"image_id"`
	Width      uint32 `json:"width"`
	Height     uint32 `json:"height"`
	Format     string `json:"format"`
	Generation uint64 `json:"generation"`
	Data       string `json:"data"`
}

// Kitty pixel-layout names. Spelled out on the wire rather than passed through
// as ghostty's enum value: this RPC crosses a version boundary — a worker
// outlives the daemon that spawned it — and a number whose meaning is a
// declaration order is one pin bump away from being read as a different layout.
const (
	kittyFormatRGB       = "rgb"
	kittyFormatRGBA      = "rgba"
	kittyFormatGrayAlpha = "gray_alpha"
	kittyFormatGray      = "gray"
)

// kittyImageToWire encodes a stored image for the JSON hop.
func kittyImageToWire(img pty.KittyImage) (KittyImageResult, error) {
	var format string
	switch img.Format {
	case ghosttyvt.KittyImageRGB:
		format = kittyFormatRGB
	case ghosttyvt.KittyImageRGBA:
		format = kittyFormatRGBA
	case ghosttyvt.KittyImageGrayAlpha:
		format = kittyFormatGrayAlpha
	case ghosttyvt.KittyImageGray:
		format = kittyFormatGray
	default:
		return KittyImageResult{}, fmt.Errorf("kitty image %d has unknown pixel format %d", img.ID, img.Format)
	}
	return KittyImageResult{
		ImageID:    img.ID,
		Width:      img.Width,
		Height:     img.Height,
		Format:     format,
		Generation: img.Generation,
		Data:       base64.StdEncoding.EncodeToString(img.Data),
	}, nil
}

// Decode reads a wire image back into the form the daemon serves to clients.
// The failures it names are both "this worker and this daemon disagree", which
// the caller reports rather than rendering.
func (r KittyImageResult) Decode() (pty.KittyImage, error) {
	var format ghosttyvt.KittyImageFormat
	switch r.Format {
	case kittyFormatRGB:
		format = ghosttyvt.KittyImageRGB
	case kittyFormatRGBA:
		format = ghosttyvt.KittyImageRGBA
	case kittyFormatGrayAlpha:
		format = ghosttyvt.KittyImageGrayAlpha
	case kittyFormatGray:
		format = ghosttyvt.KittyImageGray
	default:
		return pty.KittyImage{}, fmt.Errorf("kitty image %d has unknown pixel format %q", r.ImageID, r.Format)
	}
	data, err := base64.StdEncoding.DecodeString(r.Data)
	if err != nil {
		return pty.KittyImage{}, fmt.Errorf("decode kitty image %d pixels: %w", r.ImageID, err)
	}
	return pty.KittyImage{
		ID:         r.ImageID,
		Width:      r.Width,
		Height:     r.Height,
		Format:     format,
		Generation: r.Generation,
		Data:       data,
	}, nil
}

func IsCompatibleVersion(peerMajor, peerMinor int) bool {
	if peerMajor != RPCMajor {
		return false
	}
	if peerMinor < MinCompatibleRPCMinor {
		return false
	}
	if peerMinor > RPCMinor {
		return false
	}
	return true
}

// stateChangedEvent wraps one PTY observation as an EventStateChanged envelope.
func stateChangedEvent(sessionID string, obs pty.Observation) EventEnvelope {
	claim := obs.Claim
	source := string(obs.Source)
	evt := EventEnvelope{
		Type:        "evt",
		Event:       EventStateChanged,
		SessionID:   sessionID,
		State:       &claim,
		StateSource: &source,
	}
	if obs.Detail != "" {
		detail := obs.Detail
		evt.StateDetail = &detail
	}
	if !obs.At.IsZero() {
		at := obs.At.Format(time.RFC3339Nano)
		evt.StateObservedAt = &at
	}
	return evt
}

// ObservationFromEvent reads an EventStateChanged envelope back into an
// Observation. A worker older than the qualifying fields yields
// pty.SourceUnknown observed at fallbackAt (the receive time), which is the
// closest honest answer available.
func ObservationFromEvent(evt EventEnvelope, claim string, fallbackAt time.Time) pty.Observation {
	obs := pty.Observation{
		Source: pty.SourceUnknown,
		Claim:  claim,
		At:     fallbackAt,
	}
	if evt.StateSource != nil && *evt.StateSource != "" {
		obs.Source = pty.Source(*evt.StateSource)
	}
	if evt.StateDetail != nil {
		obs.Detail = *evt.StateDetail
	}
	if evt.StateObservedAt != nil {
		if at, err := time.Parse(time.RFC3339Nano, *evt.StateObservedAt); err == nil && !at.IsZero() {
			obs.At = at
		}
	}
	return obs
}
