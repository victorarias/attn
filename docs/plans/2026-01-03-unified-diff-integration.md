# UnifiedDiffEditor Integration into ReviewPanel

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Replace ReviewPanel's complex comment system with UnifiedDiffEditor, reducing ~900 lines of legacy code.

**Architecture:** UnifiedDiffEditor becomes a child component of ReviewPanel. ReviewPanel keeps file navigation, state management, and API calls. UnifiedDiffEditor handles all diff rendering and comment UI.

**Tech Stack:** React, CodeMirror 6, existing daemon API

---

## Architecture Overview

```
ReviewPanel (container)
├── File list sidebar (unchanged)
├── Controls bar (font size, context) (unchanged)
└── UnifiedDiffEditor (new child component)
    ├── Diff rendering
    ├── Comment UI (forms, saved, edit mode)
    └── Hunks/collapsed context
```

### What Stays in ReviewPanel
- File list with navigation
- File viewed/unviewed tracking
- Review state management (reviewId, loading, error)
- Diff fetching from daemon
- Comment CRUD API calls
- Font size and context controls
- Auto-skip patterns

### What UnifiedDiffEditor Handles
- All diff rendering (unified view with deleted lines as document lines)
- All comment UI
- Click handling for adding comments
- Hunks/collapsed context display

---

## Data Flow

### Adding a Comment
1. User clicks line in UnifiedDiffEditor
2. User types and saves comment
3. UnifiedDiffEditor calls `onAddComment(docLine, content, anchor)`
4. ReviewPanel receives anchor with `{ side: 'original'|'modified', line: N }`
5. ReviewPanel calls API with original/modified line number

### Loading Comments
1. ReviewPanel fetches comments from API (have `line_start`, `filepath`)
2. ReviewPanel converts to UnifiedDiffEditor format using `resolveAnchor()`
3. UnifiedDiffEditor receives comments with calculated `docLine`
4. Comments marked `isOutdated` or `isOrphaned` if line changed/removed

---

## Interface Changes to UnifiedDiffEditor

### Extended Callback
```typescript
// Before
onAddComment: (docLine: number, content: string) => Promise<void>

// After
onAddComment: (docLine: number, content: string, anchor: CommentAnchor) => Promise<void>
```

UnifiedDiffEditor calls `createAnchor()` internally before invoking callback.

### Comment Props
Comments passed in should include `anchor` field. UnifiedDiffEditor uses `resolveAnchor()` to calculate `docLine` and detect outdated/orphaned state.

---

## Code Removal from ReviewPanel

### Delete (~900 lines total)

1. **Widget classes** (~150 lines)
   - `InlineCommentWidget`
   - `NewCommentFormWidget`
   - `createCommentElement()` helper

2. **DOM injection for deleted lines** (~200 lines)
   - `canAddCommentToDeletedLine()`
   - `handleDeletedChunkClick` listener
   - setTimeout injection logic
   - `newDeletedLineComments` state

3. **Duplicate state** (~50 lines)
   - `newCommentLines` / `newDeletedLineComments`
   - `regularComments` / `deletedLineComments`
   - Draft refs

4. **Monolithic editor useEffect** (~400 lines)
   - Replace with UnifiedDiffEditor mount

5. **Comment widget update effect** (~100 lines)
   - Handled internally by UnifiedDiffEditor

---

## Implementation Tasks

### Task 1: Extend UnifiedDiffEditor Interface

**Files:**
- Modify: `src/components/UnifiedDiffEditor.tsx`

Add anchor to onAddComment callback:
```typescript
onAddComment: (docLine: number, content: string, anchor: CommentAnchor) => Promise<void>
```

In `handleSaveComment`, call `createAnchor()` and pass to callback.

Update harness to accept (and ignore) the new anchor parameter.

### Task 2: Add Comment Conversion Utilities

**Files:**
- Modify: `src/components/ReviewPanel.tsx`

Add helper functions:
```typescript
function toEditorComment(comment: ReviewComment, lines: DiffLine[]): InlineComment
function fromEditorAnchor(anchor: CommentAnchor): { line_start: number; side: string }
```

### Task 3: Replace Editor with UnifiedDiffEditor

**Files:**
- Modify: `src/components/ReviewPanel.tsx`

1. Import UnifiedDiffEditor
2. Remove old editor creation useEffect
3. Add UnifiedDiffEditor component with props:
   - original, modified, comments, editingCommentId
   - fontSize, language (from file extension), contextLines
   - All comment callbacks wired to API

### Task 4: Remove Legacy Comment Code

**Files:**
- Modify: `src/components/ReviewPanel.tsx`

Delete in order:
1. Widget classes (InlineCommentWidget, NewCommentFormWidget, createCommentElement)
2. DOM injection code (handleDeletedChunkClick, canAddCommentToDeletedLine)
3. Duplicate state (newDeletedLineComments, deletedLineComments, etc.)
4. Comment update effect
5. Unused imports

### Task 5: Update Tests

**Files:**
- Modify: `src/components/ReviewPanel.test.tsx`
- Create: `e2e/review-panel-comments.spec.ts`

1. Update unit tests for new comment flow
2. Add 2 E2E tests:
   - Comment round-trip (add → refresh → still there)
   - Comment on deleted line works

### Task 6: Final Cleanup

**Files:**
- Modify: `src/components/ReviewPanel.tsx`
- Modify: `src/components/ReviewPanel.css`

1. Remove unused CSS classes
2. Remove unused imports
3. Verify no dead code remains

---

## Testing Strategy

**E2E (high-level only):**
- Comment round-trip flow
- Comment on deleted line saves correctly

**Unit:**
- `toEditorComment()` conversion
- `fromEditorAnchor()` conversion

**Existing tests:**
- UnifiedDiffEditor's 21 E2E + 35 unit tests (unchanged)
- ReviewPanel file navigation tests (should still pass)

---

## Risks & Mitigations

1. **Scroll position on comment save** - UnifiedDiffEditor already handles this (no editor recreation)

2. **Outdated comment detection** - Use `resolveAnchor()` which checks content hash

3. **API compatibility** - Keep same `line_start`/`line_end` format, just compute from anchor

4. **CSS conflicts** - UnifiedDiffEditor has self-contained styles via CodeMirror theme
