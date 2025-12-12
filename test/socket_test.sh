#!/bin/bash
# Socket Integration Test Script
# Tests daemon socket communication directly without Go test overhead
set -e

SOCKET=~/.attn.sock
SESSION_ID="socket-test-$(date +%s)"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
NC='\033[0m' # No Color

pass() {
    echo -e "${GREEN}✓${NC} $1"
}

fail() {
    echo -e "${RED}✗${NC} $1"
    exit 1
}

info() {
    echo -e "${YELLOW}→${NC} $1"
}

echo "=== Session State Socket Integration Tests ==="
echo ""

# Check if daemon socket exists
if [ ! -S "$SOCKET" ]; then
    fail "Daemon socket not found at $SOCKET. Is the daemon running?"
fi

# 1. Register session
info "1. Register session..."
RESULT=$(echo "{\"cmd\":\"register\",\"id\":\"$SESSION_ID\",\"label\":\"Socket Test\",\"cwd\":\"/tmp/test\"}" | nc -U $SOCKET)
if [[ "$RESULT" == *'"ok":true'* ]]; then
    pass "Register session"
else
    fail "Register failed: $RESULT"
fi

# 2. Query initial state (should be working)
info "2. Query initial state..."
RESULT=$(echo "{\"cmd\":\"query\",\"state\":\"\"}" | nc -U $SOCKET)
if [[ "$RESULT" == *"\"$SESSION_ID\""* ]] && [[ "$RESULT" == *'"state":"working"'* ]]; then
    pass "Initial state is working"
else
    fail "Initial state query failed: $RESULT"
fi

# 3. Update to waiting_input
info "3. Update to waiting_input..."
RESULT=$(echo "{\"cmd\":\"state\",\"id\":\"$SESSION_ID\",\"state\":\"waiting_input\"}" | nc -U $SOCKET)
if [[ "$RESULT" == *'"ok":true'* ]]; then
    pass "State update to waiting_input"
else
    fail "State update failed: $RESULT"
fi

# 4. Verify state changed
info "4. Verify state changed..."
RESULT=$(echo "{\"cmd\":\"query\",\"state\":\"\"}" | nc -U $SOCKET)
if [[ "$RESULT" == *'"state":"waiting_input"'* ]]; then
    pass "State is waiting_input"
else
    fail "State verification failed: $RESULT"
fi

# 5. Update to idle
info "5. Update to idle..."
RESULT=$(echo "{\"cmd\":\"state\",\"id\":\"$SESSION_ID\",\"state\":\"idle\"}" | nc -U $SOCKET)
RESULT=$(echo "{\"cmd\":\"query\",\"state\":\"\"}" | nc -U $SOCKET)
if [[ "$RESULT" == *'"state":"idle"'* ]]; then
    pass "State is idle"
else
    fail "State not idle: $RESULT"
fi

# 6. Update back to working
info "6. Update to working..."
RESULT=$(echo "{\"cmd\":\"state\",\"id\":\"$SESSION_ID\",\"state\":\"working\"}" | nc -U $SOCKET)
RESULT=$(echo "{\"cmd\":\"query\",\"state\":\"\"}" | nc -U $SOCKET)
if [[ "$RESULT" == *'"state":"working"'* ]]; then
    pass "State is working"
else
    fail "State not working: $RESULT"
fi

# 7. Test todos update
info "7. Add todos..."
RESULT=$(echo "{\"cmd\":\"todos\",\"id\":\"$SESSION_ID\",\"todos\":[\"[ ] Task 1\",\"[ ] Task 2\"]}" | nc -U $SOCKET)
if [[ "$RESULT" == *'"ok":true'* ]]; then
    pass "Todos updated"
else
    fail "Todos update failed: $RESULT"
fi

# 8. Unregister session
info "8. Unregister session..."
RESULT=$(echo "{\"cmd\":\"unregister\",\"id\":\"$SESSION_ID\"}" | nc -U $SOCKET)
if [[ "$RESULT" == *'"ok":true'* ]]; then
    pass "Unregister session"
else
    fail "Unregister failed: $RESULT"
fi

# 9. Verify session is gone
info "9. Verify session removed..."
RESULT=$(echo "{\"cmd\":\"query\",\"state\":\"\"}" | nc -U $SOCKET)
if [[ "$RESULT" != *"\"$SESSION_ID\""* ]]; then
    pass "Session removed"
else
    fail "Session still exists: $RESULT"
fi

echo ""
echo -e "${GREEN}=== ALL SOCKET TESTS PASSED ===${NC}"
