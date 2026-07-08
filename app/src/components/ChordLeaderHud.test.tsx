import { describe, it, expect, afterEach } from 'vitest';
import { render, screen, act } from '@testing-library/react';
import { ChordLeaderHud } from './ChordLeaderHud';
import { enterLeader, cancelLeader } from '../shortcuts/chordState';

afterEach(() => {
  act(() => cancelLeader());
});

const LEADER = { key: 'k', meta: true };
const CANDIDATES = [{ id: 'dock.attention' as const, then: { key: 'd' } }];

describe('ChordLeaderHud', () => {
  it('renders nothing when no leader is armed', () => {
    render(<ChordLeaderHud />);
    expect(screen.queryByTestId('chord-leader-hud')).toBeNull();
  });

  it('shows the armed leader and a "then" affordance', () => {
    render(<ChordLeaderHud />);
    act(() => enterLeader(LEADER, CANDIDATES));
    const hud = screen.getByTestId('chord-leader-hud');
    expect(hud.textContent).toContain('⌘');
    expect(hud.textContent).toContain('K');
    expect(hud.textContent).toContain('then');
  });

  it('hides again when the leader is cancelled', () => {
    render(<ChordLeaderHud />);
    act(() => enterLeader(LEADER, CANDIDATES));
    expect(screen.getByTestId('chord-leader-hud')).toBeInTheDocument();
    act(() => cancelLeader());
    expect(screen.queryByTestId('chord-leader-hud')).toBeNull();
  });
});
