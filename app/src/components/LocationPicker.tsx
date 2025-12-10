// app/src/components/LocationPicker.tsx
import { useState, useEffect, useCallback, useRef } from 'react';
import { homeDir } from '@tauri-apps/api/path';
import { useLocationHistory } from '../hooks/useLocationHistory';
import './LocationPicker.css';

interface LocationPickerProps {
  isOpen: boolean;
  onClose: () => void;
  onSelect: (path: string) => void;
}

export function LocationPicker({ isOpen, onClose, onSelect }: LocationPickerProps) {
  const [inputValue, setInputValue] = useState('');
  const [selectedIndex, setSelectedIndex] = useState(0);
  const [homePath, setHomePath] = useState('/Users');
  const inputRef = useRef<HTMLInputElement>(null);
  const { getRecentLocations, addToHistory } = useLocationHistory();

  // Get home directory on mount
  useEffect(() => {
    homeDir().then((dir) => {
      setHomePath(dir.replace(/\/$/, ''));
    }).catch(() => {
      // Keep default /Users fallback
    });
  }, []);

  const recentLocations = getRecentLocations();

  // Filter locations based on input
  const filteredLocations = inputValue
    ? recentLocations.filter(
        (loc) =>
          loc.label.toLowerCase().includes(inputValue.toLowerCase()) ||
          loc.path.toLowerCase().includes(inputValue.toLowerCase())
      )
    : recentLocations;

  // Focus input when opened
  useEffect(() => {
    if (isOpen) {
      setInputValue('');
      setSelectedIndex(0);
      setTimeout(() => inputRef.current?.focus(), 50);
    }
  }, [isOpen]);

  const handleSelect = useCallback(
    (path: string) => {
      addToHistory(path);
      onSelect(path);
      onClose();
    },
    [addToHistory, onSelect, onClose]
  );

  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault();
        onClose();
        return;
      }

      if (e.key === 'ArrowDown') {
        e.preventDefault();
        setSelectedIndex((prev) =>
          prev < filteredLocations.length - 1 ? prev + 1 : prev
        );
        return;
      }

      if (e.key === 'ArrowUp') {
        e.preventDefault();
        setSelectedIndex((prev) => (prev > 0 ? prev - 1 : 0));
        return;
      }

      if (e.key === 'Enter') {
        e.preventDefault();
        if (filteredLocations[selectedIndex]) {
          handleSelect(filteredLocations[selectedIndex].path);
        } else if (inputValue.startsWith('/') || inputValue.startsWith('~')) {
          // Direct path input
          const path = inputValue.startsWith('~')
            ? inputValue.replace('~', homePath)
            : inputValue;
          handleSelect(path);
        }
        return;
      }
    },
    [filteredLocations, selectedIndex, inputValue, handleSelect, onClose, homePath]
  );

  if (!isOpen) return null;

  return (
    <div className="location-picker-overlay" onClick={onClose}>
      <div className="location-picker" onClick={(e) => e.stopPropagation()}>
        <div className="picker-header">
          <div className="picker-title">New Session Location</div>
          <div className="picker-input-wrap">
            <input
              ref={inputRef}
              type="text"
              className="picker-input"
              placeholder="Type path or search recent..."
              value={inputValue}
              onChange={(e) => {
                setInputValue(e.target.value);
                setSelectedIndex(0);
              }}
              onKeyDown={handleKeyDown}
            />
          </div>
        </div>

        <div className="picker-results">
          {filteredLocations.length > 0 ? (
            <div className="picker-section">
              <div className="picker-section-title">Recent</div>
              {filteredLocations.map((loc, index) => (
                <div
                  key={loc.path}
                  className={`picker-item ${index === selectedIndex ? 'selected' : ''}`}
                  onClick={() => handleSelect(loc.path)}
                  onMouseEnter={() => setSelectedIndex(index)}
                >
                  <div className="picker-icon">📁</div>
                  <div className="picker-info">
                    <div className="picker-name">{loc.label}</div>
                    <div className="picker-path">{loc.path.replace(homePath, '~')}</div>
                  </div>
                </div>
              ))}
            </div>
          ) : inputValue ? (
            <div className="picker-empty">
              No matches. Press Enter to use path directly.
            </div>
          ) : (
            <div className="picker-empty">No recent locations</div>
          )}
        </div>

        <div className="picker-footer">
          <span className="shortcut"><kbd>↑↓</kbd> navigate</span>
          <span className="shortcut"><kbd>Enter</kbd> select</span>
          <span className="shortcut"><kbd>Esc</kbd> cancel</span>
        </div>
      </div>
    </div>
  );
}
