import { useRef, useEffect, useCallback, useState, useLayoutEffect } from 'react';
import './PathInput.css';

interface PathInputProps {
  value: string;
  onChange: (value: string) => void;
  onTabComplete: (value: string) => void;
  onSelect: (path: string) => void;
  onSubmit: () => void;
  ghostText: string;
  completionValue?: string;
  hasSelectedSinceTab?: boolean;
  placeholder?: string;
  autoFocus?: boolean;
}

export function PathInput({
  value,
  onChange,
  onTabComplete,
  onSelect,
  onSubmit,
  ghostText,
  completionValue,
  hasSelectedSinceTab = true,
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
    if (e.key === 'Tab' && completionValue) {
      e.preventDefault();
      onTabComplete(completionValue);
    } else if (e.key === 'Enter') {
      e.preventDefault();
      // Decision logic:
      // - If user has intentionally selected (typed or arrowed), accept ghost text as completion
      // - If user just Tabbed (hasSelectedSinceTab=false), confirm the current value
      // - Fall back to completionValue (highlighted row) when input is empty
      const pathToSelect = (ghostText && ghostText.startsWith(value) && hasSelectedSinceTab)
        ? ghostText  // User intentionally selected, accept ghost as completion
        : (value || completionValue);  // User just Tabbed or input empty, use value or highlighted row
      if (pathToSelect) {
        onSelect(pathToSelect);
        onSubmit();
      }
    }
  }, [completionValue, ghostText, hasSelectedSinceTab, onChange, onSelect, onSubmit, onTabComplete, value]);

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
        data-testid="location-picker-path-input"
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
