// app/src/components/UtilityTerminalPanel/TabBar.tsx
import { useState, useRef, useEffect } from 'react';
import type { UtilityTerminal } from '../../store/sessions';
import './TabBar.css';

interface TabBarProps {
  terminals: UtilityTerminal[];
  activeTabId: string | null;
  onSelectTab: (id: string) => void;
  onCloseTab: (id: string) => void;
  onNewTab: () => void;
  onCollapse: () => void;
  onRenameTab: (id: string, title: string) => void;
}

export function TabBar({
  terminals,
  activeTabId,
  onSelectTab,
  onCloseTab,
  onNewTab,
  onCollapse,
  onRenameTab,
}: TabBarProps) {
  const [editingTabId, setEditingTabId] = useState<string | null>(null);
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (editingTabId && inputRef.current) {
      inputRef.current.focus();
      inputRef.current.select();
    }
  }, [editingTabId]);

  const commitRename = (id: string, value: string) => {
    const trimmed = value.trim();
    if (trimmed) {
      onRenameTab(id, trimmed);
    }
    setEditingTabId(null);
  };

  return (
    <div className="terminal-tabbar">
      <div className="terminal-tabs">
        {terminals.map((terminal) => (
          <div
            key={terminal.id}
            className={`terminal-tab ${terminal.id === activeTabId ? 'active' : ''}`}
            onClick={() => onSelectTab(terminal.id)}
          >
            {editingTabId === terminal.id ? (
              <input
                ref={inputRef}
                className="terminal-tab-input"
                defaultValue={terminal.title}
                onBlur={(e) => commitRename(terminal.id, e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') {
                    commitRename(terminal.id, e.currentTarget.value);
                  } else if (e.key === 'Escape') {
                    setEditingTabId(null);
                  }
                  e.stopPropagation();
                }}
                onClick={(e) => e.stopPropagation()}
              />
            ) : (
              <span
                className="terminal-tab-title"
                onDoubleClick={(e) => {
                  e.stopPropagation();
                  setEditingTabId(terminal.id);
                }}
              >
                {terminal.title}
              </span>
            )}
            <button
              className="terminal-tab-close"
              onClick={(e) => {
                e.stopPropagation();
                onCloseTab(terminal.id);
              }}
              title="Close terminal"
            >
              ×
            </button>
          </div>
        ))}
        <button className="terminal-tab-new" onClick={onNewTab} title="New terminal (⌘T)">
          +
        </button>
      </div>
      <button className="terminal-collapse" onClick={onCollapse} title="Collapse panel (⇧`)">
        ─
      </button>
    </div>
  );
}
