//go:build (darwin && arm64) || (linux && amd64) || (linux && arm64)

package ghosttyvt

import (
	"bytes"
	"strings"
	"testing"
)

// Kitty graphics probes. kittyQueryAPC is the support query feature-detecting
// tools send (a=q); kittyTransmitAPC is the smallest write that both stores an
// image and places it on the grid — 1x1 direct RGB, base64 of 0xFF,0x00,0x00.
const (
	kittyQueryAPC    = "\x1b_Gi=31,s=1,v=1,a=q,t=d,f=24;AAAA\x1b\\"
	kittyTransmitAPC = "\x1b_Ga=T,i=32,f=24,s=1,v=1;/wAA\x1b\\"
	kittyRespPrefix  = "\x1b_G"
)

// newKittyT builds a terminal with the given kitty storage limit and resizes
// it, which is what installs cell pixel metrics. Without them a placed image
// spans zero cells and never moves the cursor — every grid assertion below
// would hold no matter what the protocol did. Production terminals are always
// resized, so this is the honest configuration.
func newKittyT(t *testing.T, cols, rows int, limit uint64) *Terminal {
	t.Helper()
	term, err := New(cols, rows, Options{KittyImageStorageLimit: limit})
	if err != nil {
		t.Fatalf("New(%d,%d, limit=%d): %v", cols, rows, limit, err)
	}
	t.Cleanup(term.Close)
	term.Resize(cols, rows)
	return term
}

func TestKittyDisabledByDefaultSilencesSupportQuery(t *testing.T) {
	term := newKittyT(t, 20, 4, 0)
	term.DrainResponses()

	term.Write([]byte(kittyQueryAPC))

	if resp := term.DrainResponses(); bytes.Contains(resp, []byte(kittyRespPrefix)) {
		t.Fatalf("kitty support query answered with the protocol disabled: %q", resp)
	}
}

func TestKittyDisabledByDefaultPlacementHasNoGridEffect(t *testing.T) {
	term := newKittyT(t, 20, 4, 0)
	term.Write([]byte("before"))
	term.DrainResponses()
	wantX, wantY := term.CursorPos()
	wantText := term.PlainText()

	term.Write([]byte(kittyTransmitAPC))

	if x, y := term.CursorPos(); x != wantX || y != wantY {
		t.Fatalf("cursor moved from (%d,%d) to (%d,%d): the disabled protocol still placed an image", wantX, wantY, x, y)
	}
	if got := term.PlainText(); got != wantText {
		t.Fatalf("grid text changed from %q to %q", wantText, got)
	}
	if resp := term.DrainResponses(); bytes.Contains(resp, []byte(kittyRespPrefix)) {
		t.Fatalf("kitty transmit answered with the protocol disabled: %q", resp)
	}
}

// The storage limit is documented as applying to "all initialized screens". A
// lazily-created alternate screen coming up with the library's own default
// would resurrect the protocol behind every full-screen TUI.
func TestKittyDisabledOnAlternateScreen(t *testing.T) {
	term := newKittyT(t, 20, 4, 0)
	term.Write([]byte("\x1b[?1049h"))
	term.Write([]byte("before"))
	term.DrainResponses()
	wantX, wantY := term.CursorPos()

	term.Write([]byte(kittyTransmitAPC))

	if x, y := term.CursorPos(); x != wantX || y != wantY {
		t.Fatalf("alt-screen cursor moved from (%d,%d) to (%d,%d): kitty is live on the alternate screen", wantX, wantY, x, y)
	}
	if resp := term.DrainResponses(); bytes.Contains(resp, []byte(kittyRespPrefix)) {
		t.Fatalf("alt-screen kitty transmit answered with the protocol disabled: %q", resp)
	}
}

// A positive limit must leave the protocol fully functional: the value has to
// reach the right option at the right width, or turning images on later is a
// silent no-op. The placed 1x1 image occupies one cell and advances the cursor
// past it — the grid effect the client model can never reproduce, which is why
// the limit stays zero until the wire carries placements.
func TestKittyPositiveLimitKeepsProtocolLive(t *testing.T) {
	term := newKittyT(t, 20, 4, 10<<20)
	term.DrainResponses()

	term.Write([]byte(kittyQueryAPC))
	if resp := string(term.DrainResponses()); !strings.Contains(resp, "\x1b_Gi=31;OK") {
		t.Fatalf("support query response = %q, want an OK for image id 31", resp)
	}

	term.Write([]byte("before"))
	beforeX, beforeY := term.CursorPos()
	beforeText := term.PlainText()

	term.Write([]byte(kittyTransmitAPC))

	if resp := string(term.DrainResponses()); !strings.Contains(resp, "\x1b_Gi=32;OK") {
		t.Fatalf("transmit response = %q, want an OK for image id 32", resp)
	}
	if x, y := term.CursorPos(); x != beforeX+1 || y != beforeY {
		t.Fatalf("cursor = (%d,%d) after placing a one-cell image at (%d,%d), want (%d,%d)", x, y, beforeX, beforeY, beforeX+1, beforeY)
	}
	if got := term.PlainText(); got != beforeText {
		t.Fatalf("grid text changed from %q to %q: the placement is pixels, not cells", beforeText, got)
	}
}
