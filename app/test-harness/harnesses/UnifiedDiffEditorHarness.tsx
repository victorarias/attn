/**
 * UnifiedDiffEditor Test Harness
 *
 * Tests the unified diff approach where deleted lines are part of the document.
 * Includes controls for font size, language, and context lines.
 */
import { useState, useEffect, useCallback } from 'react';
import { UnifiedDiffEditor, InlineComment, calculateHunks } from '../../src/components/UnifiedDiffEditor';
import type { HarnessProps } from '../types';

// Sample diff content - small for basic tests
const SMALL_ORIGINAL = `function example() {
  console.log('line 1');
  console.log('line 2 - will be deleted');
  console.log('line 3 - will be deleted');
  console.log('line 4');
  console.log('line 5');
}`;

const SMALL_MODIFIED = `function example() {
  console.log('line 1');
  console.log('line 4');
  console.log('new line - added');
  console.log('line 5');
}`;

// Larger sample to demonstrate hunks/context mode
const LARGE_ORIGINAL = `/**
 * User authentication module
 * Handles login, logout, and session management
 */

import { hash, verify } from './crypto';
import { db } from './database';
import { logger } from './logging';

const SESSION_DURATION = 3600000; // 1 hour

export interface User {
  id: string;
  email: string;
  name: string;
  createdAt: Date;
}

export interface Session {
  token: string;
  userId: string;
  expiresAt: Date;
}

export async function login(email: string, password: string): Promise<Session | null> {
  const user = await db.users.findByEmail(email);
  if (!user) {
    logger.warn('Login attempt for non-existent user', { email });
    return null;
  }

  const isValid = await verify(password, user.passwordHash);
  if (!isValid) {
    logger.warn('Invalid password attempt', { userId: user.id });
    return null;
  }

  const session = await createSession(user.id);
  logger.info('User logged in', { userId: user.id });
  return session;
}

export async function logout(token: string): Promise<void> {
  await db.sessions.delete(token);
  logger.info('Session terminated', { token: token.slice(0, 8) });
}

async function createSession(userId: string): Promise<Session> {
  const token = generateToken();
  const expiresAt = new Date(Date.now() + SESSION_DURATION);

  await db.sessions.create({ token, userId, expiresAt });
  return { token, userId, expiresAt };
}

function generateToken(): string {
  return crypto.randomUUID();
}`;

const LARGE_MODIFIED = `/**
 * User authentication module
 * Handles login, logout, and session management
 */

import { hash, verify } from './crypto';
import { db } from './database';
import { logger } from './logging';
import { sendEmail } from './notifications';

const SESSION_DURATION = 7200000; // 2 hours (extended)
const MAX_LOGIN_ATTEMPTS = 5;

export interface User {
  id: string;
  email: string;
  name: string;
  createdAt: Date;
  failedAttempts: number;
}

export interface Session {
  token: string;
  userId: string;
  expiresAt: Date;
}

export async function login(email: string, password: string): Promise<Session | null> {
  const user = await db.users.findByEmail(email);
  if (!user) {
    logger.warn('Login attempt for non-existent user', { email });
    return null;
  }

  // Check for too many failed attempts
  if (user.failedAttempts >= MAX_LOGIN_ATTEMPTS) {
    logger.warn('Account locked due to too many attempts', { userId: user.id });
    await sendEmail(user.email, 'Account Locked', 'Too many failed login attempts.');
    return null;
  }

  const isValid = await verify(password, user.passwordHash);
  if (!isValid) {
    await db.users.incrementFailedAttempts(user.id);
    logger.warn('Invalid password attempt', { userId: user.id });
    return null;
  }

  // Reset failed attempts on successful login
  await db.users.resetFailedAttempts(user.id);
  const session = await createSession(user.id);
  logger.info('User logged in', { userId: user.id });
  return session;
}

export async function logout(token: string): Promise<void> {
  await db.sessions.delete(token);
  logger.info('Session terminated', { token: token.slice(0, 8) });
}

async function createSession(userId: string): Promise<Session> {
  const token = generateToken();
  const expiresAt = new Date(Date.now() + SESSION_DURATION);

  await db.sessions.create({ token, userId, expiresAt });
  return { token, userId, expiresAt };
}

function generateToken(): string {
  return crypto.randomUUID();
}`;

const LANGUAGES = [
  { value: '', label: 'None' },
  { value: 'javascript', label: 'JavaScript' },
  { value: 'typescript', label: 'TypeScript' },
  { value: 'python', label: 'Python' },
  { value: 'go', label: 'Go' },
  { value: 'rust', label: 'Rust' },
  { value: 'java', label: 'Java' },
  { value: 'sql', label: 'SQL' },
  { value: 'json', label: 'JSON' },
  { value: 'yaml', label: 'YAML' },
  { value: 'markdown', label: 'Markdown' },
  { value: 'css', label: 'CSS' },
  { value: 'html', label: 'HTML' },
];

export function UnifiedDiffEditorHarness({ onReady }: HarnessProps) {
  const [comments, setComments] = useState<InlineComment[]>([]);
  const [editingCommentId, setEditingCommentId] = useState<string | null>(null);

  // Configurable options
  const [fontSize, setFontSize] = useState(13);
  const [language, setLanguage] = useState('typescript');
  const [contextLines, setContextLines] = useState(0); // 0 = full diff
  const [useLargeDiff, setUseLargeDiff] = useState(false);

  const original = useLargeDiff ? LARGE_ORIGINAL : SMALL_ORIGINAL;
  const modified = useLargeDiff ? LARGE_MODIFIED : SMALL_MODIFIED;

  // Mock addComment
  const addComment = useCallback(async (docLine: number, content: string, anchor: import('../../src/components/UnifiedDiffEditor').CommentAnchor) => {
    window.__HARNESS__.recordCall('addComment', [docLine, content, anchor]);
    const newComment: InlineComment = {
      id: `comment-${Date.now()}`,
      docLine,
      content,
      resolved: false,
      author: 'user',
      anchor,
    };
    setComments((prev) => [...prev, newComment]);
  }, []);

  // Mock editComment
  const editComment = useCallback(async (id: string, content: string) => {
    window.__HARNESS__.recordCall('editComment', [id, content]);
    setComments((prev) =>
      prev.map((c) => (c.id === id ? { ...c, content } : c))
    );
    setEditingCommentId(null);
  }, []);

  // Start editing
  const startEdit = useCallback((id: string) => {
    window.__HARNESS__.recordCall('startEdit', [id]);
    setEditingCommentId(id);
  }, []);

  // Cancel editing
  const cancelEdit = useCallback(() => {
    window.__HARNESS__.recordCall('cancelEdit', []);
    setEditingCommentId(null);
  }, []);

  // Mock resolveComment - clears wontFix when resolving (mutual exclusivity)
  const resolveComment = useCallback(async (id: string, resolved: boolean) => {
    window.__HARNESS__.recordCall('resolveComment', [id, resolved]);
    setComments((prev) =>
      prev.map((c) => (c.id === id ? {
        ...c,
        resolved,
        resolvedBy: resolved ? 'user' : undefined,
        // Clear won't fix when resolving
        wontFix: resolved ? false : c.wontFix,
        wontFixBy: resolved ? undefined : c.wontFixBy,
      } : c))
    );
  }, []);

  // Mock wontFixComment - clears resolved when marking won't fix (mutual exclusivity)
  const wontFixComment = useCallback(async (id: string, wontFix: boolean) => {
    window.__HARNESS__.recordCall('wontFixComment', [id, wontFix]);
    setComments((prev) =>
      prev.map((c) => (c.id === id ? {
        ...c,
        wontFix,
        wontFixBy: wontFix ? 'user' : undefined,
        // Clear resolved when marking won't fix
        resolved: wontFix ? false : c.resolved,
        resolvedBy: wontFix ? undefined : c.resolvedBy,
      } : c))
    );
  }, []);

  // Mock deleteComment
  const deleteComment = useCallback(async (id: string) => {
    window.__HARNESS__.recordCall('deleteComment', [id]);
    setComments((prev) => prev.filter((c) => c.id !== id));
  }, []);

  useEffect(() => {
    // Give editor time to initialize
    const timer = setTimeout(() => onReady(), 300);
    return () => clearTimeout(timer);
  }, [onReady]);

  const controlsStyle: React.CSSProperties = {
    display: 'flex',
    gap: '16px',
    padding: '12px',
    backgroundColor: '#1e1e1e',
    borderBottom: '1px solid #3e4451',
    alignItems: 'center',
    flexWrap: 'wrap',
  };

  const labelStyle: React.CSSProperties = {
    display: 'flex',
    alignItems: 'center',
    gap: '8px',
    color: '#abb2bf',
    fontSize: '13px',
  };

  const selectStyle: React.CSSProperties = {
    padding: '4px 8px',
    backgroundColor: '#2d3748',
    border: '1px solid #4b5563',
    borderRadius: '4px',
    color: '#e5e7eb',
    fontSize: '13px',
  };

  const inputStyle: React.CSSProperties = {
    ...selectStyle,
    width: '60px',
  };

  const checkboxStyle: React.CSSProperties = {
    accentColor: '#3b82f6',
  };

  return (
    <div style={{ height: '100%', display: 'flex', flexDirection: 'column' }}>
      <div style={controlsStyle}>
        <label style={labelStyle}>
          Font Size:
          <input
            type="number"
            min={10}
            max={24}
            value={fontSize}
            onChange={(e) => setFontSize(Number(e.target.value))}
            style={inputStyle}
          />
        </label>

        <label style={labelStyle}>
          Language:
          <select
            value={language}
            onChange={(e) => setLanguage(e.target.value)}
            style={selectStyle}
          >
            {LANGUAGES.map((lang) => (
              <option key={lang.value} value={lang.value}>
                {lang.label}
              </option>
            ))}
          </select>
        </label>

        <label style={labelStyle}>
          Context Lines:
          <input
            type="number"
            min={0}
            max={10}
            value={contextLines}
            onChange={(e) => setContextLines(Number(e.target.value))}
            style={inputStyle}
            title="0 = show full diff"
          />
          <span style={{ color: '#6b7280', fontSize: '11px' }}>(0 = full)</span>
        </label>

        <label style={labelStyle}>
          <input
            type="checkbox"
            checked={useLargeDiff}
            onChange={(e) => {
              setUseLargeDiff(e.target.checked);
              setComments([]); // Clear comments when switching
            }}
            style={checkboxStyle}
          />
          Large Diff (for hunks demo)
        </label>
      </div>

      <div style={{ flex: 1, minHeight: 0 }}>
        <UnifiedDiffEditor
          original={original}
          modified={modified}
          comments={comments}
          editingCommentId={editingCommentId}
          fontSize={fontSize}
          language={language || undefined}
          contextLines={contextLines}
          onAddComment={addComment}
          onEditComment={editComment}
          onStartEdit={startEdit}
          onCancelEdit={cancelEdit}
          onResolveComment={resolveComment}
          onWontFixComment={wontFixComment}
          onDeleteComment={deleteComment}
        />
      </div>
    </div>
  );
}
