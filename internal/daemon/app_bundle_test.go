package daemon

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/appbuild"
)

// The bundle route. What these pin is that the URL is the whole contract: the
// path is content-addressed, so it may be cached forever and may be fetched
// from another origin, and every segment of it is validated before anything
// touches the filesystem.

const bundleTestHash = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func seedViewArtifact(t *testing.T, d *Daemon, app, hash, view, content string) {
	t.Helper()
	path := appbuild.ViewArtifactPath(d.appsDir, app, hash, view)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create the view directory for %s/%s: %v", app, view, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write the view artifact for %s/%s: %v", app, view, err)
	}
}

func getBundle(t *testing.T, d *Daemon, method, path string) *http.Response {
	t.Helper()
	rec := httptest.NewRecorder()
	d.handleAppBundle(rec, httptest.NewRequest(method, path, nil))
	return rec.Result()
}

func TestBundleRouteServesAViewCacheableForeverAndCrossOrigin(t *testing.T) {
	d := newAppDaemon(t)
	seedViewArtifact(t, d, "reviewer", bundleTestHash, "approvals", "export default function View() {}\n")

	resp := getBundle(t, d, http.MethodGet, AppBundleURLPath("reviewer", bundleTestHash, "approvals"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("serving a built view: status %d", resp.StatusCode)
	}
	body := make([]byte, resp.ContentLength)
	if _, err := resp.Body.Read(body); err != nil && err.Error() != "EOF" {
		t.Fatalf("read the served module: %v", err)
	}
	if !strings.Contains(string(body), "export default") {
		t.Fatalf("the served module is not the artifact: %q", string(body))
	}
	// A module script is fetched in CORS mode from tauri://localhost. Without
	// this header the import fails before the module is ever parsed.
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("Access-Control-Allow-Origin is %q, so a tauri://localhost import cannot read it", got)
	}
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/javascript") {
		t.Fatalf("Content-Type is %q; a module script has to be served as JavaScript", got)
	}
	cache := resp.Header.Get("Cache-Control")
	if !strings.Contains(cache, "immutable") || !strings.Contains(cache, "max-age=31536000") {
		t.Fatalf("Cache-Control is %q; the path is content-addressed and must be cacheable forever", cache)
	}
}

func TestBundleRouteAnswersThePreflightAndRefusesWrites(t *testing.T) {
	d := newAppDaemon(t)
	path := AppBundleURLPath("reviewer", bundleTestHash, "approvals")

	resp := getBundle(t, d, http.MethodOptions, path)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("preflight: status %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("preflight Access-Control-Allow-Origin is %q", got)
	}

	resp = getBundle(t, d, http.MethodPost, path)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST to the bundle route: status %d, want 405", resp.StatusCode)
	}
}

func TestBundleRouteRefusesAPathItCannotValidateBeforeTouchingDisk(t *testing.T) {
	d := newAppDaemon(t)
	// The escape it must not permit: a hash segment that walks out of the store.
	// It is refused by shape, so no path is ever built from it.
	traversal := appBundleRoutePrefix + "reviewer/" + "../../../../etc/approvals.js"
	if _, _, _, err := parseAppBundlePath(traversal); err == nil {
		t.Fatalf("a traversal path parsed: %q", traversal)
	}

	cases := []struct{ name, path string }{
		{"a hash that is not a digest", appBundleRoutePrefix + "reviewer/latest/approvals.js"},
		{"an app name the rule refuses", appBundleRoutePrefix + "Reviewer/" + bundleTestHash + "/approvals.js"},
		{"a view name the rule refuses", appBundleRoutePrefix + "reviewer/" + bundleTestHash + "/Approvals.js"},
		{"no .js suffix", appBundleRoutePrefix + "reviewer/" + bundleTestHash + "/approvals"},
		{"a fourth segment", appBundleRoutePrefix + "reviewer/" + bundleTestHash + "/views/approvals.js"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := getBundle(t, d, http.MethodGet, tc.path)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("%s: status %d, want 400", tc.path, resp.StatusCode)
			}
		})
	}
}

func TestBundleRouteNamesTheVersionWhenTheArtifactIsGone(t *testing.T) {
	d := newAppDaemon(t)
	// A name nothing in this package ever seeds: the artifact store is one temp
	// dir for the whole package, so "never applied" has to be spelled out.
	resp := getBundle(t, d, http.MethodGet, AppBundleURLPath("neverapplied", bundleTestHash, "approvals"))
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("a missing artifact: status %d, want 404", resp.StatusCode)
	}
	body := make([]byte, 1024)
	n, _ := resp.Body.Read(body)
	text := string(body[:n])
	// A tile pointing at a version whose artifacts were removed is the realistic
	// way here, so the reader is told which version and where to look next.
	for _, want := range []string{"approvals", "neverapplied", appbuild.ShortHash(bundleTestHash), "attn app status neverapplied"} {
		if !strings.Contains(text, want) {
			t.Fatalf("the 404 does not name %q: %s", want, text)
		}
	}
}
