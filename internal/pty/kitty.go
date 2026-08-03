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
	"errors"
	"os"
	"strconv"
	"strings"

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
// terminal is built with, in BYTES.
//
// Unset — the shipping default — is 0, which makes ghostty refuse every
// transmission: nothing is stored, no placement is ever observed, and the feed
// path never leaves its no-cgo path. The override exists so a non-production
// profile can run the whole description pipeline against real emitters before a
// measured default replaces it.
const kittyStorageLimitEnv = "ATTN_KITTY_STORAGE_LIMIT"

// kittyStorageLimit resolves the cap for a session about to be spawned. A value
// that is not a byte count is reported and treated as absent: a session with
// images silently disabled is confusing, but a spawn that fails over a
// diagnostic environment variable is worse.
func kittyStorageLimit(logf LogFunc) uint64 {
	raw := strings.TrimSpace(os.Getenv(kittyStorageLimitEnv))
	if raw == "" {
		return 0
	}
	limit, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		if logf != nil {
			logf(
				"pty kitty storage: ignoring %s=%q, want a byte count; images stay disabled for this session",
				kittyStorageLimitEnv,
				raw,
			)
		}
		return 0
	}
	return limit
}
