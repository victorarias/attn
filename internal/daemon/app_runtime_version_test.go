package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The daemon and the host each hold the API version as their own constant, and
// nothing else in the build compares them: the host is a compiled binary the
// daemon meets at hello, so a version that moved on one side and not the other
// is discovered as a refused runtime — or, worse, as a served one whose contract
// has quietly changed. This is the witness the frontend's protocol version has
// and this one did not.
func TestAppRuntimeAPIVersionMatchesTheHost(t *testing.T) {
	hostPath := filepath.Join("..", "..", "apphost", "src", "index.ts")
	data, err := os.ReadFile(hostPath)
	if err != nil {
		t.Fatalf("read the app runtime host: %v", err)
	}
	want := fmt.Sprintf("const APP_RUNTIME_API_VERSION = %d", appRuntimeAPIVersion)
	if !strings.Contains(string(data), want) {
		t.Fatalf("%s must contain %q — the daemon speaks version %d", hostPath, want, appRuntimeAPIVersion)
	}
}
