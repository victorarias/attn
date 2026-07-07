import type { GhosttyTerminal } from 'ghostty-web';

// The vendored wasm (app/vendor/ghostty-vt/ghostty-web-v0.4.0-compat.patch) adds
// ghostty_render_state_get_hyperlink_uri and ghostty_terminal_get_scrollback_hyperlink_uri
// exports, but ghostty-web@0.4.0's JS API has no accessor for them —
// GhosttyTerminal.getHyperlinkUri is a null stub. Patching the minified dist via
// pnpm patch would embed the whole (already-minified, one-line) file twice in the
// diff, so instead this reaches directly into the GhosttyTerminal internals
// (exports/memory/handle), which are TS-private only and stable at runtime.
// ghostty-web is pinned in package.json, so this is safe to rely on.

interface GhosttyTerminalInternals {
  handle: number;
  memory: WebAssembly.Memory;
  exports: {
    ghostty_wasm_alloc_u8_array(size: number): number;
    ghostty_render_state_get_hyperlink_uri(
      handle: number,
      row: number,
      col: number,
      out: number,
      bufSize: number
    ): number;
    ghostty_terminal_get_scrollback_hyperlink_uri(
      handle: number,
      offset: number,
      col: number,
      out: number,
      bufSize: number
    ): number;
  };
}

const HYPERLINK_URI_BUFFER_SIZE = 2048;

// One lazily-allocated wasm buffer per wasm instance (keyed by `exports`, which is
// shared by every terminal created from the same Ghostty instance). Calls into wasm
// are synchronous and single-threaded, so sharing the buffer across terminals of one
// instance is safe and avoids leaking an allocation per terminal.
const hyperlinkUriBufferPtrs = new WeakMap<GhosttyTerminalInternals['exports'], number>();
const textDecoder = new TextDecoder();

function internals(terminal: GhosttyTerminal): GhosttyTerminalInternals {
  return terminal as unknown as GhosttyTerminalInternals;
}

function hyperlinkUriBufferPtr(exports: GhosttyTerminalInternals['exports']): number {
  let ptr = hyperlinkUriBufferPtrs.get(exports);
  if (!ptr) {
    ptr = exports.ghostty_wasm_alloc_u8_array(HYPERLINK_URI_BUFFER_SIZE);
    hyperlinkUriBufferPtrs.set(exports, ptr);
  }
  return ptr;
}

function readHyperlinkUri(memory: WebAssembly.Memory, ptr: number, bytesWritten: number): string | null {
  if (bytesWritten <= 0) return null;
  // Create the view fresh on every call: wasm memory growth detaches old views.
  return textDecoder.decode(new Uint8Array(memory.buffer, ptr, bytesWritten));
}

/**
 * OSC 8 hyperlink URI of the cell at (row, col) in the active area, or null if the
 * cell has no hyperlink or the position is out of range.
 */
export function hyperlinkUriAt(terminal: GhosttyTerminal, row: number, col: number): string | null {
  const { handle, memory, exports } = internals(terminal);
  const ptr = hyperlinkUriBufferPtr(exports);
  const n = exports.ghostty_render_state_get_hyperlink_uri(handle, row, col, ptr, HYPERLINK_URI_BUFFER_SIZE);
  return readHyperlinkUri(memory, ptr, n);
}

/**
 * OSC 8 hyperlink URI of a scrollback cell (offset 0 = oldest line), or null if the
 * cell has no hyperlink or the position is out of range.
 */
export function scrollbackHyperlinkUri(terminal: GhosttyTerminal, offset: number, col: number): string | null {
  const { handle, memory, exports } = internals(terminal);
  const ptr = hyperlinkUriBufferPtr(exports);
  const n = exports.ghostty_terminal_get_scrollback_hyperlink_uri(handle, offset, col, ptr, HYPERLINK_URI_BUFFER_SIZE);
  return readHyperlinkUri(memory, ptr, n);
}
