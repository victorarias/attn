package git

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

func RepositoryCacheKey(identity string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strings.ToLower(strings.TrimSpace(identity))))
}

// ValidateLocalClone resolves an explicitly configured repository root without
// ResolveRepoDir's sibling-search convenience. Invalid overrides are failures,
// never a signal to fall back to the managed cache.
func ValidateLocalClone(path, expectedIdentity string) (string, error) {
	path = CanonicalizePath(path)
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("local clone: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("local clone is not a directory: %s", path)
	}
	root, err := GetRepoRoot(path)
	if err != nil || !sameDirectory(root, path) {
		return "", fmt.Errorf("local clone is not a repository root: %s", path)
	}
	mainRepo := ResolveMainRepoPath(root)
	host, ownerRepo := OriginHostOwnerRepo(mainRepo)
	identity := strings.ToLower(host + "/" + ownerRepo)
	if identity != strings.ToLower(expectedIdentity) {
		return "", fmt.Errorf("local clone origin mismatch: got %s want %s", identity, strings.ToLower(expectedIdentity))
	}
	remoteURL, err := runGitOutput(OpMetadata, mainRepo, "remote", "get-url", "origin")
	if err != nil {
		return "", fmt.Errorf("read local clone origin: %w", err)
	}
	if _, err := authorizationForGitURL(strings.TrimSpace(string(remoteURL)), "validation"); err != nil {
		return "", err
	}
	return mainRepo, nil
}

// authorizationForGitURL returns an HTTP authorization header only for HTTPS.
// SSH/scp transports use their own credentials; plaintext HTTP is rejected so a
// host token can never be attached to a cleartext request.
func authorizationForGitURL(rawURL, authorization string) (string, error) {
	if authorization == "" {
		return "", nil
	}
	if !strings.Contains(rawURL, "://") {
		return "", nil // scp-like SSH remote
	}
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", fmt.Errorf("parse git remote URL: %w", err)
	}
	switch strings.ToLower(parsed.Scheme) {
	case "https":
		return authorization, nil
	case "http":
		return "", errors.New("refusing authenticated Git access over plaintext HTTP")
	default:
		return "", nil
	}
}

// EnsureManagedClone atomically installs a non-bare clone at target and validates
// every adoption against the configured repository identity.
func EnsureManagedClone(cloneURL, target, expectedIdentity, authorization string) (string, bool, error) {
	if _, err := os.Stat(target); err == nil {
		mainRepo, err := ValidateLocalClone(target, expectedIdentity)
		return mainRepo, false, err
	} else if !os.IsNotExist(err) {
		return "", false, fmt.Errorf("inspect managed clone: %w", err)
	}
	parent := filepath.Dir(target)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return "", false, fmt.Errorf("create managed clone parent: %w", err)
	}
	stagingRoot, err := os.MkdirTemp(parent, ".clone-*")
	if err != nil {
		return "", false, fmt.Errorf("create managed clone staging directory: %w", err)
	}
	defer os.RemoveAll(stagingRoot)
	staging := filepath.Join(stagingRoot, "repo")
	if err := cloneWithHTTPAuthorization(cloneURL, staging, authorization); err != nil {
		return "", false, err
	}
	mainRepo, err := ValidateLocalClone(staging, expectedIdentity)
	if err != nil {
		return "", false, err
	}
	if err := os.Rename(staging, target); err != nil {
		return "", false, fmt.Errorf("publish managed clone: %w", err)
	}
	return strings.Replace(mainRepo, staging, target, 1), true, nil
}
