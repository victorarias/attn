#!/bin/bash
# Session State Management Test Suite
# Runs all state-related tests across all layers
set -e

cd "$(dirname "$0")/.."

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
BLUE='\033[0;34m'
NC='\033[0m'

section() {
    echo ""
    echo -e "${BLUE}===========================================${NC}"
    echo -e "${BLUE}$1${NC}"
    echo -e "${BLUE}===========================================${NC}"
}

success() {
    echo -e "${GREEN}✓ $1${NC}"
}

error() {
    echo -e "${RED}✗ $1${NC}"
}

section "Session State Management Test Suite"

# Layer 1: Classifier Unit Tests
section "Layer 1: Classifier Unit Tests"
if go test ./internal/classifier -v; then
    success "Classifier tests passed"
else
    error "Classifier tests failed"
    exit 1
fi

# Layer 2: Protocol Tests
section "Layer 2: Protocol Tests"
if go test ./internal/protocol -v; then
    success "Protocol tests passed"
else
    error "Protocol tests failed"
    exit 1
fi

# Layer 3: Daemon Integration Tests (State-related)
section "Layer 3: Daemon Integration Tests"
if go test ./internal/daemon -v -run "TestDaemon_State|TestDaemon_Stop|TestDaemon_Inject"; then
    success "Daemon state tests passed"
else
    error "Daemon state tests failed"
    exit 1
fi

# Layer 4: Socket Integration (requires running daemon)
section "Layer 4: Socket Integration Tests"
SOCKET=~/.attn.sock

if [ -S "$SOCKET" ]; then
    if ./test/socket_test.sh; then
        success "Socket tests passed"
    else
        error "Socket tests failed"
        exit 1
    fi
else
    echo -e "${YELLOW}⚠ Daemon not running - skipping socket tests${NC}"
    echo "  Run 'attn daemon' in another terminal to enable socket tests"
fi

# Layer 5: E2E Tests (if app is available)
section "Layer 5: E2E Tests"
if [ -d "app" ]; then
    echo "Running E2E tests for session states..."
    cd app
    if pnpm run e2e -- --grep "session"; then
        success "E2E tests passed"
    else
        error "E2E tests failed"
        exit 1
    fi
    cd ..
else
    echo -e "${YELLOW}⚠ App directory not found - skipping E2E tests${NC}"
fi

section "ALL TESTS COMPLETED SUCCESSFULLY"
echo ""
echo "Test Coverage Summary:"
echo "  ✓ Classifier parsing and prompt building"
echo "  ✓ Protocol message handling"
echo "  ✓ Daemon state updates and WebSocket broadcasts"
echo "  ✓ Socket command processing (if daemon running)"
echo "  ✓ E2E daemon→WebSocket→UI flow (if app available)"
echo ""
