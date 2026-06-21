import { describe, expect, it } from 'vitest';
import { formatExitNotice } from './exitNotice';

describe('formatExitNotice', () => {
  it('treats a clean exit (code 0, no signal) as a session end', () => {
    expect(formatExitNotice(0)).toBe('[Session ended]');
  });

  it('treats SIGTERM (143) — attn\'s normal teardown signal — as a session end, not a crash', () => {
    // This is the regression case: a completed dispatch torn down with SIGTERM
    // used to surface as the scary "[Process exited with code 143]".
    expect(formatExitNotice(143)).toBe('[Session ended]');
  });

  it('treats SIGINT (130) and SIGHUP (129) as a session end', () => {
    expect(formatExitNotice(130)).toBe('[Session ended]');
    expect(formatExitNotice(129)).toBe('[Session ended]');
  });

  it('honors an explicit graceful signal name regardless of code', () => {
    expect(formatExitNotice(143, 'SIGTERM')).toBe('[Session ended]');
    expect(formatExitNotice(0, 'SIGTERM')).toBe('[Session ended]');
    expect(formatExitNotice(1, 'sigterm')).toBe('[Session ended]');
    expect(formatExitNotice(1, 'term')).toBe('[Session ended]');
  });

  it('keeps the raw exit code for a non-zero application exit', () => {
    expect(formatExitNotice(1)).toBe('[Process exited with code 1]');
    expect(formatExitNotice(2)).toBe('[Process exited with code 2]');
  });

  it('keeps the raw exit code for genuine crash/force-kill signals', () => {
    expect(formatExitNotice(137)).toBe('[Process exited with code 137]'); // SIGKILL / OOM
    expect(formatExitNotice(139)).toBe('[Process exited with code 139]'); // SIGSEGV
    expect(formatExitNotice(134)).toBe('[Process exited with code 134]'); // SIGABRT
  });

  it('reports a non-zero code even at code 0 when a crash signal is attached', () => {
    expect(formatExitNotice(0, 'SIGKILL')).toBe('[Process exited with code 0]');
  });
});
