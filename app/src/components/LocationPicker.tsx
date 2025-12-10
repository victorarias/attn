// app/src/components/LocationPicker.tsx
import { useState, useEffect, useCallback, useRef } from 'react';
import { homeDir } from '@tauri-apps/api/path';
import { useLocationHistory } from '../hooks/useLocationHistory';
import { useFilesystemSuggestions } from '../hooks/useFilesystemSuggestions';
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
  const { suggestions: fsSuggestions, loading, currentDir } = useFilesystemSuggestions(inputValue);

  // Get home directory on mount
  useEffect(() => {
    homeDir().then((dir) => {
      setHomePath(dir.replace(/\/$/, ''));
    }).catch(() => {});
  }, []);

  const recentLocations = getRecentLocations();

  // Filter recent locations based on input
  const filteredRecent = inputValue
    ? recentLocations.filter(
        (loc) =>
          loc.label.toLowerCase().includes(inputValue.toLowerCase()) ||
          loc.path.toLowerCase().includes(inputValue.toLowerCase())
      )
    : recentLocations;

  // Combine suggestions: filesystem first, then recent
  const allSuggestions = [
    ...fsSuggestions.map(s => ({ type: 'dir' as const, ...s })),
    ...filteredRecent.slice(0, 10).map(loc => ({
      type: 'recent' as const,
      name: loc.label,
      path: loc.path
    })),
  ];

  const totalSuggestions = allSuggestions.length;

  // Reset selection when suggestions change
  useEffect(() => {
    setSelectedIndex(0);
  }, [inputValue]);

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
        setSelectedIndex((prev) => Math.min(prev + 1, totalSuggestions - 1));
        return;
      }

      if (e.key === 'ArrowUp') {
        e.preventDefault();
        setSelectedIndex((prev) => Math.max(prev - 1, 0));
        return;
      }

      // Tab autocompletes the selected directory suggestion
      if (e.key === 'Tab' && fsSuggestions.length > 0) {
        e.preventDefault();
        const selected = allSuggestions[selectedIndex];
        if (selected && selected.type === 'dir') {
          setInputValue(selected.path.replace(homePath, '~'));
        }
        return;
      }

      if (e.key === 'Enter') {
        e.preventDefault();
        const selected = allSuggestions[selectedIndex];
        if (selected) {
          if (selected.type === 'dir') {
            // For directories, expand to input for further navigation or select
            const expanded = selected.path;
            // If user presses Enter on a dir, select it
            handleSelect(expanded);
          } else {
            handleSelect(selected.path);
          }
        } else if (inputValue.startsWith('/') || inputValue.startsWith('~')) {
          // Direct path input
          const path = inputValue.startsWith('~')
            ? inputValue.replace('~', homePath)
            : inputValue;
          handleSelect(path.replace(/\/$/, '')); // Remove trailing slash
        }
        return;
      }
    },
    [allSuggestions, selectedIndex, inputValue, handleSelect, onClose, homePath, fsSuggestions.length, totalSuggestions]
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
              placeholder="Type path (e.g., ~/projects) or search..."
              value={inputValue}
              onChange={(e) => setInputValue(e.target.value)}
              onKeyDown={handleKeyDown}
            />
            {loading && <div className="picker-loading" />}
          </div>
          {currentDir && (
            <div className="picker-breadcrumb">
              <span className="picker-breadcrumb-label">Browsing:</span>
              <span className="picker-breadcrumb-path">{currentDir}</span>
            </div>
          )}
        </div>

        <div className="picker-results">
          {/* Filesystem suggestions */}
          {fsSuggestions.length > 0 && (
            <div className="picker-section">
              <div className="picker-section-title">Directories</div>
              {fsSuggestions.map((item, index) => (
                <div
                  key={item.path}
                  className={`picker-item ${index === selectedIndex ? 'selected' : ''}`}
                  onClick={() => handleSelect(item.path)}
                  onMouseEnter={() => setSelectedIndex(index)}
                >
                  <div className="picker-icon">📁</div>
                  <div className="picker-info">
                    <div className="picker-name">{item.name}</div>
                  </div>
                </div>
              ))}
            </div>
          )}

          {/* Recent locations */}
          {filteredRecent.length > 0 && (
            <div className="picker-section">
              <div className="picker-section-title">Recent</div>
              {filteredRecent.slice(0, 10).map((loc, index) => {
                const globalIndex = fsSuggestions.length + index;
                return (
                  <div
                    key={loc.path}
                    className={`picker-item ${globalIndex === selectedIndex ? 'selected' : ''}`}
                    onClick={() => handleSelect(loc.path)}
                    onMouseEnter={() => setSelectedIndex(globalIndex)}
                  >
                    <div className="picker-icon">🕐</div>
                    <div className="picker-info">
                      <div className="picker-name">{loc.label}</div>
                      <div className="picker-path">{loc.path.replace(homePath, '~')}</div>
                    </div>
                  </div>
                );
              })}
            </div>
          )}

          {/* Empty state */}
          {fsSuggestions.length === 0 && filteredRecent.length === 0 && (
            <div className="picker-empty">
              {inputValue
                ? 'No matches. Press Enter to use path directly.'
                : 'Type a path to browse directories'}
            </div>
          )}
        </div>

        <div className="picker-footer">
          <span className="shortcut"><kbd>↑↓</kbd> navigate</span>
          <span className="shortcut"><kbd>Tab</kbd> autocomplete</span>
          <span className="shortcut"><kbd>Enter</kbd> select</span>
          <span className="shortcut"><kbd>Esc</kbd> cancel</span>
        </div>
      </div>
    </div>
  );
}
