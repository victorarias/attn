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
import { useEffect, useRef, useCallback, useState, useMemo } from 'react';
import { EditorView, Decoration, DecorationSet, WidgetType, gutter, GutterMarker } from '@codemirror/view';
import { EditorState, StateField, StateEffect, RangeSetBuilder, Extension, Range } from '@codemirror/state';
import { oneDark } from '@codemirror/theme-one-dark';
import { defaultHighlightStyle, syntaxHighlighting } from '@codemirror/language';
import { highlightSpecialChars } from '@codemirror/view';
import { javascript } from '@codemirror/lang-javascript';
import { python } from '@codemirror/lang-python';
import { markdown } from '@codemirror/lang-markdown';
import { json } from '@codemirror/lang-json';
import { css } from '@codemirror/lang-css';
import { html } from '@codemirror/lang-html';
import { go } from '@codemirror/lang-go';
import { rust } from '@codemirror/lang-rust';
import { sql } from '@codemirror/lang-sql';
import { java } from '@codemirror/lang-java';
import { yaml } from '@codemirror/lang-yaml';
import { diffLines } from 'diff';
import { marked } from 'marked';
import { ClaudeIcon } from './icons/ClaudeIcon';
import type { ResolvedTheme } from '../hooks/useTheme';

// ============================================================================
// Types
// ============================================================================

export interface DiffLine {
  content: string;
  type: 'unchanged' | 'added' | 'deleted';
  originalLine: number | null; // Line number in original file (null for added)
  modifiedLine: number | null; // Line number in modified file (null for deleted)
}

export interface CommentAnchor {
  side: 'original' | 'modified'; // Which side of the diff the comment is on
  line: number; // Line number on that side
  anchorContent?: string; // Content of the line when comment was created (for staleness detection)
  anchorHash?: string; // Hash of anchor content for quick comparison
}

export interface InlineComment {
  id: string;
  docLine: number; // 1-indexed line in unified document (runtime only, recalculated from anchor)
  content: string;
  resolved: boolean;
  resolvedBy?: 'user' | 'agent'; // Who resolved the comment
  wontFix?: boolean;
  wontFixBy?: 'user' | 'agent'; // Who marked won't fix
  author?: 'user' | 'agent';
  anchor?: CommentAnchor; // For persistence and staleness detection
  isOutdated?: boolean; // Line content changed since comment was created
  isOrphaned?: boolean; // Line no longer exists in the diff
}

export interface Hunk {
  startDocLine: number; // 1-indexed start in unified doc
  endDocLine: number; // 1-indexed end (inclusive)
  originalStart: number; // Starting line in original file
  originalCount: number; // Number of lines from original
  modifiedStart: number; // Starting line in modified file
  modifiedCount: number; // Number of lines in modified
}

export interface CollapsedRegion {
  startDocLine: number; // 1-indexed start
  endDocLine: number; // 1-indexed end (inclusive)
  lineCount: number;
}

export interface UnifiedDiffEditorProps {
  original: string;
  modified: string;
  comments: InlineComment[];
  editingCommentId: string | null;
  resolvedTheme?: ResolvedTheme;
  fontSize?: number;
  language?: string;
  contextLines?: number; // Lines of context around changes (default 3, 0 for full diff)
  scrollToLine?: number; // Line number to scroll to (1-indexed, in modified file)
  filePath?: string; // For send to Claude Code reference
  onAddComment: (docLine: number, content: string, anchor: CommentAnchor) => Promise<void>;
  onEditComment: (id: string, content: string) => Promise<void>;
  onStartEdit: (id: string) => void;
  onCancelEdit: () => void;
  onResolveComment: (id: string, resolved: boolean) => Promise<void>;
  onWontFixComment: (id: string, wontFix: boolean) => Promise<void>;
  onDeleteComment: (id: string) => Promise<void>;
  onSendToClaude?: (reference: string) => void;
}

type DiffEditorTheme = {
  editorBg: string;
  guttersBg: string;
  guttersBorder: string;
  gutterText: string;
  gutterBlank: string;
  deletedLineBg: string;
  addedLineBg: string;
  commentBg: string;
  commentBorder: string;
  commentResolvedBg: string;
  commentWontFixBg: string;
  authorText: string;
  authorBg: string;
  resolvedText: string;
  resolvedBadgeBg: string;
  wontFixText: string;
  wontFixBadgeBg: string;
  commentContent: string;
  commentCodeBg: string;
  commentLink: string;
  commentBlockquoteBorder: string;
  commentBlockquoteText: string;
  buttonBorder: string;
  buttonText: string;
  buttonHoverBg: string;
  buttonHoverText: string;
  resolveButton: string;
  wontFixButton: string;
  deleteButton: string;
  sendButton: string;
  newCommentBg: string;
  newCommentBorder: string;
  textareaBg: string;
  textareaBorder: string;
  textareaText: string;
  textareaFocus: string;
  textareaPlaceholder: string;
  saveButtonBg: string;
  saveButtonHoverBg: string;
  collapsedBg: string;
  collapsedBorder: string;
  collapsedText: string;
  collapsedHoverBg: string;
  collapsedHoverText: string;
  popupBg: string;
  popupBorder: string;
  popupShadow: string;
  popupSendIcon: string;
  popupCommentIcon: string;
};

const DARK_THEME: DiffEditorTheme = {
  editorBg: '#282c34',
  guttersBg: '#21252b',
  guttersBorder: '#3e4451',
  gutterText: '#6e7681',
  gutterBlank: '#3e4451',
  deletedLineBg: '#3c1f1e',
  addedLineBg: '#1e3a1e',
  commentBg: '#2d3748',
  commentBorder: '#3b82f6',
  commentResolvedBg: '#1e3a2f',
  commentWontFixBg: '#3a2f1e',
  authorText: '#ffffff',
  authorBg: '#2563eb',
  resolvedText: '#22c55e',
  resolvedBadgeBg: 'rgba(34, 197, 94, 0.15)',
  wontFixText: '#d97706',
  wontFixBadgeBg: 'rgba(217, 119, 6, 0.15)',
  commentContent: '#e5e7eb',
  commentCodeBg: '#1f2937',
  commentLink: '#60a5fa',
  commentBlockquoteBorder: '#4b5563',
  commentBlockquoteText: '#9ca3af',
  buttonBorder: '#4b5563',
  buttonText: '#9ca3af',
  buttonHoverBg: '#374151',
  buttonHoverText: '#e5e7eb',
  resolveButton: '#22c55e',
  wontFixButton: '#d97706',
  deleteButton: '#ef4444',
  sendButton: '#d97706',
  newCommentBg: '#312e81',
  newCommentBorder: '#6366f1',
  textareaBg: '#1f2937',
  textareaBorder: '#4b5563',
  textareaText: '#e5e7eb',
  textareaFocus: '#6366f1',
  textareaPlaceholder: '#6b7280',
  saveButtonBg: '#3b82f6',
  saveButtonHoverBg: '#2563eb',
  collapsedBg: '#1e293b',
  collapsedBorder: '#334155',
  collapsedText: '#94a3b8',
  collapsedHoverBg: '#334155',
  collapsedHoverText: '#e2e8f0',
  popupBg: '#2a2a2d',
  popupBorder: '#3a3a3d',
  popupShadow: '0 4px 12px rgba(0, 0, 0, 0.4)',
  popupSendIcon: '#d4a27f',
  popupCommentIcon: '#60a5fa',
};

const LIGHT_THEME: DiffEditorTheme = {
  editorBg: '#ffffff',
  guttersBg: '#f6f8fa',
  guttersBorder: '#d0d7de',
  gutterText: '#656d76',
  gutterBlank: '#d0d7de',
  deletedLineBg: '#ffebe9',
  addedLineBg: '#dafbe1',
  commentBg: '#f6f8fa',
  commentBorder: '#0969da',
  commentResolvedBg: '#dafbe1',
  commentWontFixBg: '#fff8c5',
  authorText: '#ffffff',
  authorBg: '#0969da',
  resolvedText: '#1a7f37',
  resolvedBadgeBg: '#dcffe4',
  wontFixText: '#9a6700',
  wontFixBadgeBg: '#fff8c5',
  commentContent: '#24292f',
  commentCodeBg: '#eaedf1',
  commentLink: '#0969da',
  commentBlockquoteBorder: '#d0d7de',
  commentBlockquoteText: '#57606a',
  buttonBorder: '#d0d7de',
  buttonText: '#57606a',
  buttonHoverBg: '#eaeef2',
  buttonHoverText: '#24292f',
  resolveButton: '#1a7f37',
  wontFixButton: '#9a6700',
  deleteButton: '#cf222e',
  sendButton: '#9a6700',
  newCommentBg: '#eef2ff',
  newCommentBorder: '#6366f1',
  textareaBg: '#ffffff',
  textareaBorder: '#d0d7de',
  textareaText: '#24292f',
  textareaFocus: '#0969da',
  textareaPlaceholder: '#8c959f',
  saveButtonBg: '#0969da',
  saveButtonHoverBg: '#0550ae',
  collapsedBg: '#f6f8fa',
  collapsedBorder: '#d0d7de',
  collapsedText: '#57606a',
  collapsedHoverBg: '#eaeef2',
  collapsedHoverText: '#24292f',
  popupBg: '#ffffff',
  popupBorder: '#d0d7de',
  popupShadow: '0 4px 12px rgba(31, 35, 40, 0.15)',
  popupSendIcon: '#9a6700',
  popupCommentIcon: '#0969da',
};

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
// Hunk Calculation Utilities
// ============================================================================

/**
 * Calculate hunks and collapsed regions for a diff.
 * A hunk is a group of changes with surrounding context lines.
 * Collapsed regions are large unchanged areas between hunks.
 */
export function calculateHunks(
  lines: DiffLine[],
  contextLines: number
): { hunks: Hunk[]; collapsedRegions: CollapsedRegion[] } {
  if (contextLines <= 0 || lines.length === 0) {
    // Show full diff, no collapsing
    return { hunks: [], collapsedRegions: [] };
  }

  // Find all changed line indices (0-indexed)
  const changedIndices: number[] = [];
  for (let i = 0; i < lines.length; i++) {
    if (lines[i].type !== 'unchanged') {
      changedIndices.push(i);
    }
  }

  if (changedIndices.length === 0) {
    // No changes - entire file is one collapsed region
    if (lines.length > contextLines * 2) {
      return {
        hunks: [],
        collapsedRegions: [
          {
            startDocLine: 1,
            endDocLine: lines.length,
            lineCount: lines.length,
          },
        ],
      };
    }
    return { hunks: [], collapsedRegions: [] };
  }

  // Group changes into hunks (changes that are within 2*contextLines of each other)
  const hunkRanges: { start: number; end: number }[] = [];
  let currentStart = changedIndices[0];
  let currentEnd = changedIndices[0];

  for (let i = 1; i < changedIndices.length; i++) {
    const idx = changedIndices[i];
    // If this change is within range, extend the hunk
    if (idx <= currentEnd + contextLines * 2 + 1) {
      currentEnd = idx;
    } else {
      // Start a new hunk
      hunkRanges.push({ start: currentStart, end: currentEnd });
      currentStart = idx;
      currentEnd = idx;
    }
  }
  hunkRanges.push({ start: currentStart, end: currentEnd });

  // Build hunks with context
  const hunks: Hunk[] = [];
  const collapsedRegions: CollapsedRegion[] = [];

  let lastHunkEnd = 0; // Track end of last visible region

  for (const range of hunkRanges) {
    const hunkStart = Math.max(0, range.start - contextLines);
    const hunkEnd = Math.min(lines.length - 1, range.end + contextLines);

    // Check for collapsed region before this hunk
    if (hunkStart > lastHunkEnd + 1) {
      const collapsedStart = lastHunkEnd + 1;
      const collapsedEnd = hunkStart - 1;
      const lineCount = collapsedEnd - collapsedStart + 1;

      if (lineCount > 0) {
        collapsedRegions.push({
          startDocLine: collapsedStart + 1, // Convert to 1-indexed
          endDocLine: collapsedEnd + 1,
          lineCount,
        });
      }
    }

    // Calculate original/modified line ranges for this hunk
    let originalStart = 0;
    let originalCount = 0;
    let modifiedStart = 0;
    let modifiedCount = 0;

    for (let i = hunkStart; i <= hunkEnd; i++) {
      const line = lines[i];
      if (line.originalLine !== null) {
        if (originalStart === 0) originalStart = line.originalLine;
        originalCount++;
      }
      if (line.modifiedLine !== null) {
        if (modifiedStart === 0) modifiedStart = line.modifiedLine;
        modifiedCount++;
      }
    }

    hunks.push({
      startDocLine: hunkStart + 1, // Convert to 1-indexed
      endDocLine: hunkEnd + 1,
      originalStart,
      originalCount,
      modifiedStart,
      modifiedCount,
    });

    lastHunkEnd = hunkEnd;
  }

  // Check for collapsed region after last hunk
  if (lastHunkEnd < lines.length - 1) {
    const collapsedStart = lastHunkEnd + 1;
    const collapsedEnd = lines.length - 1;
    const lineCount = collapsedEnd - collapsedStart + 1;

    if (lineCount > 0) {
      collapsedRegions.push({
        startDocLine: collapsedStart + 1,
        endDocLine: collapsedEnd + 1,
        lineCount,
      });
    }
  }

  return { hunks, collapsedRegions };
}

/**
 * Get visible line indices for hunks mode.
 * Returns a Set of 0-indexed line numbers that should be visible.
 */
export function getVisibleLines(
  lines: DiffLine[],
  contextLines: number,
  expandedRegions: Set<number> // Set of collapsed region start lines that are expanded
): Set<number> {
  if (contextLines <= 0) {
    // Show all lines
    const allLines = new Set<number>();
    for (let i = 0; i < lines.length; i++) {
      allLines.add(i);
    }
    return allLines;
  }

  const { collapsedRegions } = calculateHunks(lines, contextLines);
  const visible = new Set<number>();

  // Start with all lines visible
  for (let i = 0; i < lines.length; i++) {
    visible.add(i);
  }

  // Remove collapsed regions (unless expanded)
  for (const region of collapsedRegions) {
    if (!expandedRegions.has(region.startDocLine)) {
      for (let i = region.startDocLine - 1; i <= region.endDocLine - 1; i++) {
        visible.delete(i);
      }
    }
  }

  return visible;
}

// ============================================================================
// Line Number Mapping Utilities
// ============================================================================

/**
 * Simple hash function for anchor content (for quick staleness comparison)
 */
export function hashContent(content: string): string {
  let hash = 0;
  for (let i = 0; i < content.length; i++) {
    const char = content.charCodeAt(i);
    hash = (hash << 5) - hash + char;
    hash = hash & hash; // Convert to 32bit integer
  }
  return hash.toString(16);
}

/**
 * Create a comment anchor from a document line number.
 * Used when saving a new comment to persist its location.
 */
export function createAnchor(docLine: number, lines: DiffLine[]): CommentAnchor | null {
  const diffLine = lines[docLine - 1];
  if (!diffLine) return null;

  // Determine which side this line belongs to
  if (diffLine.type === 'deleted') {
    // Deleted lines only exist in original
    return {
      side: 'original',
      line: diffLine.originalLine!,
      anchorContent: diffLine.content,
      anchorHash: hashContent(diffLine.content),
    };
  } else {
    // Added or unchanged lines exist in modified
    return {
      side: 'modified',
      line: diffLine.modifiedLine!,
      anchorContent: diffLine.content,
      anchorHash: hashContent(diffLine.content),
    };
  }
}

/**
 * Find the document line for a persisted comment anchor.
 * Also detects if the comment is outdated or orphaned.
 */
export function resolveAnchor(
  anchor: CommentAnchor,
  lines: DiffLine[]
): { docLine: number; isOutdated: boolean } | { isOrphaned: true } {
  for (let i = 0; i < lines.length; i++) {
    const diffLine = lines[i];
    const lineNum =
      anchor.side === 'original' ? diffLine.originalLine : diffLine.modifiedLine;

    if (lineNum === anchor.line) {
      // Found the line - check if content changed
      const currentHash = hashContent(diffLine.content);
      const isOutdated = anchor.anchorHash !== currentHash;
      return { docLine: i + 1, isOutdated };
    }
  }

  // Line no longer exists in the diff
  return { isOrphaned: true };
}

/**
 * Get original and modified line numbers for a document line.
 * Useful for displaying line context in comment UI.
 */
export function getLineNumbers(
  docLine: number,
  lines: DiffLine[]
): { original: number | null; modified: number | null; type: DiffLine['type'] } | null {
  const diffLine = lines[docLine - 1];
  if (!diffLine) return null;
  return {
    original: diffLine.originalLine,
    modified: diffLine.modifiedLine,
    type: diffLine.type,
  };
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

// Effect to update collapsed region decorations
const setCollapsedDecorations = StateEffect.define<DecorationSet>();

// StateField for collapsed region widgets (replaces ranges of lines)
const collapsedDecorationsField = StateField.define<DecorationSet>({
  create() {
    return Decoration.none;
  },
  update(decorations, tr) {
    decorations = decorations.map(tr.changes);
    for (const effect of tr.effects) {
      if (effect.is(setCollapsedDecorations)) {
        return effect.value;
      }
    }
    return decorations;
  },
  provide: (field) => EditorView.decorations.from(field),
});

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
  onWontFix: (id: string, wontFix: boolean) => void;
  onDelete: (id: string) => void;
  onSendToClaude?: () => void;
}

class CommentWidget extends WidgetType {
  constructor(readonly config: CommentWidgetConfig) {
    super();
  }

  toDOM() {
    const { comment, isEditing, onEdit, onSaveEdit, onCancelEdit, onResolve, onWontFix, onDelete, onSendToClaude } = this.config;

    const wrapper = document.createElement('div');
    wrapper.className = `unified-comment ${comment.resolved ? 'resolved' : ''} ${comment.wontFix ? 'wont-fix' : ''}`;

    if (isEditing) {
      // Edit mode
      wrapper.innerHTML = `<textarea class="unified-comment-textarea">${escapeHtml(comment.content)}</textarea><div class="unified-comment-form-actions"><button class="cancel-btn">Cancel</button><button class="save-btn">Save</button></div>`;

      const textarea = wrapper.querySelector('textarea')!;
      setTimeout(() => {
        textarea.focus();
        textarea.setSelectionRange(textarea.value.length, textarea.value.length);
      }, 0);

      textarea.addEventListener('keydown', (e) => {
        // Stop all key events from bubbling to CodeMirror
        e.stopPropagation();
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
      const resolvedBadge = comment.resolved
        ? `<span class="unified-comment-resolved">Resolved by ${comment.resolvedBy === 'agent' ? 'Claude' : 'you'}</span>`
        : '';
      const wontFixBadge = comment.wontFix
        ? `<span class="unified-comment-wontfix">Won't Fix by ${comment.wontFixBy === 'agent' ? 'Claude' : 'you'}</span>`
        : '';
      const sendToClaudeBtn = onSendToClaude
        ? `<button class="send-btn">Send to CC</button>`
        : '';
      const wontFixBtn = comment.wontFix
        ? `<button class="wontfix-btn">Undo Won't Fix</button>`
        : `<button class="wontfix-btn">Won't Fix</button>`;
      wrapper.innerHTML = `<div class="unified-comment-header"><div class="unified-comment-left"><span class="unified-comment-author">${comment.author === 'agent' ? 'Claude' : 'You'}</span>${resolvedBadge}${wontFixBadge}</div><div class="unified-comment-actions"><button class="edit-btn">Edit</button>${sendToClaudeBtn}<button class="resolve-btn">${comment.resolved ? 'Unresolve' : 'Resolve'}</button>${wontFixBtn}<button class="delete-btn">Delete</button></div></div><div class="unified-comment-content">${renderMarkdown(comment.content)}</div>`;

      wrapper.querySelector('.edit-btn')?.addEventListener('click', (e) => {
        e.stopPropagation();
        onEdit(comment.id);
      });

      wrapper.querySelector('.send-btn')?.addEventListener('click', (e) => {
        e.stopPropagation();
        onSendToClaude?.();
      });

      wrapper.querySelector('.resolve-btn')?.addEventListener('click', (e) => {
        e.stopPropagation();
        onResolve(comment.id, !comment.resolved);
      });

      wrapper.querySelector('.wontfix-btn')?.addEventListener('click', (e) => {
        e.stopPropagation();
        onWontFix(comment.id, !comment.wontFix);
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

    // Keyboard shortcuts - stop propagation to prevent CodeMirror from handling
    textarea.addEventListener('keydown', (e) => {
      e.stopPropagation();
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
// Collapsed Region Widget
// ============================================================================

interface CollapsedRegionConfig {
  startDocLine: number;
  endDocLine: number;
  lineCount: number;
  onExpand: (startDocLine: number) => void;
}

class CollapsedRegionWidget extends WidgetType {
  constructor(readonly config: CollapsedRegionConfig) {
    super();
  }

  toDOM() {
    const { startDocLine, lineCount, onExpand } = this.config;

    const wrapper = document.createElement('div');
    wrapper.className = 'cm-collapsed-region';
    wrapper.innerHTML = `<span class="cm-collapsed-icon">⊕</span><span class="cm-collapsed-text">${lineCount} lines hidden</span>`;

    wrapper.addEventListener('click', (e) => {
      e.stopPropagation();
      onExpand(startDocLine);
    });

    return wrapper;
  }

  ignoreEvent(event: Event) {
    return event.type.startsWith('mouse');
  }

  eq(other: CollapsedRegionWidget) {
    return (
      this.config.startDocLine === other.config.startDocLine &&
      this.config.lineCount === other.config.lineCount
    );
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

/**
 * Render markdown to HTML for inline comment display.
 * Uses marked with GFM support and breaks enabled.
 */
function renderMarkdown(text: string): string {
  return marked.parse(text, {
    gfm: true,
    breaks: true,
  }) as string;
}

// ============================================================================
// Component
// ============================================================================

export function UnifiedDiffEditor({
  original,
  modified,
  comments,
  editingCommentId,
  resolvedTheme = 'dark',
  fontSize = 13,
  language,
  contextLines = 0,
  scrollToLine,
  filePath,
  onAddComment,
  onEditComment,
  onStartEdit,
  onCancelEdit,
  onResolveComment,
  onWontFixComment,
  onDeleteComment,
  onSendToClaude,
}: UnifiedDiffEditorProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const editorViewRef = useRef<EditorView | null>(null);
  const linesRef = useRef<DiffLine[]>([]);

  // Track which lines have open "new comment" forms
  const [newCommentLines, setNewCommentLines] = useState<Set<number>>(new Set());

  // Track text selection for popup
  const [selection, setSelection] = useState<{
    startLine: number;
    endLine: number;
    startCol: number;
    endCol: number;
  } | null>(null);

  // Track which collapsed regions have been expanded by the user
  const [expandedRegions, setExpandedRegions] = useState<Set<number>>(new Set());

  // Handler to expand a collapsed region
  const handleExpandRegion = useCallback((startDocLine: number) => {
    setExpandedRegions((prev) => {
      const next = new Set(prev);
      next.add(startDocLine);
      return next;
    });
  }, []);

  // Build unified document - memoize to prevent editor recreation on unrelated state changes
  const { content, lines } = useMemo(
    () => buildUnifiedDocument(original, modified),
    [original, modified]
  );
  linesRef.current = lines;

  // Get language extension based on language prop
  const languageExtension = useMemo((): Extension => {
    switch (language) {
      case 'javascript':
      case 'js':
        return javascript();
      case 'typescript':
      case 'ts':
        return javascript({ typescript: true });
      case 'jsx':
        return javascript({ jsx: true });
      case 'tsx':
        return javascript({ typescript: true, jsx: true });
      case 'python':
      case 'py':
        return python();
      case 'markdown':
      case 'md':
        return markdown();
      case 'json':
        return json();
      case 'css':
        return css();
      case 'html':
        return html();
      case 'go':
        return go();
      case 'rust':
      case 'rs':
        return rust();
      case 'sql':
        return sql();
      case 'java':
        return java();
      case 'kotlin':
      case 'kt':
        // Use Java highlighting for Kotlin (similar syntax)
        return java();
      case 'yaml':
      case 'yml':
        return yaml();
      default:
        return [];
    }
  }, [language]);

  // Handlers for comment forms
  const handleSaveComment = useCallback(
    async (docLine: number, commentContent: string) => {
      const anchor = createAnchor(docLine, linesRef.current);
      if (!anchor) {
        console.error('Failed to create anchor for line', docLine);
        return;
      }
      await onAddComment(docLine, commentContent, anchor);
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

  // Selection popup handlers
  const handleSendToClaudeFromSelection = useCallback(() => {
    if (!selection || !filePath || !onSendToClaude) return;

    // Get actual file line numbers from the diff metadata
    const startDiffLine = linesRef.current[selection.startLine - 1];
    const endDiffLine = linesRef.current[selection.endLine - 1];

    // Prefer modified line numbers, fall back to original for deleted lines
    const startFileNum = startDiffLine?.modifiedLine ?? startDiffLine?.originalLine;
    const endFileNum = endDiffLine?.modifiedLine ?? endDiffLine?.originalLine;

    if (startFileNum === null || startFileNum === undefined) return;

    const lineRef = startFileNum === endFileNum
      ? `L${startFileNum}`
      : `L${startFileNum}-L${endFileNum ?? startFileNum}`;

    const reference = `@${filePath}:${lineRef}`;
    onSendToClaude(reference);
    setSelection(null);
  }, [selection, filePath, onSendToClaude]);

  const handleAddCommentFromSelection = useCallback(() => {
    if (!selection) return;
    // Open comment form on the end line of the selection
    setNewCommentLines((prev) => {
      const next = new Set(prev);
      next.add(selection.endLine);
      return next;
    });
    setSelection(null);
  }, [selection]);

  // Get popup position based on selection
  const getPopupPosition = useCallback(() => {
    if (!selection || !editorViewRef.current) return null;
    const view = editorViewRef.current;
    try {
      const endLine = view.state.doc.line(selection.endLine);
      const endPos = endLine.from + selection.endCol;
      const coords = view.coordsAtPos(endPos);
      if (!coords) return null;
      const containerRect = containerRef.current?.getBoundingClientRect();
      if (!containerRect) return null;
      return {
        top: coords.top - containerRect.top - 36, // Position above selection
        left: coords.left - containerRect.left,
      };
    } catch {
      return null;
    }
  }, [selection]);

  // Create editor
  useEffect(() => {
    if (!containerRef.current) return;

    const colorTheme = resolvedTheme === 'light' ? LIGHT_THEME : DARK_THEME;
    const syntaxTheme =
      resolvedTheme === 'light'
        ? syntaxHighlighting(defaultHighlightStyle, { fallback: true })
        : oneDark;

    const theme = EditorView.theme(
      {
        '&': {
          height: '100%',
          backgroundColor: colorTheme.editorBg,
          overflow: 'hidden',
        },
        '.cm-scroller': {
          fontFamily: "'JetBrains Mono', 'Fira Code', Consolas, monospace",
          fontSize: `${fontSize}px`,
          lineHeight: '1.6',
          overflow: 'auto !important',
        },
        '.cm-content': {
          minWidth: '0',
        },
        '.cm-gutters': {
          backgroundColor: colorTheme.guttersBg,
          borderRight: `1px solid ${colorTheme.guttersBorder}`,
        },
        // Custom line number gutters (GitHub-style compact)
        '.cm-original-gutter, .cm-modified-gutter': {
          minWidth: '32px',
          textAlign: 'right',
          padding: '0 4px',
          color: colorTheme.gutterText,
          fontSize: '12px',
          userSelect: 'none',
        },
        '.cm-original-gutter .cm-lineNumber, .cm-modified-gutter .cm-lineNumber': {
          padding: '0 4px',
        },
        '.cm-lineNumber-blank': {
          color: colorTheme.gutterBlank,
        },
        '.cm-deleted-line': {
          backgroundColor: `${colorTheme.deletedLineBg} !important`,
        },
        '.cm-added-line': {
          backgroundColor: `${colorTheme.addedLineBg} !important`,
        },
        // Saved comments
        '.unified-comment': {
          background: colorTheme.commentBg,
          borderLeft: `3px solid ${colorTheme.commentBorder}`,
          margin: '2px 0',
          padding: '6px 10px',
          borderRadius: '0 4px 4px 0',
          fontFamily: '-apple-system, BlinkMacSystemFont, sans-serif',
        },
        '.unified-comment.resolved': {
          borderLeftColor: colorTheme.resolveButton,
          background: colorTheme.commentResolvedBg,
        },
        '.unified-comment.wont-fix': {
          borderLeftColor: colorTheme.wontFixButton,
          background: colorTheme.commentWontFixBg,
        },
        '.unified-comment-header': {
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          marginBottom: '4px',
        },
        '.unified-comment-left': {
          display: 'flex',
          alignItems: 'center',
          gap: '8px',
        },
        '.unified-comment-author': {
          fontSize: `${fontSize - 1}px`,
          fontWeight: '600',
          padding: '2px 8px',
          borderRadius: '4px',
          color: colorTheme.authorText,
          background: colorTheme.authorBg,
        },
        '.unified-comment-resolved': {
          fontSize: `${fontSize - 2}px`,
          fontWeight: '500',
          padding: '2px 8px',
          borderRadius: '4px',
          color: colorTheme.resolvedText,
          background: colorTheme.resolvedBadgeBg,
        },
        '.unified-comment-wontfix': {
          fontSize: `${fontSize - 2}px`,
          fontWeight: '500',
          padding: '2px 8px',
          borderRadius: '4px',
          color: colorTheme.wontFixText,
          background: colorTheme.wontFixBadgeBg,
        },
        '.unified-comment-content': {
          color: colorTheme.commentContent,
          fontSize: `${fontSize}px`,
          lineHeight: '1.4',
        },
        '.unified-comment-content p': {
          margin: '0 0 8px 0',
        },
        '.unified-comment-content p:last-child': {
          marginBottom: '0',
        },
        '.unified-comment-content code': {
          background: colorTheme.commentCodeBg,
          padding: '1px 4px',
          borderRadius: '3px',
          fontFamily: "'SF Mono', Monaco, Menlo, monospace",
          fontSize: `${fontSize - 1}px`,
        },
        '.unified-comment-content pre': {
          background: colorTheme.commentCodeBg,
          padding: '8px',
          borderRadius: '4px',
          overflowX: 'auto',
          margin: '8px 0',
        },
        '.unified-comment-content pre code': {
          background: 'none',
          padding: '0',
        },
        '.unified-comment-content ul, .unified-comment-content ol': {
          margin: '8px 0',
          paddingLeft: '20px',
        },
        '.unified-comment-content li': {
          margin: '4px 0',
        },
        '.unified-comment-content a': {
          color: colorTheme.commentLink,
          textDecoration: 'none',
        },
        '.unified-comment-content a:hover': {
          textDecoration: 'underline',
        },
        '.unified-comment-content blockquote': {
          borderLeft: `3px solid ${colorTheme.commentBlockquoteBorder}`,
          margin: '8px 0',
          paddingLeft: '12px',
          color: colorTheme.commentBlockquoteText,
        },
        '.unified-comment-content strong': {
          fontWeight: '600',
        },
        '.unified-comment-actions': {
          display: 'flex',
          gap: '6px',
        },
        '.unified-comment-actions button': {
          background: 'transparent',
          border: `1px solid ${colorTheme.buttonBorder}`,
          color: colorTheme.buttonText,
          fontSize: `${fontSize - 2}px`,
          padding: '2px 8px',
          borderRadius: '3px',
          cursor: 'pointer',
        },
        '.unified-comment-actions button:hover': {
          background: colorTheme.buttonHoverBg,
          color: colorTheme.buttonHoverText,
        },
        '.unified-comment-actions .resolve-btn': {
          borderColor: colorTheme.resolveButton,
          color: colorTheme.resolveButton,
        },
        '.unified-comment-actions .resolve-btn:hover': {
          background: colorTheme.resolveButton,
          color: colorTheme.authorText,
        },
        '.unified-comment-actions .wontfix-btn': {
          borderColor: colorTheme.wontFixButton,
          color: colorTheme.wontFixButton,
        },
        '.unified-comment-actions .wontfix-btn:hover': {
          background: colorTheme.wontFixButton,
          color: colorTheme.authorText,
        },
        '.unified-comment-actions .delete-btn': {
          borderColor: colorTheme.deleteButton,
          color: colorTheme.deleteButton,
        },
        '.unified-comment-actions .delete-btn:hover': {
          background: colorTheme.deleteButton,
          color: colorTheme.authorText,
        },
        '.unified-comment-actions .send-btn': {
          borderColor: colorTheme.sendButton,
          color: colorTheme.sendButton,
        },
        '.unified-comment-actions .send-btn:hover': {
          background: colorTheme.sendButton,
          color: colorTheme.authorText,
        },
        // New comment form
        '.unified-comment-form': {
          background: colorTheme.newCommentBg,
          borderLeft: `3px solid ${colorTheme.newCommentBorder}`,
          margin: '2px 0',
          padding: '6px 10px',
          borderRadius: '0 4px 4px 0',
          fontFamily: '-apple-system, BlinkMacSystemFont, sans-serif',
        },
        '.unified-comment-textarea': {
          width: '100%',
          minHeight: '48px',
          padding: '6px 8px',
          background: colorTheme.textareaBg,
          border: `1px solid ${colorTheme.textareaBorder}`,
          borderRadius: '4px',
          color: colorTheme.textareaText,
          fontFamily: 'inherit',
          fontSize: `${fontSize}px`,
          lineHeight: '1.4',
          resize: 'vertical',
          boxSizing: 'border-box',
        },
        '.unified-comment-textarea:focus': {
          outline: 'none',
          borderColor: colorTheme.textareaFocus,
        },
        '.unified-comment-textarea::placeholder': {
          color: colorTheme.textareaPlaceholder,
        },
        '.unified-comment-form-actions': {
          display: 'flex',
          justifyContent: 'flex-end',
          gap: '6px',
          marginTop: '6px',
        },
        '.unified-comment-form-actions button': {
          background: 'transparent',
          border: `1px solid ${colorTheme.buttonBorder}`,
          color: colorTheme.buttonText,
          fontSize: `${fontSize - 2}px`,
          padding: '2px 8px',
          borderRadius: '3px',
          cursor: 'pointer',
        },
        '.unified-comment-form-actions button:hover': {
          background: colorTheme.buttonHoverBg,
          color: colorTheme.buttonHoverText,
        },
        '.unified-comment-form-actions .save-btn': {
          background: colorTheme.saveButtonBg,
          borderColor: colorTheme.saveButtonBg,
          color: colorTheme.authorText,
        },
        '.unified-comment-form-actions .save-btn:hover': {
          background: colorTheme.saveButtonHoverBg,
          borderColor: colorTheme.saveButtonHoverBg,
        },
        // Collapsed region indicator
        '.cm-collapsed-region': {
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          gap: '8px',
          padding: '4px 16px',
          background: colorTheme.collapsedBg,
          borderTop: `1px solid ${colorTheme.collapsedBorder}`,
          borderBottom: `1px solid ${colorTheme.collapsedBorder}`,
          color: colorTheme.collapsedText,
          fontSize: `${fontSize - 1}px`,
          fontFamily: '-apple-system, BlinkMacSystemFont, sans-serif',
          cursor: 'pointer',
          userSelect: 'none',
        },
        '.cm-collapsed-region:hover': {
          background: colorTheme.collapsedHoverBg,
          color: colorTheme.collapsedHoverText,
        },
        '.cm-collapsed-icon': {
          fontSize: `${fontSize + 1}px`,
        },
        '.cm-collapsed-text': {
          fontWeight: '500',
        },
      },
      { dark: resolvedTheme === 'dark' }
    );

    // Click handler to open comment form (only when no selection)
    const clickHandler = EditorView.domEventHandlers({
      click: (event, view) => {
        const target = event.target as HTMLElement;
        // Don't open comment form when clicking on widgets or popup
        if (
          target.closest('.unified-comment') ||
          target.closest('.unified-comment-form') ||
          target.closest('.cm-collapsed-region') ||
          target.closest('.cm-selection-popup')
        ) {
          return false;
        }

        // Don't open comment form if there's a selection
        // (the selection listener will handle showing/hiding the popup)
        const sel = view.state.selection.main;
        if (!sel.empty) {
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

    // Selection change listener
    const selectionListener = EditorView.updateListener.of((update) => {
      if (update.selectionSet) {
        const sel = update.state.selection.main;
        if (sel.empty) {
          setSelection(null);
        } else {
          const startLine = update.state.doc.lineAt(sel.from).number;
          const endLine = update.state.doc.lineAt(sel.to).number;
          const startCol = sel.from - update.state.doc.lineAt(sel.from).from;
          const endCol = sel.to - update.state.doc.lineAt(sel.to).from;
          setSelection({ startLine, endLine, startCol, endCol });
        }
      }
    });

    const view = new EditorView({
      state: EditorState.create({
        doc: content,
        extensions: [
          EditorState.readOnly.of(true),
          EditorView.editable.of(false),
          EditorView.lineWrapping,
          lineMetadataField,
          originalLineGutter,
          modifiedLineGutter,
          highlightSpecialChars(),
          languageExtension,
          syntaxTheme,
          theme,
          lineDecorationsField,
          collapsedDecorationsField,
          commentWidgetsField,
          clickHandler,
          selectionListener,
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
  }, [content, lines, fontSize, languageExtension, resolvedTheme]);

  // Clear selection when file content changes (prevents stale popup)
  useEffect(() => {
    setSelection(null);
  }, [content]);

  // Update comment widgets when comments or newCommentLines change
  useEffect(() => {
    const view = editorViewRef.current;
    if (!view) return;

    const widgets: { pos: number; widget: WidgetType }[] = [];

    // Add saved comments
    for (const comment of comments) {
      if (comment.docLine > 0 && comment.docLine <= view.state.doc.lines) {
        const line = view.state.doc.line(comment.docLine);

        // Create send to claude callback for this comment
        const sendToClaudeForComment = onSendToClaude && filePath && comment.anchor
          ? () => {
              const lineNum = comment.anchor!.line;
              const reference = `@${filePath}:L${lineNum}\nComment: ${comment.content}`;
              onSendToClaude(reference);
            }
          : undefined;

        widgets.push({
          pos: line.to,
          widget: new CommentWidget({
            comment,
            isEditing: editingCommentId === comment.id,
            onEdit: onStartEdit,
            onSaveEdit: onEditComment,
            onCancelEdit: onCancelEdit,
            onResolve: onResolveComment,
            onWontFix: onWontFixComment,
            onDelete: onDeleteComment,
            onSendToClaude: sendToClaudeForComment,
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
  // NOTE: fontSize is included because the main editor effect recreates the EditorView when fontSize changes.
  // Without fontSize here, this effect wouldn't re-run and comment widgets would be lost.
  }, [comments, newCommentLines, editingCommentId, handleSaveComment, handleCancelComment, onStartEdit, onEditComment, onCancelEdit, onResolveComment, onWontFixComment, onDeleteComment, onSendToClaude, filePath, fontSize]);

  // Update collapsed region decorations when contextLines or expandedRegions change
  useEffect(() => {
    const view = editorViewRef.current;
    if (!view || contextLines <= 0) {
      // No collapsing needed - clear any existing collapsed decorations
      if (view) {
        view.dispatch({ effects: setCollapsedDecorations.of(Decoration.none) });
      }
      return;
    }

    const { collapsedRegions } = calculateHunks(lines, contextLines);

    // Build set of comment lines for auto-expand
    const commentLines = new Set(comments.map((c) => c.docLine));
    const newCommentLinesSet = newCommentLines;

    // Filter out regions that are expanded or contain comments
    const regionsToCollapse = collapsedRegions.filter((region) => {
      // Skip if user has expanded this region
      if (expandedRegions.has(region.startDocLine)) {
        return false;
      }

      // Skip if region contains any comments (auto-expand)
      for (let line = region.startDocLine; line <= region.endDocLine; line++) {
        if (commentLines.has(line) || newCommentLinesSet.has(line)) {
          return false;
        }
      }

      return true;
    });

    // Build replace decorations for collapsed regions
    const decorations: Range<Decoration>[] = [];
    const doc = view.state.doc;

    for (const region of regionsToCollapse) {
      // Get positions for the range
      const startLine = doc.line(region.startDocLine);
      const endLine = doc.line(region.endDocLine);

      // Create a widget that replaces this range
      const widget = new CollapsedRegionWidget({
        startDocLine: region.startDocLine,
        endDocLine: region.endDocLine,
        lineCount: region.lineCount,
        onExpand: handleExpandRegion,
      });

      // Replace from start of first line to end of last line
      decorations.push(
        Decoration.replace({
          widget,
          block: true,
        }).range(startLine.from, endLine.to)
      );
    }

    // Sort by position (required for RangeSet)
    decorations.sort((a, b) => a.from - b.from);

    view.dispatch({
      effects: setCollapsedDecorations.of(Decoration.set(decorations)),
    });
  // NOTE: fontSize is included because the main editor effect recreates the EditorView when fontSize changes.
  // Without fontSize here, this effect wouldn't re-run and collapsed regions would be lost.
  }, [lines, contextLines, expandedRegions, comments, newCommentLines, handleExpandRegion, fontSize]);

  // Scroll to line when scrollToLine prop changes or content loads
  // We depend on `lines` so we re-run when new file content arrives
  useEffect(() => {
    if (!scrollToLine || !editorViewRef.current) return;

    const view = editorViewRef.current;

    // Find the doc line index that corresponds to this modified file line
    const targetIndex = lines.findIndex(
      (line) => line.type !== 'deleted' && line.modifiedLine === scrollToLine
    );

    if (targetIndex !== -1) {
      try {
        const docLineNum = targetIndex + 1; // Convert 0-indexed to 1-indexed
        const docLine = view.state.doc.line(docLineNum);
        view.dispatch({
          effects: EditorView.scrollIntoView(docLine.from, { y: 'center' }),
        });
      } catch (e) {
        console.warn('[UnifiedDiffEditor] Failed to scroll to line:', scrollToLine, e);
      }
    }
  }, [scrollToLine, lines]);

  const popupPosition = getPopupPosition();
  const colorTheme = resolvedTheme === 'light' ? LIGHT_THEME : DARK_THEME;

  return (
    <div
      ref={containerRef}
      className="unified-diff-editor"
      style={{ height: '100%', width: '100%', position: 'relative' }}
      data-testid="unified-diff-editor"
    >
      {/* Selection popup */}
      {selection && popupPosition && (
        <div
          style={{
            position: 'absolute',
            top: popupPosition.top,
            left: popupPosition.left,
            zIndex: 100,
            display: 'flex',
            gap: '4px',
            background: colorTheme.popupBg,
            border: `1px solid ${colorTheme.popupBorder}`,
            borderRadius: '4px',
            padding: '4px',
            boxShadow: colorTheme.popupShadow,
          }}
        >
          {onSendToClaude && filePath && (
            <button
              title="Send to Claude Code"
              onClick={handleSendToClaudeFromSelection}
              style={{
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
                width: '24px',
                height: '24px',
                padding: 0,
                border: 'none',
                borderRadius: '4px',
                background: 'transparent',
                cursor: 'pointer',
                color: colorTheme.popupSendIcon,
              }}
            >
              <ClaudeIcon size={16} />
            </button>
          )}
          <button
            title="Add Comment"
            onClick={handleAddCommentFromSelection}
            style={{
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              width: '24px',
              height: '24px',
              padding: 0,
              border: 'none',
              borderRadius: '4px',
              background: 'transparent',
              cursor: 'pointer',
              color: colorTheme.popupCommentIcon,
            }}
          >
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
              <path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/>
            </svg>
          </button>
        </div>
      )}
    </div>
  );
}

export default UnifiedDiffEditor;
