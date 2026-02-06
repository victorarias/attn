# Frontend Testing Strategy

## Goal

Enable Claude to catch bugs before manual testing by running integration tests against a mocked daemon. Tests should catch:
- Infinite loops from effect/state interactions
- Race conditions in async handling
- Incorrect UI state after interactions
- Extra/missing daemon calls

## Architecture

### Mock Boundary

`useDaemonSocket` is the integration boundary. We wrap it in a context that can be swapped:

```
Production:  App → DaemonProvider (real WebSocket) → Components
Tests:       App → MockDaemonProvider (controllable) → Components
```

Components use `useDaemon()` hook which returns the same interface in both cases.

### Call Tracking

The mock tracks every call made to the daemon:

```typescript
// Verify exactly one call was made
expect(mockDaemon.getCalls('get_file_diff')).toHaveLength(1);

// Verify call arguments
expect(mockDaemon.getCalls('get_file_diff')).toEqual([
  { path: 'src/App.tsx', staged: false }
]);

// Verify total calls across all methods
expect(mockDaemon.getCalls()).toHaveLength(3);
```

### Response Control

Tests control when and what responses arrive:

```typescript
// Immediate response
mockDaemon.setResponse('get_file_diff', { original: '...', modified: '...' });

// Delayed response (test loading states)
mockDaemon.setDelay('get_file_diff', 500);

// Simulate race condition - responses arrive out of order
mockDaemon.simulateResponse('file_diff_result', { path: 'file-B.ts', ... });
mockDaemon.simulateResponse('file_diff_result', { path: 'file-A.ts', ... });
```

### Guard Rails

```typescript
// Strict mode - fail if unexpected call happens
const mockDaemon = createMockDaemon({ strict: true });
mockDaemon.expect('get_file_diff', { path: 'src/App.tsx' });

// Call limits - fail if threshold exceeded (catches loops)
const mockDaemon = createMockDaemon({
  maxCalls: { 'get_file_diff': 5 }
});
```

## File Structure

```
app/src/
  test/
    setup.ts              # Vitest setup, global mocks
    mocks/
      daemon.ts           # MockDaemonProvider + createMockDaemon()
    utils.ts              # renderWithDaemon(), common fixtures
  components/
    ReviewPanel.tsx
    ReviewPanel.test.tsx  # Co-located tests
  hooks/
    useDaemonSocket.ts
    useDaemonSocket.test.ts
```

## Test Categories

| Category | Purpose | Example |
|----------|---------|---------|
| Render | Component mounts, shows initial state | ReviewPanel shows file list |
| Interaction | User actions trigger correct calls | Click file → `get_file_diff` once |
| State | UI updates after daemon responses | Badge appears when file changed |
| Guard | No loops, no duplicate calls | `toHaveLength(1)` after settling |
| Race | Out-of-order responses handled | File A response doesn't affect file B |

## Test Pattern

```typescript
describe('ReviewPanel', () => {
  let mockDaemon: MockDaemon;

  beforeEach(() => {
    mockDaemon = createMockDaemon();
  });

  it('fetches diff for first file exactly once', async () => {
    const gitStatus = createGitStatus(['src/App.tsx']);

    render(
      <MockDaemonProvider value={mockDaemon}>
        <ReviewPanel gitStatus={gitStatus} isOpen={true} {...props} />
      </MockDaemonProvider>
    );

    await waitFor(() => {
      expect(screen.getByText('src/App.tsx')).toBeVisible();
    });

    // Guard: exactly one fetch, not an infinite loop
    expect(mockDaemon.getCalls('get_file_diff')).toHaveLength(1);

    // Wait to ensure no more calls arrive
    await sleep(100);
    expect(mockDaemon.getCalls('get_file_diff')).toHaveLength(1);
  });
});
```

## When to Run Tests

- After any change to components using `useDaemon`
- After changes to `useDaemonSocket`
- Before declaring work complete

```bash
make test-frontend      # All frontend tests
pnpm test -- --watch    # Watch mode
pnpm test ReviewPanel   # Specific file
```

## Implementation Order

1. Mock foundation (`MockDaemonProvider`, `createMockDaemon`, call tracking)
2. Test utilities (`renderWithDaemon`, fixtures)
3. ReviewPanel tests (regression tests for recent bugs)
4. Expand to other components as touched
