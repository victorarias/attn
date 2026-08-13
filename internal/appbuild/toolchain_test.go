package appbuild

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestNPMRegistryEnvReplacesAnInheritedRegistry(t *testing.T) {
	env := npmRegistryEnv([]string{"PATH=/usr/bin", "NPM_CONFIG_REGISTRY=https://mirror.invalid/npm/", "HOME=/tmp"})

	if slices.Contains(env, "NPM_CONFIG_REGISTRY=https://mirror.invalid/npm/") {
		t.Fatalf("the inherited registry survived: %v", env)
	}
	if !slices.Contains(env, "NPM_CONFIG_REGISTRY="+DefaultNPMRegistry) {
		t.Fatalf("the default registry is not set: %v", env)
	}
	for _, keep := range []string{"PATH=/usr/bin", "HOME=/tmp"} {
		if !slices.Contains(env, keep) {
			t.Fatalf("%s was dropped: %v", keep, env)
		}
	}
}

func TestNPMRegistryEnvHonoursTheOverride(t *testing.T) {
	t.Setenv("ATTN_NPM_REGISTRY", "https://registry.example/npm/")

	env := npmRegistryEnv([]string{"npm_config_registry=https://mirror.invalid/npm/"})

	if !slices.Contains(env, "NPM_CONFIG_REGISTRY=https://registry.example/npm/") {
		t.Fatalf("the override is not set: %v", env)
	}
	if slices.Contains(env, "npm_config_registry=https://mirror.invalid/npm/") {
		t.Fatalf("the lowercase inherited registry survived: %v", env)
	}
}

// The seam that matters: a machine exporting a registry attn has no credentials
// for must not break an apply. This is the shape of the real failure — a
// corporate mirror answering 401 for every public package.
func TestToolchainInstallIgnoresAnUnreachableInheritedRegistry(t *testing.T) {
	t.Setenv("NPM_CONFIG_REGISTRY", "https://npm-registry.invalid/")

	dir := t.TempDir()
	if _, err := ResolveToolchain(dir, func(line string) { t.Log(line) }); err != nil {
		t.Fatalf("installing the toolchain under a hostile inherited registry: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, toolchainDirName, "node_modules", ".bin", "tsc")); err != nil {
		t.Fatalf("no compiler was installed: %v", err)
	}
}
