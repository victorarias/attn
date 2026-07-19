package git

import (
	"fmt"
	"os"
	"path/filepath"
)

// Clone clones a repository to the specified path.
// Returns error if path already exists or clone fails.
func Clone(cloneURL, targetPath string) error {
	return cloneWithHTTPAuthorization(cloneURL, targetPath, "")
}

func cloneWithHTTPAuthorization(cloneURL, targetPath, authorization string) error {
	targetPath = ExpandPath(targetPath)
	var err error
	authorization, err = authorizationForGitURL(cloneURL, authorization)
	if err != nil {
		return err
	}

	// Check if target already exists
	if _, err := os.Stat(targetPath); err == nil {
		return fmt.Errorf("target path already exists: %s", targetPath)
	}

	// Ensure parent directory exists
	parentDir := filepath.Dir(targetPath)
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		return fmt.Errorf("failed to create parent directory: %w", err)
	}

	if out, err := runGitCombinedWithHTTPAuthorization(OpClone, "", cloneURL, authorization, "clone", cloneURL, targetPath); err != nil {
		return fmt.Errorf("git clone failed: %s", string(out))
	}

	return nil
}

// EnsureRepo ensures a repository exists at the target path.
// If it doesn't exist, clones from cloneURL.
// Returns (cloned bool, error).
func EnsureRepo(cloneURL, targetPath string) (bool, error) {
	targetPath = ExpandPath(targetPath)

	// Check if repo already exists
	if isGitRepo(targetPath) {
		return false, nil
	}

	// Clone the repo
	if err := Clone(cloneURL, targetPath); err != nil {
		return false, err
	}

	return true, nil
}
