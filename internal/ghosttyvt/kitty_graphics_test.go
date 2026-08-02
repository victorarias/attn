//go:build (darwin && arm64) || (linux && amd64) || (linux && arm64)

package ghosttyvt

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
)

// kittyStorageLimit is a positive limit so the protocol is live; the value is
// irrelevant to observation, it only has to exceed the test images.
const kittyStorageLimit = 10 << 20

// kittyDirectRGB is a transmit-and-place (a=T) of a raw RGB image. extra
// carries any additional keys (",U=1", ",z=3"…) verbatim.
func kittyDirectRGB(id uint32, w, h int, pix []byte, extra string) string {
	return fmt.Sprintf("\x1b_Ga=T,i=%d,f=24,t=d,s=%d,v=%d%s;%s\x1b\\",
		id, w, h, extra, base64.StdEncoding.EncodeToString(pix))
}

// rgbPixels fills w*h RGB pixels with a per-byte-distinct pattern, so a
// round trip that drops, reorders, or truncates bytes cannot look correct.
func rgbPixels(w, h int) []byte {
	pix := make([]byte, w*h*3)
	for i := range pix {
		pix[i] = byte((i*7 + 13) % 251)
	}
	return pix
}

// Cells are 8x16 px in this package, so a 16x32 image is exactly 2x2 cells:
// every grid number below is an exact expectation, not a rounding artifact.
func TestKittyPlacementGeometry(t *testing.T) {
	term := newKittyT(t, 20, 8, kittyStorageLimit)
	term.Write([]byte("ab"))
	wantCol, wantRow := term.CursorPos()

	term.Write([]byte(kittyDirectRGB(60, 16, 32, rgbPixels(16, 32), "")))

	places := term.KittyPlacements()
	if len(places) != 1 {
		t.Fatalf("KittyPlacements() = %+v, want exactly one placement", places)
	}
	p := places[0]
	if p.ImageID != 60 {
		t.Errorf("ImageID = %d, want 60", p.ImageID)
	}
	if p.Virtual {
		t.Errorf("Virtual = true for a cursor placement")
	}
	if p.GridCols != 2 || p.GridRows != 2 {
		t.Errorf("grid size = %dx%d cells, want 2x2 for a 16x32 image on 8x16 cells", p.GridCols, p.GridRows)
	}
	if p.PixelWidth != 16 || p.PixelHeight != 32 {
		t.Errorf("pixel size = %dx%d, want 16x32", p.PixelWidth, p.PixelHeight)
	}
	if int(p.ViewportCol) != wantCol || int(p.ViewportRow) != wantRow {
		t.Errorf("viewport pos = (%d,%d), want the cursor cell (%d,%d)", p.ViewportCol, p.ViewportRow, wantCol, wantRow)
	}
	if !p.ViewportVisible {
		t.Errorf("ViewportVisible = false for a placement inside the viewport")
	}
	if p.SourceX != 0 || p.SourceY != 0 || p.SourceWidth != 16 || p.SourceHeight != 32 {
		t.Errorf("source rect = (%d,%d)+%dx%d, want (0,0)+16x32 (the whole image)",
			p.SourceX, p.SourceY, p.SourceWidth, p.SourceHeight)
	}
	if p.ImageGeneration == 0 {
		t.Errorf("ImageGeneration = 0; a stored image always carries a stamp")
	}
}

// Everything the placement carries has a non-default value here, and two
// placements of one image sit in the same iteration: a swapped key, a shared
// read buffer, or geometry resolved against the wrong placement all show up.
func TestKittyPlacementReadsNonDefaultKeys(t *testing.T) {
	term := newKittyT(t, 40, 8, kittyStorageLimit)
	term.Write([]byte(kittyDirectRGB(70, 16, 32, rgbPixels(16, 32), ",p=7,x=4,y=8,w=8,h=16,c=3,r=1,z=-5")))
	term.Write([]byte("\r\n"))
	term.Write([]byte("\x1b_Ga=p,i=70,p=8,c=1,r=1,z=9\x1b\\"))

	places := term.KittyPlacements()
	if len(places) != 2 {
		t.Fatalf("KittyPlacements() = %+v, want two placements of image 70", places)
	}
	byID := map[uint32]KittyPlacement{}
	for _, p := range places {
		byID[p.PlacementID] = p
	}

	first, ok := byID[7]
	if !ok {
		t.Fatalf("placement id 7 missing from %+v", places)
	}
	if first.ImageID != 70 || first.Z != -5 {
		t.Errorf("placement 7 = image %d z %d, want image 70 z -5", first.ImageID, first.Z)
	}
	if first.SourceX != 4 || first.SourceY != 8 || first.SourceWidth != 8 || first.SourceHeight != 16 {
		t.Errorf("placement 7 source rect = (%d,%d)+%dx%d, want the requested (4,8)+8x16",
			first.SourceX, first.SourceY, first.SourceWidth, first.SourceHeight)
	}
	if first.GridCols != 3 || first.GridRows != 1 || first.PixelWidth != 24 || first.PixelHeight != 16 {
		t.Errorf("placement 7 = %dx%d cells / %dx%d px, want the requested 3x1 cells (24x16 px)",
			first.GridCols, first.GridRows, first.PixelWidth, first.PixelHeight)
	}

	second, ok := byID[8]
	if !ok {
		t.Fatalf("placement id 8 missing from %+v", places)
	}
	if second.ImageID != 70 || second.Z != 9 {
		t.Errorf("placement 8 = image %d z %d, want image 70 z 9", second.ImageID, second.Z)
	}
	if second.GridCols != 1 || second.GridRows != 1 {
		t.Errorf("placement 8 = %dx%d cells, want the requested 1x1", second.GridCols, second.GridRows)
	}
	// No w/h on the second placement: the source rect resolves to the whole
	// image, not to whatever the previous placement asked for.
	if second.SourceX != 0 || second.SourceY != 0 || second.SourceWidth != 16 || second.SourceHeight != 32 {
		t.Errorf("placement 8 source rect = (%d,%d)+%dx%d, want the full image (0,0)+16x32",
			second.SourceX, second.SourceY, second.SourceWidth, second.SourceHeight)
	}
	if second.ViewportRow != 1 {
		t.Errorf("placement 8 row = %d, want row 1 (placed after one newline)", second.ViewportRow)
	}
	if first.ImageGeneration != second.ImageGeneration || first.ImageGeneration == 0 {
		t.Errorf("image generation differs across placements of one image: %d vs %d",
			first.ImageGeneration, second.ImageGeneration)
	}
}

func TestKittyImageRoundTrip(t *testing.T) {
	term := newKittyT(t, 20, 8, kittyStorageLimit)
	pix := rgbPixels(4, 2)

	term.Write([]byte(kittyDirectRGB(61, 4, 2, pix, "")))

	img, ok := term.KittyImage(61)
	if !ok {
		t.Fatalf("KittyImage(61) not found after transmitting it")
	}
	if img.ID != 61 || img.Width != 4 || img.Height != 2 {
		t.Errorf("image = id %d %dx%d, want id 61 4x2", img.ID, img.Width, img.Height)
	}
	if img.Format != KittyImageRGB {
		t.Errorf("Format = %d, want KittyImageRGB (%d)", img.Format, KittyImageRGB)
	}
	if img.Generation == 0 {
		t.Errorf("Generation = 0 for a stored image")
	}
	if !bytes.Equal(img.Data, pix) {
		t.Errorf("pixel data round trip mismatch:\n got %v\nwant %v", img.Data, pix)
	}
	if _, ok := term.KittyImage(62); ok {
		t.Errorf("KittyImage(62) reported an image that was never transmitted")
	}
}

func TestKittyGenerationTracksStorageMutations(t *testing.T) {
	term := newKittyT(t, 20, 8, kittyStorageLimit)
	if gen := term.KittyGeneration(); gen != 0 {
		t.Fatalf("fresh terminal generation = %d, want 0 (never mutated)", gen)
	}

	term.Write([]byte("plain text\r\n"))
	if gen := term.KittyGeneration(); gen != 0 {
		t.Fatalf("generation = %d after plain text, want it untouched", gen)
	}

	term.Write([]byte(kittyDirectRGB(63, 16, 32, rgbPixels(16, 32), "")))
	afterTransmit := term.KittyGeneration()
	if afterTransmit == 0 {
		t.Fatalf("generation still 0 after a transmit")
	}

	term.Write([]byte("more text\r\n"))
	if gen := term.KittyGeneration(); gen != afterTransmit {
		t.Errorf("generation moved from %d to %d on plain text: the stamp is not storage-scoped", afterTransmit, gen)
	}

	term.Write([]byte("\x1b_Ga=d\x1b\\"))
	afterDelete := term.KittyGeneration()
	if afterDelete == afterTransmit {
		t.Errorf("generation unchanged (%d) across a delete", afterDelete)
	}
	if places := term.KittyPlacements(); len(places) != 0 {
		t.Errorf("KittyPlacements() = %+v after a=d, want none", places)
	}
}

// The storage is per screen. Restore and snapshot work reads the ACTIVE
// screen, so a placement made behind a full-screen TUI must not surface while
// the alternate screen is up — nor be lost when it exits.
func TestKittyPlacementsAreScopedToTheActiveScreen(t *testing.T) {
	term := newKittyT(t, 20, 8, kittyStorageLimit)
	term.Write([]byte(kittyDirectRGB(64, 16, 32, rgbPixels(16, 32), "")))
	if len(term.KittyPlacements()) != 1 {
		t.Fatalf("primary screen placement missing before the alt-screen switch")
	}
	primaryGen := term.KittyGeneration()

	term.Write([]byte("\x1b[?1049h"))
	if places := term.KittyPlacements(); len(places) != 0 {
		t.Errorf("alternate screen reports %+v: primary placements leaked across screens", places)
	}
	if gen := term.KittyGeneration(); gen == primaryGen {
		t.Errorf("alternate screen generation = %d, same as the primary's: one storage, not two", gen)
	}
	if _, ok := term.KittyImage(64); ok {
		t.Errorf("alternate screen resolved image 64 from the primary storage")
	}

	term.Write([]byte("\x1b[?1049l"))
	places := term.KittyPlacements()
	if len(places) != 1 || places[0].ImageID != 64 {
		t.Fatalf("KittyPlacements() = %+v after leaving the alt screen, want the primary placement back", places)
	}
	if gen := term.KittyGeneration(); gen != primaryGen {
		t.Errorf("primary generation = %d after the round trip, want the unchanged %d", gen, primaryGen)
	}
}

// Scrolling moves placements without touching the storage: the generation stamp
// must not move, and the viewport row must go negative and then invisible. The
// synthesis work in the next phase depends on exactly this, so the numbers are
// asserted, not observed loosely.
func TestKittyPlacementScrollsOutOfTheViewport(t *testing.T) {
	term := newKittyT(t, 20, 6, kittyStorageLimit)
	term.Write([]byte("\x1b[H"))
	term.Write([]byte(kittyDirectRGB(65, 16, 32, rgbPixels(16, 32), "")))
	gen := term.KittyGeneration()

	place := func() KittyPlacement {
		t.Helper()
		places := term.KittyPlacements()
		if len(places) != 1 {
			t.Fatalf("KittyPlacements() = %+v, want the one placement", places)
		}
		return places[0]
	}
	if p := place(); p.ViewportRow != 0 || !p.ViewportVisible {
		t.Fatalf("placement starts at row %d visible=%v, want row 0 visible", p.ViewportRow, p.ViewportVisible)
	}

	// Newlines on the last row scroll the screen by one row each.
	scroll := func(n int) {
		t.Helper()
		term.Write([]byte("\x1b[6;1H"))
		term.Write(bytes.Repeat([]byte("\n"), n))
	}

	scroll(1)
	if p := place(); p.ViewportRow != -1 || !p.ViewportVisible {
		t.Errorf("after 1 scroll: row=%d visible=%v, want row -1 still visible (bottom half on screen)", p.ViewportRow, p.ViewportVisible)
	}
	scroll(1)
	if p := place(); p.ViewportRow != -2 || p.ViewportVisible {
		t.Errorf("after 2 scrolls: row=%d visible=%v, want row -2 and invisible for a 2-row placement", p.ViewportRow, p.ViewportVisible)
	}
	scroll(100)
	if p := place(); p.ViewportRow != -102 || p.ViewportVisible {
		t.Errorf("after 102 scrolls: row=%d visible=%v, want row -102 and invisible", p.ViewportRow, p.ViewportVisible)
	}
	// Everything above is viewport arithmetic; the storage never moved.
	if got := term.KittyGeneration(); got != gen {
		t.Errorf("generation moved from %d to %d across scrolling: scrolling is not a storage mutation", gen, got)
	}
	if _, ok := term.KittyImage(65); !ok {
		t.Errorf("image 65 dropped from storage by scrolling")
	}
}

// Scrolling far enough to prune the anchor row out of scrollback does NOT
// retire the placement: it stays in storage and its row clamps to the top of
// what history still holds. So a placement row is only meaningful while the
// row it names still exists — a scrolled-away image reports a position that no
// longer corresponds to its content.
func TestKittyPlacementSurvivesScrollbackPruning(t *testing.T) {
	term := newKittyT(t, 20, 6, kittyStorageLimit)
	term.Write([]byte("\x1b[H"))
	term.Write([]byte(kittyDirectRGB(67, 16, 32, rgbPixels(16, 32), "")))

	term.Write([]byte("\x1b[6;1H"))
	term.Write(bytes.Repeat([]byte("filler\n"), 50000))

	retained := strings.Count(term.PlainText(), "\n")
	places := term.KittyPlacements()
	if len(places) != 1 {
		t.Fatalf("KittyPlacements() = %+v after 50000 scrolls, want the placement retained", places)
	}
	if places[0].ViewportVisible {
		t.Errorf("scrolled-away placement reports ViewportVisible = true")
	}
	if int(-places[0].ViewportRow) >= 50000 {
		t.Errorf("row = %d after 50000 scrolls: expected it clamped near the top of the %d retained lines",
			places[0].ViewportRow, retained)
	}
	if _, ok := term.KittyImage(67); !ok {
		t.Errorf("image 67 evicted from storage by scrollback pruning")
	}
}

// Unicode-placeholder placements are exposed by the same iterator, flagged
// Virtual, with no usable position: ghostty reports viewport_visible = false
// and the position fields are meaningless. Their location lives in the
// placeholder cells of the grid, not in the storage.
func TestKittyVirtualPlacementHasNoCursorGeometry(t *testing.T) {
	term := newKittyT(t, 20, 8, kittyStorageLimit)
	beforeX, beforeY := term.CursorPos()

	term.Write([]byte(kittyDirectRGB(68, 16, 32, rgbPixels(16, 32), ",U=1,c=2,r=2")))

	if x, y := term.CursorPos(); x != beforeX || y != beforeY {
		t.Errorf("cursor moved to (%d,%d): a virtual placement occupies no cells", x, y)
	}
	places := term.KittyPlacements()
	if len(places) != 1 {
		t.Fatalf("KittyPlacements() = %+v, want the virtual placement", places)
	}
	p := places[0]
	if !p.Virtual {
		t.Errorf("Virtual = false for a U=1 placement")
	}
	if p.ViewportVisible {
		t.Errorf("ViewportVisible = true for a virtual placement (row %d, col %d): the position is not real",
			p.ViewportRow, p.ViewportCol)
	}
	if p.GridCols != 2 || p.GridRows != 2 {
		t.Errorf("grid size = %dx%d, want the requested 2x2", p.GridCols, p.GridRows)
	}
}

// The protocol is dark until the wire can carry placements, so the observation
// surface has to stay quiet under the shipping configuration rather than fail.
//
// The generation is NOT zero here: applying the zero storage limit deletes
// every image and placement, and that deletion is itself a storage mutation
// that takes a stamp. A nonzero generation therefore never means "images
// exist" — only a changed generation means "look again".
func TestKittyObservationIsEmptyWhenDisabled(t *testing.T) {
	term := newKittyT(t, 20, 8, 0)
	disabledGen := term.KittyGeneration()

	term.Write([]byte(kittyDirectRGB(69, 16, 32, rgbPixels(16, 32), "")))

	if gen := term.KittyGeneration(); gen != disabledGen {
		t.Errorf("KittyGeneration() moved from %d to %d on a rejected transmit", disabledGen, gen)
	}
	if places := term.KittyPlacements(); places != nil {
		t.Errorf("KittyPlacements() = %+v with the protocol disabled, want none", places)
	}
	if img, ok := term.KittyImage(69); ok {
		t.Errorf("KittyImage(69) = %+v with the protocol disabled, want not found", img)
	}
}

// PNG is what real emitters send (f=100). Without the sys decode hook ghostty
// rejects the transmission outright, so this covers the hook install, the
// allocator handoff, and straight-alpha conversion: the translucent pixel is
// the one that changes value if the decoder ever premultiplies.
func TestKittyPNGTransmissionDecodesToStraightAlphaRGBA(t *testing.T) {
	term := newKittyT(t, 20, 8, kittyStorageLimit)
	if pngDecoderRC != 0 {
		t.Fatalf("PNG decode hook install returned rc=%d", int(pngDecoderRC))
	}

	src := image.NewNRGBA(image.Rect(0, 0, 3, 2))
	want := []color.NRGBA{
		{R: 255, G: 0, B: 0, A: 255}, {R: 0, G: 255, B: 0, A: 255}, {R: 0, G: 0, B: 255, A: 255},
		{R: 255, G: 0, B: 0, A: 128}, {R: 10, G: 20, B: 30, A: 40}, {R: 255, G: 255, B: 255, A: 0},
	}
	for i, c := range want {
		src.SetNRGBA(i%3, i/3, c)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, src); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}

	term.Write([]byte(fmt.Sprintf("\x1b_Ga=T,i=66,f=100,t=d;%s\x1b\\",
		base64.StdEncoding.EncodeToString(buf.Bytes()))))

	img, ok := term.KittyImage(66)
	if !ok {
		t.Fatalf("KittyImage(66) not found: the PNG transmission was rejected")
	}
	if img.Format != KittyImageRGBA {
		t.Errorf("Format = %d, want KittyImageRGBA (%d)", img.Format, KittyImageRGBA)
	}
	if img.Width != 3 || img.Height != 2 {
		t.Fatalf("decoded dims = %dx%d, want 3x2", img.Width, img.Height)
	}
	wantBytes := make([]byte, 0, len(want)*4)
	for _, c := range want {
		wantBytes = append(wantBytes, c.R, c.G, c.B, c.A)
	}
	if !bytes.Equal(img.Data, wantBytes) {
		t.Errorf("decoded pixels:\n got %v\nwant %v (straight alpha)", img.Data, wantBytes)
	}
	if len(term.KittyPlacements()) != 1 {
		t.Errorf("the PNG image was stored but not placed")
	}
}

// pngChunk frames one PNG chunk (length, type, data, CRC over type+data).
func pngChunk(typ string, data []byte) []byte {
	out := binary.BigEndian.AppendUint32(nil, uint32(len(data)))
	out = append(out, typ...)
	out = append(out, data...)
	return binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(append([]byte(typ), data...)))
}

// craftedPNG builds a syntactically valid 8-bit RGBA PNG whose IHDR claims the
// given dimensions, followed by a stub IDAT — the shape of a hostile payload: a
// few hundred bytes that make a decoder allocate width*height*4 before it can
// discover the pixel data is short. Nothing of that size is materialized here,
// which is the whole point of judging the header first.
func craftedPNG(t *testing.T, w, h uint32) []byte {
	t.Helper()
	ihdr := binary.BigEndian.AppendUint32(nil, w)
	ihdr = binary.BigEndian.AppendUint32(ihdr, h)
	ihdr = append(ihdr, 8, 6, 0, 0, 0) // bit depth 8, colour type 6 (RGBA), no interlace

	var idat bytes.Buffer
	zw := zlib.NewWriter(&idat)
	if _, err := zw.Write(make([]byte, 32)); err != nil {
		t.Fatalf("zlib write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zlib close: %v", err)
	}

	out := []byte("\x89PNG\r\n\x1a\n")
	out = append(out, pngChunk("IHDR", ihdr)...)
	out = append(out, pngChunk("IDAT", idat.Bytes())...)
	return append(out, pngChunk("IEND", nil)...)
}

// kittyTransmitPNG is a transmit-only (a=t) PNG payload: the tests below are
// about what reaches storage, not about grid effects.
func kittyTransmitPNG(id uint32, data []byte) string {
	return fmt.Sprintf("\x1b_Ga=t,i=%d,f=100,t=d;%s\x1b\\", id, base64.StdEncoding.EncodeToString(data))
}

// encodeWidePNG encodes a real, decodable w x 1 RGBA PNG. One row keeps the
// over-cap case cheap: 10001 pixels, not 10001 x 10001.
func encodeWidePNG(t *testing.T, w int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, 1))
	for x := 0; x < w; x++ {
		img.SetNRGBA(x, 0, color.NRGBA{R: uint8(x), G: 1, B: 2, A: 255})
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode(%dx1): %v", w, err)
	}
	return buf.Bytes()
}

// ghosttyMaxDimension is ghostty's own max_dimension at the pinned native
// ghostty (ab0b9da), src/terminal/kitty/graphics_image.zig:16. It is written
// out rather than read from maxKittyImageDimension on purpose: these two tests
// exist to catch our constant drifting away from ghostty's, and a test that
// follows the constant it is checking proves nothing.
const ghosttyMaxDimension = 10000

// Ghostty applies max_dimension AFTER decoding, so the hook has to apply it
// before. Either way the image is absent from storage — the kitty response is
// what says who refused it. "EINVAL: invalid data" is the decode hook's
// rejection; ghostty's own post-decode check answers "dimensions too large", so
// the response is the evidence that no pixel buffer was ever allocated. A cap
// set too high shows up right here.
func TestKittyPNGOversizedIsRejectedByTheDecodeHook(t *testing.T) {
	term := newKittyT(t, 20, 8, kittyStorageLimit)

	term.DrainResponses()
	term.Write([]byte(kittyTransmitPNG(80, encodeWidePNG(t, ghosttyMaxDimension+1))))
	resp := string(term.DrainResponses())

	if _, ok := term.KittyImage(80); ok {
		t.Errorf("a %dpx-wide image was stored; ghostty's max_dimension is %d",
			ghosttyMaxDimension+1, ghosttyMaxDimension)
	}
	if strings.Contains(resp, "dimensions too large") {
		t.Errorf("response = %q: ghostty rejected it after decoding, so the hook allocated the pixels first", resp)
	}
	if !strings.Contains(resp, "EINVAL: invalid data") {
		t.Errorf("response = %q, want the decode hook's rejection (EINVAL: invalid data)", resp)
	}
}

// The boundary in the other direction: exactly max_dimension still decodes and
// stores, so the mirrored cap cannot quietly drift below ghostty's and start
// refusing images ghostty would have accepted.
func TestKittyPNGAtTheDimensionCapIsAccepted(t *testing.T) {
	term := newKittyT(t, 20, 8, kittyStorageLimit)

	term.Write([]byte(kittyTransmitPNG(81, encodeWidePNG(t, ghosttyMaxDimension))))

	img, ok := term.KittyImage(81)
	if !ok {
		t.Fatalf("a %dpx-wide image was rejected; ghostty accepts up to max_dimension (%d)",
			ghosttyMaxDimension, ghosttyMaxDimension)
	}
	if img.Width != ghosttyMaxDimension || img.Height != 1 {
		t.Errorf("stored dims = %dx%d, want %dx1", img.Width, img.Height, ghosttyMaxDimension)
	}
}

// The hostile shape: a small payload claiming an enormous image. Rejected, and
// the terminal keeps working afterwards — a legitimate PNG right behind it
// still lands, so the refusal does not wedge ghostty's loading state.
func TestKittyPNGHostileHeaderIsRejectedAndHarmless(t *testing.T) {
	term := newKittyT(t, 20, 8, kittyStorageLimit)

	term.Write([]byte(kittyTransmitPNG(82, craftedPNG(t, 20000, 20000))))
	if _, ok := term.KittyImage(82); ok {
		t.Errorf("an image claiming 20000x20000 in its IHDR was stored")
	}

	term.Write([]byte(kittyTransmitPNG(83, encodeWidePNG(t, 4))))
	if _, ok := term.KittyImage(83); !ok {
		t.Errorf("a valid PNG after the hostile one was not stored: the rejection wedged the loader")
	}
}
