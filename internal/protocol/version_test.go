package protocol

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFrontendProtocolVersionMatchesDaemon(t *testing.T) {
	hookPath := filepath.Join("..", "..", "app", "src", "hooks", "useDaemonSocket.ts")
	data, err := os.ReadFile(hookPath)
	if err != nil {
		t.Fatalf("read frontend daemon hook: %v", err)
	}

	want := fmt.Sprintf("const PROTOCOL_VERSION = '%s';", ProtocolVersion)
	if !strings.Contains(string(data), want) {
		t.Fatalf("frontend daemon hook must contain %q", want)
	}
}
