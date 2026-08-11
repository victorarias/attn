import { readFileSync } from 'node:fs';
import { describe, it, expect } from 'vitest';

/**
 * Regression guard for a bug that got past the component tests and was only
 * caught by looking at the running app: the auto-settle countdown bar rendered
 * at the correct width the whole time, but completely transparent.
 *
 * `--settling-accent` was declared only on `.settling-header` (the chip button),
 * while `.settling-header-track` is its *sibling*, not a descendant. The track's
 * background and its fill's colour therefore resolved to an undefined var(),
 * which CSS drops silently — the "Settling…" label was visible, the countdown
 * itself was not, and nothing failed.
 *
 * Rendering cannot catch this: jsdom does not resolve custom properties across
 * elements. Reading the stylesheet can. This lives in .mjs because the app
 * tsconfig has no node types and vitest stubs `?raw` CSS imports to empty.
 */
describe('SettlingIndicator stylesheet', () => {
  // Comments sit between rules and would otherwise be swallowed into the
  // selector list by the match below.
  // Relative to the vitest root (app/); import.meta.url is not a file: URL here.
  const css = readFileSync('src/components/SettlingIndicator.css', 'utf8')
    .replace(/\/\*[\s\S]*?\*\//g, '');

  // The selectors the palette-declaring rule applies to.
  const declaringRoots = (() => {
    const match = css.match(/([^{}]+)\{[^}]*--settling-accent:/);
    return match ? match[1].split(',').map((s) => s.trim()).filter(Boolean) : [];
  })();

  it('declares the palette', () => {
    expect(declaringRoots.length).toBeGreaterThan(0);
  });

  it.each([
    // Every element that paints with the palette, and the root it inherits from.
    ['.settling-header', '.settling-header'],
    ['.settling-header-track', '.settling-header-track'],
    ['.settling-header-track-fill', '.settling-header-track'],
    ['.settling-sidebar-bar', '.settling-sidebar-bar'],
    ['.settling-sidebar-bar-fill', '.settling-sidebar-bar'],
    // The kept chip carries .settling-header too, so it inherits from there.
    ['.settling-header--kept', '.settling-header'],
    ['.settling-kept-mark', '.settling-header'],
  ])('%s can resolve --settling-accent via %s', (user, root) => {
    expect(css, `${user} has no rule`).toMatch(new RegExp(`\\${user}\\s*[,{]`));
    expect(declaringRoots).toContain(root);
  });
});
