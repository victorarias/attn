package protocol

import (
	"bytes"
	"strings"
	"testing"
)

func TestPtyOutputFrameRoundTrip(t *testing.T) {
	payload := []byte("hello \x1b[31mworld\x1b[0m\r\n\x00\xff")
	frame, err := EncodePtyOutputFrame("sess-42", 123456789, payload)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	id, seq, data, err := DecodePtyOutputFrame(frame)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if id != "sess-42" {
		t.Errorf("session id = %q, want %q", id, "sess-42")
	}
	if seq != 123456789 {
		t.Errorf("seq = %d, want 123456789", seq)
	}
	if !bytes.Equal(data, payload) {
		t.Errorf("data = %q, want %q", data, payload)
	}
}

func TestPtyOutputFrameEmptyData(t *testing.T) {
	frame, err := EncodePtyOutputFrame("s", 0, nil)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	id, seq, data, err := DecodePtyOutputFrame(frame)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if id != "s" || seq != 0 || len(data) != 0 {
		t.Errorf("got id=%q seq=%d len(data)=%d", id, seq, len(data))
	}
}

func TestEncodePtyOutputFrameRejectsBadID(t *testing.T) {
	if _, err := EncodePtyOutputFrame("", 1, []byte("x")); err == nil {
		t.Error("expected error for empty session id")
	}
	if _, err := EncodePtyOutputFrame(strings.Repeat("a", 256), 1, []byte("x")); err == nil {
		t.Error("expected error for 256-byte session id")
	}
}

func TestDecodePtyOutputFrameRejectsMalformed(t *testing.T) {
	valid, err := EncodePtyOutputFrame("abc", 7, []byte("data"))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	cases := map[string][]byte{
		"empty":              {},
		"short":              valid[:5],
		"wrong type":         append([]byte{0x7f}, valid[1:]...),
		"id overruns frame":  {BinaryFrameTypePtyOutput, 200, 'a', 'b', 0, 0, 0, 1},
		"zero id length":     {BinaryFrameTypePtyOutput, 0, 0, 0, 0, 1, 'x'},
		"truncated post-id":  valid[:7],
	}
	for name, frame := range cases {
		if _, _, _, err := DecodePtyOutputFrame(frame); err == nil {
			t.Errorf("%s: expected decode error", name)
		}
	}
}
