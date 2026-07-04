// Package present parses and validates Present manifests: a small YAML
// description of a set of git changes to be presented for review.
package present

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Manifest is the top-level Present manifest (v0, kind "changes").
type Manifest struct {
	Version int         `yaml:"version"`
	Kind    string      `yaml:"kind"`
	Title   string      `yaml:"title"`
	Frame   Frame       `yaml:"frame"`
	Summary string      `yaml:"summary,omitempty"`
	Files   []FileEntry `yaml:"files,omitempty"`
	Skip    []string    `yaml:"skip,omitempty"`
}

// Frame identifies the repo and git refs a manifest presents changes between.
type Frame struct {
	Repo string `yaml:"repo"`
	Base string `yaml:"base"`
	Head string `yaml:"head"`
}

// FileEntry is one file called out in the manifest's reading order.
type FileEntry struct {
	Path string `yaml:"path"`
	Note string `yaml:"note,omitempty"`
}

// ParseManifest strictly decodes and validates a Present manifest from YAML
// bytes. Unknown fields are rejected.
func ParseManifest(data []byte) (*Manifest, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)

	var m Manifest
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("present: decode manifest: %w", err)
	}

	if err := validate(&m); err != nil {
		return nil, err
	}

	return &m, nil
}

// ParseManifestFile reads and parses a Present manifest from a file path.
func ParseManifestFile(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("present: read manifest %q: %w", path, err)
	}
	return ParseManifest(data)
}

func validate(m *Manifest) error {
	if m.Version != 1 {
		return fmt.Errorf("present: version must be 1, got %d", m.Version)
	}
	if m.Kind != "changes" {
		return fmt.Errorf("present: kind must be \"changes\", got %q", m.Kind)
	}
	if strings.TrimSpace(m.Title) == "" {
		return fmt.Errorf("present: title is required")
	}
	if strings.TrimSpace(m.Frame.Repo) == "" {
		return fmt.Errorf("present: frame.repo is required")
	}
	if !filepath.IsAbs(m.Frame.Repo) {
		return fmt.Errorf("present: frame.repo must be an absolute path, got %q", m.Frame.Repo)
	}
	if strings.TrimSpace(m.Frame.Base) == "" {
		return fmt.Errorf("present: frame.base is required")
	}
	if strings.TrimSpace(m.Frame.Head) == "" {
		return fmt.Errorf("present: frame.head is required")
	}

	seenFiles := make(map[string]bool, len(m.Files))
	for i, f := range m.Files {
		if err := validatePath(f.Path, fmt.Sprintf("files[%d].path", i)); err != nil {
			return err
		}
		if seenFiles[f.Path] {
			return fmt.Errorf("present: files[%d].path is a duplicate: %q", i, f.Path)
		}
		seenFiles[f.Path] = true
	}

	seenSkip := make(map[string]bool, len(m.Skip))
	for i, p := range m.Skip {
		if err := validatePath(p, fmt.Sprintf("skip[%d]", i)); err != nil {
			return err
		}
		if seenSkip[p] {
			return fmt.Errorf("present: skip[%d] is a duplicate: %q", i, p)
		}
		seenSkip[p] = true
		if seenFiles[p] {
			return fmt.Errorf("present: skip[%d] %q also appears in files", i, p)
		}
	}

	return nil
}

func validatePath(p, field string) error {
	if strings.TrimSpace(p) == "" {
		return fmt.Errorf("present: %s is required", field)
	}
	if filepath.IsAbs(p) {
		return fmt.Errorf("present: %s must be relative, got %q", field, p)
	}
	clean := filepath.Clean(p)
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("present: %s must not escape the repo, got %q", field, p)
	}
	return nil
}
