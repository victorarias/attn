// Where a session's kitty images sit on the client's grid.
//
// The client never anchors a placement itself: the worker describes the WHOLE
// set whenever anything moved, and each apply replaces the set wholesale,
// mapping viewport rows against the scrollback length read at that moment. A
// placement mapping above the retained history is dropped, never guessed.

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
  /** Rendered size in image pixels — the only size signal; cells are advisory. */
  pixelWidth: number;
  pixelHeight: number;
  /** Source sub-rect in image pixels. All-zero width/height means the whole image. */
  sourceX: number;
  sourceY: number;
  sourceWidth: number;
  sourceHeight: number;
}

// A wire set never applied; every real seq is a uint32, so nothing sorts below.
const NO_SEQ = -1;

export class KittyPlacementStore {
  private placed: PlacedKittyImage[] = [];
  private appliedSeq = NO_SEQ;

  /** Apply one described set, returning whether it was taken. Equal seqs are
   * accepted: a resize re-describes at the replay watermark. */
  apply(seq: number, wire: readonly PlacementElement[], scrollbackLength: number): boolean {
    if (seq < this.appliedSeq) return false;
    this.appliedSeq = seq;
    this.placed = mapPlacements(wire, scrollbackLength);
    return true;
  }

  /** Seed from a restore snapshot: it is the whole truth, and the seq gate
   * resets with it because the live stream resumes at the attach watermark. */
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
    // A unicode-placeholder placement has no cursor geometry.
    if (p.virtual) continue;
    if (p.pixel_width <= 0 || p.pixel_height <= 0) continue;
    // viewport_row is screen-relative on the worker's grid, which equals the
    // client's: every frame resizes without reflow, so no width change re-wraps
    // one grid's history and not another's.
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
  // Draw order within a layer (the renderer reads z again to pick the layer);
  // the placement id breaks ties so the order never depends on iteration order.
  placed.sort((a, b) => (a.z - b.z) || (a.placementId - b.placementId));
  return placed;
}

/** A placement's drawable rect, clipped to the grid; u/v are the surviving
 * fractions of the rendered box, in texture order. */
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

/** Where a placement lands on screen, or null when it misses the viewport. The
 * unit boundary: placements are DEVICE pixels and the grid box is CSS pixels, so
 * the box is divided by devicePixelRatio here, once. */
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

/** The texture sub-rect a clipped quad samples; an all-zero source rect means
 * the whole image, resolved against the texture's own bounds. */
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
