import { describe, expect, it } from 'vitest';
import {
  buildProbeBannerAtGridRegex,
  buildProbeLaunchCommand,
  probeBannerReadyMatchers,
  probeStyleIdentityRegex,
  remoteProbeBinaryName,
} from './scenario-tr205-remote-relaunch-close-redraw.mjs';

describe('remoteProbeBinaryName', () => {
  it('resolves the default profile to "attn"', () => {
    expect(remoteProbeBinaryName('')).toBe('attn');
    expect(remoteProbeBinaryName(undefined)).toBe('attn');
    expect(remoteProbeBinaryName('   ')).toBe('attn');
  });

  it('resolves a named profile to "attn-<profile>"', () => {
    expect(remoteProbeBinaryName('dev')).toBe('attn-dev');
    expect(remoteProbeBinaryName('agent7')).toBe('attn-agent7');
  });
});

describe('buildProbeLaunchCommand', () => {
  // Mirrors the remote binary resolution in internal/hub/ssh.go:69
  // (remoteAttnCommand). This is resolved IN THE REMOTE SHELL, not
  // precomputed in JS, because a live run showed the JS-precomputed path can
  // diverge from where the daemon actually installed: an already-running
  // daemon never sees this scenario's ATTN_REMOTE_ATTN_BIN launchEnv, so the
  // bootstrapper falls back to installing at the default
  // $HOME/.local/bin/<binaryName> path instead of the harness-scoped
  // override path. The regression this guards against: someone "simplifying"
  // this back to a single hardcoded path, which is exactly what broke live.
  it('references both the ATTN_REMOTE_ATTN_BIN override and the default install path, and execs the probe', () => {
    const command = buildProbeLaunchCommand('attn-fxm1', 'codex');
    expect(command).toContain('${ATTN_REMOTE_ATTN_BIN:-');
    expect(command).toContain('$HOME/.local/bin/attn-fxm1');
    expect(command.trim().endsWith('_probe-tui --style codex')).toBe(true);
  });
});

// Mirrors the two-row banner in internal/probetui/probetui.go
// (bannerGeometryRow "ATTN-PROBE <cols>x<rows>", bannerStyleRow
// "style=<style> seq=<seq> READY") — split across rows because a single
// combined banner line truncated past recognition in narrow (~20-31 col)
// panes.
describe('probeBannerReadyMatchers', () => {
  it('both matchers pass against a real two-row banner', () => {
    const [geometryRegex, styleRegex] = probeBannerReadyMatchers('codex');
    const text = 'ATTN-PROBE 80x24\nstyle=codex seq=1 READY';
    expect(geometryRegex.test(text)).toBe(true);
    expect(styleRegex.test(text)).toBe(true);
  });

  it('the style matcher is seq-invariant', () => {
    const [, styleRegex] = probeBannerReadyMatchers('codex');
    expect(styleRegex.test('style=codex seq=1 READY')).toBe(true);
    expect(styleRegex.test('style=codex seq=999 READY')).toBe(true);
  });

  it('the style matcher rejects the wrong style', () => {
    const [, styleRegex] = probeBannerReadyMatchers('codex');
    expect(styleRegex.test('style=claude seq=1 READY')).toBe(false);
  });

  it('the style matcher rejects a malformed style row', () => {
    const [, styleRegex] = probeBannerReadyMatchers('claude');
    expect(styleRegex.test('style=claude seq=1')).toBe(false);
    expect(styleRegex.test('some other output style=claude READY')).toBe(false);
  });
});

describe('buildProbeBannerAtGridRegex', () => {
  it('matches the geometry row pinned to the given grid, any seq', () => {
    const regex = buildProbeBannerAtGridRegex(80, 24);
    expect(regex.test('ATTN-PROBE 80x24\nstyle=codex seq=1 READY')).toBe(true);
    expect(regex.test('ATTN-PROBE 80x24\nstyle=codex seq=42 READY')).toBe(true);
  });

  it('rejects the wrong grid', () => {
    const regex = buildProbeBannerAtGridRegex(80, 24);
    expect(regex.test('ATTN-PROBE 40x12\nstyle=codex seq=1 READY')).toBe(false);
  });

  it('rejects a grid that is a numeric prefix of another (31x2 must not match 31x25)', () => {
    const regex = buildProbeBannerAtGridRegex(31, 2);
    expect(regex.test('ATTN-PROBE 31x25\nstyle=codex seq=1 READY')).toBe(false);
    expect(regex.test('ATTN-PROBE 31x2\nstyle=codex seq=1 READY')).toBe(true);
  });
});

describe('probeStyleIdentityRegex', () => {
  it('matches a right-truncated style row', () => {
    const identityRegex = probeStyleIdentityRegex('codex');
    const truncatedRow = 'style=codex seq=73 R'; // Truncated at 20 cols
    expect(identityRegex.test(truncatedRow)).toBe(true);

    // Verify the full-row matcher would have failed on the truncated row
    const fullRowRegex = /style=codex seq=\d+ READY/;
    expect(fullRowRegex.test(truncatedRow)).toBe(false);
  });

  it('does not cross-match styles', () => {
    const codexRegex = probeStyleIdentityRegex('codex');
    const claudeRegex = probeStyleIdentityRegex('claude');

    expect(codexRegex.test('style=claude seq=2 READY')).toBe(false);
    expect(claudeRegex.test('style=codex seq=2 READY')).toBe(false);
  });

  it('matches at end of line', () => {
    const regex = probeStyleIdentityRegex('codex');
    expect(regex.test('style=codex')).toBe(true);
  });
});
