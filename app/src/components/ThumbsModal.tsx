import { useState, useEffect, useRef, useCallback, useMemo } from 'react';
import { invoke } from '@tauri-apps/api/core';
import { openUrl } from '@tauri-apps/plugin-opener';
import FocusTrap from 'focus-trap-react';
import './ThumbsModal.css';

interface PatternMatch {
  pattern_type: 'url' | 'path' | 'ip_port';
  value: string;
  hint: string;
}

interface ThumbsModalProps {
  isOpen: boolean;
  terminalText: string;
  onClose: () => void;
  onCopy: (value: string) => void;
}

const TYPE_ICONS: Record<string, string> = {
  url: '🔗',
  path: '📁',
  ip_port: '🌐',
};

export function ThumbsModal({ isOpen, terminalText, onClose, onCopy }: ThumbsModalProps) {
  const [patterns, setPatterns] = useState<PatternMatch[]>([]);
  const [filter, setFilter] = useState('');
  const [isFiltering, setIsFiltering] = useState(false);
  const [hintBuffer, setHintBuffer] = useState('');
  const [error, setError] = useState<string | null>(null);
  const filterInputRef = useRef<HTMLInputElement>(null);
  const hintTimeoutRef = useRef<number | null>(null);

  // Extract patterns when modal opens
  useEffect(() => {
    if (isOpen && terminalText) {
      setError(null);
      invoke<PatternMatch[]>('extract_patterns', { text: terminalText })
        .then(setPatterns)
        .catch((err) => {
          console.error('Failed to extract patterns:', err);
          setError('Failed to extract patterns from terminal');
        });
    }
    // Reset state when modal opens/closes
    if (isOpen) {
      // Clear any pending hint timeout from previous session (app-inx)
      if (hintTimeoutRef.current) {
        clearTimeout(hintTimeoutRef.current);
        hintTimeoutRef.current = null;
      }
      setFilter('');
      setIsFiltering(false);
      setHintBuffer('');
    }
  }, [isOpen, terminalText]);

  // Focus filter input when entering filter mode
  useEffect(() => {
    if (isFiltering && filterInputRef.current) {
      filterInputRef.current.focus();
    }
  }, [isFiltering]);

  // Memoize filtered patterns (app-hxq)
  const filteredPatterns = useMemo(() =>
    patterns.filter(p => p.value.toLowerCase().includes(filter.toLowerCase())),
    [patterns, filter]
  );

  const handleAction = useCallback(async (pattern: PatternMatch, action: 'copy' | 'open') => {
    try {
      if (action === 'copy') {
        await navigator.clipboard.writeText(pattern.value);
        onCopy(pattern.value);
      } else {
        // Open action
        if (pattern.pattern_type === 'url') {
          // Add protocol if missing (for localhost)
          const url = pattern.value.startsWith('http') ? pattern.value : `http://${pattern.value}`;
          await openUrl(url);
        } else if (pattern.pattern_type === 'ip_port') {
          await openUrl(`http://${pattern.value}`);
        } else {
          // Path - use openUrl which should handle file:// URIs
          // For local paths, prepend file:// protocol
          const fileUrl = pattern.value.startsWith('/')
            ? `file://${pattern.value}`
            : pattern.value;
          await openUrl(fileUrl);
        }
      }
      onClose();
    } catch (err) {
      console.error(`Failed to ${action}:`, err);
      setError(`Failed to ${action === 'copy' ? 'copy to clipboard' : 'open'}`);
    }
  }, [onClose, onCopy]);

  const findPatternByHint = useCallback((hint: string): PatternMatch | undefined => {
    return filteredPatterns.find(p => p.hint === hint.toLowerCase());
  }, [filteredPatterns]);

  const processHint = useCallback((hint: string, withShift: boolean) => {
    const pattern = findPatternByHint(hint);
    if (pattern) {
      handleAction(pattern, withShift ? 'open' : 'copy');
    }
  }, [findPatternByHint, handleAction]);

  // Handle keyboard input
  useEffect(() => {
    if (!isOpen) return;

    const handleKeyDown = (e: KeyboardEvent) => {
      // Escape handling
      if (e.key === 'Escape') {
        e.preventDefault();
        if (isFiltering) {
          setIsFiltering(false);
          setFilter('');
        } else {
          onClose();
        }
        return;
      }

      // Don't process hints when filtering and focused on input
      if (isFiltering && document.activeElement === filterInputRef.current) {
        return;
      }

      // Enter filter mode with /
      if (e.key === '/' && !isFiltering) {
        e.preventDefault();
        setIsFiltering(true);
        return;
      }

      // Single letter hint
      if (e.key.length === 1 && e.key.match(/[a-z]/i) && !e.metaKey && !e.ctrlKey && !e.altKey) {
        e.preventDefault();
        const letter = e.key.toLowerCase();
        // Capture shift state immediately (app-tb9 fix)
        const withShift = e.shiftKey;

        // Check if we have two-letter hints
        const hasTwoLetterHints = patterns.some(p => p.hint.length === 2);

        if (hasTwoLetterHints && hintBuffer === '') {
          // First letter - buffer it
          setHintBuffer(letter);

          // Try single-letter match after delay
          hintTimeoutRef.current = window.setTimeout(() => {
            processHint(letter, withShift);
            setHintBuffer('');
          }, 300);
        } else if (hintBuffer) {
          // Second letter - try two-letter hint
          if (hintTimeoutRef.current) {
            clearTimeout(hintTimeoutRef.current);
          }
          processHint(hintBuffer + letter, withShift);
          setHintBuffer('');
        } else {
          // No two-letter hints, immediate match
          processHint(letter, withShift);
        }
      }
    };

    window.addEventListener('keydown', handleKeyDown);
    return () => {
      window.removeEventListener('keydown', handleKeyDown);
      if (hintTimeoutRef.current) {
        clearTimeout(hintTimeoutRef.current);
      }
    };
  }, [isOpen, isFiltering, hintBuffer, patterns, processHint, onClose]);

  if (!isOpen) return null;

  return (
    <div className="thumbs-overlay" onClick={onClose}>
      <FocusTrap
        focusTrapOptions={{
          allowOutsideClick: true,
          escapeDeactivates: false, // We handle Escape ourselves
        }}
      >
        <div
          className="thumbs-modal"
          onClick={e => e.stopPropagation()}
          role="dialog"
          aria-modal="true"
          aria-labelledby="thumbs-modal-title"
        >
        <div className="thumbs-header">
          <h2 id="thumbs-modal-title">Quick Find</h2>
          <button
            className="thumbs-close"
            onClick={onClose}
            aria-label="Close Quick Find"
            type="button"
          >
            ×
          </button>
        </div>

        {isFiltering && (
          <div className="thumbs-filter">
            <span className="filter-prefix">/</span>
            <input
              ref={filterInputRef}
              type="text"
              value={filter}
              onChange={e => setFilter(e.target.value)}
              placeholder="Filter..."
              aria-label="Filter patterns"
              className="filter-input"
            />
          </div>
        )}

        <div className="thumbs-body">
          {error ? (
            <div className="thumbs-error">{error}</div>
          ) : filteredPatterns.length === 0 ? (
            <div className="thumbs-empty">
              {patterns.length === 0
                ? 'No URLs, paths, or addresses found'
                : 'No matches for filter'}
            </div>
          ) : (
            <div className="thumbs-list">
              {filteredPatterns.map((p) => (
                <div
                  key={p.value}
                  className="thumbs-item"
                  onClick={() => handleAction(p, 'copy')}
                >
                  <span className={`thumbs-hint ${hintBuffer && p.hint.startsWith(hintBuffer) ? 'active' : ''}`}>
                    {p.hint}
                  </span>
                  <span className="thumbs-icon" aria-hidden="true">{TYPE_ICONS[p.pattern_type]}</span>
                  <span className="thumbs-value" title={p.value}>{p.value}</span>
                </div>
              ))}
            </div>
          )}
        </div>

        <div className="thumbs-footer">
          <span>type hint to copy</span>
          <span className="footer-sep">·</span>
          <span>⇧+hint to open</span>
          <span className="footer-sep">·</span>
          <span>/ search</span>
        </div>
        </div>
      </FocusTrap>
    </div>
  );
}
