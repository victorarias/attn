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

      // Create terminal
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

      // Store refs immediately so imperative handle works
      xtermRef.current = term;
      fitAddonRef.current = fitAddon;

      // Use ResizeObserver to wait for container to have real dimensions
      // This ensures we don't spawn PTY until terminal can calculate correct size
      let readyFired = false;
      const observer = new ResizeObserver((entries) => {
        const entry = entries[0];
        if (readyFired) return;

        // Wait until container has actual dimensions
        if (entry.contentRect.width > 0 && entry.contentRect.height > 0) {
          // Fit to get correct dimensions
          fitAddon.fit();

          // Only proceed if we got valid (non-default) dimensions
          // or if we've waited long enough (fallback after multiple frames)
          if (term.cols > 0 && term.rows > 0) {
            readyFired = true;
            observer.disconnect();

            if (onResizeRef.current) {
              onResizeRef.current(term.cols, term.rows);
            }

            // NOW notify that terminal is ready with correct dimensions
            if (onReadyRef.current) {
              onReadyRef.current(term);
            }
          }
        }
      });
      observer.observe(containerRef.current);

      // Handle resize with debounce
      // Key insight: We need to resize PTY BEFORE xterm.js display to avoid race condition
      // 1. Calculate new dimensions with proposeDimensions()
      // 2. Resize PTY first (sends SIGWINCH to Claude Code)
      // 3. Wait for Claude Code to process the resize
      // 4. Then resize xterm.js display with fit()
      let resizeTimeout: number;
      const handleResize = () => {
        clearTimeout(resizeTimeout);
        resizeTimeout = window.setTimeout(() => {
          // Calculate what dimensions would be without applying them yet
          const proposedDims = fitAddon.proposeDimensions();
          if (proposedDims && proposedDims.cols > 0 && proposedDims.rows > 0) {
            // Tell PTY about new size first (sends SIGWINCH to Claude Code)
            if (onResizeRef.current) {
              onResizeRef.current(proposedDims.cols, proposedDims.rows);
            }
            // Wait for Claude Code to process SIGWINCH and re-render
            // Then update xterm.js display to match
            setTimeout(() => {
              fitAddon.fit();
            }, 50);
          } else {
            // Fallback: just fit if proposeDimensions fails
            fitAddon.fit();
            if (onResizeRef.current) {
              onResizeRef.current(term.cols, term.rows);
            }
          }
        }, 100);
      };
      window.addEventListener('resize', handleResize);

      // Cleanup
      return () => {
        observer.disconnect();
        clearTimeout(resizeTimeout);
        window.removeEventListener('resize', handleResize);
        term.dispose();
      };
    }, []);

    return <div ref={containerRef} className="terminal-container" />;
  }
);
