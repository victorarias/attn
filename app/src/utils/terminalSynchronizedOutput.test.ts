import { describe, expect, it } from 'vitest';
import { parseSynchronizedOutput, type SynchronizedOutputState } from './terminalSynchronizedOutput';

describe('parseSynchronizedOutput', () => {
  it('renders ordinary output immediately', () => {
    const parsed = parseSynchronizedOutput({ active: false, pending: '' }, 'hello');

    expect(parsed.shouldRender).toBe(true);
    expect(parsed.state).toEqual({ active: false, pending: '' });
  });

  it('suppresses intermediate synchronized output and renders on end', () => {
    let state: SynchronizedOutputState = { active: false, pending: '' };
    let parsed = parseSynchronizedOutput(state, 'before\x1b[?2026hredraw ');
    expect(parsed.shouldRender).toBe(false);
    expect(parsed.state.active).toBe(true);
    state = parsed.state;

    parsed = parseSynchronizedOutput(state, 'more redraw');
    expect(parsed.shouldRender).toBe(false);
    expect(parsed.state.active).toBe(true);
    state = parsed.state;

    parsed = parseSynchronizedOutput(state, 'done\x1b[?2026lafter');
    expect(parsed.shouldRender).toBe(true);
    expect(parsed.state.active).toBe(false);
  });

  it('handles split synchronized-output markers across chunks', () => {
    let state: SynchronizedOutputState = { active: false, pending: '' };
    let parsed = parseSynchronizedOutput(state, '\x1b[?20');
    expect(parsed.shouldRender).toBe(true);
    state = parsed.state;

    parsed = parseSynchronizedOutput(state, '26hredraw');
    expect(parsed.shouldRender).toBe(false);
    expect(parsed.state.active).toBe(true);
    state = parsed.state;

    parsed = parseSynchronizedOutput(state, '\x1b[?202');
    expect(parsed.shouldRender).toBe(false);
    state = parsed.state;

    parsed = parseSynchronizedOutput(state, '6l');
    expect(parsed.shouldRender).toBe(true);
    expect(parsed.state.active).toBe(false);
  });
});
