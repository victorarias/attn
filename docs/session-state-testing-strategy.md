# Session State Testing Strategy

## Overview

This document defines a comprehensive testing strategy for session state management that can be executed without user interaction.

## Testing Layers

```
┌─────────────────────────────────────────────────────────────────┐
│  Layer 4: E2E Tests (Playwright)                                │
│  - Full app UI with mock daemon                                 │
├─────────────────────────────────────────────────────────────────┤
│  Layer 3: Integration Tests (Daemon + Socket)                   │
│  - Real daemon, direct socket commands                          │
├─────────────────────────────────────────────────────────────────┤
│  Layer 2: WebSocket Tests (Daemon → App)                        │
│  - WebSocket event broadcasting and receiving                   │
├─────────────────────────────────────────────────────────────────┤
│  Layer 1: Unit Tests                                            │
│  - Classifier, transcript parser, protocol parsing              │
└─────────────────────────────────────────────────────────────────┘
```

---

## Layer 1: Unit Tests

### 1.1 Classifier Tests (`internal/classifier/classifier_test.go`)

```go
func TestClassifier_ParseResponse(t *testing.T) {
    tests := []struct {
        input    string
        expected string
    }{
        {"WAITING", "waiting_input"},
        {"waiting", "waiting_input"},
        {"DONE", "idle"},
        {"done", "idle"},
        {"The response is WAITING for user input", "waiting_input"},
        {"Task is DONE", "idle"},
        {"", "idle"},
    }
    for _, tt := range tests {
        result := ParseResponse(tt.input)
        if result != tt.expected {
            t.Errorf("ParseResponse(%q) = %q, want %q", tt.input, result, tt.expected)
        }
    }
}

func TestClassifier_BuildPrompt(t *testing.T) {
    prompt := BuildPrompt("Hello, how are you?")
    if !strings.Contains(prompt, "Hello, how are you?") {
        t.Error("Prompt should contain input text")
    }
    if !strings.Contains(prompt, "WAITING") {
        t.Error("Prompt should mention WAITING")
    }
}
```

### 1.2 Transcript Parser Tests (`internal/transcript/transcript_test.go`)

```go
func TestTranscript_ExtractLastAssistantMessage(t *testing.T) {
    // Create temp JSONL file with test data
    // Test extraction works
    // Test empty transcript
    // Test no assistant messages
    // Test truncation at maxLength
}
```

### 1.3 Protocol Parser Tests (`internal/protocol/parse_test.go`)

Already exists - verify all message types parse correctly.

### Run Command:
```bash
go test ./internal/classifier ./internal/transcript ./internal/protocol -v
```

---

## Layer 2: Daemon Integration Tests

### 2.1 State Update via Socket (`internal/daemon/daemon_test.go`)

```go
func TestDaemon_StateUpdate_Working(t *testing.T) {
    // 1. Start daemon
    // 2. Register session
    // 3. Send state=working
    // 4. Query session, verify state=working
}

func TestDaemon_StateUpdate_WaitingInput(t *testing.T) {
    // 1. Start daemon
    // 2. Register session
    // 3. Send state=waiting_input
    // 4. Query session, verify state=waiting_input
}

func TestDaemon_StateUpdate_Idle(t *testing.T) {
    // 1. Start daemon
    // 2. Register session
    // 3. Send state=idle
    // 4. Query session, verify state=idle
}
```

### 2.2 Stop Command with Classification

```go
func TestDaemon_StopCommand_WithPendingTodos(t *testing.T) {
    // 1. Start daemon
    // 2. Register session
    // 3. Send todos with pending items
    // 4. Send stop command (any transcript)
    // 5. Verify state=waiting_input (due to pending todos)
}

func TestDaemon_StopCommand_AllTodosCompleted(t *testing.T) {
    // 1. Start daemon
    // 2. Register session
    // 3. Send todos with all completed ([✓])
    // 4. Send stop command
    // 5. Verify classification runs (not short-circuited by todos)
}
```

### 2.3 WebSocket Broadcast Verification

```go
func TestDaemon_WebSocket_BroadcastsStateChange(t *testing.T) {
    // 1. Start daemon with WebSocket
    // 2. Connect WebSocket client
    // 3. Read initial state
    // 4. Register session via unix socket
    // 5. Verify WebSocket receives session_registered event
    // 6. Send state update via unix socket
    // 7. Verify WebSocket receives session_state_changed event
    // 8. Verify event contains correct state
}
```

### Run Command:
```bash
go test ./internal/daemon -v -run "TestDaemon_State|TestDaemon_Stop|TestDaemon_WebSocket"
```

---

## Layer 3: CLI Integration Tests

### 3.1 Manual Socket Test Script

Create `test/socket_test.sh`:
```bash
#!/bin/bash
set -e

SOCKET=~/.attn.sock
SESSION_ID="test-$(date +%s)"

echo "=== Testing State Management ==="

# 1. Register session
echo "1. Register session..."
RESULT=$(echo "{\"cmd\":\"register\",\"id\":\"$SESSION_ID\",\"label\":\"test\",\"cwd\":\"/tmp\"}" | nc -U $SOCKET)
echo "   Result: $RESULT"
[[ "$RESULT" == *'"ok":true'* ]] || { echo "FAIL: Register"; exit 1; }

# 2. Verify initial state
echo "2. Query initial state..."
RESULT=$(echo "{\"cmd\":\"query\",\"state\":\"\"}" | nc -U $SOCKET)
echo "   Result: $RESULT"
[[ "$RESULT" == *'"state":"working"'* ]] || { echo "FAIL: Initial state not working"; exit 1; }

# 3. Update to waiting_input
echo "3. Update to waiting_input..."
RESULT=$(echo "{\"cmd\":\"state\",\"id\":\"$SESSION_ID\",\"state\":\"waiting_input\"}" | nc -U $SOCKET)
echo "   Result: $RESULT"
[[ "$RESULT" == *'"ok":true'* ]] || { echo "FAIL: State update"; exit 1; }

# 4. Verify state changed
echo "4. Verify state changed..."
RESULT=$(echo "{\"cmd\":\"query\",\"state\":\"\"}" | nc -U $SOCKET)
echo "   Result: $RESULT"
[[ "$RESULT" == *'"state":"waiting_input"'* ]] || { echo "FAIL: State not waiting_input"; exit 1; }

# 5. Update to idle
echo "5. Update to idle..."
RESULT=$(echo "{\"cmd\":\"state\",\"id\":\"$SESSION_ID\",\"state\":\"idle\"}" | nc -U $SOCKET)
RESULT=$(echo "{\"cmd\":\"query\",\"state\":\"\"}" | nc -U $SOCKET)
[[ "$RESULT" == *'"state":"idle"'* ]] || { echo "FAIL: State not idle"; exit 1; }

# 6. Update back to working
echo "6. Update to working..."
RESULT=$(echo "{\"cmd\":\"state\",\"id\":\"$SESSION_ID\",\"state\":\"working\"}" | nc -U $SOCKET)
RESULT=$(echo "{\"cmd\":\"query\",\"state\":\"\"}" | nc -U $SOCKET)
[[ "$RESULT" == *'"state":"working"'* ]] || { echo "FAIL: State not working"; exit 1; }

# 7. Test todos affecting classification
echo "7. Add pending todos..."
RESULT=$(echo "{\"cmd\":\"todos\",\"id\":\"$SESSION_ID\",\"todos\":[\"[ ] Task 1\",\"[ ] Task 2\"]}" | nc -U $SOCKET)
[[ "$RESULT" == *'"ok":true'* ]] || { echo "FAIL: Todos update"; exit 1; }

# 8. Cleanup
echo "8. Unregister session..."
RESULT=$(echo "{\"cmd\":\"unregister\",\"id\":\"$SESSION_ID\"}" | nc -U $SOCKET)
[[ "$RESULT" == *'"ok":true'* ]] || { echo "FAIL: Unregister"; exit 1; }

echo ""
echo "=== ALL TESTS PASSED ==="
```

### Run Command:
```bash
chmod +x test/socket_test.sh && ./test/socket_test.sh
```

---

## Layer 4: E2E Tests (Playwright)

### 4.1 Current E2E Tests (`app/e2e/session-states.spec.ts`)

These tests inject sessions directly into the UI store, bypassing daemon. They test:
- UI renders states correctly
- State colors match spec
- Real-time updates work
- Sessions move between groups

### 4.2 Full Flow E2E Tests (New)

Create `app/e2e/session-state-flow.spec.ts`:
```typescript
import { test, expect } from './fixtures';

test.describe('Session State Full Flow', () => {
  test('state update via daemon socket reflects in UI', async ({ page, daemon }) => {
    await daemon.start();
    await page.goto('/');

    // 1. Inject session via daemon (not UI store)
    await daemon.injectSession({
      id: 'flow-test-1',
      label: 'flow-test',
      state: 'working',
      cwd: '/tmp',
    });

    // 2. Wait for UI to show session
    const session = page.locator('[data-testid="session-flow-test-1"]');
    await expect(session).toBeVisible();
    await expect(session).toHaveAttribute('data-state', 'working');

    // 3. Update state via daemon socket
    await daemon.updateSessionState('flow-test-1', 'waiting_input');

    // 4. Verify UI updates
    await expect(session).toHaveAttribute('data-state', 'waiting_input');

    // 5. Update to idle
    await daemon.updateSessionState('flow-test-1', 'idle');
    await expect(session).toHaveAttribute('data-state', 'idle');
  });

  test('WebSocket reconnection preserves state', async ({ page, daemon }) => {
    // Test WebSocket disconnect/reconnect scenarios
  });
});
```

### 4.3 Fixture Enhancement

Update `app/e2e/fixtures.ts` to support direct daemon socket commands:
```typescript
daemon: {
  start: () => Promise<void>,
  stop: () => void,
  injectSession: (session) => Promise<void>,  // via unix socket
  updateSessionState: (id, state) => Promise<void>,  // via unix socket
  sendCommand: (cmd) => Promise<string>,  // generic socket command
}
```

### Run Command:
```bash
cd app && pnpm run e2e
```

---

## Automated Test Runner

Create `test/run-all-state-tests.sh`:
```bash
#!/bin/bash
set -e

echo "=========================================="
echo "Session State Management Test Suite"
echo "=========================================="

cd "$(dirname "$0")/.."

echo ""
echo "=== Layer 1: Unit Tests ==="
go test ./internal/classifier ./internal/transcript ./internal/protocol -v

echo ""
echo "=== Layer 2: Daemon Integration Tests ==="
go test ./internal/daemon -v -run "TestDaemon_State|TestDaemon_Stop|TestDaemon_WebSocket"

echo ""
echo "=== Layer 3: Socket Integration Tests ==="
# Ensure daemon is running
pkill -f "attn daemon" 2>/dev/null || true
./attn daemon &
DAEMON_PID=$!
sleep 1
./test/socket_test.sh
kill $DAEMON_PID 2>/dev/null

echo ""
echo "=== Layer 4: E2E Tests ==="
cd app && pnpm run e2e --grep "session"

echo ""
echo "=========================================="
echo "ALL TESTS PASSED"
echo "=========================================="
```

---

## Test Coverage Matrix

| Flow | Unit | Daemon | Socket | E2E |
|------|------|--------|--------|-----|
| Register session | - | ✓ | ✓ | ✓ |
| Unregister session | - | ✓ | ✓ | - |
| State → working | - | ✓ | ✓ | ✓ |
| State → waiting_input | - | ✓ | ✓ | ✓ |
| State → idle | - | ✓ | ✓ | ✓ |
| Stop with todos | - | ✓ | - | - |
| Stop without todos | - | ✓ | - | - |
| Classifier parse | ✓ | - | - | - |
| Transcript parse | ✓ | - | - | - |
| WebSocket broadcast | - | ✓ | - | - |
| UI state colors | - | - | - | ✓ |
| UI grouping | - | - | - | ✓ |
| UI real-time update | - | - | - | ✓ |

---

## Debugging Checklist

When state isn't updating:

1. **Check daemon is running:**
   ```bash
   pgrep -f "attn daemon" && echo "Running" || echo "NOT RUNNING"
   ```

2. **Check daemon logs:**
   ```bash
   tail -50 ~/.attn/daemon.log
   ```

3. **Test socket directly:**
   ```bash
   echo '{"cmd":"query","state":""}' | nc -U ~/.attn.sock
   ```

4. **Test state update:**
   ```bash
   echo '{"cmd":"state","id":"test","state":"working"}' | nc -U ~/.attn.sock
   ```

5. **Check WebSocket:**
   ```bash
   # In browser console:
   const ws = new WebSocket('ws://127.0.0.1:9849/ws');
   ws.onmessage = (e) => console.log(JSON.parse(e.data));
   ```

6. **Check hooks config:**
   ```bash
   ls -la /tmp/attn-hooks-*.json
   cat /tmp/attn-hooks-*.json
   ```

---

## Known Issues to Test

1. **Session ID mismatch:** Hook uses different ID than registered session
2. **Todos not cleared:** Completed todos still counted as pending
3. **WebSocket timeout:** 60s read timeout causes disconnects
4. **Classifier timeout:** 30s timeout may be too short
5. **Transcript not found:** File doesn't exist or wrong path
