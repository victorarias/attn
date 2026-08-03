package ptyworker

// The kitty hop: an event that says where the images are, and a method that
// hands back the pixels. Both cross a JSON boundary between two processes that
// can be different builds, so what is asserted here is that nothing is lost or
// silently reinterpreted on the way.

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/victorarias/attn/internal/ghosttyvt"
	"github.com/victorarias/attn/internal/pty"
)

func testPlacement(imageID, placementID uint32) pty.KittyPlacement {
	return pty.KittyPlacement{
		ImageID:         imageID,
		PlacementID:     placementID,
		Virtual:         true,
		Z:               -3,
		PixelWidth:      640,
		PixelHeight:     480,
		GridCols:        80,
		GridRows:        30,
		ViewportCol:     4,
		ViewportRow:     -2,
		ViewportVisible: true,
		SourceX:         1,
		SourceY:         2,
		SourceWidth:     320,
		SourceHeight:    240,
		ImageGeneration: 9,
	}
}

// Every field of a placement survives the trip. Asserted whole rather than
// field by field: a geometry field that arrives zeroed puts an image at the
// wrong size or the wrong row, and a list of the fields worth checking would go
// stale the first time ghostty resolves a new one.
func TestKittyPlacementsSurviveTheWire(t *testing.T) {
	observed := []pty.KittyPlacement{testPlacement(7, 1), testPlacement(9, 2)}

	payload, err := json.Marshal(placementsToWire(observed))
	if err != nil {
		t.Fatalf("marshal placements: %v", err)
	}
	var wire []KittyPlacement
	if err := json.Unmarshal(payload, &wire); err != nil {
		t.Fatalf("unmarshal placements: %v", err)
	}

	if got := PlacementsFromWire(wire); !reflect.DeepEqual(got, observed) {
		t.Errorf("placements after the round trip:\n got %+v\nwant %+v", got, observed)
	}
}

// The empty set is the message that the last image is gone, and it travels as
// an event with no placements rather than as no event at all. A reader that
// treats "no array" as "nothing to say" leaves a ghost image on the screen with
// nothing left to remove it.
func TestKittyPlacementsEventCarriesTheEmptySet(t *testing.T) {
	seq := uint32(41)
	payload, err := json.Marshal(EventEnvelope{
		Type:       "evt",
		Event:      EventKittyPlacements,
		SessionID:  "s1",
		Seq:        &seq,
		Placements: placementsToWire(nil),
	})
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}

	var decoded EventEnvelope
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}
	if decoded.Event != EventKittyPlacements {
		t.Fatalf("event = %q, want %q", decoded.Event, EventKittyPlacements)
	}
	if decoded.Seq == nil || *decoded.Seq != seq {
		t.Errorf("seq = %v, want %d: an update means nothing without the chunk it describes", decoded.Seq, seq)
	}
	if got := PlacementsFromWire(decoded.Placements); len(got) != 0 {
		t.Errorf("placements = %+v, want the empty set", got)
	}
}

// A stamped set stays with its stamp on a full envelope, alongside the fields
// the other events use.
func TestKittyPlacementsEventRoundTrip(t *testing.T) {
	seq := uint32(1234)
	payload, err := json.Marshal(EventEnvelope{
		Type:       "evt",
		Event:      EventKittyPlacements,
		SessionID:  "s1",
		Seq:        &seq,
		Placements: placementsToWire([]pty.KittyPlacement{testPlacement(3, 0)}),
	})
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}

	var decoded EventEnvelope
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}
	if decoded.Seq == nil || *decoded.Seq != seq {
		t.Fatalf("seq = %v, want %d", decoded.Seq, seq)
	}
	placements := PlacementsFromWire(decoded.Placements)
	if len(placements) != 1 || placements[0].ImageID != 3 {
		t.Fatalf("placements = %+v, want the one image", placements)
	}
	if placements[0].ViewportRow != -2 {
		t.Errorf("viewport row = %d, want -2: a placement scrolled above the screen keeps its sign",
			placements[0].ViewportRow)
	}
}

// The pixels and the layout they are in. The layout crosses as a name because
// the two ends can be different builds; a number would be read through whatever
// enum order the reader was compiled with.
func TestKittyImageResultRoundTrip(t *testing.T) {
	pixels := []byte{0, 1, 2, 253, 254, 255}
	img := pty.KittyImage{
		ID:         12,
		Width:      2,
		Height:     1,
		Format:     ghosttyvt.KittyImageRGB,
		Generation: 4,
		Data:       pixels,
	}

	result, err := kittyImageToWire(img)
	if err != nil {
		t.Fatalf("kittyImageToWire: %v", err)
	}
	if result.Format != kittyFormatRGB {
		t.Errorf("format on the wire = %q, want %q", result.Format, kittyFormatRGB)
	}

	payload, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	var decoded KittyImageResult
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	back, err := decoded.Decode()
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !reflect.DeepEqual(back, img) {
		t.Errorf("image after the round trip:\n got %+v\nwant %+v", back, img)
	}
}

// Every layout ghostty can store has a name and comes back as itself. A layout
// that collapsed into another would be drawn with the wrong bytes per pixel —
// a smeared image, not an error.
func TestKittyImageFormatsAreDistinctOnTheWire(t *testing.T) {
	formats := []ghosttyvt.KittyImageFormat{
		ghosttyvt.KittyImageRGB,
		ghosttyvt.KittyImageRGBA,
		ghosttyvt.KittyImageGrayAlpha,
		ghosttyvt.KittyImageGray,
	}

	seen := map[string]bool{}
	for _, format := range formats {
		result, err := kittyImageToWire(pty.KittyImage{ID: 1, Format: format, Data: []byte{1}})
		if err != nil {
			t.Fatalf("kittyImageToWire(format %d): %v", format, err)
		}
		if seen[result.Format] {
			t.Errorf("format %d shares the wire name %q with another layout", format, result.Format)
		}
		seen[result.Format] = true

		back, err := result.Decode()
		if err != nil {
			t.Fatalf("Decode(format %q): %v", result.Format, err)
		}
		if back.Format != format {
			t.Errorf("format %d came back as %d", format, back.Format)
		}
	}
}

// A name this build does not know is reported, never guessed at: rendering
// pixels as a layout they are not in produces a plausible-looking wrong image.
func TestKittyImageResultRejectsAnUnknownFormat(t *testing.T) {
	_, err := KittyImageResult{ImageID: 5, Format: "yuv420", Data: ""}.Decode()
	if err == nil {
		t.Fatal("Decode() accepted an unknown pixel format")
	}
}
