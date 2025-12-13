# Session State E2E Test Design

## Overview

E2E tests for verifying session state changes propagate correctly from daemon to UI.

## Approach

Test via WebSocket commands - inject test sessions and state changes directly into the daemon, verify UI reflects changes correctly.

## Protocol Addition

```go
// internal/protocol/types.go
const MsgInjectTestSession = "inject_test_session"

type InjectTestSessionMessage struct {
    Cmd     string   `json:"cmd"`
    Session *Session `json:"session"`
}
```

Add to `ParseMessage` switch and daemon handler.

## Daemon Handler

```go
// internal/daemon/daemon.go
case protocol.MsgInjectTestSession:
    d.handleInjectTestSession(conn, msg.(*protocol.InjectTestSessionMessage))

func (d *Daemon) handleInjectTestSession(conn net.Conn, msg *protocol.InjectTestSessionMessage) {
    d.store.Add(msg.Session)
    d.broadcastEvent(protocol.EventSessionRegistered, msg.Session, nil, nil, nil)
    writeResponse(conn, protocol.Response{OK: true})
}
```

## UI Test Attributes

Add `data-testid` and `data-state` attributes for reliable selectors:

### Dashboard.tsx
```tsx
<div className="session-group" data-testid="session-group-waiting">
<div className="session-group" data-testid="session-group-working">
<div className="session-group" data-testid="session-group-idle">

<div
  key={s.id}
  className="session-row clickable"
  data-testid={`session-${s.id}`}
  data-state={s.state}
>
  <span className={`state-dot ${s.state}`} data-testid="state-indicator" />
```

### Sidebar.tsx
```tsx
<div
  key={session.id}
  className={`session-item ${selectedId === session.id ? 'selected' : ''}`}
  data-testid={`sidebar-session-${session.id}`}
  data-state={session.state}
>
  <span className={`state-indicator ${session.state}`} data-testid="state-indicator" />
```

### AttentionDrawer.tsx
```tsx
<div
  key={s.id}
  className="attention-item clickable"
  data-testid={`attention-session-${s.id}`}
  data-state={s.state}
>
```

## Fixture Updates

```typescript
// app/e2e/fixtures.ts

async function injectTestSession(
  socketPath: string,
  session: { id: string; label: string; state: string; directory?: string }
): Promise<void> {
  return new Promise((resolve, reject) => {
    const client = net.createConnection(socketPath, () => {
      const msg = {
        cmd: 'inject_test_session',
        session: {
          id: session.id,
          label: session.label,
          directory: session.directory || '/tmp/test',
          state: session.state,
          state_since: new Date().toISOString(),
          last_seen: new Date().toISOString(),
          todos: null,
          muted: false,
        },
      };
      client.write(JSON.stringify(msg));
    });
    client.on('data', () => { client.end(); resolve(); });
    client.on('error', reject);
  });
}

async function updateSessionState(
  socketPath: string,
  id: string,
  state: string
): Promise<void> {
  return new Promise((resolve, reject) => {
    const client = net.createConnection(socketPath, () => {
      client.write(JSON.stringify({ cmd: 'state', id, state }));
    });
    client.on('data', () => { client.end(); resolve(); });
    client.on('error', reject);
  });
}

// Updated Fixtures type
type Fixtures = {
  mockGitHub: MockGitHubServer;
  daemon: {
    start: () => Promise<{ wsUrl: string; socketPath: string }>;
    injectSession: (s: { id: string; label: string; state: string }) => Promise<void>;
    updateSessionState: (id: string, state: string) => Promise<void>;
  };
};
```

## Test Cases

### app/e2e/session-states.spec.ts

```typescript
import { test, expect } from './fixtures';

test.describe('Session State Changes', () => {

  test('displays sessions grouped by state on dashboard', async ({ page, daemon }) => {
    await daemon.start();
    await daemon.injectSession({ id: 's1', label: 'Working Task', state: 'working' });
    await daemon.injectSession({ id: 's2', label: 'Needs Input', state: 'waiting_input' });
    await daemon.injectSession({ id: 's3', label: 'Finished', state: 'idle' });

    await page.goto('/');

    // Verify grouping headers
    await expect(page.locator('[data-testid="session-group-working"]')).toBeVisible();
    await expect(page.locator('[data-testid="session-group-waiting"]')).toBeVisible();
    await expect(page.locator('[data-testid="session-group-idle"]')).toBeVisible();

    // Verify sessions in correct groups
    await expect(page.locator('[data-testid="session-s1"][data-state="working"]')).toBeVisible();
    await expect(page.locator('[data-testid="session-s2"][data-state="waiting_input"]')).toBeVisible();
    await expect(page.locator('[data-testid="session-s3"][data-state="idle"]')).toBeVisible();
  });

  test('state indicator colors match design spec', async ({ page, daemon }) => {
    await daemon.start();
    await daemon.injectSession({ id: 's1', label: 'Working', state: 'working' });
    await daemon.injectSession({ id: 's2', label: 'Waiting', state: 'waiting_input' });
    await daemon.injectSession({ id: 's3', label: 'Idle', state: 'idle' });

    await page.goto('/');

    // Verify colors (RGB equivalents)
    const workingDot = page.locator('[data-testid="session-s1"] [data-testid="state-indicator"]');
    const waitingDot = page.locator('[data-testid="session-s2"] [data-testid="state-indicator"]');
    const idleDot = page.locator('[data-testid="session-s3"] [data-testid="state-indicator"]');

    await expect(workingDot).toHaveCSS('background-color', 'rgb(34, 197, 94)');  // #22c55e green
    await expect(waitingDot).toHaveCSS('background-color', 'rgb(245, 158, 11)'); // #f59e0b yellow
    await expect(idleDot).toHaveCSS('background-color', 'rgb(107, 114, 128)');   // #6b7280 grey
  });

  test('UI updates in real-time when state changes', async ({ page, daemon }) => {
    await daemon.start();
    await daemon.injectSession({ id: 's1', label: 'My Session', state: 'working' });

    await page.goto('/');

    // Initially working
    await expect(page.locator('[data-testid="session-s1"][data-state="working"]')).toBeVisible();

    // Change to waiting_input
    await daemon.updateSessionState('s1', 'waiting_input');
    await expect(page.locator('[data-testid="session-s1"][data-state="waiting_input"]')).toBeVisible({ timeout: 2000 });

    // Change to idle
    await daemon.updateSessionState('s1', 'idle');
    await expect(page.locator('[data-testid="session-s1"][data-state="idle"]')).toBeVisible({ timeout: 2000 });
  });

  test('sidebar reflects state changes', async ({ page, daemon }) => {
    await daemon.start();
    await daemon.injectSession({ id: 's1', label: 'Sidebar Test', state: 'working' });

    await page.goto('/');

    // Click on session to show sidebar
    await page.locator('[data-testid="session-s1"]').click();

    // Verify sidebar indicator
    const sidebarIndicator = page.locator('[data-testid="sidebar-session-s1"] [data-testid="state-indicator"]');
    await expect(sidebarIndicator).toHaveClass(/working/);

    // Change state
    await daemon.updateSessionState('s1', 'waiting_input');
    await expect(sidebarIndicator).toHaveClass(/waiting_input/, { timeout: 2000 });
  });

  test('attention drawer shows only waiting_input sessions', async ({ page, daemon }) => {
    await daemon.start();
    await daemon.injectSession({ id: 's1', label: 'Working', state: 'working' });
    await daemon.injectSession({ id: 's2', label: 'Waiting', state: 'waiting_input' });
    await daemon.injectSession({ id: 's3', label: 'Idle', state: 'idle' });

    await page.goto('/');

    // Open attention drawer (Cmd+K)
    await page.keyboard.press('Meta+k');

    // Only waiting_input session should appear
    await expect(page.locator('[data-testid="attention-session-s2"]')).toBeVisible();
    await expect(page.locator('[data-testid="attention-session-s1"]')).not.toBeVisible();
    await expect(page.locator('[data-testid="attention-session-s3"]')).not.toBeVisible();
  });

});
```

## Implementation Order

1. Add protocol types (`MsgInjectTestSession`, `InjectTestSessionMessage`)
2. Add daemon handler
3. Add data-testid attributes to UI components
4. Update e2e fixtures
5. Create test file
6. Run tests and verify
