export const TERMINAL_OUTPUT_MAX_FPS = 30;
export const TERMINAL_OUTPUT_FRAME_MS = 1000 / TERMINAL_OUTPUT_MAX_FPS;

// A new burst should feel immediate. Once output has been quiet for longer than
// two capped frame intervals, forget the old cadence and paint on the next
// browser frame instead of waiting behind a stale timestamp.
export const TERMINAL_OUTPUT_IDLE_RESET_MS = TERMINAL_OUTPUT_FRAME_MS * 2;

export function terminalOutputDelayMs(now: number, lastPaintAt: number | null): number {
  if (lastPaintAt === null) return 0;
  const elapsed = now - lastPaintAt;
  if (elapsed >= TERMINAL_OUTPUT_IDLE_RESET_MS) return 0;
  return Math.max(0, TERMINAL_OUTPUT_FRAME_MS - elapsed);
}
