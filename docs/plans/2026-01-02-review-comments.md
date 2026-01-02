# Review Comments Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add inline review comments that persist across sessions and can be sent to Claude Code.

**Architecture:** SQLite-backed comments with line anchoring. Comments display as gutter markers in CodeMirror; clicking opens a popover. "Send to Claude Code" copies context to clipboard for pasting into the main session.

**Tech Stack:** Go (SQLite, WebSocket handlers), TypeSpec (protocol), TypeScript/React (UI), CodeMirror 6 (gutter markers)

---

## Task 1: SQLite Migration for review_comments Table

**Files:**
- Modify: `internal/store/sqlite.go:122` (add migration 14)

**Step 1: Add migration**

Add after the existing migration 13:

```go
{14, "create review_comments table", `CREATE TABLE IF NOT EXISTS review_comments (
	id TEXT PRIMARY KEY,
	review_id TEXT NOT NULL,
	filepath TEXT NOT NULL,
	line_start INTEGER NOT NULL,
	line_end INTEGER NOT NULL,
	content TEXT NOT NULL,
	author TEXT NOT NULL,
	resolved INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL,
	FOREIGN KEY (review_id) REFERENCES reviews(id) ON DELETE CASCADE
)`},
```

**Step 2: Verify migration applies**

Run: `make install && rm ~/.attn/attn.db && make install`

Expected: New daemon starts, creates DB with review_comments table

**Step 3: Commit**

```bash
git add internal/store/sqlite.go
git commit -m "feat: add review_comments table migration"
```

---

## Task 2: ReviewComment Store Operations

**Files:**
- Modify: `internal/store/review.go` (add after line 155)
- Test: `internal/store/review_test.go`

**Step 1: Write tests for comment CRUD**

Add to `internal/store/review_test.go`:

```go
func TestReviewComments(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	// Create a review first
	review, err := store.GetOrCreateReview("/test/repo", "main")
	require.NoError(t, err)

	// Test AddComment
	comment, err := store.AddComment(review.ID, "src/foo.go", 10, 15, "Check null here", "user")
	require.NoError(t, err)
	assert.NotEmpty(t, comment.ID)
	assert.Equal(t, review.ID, comment.ReviewID)
	assert.Equal(t, "src/foo.go", comment.Filepath)
	assert.Equal(t, 10, comment.LineStart)
	assert.Equal(t, 15, comment.LineEnd)
	assert.Equal(t, "Check null here", comment.Content)
	assert.Equal(t, "user", comment.Author)
	assert.False(t, comment.Resolved)

	// Test GetComments
	comments, err := store.GetComments(review.ID)
	require.NoError(t, err)
	assert.Len(t, comments, 1)

	// Test GetCommentsForFile
	fileComments, err := store.GetCommentsForFile(review.ID, "src/foo.go")
	require.NoError(t, err)
	assert.Len(t, fileComments, 1)

	// Test UpdateComment
	err = store.UpdateComment(comment.ID, "Updated content")
	require.NoError(t, err)
	updated, err := store.GetCommentByID(comment.ID)
	require.NoError(t, err)
	assert.Equal(t, "Updated content", updated.Content)

	// Test ResolveComment
	err = store.ResolveComment(comment.ID, true)
	require.NoError(t, err)
	resolved, err := store.GetCommentByID(comment.ID)
	require.NoError(t, err)
	assert.True(t, resolved.Resolved)

	// Test DeleteComment
	err = store.DeleteComment(comment.ID)
	require.NoError(t, err)
	comments, err = store.GetComments(review.ID)
	require.NoError(t, err)
	assert.Len(t, comments, 0)
}
```

**Step 2: Run tests to verify they fail**

Run: `go test ./internal/store -run TestReviewComments -v`

Expected: FAIL - AddComment not defined

**Step 3: Implement ReviewComment struct and operations**

Add to `internal/store/review.go`:

```go
// ReviewComment represents a comment on a code review
type ReviewComment struct {
	ID        string
	ReviewID  string
	Filepath  string
	LineStart int
	LineEnd   int
	Content   string
	Author    string // "user" or "agent"
	Resolved  bool
	CreatedAt time.Time
}

// AddComment adds a new comment to a review
func (s *Store) AddComment(reviewID, filepath string, lineStart, lineEnd int, content, author string) (*ReviewComment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	comment := &ReviewComment{
		ID:        uuid.New().String(),
		ReviewID:  reviewID,
		Filepath:  filepath,
		LineStart: lineStart,
		LineEnd:   lineEnd,
		Content:   content,
		Author:    author,
		Resolved:  false,
		CreatedAt: now,
	}

	_, err := s.db.Exec(`
		INSERT INTO review_comments (id, review_id, filepath, line_start, line_end, content, author, resolved, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, comment.ID, comment.ReviewID, comment.Filepath, comment.LineStart, comment.LineEnd,
		comment.Content, comment.Author, 0, now.Format(time.RFC3339))
	if err != nil {
		return nil, err
	}

	return comment, nil
}

// GetComments returns all comments for a review
func (s *Store) GetComments(reviewID string) ([]*ReviewComment, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(`
		SELECT id, review_id, filepath, line_start, line_end, content, author, resolved, created_at
		FROM review_comments WHERE review_id = ? ORDER BY filepath, line_start
	`, reviewID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanComments(rows)
}

// GetCommentsForFile returns comments for a specific file
func (s *Store) GetCommentsForFile(reviewID, filepath string) ([]*ReviewComment, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(`
		SELECT id, review_id, filepath, line_start, line_end, content, author, resolved, created_at
		FROM review_comments WHERE review_id = ? AND filepath = ? ORDER BY line_start
	`, reviewID, filepath)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanComments(rows)
}

// GetCommentByID returns a single comment by ID
func (s *Store) GetCommentByID(id string) (*ReviewComment, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var comment ReviewComment
	var resolved int
	var createdAt string

	err := s.db.QueryRow(`
		SELECT id, review_id, filepath, line_start, line_end, content, author, resolved, created_at
		FROM review_comments WHERE id = ?
	`, id).Scan(&comment.ID, &comment.ReviewID, &comment.Filepath, &comment.LineStart,
		&comment.LineEnd, &comment.Content, &comment.Author, &resolved, &createdAt)
	if err != nil {
		return nil, err
	}

	comment.Resolved = resolved == 1
	comment.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	return &comment, nil
}

// UpdateComment updates the content of a comment
func (s *Store) UpdateComment(id, content string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`UPDATE review_comments SET content = ? WHERE id = ?`, content, id)
	return err
}

// ResolveComment sets the resolved status of a comment
func (s *Store) ResolveComment(id string, resolved bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	resolvedInt := 0
	if resolved {
		resolvedInt = 1
	}
	_, err := s.db.Exec(`UPDATE review_comments SET resolved = ? WHERE id = ?`, resolvedInt, id)
	return err
}

// DeleteComment deletes a comment
func (s *Store) DeleteComment(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`DELETE FROM review_comments WHERE id = ?`, id)
	return err
}

func scanComments(rows *sql.Rows) ([]*ReviewComment, error) {
	var comments []*ReviewComment
	for rows.Next() {
		var comment ReviewComment
		var resolved int
		var createdAt string
		if err := rows.Scan(&comment.ID, &comment.ReviewID, &comment.Filepath, &comment.LineStart,
			&comment.LineEnd, &comment.Content, &comment.Author, &resolved, &createdAt); err != nil {
			return nil, err
		}
		comment.Resolved = resolved == 1
		comment.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		comments = append(comments, &comment)
	}
	return comments, rows.Err()
}
```

**Step 4: Run tests to verify they pass**

Run: `go test ./internal/store -run TestReviewComments -v`

Expected: PASS

**Step 5: Commit**

```bash
git add internal/store/review.go internal/store/review_test.go
git commit -m "feat: add ReviewComment store operations"
```

---

## Task 3: Protocol Types for Comments

**Files:**
- Modify: `internal/protocol/schema/main.tsp`
- Run: `make generate-types`

**Step 1: Add comment model and messages to TypeSpec**

Add after the `ReviewState` model (~line 550):

```typespec
model ReviewComment {
  id: string;
  review_id: string;
  filepath: string;
  line_start: int32;
  line_end: int32;
  content: string;
  author: string;  // "user" or "agent"
  resolved: boolean;
  created_at: string;  // ISO timestamp
}

// Commands
model AddCommentMessage {
  cmd: "add_comment";
  review_id: string;
  filepath: string;
  line_start: int32;
  line_end: int32;
  content: string;
}

model UpdateCommentMessage {
  cmd: "update_comment";
  comment_id: string;
  content: string;
}

model ResolveCommentMessage {
  cmd: "resolve_comment";
  comment_id: string;
  resolved: boolean;
}

model DeleteCommentMessage {
  cmd: "delete_comment";
  comment_id: string;
}

model GetCommentsMessage {
  cmd: "get_comments";
  review_id: string;
  filepath?: string;  // Optional: filter by file
}

// Results
model AddCommentResultMessage {
  event: "add_comment_result";
  success: boolean;
  comment?: ReviewComment;
  error?: string;
}

model UpdateCommentResultMessage {
  event: "update_comment_result";
  success: boolean;
  error?: string;
}

model ResolveCommentResultMessage {
  event: "resolve_comment_result";
  success: boolean;
  error?: string;
}

model DeleteCommentResultMessage {
  event: "delete_comment_result";
  success: boolean;
  error?: string;
}

model GetCommentsResultMessage {
  event: "get_comments_result";
  success: boolean;
  comments?: ReviewComment[];
  error?: string;
}
```

**Step 2: Generate types**

Run: `make generate-types`

Expected: `internal/protocol/generated.go` and `app/src/types/generated.ts` updated

**Step 3: Verify generation succeeded**

Run: `make check-types`

Expected: No differences

**Step 4: Commit**

```bash
git add internal/protocol/schema/main.tsp internal/protocol/generated.go app/src/types/generated.ts
git commit -m "feat: add comment protocol types"
```

---

## Task 4: Protocol Constants and Parsing

**Files:**
- Modify: `internal/protocol/constants.go`

**Step 1: Add command and event constants**

Add to command constants section:

```go
CmdAddComment      = "add_comment"
CmdUpdateComment   = "update_comment"
CmdResolveComment  = "resolve_comment"
CmdDeleteComment   = "delete_comment"
CmdGetComments     = "get_comments"
```

Add to event constants section:

```go
EventAddCommentResult     = "add_comment_result"
EventUpdateCommentResult  = "update_comment_result"
EventResolveCommentResult = "resolve_comment_result"
EventDeleteCommentResult  = "delete_comment_result"
EventGetCommentsResult    = "get_comments_result"
```

**Step 2: Add parse cases**

Add to `ParseMessage()` switch:

```go
case CmdAddComment:
	var msg AddCommentMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, fmt.Errorf("unmarshal add_comment: %w", err)
	}
	return &msg, nil
case CmdUpdateComment:
	var msg UpdateCommentMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, fmt.Errorf("unmarshal update_comment: %w", err)
	}
	return &msg, nil
case CmdResolveComment:
	var msg ResolveCommentMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, fmt.Errorf("unmarshal resolve_comment: %w", err)
	}
	return &msg, nil
case CmdDeleteComment:
	var msg DeleteCommentMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, fmt.Errorf("unmarshal delete_comment: %w", err)
	}
	return &msg, nil
case CmdGetComments:
	var msg GetCommentsMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, fmt.Errorf("unmarshal get_comments: %w", err)
	}
	return &msg, nil
```

**Step 3: Increment protocol version**

Update `ProtocolVersion` constant (increment by 1).

**Step 4: Verify build**

Run: `make build`

Expected: Build succeeds

**Step 5: Commit**

```bash
git add internal/protocol/constants.go
git commit -m "feat: add comment protocol constants and parsing"
```

---

## Task 5: WebSocket Handlers for Comments

**Files:**
- Modify: `internal/daemon/websocket.go`

**Step 1: Add handler case statements**

Add to the message handling switch in `handleWebSocketMessage`:

```go
case protocol.CmdAddComment:
	commentMsg := msg.(*protocol.AddCommentMessage)
	d.handleAddComment(client, commentMsg)

case protocol.CmdUpdateComment:
	commentMsg := msg.(*protocol.UpdateCommentMessage)
	d.handleUpdateComment(client, commentMsg)

case protocol.CmdResolveComment:
	commentMsg := msg.(*protocol.ResolveCommentMessage)
	d.handleResolveComment(client, commentMsg)

case protocol.CmdDeleteComment:
	commentMsg := msg.(*protocol.DeleteCommentMessage)
	d.handleDeleteComment(client, commentMsg)

case protocol.CmdGetComments:
	commentMsg := msg.(*protocol.GetCommentsMessage)
	d.handleGetComments(client, commentMsg)
```

**Step 2: Implement handlers**

Add handler functions:

```go
func (d *Daemon) handleAddComment(client *wsClient, msg *protocol.AddCommentMessage) {
	result := protocol.AddCommentResultMessage{
		Event:   protocol.EventAddCommentResult,
		Success: false,
	}

	comment, err := d.store.AddComment(msg.ReviewId, msg.Filepath, int(msg.LineStart), int(msg.LineEnd), msg.Content, "user")
	if err != nil {
		result.Error = ptr(err.Error())
		client.send(result)
		return
	}

	result.Success = true
	result.Comment = &protocol.ReviewComment{
		Id:        comment.ID,
		ReviewId:  comment.ReviewID,
		Filepath:  comment.Filepath,
		LineStart: int32(comment.LineStart),
		LineEnd:   int32(comment.LineEnd),
		Content:   comment.Content,
		Author:    comment.Author,
		Resolved:  comment.Resolved,
		CreatedAt: comment.CreatedAt.Format(time.RFC3339),
	}
	client.send(result)
}

func (d *Daemon) handleUpdateComment(client *wsClient, msg *protocol.UpdateCommentMessage) {
	result := protocol.UpdateCommentResultMessage{
		Event:   protocol.EventUpdateCommentResult,
		Success: false,
	}

	err := d.store.UpdateComment(msg.CommentId, msg.Content)
	if err != nil {
		result.Error = ptr(err.Error())
		client.send(result)
		return
	}

	result.Success = true
	client.send(result)
}

func (d *Daemon) handleResolveComment(client *wsClient, msg *protocol.ResolveCommentMessage) {
	result := protocol.ResolveCommentResultMessage{
		Event:   protocol.EventResolveCommentResult,
		Success: false,
	}

	err := d.store.ResolveComment(msg.CommentId, msg.Resolved)
	if err != nil {
		result.Error = ptr(err.Error())
		client.send(result)
		return
	}

	result.Success = true
	client.send(result)
}

func (d *Daemon) handleDeleteComment(client *wsClient, msg *protocol.DeleteCommentMessage) {
	result := protocol.DeleteCommentResultMessage{
		Event:   protocol.EventDeleteCommentResult,
		Success: false,
	}

	err := d.store.DeleteComment(msg.CommentId)
	if err != nil {
		result.Error = ptr(err.Error())
		client.send(result)
		return
	}

	result.Success = true
	client.send(result)
}

func (d *Daemon) handleGetComments(client *wsClient, msg *protocol.GetCommentsMessage) {
	result := protocol.GetCommentsResultMessage{
		Event:   protocol.EventGetCommentsResult,
		Success: false,
	}

	var comments []*store.ReviewComment
	var err error

	if msg.Filepath != nil && *msg.Filepath != "" {
		comments, err = d.store.GetCommentsForFile(msg.ReviewId, *msg.Filepath)
	} else {
		comments, err = d.store.GetComments(msg.ReviewId)
	}

	if err != nil {
		result.Error = ptr(err.Error())
		client.send(result)
		return
	}

	result.Success = true
	result.Comments = make([]protocol.ReviewComment, len(comments))
	for i, c := range comments {
		result.Comments[i] = protocol.ReviewComment{
			Id:        c.ID,
			ReviewId:  c.ReviewID,
			Filepath:  c.Filepath,
			LineStart: int32(c.LineStart),
			LineEnd:   int32(c.LineEnd),
			Content:   c.Content,
			Author:    c.Author,
			Resolved:  c.Resolved,
			CreatedAt: c.CreatedAt.Format(time.RFC3339),
		}
	}
	client.send(result)
}
```

**Step 3: Verify build and run tests**

Run: `make build && make test`

Expected: All tests pass

**Step 4: Commit**

```bash
git add internal/daemon/websocket.go
git commit -m "feat: add WebSocket handlers for comments"
```

---

## Task 6: Frontend Hook Functions for Comments

**Files:**
- Modify: `app/src/hooks/useDaemonSocket.ts`

**Step 1: Add comment types and hook functions**

Add after the existing ReviewState types (~line 145):

```typescript
export interface ReviewComment {
  id: string;
  review_id: string;
  filepath: string;
  line_start: number;
  line_end: number;
  content: string;
  author: string;
  resolved: boolean;
  created_at: string;
}

interface AddCommentResult {
  success: boolean;
  comment?: ReviewComment;
  error?: string;
}

interface CommentActionResult {
  success: boolean;
  error?: string;
}

interface GetCommentsResult {
  success: boolean;
  comments?: ReviewComment[];
  error?: string;
}
```

**Step 2: Add result handlers to message switch**

Add cases in the message handling switch:

```typescript
case 'add_comment_result': {
  const pending = pendingActionsRef.current.get('add_comment');
  if (pending) {
    pendingActionsRef.current.delete('add_comment');
    if (data.success) {
      pending.resolve({ success: true, comment: data.comment });
    } else {
      pending.reject(new Error(data.error || 'Failed to add comment'));
    }
  }
  break;
}

case 'update_comment_result': {
  const key = `update_comment_${data.comment_id}`;
  const pending = pendingActionsRef.current.get(key);
  if (pending) {
    pendingActionsRef.current.delete(key);
    if (data.success) {
      pending.resolve({ success: true });
    } else {
      pending.reject(new Error(data.error || 'Failed to update comment'));
    }
  }
  break;
}

case 'resolve_comment_result': {
  const key = `resolve_comment_${data.comment_id}`;
  const pending = pendingActionsRef.current.get(key);
  if (pending) {
    pendingActionsRef.current.delete(key);
    if (data.success) {
      pending.resolve({ success: true });
    } else {
      pending.reject(new Error(data.error || 'Failed to resolve comment'));
    }
  }
  break;
}

case 'delete_comment_result': {
  const key = `delete_comment_${data.comment_id}`;
  const pending = pendingActionsRef.current.get(key);
  if (pending) {
    pendingActionsRef.current.delete(key);
    if (data.success) {
      pending.resolve({ success: true });
    } else {
      pending.reject(new Error(data.error || 'Failed to delete comment'));
    }
  }
  break;
}

case 'get_comments_result': {
  const pending = pendingActionsRef.current.get('get_comments');
  if (pending) {
    pendingActionsRef.current.delete('get_comments');
    if (data.success) {
      pending.resolve({ success: true, comments: data.comments || [] });
    } else {
      pending.reject(new Error(data.error || 'Failed to get comments'));
    }
  }
  break;
}
```

**Step 3: Add send functions**

Add before the return statement:

```typescript
const sendAddComment = useCallback((
  reviewId: string,
  filepath: string,
  lineStart: number,
  lineEnd: number,
  content: string
): Promise<AddCommentResult> => {
  return new Promise((resolve, reject) => {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) {
      reject(new Error('WebSocket not connected'));
      return;
    }

    pendingActionsRef.current.set('add_comment', { resolve, reject });

    ws.send(JSON.stringify({
      cmd: 'add_comment',
      review_id: reviewId,
      filepath,
      line_start: lineStart,
      line_end: lineEnd,
      content,
    }));

    setTimeout(() => {
      if (pendingActionsRef.current.has('add_comment')) {
        pendingActionsRef.current.delete('add_comment');
        reject(new Error('Add comment timeout'));
      }
    }, 30000);
  });
}, []);

const sendUpdateComment = useCallback((commentId: string, content: string): Promise<CommentActionResult> => {
  return new Promise((resolve, reject) => {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) {
      reject(new Error('WebSocket not connected'));
      return;
    }

    const key = `update_comment_${commentId}`;
    pendingActionsRef.current.set(key, { resolve, reject });

    ws.send(JSON.stringify({
      cmd: 'update_comment',
      comment_id: commentId,
      content,
    }));

    setTimeout(() => {
      if (pendingActionsRef.current.has(key)) {
        pendingActionsRef.current.delete(key);
        reject(new Error('Update comment timeout'));
      }
    }, 30000);
  });
}, []);

const sendResolveComment = useCallback((commentId: string, resolved: boolean): Promise<CommentActionResult> => {
  return new Promise((resolve, reject) => {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) {
      reject(new Error('WebSocket not connected'));
      return;
    }

    const key = `resolve_comment_${commentId}`;
    pendingActionsRef.current.set(key, { resolve, reject });

    ws.send(JSON.stringify({
      cmd: 'resolve_comment',
      comment_id: commentId,
      resolved,
    }));

    setTimeout(() => {
      if (pendingActionsRef.current.has(key)) {
        pendingActionsRef.current.delete(key);
        reject(new Error('Resolve comment timeout'));
      }
    }, 30000);
  });
}, []);

const sendDeleteComment = useCallback((commentId: string): Promise<CommentActionResult> => {
  return new Promise((resolve, reject) => {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) {
      reject(new Error('WebSocket not connected'));
      return;
    }

    const key = `delete_comment_${commentId}`;
    pendingActionsRef.current.set(key, { resolve, reject });

    ws.send(JSON.stringify({
      cmd: 'delete_comment',
      comment_id: commentId,
    }));

    setTimeout(() => {
      if (pendingActionsRef.current.has(key)) {
        pendingActionsRef.current.delete(key);
        reject(new Error('Delete comment timeout'));
      }
    }, 30000);
  });
}, []);

const sendGetComments = useCallback((reviewId: string, filepath?: string): Promise<GetCommentsResult> => {
  return new Promise((resolve, reject) => {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) {
      reject(new Error('WebSocket not connected'));
      return;
    }

    pendingActionsRef.current.set('get_comments', { resolve, reject });

    ws.send(JSON.stringify({
      cmd: 'get_comments',
      review_id: reviewId,
      ...(filepath && { filepath }),
    }));

    setTimeout(() => {
      if (pendingActionsRef.current.has('get_comments')) {
        pendingActionsRef.current.delete('get_comments');
        reject(new Error('Get comments timeout'));
      }
    }, 30000);
  });
}, []);
```

**Step 4: Add to return object**

Add to the return statement:

```typescript
sendAddComment,
sendUpdateComment,
sendResolveComment,
sendDeleteComment,
sendGetComments,
```

**Step 5: Verify build**

Run: `cd app && pnpm run build`

Expected: Build succeeds

**Step 6: Commit**

```bash
git add app/src/hooks/useDaemonSocket.ts
git commit -m "feat: add frontend comment hook functions"
```

---

## Task 7: Comment UI Components

**Files:**
- Create: `app/src/components/CommentPopover.tsx`
- Create: `app/src/components/CommentPopover.css`

**Step 1: Create CommentPopover component**

Create `app/src/components/CommentPopover.tsx`:

```tsx
import { useState, useRef, useEffect } from 'react';
import type { ReviewComment } from '../hooks/useDaemonSocket';
import './CommentPopover.css';

interface CommentPopoverProps {
  // For new comments
  isNew?: boolean;
  lineStart?: number;
  lineEnd?: number;
  // For existing comments
  comment?: ReviewComment;
  // Actions
  onSave: (content: string) => Promise<void>;
  onCancel: () => void;
  onResolve?: (resolved: boolean) => Promise<void>;
  onDelete?: () => Promise<void>;
  onSendToClaude?: () => void;
  // Position
  position: { top: number; left: number };
}

export function CommentPopover({
  isNew = false,
  lineStart,
  lineEnd,
  comment,
  onSave,
  onCancel,
  onResolve,
  onDelete,
  onSendToClaude,
  position,
}: CommentPopoverProps) {
  const [content, setContent] = useState(comment?.content || '');
  const [isEditing, setIsEditing] = useState(isNew);
  const [isSaving, setIsSaving] = useState(false);
  const textareaRef = useRef<HTMLTextAreaElement>(null);

  useEffect(() => {
    if (isEditing && textareaRef.current) {
      textareaRef.current.focus();
    }
  }, [isEditing]);

  const handleSave = async () => {
    if (!content.trim()) return;
    setIsSaving(true);
    try {
      await onSave(content.trim());
      if (isNew) {
        onCancel(); // Close after creating new comment
      } else {
        setIsEditing(false);
      }
    } finally {
      setIsSaving(false);
    }
  };

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Escape') {
      onCancel();
    } else if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
      e.preventDefault();
      handleSave();
    }
  };

  const formatTimestamp = (iso: string) => {
    const date = new Date(iso);
    return date.toLocaleString('en-US', {
      month: 'short',
      day: 'numeric',
      hour: 'numeric',
      minute: '2-digit',
    });
  };

  return (
    <div
      className={`comment-popover ${comment?.resolved ? 'resolved' : ''}`}
      style={{ top: position.top, left: position.left }}
      onKeyDown={handleKeyDown}
    >
      {!isNew && comment && (
        <div className="comment-header">
          <span className={`comment-author ${comment.author}`}>
            {comment.author === 'agent' ? 'Claude' : 'You'}
          </span>
          <span className="comment-time">{formatTimestamp(comment.created_at)}</span>
          {comment.resolved && <span className="comment-resolved-badge">Resolved</span>}
        </div>
      )}

      {isNew && (
        <div className="comment-header">
          <span className="comment-lines">
            Line{lineStart !== lineEnd ? `s ${lineStart}-${lineEnd}` : ` ${lineStart}`}
          </span>
        </div>
      )}

      {isEditing ? (
        <textarea
          ref={textareaRef}
          className="comment-textarea"
          value={content}
          onChange={(e) => setContent(e.target.value)}
          placeholder="Add a comment..."
          rows={3}
        />
      ) : (
        <div className="comment-content">{comment?.content}</div>
      )}

      <div className="comment-actions">
        {isEditing ? (
          <>
            <button className="comment-btn cancel" onClick={onCancel}>
              Cancel
            </button>
            <button
              className="comment-btn save"
              onClick={handleSave}
              disabled={!content.trim() || isSaving}
            >
              {isSaving ? 'Saving...' : 'Save'}
            </button>
          </>
        ) : (
          <>
            <button className="comment-btn edit" onClick={() => setIsEditing(true)}>
              Edit
            </button>
            {onSendToClaude && (
              <button className="comment-btn send" onClick={onSendToClaude}>
                Send to CC
              </button>
            )}
            {onResolve && !comment?.resolved && (
              <button
                className="comment-btn resolve"
                onClick={() => onResolve(true)}
              >
                Resolve
              </button>
            )}
            {onResolve && comment?.resolved && (
              <button
                className="comment-btn unresolve"
                onClick={() => onResolve(false)}
              >
                Unresolve
              </button>
            )}
          </>
        )}
      </div>
    </div>
  );
}
```

**Step 2: Create CommentPopover styles**

Create `app/src/components/CommentPopover.css`:

```css
.comment-popover {
  position: absolute;
  background: #2d2d2d;
  border: 1px solid #454545;
  border-radius: 6px;
  padding: 12px;
  min-width: 280px;
  max-width: 400px;
  box-shadow: 0 4px 12px rgba(0, 0, 0, 0.4);
  z-index: 100;
}

.comment-popover.resolved {
  opacity: 0.7;
}

.comment-header {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
  font-size: 12px;
}

.comment-author {
  font-weight: 600;
  padding: 2px 6px;
  border-radius: 3px;
}

.comment-author.user {
  background: #2563eb;
  color: white;
}

.comment-author.agent {
  background: #d97706;
  color: white;
}

.comment-time {
  color: #858585;
}

.comment-lines {
  color: #858585;
}

.comment-resolved-badge {
  background: #22c55e;
  color: white;
  padding: 2px 6px;
  border-radius: 3px;
  font-size: 10px;
  font-weight: 600;
  text-transform: uppercase;
}

.comment-textarea {
  width: 100%;
  background: #1e1e1e;
  border: 1px solid #3c3c3c;
  border-radius: 4px;
  color: #e0e0e0;
  padding: 8px;
  font-size: 13px;
  font-family: inherit;
  resize: vertical;
  min-height: 60px;
}

.comment-textarea:focus {
  outline: none;
  border-color: #2563eb;
}

.comment-content {
  color: #e0e0e0;
  font-size: 13px;
  line-height: 1.5;
  white-space: pre-wrap;
  word-break: break-word;
}

.comment-actions {
  display: flex;
  gap: 8px;
  margin-top: 12px;
  justify-content: flex-end;
}

.comment-btn {
  padding: 4px 10px;
  border-radius: 4px;
  font-size: 12px;
  cursor: pointer;
  border: 1px solid transparent;
  transition: all 0.15s ease;
}

.comment-btn.cancel {
  background: transparent;
  border-color: #454545;
  color: #858585;
}

.comment-btn.cancel:hover {
  background: #3c3c3c;
  color: #e0e0e0;
}

.comment-btn.save {
  background: #2563eb;
  color: white;
}

.comment-btn.save:hover:not(:disabled) {
  background: #1d4ed8;
}

.comment-btn.save:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.comment-btn.edit,
.comment-btn.send {
  background: transparent;
  border-color: #454545;
  color: #e0e0e0;
}

.comment-btn.edit:hover,
.comment-btn.send:hover {
  background: #3c3c3c;
}

.comment-btn.resolve {
  background: #22c55e;
  color: white;
}

.comment-btn.resolve:hover {
  background: #16a34a;
}

.comment-btn.unresolve {
  background: transparent;
  border-color: #454545;
  color: #858585;
}

.comment-btn.unresolve:hover {
  background: #3c3c3c;
  color: #e0e0e0;
}
```

**Step 3: Verify build**

Run: `cd app && pnpm run build`

Expected: Build succeeds

**Step 4: Commit**

```bash
git add app/src/components/CommentPopover.tsx app/src/components/CommentPopover.css
git commit -m "feat: add CommentPopover component"
```

---

## Task 8: Integrate Comments into ReviewPanel

**Files:**
- Modify: `app/src/components/ReviewPanel.tsx`
- Modify: `app/src/components/ReviewPanel.css`

**Step 1: Add comment state and props**

Add to props interface:

```typescript
addComment: (reviewId: string, filepath: string, lineStart: number, lineEnd: number, content: string) => Promise<{ success: boolean; comment?: ReviewComment }>;
updateComment: (commentId: string, content: string) => Promise<{ success: boolean }>;
resolveComment: (commentId: string, resolved: boolean) => Promise<{ success: boolean }>;
deleteComment: (commentId: string) => Promise<{ success: boolean }>;
getComments: (reviewId: string, filepath?: string) => Promise<{ success: boolean; comments?: ReviewComment[] }>;
```

Add state:

```typescript
const [comments, setComments] = useState<ReviewComment[]>([]);
const [activePopover, setActivePopover] = useState<{
  type: 'new' | 'existing';
  lineStart?: number;
  lineEnd?: number;
  comment?: ReviewComment;
  position: { top: number; left: number };
} | null>(null);
```

**Step 2: Add comment loading effect**

Add effect to load comments when file changes:

```typescript
// Load comments for current file
useEffect(() => {
  if (!reviewId || !selectedFilePath) {
    setComments([]);
    return;
  }

  getComments(reviewId, selectedFilePath)
    .then((result) => {
      if (result.success && result.comments) {
        setComments(result.comments);
      }
    })
    .catch(console.error);
}, [reviewId, selectedFilePath, getComments]);
```

**Step 3: Add comment gutter markers to CodeMirror**

In the CodeMirror effect, add gutter extension for comment markers. This requires creating a custom gutter that shows 💬 on lines with comments.

**Step 4: Add popover rendering**

Add before the closing of the component:

```tsx
{activePopover && (
  <CommentPopover
    isNew={activePopover.type === 'new'}
    lineStart={activePopover.lineStart}
    lineEnd={activePopover.lineEnd}
    comment={activePopover.comment}
    position={activePopover.position}
    onSave={async (content) => {
      if (activePopover.type === 'new' && reviewId && selectedFilePath) {
        const result = await addComment(
          reviewId,
          selectedFilePath,
          activePopover.lineStart!,
          activePopover.lineEnd!,
          content
        );
        if (result.success && result.comment) {
          setComments(prev => [...prev, result.comment!]);
        }
      } else if (activePopover.comment) {
        await updateComment(activePopover.comment.id, content);
        setComments(prev => prev.map(c =>
          c.id === activePopover.comment!.id ? { ...c, content } : c
        ));
      }
    }}
    onCancel={() => setActivePopover(null)}
    onResolve={activePopover.comment ? async (resolved) => {
      await resolveComment(activePopover.comment!.id, resolved);
      setComments(prev => prev.map(c =>
        c.id === activePopover.comment!.id ? { ...c, resolved } : c
      ));
      setActivePopover(null);
    } : undefined}
    onDelete={activePopover.comment ? async () => {
      await deleteComment(activePopover.comment!.id);
      setComments(prev => prev.filter(c => c.id !== activePopover.comment!.id));
      setActivePopover(null);
    } : undefined}
    onSendToClaude={() => {
      // Copy to clipboard for pasting into Claude Code
      const comment = activePopover.comment;
      if (comment) {
        const text = `${selectedFilePath}:${comment.line_start}-${comment.line_end}\n\n${comment.content}`;
        navigator.clipboard.writeText(text);
      }
      setActivePopover(null);
    }}
  />
)}
```

**Step 5: Add comment count to file list**

Update file item to show comment count badge:

```tsx
{fileCommentCounts[file.path] > 0 && (
  <span className="file-comment-count">{fileCommentCounts[file.path]}</span>
)}
```

**Step 6: Add CSS for comment count badge**

Add to ReviewPanel.css:

```css
.file-comment-count {
  background: #2563eb;
  color: white;
  font-size: 10px;
  font-weight: 600;
  padding: 1px 5px;
  border-radius: 10px;
  min-width: 16px;
  text-align: center;
}
```

**Step 7: Verify build**

Run: `cd app && pnpm run build`

Expected: Build succeeds

**Step 8: Commit**

```bash
git add app/src/components/ReviewPanel.tsx app/src/components/ReviewPanel.css
git commit -m "feat: integrate comments into ReviewPanel"
```

---

## Task 9: Wire Up Comments in App.tsx

**Files:**
- Modify: `app/src/App.tsx`

**Step 1: Pass comment functions to ReviewPanel**

Add the comment functions from useDaemonSocket to ReviewPanel props:

```tsx
<ReviewPanel
  // ... existing props
  addComment={sendAddComment}
  updateComment={sendUpdateComment}
  resolveComment={sendResolveComment}
  deleteComment={sendDeleteComment}
  getComments={sendGetComments}
/>
```

**Step 2: Verify build**

Run: `cd app && pnpm run build`

Expected: Build succeeds

**Step 3: Commit**

```bash
git add app/src/App.tsx
git commit -m "feat: wire up comment functions in App"
```

---

## Task 10: End-to-End Testing

**Step 1: Manual verification checklist**

1. Open review panel on a branch with changes
2. Click line number in diff to add comment
3. Type comment content and save
4. Verify comment appears with 💬 marker
5. Click comment marker to view/edit
6. Resolve comment, verify it dims
7. Close and reopen review - verify comments persist
8. Verify comment count badge in file list

**Step 2: Run all tests**

Run: `make test-all`

Expected: All tests pass

**Step 3: Final commit**

```bash
git add -A
git commit -m "feat: complete Phase 2 review comments implementation"
```

---

## Summary

This plan implements:
- SQLite storage for review comments (migration 14)
- Store CRUD operations with tests
- Protocol types via TypeSpec
- WebSocket handlers for comment operations
- Frontend hook functions
- CommentPopover UI component
- Integration into ReviewPanel with gutter markers

**Verification:** Comments can be added, edited, resolved, and persisted. They survive app restart and display correctly in the diff viewer.
