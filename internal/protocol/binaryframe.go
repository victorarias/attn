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

const binaryPtyHeaderBytes = 1 + 1 + 4 // type + id length + seq

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
