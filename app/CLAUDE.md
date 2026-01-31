# Frontend Development (Tauri + React)

This file provides guidance for the Tauri/React frontend in `app/`.

## Running the App

```bash
pnpm run dev    # Starts tauri dev with hot reload
```

## Architecture

### Key Components

- **App.tsx**: Main layout, state orchestration (split into `App` + `AppContent` for provider wrapping - when adding daemon socket functions, update both components' props)
- **Sidebar.tsx**: Session/PR list with state indicators
- **Dashboard.tsx**: Terminal tabs and main content area
- **Terminal.tsx**: xterm.js integration with PTY bridge
- **LocationPicker.tsx**: Path selection with filesystem suggestions
- **NewSessionDialog/**: Session creation (PathInput, RepoOptions subcomponents)
- **ChangesPanel.tsx**: Git changes display
- **DiffOverlay.tsx**: Monaco-based diff viewing
- **BranchPicker.tsx**: Branch selection UI
- **AttentionDrawer.tsx**: Quick view of items needing attention

### State Management

- **store/daemonSessions.ts**: Zustand store for session/PR state from daemon
- **store/sessions.ts**: Local terminal session management
- **hooks/useDaemonSocket.ts**: WebSocket connection with circuit breaker (3 reconnects → 2 daemon restarts → circuit opens 30s)

### Terminal Component (xterm.js)

When modifying `src/components/Terminal.tsx`:

1. **Wait for container dimensions** - Use ResizeObserver to wait for valid size before calling `onReady`
2. **Pre-calculate initial dimensions** - Measure font before creating XTerm to avoid 80x24 default
3. **Resize xterm first, then PTY** - Call `term.resize()` then notify PTY (sends SIGWINCH)
4. **Use VS Code's resize debouncing** - Y-axis immediate, X-axis 100ms debounced (text reflow is expensive)

### PTY Architecture

Native Rust PTY handling via `portable-pty` (`src-tauri/src/pty_manager.rs`):
- Direct PTY management in Rust, no separate process
- Event-driven streaming to frontend via Tauri events
- Handles UTF-8 boundary splits for proper terminal rendering

## Testing

### Unit Tests (Vitest)

```bash
pnpm test               # Run all tests
pnpm test -- --watch    # Watch mode
pnpm test ReviewPanel   # Run specific component tests
```

**Purpose:** Catch bugs before manual testing - infinite loops, race conditions, incorrect state management.

**Architecture:** Components use `useDaemon()` hook. In tests, a `MockDaemonProvider` replaces the real WebSocket with a controllable mock that tracks all calls.

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
  render(<ReviewPanel {...props} />, { wrapper: MockDaemonProvider });
  await waitFor(() => screen.getByText('file.tsx'));

  expect(mockDaemon.getCalls('get_file_diff')).toHaveLength(1);
  await sleep(100); // Ensure no loop
  expect(mockDaemon.getCalls('get_file_diff')).toHaveLength(1);
});
```

**Full design:** See `docs/plans/2026-01-02-frontend-testing-strategy.md`

### E2E Tests (Playwright)

```bash
pnpm run e2e               # Run all E2E tests
pnpm run e2e:headed        # Run with browser visible
pnpm run e2e -- --ui       # Run with Playwright UI
```

### Component Test Harness (Playwright)

**When to use:** For components that need real browser APIs that jsdom can't simulate:
- CodeMirror (requires DOM measurements, layout, ResizeObserver)
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
  ReviewPanel: ReviewPanelHarness,
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

1. **Worktree action key collision**: `sendCreateWorktree()` and `sendCreateWorktreeFromBranch()` use the same pending action key. Don't call both simultaneously.

2. **Timeout vs completion race**: Async operations timeout after 30s. If daemon responds after timeout, the operation completed but UI shows error.

3. **Git status subscription**: Only 1 subscription per client. New subscription replaces old one.

4. **Circuit breaker auto-reset**: Opens after failed reconnects, auto-resets after 30s even without user action.
