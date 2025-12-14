// app/src/store/sessions.test.ts
import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest';
import { useSessionStore } from './sessions';
import { act } from '@testing-library/react';

// Mock Tauri APIs
vi.mock('@tauri-apps/api/core', () => ({
  invoke: vi.fn().mockResolvedValue(undefined),
}));

vi.mock('@tauri-apps/api/event', () => ({
  listen: vi.fn().mockResolvedValue(() => {}),
}));

describe('sessions store - terminal panel', () => {
  let timeCounter = 1000000000000;

  beforeEach(() => {
    // Reset the store to initial state
    useSessionStore.setState({
      sessions: [],
      activeSessionId: null,
      connected: false,
    });
    // Mock Date.now to return incrementing values (avoids ID collisions)
    vi.spyOn(Date, 'now').mockImplementation(() => timeCounter++);
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  describe('terminal panel state', () => {
    it('creates session with default panel state', async () => {
      const sessionId = await useSessionStore.getState().createSession('test', '/tmp');
      const session = useSessionStore.getState().sessions.find((s) => s.id === sessionId);

      expect(session?.terminalPanel).toEqual({
        isOpen: false,
        height: 200,
        activeTabId: null,
        terminals: [],
        nextTerminalNumber: 1,
      });
    });

    it('openTerminalPanel sets isOpen to true', async () => {
      const sessionId = await useSessionStore.getState().createSession('test', '/tmp');
      useSessionStore.getState().openTerminalPanel(sessionId);

      const session = useSessionStore.getState().sessions.find((s) => s.id === sessionId);
      expect(session?.terminalPanel.isOpen).toBe(true);
    });

    it('collapseTerminalPanel sets isOpen to false', async () => {
      const sessionId = await useSessionStore.getState().createSession('test', '/tmp');
      useSessionStore.getState().openTerminalPanel(sessionId);
      useSessionStore.getState().collapseTerminalPanel(sessionId);

      const session = useSessionStore.getState().sessions.find((s) => s.id === sessionId);
      expect(session?.terminalPanel.isOpen).toBe(false);
    });

    it('setTerminalPanelHeight updates height', async () => {
      const sessionId = await useSessionStore.getState().createSession('test', '/tmp');
      useSessionStore.getState().setTerminalPanelHeight(sessionId, 300);

      const session = useSessionStore.getState().sessions.find((s) => s.id === sessionId);
      expect(session?.terminalPanel.height).toBe(300);
    });
  });

  describe('utility terminal management', () => {
    it('addUtilityTerminal adds terminal and sets it active', async () => {
      const sessionId = await useSessionStore.getState().createSession('test', '/tmp');
      const terminalId = useSessionStore.getState().addUtilityTerminal(sessionId, 'pty-123');

      const session = useSessionStore.getState().sessions.find((s) => s.id === sessionId);
      expect(session?.terminalPanel.terminals).toHaveLength(1);
      expect(session?.terminalPanel.terminals[0]).toEqual({
        id: terminalId,
        ptyId: 'pty-123',
        title: 'Shell 1',
      });
      expect(session?.terminalPanel.activeTabId).toBe(terminalId);
      expect(session?.terminalPanel.nextTerminalNumber).toBe(2);
    });

    it('addUtilityTerminal increments terminal number', async () => {
      const sessionId = await useSessionStore.getState().createSession('test', '/tmp');
      useSessionStore.getState().addUtilityTerminal(sessionId, 'pty-1');
      useSessionStore.getState().addUtilityTerminal(sessionId, 'pty-2');
      useSessionStore.getState().addUtilityTerminal(sessionId, 'pty-3');

      const session = useSessionStore.getState().sessions.find((s) => s.id === sessionId);
      expect(session?.terminalPanel.terminals.map((t) => t.title)).toEqual([
        'Shell 1',
        'Shell 2',
        'Shell 3',
      ]);
    });

    it('setActiveUtilityTerminal changes active tab', async () => {
      const sessionId = await useSessionStore.getState().createSession('test', '/tmp');
      const t1 = useSessionStore.getState().addUtilityTerminal(sessionId, 'pty-1');
      const t2 = useSessionStore.getState().addUtilityTerminal(sessionId, 'pty-2');

      // t2 should be active after adding
      expect(useSessionStore.getState().sessions.find((s) => s.id === sessionId)?.terminalPanel.activeTabId).toBe(t2);

      // Switch to t1
      useSessionStore.getState().setActiveUtilityTerminal(sessionId, t1);
      expect(useSessionStore.getState().sessions.find((s) => s.id === sessionId)?.terminalPanel.activeTabId).toBe(t1);
    });

    it('renameUtilityTerminal updates title', async () => {
      const sessionId = await useSessionStore.getState().createSession('test', '/tmp');
      const terminalId = useSessionStore.getState().addUtilityTerminal(sessionId, 'pty-123');

      useSessionStore.getState().renameUtilityTerminal(sessionId, terminalId, 'My Custom Shell');

      const session = useSessionStore.getState().sessions.find((s) => s.id === sessionId);
      expect(session?.terminalPanel.terminals[0].title).toBe('My Custom Shell');
    });

    it('renameUtilityTerminal keeps original title if empty string', async () => {
      const sessionId = await useSessionStore.getState().createSession('test', '/tmp');
      const terminalId = useSessionStore.getState().addUtilityTerminal(sessionId, 'pty-123');

      useSessionStore.getState().renameUtilityTerminal(sessionId, terminalId, '');

      const session = useSessionStore.getState().sessions.find((s) => s.id === sessionId);
      expect(session?.terminalPanel.terminals[0].title).toBe('Shell 1');
    });
  });

  describe('removeUtilityTerminal', () => {
    it('removes terminal from list', async () => {
      const sessionId = await useSessionStore.getState().createSession('test', '/tmp');
      const t1 = useSessionStore.getState().addUtilityTerminal(sessionId, 'pty-1');
      const t2 = useSessionStore.getState().addUtilityTerminal(sessionId, 'pty-2');

      useSessionStore.getState().removeUtilityTerminal(sessionId, t1);

      const session = useSessionStore.getState().sessions.find((s) => s.id === sessionId);
      expect(session?.terminalPanel.terminals).toHaveLength(1);
      expect(session?.terminalPanel.terminals[0].id).toBe(t2);
    });

    it('selects next tab when removing active tab', async () => {
      const sessionId = await useSessionStore.getState().createSession('test', '/tmp');
      const t1 = useSessionStore.getState().addUtilityTerminal(sessionId, 'pty-1');
      const t2 = useSessionStore.getState().addUtilityTerminal(sessionId, 'pty-2');
      const t3 = useSessionStore.getState().addUtilityTerminal(sessionId, 'pty-3');

      // t3 is active, switch to t2
      useSessionStore.getState().setActiveUtilityTerminal(sessionId, t2);

      // Remove t2, should select t3 (next)
      useSessionStore.getState().removeUtilityTerminal(sessionId, t2);

      const session = useSessionStore.getState().sessions.find((s) => s.id === sessionId);
      expect(session?.terminalPanel.activeTabId).toBe(t3);
    });

    it('selects previous tab when removing last tab in list', async () => {
      const sessionId = await useSessionStore.getState().createSession('test', '/tmp');
      const t1 = useSessionStore.getState().addUtilityTerminal(sessionId, 'pty-1');
      const t2 = useSessionStore.getState().addUtilityTerminal(sessionId, 'pty-2');

      // t2 is active (last in list), remove it
      useSessionStore.getState().removeUtilityTerminal(sessionId, t2);

      const session = useSessionStore.getState().sessions.find((s) => s.id === sessionId);
      expect(session?.terminalPanel.activeTabId).toBe(t1);
    });

    it('sets activeTabId to null when removing last terminal', async () => {
      const sessionId = await useSessionStore.getState().createSession('test', '/tmp');
      const t1 = useSessionStore.getState().addUtilityTerminal(sessionId, 'pty-1');

      useSessionStore.getState().removeUtilityTerminal(sessionId, t1);

      const session = useSessionStore.getState().sessions.find((s) => s.id === sessionId);
      expect(session?.terminalPanel.activeTabId).toBe(null);
      expect(session?.terminalPanel.terminals).toHaveLength(0);
    });

    it('closes panel when last terminal is removed', async () => {
      const sessionId = await useSessionStore.getState().createSession('test', '/tmp');
      const t1 = useSessionStore.getState().addUtilityTerminal(sessionId, 'pty-1');

      useSessionStore.getState().openTerminalPanel(sessionId);
      useSessionStore.getState().removeUtilityTerminal(sessionId, t1);

      const session = useSessionStore.getState().sessions.find((s) => s.id === sessionId);
      expect(session?.terminalPanel.isOpen).toBe(false);
    });

    it('keeps panel open when terminals remain', async () => {
      const sessionId = await useSessionStore.getState().createSession('test', '/tmp');
      const t1 = useSessionStore.getState().addUtilityTerminal(sessionId, 'pty-1');
      const t2 = useSessionStore.getState().addUtilityTerminal(sessionId, 'pty-2');

      useSessionStore.getState().openTerminalPanel(sessionId);
      useSessionStore.getState().removeUtilityTerminal(sessionId, t1);

      const session = useSessionStore.getState().sessions.find((s) => s.id === sessionId);
      expect(session?.terminalPanel.isOpen).toBe(true);
    });

    it('does not change active tab when removing non-active tab', async () => {
      const sessionId = await useSessionStore.getState().createSession('test', '/tmp');
      const t1 = useSessionStore.getState().addUtilityTerminal(sessionId, 'pty-1');
      const t2 = useSessionStore.getState().addUtilityTerminal(sessionId, 'pty-2');
      const t3 = useSessionStore.getState().addUtilityTerminal(sessionId, 'pty-3');

      // t3 is active, remove t1
      useSessionStore.getState().removeUtilityTerminal(sessionId, t1);

      const session = useSessionStore.getState().sessions.find((s) => s.id === sessionId);
      expect(session?.terminalPanel.activeTabId).toBe(t3);
    });
  });

  describe('session isolation', () => {
    it('terminal panel state is separate per session', async () => {
      const s1 = await useSessionStore.getState().createSession('session1', '/tmp/s1');
      const s2 = await useSessionStore.getState().createSession('session2', '/tmp/s2');

      useSessionStore.getState().openTerminalPanel(s1);
      useSessionStore.getState().addUtilityTerminal(s1, 'pty-s1');
      useSessionStore.getState().setTerminalPanelHeight(s1, 400);

      const session1 = useSessionStore.getState().sessions.find((s) => s.id === s1);
      const session2 = useSessionStore.getState().sessions.find((s) => s.id === s2);

      expect(session1?.terminalPanel.isOpen).toBe(true);
      expect(session1?.terminalPanel.height).toBe(400);
      expect(session1?.terminalPanel.terminals).toHaveLength(1);

      expect(session2?.terminalPanel.isOpen).toBe(false);
      expect(session2?.terminalPanel.height).toBe(200);
      expect(session2?.terminalPanel.terminals).toHaveLength(0);
    });
  });
});
