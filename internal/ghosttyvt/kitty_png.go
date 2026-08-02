//go:build (darwin && arm64) || (linux && amd64) || (linux && arm64)

package ghosttyvt

/*
#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>
#include <ghostty/vt.h>
*/
import "C"

import (
	"bytes"
	"image"
	"image/draw"
	"image/png"
	"unsafe"
)

// Ghostty's own limits for a kitty image, mirrored here so the hook refuses
// before allocating what ghostty would refuse after decode. At the pinned
// native ghostty (ab0b9da), src/terminal/kitty/graphics_image.zig:
//
//	const max_dimension = 10000;         // "Maximum width or height of an image. Taken directly from Kitty."
//	const max_size = 400 * 1024 * 1024;  // "Maximum size in bytes, taken from Kitty."
//
// and LoadingImage.complete() decodes PNG *first* (line 416) and validates
// dimensions only afterwards (lines 418-420). Ghostty cannot pre-check what it
// has not decoded — this hook is the decoder — so a payload of a few hundred
// bytes whose IHDR claims 20000x20000 would otherwise size a 1.6GB allocation
// inside vt_write for an image ghostty is about to reject anyway.
//
// The dimension bound is the binding one: 10000x10000 RGBA is 4e8 bytes, under
// max_size already, and it keeps every byte count and both uint32 out fields
// far inside their range. The size bound is mirrored too so the pair keeps
// matching ghostty if either constant moves.
const (
	maxKittyImageDimension = 10000
	maxKittyImageBytes     = 400 * 1024 * 1024
)

// goDecodePNG is libghostty-vt's PNG decode hook (GHOSTTY_SYS_OPT_DECODE_PNG),
// installed once per process. Ghostty calls it synchronously from vt_write when
// a kitty transmission carries f=100, then stores the decoded pixels as RGBA.
//
// The output buffer MUST come from the allocator ghostty passes in: ghostty
// takes ownership and frees it with that same allocator (a Go-allocated buffer
// or a plain malloc would be a use-after-free or a cross-heap free). On any
// failure the buffer is not allocated and false tells ghostty to reject the
// image.
//
// The bytes are attacker-controlled — any program on the PTY can send a
// deliberately malformed PNG — so a panic anywhere in the decode is recovered
// into a plain rejection rather than taking the worker down.
//
//export goDecodePNG
func goDecodePNG(userdata unsafe.Pointer, allocator *C.GhosttyAllocator, data *C.uint8_t, dataLen C.size_t, out *C.GhosttySysImage) (ok C._Bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	if data == nil || dataLen == 0 || out == nil {
		return false
	}

	raw := C.GoBytes(unsafe.Pointer(data), C.int(dataLen))

	// DecodeConfig reads the header only; png.Decode allocates the whole pixel
	// buffer from those same header dimensions before producing a single pixel,
	// so the size the sender claims has to be judged before that call, not after.
	cfg, err := png.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return false
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return false
	}
	if cfg.Width > maxKittyImageDimension || cfg.Height > maxKittyImageDimension {
		return false
	}
	if int64(cfg.Width)*int64(cfg.Height)*4 > maxKittyImageBytes {
		return false
	}

	src, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		return false
	}
	b := src.Bounds()
	if b.Dx() <= 0 || b.Dy() <= 0 {
		return false
	}
	// NRGBA is straight (non-premultiplied) alpha, which is what the kitty
	// protocol's RGBA format means. Converting via image.RGBA instead would
	// hand ghostty premultiplied pixels and darken every translucent image.
	rgba := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(rgba, rgba.Bounds(), src, b.Min, draw.Src)

	buf := C.ghostty_alloc(allocator, C.size_t(len(rgba.Pix)))
	if buf == nil {
		return false
	}
	copy(unsafe.Slice((*byte)(unsafe.Pointer(buf)), len(rgba.Pix)), rgba.Pix)
	out.width = C.uint32_t(b.Dx())
	out.height = C.uint32_t(b.Dy())
	out.data = buf
	out.data_len = C.size_t(len(rgba.Pix))
	return true
}
