package plugins

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

const (
	APIVersion   = 1
	ManifestName = "attn-plugin.toml"
)

type Manifest struct {
	Name           string `toml:"name" json:"name"`
	Version        string `toml:"version" json:"version"`
	AttnAPIVersion int    `toml:"attn_api_version" json:"attn_api_version"`
	Description    string `toml:"description" json:"description,omitempty"`
	Plugin         struct {
		Entrypoint string `toml:"entrypoint" json:"entrypoint"`
	} `toml:"plugin" json:"plugin"`

	Dir string `toml:"-" json:"dir"`
}

type ManifestIssue struct {
	Path string `json:"path"`
	Err  error  `json:"-"`
}

type InstallOptions struct {
	Env []string
}

func (i ManifestIssue) Error() string {
	return fmt.Sprintf("%s: %v", i.Path, i.Err)
}

func LoadManifest(path string) (Manifest, error) {
	var manifest Manifest
	if _, err := toml.DecodeFile(path, &manifest); err != nil {
		return Manifest{}, err
	}

	manifest.Name = strings.TrimSpace(manifest.Name)
	manifest.Version = strings.TrimSpace(manifest.Version)
	manifest.Description = strings.TrimSpace(manifest.Description)
	manifest.Plugin.Entrypoint = strings.TrimSpace(manifest.Plugin.Entrypoint)
	manifest.Dir = filepath.Dir(path)

	switch {
	case manifest.Name == "":
		return Manifest{}, errors.New("name is required")
	case manifest.Version == "":
		return Manifest{}, errors.New("version is required")
	case manifest.AttnAPIVersion != APIVersion:
		return Manifest{}, fmt.Errorf("unsupported attn_api_version %d", manifest.AttnAPIVersion)
	case manifest.Plugin.Entrypoint == "":
		return Manifest{}, errors.New("plugin.entrypoint is required")
	case filepath.IsAbs(manifest.Plugin.Entrypoint):
		return Manifest{}, errors.New("plugin.entrypoint must be relative to the plugin directory")
	}

	entrypointPath := filepath.Clean(filepath.Join(manifest.Dir, manifest.Plugin.Entrypoint))
	if !pathWithinDir(manifest.Dir, entrypointPath) {
		return Manifest{}, errors.New("plugin.entrypoint must stay within the plugin directory")
	}
	if _, err := os.Stat(entrypointPath); err != nil {
		return Manifest{}, fmt.Errorf("plugin.entrypoint %q: %w", manifest.Plugin.Entrypoint, err)
	}
	return manifest, nil
}

func Discover(pluginDir string) ([]Manifest, []ManifestIssue) {
	pluginDir = strings.TrimSpace(pluginDir)
	if pluginDir == "" {
		return nil, nil
	}

	entries, err := os.ReadDir(pluginDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, []ManifestIssue{{Path: pluginDir, Err: err}}
	}

	manifests := make([]Manifest, 0, len(entries))
	var issues []ManifestIssue
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		manifestPath := filepath.Join(pluginDir, entry.Name(), ManifestName)
		manifest, err := LoadManifest(manifestPath)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			issues = append(issues, ManifestIssue{Path: manifestPath, Err: err})
			continue
		}
		manifests = append(manifests, manifest)
	}

	sort.Slice(manifests, func(i, j int) bool {
		return manifests[i].Name < manifests[j].Name
	})
	return manifests, issues
}

func InstallPath(sourceDir, pluginDir string) (Manifest, error) {
	return InstallPathWithOptions(sourceDir, pluginDir, InstallOptions{})
}

func InstallPathWithOptions(sourceDir, pluginDir string, opts InstallOptions) (Manifest, error) {
	sourceDir, err := filepath.Abs(strings.TrimSpace(sourceDir))
	if err != nil {
		return Manifest{}, fmt.Errorf("resolve source directory: %w", err)
	}
	sourceManifest, err := LoadManifest(filepath.Join(sourceDir, ManifestName))
	if err != nil {
		return Manifest{}, fmt.Errorf("load source manifest: %w", err)
	}
	if !validInstallName(sourceManifest.Name) {
		return Manifest{}, fmt.Errorf("name %q cannot be used as an install directory", sourceManifest.Name)
	}
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		return Manifest{}, fmt.Errorf("create plugin directory: %w", err)
	}

	targetDir := filepath.Join(pluginDir, sourceManifest.Name)
	if _, err := os.Stat(targetDir); err == nil {
		return Manifest{}, fmt.Errorf("plugin %q is already installed", sourceManifest.Name)
	} else if !os.IsNotExist(err) {
		return Manifest{}, fmt.Errorf("inspect install path: %w", err)
	}

	stagingDir, err := os.MkdirTemp(pluginDir, "."+sourceManifest.Name+".install-*")
	if err != nil {
		return Manifest{}, fmt.Errorf("create staging install directory: %w", err)
	}
	defer os.RemoveAll(stagingDir)

	if err := copyTree(sourceDir, stagingDir); err != nil {
		return Manifest{}, err
	}
	installed, err := LoadManifest(filepath.Join(stagingDir, ManifestName))
	if err != nil {
		return Manifest{}, fmt.Errorf("validate installed manifest: %w", err)
	}
	if err := installDependencies(stagingDir, opts.Env); err != nil {
		return Manifest{}, err
	}
	if err := os.Rename(stagingDir, targetDir); err != nil {
		if _, statErr := os.Stat(targetDir); statErr == nil {
			return Manifest{}, fmt.Errorf("plugin %q is already installed", sourceManifest.Name)
		}
		return Manifest{}, fmt.Errorf("publish plugin %q: %w", sourceManifest.Name, err)
	}
	installed.Dir = targetDir
	return installed, nil
}

func Remove(pluginDir, name string) error {
	name = strings.TrimSpace(name)
	if !validInstallName(name) {
		return fmt.Errorf("invalid plugin name %q", name)
	}
	path := filepath.Join(pluginDir, name)
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("plugin %q is not installed", name)
		}
		return fmt.Errorf("inspect plugin %q: %w", name, err)
	}
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("remove plugin %q: %w", name, err)
	}
	return nil
}

func validInstallName(name string) bool {
	return name != "." &&
		name != ".." &&
		name == filepath.Base(name) &&
		!strings.ContainsAny(name, `/\`)
}

func pathWithinDir(root, target string) bool {
	root = filepath.Clean(root)
	target = filepath.Clean(target)
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)))
}

func installDependencies(pluginDir string, env []string) error {
	cmd := exec.Command("/usr/bin/env", "bun", "install")
	cmd.Dir = pluginDir
	cmd.Env = env
	output, err := cmd.CombinedOutput()
	if err != nil {
		details := strings.TrimSpace(string(output))
		if details == "" {
			return fmt.Errorf("run bun install: %w", err)
		}
		return fmt.Errorf("run bun install: %w: %s", err, details)
	}
	return nil
}

func copyTree(sourceDir, targetDir string) error {
	return filepath.WalkDir(sourceDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return fmt.Errorf("relative plugin path: %w", err)
		}
		if rel == "." {
			return os.MkdirAll(targetDir, 0o755)
		}
		if entry.IsDir() && entry.Name() == ".git" {
			return filepath.SkipDir
		}

		targetPath := filepath.Join(targetDir, rel)
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("inspect %s: %w", rel, err)
		}
		switch {
		case entry.Type()&os.ModeSymlink != 0:
			return fmt.Errorf("copy %s: symlinks are not supported in plugin installs", rel)
		case entry.IsDir():
			if err := os.MkdirAll(targetPath, info.Mode().Perm()); err != nil {
				return fmt.Errorf("create directory %s: %w", rel, err)
			}
			return nil
		case info.Mode().IsRegular():
			if err := copyRegularFile(path, targetPath, info.Mode().Perm()); err != nil {
				return fmt.Errorf("copy file %s: %w", rel, err)
			}
			return nil
		default:
			return fmt.Errorf("copy %s: unsupported file type", rel)
		}
	})
}

func copyRegularFile(sourcePath, targetPath string, mode fs.FileMode) error {
	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer source.Close()

	target, err := os.OpenFile(targetPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer target.Close()

	if _, err := io.Copy(target, source); err != nil {
		return err
	}
	return nil
}
