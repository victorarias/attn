import { useRef, useEffect, useCallback, useState, useLayoutEffect } from 'react';
import './PathInput.css';

interface PathInputProps {
  value: string;
  onChange: (value: string) => void;
  onTabComplete: (value: string) => void;
  onSelect: (path: string) => void;
  ghostText: string;
  hasSelectedSinceTab: boolean;
  placeholder?: string;
  autoFocus?: boolean;
}

export function PathInput({
  value,
  onChange,
  onTabComplete,
  onSelect,
  ghostText,
  hasSelectedSinceTab,
  placeholder = 'Type path (e.g., ~/projects)...',
  autoFocus = true,
}: PathInputProps) {
  const inputRef = useRef<HTMLInputElement>(null);
  const measureRef = useRef<HTMLSpanElement>(null);
  const [ghostOffset, setGhostOffset] = useState(0);

  useEffect(() => {
    if (autoFocus) {
      inputRef.current?.focus();
    }
  }, [autoFocus]);

  // Measure text width to position ghost text
  useLayoutEffect(() => {
    if (measureRef.current) {
      setGhostOffset(measureRef.current.offsetWidth);
    }
  }, [value]);

  const handleKeyDown = useCallback((e: React.KeyboardEvent) => {
    if (e.key === 'Tab' && ghostText) {
      e.preventDefault();
      // Complete to ghost text - use onTabComplete to signal this was Tab (not typing)
      onTabComplete(ghostText);
    } else if (e.key === 'Enter') {
      e.preventDefault();
      // Decision logic:
      // - If user has intentionally selected (typed or arrowed), accept ghost text as completion
      // - If user just Tabbed (hasSelectedSinceTab=false), confirm the current value
      const pathToSelect = (ghostText && ghostText.startsWith(value) && hasSelectedSinceTab)
        ? ghostText  // User intentionally selected, accept ghost as completion
        : value;     // User just Tabbed, confirm current path
      if (pathToSelect) {
        onSelect(pathToSelect);
      }
    }
  }, [ghostText, value, onTabComplete, onSelect, hasSelectedSinceTab]);

  // Calculate ghost text to show (portion not yet typed)
  const visibleGhost = ghostText.startsWith(value)
    ? ghostText.slice(value.length)
    : '';

  return (
    <div className="path-input-container">
      {/* Hidden span to measure typed text width */}
      <span ref={measureRef} className="path-input-measure" aria-hidden="true">
        {value}
      </span>
      <input
        ref={inputRef}
        type="text"
        className="path-input"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        onKeyDown={handleKeyDown}
        placeholder={placeholder}
        spellCheck={false}
        autoComplete="off"
      />
      {visibleGhost && (
        <span
          className="path-ghost"
          style={{ left: 16 + ghostOffset }}
        >
          {visibleGhost}
        </span>
      )}
    </div>
  );
}
