// Where a session's kitty images sit on the client's grid.
//
// The client never anchors a placement itself. Command blocks re-anchor by
// their command text; an image has no text, and the model's scrollback is
// byte-capped with no eviction counter, so an absolute row held across a long
// session drifts by an unknowable amount. Instead the worker re-observes its
// own grid and describes the WHOLE set whenever anything moved, and this store
// replaces its set wholesale at each apply — mapping viewport rows to client
// buffer rows against the scrollback length read fresh at that moment.
//
// Between applies a placement only moves with client-side scrolling, which the
// buffer row already accounts for. A placement whose mapped row falls above the
// history the client still retains is dropped: correct or absent, never guessed.

import type { PlacementElement } from '../types/generated';

/** One placement as the client holds it: buffer-anchored and drawable. */
export interface PlacedKittyImage {
  imageId: number;
  placementId: number;
  /** Per-image stamp; changes on any retransmission of the id. */
  generation: number;
  z: number;
  /** Absolute client buffer row of the placement's top-left cell. */
  bufferRow: number;
  col: number;
  /** Rendered size in image pixels — the only size signal; grid cells are advisory. */
  pixelWidth: number;
  pixelHeight: number;
  /** Source sub-rect in image pixels. All-zero width/height means the whole image. */
  sourceX: number;
  sourceY: number;
  sourceWidth: number;
  sourceHeight: number;
}

// A wire set that has never been applied. Every seq the daemon sends is a
// uint32, so nothing real compares below this.
const NO_SEQ = -1;

export class KittyPlacementStore {
  private placed: PlacedKittyImage[] = [];
  private appliedSeq = NO_SEQ;

  /**
   * Apply one described set. Accepts seq >= the last applied because a resize
   * re-describes at the replay watermark, so equal seqs happen and both
   * descriptions are truthful. Returns whether the set was taken, so the caller
   * knows whether to repaint.
   */
  apply(seq: number, wire: readonly PlacementElement[], scrollbackLength: number): boolean {
    if (seq < this.appliedSeq) return false;
    this.appliedSeq = seq;
    this.placed = mapPlacements(wire, scrollbackLength);
    return true;
  }

  /**
   * Seed from a restore snapshot. A restore resets the model, so the snapshot's
   * set (or the empty set when it carries none) is the whole truth — anything
   * held from before the reattach would draw over a terminal that no longer has
   * it. The seq gate resets with it: the live stream resumes from the attach
   * watermark, and holding a pre-restore seq would reject its first descriptions.
   */
  seed(wire: readonly PlacementElement[], scrollbackLength: number): void {
    this.appliedSeq = NO_SEQ;
    this.placed = mapPlacements(wire, scrollbackLength);
  }

  placements(): readonly PlacedKittyImage[] {
    return this.placed;
  }

  lastAppliedSeq(): number {
    return this.appliedSeq;
  }

  /** Drop everything, e.g. after a reflow renumbered every row this store holds. */
  clear(): void {
    this.placed = [];
    this.appliedSeq = NO_SEQ;
  }
}

function mapPlacements(
  wire: readonly PlacementElement[],
  scrollbackLength: number,
): PlacedKittyImage[] {
  const placed: PlacedKittyImage[] = [];
  for (const p of wire) {
    // A unicode-placeholder placement has no cursor geometry — its cells are
    // drawn by the program, not by us.
    if (p.virtual) continue;
    if (p.pixel_width <= 0 || p.pixel_height <= 0) continue;
    // viewport_row is screen-relative on the worker's grid, which equals the
    // client's by the grid-equality invariant, and goes negative once the
    // placement scrolls above the screen. The equality holds across resizes
    // too: every frame — the worker's and every client's — resizes without
    // reflow, so a width change never re-wraps one grid's history and not
    // another's.
    const bufferRow = scrollbackLength + p.viewport_row;
    if (bufferRow < 0) continue;
    placed.push({
      imageId: p.image_id,
      placementId: p.placement_id,
      generation: p.image_generation,
      z: p.z,
      bufferRow,
      col: p.viewport_col,
      pixelWidth: p.pixel_width,
      pixelHeight: p.pixel_height,
      sourceX: p.source_x,
      sourceY: p.source_y,
      sourceWidth: p.source_width,
      sourceHeight: p.source_height,
    });
  }
  // Draw order, and the order every report of the set uses. The renderer reads
  // z again to pick which of kitty's three layers an image lands in — over the
  // text at z >= 0, under the text below that, under non-default cell
  // backgrounds too past KITTY_Z_UNDER_BACKGROUND — so this sort is what orders
  // images against each other within a layer; the placement id breaks ties so
  // the order never depends on how the worker's iterator happened to walk the
  // storage.
  placed.sort((a, b) => (a.z - b.z) || (a.placementId - b.placementId));
  return placed;
}

/**
 * A placement's drawable rectangle in the pane, clipped to the grid's pixel box.
 * u/v are the fractions of the placement's rendered box that survived clipping,
 * in texture order (u across, v down); placementSourceRect turns them into a
 * source sub-rect once the texture's real size is known.
 */
export interface PlacementQuad {
  placement: PlacedKittyImage;
  /** Destination rect in CSS pixels, relative to the grid's top-left. */
  x: number;
  y: number;
  width: number;
  height: number;
  u0: number;
  v0: number;
  u1: number;
  v1: number;
}

/**
 * Where a placement lands on screen, or null when it does not intersect the
 * viewport at all. Size comes from the rendered pixel size: grid_cols/grid_rows
 * are 0 on a fresh placement (kitty "natural size" — ghostty resolves cell
 * counts only on reflow) and are advisory even when set.
 *
 * This is the unit boundary. A placement is measured in DEVICE pixels — the
 * emitter sized it against the pixel geometry the PTY reports — while the cell
 * metrics and the grid box are CSS pixels, so the placement's box is divided by
 * devicePixelRatio here, once, before any of the clipping arithmetic mixes the
 * two. Drawn without it, an image lands devicePixelRatio times too large.
 */
export function placementQuad(
  placement: PlacedKittyImage,
  firstVisibleBufferRow: number,
  cellWidth: number,
  cellHeight: number,
  gridWidth: number,
  gridHeight: number,
  devicePixelRatio: number,
): PlacementQuad | null {
  const scale = devicePixelRatio > 0 ? devicePixelRatio : 1;
  const x = placement.col * cellWidth;
  const y = (placement.bufferRow - firstVisibleBufferRow) * cellHeight;
  const width = placement.pixelWidth / scale;
  const height = placement.pixelHeight / scale;
  const left = Math.max(x, 0);
  const top = Math.max(y, 0);
  const right = Math.min(x + width, gridWidth);
  const bottom = Math.min(y + height, gridHeight);
  if (right <= left || bottom <= top) return null;
  return {
    placement,
    x: left,
    y: top,
    width: right - left,
    height: bottom - top,
    u0: (left - x) / width,
    v0: (top - y) / height,
    u1: (right - x) / width,
    v1: (bottom - y) / height,
  };
}

export interface PlacementSourceRect {
  x: number;
  y: number;
  width: number;
  height: number;
}

/**
 * The texture sub-rect a clipped quad samples. A placement that names no source
 * rect (all zeroes, the common "draw the whole image" case) falls back to the
 * texture's own bounds — which only the caller holding the pixels knows, so the
 * clipping fractions are resolved here rather than at apply time.
 */
export function placementSourceRect(
  quad: PlacementQuad,
  textureWidth: number,
  textureHeight: number,
): PlacementSourceRect {
  const { placement } = quad;
  const sized = placement.sourceWidth > 0 && placement.sourceHeight > 0;
  const sourceX = sized ? placement.sourceX : 0;
  const sourceY = sized ? placement.sourceY : 0;
  const sourceWidth = sized ? placement.sourceWidth : textureWidth;
  const sourceHeight = sized ? placement.sourceHeight : textureHeight;
  return {
    x: sourceX + quad.u0 * sourceWidth,
    y: sourceY + quad.v0 * sourceHeight,
    width: (quad.u1 - quad.u0) * sourceWidth,
    height: (quad.v1 - quad.v0) * sourceHeight,
  };
}
