package protocol

import (
	"bytes"
	"testing"
)

// A kitty image frame is a header followed by raw pixels with no length prefix
// and no self-describing structure: everything after the header IS the image.
// So every field boundary in the header has to be exactly where the decoder
// thinks it is — an off-by-one there does not fail, it hands the client the
// right pixels with the wrong stride, which renders as plausible garbage.
func TestKittyImageFrameRoundTripsEveryPixelLayout(t *testing.T) {
	pixels := []byte{0xde, 0xad, 0xbe, 0xef, 0x00, 0x01, 0x7f, 0x80}
	layouts := map[byte]string{
		KittyImageFormatCodeRGB:       "rgb",
		KittyImageFormatCodeRGBA:      "rgba",
		KittyImageFormatCodeGrayAlpha: "gray_alpha",
		KittyImageFormatCodeGray:      "gray",
	}

	for code, wantName := range layouts {
		frame, err := EncodeKittyImageFrame("sess-1", 4242, 1<<40, 1800, 1200, code, pixels)
		if err != nil {
			t.Fatalf("encode format %d: %v", code, err)
		}
		decoded, err := DecodeKittyImageFrame(frame)
		if err != nil {
			t.Fatalf("decode format %d: %v", code, err)
		}
		if decoded.SessionID != "sess-1" {
			t.Errorf("format %d: session id = %q, want sess-1", code, decoded.SessionID)
		}
		if decoded.ImageID != 4242 {
			t.Errorf("format %d: image id = %d, want 4242", code, decoded.ImageID)
		}
		// Past uint32: a generation truncated to 32 bits collides with an
		// earlier one and a client's cache serves the stale pixels forever.
		if decoded.Generation != 1<<40 {
			t.Errorf("format %d: generation = %d, want %d", code, decoded.Generation, uint64(1)<<40)
		}
		if decoded.Width != 1800 || decoded.Height != 1200 {
			t.Errorf("format %d: size = %dx%d, want 1800x1200", code, decoded.Width, decoded.Height)
		}
		if decoded.Format != code {
			t.Errorf("format %d: decoded format = %d", code, decoded.Format)
		}
		if !bytes.Equal(decoded.Pixels, pixels) {
			t.Errorf("format %d: pixels = %x, want %x", code, decoded.Pixels, pixels)
		}
		if name, ok := KittyImageFormatName(code); !ok || name != wantName {
			t.Errorf("format %d: name = %q (ok=%v), want %q", code, name, ok, wantName)
		}
	}
}

// Session ids vary in length and the header shifts with them, so a decoder that
// hardcodes an offset reads the image id out of the session id's bytes.
func TestKittyImageFrameSurvivesSessionIDLengths(t *testing.T) {
	pixels := []byte{1, 2, 3, 4}
	for _, id := range []string{"a", "session-with-a-long-name", string(bytes.Repeat([]byte("x"), 255))} {
		frame, err := EncodeKittyImageFrame(id, 7, 9, 2, 2, KittyImageFormatCodeRGBA, pixels)
		if err != nil {
			t.Fatalf("encode id len %d: %v", len(id), err)
		}
		decoded, err := DecodeKittyImageFrame(frame)
		if err != nil {
			t.Fatalf("decode id len %d: %v", len(id), err)
		}
		if decoded.SessionID != id || decoded.ImageID != 7 || decoded.Generation != 9 {
			t.Fatalf("id len %d: got %+v", len(id), decoded)
		}
		if !bytes.Equal(decoded.Pixels, pixels) {
			t.Fatalf("id len %d: pixels = %x, want %x", len(id), decoded.Pixels, pixels)
		}
	}
}

func TestEncodeKittyImageFrameRejectsUnservableFrames(t *testing.T) {
	if _, err := EncodeKittyImageFrame("", 1, 1, 1, 1, KittyImageFormatCodeRGB, []byte{0}); err == nil {
		t.Error("encoding an empty session id succeeded, want an error")
	}
	if _, err := EncodeKittyImageFrame(string(bytes.Repeat([]byte("x"), 256)), 1, 1, 1, 1, KittyImageFormatCodeRGB, []byte{0}); err == nil {
		t.Error("encoding a 256-byte session id succeeded, want an error")
	}
	// A layout the protocol has no name for would reach the client as pixels
	// with an unknowable stride, so it never leaves the daemon.
	if _, err := EncodeKittyImageFrame("sess-1", 1, 1, 1, 1, 9, []byte{0}); err == nil {
		t.Error("encoding an unknown format code succeeded, want an error")
	}
	// A header with nothing behind it sizes a texture the client then has
	// nothing to fill.
	if _, err := EncodeKittyImageFrame("sess-1", 1, 1, 4, 4, KittyImageFormatCodeRGB, nil); err == nil {
		t.Error("encoding a pixel-less image succeeded, want an error")
	}
}

func TestDecodeKittyImageFrameRejectsMalformedFrames(t *testing.T) {
	good, err := EncodeKittyImageFrame("sess-1", 4242, 7, 4, 2, KittyImageFormatCodeRGB, []byte{1, 2, 3, 4})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	cases := map[string][]byte{
		"empty":                  {},
		"header only":            good[:len(good)-4],
		"truncated mid-header":   good[:6],
		"zero session id length": func() []byte { f := append([]byte(nil), good...); f[1] = 0; return f }(),
		"session id length past the frame": func() []byte {
			f := append([]byte(nil), good...)
			f[1] = 255
			return f
		}(),
		"unknown format code": func() []byte {
			f := append([]byte(nil), good...)
			f[2+len("sess-1")+20] = 9
			return f
		}(),
		"pty output frame": func() []byte {
			frame, err := EncodePtyOutputFrame("sess-1", 1, []byte("hello"))
			if err != nil {
				t.Fatalf("encode pty frame: %v", err)
			}
			return frame
		}(),
	}

	for name, frame := range cases {
		if _, err := DecodeKittyImageFrame(frame); err == nil {
			t.Errorf("%s: decoded without error, want a rejection", name)
		}
	}

	// The other direction too: the type byte is the only thing separating a
	// multi-megabyte blob from terminal bytes about to be written to a screen.
	if _, _, _, err := DecodePtyOutputFrame(good); err == nil {
		t.Error("pty output decoder accepted a kitty image frame, want a rejection")
	}
}

func TestKittyImageFormatNameRejectsUnknownCodes(t *testing.T) {
	if _, ok := KittyImageFormatName(4); ok {
		t.Error("format code 4 has a name, want none")
	}
	if _, ok := KittyImageFormatName(255); ok {
		t.Error("format code 255 has a name, want none")
	}
}
