package daemon

import (
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"

	"github.com/victorarias/attn/internal/appbuild"
	"github.com/victorarias/attn/internal/apps"
)

// The bundle route: how a built view's bytes reach the frontend.
//
// It sits on the same mux as /ws and /health because that is the listener the
// app already talks to. What it serves is a content-addressed artifact — the
// path names the app, the version's content hash and the view — so the URL is
// immutable by construction: a new version is a different URL rather than a
// cache to bust, and `immutable` caching is honest rather than a promise
// somebody has to keep.
//
// Design: docs/plans/2026-08-13-ext-a5-ui-host-and-app-sdk.md, "The bundle is
// imported by URL".

// appBundleRoutePrefix is where the route is mounted. Exported through
// AppBundleURLPath below rather than as a raw string, so nothing composes a path
// by hand.
const appBundleRoutePrefix = "/apps/bundle/"

// appBundleMaxAge is a year in seconds — the conventional ceiling for
// `immutable` content, and what every artifact under this route is: the hash in
// its own path is a digest of its bytes, so the URL cannot outlive the content
// it names.
const appBundleMaxAge = 31536000

// contentHashRe is the shape of a version's identity: a sha256 digest, hex. It
// is checked before the path is touched, so a hash from the wire can never
// become a directory traversal — the app and view names are validated by
// internal/apps for the same reason.
var contentHashRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

// AppBundleURLPath is the path a view's module is served at. The daemon that
// serves it and the frontend that imports it both derive it here rather than
// each writing the same string.
func AppBundleURLPath(app, contentHash, view string) string {
	return appBundleRoutePrefix + app + "/" + contentHash + "/" + view + ".js"
}

// handleAppBundle serves one built view.
//
// Every segment is validated before it reaches the filesystem, and the artifact
// path is derived by appbuild rather than joined here: there is exactly one
// place a view's bytes can be, and both the builder and this handler have to
// agree on it or a rollback serves the wrong module.
func (d *Daemon) handleAppBundle(w http.ResponseWriter, r *http.Request) {
	// A module script is fetched in CORS mode from tauri://localhost, which is a
	// different origin from this daemon's port. `*` is the whole allowance: the
	// route serves immutable public artifacts and reads no credentials, so there
	// is nothing an origin could be trusted with that another could not.
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "the app bundle route serves GET and HEAD", http.StatusMethodNotAllowed)
		return
	}

	app, hash, view, err := parseAppBundlePath(r.URL.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	path := appbuild.ViewArtifactPath(d.appsDir, app, hash, view)
	file, err := os.Open(path)
	if err != nil {
		// Naming the path and the command that explains it: the realistic way to
		// get here is a tile still pointing at a version whose artifacts were
		// removed, and the reader needs to know which version that was.
		http.Error(w, fmt.Sprintf(
			"no built view %q of app %q at version %s (looked for %s); `attn app status %s` shows the version it serves now",
			view, app, appbuild.ShortHash(hash), path, app), http.StatusNotFound)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.IsDir() {
		http.Error(w, fmt.Sprintf("the built view %q of app %q is not a readable file at %s", view, app, path), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d, immutable", appBundleMaxAge))
	// ServeContent writes Content-Length, handles HEAD and answers range
	// requests; the modtime is the artifact's, which is stable because the store
	// never rewrites a hash directory it already holds.
	http.ServeContent(w, r, view+".js", info.ModTime(), file)
}

// parseAppBundlePath splits the route's three segments and validates each one by
// the rule that owns it.
func parseAppBundlePath(urlPath string) (app, hash, view string, err error) {
	rest := strings.TrimPrefix(urlPath, appBundleRoutePrefix)
	if rest == urlPath {
		return "", "", "", fmt.Errorf("an app bundle path starts with %s", appBundleRoutePrefix)
	}
	parts := strings.Split(rest, "/")
	if len(parts) != 3 {
		return "", "", "", fmt.Errorf(
			"an app bundle path is %s<app>/<content-hash>/<view>.js; %q has %d segments after the prefix, not 3",
			appBundleRoutePrefix, urlPath, len(parts))
	}
	app, hash, file := parts[0], parts[1], parts[2]
	if err := apps.ValidateName(app); err != nil {
		return "", "", "", err
	}
	if !contentHashRe.MatchString(hash) {
		return "", "", "", fmt.Errorf("%q is not a version content hash (64 hex characters)", hash)
	}
	view, ok := strings.CutSuffix(file, ".js")
	if !ok {
		return "", "", "", fmt.Errorf("a view is served as <view>.js; %q has no .js suffix", file)
	}
	if err := apps.ValidateViewName(view); err != nil {
		return "", "", "", err
	}
	return app, hash, view, nil
}
