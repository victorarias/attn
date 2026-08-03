package protocol

import (
	"encoding/binary"
	"fmt"
)

// Binary websocket frames carry high-volume PTY output to clients that
// advertised CapabilityBinaryPtyOutput in client_hello. Unlike every other
// daemon event, these are not JSON: base64-in-JSON costs a 33% size
// inflation plus a large transient-allocation churn (envelope string, base64
// string, decoded bytes) on both sides of the socket for every chunk.
//
// Frame layout (big-endian):
//
//	offset 0      frame type (1 byte) — BinaryFrameTypePtyOutput
//	offset 1      session id length L (1 byte)
//	offset 2      session id (L bytes, UTF-8)
//	offset 2+L    seq (4 bytes, uint32)
//	offset 6+L    raw PTY bytes (rest of frame)
//
// Clients that did not opt in keep receiving the JSON pty_output event.
const BinaryFrameTypePtyOutput byte = 0x01

// Kitty image blobs take the same route for the same reason, scaled up: a
// stored image is decoded raw pixels, so a measured real emission is 1.9–6.5MB
// and base64-in-JSON would add a third of that plus a multi-megabyte
// JSON.parse stall on the UI thread. One frame carries one whole image; there
// is no chunking, and none of it is retained by the decoder.
//
// Frame layout (big-endian):
//
//	offset 0       frame type (1 byte) — BinaryFrameTypeKittyImage
//	offset 1       session id length L (1 byte)
//	offset 2       session id (L bytes, UTF-8)
//	offset 2+L     image id (4 bytes, uint32)
//	offset 6+L     image generation (8 bytes, uint64)
//	offset 14+L    width in pixels (4 bytes, uint32)
//	offset 18+L    height in pixels (4 bytes, uint32)
//	offset 22+L    pixel format (1 byte, KittyImageFormatCode*)
//	offset 23+L    raw pixels (rest of frame)
//
// There is no request id here or on the JSON fallback: an answer is matched to
// what asked for it by CONTENT KEY — (session id, image id, generation) — which
// is the same key a client's blob cache is keyed on, so a duplicate answer is
// an idempotent cache fill rather than an orphan.
//
// Clients that did not advertise CapabilityKittyImages get the base64
// kitty_image_result event instead.
const BinaryFrameTypeKittyImage byte = 0x02

const binaryPtyHeaderBytes = 1 + 1 + 4 // type + id length + seq

// type + id length + image id + generation + width + height + format
const binaryKittyImageHeaderBytes = 1 + 1 + 4 + 8 + 4 + 4 + 1

// Kitty pixel layouts as the client protocol names and codes them. PNG is
// absent on purpose: ghostty decodes PNG and inflates zlib before storing, so a
// stored image is always one of these four raw layouts.
//
// The codes are the daemon's own, assigned here and translated from ghostty's
// enum by an explicit switch at the boundary. Passing ghostty's value through
// would make a pin that reorders its enum silently reinterpret every client's
// pixels as a different layout.
const (
	KittyImageFormatCodeRGB       byte = 0
	KittyImageFormatCodeRGBA      byte = 1
	KittyImageFormatCodeGrayAlpha byte = 2
	KittyImageFormatCodeGray      byte = 3
)

// kittyImageFormatNames indexes the wire names by format code. One table, so a
// layout added here cannot get a code without also getting a name.
var kittyImageFormatNames = [...]string{
	KittyImageFormatCodeRGB:       "rgb",
	KittyImageFormatCodeRGBA:      "rgba",
	KittyImageFormatCodeGrayAlpha: "gray_alpha",
	KittyImageFormatCodeGray:      "gray",
}

// KittyImageFormatName maps a format code to the name the JSON
// kitty_image_result carries. Both transports describe the same four layouts,
// so the JSON answer names what the binary frame would have coded.
func KittyImageFormatName(code byte) (string, bool) {
	if int(code) >= len(kittyImageFormatNames) {
		return "", false
	}
	return kittyImageFormatNames[code], true
}

// EncodeKittyImageFrame builds a binary kitty image frame. It rejects a format
// code it has no name for: an unknown layout reaching a client is pixels
// interpreted with the wrong stride, which renders as plausible garbage rather
// than failing.
func EncodeKittyImageFrame(sessionID string, imageID uint32, generation uint64, width, height uint32, format byte, pixels []byte) ([]byte, error) {
	if len(sessionID) == 0 || len(sessionID) > 255 {
		return nil, fmt.Errorf("session id length %d out of range [1,255]", len(sessionID))
	}
	if _, ok := KittyImageFormatName(format); !ok {
		return nil, fmt.Errorf("unknown kitty image format code %d", format)
	}
	// Everything after the header is the image, so a frame with nothing after
	// it describes an image that cannot be drawn. Saying so here beats a client
	// sizing a texture from the header and uploading no pixels into it.
	if len(pixels) == 0 {
		return nil, fmt.Errorf("kitty image %d carries no pixels for its %dx%d header", imageID, width, height)
	}
	frame := make([]byte, binaryKittyImageHeaderBytes+len(sessionID)+len(pixels))
	frame[0] = BinaryFrameTypeKittyImage
	frame[1] = byte(len(sessionID))
	offset := 2 + copy(frame[2:], sessionID)
	binary.BigEndian.PutUint32(frame[offset:], imageID)
	binary.BigEndian.PutUint64(frame[offset+4:], generation)
	binary.BigEndian.PutUint32(frame[offset+12:], width)
	binary.BigEndian.PutUint32(frame[offset+16:], height)
	frame[offset+20] = format
	copy(frame[offset+21:], pixels)
	return frame, nil
}

// KittyImageFrame is a decoded kitty image frame. Pixels aliases the input
// frame; callers must not retain it past the frame's lifetime.
type KittyImageFrame struct {
	SessionID  string
	ImageID    uint32
	Generation uint64
	Width      uint32
	Height     uint32
	Format     byte
	Pixels     []byte
}

// DecodeKittyImageFrame parses a binary kitty image frame. Go has no client
// that decodes one — the app does, in TypeScript — so this exists to pin the
// layout against the encoder from the same table the encoder wrote.
func DecodeKittyImageFrame(frame []byte) (KittyImageFrame, error) {
	if len(frame) < binaryKittyImageHeaderBytes+1 {
		return KittyImageFrame{}, fmt.Errorf("frame too short: %d bytes", len(frame))
	}
	if frame[0] != BinaryFrameTypeKittyImage {
		return KittyImageFrame{}, fmt.Errorf("unknown binary frame type 0x%02x", frame[0])
	}
	idLen := int(frame[1])
	if idLen == 0 || len(frame) <= binaryKittyImageHeaderBytes+idLen {
		return KittyImageFrame{}, fmt.Errorf("frame too short for id length %d: %d bytes", idLen, len(frame))
	}
	offset := 2 + idLen
	format := frame[offset+20]
	if _, ok := KittyImageFormatName(format); !ok {
		return KittyImageFrame{}, fmt.Errorf("unknown kitty image format code %d", format)
	}
	return KittyImageFrame{
		SessionID:  string(frame[2:offset]),
		ImageID:    binary.BigEndian.Uint32(frame[offset:]),
		Generation: binary.BigEndian.Uint64(frame[offset+4:]),
		Width:      binary.BigEndian.Uint32(frame[offset+12:]),
		Height:     binary.BigEndian.Uint32(frame[offset+16:]),
		Format:     format,
		Pixels:     frame[binaryKittyImageHeaderBytes+idLen:],
	}, nil
}

// EncodePtyOutputFrame builds a binary pty_output frame.
func EncodePtyOutputFrame(sessionID string, seq uint32, data []byte) ([]byte, error) {
	if len(sessionID) == 0 || len(sessionID) > 255 {
		return nil, fmt.Errorf("session id length %d out of range [1,255]", len(sessionID))
	}
	frame := make([]byte, binaryPtyHeaderBytes+len(sessionID)+len(data))
	frame[0] = BinaryFrameTypePtyOutput
	frame[1] = byte(len(sessionID))
	offset := 2 + copy(frame[2:], sessionID)
	binary.BigEndian.PutUint32(frame[offset:], seq)
	copy(frame[offset+4:], data)
	return frame, nil
}

// DecodePtyOutputFrame parses a binary pty_output frame. The returned data
// aliases the input frame; callers must not retain it past the frame's
// lifetime.
func DecodePtyOutputFrame(frame []byte) (sessionID string, seq uint32, data []byte, err error) {
	if len(frame) < binaryPtyHeaderBytes+1 {
		return "", 0, nil, fmt.Errorf("frame too short: %d bytes", len(frame))
	}
	if frame[0] != BinaryFrameTypePtyOutput {
		return "", 0, nil, fmt.Errorf("unknown binary frame type 0x%02x", frame[0])
	}
	idLen := int(frame[1])
	if idLen == 0 || len(frame) < binaryPtyHeaderBytes+idLen {
		return "", 0, nil, fmt.Errorf("frame too short for id length %d: %d bytes", idLen, len(frame))
	}
	sessionID = string(frame[2 : 2+idLen])
	seq = binary.BigEndian.Uint32(frame[2+idLen:])
	data = frame[binaryPtyHeaderBytes+idLen:]
	return sessionID, seq, data, nil
}
