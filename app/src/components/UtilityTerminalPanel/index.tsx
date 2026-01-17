// app/src/components/UtilityTerminalPanel/index.tsx
import { useRef, useCallback, useEffect } from 'react';
import { Terminal as XTerm } from '@xterm/xterm';
import { Terminal, TerminalHandle } from '../Terminal';
import { TabBar } from './TabBar';
import { ResizeHandle } from './ResizeHandle';
import { useShortcut } from '../../shortcuts';
import type { TerminalPanelState } from '../../store/sessions';
import { listenPtyEvents, ptyKill, ptyResize, ptySpawn, ptyWrite } from '../../pty/bridge';
import './UtilityTerminalPanel.css';

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
  enabled,
}: UtilityTerminalPanelProps) {
  const terminalRefs = useRef<Map<string, TerminalHandle>>(new Map());
  const xtermRefs = useRef<Map<string, XTerm>>(new Map());
  const ptyIdToTerminalId = useRef<Map<string, string>>(new Map());

  // Listen for PTY events for utility terminals
  useEffect(() => {
    const unlisten = listenPtyEvents((event) => {
      const msg = event.payload;
      const terminalId = ptyIdToTerminalId.current.get(msg.id);
      if (!terminalId) return;

      const xterm = xtermRefs.current.get(terminalId);
      if (!xterm) return;

      switch (msg.event) {
        case 'data': {
          const binaryStr = atob(msg.data);
          const bytes = Uint8Array.from(binaryStr, (c) => c.charCodeAt(0));
          const data = new TextDecoder('utf-8').decode(bytes);
          xterm.write(data);
          break;
        }
        case 'exit': {
          xterm.write(`\r\n[Process exited with code ${msg.code}]\r\n`);
          break;
        }
        case 'error': {
          xterm.write(`\r\n[Error: ${msg.error}]\r\n`);
          break;
        }
      }
    });

    return () => {
      unlisten.then((fn) => fn());
    };
  }, []);

  const spawnTerminal = useCallback(async () => {
    const ptyId = `util-pty-${Date.now()}`;

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

      const terminalId = onAddTerminal(ptyId);
      ptyIdToTerminalId.current.set(ptyId, terminalId);

      return terminalId;
    } catch (e) {
      console.error('[UtilityTerminal] Spawn failed:', e);
      return null;
    }
  }, [cwd, onAddTerminal]);

  const handleNewTab = useCallback(async () => {
    const terminalId = await spawnTerminal();
    if (terminalId) {
      // Focus after terminal renders
      setTimeout(() => {
        terminalRefs.current.get(terminalId)?.focus();
      }, 50);
    }
  }, [spawnTerminal]);

  const handleCloseTab = useCallback(
    (terminalId: string) => {
      const terminal = panel.terminals.find((t) => t.id === terminalId);
      if (terminal) {
        ptyKill({ id: terminal.ptyId }).catch(console.error);
        ptyIdToTerminalId.current.delete(terminal.ptyId);
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
        setTimeout(() => {
          terminalRefs.current.get(panel.activeTabId!)?.focus();
        }, 50);
      }
    } else if (panel.activeTabId) {
      terminalRefs.current.get(panel.activeTabId)?.focus();
    }
  }, [panel.isOpen, panel.terminals.length, panel.activeTabId, onOpen, handleNewTab]);

  const handleNewTabOrOpen = useCallback(async () => {
    if (!panel.isOpen) {
      onOpen();
    }
    await handleNewTab();
  }, [panel.isOpen, onOpen, handleNewTab]);

  const handleTerminalReady = useCallback(
    (terminalId: string, ptyId: string) => (xterm: XTerm) => {
      xtermRefs.current.set(terminalId, xterm);

      // Terminal input -> PTY
      xterm.onData((data: string) => {
        ptyWrite({ id: ptyId, data }).catch(console.error);
      });
    },
    []
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
                if (ref) terminalRefs.current.set(terminal.id, ref);
              }}
              fontSize={fontSize}
              onReady={handleTerminalReady(terminal.id, terminal.ptyId)}
              onResize={handleTerminalResize(terminal.ptyId)}
            />
          </div>
        ))}
      </div>
    </div>
  );
}
