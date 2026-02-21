// app/src/components/UtilityTerminalPanel/index.tsx
import { useRef, useCallback, useEffect } from 'react';
import { Terminal as XTerm } from '@xterm/xterm';
import { Terminal, TerminalHandle, type ResolvedTheme } from '../Terminal';
import { TabBar } from './TabBar';
import { ResizeHandle } from './ResizeHandle';
import { useShortcut } from '../../shortcuts';
import type { TerminalPanelState } from '../../store/sessions';
import { listenPtyEvents, ptyKill, ptyResize, ptySpawn, ptyWrite, type PtyEventPayload } from '../../pty/bridge';
import './UtilityTerminalPanel.css';

declare global {
  interface Window {
    __TEST_GET_ACTIVE_UTILITY_TEXT?: () => string;
  }
}

interface UtilityTerminalPanelProps {
  cwd: string;
  panel: TerminalPanelState;
  fontSize: number;
  onOpen: () => void;
  onCollapse: () => void;
  onSetHeight: (height: number) => void;
  onAddTerminal: (ptyId: string) => string;
  onRemoveTerminal: (terminalId: string) => void;
  onSetActiveTerminal: (terminalId: string) => void;
  onRenameTerminal: (terminalId: string, title: string) => void;
  resolvedTheme?: ResolvedTheme;
  focusRequestToken?: number;
  enabled: boolean;
}

export function UtilityTerminalPanel({
  cwd,
  panel,
  fontSize,
  onOpen,
  onCollapse,
  onSetHeight,
  onAddTerminal,
  onRemoveTerminal,
  onSetActiveTerminal,
  onRenameTerminal,
  resolvedTheme,
  focusRequestToken,
  enabled,
}: UtilityTerminalPanelProps) {
  const terminalRefs = useRef<Map<string, TerminalHandle>>(new Map());
  const xtermRefs = useRef<Map<string, XTerm>>(new Map());
  const inputSubscriptions = useRef<Map<string, { dispose: () => void }>>(new Map());
  const ptyIdToTerminalId = useRef<Map<string, string>>(new Map());
  const pendingTerminalEvents = useRef<Map<string, PtyEventPayload[]>>(new Map());
  const restoreOnReady = useRef<Set<string>>(new Set(panel.terminals.map((terminal) => terminal.id)));

  const focusTerminalWithRetry = useCallback((terminalId: string, retries = 20) => {
    const tryFocus = (remaining: number) => {
      const xterm = xtermRefs.current.get(terminalId);
      if (xterm) {
        xterm.focus();
        return;
      }
      const handle = terminalRefs.current.get(terminalId);
      if (handle?.terminal) {
        handle.focus();
        return;
      }
      if (remaining <= 0) {
        return;
      }
      window.setTimeout(() => tryFocus(remaining - 1), 50);
    };
    tryFocus(retries);
  }, []);

  useEffect(() => {
    const activeTerminalIds = new Set(panel.terminals.map((terminal) => terminal.id));

    for (const [terminalId, sub] of inputSubscriptions.current.entries()) {
      if (!activeTerminalIds.has(terminalId)) {
        sub.dispose();
        inputSubscriptions.current.delete(terminalId);
      }
    }

    for (const terminalId of Array.from(xtermRefs.current.keys())) {
      if (!activeTerminalIds.has(terminalId)) {
        xtermRefs.current.delete(terminalId);
      }
    }

    for (const terminalId of Array.from(terminalRefs.current.keys())) {
      if (!activeTerminalIds.has(terminalId)) {
        terminalRefs.current.delete(terminalId);
      }
    }

    for (const terminalId of Array.from(pendingTerminalEvents.current.keys())) {
      if (!activeTerminalIds.has(terminalId)) {
        pendingTerminalEvents.current.delete(terminalId);
      }
    }

    for (const terminalId of Array.from(restoreOnReady.current.keys())) {
      if (!activeTerminalIds.has(terminalId)) {
        restoreOnReady.current.delete(terminalId);
      }
    }

    ptyIdToTerminalId.current = new Map(panel.terminals.map((terminal) => [terminal.ptyId, terminal.id]));
  }, [panel.terminals]);

  const decodePtyData = useCallback((payload: string) => {
    const binaryStr = atob(payload);
    const bytes = Uint8Array.from(binaryStr, (c) => c.charCodeAt(0));
    return new TextDecoder('utf-8').decode(bytes);
  }, []);

  const writeToTerminal = useCallback((xterm: XTerm, msg: PtyEventPayload) => {
    switch (msg.event) {
      case 'data':
        xterm.write(decodePtyData(msg.data));
        break;
      case 'reset':
        xterm.reset();
        break;
      case 'exit':
        xterm.write(`\r\n[Process exited with code ${msg.code}]\r\n`);
        break;
      case 'error':
        xterm.write(`\r\n[Error: ${msg.error}]\r\n`);
        break;
      default:
        break;
    }
  }, [decodePtyData]);

  const queueTerminalEvent = useCallback((terminalId: string, msg: PtyEventPayload) => {
    const events = pendingTerminalEvents.current.get(terminalId) || [];
    if (events.length >= 256) {
      events.shift();
    }
    events.push(msg);
    pendingTerminalEvents.current.set(terminalId, events);
  }, []);

  const flushPendingEvents = useCallback((terminalId: string, xterm: XTerm) => {
    const events = pendingTerminalEvents.current.get(terminalId);
    if (!events || events.length === 0) {
      return;
    }
    for (const event of events) {
      writeToTerminal(xterm, event);
    }
    pendingTerminalEvents.current.delete(terminalId);
  }, [writeToTerminal]);

  const getTerminalText = useCallback((xterm: XTerm) => {
    const buffer = xterm.buffer.active;
    if (!buffer) {
      return '';
    }
    const lines: string[] = [];
    for (let i = 0; i < buffer.length; i += 1) {
      const line = buffer.getLine(i);
      if (line) {
        lines.push(line.translateToString(true));
      }
    }
    return lines.join('\n');
  }, []);

  useEffect(() => {
    if (!import.meta.env.DEV) {
      return;
    }

    window.__TEST_GET_ACTIVE_UTILITY_TEXT = () => {
      if (!panel.activeTabId) {
        return '';
      }
      const xterm = xtermRefs.current.get(panel.activeTabId);
      if (!xterm) {
        return '';
      }
      return getTerminalText(xterm);
    };

    return () => {
      delete window.__TEST_GET_ACTIVE_UTILITY_TEXT;
    };
  }, [getTerminalText, panel.activeTabId]);

  // Listen for PTY events for utility terminals
  useEffect(() => {
    const unlisten = listenPtyEvents((event) => {
      const msg = event.payload;
      const terminalId = ptyIdToTerminalId.current.get(msg.id);
      if (!terminalId) return;

      const xterm = xtermRefs.current.get(terminalId);
      if (!xterm) {
        queueTerminalEvent(terminalId, msg);
        return;
      }
      flushPendingEvents(terminalId, xterm);
      writeToTerminal(xterm, msg);
    });

    return () => {
      unlisten.then((fn) => fn());
    };
  }, [flushPendingEvents, queueTerminalEvent, writeToTerminal]);

  const spawnTerminal = useCallback(async () => {
    const ptyId = `util-pty-${Date.now()}`;
    const terminalId = onAddTerminal(ptyId);
    ptyIdToTerminalId.current.set(ptyId, terminalId);

    try {
      await ptySpawn({
        args: {
          id: ptyId,
          cwd,
          cols: 80,
          rows: 24,
          shell: true, // Utility terminals spawn a plain shell, not attn/Claude
        },
      });

      return terminalId;
    } catch (e) {
      console.error('[UtilityTerminal] Spawn failed:', e);
      ptyIdToTerminalId.current.delete(ptyId);
      pendingTerminalEvents.current.delete(terminalId);
      onRemoveTerminal(terminalId);
      return null;
    }
  }, [cwd, onAddTerminal, onRemoveTerminal]);

  const handleNewTab = useCallback(async () => {
    const terminalId = await spawnTerminal();
    if (terminalId) {
      focusTerminalWithRetry(terminalId);
    }
  }, [focusTerminalWithRetry, spawnTerminal]);

  const handleCloseTab = useCallback(
    (terminalId: string) => {
      const terminal = panel.terminals.find((t) => t.id === terminalId);
      if (terminal) {
        inputSubscriptions.current.get(terminalId)?.dispose();
        inputSubscriptions.current.delete(terminalId);
        ptyKill({ id: terminal.ptyId }).catch(console.error);
        ptyIdToTerminalId.current.delete(terminal.ptyId);
        pendingTerminalEvents.current.delete(terminalId);
        xtermRefs.current.delete(terminalId);
        terminalRefs.current.delete(terminalId);
      }
      onRemoveTerminal(terminalId);
    },
    [panel.terminals, onRemoveTerminal]
  );

  const handleOpenOrFocus = useCallback(async () => {
    if (!panel.isOpen) {
      onOpen();
      if (panel.terminals.length === 0) {
        await handleNewTab();
      } else if (panel.activeTabId) {
        focusTerminalWithRetry(panel.activeTabId);
      }
    } else if (panel.activeTabId) {
      focusTerminalWithRetry(panel.activeTabId, 2);
    }
  }, [focusTerminalWithRetry, panel.isOpen, panel.terminals.length, panel.activeTabId, onOpen, handleNewTab]);

  const handleNewTabOrOpen = useCallback(async () => {
    if (!panel.isOpen) {
      onOpen();
    }
    await handleNewTab();
  }, [panel.isOpen, onOpen, handleNewTab]);

  useEffect(() => {
    if (!panel.isOpen || !panel.activeTabId) {
      return;
    }
    focusTerminalWithRetry(panel.activeTabId, 30);
  }, [focusTerminalWithRetry, panel.activeTabId, panel.isOpen]);

  useEffect(() => {
    if (!panel.isOpen || !panel.activeTabId) {
      return;
    }
    focusTerminalWithRetry(panel.activeTabId, 60);
  }, [focusRequestToken, focusTerminalWithRetry, panel.activeTabId, panel.isOpen]);

  const wireTerminal = useCallback(
    (terminalId: string, ptyId: string, xterm: XTerm) => {
      xtermRefs.current.set(terminalId, xterm);
      ptyIdToTerminalId.current.set(ptyId, terminalId);

      // Terminal input -> PTY
      inputSubscriptions.current.get(terminalId)?.dispose();
      const sub = xterm.onData((data: string) => {
        ptyWrite({ id: ptyId, data }).catch(console.error);
      });
      inputSubscriptions.current.set(terminalId, sub);

      // Flush buffered PTY output after input wiring so terminal query
      // responses emitted during replay are not dropped.
      flushPendingEvents(terminalId, xterm);
    },
    [flushPendingEvents]
  );

  const restoreTerminalOutput = useCallback(
    async (ptyId: string, xterm: XTerm) => {
      const cols = xterm.cols > 0 ? xterm.cols : 80;
      const rows = xterm.rows > 0 ? xterm.rows : 24;

      try {
        // Reattach to restore scrollback after panel unmount/remount
        // (for example dashboard/home roundtrip).
        await ptySpawn({
          args: {
            id: ptyId,
            cwd,
            cols,
            rows,
            shell: true,
          },
        });
      } catch (e) {
        console.error('[UtilityTerminal] Restore attach failed:', e);
      }
    },
    [cwd]
  );

  const handleTerminalInit = useCallback(
    (terminalId: string, ptyId: string) => (xterm: XTerm) => {
      wireTerminal(terminalId, ptyId, xterm);
      if (panel.isOpen && panel.activeTabId === terminalId) {
        xterm.focus();
      }
    },
    [panel.activeTabId, panel.isOpen, wireTerminal]
  );

  const handleTerminalReady = useCallback(
    (terminalId: string, ptyId: string) => (xterm: XTerm) => {
      wireTerminal(terminalId, ptyId, xterm);
      if (restoreOnReady.current.has(terminalId)) {
        restoreOnReady.current.delete(terminalId);
        void restoreTerminalOutput(ptyId, xterm);
      }
      if (panel.isOpen && panel.activeTabId === terminalId) {
        xterm.focus();
      }
    },
    [panel.activeTabId, panel.isOpen, restoreTerminalOutput, wireTerminal]
  );

  const handleTerminalResize = useCallback(
    (ptyId: string) => (cols: number, rows: number) => {
      ptyResize({ id: ptyId, cols, rows }).catch(console.error);
    },
    []
  );

  const handlePrevTab = useCallback(() => {
    if (!panel.activeTabId || panel.terminals.length < 2) return;
    const currentIndex = panel.terminals.findIndex((t) => t.id === panel.activeTabId);
    const prevIndex = currentIndex > 0 ? currentIndex - 1 : panel.terminals.length - 1;
    onSetActiveTerminal(panel.terminals[prevIndex].id);
  }, [panel.activeTabId, panel.terminals, onSetActiveTerminal]);

  const handleNextTab = useCallback(() => {
    if (!panel.activeTabId || panel.terminals.length < 2) return;
    const currentIndex = panel.terminals.findIndex((t) => t.id === panel.activeTabId);
    const nextIndex = currentIndex < panel.terminals.length - 1 ? currentIndex + 1 : 0;
    onSetActiveTerminal(panel.terminals[nextIndex].id);
  }, [panel.activeTabId, panel.terminals, onSetActiveTerminal]);

  const handleCloseCurrentTab = useCallback(() => {
    if (panel.activeTabId) {
      handleCloseTab(panel.activeTabId);
    }
  }, [panel.activeTabId, handleCloseTab]);

  // Register shortcuts
  useShortcut('terminal.open', handleOpenOrFocus, enabled);
  useShortcut('terminal.collapse', onCollapse, enabled && panel.isOpen);
  useShortcut('terminal.new', handleNewTabOrOpen, enabled);
  useShortcut('terminal.close', handleCloseCurrentTab, enabled && panel.isOpen);
  useShortcut('terminal.prevTab', handlePrevTab, enabled && panel.isOpen);
  useShortcut('terminal.nextTab', handleNextTab, enabled && panel.isOpen);

  // Don't render anything if there are no terminals
  if (panel.terminals.length === 0) {
    return null;
  }

  // Keep terminals mounted but hidden when collapsed to preserve buffer content
  return (
    <div
      className={`utility-terminal-panel ${!panel.isOpen ? 'collapsed' : ''}`}
      style={{ height: panel.isOpen ? panel.height : 0 }}
    >
      <ResizeHandle height={panel.height} onHeightChange={onSetHeight} />
      <TabBar
        terminals={panel.terminals}
        activeTabId={panel.activeTabId}
        onSelectTab={onSetActiveTerminal}
        onCloseTab={handleCloseTab}
        onNewTab={handleNewTab}
        onCollapse={onCollapse}
        onRenameTab={onRenameTerminal}
      />
      <div className="utility-terminal-content">
        {panel.terminals.map((terminal) => (
          <div
            key={terminal.id}
            className={`utility-terminal-wrapper ${terminal.id === panel.activeTabId ? 'active' : ''}`}
          >
            <Terminal
              ref={(ref) => {
                if (ref) {
                  terminalRefs.current.set(terminal.id, ref);
                  return;
                }
                // No-op: cleanup is handled by terminal list reconciliation.
              }}
              fontSize={fontSize}
              resolvedTheme={resolvedTheme}
              debugName={`utility:${terminal.title}:${terminal.id}`}
              onInit={handleTerminalInit(terminal.id, terminal.ptyId)}
              onReady={handleTerminalReady(terminal.id, terminal.ptyId)}
              onResize={handleTerminalResize(terminal.ptyId)}
            />
          </div>
        ))}
      </div>
    </div>
  );
}
