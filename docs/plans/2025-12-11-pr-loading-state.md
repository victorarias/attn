# PR Loading State Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Show a skeleton loading state for PRs until initial data is received from the daemon.

**Architecture:** Add `hasReceivedInitialState` flag to useDaemonSocket hook, pass to Dashboard, conditionally render skeleton loader vs empty state vs PR list.

**Tech Stack:** React, TypeScript, CSS animations

---

### Task 1: Add Loading State to useDaemonSocket

**Files:**
- Modify: `app/src/hooks/useDaemonSocket.ts:68-80` (add state and update on initial_state)
- Modify: `app/src/hooks/useDaemonSocket.ts:272-278` (add to return)

**Step 1: Add hasReceivedInitialState state**

In `useDaemonSocket` function, after line 80 (`const [connectionError, setConnectionError] = useState<string | null>(null);`), add:

```typescript
const [hasReceivedInitialState, setHasReceivedInitialState] = useState(false);
```

**Step 2: Set flag on initial_state event**

In the `case 'initial_state':` block (around line 117), after the repos update, add:

```typescript
setHasReceivedInitialState(true);
```

**Step 3: Add to return object**

Update the return statement (around line 272) to include:

```typescript
return {
  isConnected: wsRef.current?.readyState === WebSocket.OPEN,
  connectionError,
  hasReceivedInitialState,
  sendPRAction,
  sendMutePR,
  sendMuteRepo,
};
```

**Step 4: Run type check**

Run: `cd app && pnpm tsc --noEmit`
Expected: Should pass (or show errors in App.tsx which we'll fix next)

**Step 5: Commit**

```bash
git add app/src/hooks/useDaemonSocket.ts
git commit -m "feat(daemon): track initial state received for loading states"
```

---

### Task 2: Pass Loading State to Dashboard

**Files:**
- Modify: `app/src/App.tsx:58` (destructure new prop)
- Modify: `app/src/App.tsx:290-296` (pass to Dashboard)
- Modify: `app/src/components/Dashboard.tsx:10-21` (add prop to interface)

**Step 1: Destructure hasReceivedInitialState from hook**

In App.tsx around line 58, update:

```typescript
const { sendPRAction, sendMutePR, sendMuteRepo, connectionError, hasReceivedInitialState } = useDaemonSocket({
```

**Step 2: Pass to Dashboard component**

In App.tsx around line 290-296, add the `isLoading` prop:

```tsx
<Dashboard
  sessions={enrichedLocalSessions}
  daemonSessions={externalDaemonSessions}
  prs={prs}
  isLoading={!hasReceivedInitialState}
  onSelectSession={handleSelectSession}
  onNewSession={handleNewSession}
/>
```

**Step 3: Add isLoading to Dashboard props interface**

In Dashboard.tsx, update the interface (around line 10-21):

```typescript
interface DashboardProps {
  sessions: Array<{
    id: string;
    label: string;
    state: 'working' | 'waiting';
    cwd: string;
  }>;
  daemonSessions: DaemonSession[];
  prs: DaemonPR[];
  isLoading: boolean;
  onSelectSession: (id: string) => void;
  onNewSession: () => void;
}
```

**Step 4: Destructure isLoading in component**

In Dashboard.tsx, update the function parameters (around line 23-29):

```typescript
export function Dashboard({
  sessions,
  daemonSessions: _daemonSessions,
  prs,
  isLoading,
  onSelectSession,
  onNewSession,
}: DashboardProps) {
```

**Step 5: Run type check**

Run: `cd app && pnpm tsc --noEmit`
Expected: PASS

**Step 6: Commit**

```bash
git add app/src/App.tsx app/src/components/Dashboard.tsx
git commit -m "feat(dashboard): wire up isLoading prop from daemon socket"
```

---

### Task 3: Add Skeleton Loader CSS

**Files:**
- Modify: `app/src/components/Dashboard.css` (append skeleton styles)

**Step 1: Add skeleton loader styles**

Append to Dashboard.css:

```css
/* PR Loading skeleton */
.pr-loading {
  padding: 8px 0;
}

.pr-loading-status {
  font-family: 'JetBrains Mono', monospace;
  font-size: 10px;
  color: #555;
  letter-spacing: 0.05em;
  margin-bottom: 12px;
  padding: 0 10px;
}

.pr-skeleton-row {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 10px;
  margin-bottom: 4px;
}

.pr-skeleton-dot {
  width: 16px;
  height: 16px;
  border-radius: 4px;
  background: #1a1a1d;
  flex-shrink: 0;
}

.pr-skeleton-number {
  width: 32px;
  height: 14px;
  border-radius: 3px;
  background: #1a1a1d;
  flex-shrink: 0;
}

.pr-skeleton-title {
  flex: 1;
  height: 14px;
  border-radius: 3px;
  background: #1a1a1d;
}

/* Vary title widths for realism */
.pr-skeleton-row:nth-child(2) .pr-skeleton-title { width: 85%; }
.pr-skeleton-row:nth-child(3) .pr-skeleton-title { width: 70%; }
.pr-skeleton-row:nth-child(4) .pr-skeleton-title { width: 90%; }

/* Shimmer animation */
.pr-skeleton-dot,
.pr-skeleton-number,
.pr-skeleton-title {
  position: relative;
  overflow: hidden;
}

.pr-skeleton-dot::after,
.pr-skeleton-number::after,
.pr-skeleton-title::after {
  content: '';
  position: absolute;
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  background: linear-gradient(
    90deg,
    transparent 0%,
    rgba(255, 107, 53, 0.08) 50%,
    transparent 100%
  );
  animation: pr-shimmer 1.5s infinite;
}

@keyframes pr-shimmer {
  0% { transform: translateX(-100%); }
  100% { transform: translateX(100%); }
}

/* Stagger animation per row */
.pr-skeleton-row:nth-child(2) .pr-skeleton-dot::after,
.pr-skeleton-row:nth-child(2) .pr-skeleton-number::after,
.pr-skeleton-row:nth-child(2) .pr-skeleton-title::after {
  animation-delay: 0.15s;
}

.pr-skeleton-row:nth-child(3) .pr-skeleton-dot::after,
.pr-skeleton-row:nth-child(3) .pr-skeleton-number::after,
.pr-skeleton-row:nth-child(3) .pr-skeleton-title::after {
  animation-delay: 0.3s;
}

.pr-skeleton-row:nth-child(4) .pr-skeleton-dot::after,
.pr-skeleton-row:nth-child(4) .pr-skeleton-number::after,
.pr-skeleton-row:nth-child(4) .pr-skeleton-title::after {
  animation-delay: 0.45s;
}
```

**Step 2: Commit**

```bash
git add app/src/components/Dashboard.css
git commit -m "style(dashboard): add PR skeleton loader styles"
```

---

### Task 4: Render Skeleton Loader in Dashboard

**Files:**
- Modify: `app/src/components/Dashboard.tsx:149-152` (replace card-body conditional)

**Step 1: Update PR card body to show loading state**

Replace the card-body content (around lines 149-221) with:

```tsx
<div className="card-body scrollable">
  {isLoading ? (
    <div className="pr-loading">
      <div className="pr-loading-status">Fetching PRs...</div>
      <div className="pr-skeleton-row">
        <div className="pr-skeleton-dot" />
        <div className="pr-skeleton-number" />
        <div className="pr-skeleton-title" />
      </div>
      <div className="pr-skeleton-row">
        <div className="pr-skeleton-dot" />
        <div className="pr-skeleton-number" />
        <div className="pr-skeleton-title" />
      </div>
      <div className="pr-skeleton-row">
        <div className="pr-skeleton-dot" />
        <div className="pr-skeleton-number" />
        <div className="pr-skeleton-title" />
      </div>
    </div>
  ) : prsByRepo.size === 0 ? (
    <div className="card-empty">No PRs need attention</div>
  ) : (
    Array.from(prsByRepo.entries()).map(([repo, repoPRs]) => {
      const repoName = repo.split('/')[1] || repo;
      const isCollapsed = collapsedRepos.has(repo);
      const reviewCount = repoPRs.filter((p) => p.role === 'reviewer').length;
      const authorCount = repoPRs.filter((p) => p.role === 'author').length;

      return (
        <div key={repo} className="pr-repo-group">
          <div className="repo-header">
            <div
              className="repo-header-content clickable"
              onClick={() => toggleRepo(repo)}
            >
              <span className={`collapse-icon ${isCollapsed ? 'collapsed' : ''}`}>▾</span>
              <span className="repo-name">{repoName}</span>
              <span className="repo-counts">
                {reviewCount > 0 && <span className="count review">{reviewCount} review</span>}
                {authorCount > 0 && <span className="count author">{authorCount} yours</span>}
              </span>
            </div>
            <button
              className="repo-mute-btn"
              onClick={(e) => {
                e.stopPropagation();
                sendMuteRepo(repo);
              }}
              title="Mute all PRs from this repo"
            >
              ⊘
            </button>
          </div>
          {!isCollapsed && (
            <div className="repo-prs">
              {repoPRs.map((pr) => (
                <div
                  key={pr.id}
                  className={`pr-row ${fadingPRs.has(pr.id) ? 'fading-out' : ''}`}
                  data-testid="pr-card"
                >
                  <a
                    href={pr.url}
                    target="_blank"
                    rel="noopener noreferrer"
                    className="pr-link"
                  >
                    <span className={`pr-role ${pr.role}`}>
                      {pr.role === 'reviewer' ? '👀' : '✏️'}
                    </span>
                    <span className="pr-number">#{pr.number}</span>
                    <span className="pr-title">{pr.title}</span>
                    {pr.role === 'author' && (
                      <span className="pr-reason">{pr.reason.replace(/_/g, ' ')}</span>
                    )}
                  </a>
                  <PRActions
                    repo={pr.repo}
                    number={pr.number}
                    prId={pr.id}
                    onActionComplete={handleActionComplete}
                  />
                </div>
              ))}
            </div>
          )}
        </div>
      );
    })
  )}
</div>
```

**Step 2: Run type check**

Run: `cd app && pnpm tsc --noEmit`
Expected: PASS

**Step 3: Commit**

```bash
git add app/src/components/Dashboard.tsx
git commit -m "feat(dashboard): show skeleton loader while PRs are loading"
```

---

### Task 5: Manual Verification

**Step 1: Start the dev server**

Run: `cd app && pnpm run dev:all`

**Step 2: Verify loading state**

1. Stop the daemon if running: `pkill -f "attn daemon"` or similar
2. Open the app - should show skeleton loader with "Fetching PRs..."
3. Start the daemon - skeleton should transition to actual PRs or "No PRs need attention"

**Step 3: Final commit (if any fixes needed)**

```bash
git add -A
git commit -m "fix(dashboard): polish loading state"
```

---

## Summary

| Task | Description | Files |
|------|-------------|-------|
| 1 | Add hasReceivedInitialState to hook | useDaemonSocket.ts |
| 2 | Wire loading state to Dashboard | App.tsx, Dashboard.tsx |
| 3 | Add skeleton CSS styles | Dashboard.css |
| 4 | Render skeleton in Dashboard | Dashboard.tsx |
| 5 | Manual verification | - |
