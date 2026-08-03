package pty

// What the worker says about kitty images, and how the pixels are fetched.
//
// The worker is the system's only kitty parser (see wirefeed.go): the APC bytes
// never reach a client, so a client that wants to draw an image has to be told
// where it is. That is two things and no more — an update carrying the whole
// placement set of the active screen, and a pull for the pixels behind a
// placement whose image the puller has not seen.
//
// Both are descriptions. Nothing here changes a grid, and a session that never
// stores an image never reaches any of it.

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/ghosttyvt"
)

// KittyPlacement is one image placed on the active screen, as ghostty resolved
// it at observation time: geometry in both cells and pixels, the source rect,
// and the generation of the image behind it.
//
// The observation IS the description — there is deliberately no second struct
// to copy into. Ghostty resolves geometry the worker does not interpret, and a
// hand-maintained mirror would rot the first time a pin resolves a new piece of
// it. Callers outside this package name the type through pty.
type KittyPlacement = ghosttyvt.KittyPlacement

// KittyImage is one stored image copied out of a terminal: always raw pixels in
// Format's layout, never PNG and never compressed, because ghostty decodes and
// inflates before storing.
type KittyImage = ghosttyvt.KittyImage

// PlacementUpdate is the FULL placement set of a session's active screen as of
// the chunk stamped Seq — never a delta, and the empty set is an ordinary
// update rather than a special case. Sets are tiny (zero to a handful), so
// wholesale replacement costs nothing and makes a missed update self-healing:
// the next one is the whole truth again.
//
// Seq ties the set to the bytes it was measured on. The read loop delivers an
// update after fanning out the chunk with the same seq, so a consumer that
// applies both in arrival order measures placements against the grid those
// bytes produced.
type PlacementUpdate struct {
	Seq        uint32
	Placements []KittyPlacement
}

// ErrKittyImageNotFound reports that a terminal holds no image with the id
// asked for — evicted at the storage limit, deleted by the program, or never
// transmitted at all. It is an ordinary answer rather than a failure: the
// asking client drops that placement's render, and a retransmission describes
// the image again.
var ErrKittyImageNotFound = errors.New("kitty image not found")

// SubscriberOption configures an attaching subscriber beyond its byte stream
// and drop callback.
type SubscriberOption func(*sessionSubscriber)

// OnPlacements delivers placement updates to this subscriber. Placements ride
// the subscriber rather than a session-wide handler because an update is only
// meaningful in order against that subscriber's own byte stream.
func OnPlacements(fn func(PlacementUpdate)) SubscriberOption {
	return func(sub *sessionSubscriber) {
		sub.onPlacements = fn
	}
}

// kittyStorageLimitEnv overrides the kitty image storage cap a new session's
// terminal is built with, in BYTES. Images are on by default, so this is a
// tripwire override rather than a feature flag: a value here is for tuning a
// session that hit the ceiling, or for turning the protocol off.
//
// Zero is the one special value — it makes ghostty refuse every transmission,
// so nothing is stored, no placement is ever observed, and the feed path never
// leaves its no-cgo path. That is the escape hatch if a program's images ever
// misbehave, and it is what FuzzKittyWireMirrorShipping still guards.
const kittyStorageLimitEnv = "ATTN_KITTY_STORAGE_LIMIT"

// kittyStorageLimitDefault is what a session gets with nothing in the
// environment: 320MB, which is ghostty's own app default and within 5% of
// kitty's.
//
// Set past where any real image lands, because the failure past it is total
// rather than gradual. Ghostty refuses a single image larger than the WHOLE
// limit outright (addImage returns error.OutOfMemory) and every emitter
// measured in the A4 sweep transmits with q=2, which suppresses the response —
// so an over-limit image does not degrade, it silently does not appear.
// The largest legitimate single image the sweep produced is ~81.4MB, a
// full-screen Pro Display XDR capture at 2x; 320MB clears that by about 4x.
//
// Under the limit, hitting it is ordinary: ghostty evicts the oldest image to
// admit a new one, which is what an animation does all day and costs nothing.
const kittyStorageLimitDefault = 320_000_000

// kittyStorageLimit resolves the cap for a session about to be spawned. A value
// that is not a byte count is reported and treated as absent — meaning the
// DEFAULT, not zero: a typo in a tuning variable must not silently turn images
// off, and a spawn that fails over a diagnostic environment variable is worse
// than either.
func kittyStorageLimit(logf LogFunc) uint64 {
	raw := strings.TrimSpace(os.Getenv(kittyStorageLimitEnv))
	if raw == "" {
		return kittyStorageLimitDefault
	}
	limit, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		if logf != nil {
			logf(
				"pty kitty storage: ignoring %s=%q, want a byte count; images run at the default %d bytes for this session",
				kittyStorageLimitEnv,
				raw,
				uint64(kittyStorageLimitDefault),
			)
		}
		return kittyStorageLimitDefault
	}
	return limit
}

// mintKittyEpoch draws the per-terminal-instance offset folded into every kitty
// generation this session reports. There are exactly two folds — the placement
// read (wireFeeder.readPlacements) and the image serve (Session.kittyImage) —
// and they must use the same value, or a placement stops naming the blob behind
// it and no client can correlate the two again.
//
// Ghostty numbers its stamps per process, starting over with every terminal,
// while a session id outlives its worker: runtime_respawned replaces the worker
// process, and so do a daemon restart and a revive. Raw, a replacement worker
// would describe (same session, same image id, same generation) for entirely
// different pixels — and the app caches by exactly that triple, blobs and GPU
// textures both, so it would redraw the dead worker's image and never pull the
// new one. A fresh epoch per terminal makes every identity minted after a
// respawn a new cache key by construction, which is one mechanism for all three
// lifecycles and no wire change.
//
// The window is [2^32, 2^52). Ceiling receipt: generations ride JSON
// (kitty_placements, the attach snapshot, kitty_image_result) into JS Numbers,
// which are exact only to 2^53 — the binary frame decoder drops any generation
// past Number.MAX_SAFE_INTEGER outright (app/src/pty/binaryPtyFrame.ts). An
// epoch below 2^52 leaves 2^52 of stamp headroom above it, and a stamp moves by
// one per storage mutation, so nothing a person sits in front of approaches it.
// Floor receipt: 2^32 is past any stamp a real process reaches, so an epoched
// identity is disjoint from a raw one.
func mintKittyEpoch() uint64 {
	const floor = uint64(1) << 32
	const span = uint64(1)<<52 - floor
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Effectively unreachable — crypto/rand does not fail on a platform the
		// worker runs on. What the fallback protects is the invariant rather
		// than the randomness: the epoch stays inside the window and is never
		// zero, because a zero epoch is the defect above and a spawn that fails
		// over entropy is worse than a predictable identity.
		return floor + uint64(time.Now().UnixNano())%span
	}
	return floor + binary.BigEndian.Uint64(b[:])%span
}
