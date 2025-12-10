# Attn UX Redesign Implementation Plan

**Status:** Complete

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Transform attn from a sidebar-based UI to a dashboard + drawer layout with keyboard shortcuts and custom location picker.

**Architecture:** App has two views: Dashboard (home) and Session (terminal). Attention items show in a slide-out drawer when in session view. Global keyboard shortcuts work everywhere. Custom location picker replaces native file dialog.

**Tech Stack:** React, TypeScript, Zustand, Tauri, xterm.js, CSS

---

## Task 1: Add View State to App

**Files:**
- Modify: `app/src/App.tsx`
- Modify: `app/src/App.css`

**Step 1: Add view state**

In `App.tsx`, add state for current view:

```typescript
// After other useState/store hooks, add:
const [view, setView] = useState<'dashboard' | 'session'>('dashboard');

// When activeSessionId changes, update view:
useEffect(() => {
  if (activeSessionId) {
    setView('session');
  }
}, [activeSessionId]);

// Add function to go to dashboard:
const goToDashboard = useCallback(() => {
  setActiveSession(null);
  setView('dashboard');
}, [setActiveSession]);
```

**Step 2: Update imports**

```typescript
import { useCallback, useEffect, useRef, useState } from 'react';
```

**Step 3: Verify app still compiles**

Run: `cd app && pnpm tauri dev`
Expected: App launches without errors

**Step 4: Commit**

```bash
git add app/src/App.tsx
git commit -m "feat(app): add view state for dashboard/session modes"
```

---

## Task 2: Create Dashboard Component

**Files:**
- Create: `app/src/components/Dashboard.tsx`
- Create: `app/src/components/Dashboard.css`

**Step 1: Create Dashboard component**

```typescript
// app/src/components/Dashboard.tsx
import { DaemonSession, DaemonPR } from '../hooks/useDaemonSocket';
import './Dashboard.css';

interface DashboardProps {
  sessions: Array<{
    id: string;
    label: string;
    state: 'working' | 'waiting';
    cwd: string;
  }>;
  daemonSessions: DaemonSession[];
  prs: DaemonPR[];
  onSelectSession: (id: string) => void;
  onNewSession: () => void;
}

export function Dashboard({
  sessions,
  daemonSessions,
  prs,
  onSelectSession,
  onNewSession,
}: DashboardProps) {
  const waitingSessions = sessions.filter((s) => s.state === 'waiting');
  const workingSessions = sessions.filter((s) => s.state === 'working');

  return (
    <div className="dashboard">
      <header className="dashboard-header">
        <h1>attn</h1>
        <span className="dashboard-subtitle">attention hub</span>
      </header>

      <div className="dashboard-grid">
        {/* Sessions Card */}
        <div className="dashboard-card">
          <div className="card-header">
            <h2>Sessions</h2>
            <button className="card-action" onClick={onNewSession}>
              + New
            </button>
          </div>
          <div className="card-body">
            {sessions.length === 0 ? (
              <div className="card-empty">No active sessions</div>
            ) : (
              <>
                {waitingSessions.length > 0 && (
                  <div className="session-group">
                    <div className="group-label">Waiting for input</div>
                    {waitingSessions.map((s) => (
                      <div
                        key={s.id}
                        className="session-row clickable"
                        onClick={() => onSelectSession(s.id)}
                      >
                        <span className="state-dot waiting" />
                        <span className="session-name">{s.label}</span>
                      </div>
                    ))}
                  </div>
                )}
                {workingSessions.length > 0 && (
                  <div className="session-group">
                    <div className="group-label">Working</div>
                    {workingSessions.map((s) => (
                      <div
                        key={s.id}
                        className="session-row clickable"
                        onClick={() => onSelectSession(s.id)}
                      >
                        <span className="state-dot working" />
                        <span className="session-name">{s.label}</span>
                      </div>
                    ))}
                  </div>
                )}
              </>
            )}
          </div>
        </div>

        {/* PRs Card - placeholder for now */}
        <div className="dashboard-card">
          <div className="card-header">
            <h2>Pull Requests</h2>
            <span className="card-count">{prs.length}</span>
          </div>
          <div className="card-body">
            {prs.length === 0 ? (
              <div className="card-empty">No PRs need attention</div>
            ) : (
              <div className="card-empty">PR list coming soon</div>
            )}
          </div>
        </div>
      </div>

      <footer className="dashboard-footer">
        <span className="shortcut"><kbd>⌘N</kbd> new session</span>
        <span className="shortcut"><kbd>⌘1-9</kbd> switch session</span>
      </footer>
    </div>
  );
}
```

**Step 2: Create Dashboard styles**

```css
/* app/src/components/Dashboard.css */
.dashboard {
  height: 100%;
  width: 100%;
  background: #0a0a0b;
  display: flex;
  flex-direction: column;
  padding: 40px;
}

.dashboard-header {
  margin-bottom: 40px;
}

.dashboard-header h1 {
  font-family: 'JetBrains Mono', monospace;
  font-size: 24px;
  font-weight: 700;
  color: #e8e8e8;
  margin: 0;
}

.dashboard-subtitle {
  font-size: 12px;
  color: #555;
  letter-spacing: 0.1em;
  text-transform: uppercase;
}

.dashboard-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(300px, 1fr));
  gap: 20px;
  flex: 1;
  overflow-y: auto;
}

.dashboard-card {
  background: #111113;
  border: 1px solid #2a2a2d;
  border-radius: 8px;
  display: flex;
  flex-direction: column;
  max-height: 400px;
}

.card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 16px;
  border-bottom: 1px solid #2a2a2d;
}

.card-header h2 {
  font-family: 'JetBrains Mono', monospace;
  font-size: 11px;
  font-weight: 600;
  letter-spacing: 0.1em;
  text-transform: uppercase;
  color: #555;
  margin: 0;
}

.card-action {
  background: #1a1a1d;
  border: 1px solid #2a2a2d;
  border-radius: 4px;
  color: #888;
  font-size: 12px;
  padding: 4px 10px;
  cursor: pointer;
}

.card-action:hover {
  background: #2a2a2d;
  color: #e8e8e8;
}

.card-count {
  background: #ff6b35;
  color: #000;
  font-size: 11px;
  font-weight: 700;
  padding: 2px 8px;
  border-radius: 10px;
}

.card-body {
  padding: 12px;
  overflow-y: auto;
  flex: 1;
}

.card-empty {
  color: #555;
  font-size: 13px;
  text-align: center;
  padding: 20px;
}

.session-group {
  margin-bottom: 16px;
}

.group-label {
  font-size: 10px;
  color: #555;
  text-transform: uppercase;
  letter-spacing: 0.08em;
  margin-bottom: 8px;
  padding: 0 8px;
}

.session-row {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 12px;
  border-radius: 6px;
  font-size: 13px;
  color: #e8e8e8;
}

.session-row.clickable {
  cursor: pointer;
}

.session-row.clickable:hover {
  background: #1a1a1d;
}

.state-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  flex-shrink: 0;
}

.state-dot.waiting {
  background: #ff6b35;
}

.state-dot.working {
  background: #22c55e;
}

.session-name {
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.dashboard-footer {
  display: flex;
  gap: 24px;
  padding-top: 20px;
  border-top: 1px solid #2a2a2d;
  margin-top: 20px;
}

.shortcut {
  font-size: 12px;
  color: #555;
}

.shortcut kbd {
  background: #1a1a1d;
  border: 1px solid #2a2a2d;
  border-radius: 3px;
  padding: 2px 6px;
  font-family: 'JetBrains Mono', monospace;
  font-size: 11px;
  margin-right: 6px;
}
```

**Step 3: Verify component renders**

Import in App.tsx temporarily to verify:
```typescript
import { Dashboard } from './components/Dashboard';
// In render, add: <Dashboard ... /> (will integrate in next task)
```

Run: `cd app && pnpm tauri dev`
Expected: No TypeScript errors

**Step 4: Commit**

```bash
git add app/src/components/Dashboard.tsx app/src/components/Dashboard.css
git commit -m "feat(app): create Dashboard component"
```

---

## Task 3: Integrate Dashboard View

**Files:**
- Modify: `app/src/App.tsx`
- Modify: `app/src/App.css`

**Step 1: Import Dashboard**

```typescript
import { Dashboard } from './components/Dashboard';
```

**Step 2: Update render to switch views**

Replace the current return statement with:

```typescript
return (
  <div className="app">
    {view === 'dashboard' ? (
      <Dashboard
        sessions={enrichedLocalSessions}
        daemonSessions={externalDaemonSessions}
        prs={prs}
        onSelectSession={handleSelectSession}
        onNewSession={handleNewSession}
      />
    ) : (
      <>
        <Sidebar
          localSessions={enrichedLocalSessions}
          selectedId={activeSessionId}
          onSelectSession={handleSelectSession}
          onNewSession={handleNewSession}
          onCloseSession={handleCloseSession}
          daemonSessions={externalDaemonSessions}
          prs={prs}
          isConnected={isConnected}
        />
        <div className="terminal-pane">
          {sessions.map((session) => (
            <div
              key={session.id}
              className={`terminal-wrapper ${session.id === activeSessionId ? 'active' : ''}`}
            >
              <Terminal
                ref={setTerminalRef(session.id)}
                onReady={handleTerminalReady(session.id)}
                onResize={handleResize(session.id)}
              />
            </div>
          ))}
        </div>
      </>
    )}
  </div>
);
```

**Step 3: Update App.css for full-width dashboard**

Add to App.css:

```css
.app {
  height: 100%;
  width: 100%;
  display: flex;
  background: #0a0a0b;
}
```

**Step 4: Test view switching**

Run: `cd app && pnpm tauri dev`
Expected:
- App starts on dashboard
- Clicking a session switches to terminal view
- Creating new session switches to terminal view

**Step 5: Commit**

```bash
git add app/src/App.tsx app/src/App.css
git commit -m "feat(app): integrate dashboard view with view switching"
```

---

## Task 4: Create Attention Drawer Component

**Files:**
- Create: `app/src/components/AttentionDrawer.tsx`
- Create: `app/src/components/AttentionDrawer.css`

**Step 1: Create AttentionDrawer component**

```typescript
// app/src/components/AttentionDrawer.tsx
import { DaemonSession, DaemonPR } from '../hooks/useDaemonSocket';
import './AttentionDrawer.css';

interface AttentionDrawerProps {
  isOpen: boolean;
  onClose: () => void;
  waitingSessions: Array<{
    id: string;
    label: string;
    state: 'working' | 'waiting';
  }>;
  daemonSessions: DaemonSession[];
  prs: DaemonPR[];
  onSelectSession: (id: string) => void;
}

export function AttentionDrawer({
  isOpen,
  onClose,
  waitingSessions,
  daemonSessions,
  prs,
  onSelectSession,
}: AttentionDrawerProps) {
  const waitingDaemonSessions = daemonSessions.filter((s) => s.state === 'waiting');
  const reviewPRs = prs.filter((p) => p.role === 'reviewer' && !p.muted);
  const authorPRs = prs.filter((p) => p.role === 'author' && !p.muted);

  const totalItems = waitingSessions.length + waitingDaemonSessions.length + reviewPRs.length + authorPRs.length;

  return (
    <div className={`attention-drawer ${isOpen ? 'open' : ''}`}>
      <div className="drawer-header">
        <span className="drawer-title">Needs Attention</span>
        <span className="drawer-count">{totalItems}</span>
        <button className="drawer-close" onClick={onClose}>×</button>
      </div>

      <div className="drawer-body">
        {/* Waiting Sessions (local) */}
        {waitingSessions.length > 0 && (
          <div className="drawer-section">
            <div className="section-title">
              Sessions Waiting
              <span className="section-count">{waitingSessions.length}</span>
            </div>
            {waitingSessions.map((s) => (
              <div
                key={s.id}
                className="attention-item clickable"
                onClick={() => onSelectSession(s.id)}
              >
                <span className="item-dot session" />
                <span className="item-name">{s.label}</span>
              </div>
            ))}
          </div>
        )}

        {/* Waiting Sessions (external) */}
        {waitingDaemonSessions.length > 0 && (
          <div className="drawer-section">
            <div className="section-title">
              Other Sessions Waiting
              <span className="section-count">{waitingDaemonSessions.length}</span>
            </div>
            {waitingDaemonSessions.map((s) => (
              <div key={s.id} className="attention-item">
                <span className="item-dot session" />
                <span className="item-name">{s.label}</span>
              </div>
            ))}
          </div>
        )}

        {/* PRs - Review Requested */}
        {reviewPRs.length > 0 && (
          <div className="drawer-section">
            <div className="section-title">
              Review Requested
              <span className="section-count">{reviewPRs.length}</span>
            </div>
            {reviewPRs.map((pr) => (
              <a
                key={pr.id}
                href={pr.url}
                target="_blank"
                rel="noopener noreferrer"
                className="attention-item clickable"
              >
                <span className="item-dot pr" />
                <span className="item-name">
                  {pr.repo.split('/')[1]} #{pr.number}
                </span>
              </a>
            ))}
          </div>
        )}

        {/* PRs - Your PRs */}
        {authorPRs.length > 0 && (
          <div className="drawer-section">
            <div className="section-title">
              Your PRs
              <span className="section-count">{authorPRs.length}</span>
            </div>
            {authorPRs.map((pr) => (
              <a
                key={pr.id}
                href={pr.url}
                target="_blank"
                rel="noopener noreferrer"
                className="attention-item clickable"
              >
                <span className="item-dot pr" />
                <span className="item-name">
                  {pr.repo.split('/')[1]} #{pr.number}
                </span>
                <span className="item-reason">{pr.reason.replace(/_/g, ' ')}</span>
              </a>
            ))}
          </div>
        )}

        {totalItems === 0 && (
          <div className="drawer-empty">Nothing needs attention</div>
        )}
      </div>

      <div className="drawer-footer">
        <span className="shortcut"><kbd>⌘K</kbd> toggle</span>
        <span className="shortcut"><kbd>Esc</kbd> close</span>
      </div>
    </div>
  );
}
```

**Step 2: Create AttentionDrawer styles**

```css
/* app/src/components/AttentionDrawer.css */
.attention-drawer {
  position: fixed;
  top: 0;
  right: -320px;
  bottom: 0;
  width: 320px;
  background: #111113;
  border-left: 1px solid #2a2a2d;
  display: flex;
  flex-direction: column;
  transition: right 0.2s ease-out;
  z-index: 100;
  box-shadow: -8px 0 32px rgba(0, 0, 0, 0.4);
}

.attention-drawer.open {
  right: 0;
}

.drawer-header {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 16px;
  border-bottom: 1px solid #2a2a2d;
}

.drawer-title {
  font-family: 'JetBrains Mono', monospace;
  font-size: 11px;
  font-weight: 600;
  letter-spacing: 0.1em;
  text-transform: uppercase;
  color: #555;
}

.drawer-count {
  background: #ff6b35;
  color: #000;
  font-size: 10px;
  font-weight: 700;
  padding: 2px 6px;
  border-radius: 8px;
}

.drawer-close {
  margin-left: auto;
  background: none;
  border: none;
  color: #555;
  font-size: 20px;
  cursor: pointer;
  padding: 0;
  line-height: 1;
}

.drawer-close:hover {
  color: #e8e8e8;
}

.drawer-body {
  flex: 1;
  overflow-y: auto;
  padding: 8px;
}

.drawer-section {
  margin-bottom: 16px;
}

.section-title {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 10px;
  font-weight: 600;
  letter-spacing: 0.08em;
  text-transform: uppercase;
  color: #555;
  padding: 8px 8px 4px;
}

.section-count {
  background: rgba(255, 107, 53, 0.2);
  color: #ff6b35;
  font-size: 10px;
  padding: 1px 5px;
  border-radius: 6px;
}

.attention-item {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 12px;
  border-radius: 6px;
  font-size: 13px;
  color: #e8e8e8;
  text-decoration: none;
}

.attention-item.clickable {
  cursor: pointer;
}

.attention-item.clickable:hover {
  background: #1a1a1d;
}

.item-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  flex-shrink: 0;
}

.item-dot.session {
  background: #ff6b35;
}

.item-dot.pr {
  background: #a78bfa;
}

.item-name {
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.item-reason {
  font-size: 10px;
  color: #555;
  background: #1a1a1d;
  padding: 2px 6px;
  border-radius: 4px;
  flex-shrink: 0;
}

.drawer-empty {
  color: #555;
  font-size: 13px;
  text-align: center;
  padding: 40px 20px;
}

.drawer-footer {
  display: flex;
  gap: 16px;
  padding: 12px 16px;
  border-top: 1px solid #2a2a2d;
}

.drawer-footer .shortcut {
  font-size: 11px;
  color: #555;
}

.drawer-footer kbd {
  background: #1a1a1d;
  border: 1px solid #2a2a2d;
  border-radius: 3px;
  padding: 2px 5px;
  font-family: 'JetBrains Mono', monospace;
  font-size: 10px;
  margin-right: 4px;
}
```

**Step 3: Commit**

```bash
git add app/src/components/AttentionDrawer.tsx app/src/components/AttentionDrawer.css
git commit -m "feat(app): create AttentionDrawer component"
```

---

## Task 5: Add Drawer Trigger Badge

**Files:**
- Create: `app/src/components/DrawerTrigger.tsx`
- Create: `app/src/components/DrawerTrigger.css`

**Step 1: Create DrawerTrigger component**

```typescript
// app/src/components/DrawerTrigger.tsx
import './DrawerTrigger.css';

interface DrawerTriggerProps {
  count: number;
  onClick: () => void;
}

export function DrawerTrigger({ count, onClick }: DrawerTriggerProps) {
  if (count === 0) return null;

  return (
    <button className="drawer-trigger" onClick={onClick}>
      <span className="trigger-count">{count}</span>
      <span className="trigger-label">need attention</span>
    </button>
  );
}
```

**Step 2: Create DrawerTrigger styles**

```css
/* app/src/components/DrawerTrigger.css */
.drawer-trigger {
  position: fixed;
  top: 12px;
  right: 12px;
  z-index: 50;
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 14px;
  background: rgba(255, 107, 53, 0.15);
  border: 1px solid rgba(255, 107, 53, 0.3);
  border-radius: 6px;
  cursor: pointer;
  transition: all 0.15s ease;
}

.drawer-trigger:hover {
  background: rgba(255, 107, 53, 0.25);
  border-color: rgba(255, 107, 53, 0.5);
}

.trigger-count {
  font-family: 'JetBrains Mono', monospace;
  font-size: 13px;
  font-weight: 600;
  color: #ff6b35;
}

.trigger-label {
  font-size: 12px;
  color: #888;
}
```

**Step 3: Commit**

```bash
git add app/src/components/DrawerTrigger.tsx app/src/components/DrawerTrigger.css
git commit -m "feat(app): create DrawerTrigger badge component"
```

---

## Task 6: Integrate Drawer into Session View

**Files:**
- Modify: `app/src/App.tsx`

**Step 1: Add drawer state and imports**

```typescript
import { AttentionDrawer } from './components/AttentionDrawer';
import { DrawerTrigger } from './components/DrawerTrigger';

// In App component, add state:
const [drawerOpen, setDrawerOpen] = useState(false);

const toggleDrawer = useCallback(() => {
  setDrawerOpen((prev) => !prev);
}, []);

const closeDrawer = useCallback(() => {
  setDrawerOpen(false);
}, []);
```

**Step 2: Calculate attention count**

```typescript
// Before return statement:
const waitingLocalSessions = enrichedLocalSessions.filter((s) => s.state === 'waiting');
const waitingExternalSessions = externalDaemonSessions.filter((s) => s.state === 'waiting');
const activePRs = prs.filter((p) => !p.muted);
const attentionCount = waitingLocalSessions.length + waitingExternalSessions.length + activePRs.length;
```

**Step 3: Add drawer and trigger to session view**

In the session view branch of the return, add after terminal-pane:

```typescript
{view === 'session' && (
  <>
    <DrawerTrigger count={attentionCount} onClick={toggleDrawer} />
    <AttentionDrawer
      isOpen={drawerOpen}
      onClose={closeDrawer}
      waitingSessions={waitingLocalSessions}
      daemonSessions={externalDaemonSessions}
      prs={prs}
      onSelectSession={handleSelectSession}
    />
  </>
)}
```

**Step 4: Test drawer toggle**

Run: `cd app && pnpm tauri dev`
Expected:
- Badge shows in top-right when in session view
- Clicking badge opens drawer
- X closes drawer

**Step 5: Commit**

```bash
git add app/src/App.tsx
git commit -m "feat(app): integrate attention drawer into session view"
```

---

## Task 7: Add Keyboard Shortcuts Hook

**Files:**
- Create: `app/src/hooks/useKeyboardShortcuts.ts`

**Step 1: Create keyboard shortcuts hook**

```typescript
// app/src/hooks/useKeyboardShortcuts.ts
import { useEffect, useCallback } from 'react';

interface KeyboardShortcutsConfig {
  onNewSession: () => void;
  onCloseSession: () => void;
  onToggleDrawer: () => void;
  onGoToDashboard: () => void;
  onJumpToWaiting: () => void;
  onSelectSession: (index: number) => void;
  onPrevSession: () => void;
  onNextSession: () => void;
  enabled: boolean;
}

export function useKeyboardShortcuts({
  onNewSession,
  onCloseSession,
  onToggleDrawer,
  onGoToDashboard,
  onJumpToWaiting,
  onSelectSession,
  onPrevSession,
  onNextSession,
  enabled,
}: KeyboardShortcutsConfig) {
  const handleKeyDown = useCallback(
    (e: KeyboardEvent) => {
      if (!enabled) return;

      const isMeta = e.metaKey || e.ctrlKey;

      // ⌘N - New session
      if (isMeta && e.key === 'n') {
        e.preventDefault();
        onNewSession();
        return;
      }

      // ⌘W - Close session
      if (isMeta && e.key === 'w') {
        e.preventDefault();
        onCloseSession();
        return;
      }

      // ⌘K or ⌘. - Toggle drawer
      if (isMeta && (e.key === 'k' || e.key === '.')) {
        e.preventDefault();
        onToggleDrawer();
        return;
      }

      // ⌘D or Escape - Go to dashboard
      if ((isMeta && e.key === 'd') || e.key === 'Escape') {
        e.preventDefault();
        onGoToDashboard();
        return;
      }

      // ⌘J - Jump to next waiting session
      if (isMeta && e.key === 'j') {
        e.preventDefault();
        onJumpToWaiting();
        return;
      }

      // ⌘1-9 - Select session by index
      if (isMeta && e.key >= '1' && e.key <= '9') {
        e.preventDefault();
        const index = parseInt(e.key, 10) - 1;
        onSelectSession(index);
        return;
      }

      // ⌘↑ - Previous session
      if (isMeta && e.key === 'ArrowUp') {
        e.preventDefault();
        onPrevSession();
        return;
      }

      // ⌘↓ - Next session
      if (isMeta && e.key === 'ArrowDown') {
        e.preventDefault();
        onNextSession();
        return;
      }
    },
    [
      enabled,
      onNewSession,
      onCloseSession,
      onToggleDrawer,
      onGoToDashboard,
      onJumpToWaiting,
      onSelectSession,
      onPrevSession,
      onNextSession,
    ]
  );

  useEffect(() => {
    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [handleKeyDown]);
}
```

**Step 2: Commit**

```bash
git add app/src/hooks/useKeyboardShortcuts.ts
git commit -m "feat(app): create keyboard shortcuts hook"
```

---

## Task 8: Integrate Keyboard Shortcuts

**Files:**
- Modify: `app/src/App.tsx`

**Step 1: Import and configure shortcuts hook**

```typescript
import { useKeyboardShortcuts } from './hooks/useKeyboardShortcuts';

// In App component, add handlers:
const handleJumpToWaiting = useCallback(() => {
  const waiting = enrichedLocalSessions.find((s) => s.state === 'waiting');
  if (waiting) {
    handleSelectSession(waiting.id);
  }
}, [enrichedLocalSessions, handleSelectSession]);

const handleSelectSessionByIndex = useCallback(
  (index: number) => {
    const session = sessions[index];
    if (session) {
      handleSelectSession(session.id);
    }
  },
  [sessions, handleSelectSession]
);

const handlePrevSession = useCallback(() => {
  if (!activeSessionId || sessions.length === 0) return;
  const currentIndex = sessions.findIndex((s) => s.id === activeSessionId);
  const prevIndex = currentIndex > 0 ? currentIndex - 1 : sessions.length - 1;
  handleSelectSession(sessions[prevIndex].id);
}, [activeSessionId, sessions, handleSelectSession]);

const handleNextSession = useCallback(() => {
  if (!activeSessionId || sessions.length === 0) return;
  const currentIndex = sessions.findIndex((s) => s.id === activeSessionId);
  const nextIndex = currentIndex < sessions.length - 1 ? currentIndex + 1 : 0;
  handleSelectSession(sessions[nextIndex].id);
}, [activeSessionId, sessions, handleSelectSession]);

const handleCloseCurrentSession = useCallback(() => {
  if (activeSessionId) {
    handleCloseSession(activeSessionId);
  }
}, [activeSessionId, handleCloseSession]);

// Use the hook:
useKeyboardShortcuts({
  onNewSession: handleNewSession,
  onCloseSession: handleCloseCurrentSession,
  onToggleDrawer: toggleDrawer,
  onGoToDashboard: goToDashboard,
  onJumpToWaiting: handleJumpToWaiting,
  onSelectSession: handleSelectSessionByIndex,
  onPrevSession: handlePrevSession,
  onNextSession: handleNextSession,
  enabled: true,
});
```

**Step 2: Test shortcuts**

Run: `cd app && pnpm tauri dev`
Expected:
- ⌘N opens new session dialog
- ⌘K toggles drawer
- ⌘D goes to dashboard
- ⌘1-9 switches sessions
- ⌘↑/⌘↓ cycles sessions

**Step 3: Commit**

```bash
git add app/src/App.tsx
git commit -m "feat(app): integrate keyboard shortcuts"
```

---

## Task 9: Create Location Picker Component

**Files:**
- Create: `app/src/components/LocationPicker.tsx`
- Create: `app/src/components/LocationPicker.css`
- Create: `app/src/hooks/useLocationHistory.ts`

**Step 1: Create location history hook**

```typescript
// app/src/hooks/useLocationHistory.ts
import { useState, useEffect, useCallback } from 'react';

const STORAGE_KEY = 'attn-location-history';
const MAX_HISTORY = 20;

interface LocationEntry {
  path: string;
  label: string;
  lastUsed: number;
}

export function useLocationHistory() {
  const [history, setHistory] = useState<LocationEntry[]>([]);

  // Load from localStorage on mount
  useEffect(() => {
    try {
      const stored = localStorage.getItem(STORAGE_KEY);
      if (stored) {
        setHistory(JSON.parse(stored));
      }
    } catch (e) {
      console.error('Failed to load location history:', e);
    }
  }, []);

  // Save to localStorage when history changes
  useEffect(() => {
    try {
      localStorage.setItem(STORAGE_KEY, JSON.stringify(history));
    } catch (e) {
      console.error('Failed to save location history:', e);
    }
  }, [history]);

  const addToHistory = useCallback((path: string) => {
    const label = path.split('/').pop() || path;
    setHistory((prev) => {
      const filtered = prev.filter((e) => e.path !== path);
      const newEntry: LocationEntry = { path, label, lastUsed: Date.now() };
      return [newEntry, ...filtered].slice(0, MAX_HISTORY);
    });
  }, []);

  const getRecentLocations = useCallback(() => {
    return [...history].sort((a, b) => b.lastUsed - a.lastUsed);
  }, [history]);

  return { history, addToHistory, getRecentLocations };
}
```

**Step 2: Create LocationPicker component**

```typescript
// app/src/components/LocationPicker.tsx
import { useState, useEffect, useCallback, useRef } from 'react';
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
  const inputRef = useRef<HTMLInputElement>(null);
  const { getRecentLocations, addToHistory } = useLocationHistory();

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
            ? inputValue.replace('~', process.env.HOME || '')
            : inputValue;
          handleSelect(path);
        }
        return;
      }
    },
    [filteredLocations, selectedIndex, inputValue, handleSelect, onClose]
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
                    <div className="picker-path">{loc.path.replace(process.env.HOME || '', '~')}</div>
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
```

**Step 3: Create LocationPicker styles**

```css
/* app/src/components/LocationPicker.css */
.location-picker-overlay {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.6);
  display: flex;
  align-items: flex-start;
  justify-content: center;
  padding-top: 100px;
  z-index: 200;
}

.location-picker {
  width: 560px;
  background: #111113;
  border: 1px solid #2a2a2d;
  border-radius: 12px;
  box-shadow: 0 24px 64px rgba(0, 0, 0, 0.6);
  overflow: hidden;
}

.picker-header {
  padding: 16px 16px 0;
}

.picker-title {
  font-family: 'JetBrains Mono', monospace;
  font-size: 11px;
  font-weight: 600;
  letter-spacing: 0.1em;
  text-transform: uppercase;
  color: #555;
  margin-bottom: 12px;
}

.picker-input-wrap {
  position: relative;
}

.picker-input {
  width: 100%;
  background: #0d0d0e;
  border: 1px solid #2a2a2d;
  border-radius: 8px;
  padding: 12px 14px;
  font-family: 'JetBrains Mono', monospace;
  font-size: 14px;
  color: #e8e8e8;
  outline: none;
}

.picker-input:focus {
  border-color: #4a4a4d;
}

.picker-input::placeholder {
  color: #555;
}

.picker-results {
  max-height: 320px;
  overflow-y: auto;
  padding: 8px;
}

.picker-section {
  margin-bottom: 8px;
}

.picker-section-title {
  font-size: 10px;
  font-weight: 600;
  letter-spacing: 0.08em;
  text-transform: uppercase;
  color: #555;
  padding: 8px 8px 4px;
}

.picker-item {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 10px 12px;
  border-radius: 6px;
  cursor: pointer;
}

.picker-item:hover,
.picker-item.selected {
  background: rgba(59, 130, 246, 0.15);
}

.picker-icon {
  width: 28px;
  height: 28px;
  border-radius: 6px;
  background: #1a1a1d;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 14px;
  flex-shrink: 0;
}

.picker-item.selected .picker-icon {
  background: #3b82f6;
}

.picker-info {
  flex: 1;
  min-width: 0;
}

.picker-name {
  font-size: 13px;
  color: #e8e8e8;
}

.picker-path {
  font-family: 'JetBrains Mono', monospace;
  font-size: 11px;
  color: #555;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.picker-empty {
  color: #555;
  font-size: 13px;
  text-align: center;
  padding: 40px 20px;
}

.picker-footer {
  border-top: 1px solid #2a2a2d;
  padding: 10px 16px;
  display: flex;
  align-items: center;
  gap: 16px;
}

.picker-footer .shortcut {
  font-size: 11px;
  color: #555;
}

.picker-footer kbd {
  background: #1a1a1d;
  border: 1px solid #2a2a2d;
  border-radius: 3px;
  padding: 2px 5px;
  font-family: 'JetBrains Mono', monospace;
  font-size: 10px;
  margin-right: 4px;
}
```

**Step 4: Commit**

```bash
git add app/src/components/LocationPicker.tsx app/src/components/LocationPicker.css app/src/hooks/useLocationHistory.ts
git commit -m "feat(app): create LocationPicker component with history"
```

---

## Task 10: Integrate Location Picker

**Files:**
- Modify: `app/src/App.tsx`

**Step 1: Import LocationPicker**

```typescript
import { LocationPicker } from './components/LocationPicker';
import { useLocationHistory } from './hooks/useLocationHistory';
```

**Step 2: Add picker state and update new session handler**

```typescript
// Add state:
const [locationPickerOpen, setLocationPickerOpen] = useState(false);
const { addToHistory } = useLocationHistory();

// Replace handleNewSession:
const handleNewSession = useCallback(() => {
  setLocationPickerOpen(true);
}, []);

const handleLocationSelect = useCallback(
  async (path: string) => {
    addToHistory(path);
    const folderName = path.split('/').pop() || 'session';
    await createSession(folderName, path);
  },
  [addToHistory, createSession]
);

const closeLocationPicker = useCallback(() => {
  setLocationPickerOpen(false);
}, []);
```

**Step 3: Add LocationPicker to render**

At the end of the return, before the closing `</div>`:

```typescript
<LocationPicker
  isOpen={locationPickerOpen}
  onClose={closeLocationPicker}
  onSelect={handleLocationSelect}
/>
```

**Step 4: Test location picker**

Run: `cd app && pnpm tauri dev`
Expected:
- ⌘N opens location picker instead of native dialog
- Can type to filter
- Enter selects
- History is remembered

**Step 5: Commit**

```bash
git add app/src/App.tsx
git commit -m "feat(app): integrate LocationPicker replacing native dialog"
```

---

## Task 11: Simplify Sidebar for Session View

**Files:**
- Modify: `app/src/components/Sidebar.tsx`
- Modify: `app/src/components/Sidebar.css`

**Step 1: Simplify Sidebar to sessions only**

The drawer now handles PRs and other sessions, so simplify sidebar:

```typescript
// app/src/components/Sidebar.tsx
import './Sidebar.css';

interface LocalSession {
  id: string;
  label: string;
  state: 'working' | 'waiting';
}

interface SidebarProps {
  sessions: LocalSession[];
  selectedId: string | null;
  onSelectSession: (id: string) => void;
  onNewSession: () => void;
  onCloseSession: (id: string) => void;
  onGoToDashboard: () => void;
}

export function Sidebar({
  sessions,
  selectedId,
  onSelectSession,
  onNewSession,
  onCloseSession,
  onGoToDashboard,
}: SidebarProps) {
  return (
    <div className="sidebar">
      <div className="sidebar-header">
        <button className="home-btn" onClick={onGoToDashboard} title="Dashboard (⌘D)">
          ⌂
        </button>
        <span className="sidebar-title">Sessions</span>
        <button className="new-session-btn" onClick={onNewSession} title="New Session (⌘N)">
          +
        </button>
      </div>

      <div className="session-list">
        {sessions.map((session, index) => (
          <div
            key={session.id}
            className={`session-item ${selectedId === session.id ? 'selected' : ''}`}
            onClick={() => onSelectSession(session.id)}
          >
            <span className={`state-indicator ${session.state}`} />
            <span className="session-label">{session.label}</span>
            <span className="session-shortcut">⌘{index + 1}</span>
            <button
              className="close-session-btn"
              onClick={(e) => {
                e.stopPropagation();
                onCloseSession(session.id);
              }}
              title="Close session (⌘W)"
            >
              ×
            </button>
          </div>
        ))}
      </div>

      <div className="sidebar-footer">
        <span className="shortcut-hint">⌘K drawer</span>
      </div>
    </div>
  );
}
```

**Step 2: Update Sidebar styles**

```css
/* app/src/components/Sidebar.css */
.sidebar {
  width: 200px;
  height: 100%;
  background: #111113;
  border-right: 1px solid #2a2a2d;
  display: flex;
  flex-direction: column;
  flex-shrink: 0;
}

.sidebar-header {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 12px;
  border-bottom: 1px solid #2a2a2d;
}

.home-btn {
  background: #1a1a1d;
  border: 1px solid #2a2a2d;
  border-radius: 4px;
  color: #888;
  font-size: 14px;
  width: 28px;
  height: 28px;
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
}

.home-btn:hover {
  background: #2a2a2d;
  color: #e8e8e8;
}

.sidebar-title {
  font-family: 'JetBrains Mono', monospace;
  font-size: 11px;
  font-weight: 600;
  letter-spacing: 0.1em;
  text-transform: uppercase;
  color: #555;
  flex: 1;
}

.new-session-btn {
  background: #1a1a1d;
  border: 1px solid #2a2a2d;
  border-radius: 4px;
  color: #888;
  font-size: 16px;
  width: 28px;
  height: 28px;
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
}

.new-session-btn:hover {
  background: #2a2a2d;
  color: #e8e8e8;
}

.session-list {
  flex: 1;
  overflow-y: auto;
  padding: 8px;
}

.session-item {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 10px;
  border-radius: 6px;
  cursor: pointer;
  font-size: 13px;
  color: #e8e8e8;
}

.session-item:hover {
  background: #1a1a1d;
}

.session-item.selected {
  background: #1a1a1d;
}

.state-indicator {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  flex-shrink: 0;
}

.state-indicator.waiting {
  background: #ff6b35;
}

.state-indicator.working {
  background: #22c55e;
}

.session-label {
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.session-shortcut {
  font-family: 'JetBrains Mono', monospace;
  font-size: 10px;
  color: #555;
}

.close-session-btn {
  background: none;
  border: none;
  color: #555;
  font-size: 14px;
  cursor: pointer;
  padding: 0;
  opacity: 0;
  transition: opacity 0.1s;
}

.session-item:hover .close-session-btn {
  opacity: 1;
}

.close-session-btn:hover {
  color: #e8e8e8;
}

.sidebar-footer {
  padding: 12px;
  border-top: 1px solid #2a2a2d;
}

.shortcut-hint {
  font-size: 11px;
  color: #555;
}
```

**Step 3: Update App.tsx to pass new props**

```typescript
<Sidebar
  sessions={enrichedLocalSessions}
  selectedId={activeSessionId}
  onSelectSession={handleSelectSession}
  onNewSession={handleNewSession}
  onCloseSession={handleCloseSession}
  onGoToDashboard={goToDashboard}
/>
```

**Step 4: Test simplified sidebar**

Run: `cd app && pnpm tauri dev`
Expected:
- Sidebar shows only sessions
- Home button returns to dashboard
- Shortcuts shown inline

**Step 5: Commit**

```bash
git add app/src/components/Sidebar.tsx app/src/components/Sidebar.css app/src/App.tsx
git commit -m "refactor(app): simplify Sidebar to sessions-only for session view"
```

---

## Task 12: Final Integration and Testing

**Files:**
- Modify: `app/src/App.tsx` (cleanup)

**Step 1: Remove unused imports**

Remove the `open` import from `@tauri-apps/plugin-dialog` if no longer used:

```typescript
// Remove this line if present:
// import { open } from '@tauri-apps/plugin-dialog';
```

**Step 2: Full integration test**

Run: `cd app && pnpm tauri dev`

Test checklist:
- [ ] App starts on dashboard
- [ ] ⌘N opens location picker
- [ ] Selecting location creates session and switches to session view
- [ ] Session view shows sidebar + terminal + drawer trigger
- [ ] ⌘K toggles attention drawer
- [ ] ⌘D returns to dashboard
- [ ] ⌘1-9 switches sessions
- [ ] ⌘W closes current session
- [ ] ⌘J jumps to waiting session
- [ ] Location history persists across restarts

**Step 3: Commit**

```bash
git add -A
git commit -m "feat(app): complete UX redesign with dashboard, drawer, and location picker"
```

---

## Summary

This plan implements:
1. **Dashboard view** - Home screen with sessions and PRs cards
2. **Session view** - Simplified sidebar + terminal + attention drawer
3. **Attention drawer** - Slide-out panel showing all attention items
4. **Keyboard shortcuts** - Full set (⌘N, ⌘W, ⌘K, ⌘D, ⌘J, ⌘1-9, ⌘↑/↓)
5. **Location picker** - Custom picker with history and fuzzy search

Total: 12 tasks, ~45 steps
