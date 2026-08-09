package pty

// Kitty image descriptions and pixel fetch. The worker is the system's only
// kitty parser (see wirefeed.go); clients are told where images sit and pull
// pixels on demand. Nothing here changes a grid.

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
// it at observation time. Deliberately an alias, not a mirror struct: ghostty
// owns the geometry, and a hand-maintained copy would rot on a pin bump.
type KittyPlacement = ghosttyvt.KittyPlacement

// KittyImage is one stored image copied out of a terminal: always raw pixels in
// Format's layout, never PNG — ghostty decodes before storing.
type KittyImage = ghosttyvt.KittyImage

// PlacementUpdate is the FULL placement set of the active screen as of the
// chunk stamped Seq — never a delta; the empty set is an ordinary update, so a
// missed update is self-healing. The read loop delivers it after the chunk with
// the same seq, so applying both in arrival order keeps set and grid aligned.
type PlacementUpdate struct {
	Seq        uint32
	Placements []KittyPlacement
}

// ErrKittyImageNotFound reports that a terminal holds no image with the id
// asked for. An ordinary answer, not a failure: the client drops that render.
var ErrKittyImageNotFound = errors.New("kitty image not found")

// SubscriberOption configures an attaching subscriber.
type SubscriberOption func(*sessionSubscriber)

// OnPlacements delivers placement updates to this subscriber. Rides the
// subscriber, not a session-wide handler: an update is only meaningful in
// order against that subscriber's own byte stream.
func OnPlacements(fn func(PlacementUpdate)) SubscriberOption {
	return func(sub *sessionSubscriber) {
		sub.onPlacements = fn
	}
}

// kittyStorageLimitEnv overrides the kitty image storage cap, in BYTES. Zero is
// special: ghostty refuses every transmission, so nothing is stored, no
// placement is observed, and the feed path never leaves its no-cgo path.
const kittyStorageLimitEnv = "ATTN_KITTY_STORAGE_LIMIT"

// kittyStorageLimitDefault is 320MB — ghostty's own app default, within 5% of
// kitty's. An image larger than the WHOLE limit is refused silently (q=2
// suppresses the response on every emitter measured in the A4 sweep), so the
// tripwire sits far past the largest measured single image, ~81.4MB (2x XDR
// full-screen capture) — about 4x. Under the limit, eviction is ordinary.
const kittyStorageLimitDefault = 320_000_000

// kittyStorageLimit resolves the cap for a session about to be spawned. A
// non-numeric value is reported and treated as absent — the DEFAULT, not zero:
// a typo must not silently turn images off, and a spawn must not fail over it.
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
// generation this session reports. Exactly two folds — wireFeeder.readPlacements
// and Session.kittyImage — and they must use the same value, or placements stop
// naming the blobs behind them. Ghostty numbers stamps per process while a
// session id outlives its worker (respawn, daemon restart, revive); the app
// caches by (session, image id, generation), so a fresh epoch per terminal is
// what keeps a replacement worker from reusing a cached identity.
//
// Window [2^32, 2^52). Ceiling receipt: generations ride JSON into JS Numbers,
// exact only to 2^53; the binary frame decoder drops generations past
// Number.MAX_SAFE_INTEGER (app/src/pty/binaryPtyFrame.ts). Floor receipt: 2^32
// is past any stamp a real process reaches, so epoched and raw are disjoint.
func mintKittyEpoch() uint64 {
	const floor = uint64(1) << 32
	const span = uint64(1)<<52 - floor
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Effectively unreachable; the fallback protects the invariant (inside
		// the window, never zero), not the randomness.
		return floor + uint64(time.Now().UnixNano())%span
	}
	return floor + binary.BigEndian.Uint64(b[:])%span
}
