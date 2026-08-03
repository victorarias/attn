//go:build (darwin && arm64) || (linux && amd64) || (linux && arm64)

// Kitty graphics observation. The worker never implements kitty semantics: it
// reads back what ghostty's parser already decided — which images exist, where
// their placements landed — and diffs consecutive observations. Everything here
// is read-only with respect to the terminal.
//
// Handle lifetime is the whole design constraint. The storage handle and every
// image handle are borrowed from the terminal and die on the next mutating call
// (vt_write, resize, reset). So every exported method takes t.mu — the same lock
// every mutation takes — copies what it needs into Go memory, and returns owned
// data. Nothing borrowed escapes the lock.
package ghosttyvt

/*
#include <stdlib.h>
#include <string.h>
#include <ghostty/vt.h>

// Implemented in kitty_png.go. libghostty-vt calls it synchronously during
// vt_write when a kitty transmission carries a PNG payload (f=100).
extern bool goDecodePNG(void* userdata, const GhosttyAllocator* allocator, const uint8_t* data, size_t data_len, GhosttySysImage* out);

// Install the PNG decode hook. Process-global by design (ghostty_sys_set has no
// per-terminal form), hence the sync.Once on the Go side.
static GhosttyResult ghosttyvt_install_png_decoder(void) {
	return ghostty_sys_set(GHOSTTY_SYS_OPT_DECODE_PNG, (const void*)goDecodePNG);
}

// Borrowed image storage for the ACTIVE screen; NULL when kitty graphics are
// compiled out of the library (GHOSTTY_NO_VALUE) or the screen has no storage.
// Both degrade to "no images", never to an error: callers poll this per chunk.
static GhosttyKittyGraphics ghosttyvt_kitty_storage(GhosttyTerminal t) {
	GhosttyKittyGraphics g = NULL;
	if (ghostty_terminal_get(t, GHOSTTY_TERMINAL_DATA_KITTY_GRAPHICS, &g) != GHOSTTY_SUCCESS) return NULL;
	return g;
}

static uint64_t ghosttyvt_kitty_generation(GhosttyKittyGraphics g) {
	uint64_t v = 0;
	if (g == NULL) return 0;
	if (ghostty_kitty_graphics_get(g, GHOSTTY_KITTY_GRAPHICS_DATA_GENERATION, &v) != GHOSTTY_SUCCESS) return 0;
	return v;
}

typedef struct {
	uint32_t image_id;
	uint32_t placement_id;
	bool is_virtual;
	int32_t z;
	uint64_t image_generation;
	GhosttyResult render_rc;
	GhosttyKittyGraphicsPlacementRenderInfo info;
} ghosttyvt_kitty_placement;

// Read everything about the iterator's current placement in one crossing: four
// scalars via get_multi, then all of the geometry via render_info (which is
// itself the batched form of pixel_size/grid_size/viewport_pos/source_rect).
// Returns false only when the placement's own fields cannot be read; a
// placement whose geometry is unavailable still comes back, with zeroed
// geometry and the result code in render_rc.
static bool ghosttyvt_kitty_placement_read(
	GhosttyKittyGraphicsPlacementIterator it,
	GhosttyKittyGraphics g,
	GhosttyTerminal t,
	ghosttyvt_kitty_placement* out
) {
	memset(out, 0, sizeof(*out));
	const GhosttyKittyGraphicsPlacementData keys[4] = {
		GHOSTTY_KITTY_GRAPHICS_PLACEMENT_DATA_IMAGE_ID,
		GHOSTTY_KITTY_GRAPHICS_PLACEMENT_DATA_PLACEMENT_ID,
		GHOSTTY_KITTY_GRAPHICS_PLACEMENT_DATA_IS_VIRTUAL,
		GHOSTTY_KITTY_GRAPHICS_PLACEMENT_DATA_Z,
	};
	void* values[4] = {&out->image_id, &out->placement_id, &out->is_virtual, &out->z};
	if (ghostty_kitty_graphics_placement_get_multi(it, 4, keys, values, NULL) != GHOSTTY_SUCCESS) return false;

	out->info.size = sizeof(out->info);
	GhosttyKittyGraphicsImage img = ghostty_kitty_graphics_image(g, out->image_id);
	if (img == NULL) {
		out->render_rc = GHOSTTY_NO_VALUE;
		return true;
	}
	ghostty_kitty_graphics_image_get(img, GHOSTTY_KITTY_IMAGE_DATA_GENERATION, &out->image_generation);
	out->render_rc = ghostty_kitty_graphics_placement_render_info(it, img, t, &out->info);
	if (out->render_rc != GHOSTTY_SUCCESS) {
		// Undefined geometry must not read as a position at (0,0).
		memset(&out->info, 0, sizeof(out->info));
		out->info.size = sizeof(out->info);
	}
	return true;
}

typedef struct {
	uint32_t width;
	uint32_t height;
	GhosttyKittyImageFormat format;
	GhosttyKittyImageCompression compression;
	const uint8_t* data;
	size_t data_len;
	uint64_t generation;
} ghosttyvt_kitty_image;

// Look up one stored image and read its whole descriptor in a single crossing.
// data stays borrowed from the terminal: the caller copies it out before
// releasing the terminal lock.
static bool ghosttyvt_kitty_image_read(GhosttyKittyGraphics g, uint32_t id, ghosttyvt_kitty_image* out) {
	memset(out, 0, sizeof(*out));
	if (g == NULL) return false;
	GhosttyKittyGraphicsImage img = ghostty_kitty_graphics_image(g, id);
	if (img == NULL) return false;
	const GhosttyKittyGraphicsImageData keys[7] = {
		GHOSTTY_KITTY_IMAGE_DATA_WIDTH,
		GHOSTTY_KITTY_IMAGE_DATA_HEIGHT,
		GHOSTTY_KITTY_IMAGE_DATA_FORMAT,
		GHOSTTY_KITTY_IMAGE_DATA_COMPRESSION,
		GHOSTTY_KITTY_IMAGE_DATA_DATA_PTR,
		GHOSTTY_KITTY_IMAGE_DATA_DATA_LEN,
		GHOSTTY_KITTY_IMAGE_DATA_GENERATION,
	};
	void* values[7] = {
		&out->width,
		&out->height,
		&out->format,
		&out->compression,
		(void*)&out->data,
		&out->data_len,
		&out->generation,
	};
	return ghostty_kitty_graphics_image_get_multi(img, 7, keys, values, NULL) == GHOSTTY_SUCCESS;
}
*/
import "C"

import (
	"sync"
	"unsafe"
)

// pngDecoderOnce guards the process-global PNG decode hook. ghostty_sys_set has
// no per-terminal form, so the install happens once, from the first New.
var (
	pngDecoderOnce sync.Once
	pngDecoderRC   C.GhosttyResult
)

func installPNGDecoder() {
	pngDecoderOnce.Do(func() { pngDecoderRC = C.ghosttyvt_install_png_decoder() })
}

// KittyImageFormat is the pixel layout of a stored kitty image. PNG is absent
// on purpose: ghostty decodes PNG payloads to RGBA before storing, so a stored
// image is always raw pixels.
type KittyImageFormat uint8

const (
	KittyImageRGB KittyImageFormat = iota
	KittyImageRGBA
	KittyImageGrayAlpha
	KittyImageGray
)

// KittyPlacement is one placement observed in the active screen's storage,
// geometry resolved at observation time (viewport-relative; scrolling moves
// placements between observations without changing the generation stamp).
//
// ImageGeneration is PROCESS-LOCAL, like every stamp in this file: ghostty
// numbers them per process, so two terminals mint the same numbers for
// different pixels. internal/pty folds a per-terminal-instance epoch into every
// generation before it leaves the worker (mintKittyEpoch in
// internal/pty/kitty.go); nothing here does, and nothing here should.
type KittyPlacement struct {
	ImageID         uint32
	PlacementID     uint32
	Virtual         bool // unicode-placeholder placement; no cursor geometry
	Z               int32
	PixelWidth      uint32 // rendered size (source rect + aspect applied)
	PixelHeight     uint32
	GridCols        uint32
	GridRows        uint32
	ViewportCol     int32 // top-left, viewport-relative; negative = scrolled up
	ViewportRow     int32
	ViewportVisible bool   // false: fully off-screen or virtual
	SourceX         uint32 // resolved source rect in image pixels
	SourceY         uint32
	SourceWidth     uint32
	SourceHeight    uint32
	ImageGeneration uint64 // per-image stamp; changes on any retransmission of the id
}

// KittyImage is a decoded, uncompressed image copied out of the storage.
// Format is never PNG (ghostty decodes PNG to RGBA before storing) and the
// data is never compressed. Generation is the same process-local stamp
// KittyPlacement.ImageGeneration carries, and gets the same epoch folded into
// it on the way out of the worker.
type KittyImage struct {
	ID         uint32
	Width      uint32
	Height     uint32
	Format     KittyImageFormat
	Generation uint64
	Data       []byte
}

// KittyGeneration returns the storage-wide generation stamp of the ACTIVE
// screen's kitty image storage. Zero means never mutated (and thus empty).
// Stamps are unique and monotonically increasing process-wide; an unchanged
// value guarantees the placement set and all image data are identical.
//
// The converse does not hold: a nonzero stamp does not mean images exist.
// Applying a zero storage limit deletes everything, and that deletion takes a
// stamp of its own — so a terminal with kitty disabled reports nonzero and
// empty. Only a *changed* stamp means "observe again".
//
// Unlike the per-image stamps, this one is used raw wherever it is read: it is
// a change detector that never leaves the process, so it needs none of the
// identity epoching internal/pty applies to what it describes to clients.
func (t *Terminal) KittyGeneration() uint64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return 0
	}
	return uint64(C.ghosttyvt_kitty_generation(C.ghosttyvt_kitty_storage(t.term)))
}

// KittyPlacements snapshots every placement in the active screen's storage,
// in iterator order. All data is copied out; the result stays valid across
// later terminal mutations.
//
// This is the one place that creates a placement iterator, so "every path frees
// it" is a property of this function alone. The free is deferred in a closure
// rather than by value because populating the iterator may replace the handle.
func (t *Terminal) KittyPlacements() []KittyPlacement {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil
	}
	g := C.ghosttyvt_kitty_storage(t.term)
	if g == nil {
		return nil
	}
	var it C.GhosttyKittyGraphicsPlacementIterator
	if rc := C.ghostty_kitty_graphics_placement_iterator_new(nil, &it); rc != C.GHOSTTY_SUCCESS {
		return nil
	}
	defer func() { C.ghostty_kitty_graphics_placement_iterator_free(it) }()
	if rc := C.ghostty_kitty_graphics_get(g, C.GHOSTTY_KITTY_GRAPHICS_DATA_PLACEMENT_ITERATOR, unsafe.Pointer(&it)); rc != C.GHOSTTY_SUCCESS {
		return nil
	}

	var out []KittyPlacement
	var rec C.ghosttyvt_kitty_placement
	for C.ghostty_kitty_graphics_placement_next(it) {
		if !C.ghosttyvt_kitty_placement_read(it, g, t.term, &rec) {
			continue
		}
		out = append(out, KittyPlacement{
			ImageID:         uint32(rec.image_id),
			PlacementID:     uint32(rec.placement_id),
			Virtual:         bool(rec.is_virtual),
			Z:               int32(rec.z),
			PixelWidth:      uint32(rec.info.pixel_width),
			PixelHeight:     uint32(rec.info.pixel_height),
			GridCols:        uint32(rec.info.grid_cols),
			GridRows:        uint32(rec.info.grid_rows),
			ViewportCol:     int32(rec.info.viewport_col),
			ViewportRow:     int32(rec.info.viewport_row),
			ViewportVisible: bool(rec.info.viewport_visible),
			SourceX:         uint32(rec.info.source_x),
			SourceY:         uint32(rec.info.source_y),
			SourceWidth:     uint32(rec.info.source_width),
			SourceHeight:    uint32(rec.info.source_height),
			ImageGeneration: uint64(rec.image_generation),
		})
	}
	return out
}

// KittyImage looks up a stored image by ghostty's image id. ok is false when
// no such image exists (or on the stub).
func (t *Terminal) KittyImage(id uint32) (KittyImage, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return KittyImage{}, false
	}
	g := C.ghosttyvt_kitty_storage(t.term)
	if g == nil {
		return KittyImage{}, false
	}
	var rec C.ghosttyvt_kitty_image
	if !C.ghosttyvt_kitty_image_read(g, C.uint32_t(id), &rec) {
		return KittyImage{}, false
	}
	// Both branches are documented as impossible for a stored image (PNG is
	// decoded and zlib is inflated at transmission time). Reporting "no image"
	// beats handing a consumer bytes whose layout does not match the format it
	// was told.
	format, ok := kittyFormat(rec.format)
	if !ok || rec.compression != C.GHOSTTY_KITTY_IMAGE_COMPRESSION_NONE {
		return KittyImage{}, false
	}
	var data []byte
	if rec.data != nil && rec.data_len > 0 {
		data = C.GoBytes(unsafe.Pointer(rec.data), C.int(rec.data_len))
	}
	return KittyImage{
		ID:         id,
		Width:      uint32(rec.width),
		Height:     uint32(rec.height),
		Format:     format,
		Generation: uint64(rec.generation),
		Data:       data,
	}, true
}

func kittyFormat(f C.GhosttyKittyImageFormat) (KittyImageFormat, bool) {
	switch f {
	case C.GHOSTTY_KITTY_IMAGE_FORMAT_RGB:
		return KittyImageRGB, true
	case C.GHOSTTY_KITTY_IMAGE_FORMAT_RGBA:
		return KittyImageRGBA, true
	case C.GHOSTTY_KITTY_IMAGE_FORMAT_GRAY_ALPHA:
		return KittyImageGrayAlpha, true
	case C.GHOSTTY_KITTY_IMAGE_FORMAT_GRAY:
		return KittyImageGray, true
	default:
		return 0, false
	}
}
