//go:build cgo && ((darwin && arm64) || (linux && amd64) || (linux && arm64))

package pty

// Kitty image identity across worker lifetimes.
//
// A session id outlives its worker — runtime_respawned replaces the process, and
// so do a daemon restart and a revive — while ghostty's generation stamps start
// over with every terminal. A client keys its pixels by (session, image id,
// generation), so the two facts together let a replacement worker describe an
// identity the client already holds different pixels for. mintKittyEpoch is what
// keeps that from happening; these hold it to the two properties it needs.

import (
	"testing"
)

// kittyEpochFloor and kittyEpochCeiling bound where an epoched identity may
// land: past any stamp a process reaches, and inside what a JS Number keys
// exactly. Written out rather than shared with the production constants, so a
// window that moves has to be re-justified here too.
const (
	kittyEpochFloor   = uint64(1) << 32
	kittyEpochCeiling = uint64(1) << 53
)

// Both exits of a session speak one numbering. A placement names a generation,
// the client pulls the pixels behind it, and it stores what comes back under the
// generation the ANSWER carries — so a fold applied to one exit and not the
// other leaves every pull landing on a key nothing ever asks for, and the image
// is re-pulled on every description forever.
//
// Every placement exit is taken, because they are separate calls and only one
// fold site is shared: the live fan-out, the attach snapshot a re-attaching
// client restores from, and the re-describe a reflow produces.
func TestKittyIdentityIsTheSameAtEveryExit(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real PTY spawn in short mode")
	}
	t.Setenv(kittyStorageLimitEnv, "16777216")

	const done = "PAYLOAD-END"
	spawn := newHeldKittySpawn(t, "kitty-identity", "\x1b[6;1H"+kittyPlaceRGB(84, 16, 32, "")+done)
	placed := releaseAndPlace(t, spawn)
	live := placed.Placements[0].ImageGeneration
	spawn.waitForOutput(t, done)

	if live < kittyEpochFloor || live >= kittyEpochCeiling {
		t.Fatalf("described generation = %d, want an epoched identity in [2^32, 2^53): the fold never happened", live)
	}

	attached, err := spawn.manager.Attach(spawn.id, "identity-client",
		func([]byte, uint32) bool { return true }, nil)
	if err != nil {
		t.Fatalf("Attach() error: %v", err)
	}
	if len(attached.GhosttyPlacements) != 1 {
		t.Fatalf("attach placements = %+v, want the one image", attached.GhosttyPlacements)
	}
	if got := attached.GhosttyPlacements[0].ImageGeneration; got != live {
		t.Errorf("attach snapshot generation = %d, want the %d the live description carried", got, live)
	}

	if err := spawn.manager.Resize(spawn.id, 40, 4); err != nil {
		t.Fatalf("Resize() error: %v", err)
	}
	var reflowed PlacementUpdate
	select {
	case reflowed = <-spawn.updates:
	default:
		t.Fatal("the reflow described nothing")
	}
	if len(reflowed.Placements) != 1 {
		t.Fatalf("placements after the reflow = %+v, want the image still described", reflowed.Placements)
	}
	if got := reflowed.Placements[0].ImageGeneration; got != live {
		t.Errorf("reflow generation = %d, want the %d the live description carried", got, live)
	}

	img, err := spawn.manager.KittyImage(spawn.id, 84)
	if err != nil {
		t.Fatalf("KittyImage(84) error: %v", err)
	}
	if img.Generation != live {
		t.Errorf("served image generation = %d, want the %d its placement named: the pull can never be correlated back", img.Generation, live)
	}
}

// The reviewer's scenario, and the whole reason the epoch exists: a worker that
// replaces another must not be able to mint an identity a client is already
// holding pixels for. The replacement is a new PROCESS — ghostty's stamps
// restart there, so the same program emitting the same image gets the same low
// number again, and the client's blob cache and its GPU textures both answer the
// new placement with the dead worker's pixels.
//
// Two spawns, and the assertions are what a unit test can honestly make about
// that. Ghostty's stamps are unique process-wide (see KittyGeneration), so two
// terminals inside ONE test binary never collide on their own and comparing
// their described generations proves nothing — measured: with the fold removed,
// these two sessions still describe 3 and 5. What has to differ is the epoch
// itself, because that is the only part of the identity a second process does
// not reproduce. So: each identity is epoched, and the two epochs are
// independent draws rather than one process-wide constant.
func TestKittyIdentitiesFromDifferentWorkersNeverCollide(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real PTY spawn in short mode")
	}
	t.Setenv(kittyStorageLimitEnv, "16777216")

	const imageID = 85
	payload := "\x1b[3;1H" + kittyPlaceRGB(imageID, 16, 32, "")

	describe := func(sessionID string) (generation, epoch uint64) {
		t.Helper()
		spawn := newKittySpawn(t, sessionID, payload)
		spawn.release(t)
		session, err := spawn.manager.getSession(sessionID)
		if err != nil {
			t.Fatalf("%s: getSession() error: %v", sessionID, err)
		}
		select {
		case update := <-spawn.updates:
			if len(update.Placements) != 1 {
				t.Fatalf("%s: placements = %+v, want the one image", sessionID, update.Placements)
			}
			return update.Placements[0].ImageGeneration, session.kittyEpoch
		default:
			t.Fatalf("%s: the image was never described", sessionID)
			return 0, 0
		}
	}

	first, firstEpoch := describe("kitty-worker-one")
	second, secondEpoch := describe("kitty-worker-two")

	// Inequality of two random draws out of a 2^52-wide window, asserted the way
	// UUID inequality is: a collision is not a flake anyone will see.
	if firstEpoch == secondEpoch {
		t.Errorf("both workers minted epoch %d: a replacement worker would describe the dead one's identities",
			firstEpoch)
	}
	for _, got := range []uint64{first, second} {
		if got < kittyEpochFloor || got >= kittyEpochCeiling {
			t.Errorf("described generation = %d, want an epoched identity in [2^32, 2^53)", got)
		}
	}
}
