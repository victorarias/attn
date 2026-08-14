# Frontend Development (Tauri + React)

This file provides guidance for the Tauri/React frontend in `app/`.

## Running the App

```bash
pnpm run dev    # Starts tauri dev with hot reload
```

## Architecture

### Key Components

- **App.tsx**: Main layout, state orchestration (see "App.tsx two-component pattern" in Gotchas)
- **Sidebar.tsx**: Session/PR list with state indicators
- **Dashboard.tsx**: Terminal tabs and main content area
- **GhosttyTerminal.tsx**: Ghostty WASM terminal model and GPU renderer integration
- **SessionTerminalWorkspace/**: Pane lifecycle and PTY bridge wiring
- **LocationPicker.tsx**: Path selection with filesystem suggestions
- **NewSessionDialog/**: Session creation (PathInput, RepoOptions subcomponents)
- **PresentRoot/**, **PresentTour/**: Present window — review/diff orchestration and guided tour (replaced the old ChangesPanel/DiffDetailPanel)
- **DiffView.tsx**: `@pierre/diffs` (diffs.com) wrapper — unified/split diff rendering, Shiki highlighting, inline review-comment annotations (via DiffCommentThread)
- **AttentionDrawer.tsx**: Quick view of items needing attention

### State Management

- **store/daemonSessions.ts**: Zustand store for session/PR state from daemon
- **store/sessions.ts**: Local terminal session management
- **hooks/useDaemonSocket.ts**: WebSocket connection with reconnect + circuit breaker (manual daemon recovery, no auto-restart)

### Terminal Component (Ghostty)

When modifying `src/components/GhosttyTerminal.tsx` or `src/components/SessionTerminalWorkspace/`:

1. **Wait for container dimensions** - Use ResizeObserver to wait for valid size before calling `onReady`
2. **Keep the GPU surface aligned with the model** - Resize the Ghostty model and canvas before notifying the PTY (sends SIGWINCH)
3. **Preserve user-facing behaviors** - Keep copy-on-select, URL opening, keyboard input, and automation bridge coverage working through the Ghostty component

### PTY Architecture

Daemon-managed PTY handling (`internal/pty` in Go):
- Daemon spawns/manages Claude/Codex/shell PTYs
- Frontend streams terminal I/O over WebSocket (`spawn_session`, `attach_session`, `pty_input`, `pty_output`, etc.)
- Reattach/replay path restores terminal state from daemon scrollback after reconnect

## Testing

### Unit Tests (Vitest)

```bash
pnpm test                   # Run all tests
pnpm test -- --watch        # Watch mode
pnpm test PresentRoot       # Run specific component tests
```

**Purpose:** Catch bugs before manual testing - infinite loops, race conditions, incorrect state management.

**Architecture:** Components receive daemon functions as props. In tests, `createMockDaemon()` (`src/test/mocks/daemon.ts`) provides `create*()` factories (e.g. `createFetchDiff()`) that record every call and return configured responses; `src/test/utils.ts` re-exports testing-library plus `setupDefaultResponses()`, `waitForCalls()`, and `assertNoMoreCalls()`.

**When to run:**
- After ANY change to components that use `useDaemon()`
- After changes to `useDaemonSocket.ts`
- Before declaring work complete on frontend changes

**What tests catch:**
- **Infinite loops**: Assert exact call counts after render settles
- **Race conditions**: Simulate out-of-order responses, verify correct content displayed
- **Extra daemon calls**: Mock tracks all calls; assert expected count
- **State bugs**: Verify UI updates correctly after interactions

**Test pattern:**
```typescript
it('fetches diff exactly once on open', async () => {
  const mockDaemon = createMockDaemon();
  setupDefaultResponses(mockDaemon);
  render(<MyPanel fetchDiff={mockDaemon.createFetchDiff()} {...props} />);
  await waitFor(() => screen.getByText('file.tsx'));

  expect(mockDaemon.getCalls('fetchDiff')).toHaveLength(1);
  await sleep(100); // Ensure no loop
  expect(mockDaemon.getCalls('fetchDiff')).toHaveLength(1);
});
```

Canonical example: `src/components/PresentRoot/PresentRoot.test.tsx`. When wrapping components, memoize callbacks passed as props so wrapper re-renders don't retrigger fetch effects.

### E2E Tests (Playwright)

```bash
pnpm run e2e               # Run all E2E tests
pnpm run e2e:headed        # Run with browser visible
pnpm run e2e -- --ui       # Run with Playwright UI
```

### Component Test Harness (Playwright)

**When to use:** For components that need real browser APIs that jsdom can't simulate:
- `@pierre/diffs` / DiffView (custom element + shadow DOM, Shiki highlighting, `adoptedStyleSheets`)
- Complex DOM interactions (drag/drop, scroll-based behaviors)
- Components with native browser features

**When NOT to use:** Prefer vitest + happy-dom for simpler components. The harness adds overhead - use it only when necessary.

**How it works:**
1. Harness page at `/test-harness/?component=ComponentName`
2. Component rendered in isolation with mocked props
3. `window.__HARNESS__` API for test control
4. Real browser environment via Playwright

**Creating a new harness:**

```bash
# 1. Create harness file
test-harness/harnesses/MyComponentHarness.tsx

# 2. Register in harnesses/index.ts
export const harnesses = {
  DiffView: DiffViewHarness,
  MyComponent: MyComponentHarness,  // Add here
};

# 3. Write Playwright test
e2e/component-harness.spec.ts  # Or new file
```

**Harness pattern:**
```typescript
export function MyComponentHarness({ onReady, setTriggerRerender }: HarnessProps) {
  // Mock all props/callbacks
  const mockCallback = useCallback(async (...args) => {
    window.__HARNESS__.recordCall('callbackName', args);
    return { success: true };
  }, []);

  useEffect(() => { onReady(); }, [onReady]);

  return <MyComponent prop={mockCallback} />;
}
```

**Test pattern:**
```typescript
test('describes exact behavior being tested', async ({ page }) => {
  await page.goto('/test-harness/?component=MyComponent');
  await page.waitForSelector('.expected-element');

  // Use real interactions
  await page.locator('.input').focus();
  await page.keyboard.type('content', { delay: 10 });

  // Trigger state changes naturally (not artificial rerenders)
  await page.locator('.another-element').click();

  // Assert behavior
  await expect(page.locator('.input')).toHaveValue('content');

  // Verify mock calls
  const calls = await page.evaluate(() => window.__HARNESS__.getCalls('callbackName'));
  expect(calls[0][0]).toBe('expected-arg');
});
```

**Key learnings:**
- Use `page.keyboard.type()` not `.fill()` for realistic input
- Use `.click()` not `dispatchEvent()` when possible
- Trigger re-renders naturally (open another form, change state) not artificially
- Document the exact bug scenario in regression tests with JSDoc
- Wait for specific elements, not arbitrary timeouts

**Running harness tests:**
```bash
pnpm run e2e -- e2e/component-harness.spec.ts
pnpm run e2e:headed -- e2e/component-harness.spec.ts  # Debug visually
```

## Known Gotchas

1. **App.tsx two-component pattern**: App.tsx has two components: `App` (outer) and `AppContent` (inner). This split exists because `AppContent` needs to be wrapped in providers (like `DaemonProvider`) that require functions from `useDaemonSocket()`.

   **A new daemon socket function needs no wiring in App.tsx.** `App` is the only
   caller of `useDaemonSocket()`, and it publishes the whole return value through
   `DaemonApiProvider`; read it with `useDaemonApi()` wherever you need it. Return
   it from the hook and it is there.

   `AppContentProps` is now only App's own state — sessions, workspaces, PRs,
   settings, the change signals. A daemon send function does not belong there:
   passing one as a prop puts its name in four places that have to agree, which
   is what this replaced.

2. **Worktree action key collision**: `sendCreateWorktree()` and `sendCreateWorktreeFromBranch()` use the same pending action key. Don't call both simultaneously.

3. **Timeout vs completion race**: Async operations timeout after 30s. If daemon responds after timeout, the operation completed but UI shows error.

4. **Git status subscription**: Only 1 subscription per client. New subscription replaces old one.

5. **Circuit breaker auto-reset**: Opens after failed reconnects, auto-resets after 30s even without user action.

6. **GPU surfaces outnumber what you can see.** Three traps, each of which
   cost the app over 100MB at rest until 2026-08-14. Receipts and the
   measurement recipe:
   [docs/plans/2026-08-14-app-memory-floor.md](../docs/plans/2026-08-14-app-memory-floor.md).

   - **`will-change` on an always-mounted component.** The right dock mounts
     all five panels and only toggles a class, so a permanent hint gave every
     closed one a full-height backing store it never drew. Promote under the
     open state, not on the base rule — and check whether the component is
     conditionally rendered before reaching for the hint at all. The same
     applies to `translateZ`, `backdrop-filter`, `isolation`, and `contain`.
   - **WebGL context attributes default to on.** `depth` is `true` unless you
     say otherwise, and each attachment is another drawing-buffer-sized
     allocation per canvas. Ask for what you read; the terminal renderer reads
     neither depth nor stencil.
   - **A hidden canvas still owns its drawing buffer.** `display:none` hides
     an element; the buffer is sized by the canvas's width/height attributes.
     An inactive session's panes hand theirs back via
     `setSurfaceReleased(true)` on the terminal handle, driven from
     `SessionTerminalWorkspace`. If you add a surface that survives being
     off-screen, give it the same treatment — and restore in a layout effect
     so the repaint precedes the frame that reveals it.

   Never resolve one of these by tearing down and rebuilding a WebGL context.
   WKWebView's live-context pool is small enough that rebuilding every mounted
   pane loses contexts and permanently breaks panes — the reason the font-size
   effect in `GhosttyTerminal.tsx` re-metrics in place.

   Measure with `scenario-perf-baseline`'s `APP FOOTPRINT` and its
   `paneSizedSurfaces` histogram. `ps` RSS cannot see graphics memory at all.
