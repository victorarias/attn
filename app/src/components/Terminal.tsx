import { useEffect, useRef, useImperativeHandle, forwardRef } from 'react';
import { Terminal as XTerm } from '@xterm/xterm';
import { FitAddon } from '@xterm/addon-fit';
import { WebLinksAddon } from '@xterm/addon-web-links';
import '@xterm/xterm/css/xterm.css';
import './Terminal.css';

export interface TerminalHandle {
  terminal: XTerm | null;
  fit: () => void;
  focus: () => void;
}

interface TerminalProps {
  onReady?: (terminal: XTerm) => void;
  onResize?: (cols: number, rows: number) => void;
}

export const Terminal = forwardRef<TerminalHandle, TerminalProps>(
  function Terminal({ onReady, onResize }, ref) {
    const containerRef = useRef<HTMLDivElement>(null);
    const xtermRef = useRef<XTerm | null>(null);
    const fitAddonRef = useRef<FitAddon | null>(null);

    // Store callbacks in refs to avoid re-running effect when they change
    const onReadyRef = useRef(onReady);
    const onResizeRef = useRef(onResize);

    useEffect(() => {
      onReadyRef.current = onReady;
      onResizeRef.current = onResize;
    });

    useImperativeHandle(ref, () => ({
      terminal: xtermRef.current,
      fit: () => {
        if (fitAddonRef.current && xtermRef.current) {
          fitAddonRef.current.fit();
          if (onResize) {
            onResize(xtermRef.current.cols, xtermRef.current.rows);
          }
        }
      },
      focus: () => {
        xtermRef.current?.focus();
      },
    }));

    useEffect(() => {
      if (!containerRef.current) return;

      // Create terminal with better settings
      const term = new XTerm({
        cursorBlink: true,
        fontSize: 14,
        fontFamily: 'Menlo, Monaco, "Courier New", monospace',
        scrollback: 10000,
        convertEol: true,
        theme: {
          background: '#1e1e1e',
          foreground: '#d4d4d4',
          cursor: '#d4d4d4',
          cursorAccent: '#1e1e1e',
          selectionBackground: '#264f78',
        },
      });

      // Add addons
      const fitAddon = new FitAddon();
      term.loadAddon(fitAddon);
      term.loadAddon(new WebLinksAddon());

      // Open terminal in container
      term.open(containerRef.current);

      // Initial fit after a small delay to ensure DOM is ready
      requestAnimationFrame(() => {
        fitAddon.fit();
        if (onResizeRef.current) {
          onResizeRef.current(term.cols, term.rows);
        }
      });

      // Store refs
      xtermRef.current = term;
      fitAddonRef.current = fitAddon;

      // Notify that terminal is ready
      if (onReadyRef.current) {
        onReadyRef.current(term);
      }

      // Handle resize with debounce
      let resizeTimeout: number;
      const handleResize = () => {
        clearTimeout(resizeTimeout);
        resizeTimeout = window.setTimeout(() => {
          fitAddon.fit();
          if (onResizeRef.current) {
            onResizeRef.current(term.cols, term.rows);
          }
        }, 100);
      };
      window.addEventListener('resize', handleResize);

      // Cleanup
      return () => {
        clearTimeout(resizeTimeout);
        window.removeEventListener('resize', handleResize);
        term.dispose();
      };
    }, []);

    return <div ref={containerRef} className="terminal-container" />;
  }
);
