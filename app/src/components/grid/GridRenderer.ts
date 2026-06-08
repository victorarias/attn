// The renderer abstraction. The GridCompositor (which owns the rAF loop, layout,
// animation, and input) is renderer-agnostic: it computes every animated
// TileFrame and hands them in; a GridRenderer is a DUMB per-frame function of
// (tiles, frames) that only draws.
//
// Grid mode renders many live terminal tiles inside ONE WebGL2 context (see
// UnifiedGridRenderer). This matters because WKWebView caps the number of
// simultaneously-live WebGL contexts; the production pane renderer
// (GhosttyTerminal) spends one context per pane, so a global "see every session"
// surface has to composite into a single context to scale.
import type { GhosttyTerminal } from 'ghostty-web';
import type { UISessionState } from '../../types/sessionState';

export interface TileModel {
  id: string;
  model: GhosttyTerminal; // pure VT state machine; owns no GPU resource
}

// Container-space CSS-px bounding box of a tile's (scaled) content.
export interface Rect {
  x: number;
  y: number;
  w: number;
  h: number;
}

// Per-frame, animated placement of one tile. Recomputed by the compositor every
// frame; the renderer treats it as immutable input.
export interface TileFrame {
  id: string;
  rect: Rect; // where the scaled content sits, in container CSS px
  scale: number; // 1.0 == native 1:1 (logical px); thumbnails are < 1
  alpha: number; // 0..1 — fade non-focused tiles during a zoom morph
  attention: number; // 0..1 — pulsing state emphasis ("needs you")
  state: UISessionState;
  hidden: boolean; // excluded from the batch / surface
  focused: boolean;
}

export interface GridRenderStats {
  drawCalls: number; // unified target: 1
  quads: number;
  atlasUploads: number; // glyph slots uploaded this frame
  atlasResets: number; // resetAtlas() calls this frame
  liveContexts: number; // honestly measured: contexts not isContextLost()
  cpuSubmitMs: number; // vertex assembly + GL submit wall time
}

export interface GridRenderer {
  readonly name: string;
  mount(container: HTMLElement): void;
  setTiles(tiles: TileModel[]): void;
  frame(frames: TileFrame[], now: number): GridRenderStats;
  dispose(): void;
}

export const EMPTY_STATS: GridRenderStats = {
  drawCalls: 0,
  quads: 0,
  atlasUploads: 0,
  atlasResets: 0,
  liveContexts: 0,
  cpuSubmitMs: 0,
};
