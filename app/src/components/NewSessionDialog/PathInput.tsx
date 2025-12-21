import { useState, useRef, useEffect, useCallback } from 'react';
import './PathInput.css';

interface PathInputProps {
  value: string;
  onChange: (value: string) => void;
  onSelect: (path: string) => void;
  ghostText: string;
  placeholder?: string;
  autoFocus?: boolean;
}

export function PathInput({
  value,
  onChange,
  onSelect,
  ghostText,
  placeholder = 'Type path (e.g., ~/projects)...',
  autoFocus = true,
}: PathInputProps) {
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (autoFocus) {
      inputRef.current?.focus();
    }
  }, [autoFocus]);

  const handleKeyDown = useCallback((e: React.KeyboardEvent) => {
    if (e.key === 'Tab' && ghostText) {
      e.preventDefault();
      // Complete to ghost text
      onChange(ghostText);
    } else if (e.key === 'Enter') {
      e.preventDefault();
      onSelect(value || ghostText);
    }
  }, [ghostText, value, onChange, onSelect]);

  // Calculate ghost text to show (portion not yet typed)
  const visibleGhost = ghostText.startsWith(value)
    ? ghostText.slice(value.length)
    : '';

  return (
    <div className="path-input-container">
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
        <span className="path-ghost">{visibleGhost}</span>
      )}
    </div>
  );
}
