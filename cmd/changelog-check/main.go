// changelog-check validates the pending changelog fragments under
// changelog.d/. It is run by the CI changelog gate (scripts/changelog-gate.sh)
// and can be run locally with `go run ./cmd/changelog-check`.
//
// Fragments are raw material for the release-time changelog compilation, not
// final copy. docs/making-a-release.md describes the format and workflow.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

type fragment struct {
	Kind    string `yaml:"kind"`
	Area    string `yaml:"area"`
	Change  string `yaml:"change"`
	Symptom string `yaml:"symptom"`
	Notes   string `yaml:"notes"`
}

var validKinds = []string{"added", "changed", "fixed", "removed", "internal"}

func validateFragment(data []byte) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var f fragment
	if err := dec.Decode(&f); err != nil {
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("fragment is empty")
		}
		return err
	}
	if !slices.Contains(validKinds, f.Kind) {
		return fmt.Errorf("kind must be one of %s (got %q)", strings.Join(validKinds, ", "), f.Kind)
	}
	if strings.TrimSpace(f.Area) == "" {
		return fmt.Errorf("area is required")
	}
	if strings.TrimSpace(f.Change) == "" {
		return fmt.Errorf("change is required")
	}
	return nil
}

// validateDir checks every entry in the fragments directory: README.md is
// ignored, everything else must be a .yaml fragment that parses and passes
// validateFragment.
func validateDir(dir string) []error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return []error{err}
	}
	var errs []error
	for _, e := range entries {
		name := e.Name()
		if name == "README.md" {
			continue
		}
		path := filepath.Join(dir, name)
		// Type() comes from lstat: a symlink is reported as a symlink, not as
		// its target. Anything but a regular file is rejected so the compile
		// step can never be pointed at a file outside changelog.d/.
		if !e.Type().IsRegular() {
			errs = append(errs, fmt.Errorf("%s: fragments must be regular files (not directories, symlinks, or other special files)", path))
			continue
		}
		if !strings.HasSuffix(name, ".yaml") {
			errs = append(errs, fmt.Errorf("%s: fragments must be .yaml files", path))
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", path, err))
			continue
		}
		if err := validateFragment(data); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", path, err))
		}
	}
	return errs
}

func main() {
	dir := "changelog.d"
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	errs := validateDir(dir)
	for _, err := range errs {
		fmt.Fprintln(os.Stderr, "changelog-check:", err)
	}
	if len(errs) > 0 {
		fmt.Fprintln(os.Stderr, "see docs/making-a-release.md for the fragment format")
		os.Exit(1)
	}
}
