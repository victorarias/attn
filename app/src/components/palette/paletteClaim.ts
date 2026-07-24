// ⌘P is one global shortcut, but a focused notebook tile owns its own in-tile
// finder (two tiles must not fight over one binding). The global dispatcher
// runs in the capture phase, so it always sees the keystroke first; instead of
// racing it with a container keydown, a surface registers a claim here and the
// global handler hands ⌘P back to whichever registered surface currently
// contains focus.

interface PaletteClaim {
  // Resolved at claim time — a ref's element is null on the render that
  // registers it.
  container: () => HTMLElement | null;
  open: () => void;
}

const claims = new Set<PaletteClaim>();

// Register a surface that owns ⌘P while focus is inside it. Returns the
// unregister function, so a caller can use it directly as an effect cleanup.
export function registerPaletteClaim(claim: PaletteClaim): () => void {
  claims.add(claim);
  return () => { claims.delete(claim); };
}

// Hand ⌘P to the registered surface containing `active`, if any. Returns true
// when a surface handled it and the global opener must stand down.
export function claimPaletteFocus(active: Element | null = document.activeElement): boolean {
  if (!active) return false;
  for (const claim of claims) {
    const container = claim.container();
    if (container && container.contains(active)) {
      claim.open();
      return true;
    }
  }
  return false;
}
