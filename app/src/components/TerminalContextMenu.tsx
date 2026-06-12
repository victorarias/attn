import { useLayoutEffect, useRef } from 'react';
import './TerminalContextMenu.css';

export interface TerminalContextMenuItem {
  id: string;
  label: string;
  // Display-only accelerator hint; the real shortcuts live on the terminal.
  shortcut?: string;
  disabled?: boolean;
  separatorBefore?: boolean;
}

interface TerminalContextMenuProps {
  // Position in pixels relative to the offset parent (the terminal frame).
  position: { x: number; y: number };
  items: TerminalContextMenuItem[];
  onSelect: (id: string) => void;
  onClose: () => void;
}

export function TerminalContextMenu({ position, items, onSelect, onClose }: TerminalContextMenuProps) {
  const menuRef = useRef<HTMLDivElement>(null);

  // Keep the menu inside the frame: flip/clamp after the first layout.
  useLayoutEffect(() => {
    const menu = menuRef.current;
    const frame = menu?.offsetParent as HTMLElement | null;
    if (!menu || !frame) return;
    let left = position.x;
    let top = position.y;
    if (left + menu.offsetWidth > frame.clientWidth) {
      left = Math.max(0, frame.clientWidth - menu.offsetWidth - 4);
    }
    if (top + menu.offsetHeight > frame.clientHeight) {
      top = Math.max(0, frame.clientHeight - menu.offsetHeight - 4);
    }
    menu.style.left = `${left}px`;
    menu.style.top = `${top}px`;
  }, [position]);

  useLayoutEffect(() => {
    const handlePointerDown = (event: MouseEvent) => {
      if (menuRef.current && event.target instanceof Node && menuRef.current.contains(event.target)) return;
      onClose();
    };
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        event.stopPropagation();
        onClose();
      }
    };
    document.addEventListener('mousedown', handlePointerDown, true);
    document.addEventListener('keydown', handleKeyDown, true);
    return () => {
      document.removeEventListener('mousedown', handlePointerDown, true);
      document.removeEventListener('keydown', handleKeyDown, true);
    };
  }, [onClose]);

  return (
    <div
      ref={menuRef}
      className="ghostty-context-menu"
      data-testid="terminal-context-menu"
      style={{ left: position.x, top: position.y }}
      role="menu"
      // The terminal container must not treat menu interaction as terminal
      // mouse input or steal focus mid-click.
      onMouseDown={(event) => event.stopPropagation()}
      onContextMenu={(event) => event.preventDefault()}
    >
      {items.map((item) => (
        <div key={item.id} className="ghostty-context-menu-group">
          {item.separatorBefore && <div className="ghostty-context-menu-separator" role="separator" />}
          <button
            type="button"
            role="menuitem"
            className="ghostty-context-menu-item"
            data-testid={`terminal-context-menu-${item.id}`}
            disabled={item.disabled}
            onClick={() => {
              if (item.disabled) return;
              onSelect(item.id);
            }}
          >
            <span className="ghostty-context-menu-label">{item.label}</span>
            {item.shortcut && <span className="ghostty-context-menu-shortcut">{item.shortcut}</span>}
          </button>
        </div>
      ))}
    </div>
  );
}
