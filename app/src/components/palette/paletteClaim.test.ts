import { describe, it, expect, vi, afterEach } from 'vitest';
import { registerPaletteClaim, claimPaletteFocus } from './paletteClaim';

const cleanups: (() => void)[] = [];

afterEach(() => {
  cleanups.splice(0).forEach((fn) => fn());
  document.body.innerHTML = '';
});

function claim(container: HTMLElement | null, open: () => void) {
  cleanups.push(registerPaletteClaim({ container: () => container, open }));
}

describe('claimPaletteFocus', () => {
  it('hands the shortcut to the surface containing the focused element', () => {
    const surface = document.createElement('div');
    const inner = document.createElement('button');
    surface.appendChild(inner);
    document.body.appendChild(surface);
    const open = vi.fn();
    claim(surface, open);

    expect(claimPaletteFocus(inner)).toBe(true);
    expect(open).toHaveBeenCalledTimes(1);
  });

  it('declines when focus is outside every registered surface', () => {
    const surface = document.createElement('div');
    const outside = document.createElement('button');
    document.body.append(surface, outside);
    const open = vi.fn();
    claim(surface, open);

    expect(claimPaletteFocus(outside)).toBe(false);
    expect(open).not.toHaveBeenCalled();
  });

  it('picks the surface that actually holds focus when several are registered', () => {
    const first = document.createElement('div');
    const second = document.createElement('div');
    const inner = document.createElement('button');
    second.appendChild(inner);
    document.body.append(first, second);
    const openFirst = vi.fn();
    const openSecond = vi.fn();
    claim(first, openFirst);
    claim(second, openSecond);

    expect(claimPaletteFocus(inner)).toBe(true);
    expect(openFirst).not.toHaveBeenCalled();
    expect(openSecond).toHaveBeenCalledTimes(1);
  });

  it('stops claiming once unregistered', () => {
    const surface = document.createElement('div');
    const inner = document.createElement('button');
    surface.appendChild(inner);
    document.body.appendChild(surface);
    const open = vi.fn();
    const unregister = registerPaletteClaim({ container: () => surface, open });

    unregister();

    expect(claimPaletteFocus(inner)).toBe(false);
    expect(open).not.toHaveBeenCalled();
  });

  it('declines when nothing is focused', () => {
    const surface = document.createElement('div');
    document.body.appendChild(surface);
    claim(surface, vi.fn());
    expect(claimPaletteFocus(null)).toBe(false);
  });
});
