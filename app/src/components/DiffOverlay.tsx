// app/src/components/DiffOverlay.tsx
import { Suspense, lazy, useEffect, useState, useCallback } from 'react';
import './DiffOverlay.css';

// Lazy load Monaco to reduce initial bundle size
const DiffEditor = lazy(() =>
  import('@monaco-editor/react').then((mod) => ({ default: mod.DiffEditor }))
);

interface DiffOverlayProps {
  isOpen: boolean;
  filePath: string;
  fileIndex: number;
  totalFiles: number;
  onClose: () => void;
  onPrev: () => void;
  onNext: () => void;
  fetchDiff: () => Promise<{ original: string; modified: string }>;
}

export function DiffOverlay({
  isOpen,
  filePath,
  fileIndex,
  totalFiles,
  onClose,
  onPrev,
  onNext,
  fetchDiff,
}: DiffOverlayProps) {
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [original, setOriginal] = useState('');
  const [modified, setModified] = useState('');

  // Load diff content when file changes
  useEffect(() => {
    if (!isOpen) return;

    setLoading(true);
    setError(null);

    fetchDiff()
      .then(({ original, modified }) => {
        setOriginal(original);
        setModified(modified);
        setLoading(false);
      })
      .catch((err) => {
        setError(err.message || 'Failed to load diff');
        setLoading(false);
      });
  }, [isOpen, filePath, fetchDiff]);

  // Keyboard shortcuts
  useEffect(() => {
    if (!isOpen) return;

    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault();
        onClose();
      } else if (e.key === 'ArrowLeft' || e.key === 'k') {
        e.preventDefault();
        onPrev();
      } else if (e.key === 'ArrowRight' || e.key === 'j') {
        e.preventDefault();
        onNext();
      }
    };

    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [isOpen, onClose, onPrev, onNext]);

  // Detect language from file extension
  const getLanguage = useCallback((path: string) => {
    const ext = path.split('.').pop()?.toLowerCase();
    const langMap: Record<string, string> = {
      ts: 'typescript',
      tsx: 'typescript',
      js: 'javascript',
      jsx: 'javascript',
      json: 'json',
      md: 'markdown',
      css: 'css',
      scss: 'scss',
      html: 'html',
      go: 'go',
      py: 'python',
      rs: 'rust',
      yaml: 'yaml',
      yml: 'yaml',
      sh: 'shell',
      bash: 'shell',
    };
    return langMap[ext || ''] || 'plaintext';
  }, []);

  if (!isOpen) return null;

  return (
    <>
      <div className="diff-overlay-backdrop" onClick={onClose} />
      <div className="diff-overlay">
        <div className="diff-header">
          <span className="diff-filename">{filePath}</span>
          <div className="diff-nav">
            <button onClick={onPrev} disabled={fileIndex === 0}>
              ← Prev
            </button>
            <span>
              {fileIndex + 1} / {totalFiles}
            </span>
            <button onClick={onNext} disabled={fileIndex === totalFiles - 1}>
              Next →
            </button>
          </div>
          <button className="diff-close" onClick={onClose}>
            ×
          </button>
        </div>
        <div className="diff-body">
          {loading ? (
            <div className="diff-loading">Loading diff...</div>
          ) : error ? (
            <div className="diff-error">{error}</div>
          ) : (
            <Suspense fallback={<div className="diff-loading">Loading editor...</div>}>
              <DiffEditor
                original={original}
                modified={modified}
                language={getLanguage(filePath)}
                theme="vs-dark"
                options={{
                  readOnly: true,
                  renderSideBySide: true,
                  minimap: { enabled: false },
                  scrollBeyondLastLine: false,
                  fontSize: 12,
                  fontFamily: "'JetBrains Mono', monospace",
                  lineNumbers: 'on',
                  renderLineHighlight: 'none',
                  scrollbar: {
                    verticalScrollbarSize: 8,
                    horizontalScrollbarSize: 8,
                  },
                }}
              />
            </Suspense>
          )}
        </div>
      </div>
    </>
  );
}
