// app/src/components/DiffOverlay.tsx
import { Suspense, lazy, useEffect, useState, useCallback, useRef } from 'react';
import type { Monaco } from '@monaco-editor/react';
import type { editor } from 'monaco-editor';
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
  fontSize?: number;
  onClose: () => void;
  onPrev: () => void;
  onNext: () => void;
  fetchDiff: () => Promise<{ original: string; modified: string }>;
  onSendToClaude?: (reference: string) => void;
}

export function DiffOverlay({
  isOpen,
  filePath,
  fileIndex,
  totalFiles,
  fontSize = 12,
  onClose,
  onPrev,
  onNext,
  fetchDiff,
  onSendToClaude,
}: DiffOverlayProps) {
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [original, setOriginal] = useState('');
  const [modified, setModified] = useState('');

  // Selection tracking for "Send to Claude" widget
  const [selection, setSelection] = useState<{
    startLine: number;
    endLine: number;
    endColumn: number;
  } | null>(null);
  const editorRef = useRef<editor.IStandaloneCodeEditor | null>(null);
  const monacoRef = useRef<Monaco | null>(null);
  const widgetRef = useRef<editor.IContentWidget | null>(null);

  // Keep fetchDiff in a ref to avoid re-triggering the effect when it changes
  // (it changes when sessions/daemonSessions update, but we only want to refetch
  // when the overlay opens or the file changes)
  const fetchDiffRef = useRef(fetchDiff);
  fetchDiffRef.current = fetchDiff;

  // Load diff content when file changes
  useEffect(() => {
    if (!isOpen) return;

    setLoading(true);
    setError(null);

    fetchDiffRef.current()
      .then(({ original, modified }) => {
        setOriginal(original);
        setModified(modified);
        setLoading(false);
      })
      .catch((err) => {
        setError(err.message || 'Failed to load diff');
        setLoading(false);
      });
  }, [isOpen, filePath]);

  // Keyboard shortcuts - use capture phase to intercept before terminal
  useEffect(() => {
    if (!isOpen) return;

    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault();
        e.stopPropagation();
        onClose();
      } else if (e.key === 'ArrowLeft' || e.key === 'k') {
        e.preventDefault();
        e.stopPropagation();
        onPrev();
      } else if (e.key === 'ArrowRight' || e.key === 'j') {
        e.preventDefault();
        e.stopPropagation();
        onNext();
      }
    };

    window.addEventListener('keydown', handleKeyDown, true);
    return () => window.removeEventListener('keydown', handleKeyDown, true);
  }, [isOpen, onClose, onPrev, onNext]);

  // Handle sending selection to Claude
  const handleSendToClaude = useCallback(() => {
    if (!selection || !onSendToClaude) return;

    const lineRef =
      selection.startLine === selection.endLine
        ? `L${selection.startLine}`
        : `L${selection.startLine}-L${selection.endLine}`;

    const reference = `@${filePath}:${lineRef}`;
    onSendToClaude(reference);
    onClose(); // Close the diff overlay after sending
  }, [selection, onSendToClaude, filePath, onClose]);

  // Content widget for "Send to Claude" popup
  useEffect(() => {
    const editor = editorRef.current;
    const monaco = monacoRef.current;
    if (!editor || !monaco || !onSendToClaude) return;

    // Remove existing widget if any
    if (widgetRef.current) {
      editor.removeContentWidget(widgetRef.current);
      widgetRef.current = null;
    }

    // Only show widget if there's a selection
    if (!selection) return;

    // Create widget DOM node
    const domNode = document.createElement('div');
    domNode.className = 'send-to-claude-widget';
    domNode.title = 'Send to Claude Code';
    domNode.onclick = handleSendToClaude;

    // Render the Claude AI icon
    const iconSvg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
    iconSvg.setAttribute('width', '16');
    iconSvg.setAttribute('height', '16');
    iconSvg.setAttribute('viewBox', '0 0 1200 1200');
    iconSvg.innerHTML = `<path fill="currentColor" d="M 233.959793 800.214905 L 468.644287 668.536987 L 472.590637 657.100647 L 468.644287 650.738403 L 457.208069 650.738403 L 417.986633 648.322144 L 283.892639 644.69812 L 167.597321 639.865845 L 54.926208 633.825623 L 26.577238 627.785339 L 3.3e-05 592.751709 L 2.73832 575.27533 L 26.577238 559.248352 L 60.724873 562.228149 L 136.187973 567.382629 L 249.422867 575.194763 L 331.570496 580.026978 L 453.261841 592.671082 L 472.590637 592.671082 L 475.328857 584.859009 L 468.724915 580.026978 L 463.570557 575.194763 L 346.389313 495.785217 L 219.543671 411.865906 L 153.100723 363.543762 L 117.181267 339.060425 L 99.060455 316.107361 L 91.248367 266.01355 L 123.865784 230.093994 L 167.677887 233.073853 L 178.872513 236.053772 L 223.248367 270.201477 L 318.040283 343.570496 L 441.825592 434.738342 L 459.946411 449.798706 L 467.194672 444.64447 L 468.080597 441.020203 L 459.946411 427.409485 L 392.617493 305.718323 L 320.778564 181.932983 L 288.80542 130.630859 L 280.348999 99.865845 C 277.369171 87.221436 275.194641 76.590698 275.194641 63.624268 L 312.322174 13.20813 L 332.8591 6.604126 L 382.389313 13.20813 L 403.248352 31.328979 L 434.013519 101.71814 L 483.865753 212.537048 L 561.181274 363.221497 L 583.812134 407.919434 L 595.892639 449.315491 L 600.40271 461.959839 L 608.214783 461.959839 L 608.214783 454.711609 L 614.577271 369.825623 L 626.335632 265.61084 L 637.771851 131.516846 L 641.718201 93.745117 L 660.402832 48.483276 L 697.530334 24.000122 L 726.52356 37.852417 L 750.362549 72 L 747.060486 94.067139 L 732.886047 186.201416 L 705.100708 330.52356 L 686.979919 427.167847 L 697.530334 427.167847 L 709.61084 415.087341 L 758.496704 350.174561 L 840.644348 247.490051 L 876.885925 206.738342 L 919.167847 161.71814 L 946.308838 140.29541 L 997.61084 140.29541 L 1035.38269 196.429626 L 1018.469849 254.416199 L 965.637634 321.422852 L 921.825562 378.201538 L 859.006714 462.765259 L 819.785278 530.41626 L 823.409424 535.812073 L 832.75177 534.92627 L 974.657776 504.724915 L 1051.328979 490.872559 L 1142.818848 475.167786 L 1184.214844 494.496582 L 1188.724854 514.147644 L 1172.456421 554.335693 L 1074.604126 578.496765 L 959.838989 601.449829 L 788.939636 641.879272 L 786.845764 643.409485 L 789.261841 646.389343 L 866.255127 653.637634 L 899.194702 655.409424 L 979.812134 655.409424 L 1129.932861 666.604187 L 1169.154419 692.537109 L 1192.671265 724.268677 L 1188.724854 748.429688 L 1128.322144 779.194641 L 1046.818848 759.865845 L 856.590759 714.604126 L 791.355774 698.335754 L 782.335693 698.335754 L 782.335693 703.731567 L 836.69812 756.885986 L 936.322205 846.845581 L 1061.073975 962.81897 L 1067.436279 991.490112 L 1051.409424 1014.120911 L 1034.496704 1011.704712 L 924.885986 929.234924 L 882.604126 892.107544 L 786.845764 811.48999 L 780.483276 811.48999 L 780.483276 819.946289 L 802.550415 852.241699 L 919.087341 1027.409424 L 925.127625 1081.127686 L 916.671204 1098.604126 L 886.469849 1109.154419 L 853.288696 1103.114136 L 785.073914 1007.355835 L 714.684631 899.516785 L 657.906067 802.872498 L 650.979858 806.81897 L 617.476624 1167.704834 L 601.771851 1186.147705 L 565.530212 1200 L 535.328857 1177.046997 L 519.302124 1139.919556 L 535.328857 1066.550537 L 554.657776 970.792053 L 570.362488 894.68457 L 584.536926 800.134277 L 592.993347 768.724976 L 592.429626 766.630859 L 585.503479 767.516968 L 514.22821 865.369263 L 405.825531 1011.865906 L 320.053711 1103.677979 L 299.516815 1111.812256 L 263.919525 1093.369263 L 267.221497 1060.429688 L 287.114136 1031.114136 L 405.825531 880.107361 L 477.422913 786.52356 L 523.651062 732.483276 L 523.328918 724.671265 L 520.590698 724.671265 L 205.288605 929.395935 L 149.154434 936.644409 L 124.993355 914.01355 L 127.973183 876.885986 L 139.409409 864.80542 L 234.201385 799.570435 L 233.879227 799.8927 Z"/>`;
    domNode.appendChild(iconSvg);

    const widget: editor.IContentWidget = {
      getId: () => 'send-to-claude-widget',
      getDomNode: () => domNode,
      getPosition: () => ({
        position: { lineNumber: selection.endLine, column: selection.endColumn },
        preference: [monaco.editor.ContentWidgetPositionPreference.ABOVE],
      }),
    };

    editor.addContentWidget(widget);
    widgetRef.current = widget;

    return () => {
      if (widgetRef.current) {
        editor.removeContentWidget(widgetRef.current);
        widgetRef.current = null;
      }
    };
  }, [selection, handleSendToClaude, onSendToClaude]);

  // Clear selection when file changes
  useEffect(() => {
    setSelection(null);
  }, [filePath]);

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
                onMount={(editor, monaco) => {
                  monacoRef.current = monaco;
                  const modifiedEditor = editor.getModifiedEditor();
                  editorRef.current = modifiedEditor;

                  // Listen for selection changes on the modified editor
                  modifiedEditor.onDidChangeCursorSelection((e) => {
                    const sel = e.selection;
                    if (sel.isEmpty()) {
                      setSelection(null);
                    } else {
                      setSelection({
                        startLine: sel.startLineNumber,
                        endLine: sel.endLineNumber,
                        endColumn: sel.endColumn,
                      });
                    }
                  });

                  // Scroll to first diff line after a short delay to ensure diff is computed
                  setTimeout(() => {
                    const lineChanges = editor.getLineChanges();
                    if (lineChanges && lineChanges.length > 0) {
                      const firstChange = lineChanges[0];
                      // Use modifiedStartLineNumber for the right (modified) side
                      const targetLine = firstChange.modifiedStartLineNumber || firstChange.originalStartLineNumber || 1;
                      modifiedEditor.revealLineInCenter(targetLine);
                    }
                  }, 100);
                }}
                options={{
                  readOnly: true,
                  renderSideBySide: true,
                  minimap: { enabled: false },
                  scrollBeyondLastLine: false,
                  fontSize,
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
