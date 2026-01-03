/**
 * UnifiedDiffEditor - Prototype for unified diff with inline comments
 *
 * Key insight: By making deleted lines part of the document, we can use
 * ONE comment mechanism for all lines (no DOM injection hack).
 *
 * Document structure:
 * - Each line has metadata: type (unchanged/deleted/added), line numbers
 * - Deleted lines are real document lines with decorations
 * - Comments attach to document positions, not DOM elements
 */
import { useEffect, useRef, useCallback, useState } from 'react';
import { EditorView, Decoration, DecorationSet, WidgetType, gutter, GutterMarker } from '@codemirror/view';
import { EditorState, StateField, StateEffect, RangeSetBuilder } from '@codemirror/state';
import { oneDark } from '@codemirror/theme-one-dark';
import { highlightSpecialChars } from '@codemirror/view';
import { diffLines } from 'diff';

// ============================================================================
// Types
// ============================================================================

export interface DiffLine {
  content: string;
  type: 'unchanged' | 'added' | 'deleted';
  originalLine: number | null; // Line number in original file (null for added)
  modifiedLine: number | null; // Line number in modified file (null for deleted)
}

export interface InlineComment {
  id: string;
  docLine: number; // 1-indexed line in unified document
  content: string;
  resolved: boolean;
  author?: 'user' | 'agent';
}

export interface UnifiedDiffEditorProps {
  original: string;
  modified: string;
  comments: InlineComment[];
  editingCommentId: string | null;
  onAddComment: (docLine: number, content: string) => Promise<void>;
  onEditComment: (id: string, content: string) => Promise<void>;
  onStartEdit: (id: string) => void;
  onCancelEdit: () => void;
  onResolveComment: (id: string, resolved: boolean) => Promise<void>;
  onDeleteComment: (id: string) => Promise<void>;
}

// ============================================================================
// Diff Parsing
// ============================================================================

export function buildUnifiedDocument(original: string, modified: string): {
  content: string;
  lines: DiffLine[];
} {
  const changes = diffLines(original, modified);
  const lines: DiffLine[] = [];
  let originalLine = 1;
  let modifiedLine = 1;

  for (const change of changes) {
    const changeLines = change.value.split('\n');
    // diffLines includes trailing newline in the value, so last element is often empty
    if (changeLines[changeLines.length - 1] === '') {
      changeLines.pop();
    }

    for (const line of changeLines) {
      if (change.added) {
        lines.push({
          content: line,
          type: 'added',
          originalLine: null,
          modifiedLine: modifiedLine++,
        });
      } else if (change.removed) {
        lines.push({
          content: line,
          type: 'deleted',
          originalLine: originalLine++,
          modifiedLine: null,
        });
      } else {
        lines.push({
          content: line,
          type: 'unchanged',
          originalLine: originalLine++,
          modifiedLine: modifiedLine++,
        });
      }
    }
  }

  // Build document content (just the text, no markers)
  const content = lines.map((l) => l.content).join('\n');

  return { content, lines };
}

// ============================================================================
// CodeMirror Extensions - Line Metadata & Gutters
// ============================================================================

// Effect to update line metadata (for gutters)
const setLineMetadata = StateEffect.define<DiffLine[]>();

// StateField for line metadata - gutters read from this
const lineMetadataField = StateField.define<DiffLine[]>({
  create() {
    return [];
  },
  update(lines, tr) {
    for (const effect of tr.effects) {
      if (effect.is(setLineMetadata)) {
        return effect.value;
      }
    }
    return lines;
  },
});

// GutterMarker for line numbers
class LineNumberMarker extends GutterMarker {
  constructor(readonly lineNum: number | null, readonly lineType: 'unchanged' | 'added' | 'deleted') {
    super();
  }

  toDOM() {
    const el = document.createElement('span');
    el.className = `cm-lineNumber ${this.lineNum === null ? 'cm-lineNumber-blank' : ''}`;
    el.textContent = this.lineNum === null ? '-' : String(this.lineNum);
    return el;
  }
}

// Original file line number gutter
const originalLineGutter = gutter({
  class: 'cm-original-gutter',
  lineMarker: (view, line) => {
    const lines = view.state.field(lineMetadataField);
    const docLine = view.state.doc.lineAt(line.from).number;
    const diffLine = lines[docLine - 1];
    if (!diffLine) return null;
    return new LineNumberMarker(diffLine.originalLine, diffLine.type);
  },
});

// Modified file line number gutter
const modifiedLineGutter = gutter({
  class: 'cm-modified-gutter',
  lineMarker: (view, line) => {
    const lines = view.state.field(lineMetadataField);
    const docLine = view.state.doc.lineAt(line.from).number;
    const diffLine = lines[docLine - 1];
    if (!diffLine) return null;
    return new LineNumberMarker(diffLine.modifiedLine, diffLine.type);
  },
});

// ============================================================================
// CodeMirror Extensions - Decorations
// ============================================================================

// Effect to update line decorations
const setLineDecorations = StateEffect.define<DecorationSet>();

// StateField for line type decorations (added/deleted/unchanged)
const lineDecorationsField = StateField.define<DecorationSet>({
  create() {
    return Decoration.none;
  },
  update(decorations, tr) {
    decorations = decorations.map(tr.changes);
    for (const effect of tr.effects) {
      if (effect.is(setLineDecorations)) {
        return effect.value;
      }
    }
    return decorations;
  },
  provide: (field) => EditorView.decorations.from(field),
});

// Effect to update comment widgets
const setCommentWidgets = StateEffect.define<DecorationSet>();

// StateField for inline comment widgets
const commentWidgetsField = StateField.define<DecorationSet>({
  create() {
    return Decoration.none;
  },
  update(decorations, tr) {
    decorations = decorations.map(tr.changes);
    for (const effect of tr.effects) {
      if (effect.is(setCommentWidgets)) {
        return effect.value;
      }
    }
    return decorations;
  },
  provide: (field) => EditorView.decorations.from(field),
});

// Line decorations for diff highlighting
const deletedLineDecoration = Decoration.line({ class: 'cm-deleted-line' });
const addedLineDecoration = Decoration.line({ class: 'cm-added-line' });

function buildLineDecorations(view: EditorView, lines: DiffLine[]): DecorationSet {
  const builder = new RangeSetBuilder<Decoration>();
  const doc = view.state.doc;

  for (let i = 0; i < lines.length && i < doc.lines; i++) {
    const line = doc.line(i + 1);
    const diffLine = lines[i];

    if (diffLine.type === 'deleted') {
      builder.add(line.from, line.from, deletedLineDecoration);
    } else if (diffLine.type === 'added') {
      builder.add(line.from, line.from, addedLineDecoration);
    }
  }

  return builder.finish();
}

// ============================================================================
// Comment Widget
// ============================================================================

interface CommentWidgetConfig {
  comment: InlineComment;
  isEditing: boolean;
  onEdit: (id: string) => void;
  onSaveEdit: (id: string, content: string) => void;
  onCancelEdit: () => void;
  onResolve: (id: string, resolved: boolean) => void;
  onDelete: (id: string) => void;
}

class CommentWidget extends WidgetType {
  constructor(readonly config: CommentWidgetConfig) {
    super();
  }

  toDOM() {
    const { comment, isEditing, onEdit, onSaveEdit, onCancelEdit, onResolve, onDelete } = this.config;

    const wrapper = document.createElement('div');
    wrapper.className = `unified-comment ${comment.resolved ? 'resolved' : ''}`;

    if (isEditing) {
      // Edit mode
      wrapper.innerHTML = `<textarea class="unified-comment-textarea">${escapeHtml(comment.content)}</textarea><div class="unified-comment-form-actions"><button class="cancel-btn">Cancel</button><button class="save-btn">Save</button></div>`;

      const textarea = wrapper.querySelector('textarea')!;
      setTimeout(() => {
        textarea.focus();
        textarea.setSelectionRange(textarea.value.length, textarea.value.length);
      }, 0);

      textarea.addEventListener('keydown', (e) => {
        if (e.key === 'Escape') {
          e.preventDefault();
          onCancelEdit();
        } else if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
          e.preventDefault();
          const content = textarea.value.trim();
          if (content) {
            onSaveEdit(comment.id, content);
          }
        }
      });

      wrapper.querySelector('.cancel-btn')?.addEventListener('click', (e) => {
        e.stopPropagation();
        onCancelEdit();
      });

      wrapper.querySelector('.save-btn')?.addEventListener('click', (e) => {
        e.stopPropagation();
        const content = textarea.value.trim();
        if (content) {
          onSaveEdit(comment.id, content);
        }
      });
    } else {
      // Display mode
      wrapper.innerHTML = `<div class="unified-comment-header"><span class="unified-comment-author">${comment.author === 'agent' ? 'Claude' : 'You'}</span><div class="unified-comment-actions"><button class="edit-btn">Edit</button><button class="resolve-btn">${comment.resolved ? 'Unresolve' : 'Resolve'}</button><button class="delete-btn">Delete</button></div></div><div class="unified-comment-content">${escapeHtml(comment.content)}</div>`;

      wrapper.querySelector('.edit-btn')?.addEventListener('click', (e) => {
        e.stopPropagation();
        onEdit(comment.id);
      });

      wrapper.querySelector('.resolve-btn')?.addEventListener('click', (e) => {
        e.stopPropagation();
        onResolve(comment.id, !comment.resolved);
      });

      wrapper.querySelector('.delete-btn')?.addEventListener('click', (e) => {
        e.stopPropagation();
        onDelete(comment.id);
      });
    }

    return wrapper;
  }

  ignoreEvent(event: Event) {
    // Let the widget handle mouse events
    return event.type.startsWith('mouse');
  }
}

// ============================================================================
// New Comment Form Widget
// ============================================================================

interface NewCommentFormConfig {
  docLine: number;
  onSave: (docLine: number, content: string) => void;
  onCancel: (docLine: number) => void;
}

class NewCommentFormWidget extends WidgetType {
  constructor(readonly config: NewCommentFormConfig) {
    super();
  }

  toDOM() {
    const { docLine, onSave, onCancel } = this.config;

    const wrapper = document.createElement('div');
    wrapper.className = 'unified-comment-form';
    wrapper.innerHTML = `<textarea class="unified-comment-textarea" rows="2" placeholder="Add a comment..."></textarea><div class="unified-comment-form-actions"><button class="cancel-btn">Cancel</button><button class="save-btn">Save</button></div>`;

    const textarea = wrapper.querySelector('textarea')!;
    setTimeout(() => textarea.focus(), 0);

    // Keyboard shortcuts
    textarea.addEventListener('keydown', (e) => {
      if (e.key === 'Escape') {
        e.preventDefault();
        onCancel(docLine);
      } else if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
        e.preventDefault();
        const content = textarea.value.trim();
        if (content) {
          onSave(docLine, content);
        }
      }
    });

    wrapper.querySelector('.cancel-btn')?.addEventListener('click', (e) => {
      e.stopPropagation();
      onCancel(docLine);
    });

    wrapper.querySelector('.save-btn')?.addEventListener('click', (e) => {
      e.stopPropagation();
      const content = textarea.value.trim();
      if (content) {
        onSave(docLine, content);
      }
    });

    return wrapper;
  }

  ignoreEvent(event: Event) {
    return event.type.startsWith('mouse');
  }
}

// ============================================================================
// Utilities
// ============================================================================

function escapeHtml(text: string): string {
  const div = document.createElement('div');
  div.textContent = text;
  return div.innerHTML;
}

// ============================================================================
// Component
// ============================================================================

export function UnifiedDiffEditor({
  original,
  modified,
  comments,
  editingCommentId,
  onAddComment,
  onEditComment,
  onStartEdit,
  onCancelEdit,
  onResolveComment,
  onDeleteComment,
}: UnifiedDiffEditorProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const editorViewRef = useRef<EditorView | null>(null);
  const linesRef = useRef<DiffLine[]>([]);

  // Track which lines have open "new comment" forms
  const [newCommentLines, setNewCommentLines] = useState<Set<number>>(new Set());

  // Build unified document
  const { content, lines } = buildUnifiedDocument(original, modified);
  linesRef.current = lines;

  // Handlers for comment forms
  const handleSaveComment = useCallback(
    async (docLine: number, commentContent: string) => {
      await onAddComment(docLine, commentContent);
      setNewCommentLines((prev) => {
        const next = new Set(prev);
        next.delete(docLine);
        return next;
      });
    },
    [onAddComment]
  );

  const handleCancelComment = useCallback((docLine: number) => {
    setNewCommentLines((prev) => {
      const next = new Set(prev);
      next.delete(docLine);
      return next;
    });
  }, []);

  // Create editor
  useEffect(() => {
    if (!containerRef.current) return;

    const theme = EditorView.theme(
      {
        '&': {
          height: '100%',
          backgroundColor: '#282c34',
          overflow: 'hidden',
        },
        '.cm-scroller': {
          fontFamily: "'JetBrains Mono', 'Fira Code', Consolas, monospace",
          fontSize: '13px',
          lineHeight: '1.6',
          overflow: 'auto !important',
        },
        '.cm-content': {
          minWidth: '0',
        },
        '.cm-gutters': {
          backgroundColor: '#21252b',
          borderRight: '1px solid #3e4451',
        },
        // Custom line number gutters (GitHub-style compact)
        '.cm-original-gutter, .cm-modified-gutter': {
          minWidth: '32px',
          textAlign: 'right',
          padding: '0 4px',
          color: '#6e7681',
          fontSize: '12px',
          userSelect: 'none',
        },
        '.cm-original-gutter .cm-lineNumber, .cm-modified-gutter .cm-lineNumber': {
          padding: '0 4px',
        },
        '.cm-lineNumber-blank': {
          color: '#3e4451',
        },
        '.cm-deleted-line': {
          backgroundColor: '#3c1f1e !important',
        },
        '.cm-added-line': {
          backgroundColor: '#1e3a1e !important',
        },
        // Saved comments
        '.unified-comment': {
          background: '#2d3748',
          borderLeft: '3px solid #3b82f6',
          margin: '2px 0',
          padding: '6px 10px',
          borderRadius: '0 4px 4px 0',
          fontFamily: '-apple-system, BlinkMacSystemFont, sans-serif',
        },
        '.unified-comment.resolved': {
          borderLeftColor: '#22c55e',
          background: '#1e3a2f',
        },
        '.unified-comment-header': {
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          marginBottom: '4px',
        },
        '.unified-comment-author': {
          fontSize: '12px',
          fontWeight: '600',
          padding: '2px 8px',
          borderRadius: '4px',
          color: '#fff',
          background: '#2563eb',
        },
        '.unified-comment-content': {
          color: '#e5e7eb',
          fontSize: '13px',
          lineHeight: '1.4',
          whiteSpace: 'pre-wrap',
        },
        '.unified-comment-actions': {
          display: 'flex',
          gap: '6px',
        },
        '.unified-comment-actions button': {
          background: 'transparent',
          border: '1px solid #4b5563',
          color: '#9ca3af',
          fontSize: '11px',
          padding: '2px 8px',
          borderRadius: '3px',
          cursor: 'pointer',
        },
        '.unified-comment-actions button:hover': {
          background: '#374151',
          color: '#e5e7eb',
        },
        '.unified-comment-actions .resolve-btn': {
          borderColor: '#22c55e',
          color: '#22c55e',
        },
        '.unified-comment-actions .resolve-btn:hover': {
          background: '#22c55e',
          color: '#fff',
        },
        '.unified-comment-actions .delete-btn': {
          borderColor: '#ef4444',
          color: '#ef4444',
        },
        '.unified-comment-actions .delete-btn:hover': {
          background: '#ef4444',
          color: '#fff',
        },
        // New comment form
        '.unified-comment-form': {
          background: '#312e81',
          borderLeft: '3px solid #6366f1',
          margin: '2px 0',
          padding: '6px 10px',
          borderRadius: '0 4px 4px 0',
          fontFamily: '-apple-system, BlinkMacSystemFont, sans-serif',
        },
        '.unified-comment-textarea': {
          width: '100%',
          minHeight: '48px',
          padding: '6px 8px',
          background: '#1f2937',
          border: '1px solid #4b5563',
          borderRadius: '4px',
          color: '#e5e7eb',
          fontFamily: 'inherit',
          fontSize: '13px',
          lineHeight: '1.4',
          resize: 'vertical',
          boxSizing: 'border-box',
        },
        '.unified-comment-textarea:focus': {
          outline: 'none',
          borderColor: '#6366f1',
        },
        '.unified-comment-textarea::placeholder': {
          color: '#6b7280',
        },
        '.unified-comment-form-actions': {
          display: 'flex',
          justifyContent: 'flex-end',
          gap: '6px',
          marginTop: '6px',
        },
        '.unified-comment-form-actions button': {
          background: 'transparent',
          border: '1px solid #4b5563',
          color: '#9ca3af',
          fontSize: '11px',
          padding: '2px 8px',
          borderRadius: '3px',
          cursor: 'pointer',
        },
        '.unified-comment-form-actions button:hover': {
          background: '#374151',
          color: '#e5e7eb',
        },
        '.unified-comment-form-actions .save-btn': {
          background: '#3b82f6',
          borderColor: '#3b82f6',
          color: '#fff',
        },
        '.unified-comment-form-actions .save-btn:hover': {
          background: '#2563eb',
          borderColor: '#2563eb',
        },
      },
      { dark: true }
    );

    // Click handler to open comment form
    const clickHandler = EditorView.domEventHandlers({
      click: (event, view) => {
        const target = event.target as HTMLElement;
        if (target.closest('.unified-comment') || target.closest('.unified-comment-form')) {
          return false;
        }

        const pos = view.posAtCoords({ x: event.clientX, y: event.clientY });
        if (pos !== null) {
          const docLine = view.state.doc.lineAt(pos).number;
          setNewCommentLines((prev) => {
            if (prev.has(docLine)) return prev;
            const next = new Set(prev);
            next.add(docLine);
            return next;
          });
        }
        return false;
      },
    });

    const view = new EditorView({
      state: EditorState.create({
        doc: content,
        extensions: [
          EditorState.readOnly.of(true),
          EditorView.lineWrapping,
          lineMetadataField,
          originalLineGutter,
          modifiedLineGutter,
          highlightSpecialChars(),
          oneDark,
          theme,
          lineDecorationsField,
          commentWidgetsField,
          clickHandler,
        ],
      }),
      parent: containerRef.current,
    });

    editorViewRef.current = view;

    // Apply initial line metadata and decorations
    const lineDecos = buildLineDecorations(view, lines);
    view.dispatch({
      effects: [
        setLineMetadata.of(lines),
        setLineDecorations.of(lineDecos),
      ],
    });

    return () => {
      view.destroy();
      editorViewRef.current = null;
    };
  }, [content, lines]);

  // Update comment widgets when comments or newCommentLines change
  useEffect(() => {
    const view = editorViewRef.current;
    if (!view) return;

    const widgets: { pos: number; widget: WidgetType }[] = [];

    // Add saved comments
    for (const comment of comments) {
      if (comment.docLine > 0 && comment.docLine <= view.state.doc.lines) {
        const line = view.state.doc.line(comment.docLine);
        widgets.push({
          pos: line.to,
          widget: new CommentWidget({
            comment,
            isEditing: editingCommentId === comment.id,
            onEdit: onStartEdit,
            onSaveEdit: onEditComment,
            onCancelEdit: onCancelEdit,
            onResolve: onResolveComment,
            onDelete: onDeleteComment,
          }),
        });
      }
    }

    // Add new comment forms
    for (const docLine of newCommentLines) {
      if (docLine > 0 && docLine <= view.state.doc.lines) {
        const line = view.state.doc.line(docLine);
        widgets.push({
          pos: line.to,
          widget: new NewCommentFormWidget({
            docLine,
            onSave: handleSaveComment,
            onCancel: handleCancelComment,
          }),
        });
      }
    }

    // Sort by position and build decoration set
    widgets.sort((a, b) => a.pos - b.pos);
    const decorations = Decoration.set(
      widgets.map(({ pos, widget }) => Decoration.widget({ widget, block: true, side: 1 }).range(pos))
    );

    view.dispatch({ effects: setCommentWidgets.of(decorations) });
  }, [comments, newCommentLines, editingCommentId, handleSaveComment, handleCancelComment, onStartEdit, onEditComment, onCancelEdit, onResolveComment, onDeleteComment]);

  return (
    <div
      ref={containerRef}
      className="unified-diff-editor"
      style={{ height: '100%', width: '100%' }}
      data-testid="unified-diff-editor"
    />
  );
}

export default UnifiedDiffEditor;
