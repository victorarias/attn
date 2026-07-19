package automation

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefinitionPersistsCanonicalDirectory(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "configured")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	raw := strings.ReplaceAll(`api_version: attn.dev/automations/v1alpha1
id: canonical
name: Canonical
enabled: true
trigger: {type: manual}
prompt: Inspect.
launch: {driver: codex}
location: {type: directory, path: PATH}
policy: {continuity: fresh}
`, `PATH`, link)
	spec, canonical, err := ParseDefinitionYAML([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Location.Path != resolved || strings.Contains(string(canonical), link) {
		t.Fatalf("path=%q canonical=%s, want resolved %q", spec.Location.Path, canonical, resolved)
	}
}

func TestDefinitionHasNoApprovalAndRequiresDirectory(t *testing.T) {
	dir := t.TempDir()
	raw := strings.ReplaceAll(`api_version: attn.dev/automations/v1alpha1
id: harmless
name: Harmless
enabled: true
trigger: {type: manual}
prompt: Inspect the supplied context.
launch: {driver: codex, model: gpt-5.5, effort: high}
location: {type: directory, path: PATH}
policy: {continuity: fresh}
`, `PATH`, dir)
	spec, canonical, err := ParseDefinitionYAML([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(canonical), "approval") {
		t.Fatalf("configurable approval leaked into definition: %s", canonical)
	}
	snapshot, err := Effective(spec, 3)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Launch.ApprovalProductMode != "auto" || snapshot.Launch.ApprovalDriverMode != "auto_review" {
		t.Fatalf("effective launch=%#v", snapshot.Launch)
	}
	if snapshot.Launch.Agent != "codex" || snapshot.Launch.DirectoryTrust != "configured_directory" || snapshot.Launch.Recovery != "adopt_or_restart_fresh" {
		t.Fatalf("effective unattended contract=%#v", snapshot.Launch)
	}
	_ = os.RemoveAll(dir)
}

func TestDefinitionCanonicalizesLaunchDriver(t *testing.T) {
	dir := t.TempDir()
	raw := strings.ReplaceAll(`api_version: attn.dev/automations/v1alpha1
id: canonical-driver
name: Canonical driver
enabled: true
trigger: {type: manual}
prompt: Inspect.
launch: {driver: " CoDeX "}
location: {type: directory, path: PATH}
policy: {continuity: fresh}
`, `PATH`, dir)
	spec, canonical, err := ParseDefinitionYAML([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if spec.Launch.Driver != "codex" || strings.Contains(string(canonical), " CoDeX ") {
		t.Fatalf("driver=%q canonical=%s", spec.Launch.Driver, canonical)
	}
}

func TestDefinitionRejectsUnknownFields(t *testing.T) {
	_, _, err := ParseDefinitionYAML([]byte(`api_version: attn.dev/automations/v1alpha1
id: bad
name: Bad
enabled: true
trigger: {type: manual}
prompt: x
launch: {driver: codex, approval: yolo}
location: {type: directory, path: /tmp}
policy: {continuity: fresh}
`))
	if err == nil || !strings.Contains(err.Error(), "field approval not found") {
		t.Fatalf("err=%v", err)
	}
}

func TestRepositoryWorktreeDefinitionCanonicalizesOverride(t *testing.T) {
	repo := t.TempDir()
	raw := strings.ReplaceAll(`api_version: attn.dev/automations/v1alpha1
id: pr-review
name: PR review
enabled: true
trigger: {type: manual}
prompt: Review the pull request locally.
launch: {driver: codex, effort: high}
location:
  type: repository_worktree
  repository_sources:
    default: {type: managed_cache}
    overrides:
      "GitHub.COM/Owner/Repo": {type: local_clone, path: PATH}
policy: {continuity: fresh, overlap: coalesce}
`, "PATH", repo)
	spec, canonical, err := ParseDefinitionYAML([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := spec.Location.RepositorySources.Overrides["github.com/owner/repo"]; !ok {
		t.Fatalf("canonical overrides = %#v", spec.Location.RepositorySources.Overrides)
	}
	if strings.Contains(string(canonical), "GitHub.COM") {
		t.Fatalf("canonical definition retained display identity: %s", canonical)
	}
}

func TestRepositoryWorktreeDefinitionRejectsDirectoryPath(t *testing.T) {
	raw := `api_version: attn.dev/automations/v1alpha1
id: pr-review
name: PR review
enabled: true
trigger: {type: manual}
prompt: Review.
launch: {driver: codex}
location:
  type: repository_worktree
  path: /tmp
  repository_sources: {default: {type: managed_cache}}
policy: {continuity: fresh}
`
	if _, _, err := ParseDefinitionYAML([]byte(raw)); err == nil || !strings.Contains(err.Error(), "cannot configure path") {
		t.Fatalf("err = %v", err)
	}
}

func TestPullRequestInputIsStrictAndCanonical(t *testing.T) {
	raw := json.RawMessage(`{"provider":"github","host":"GitHub.COM","owner":"Owner","repository":"Repo","number":42,"url":"https://github.com/owner/repo/pull/42/","state":"open","draft":false,"head_sha":"0123456789ABCDEF0123456789ABCDEF01234567"}`)
	input, err := ParsePullRequestInput(raw)
	if err != nil {
		t.Fatal(err)
	}
	if input.RepositoryIdentity() != "github.com/owner/repo" || input.SubjectKey() != "github.com/owner/repo#42" || input.HeadSHA != "0123456789abcdef0123456789abcdef01234567" {
		t.Fatalf("canonical input = %#v", input)
	}
	for _, invalid := range []string{
		`{"provider":"github","host":"github.com","owner":"owner","repository":"repo","number":42,"url":"https://github.com/other/repo/pull/42","head_sha":"0123456789abcdef0123456789abcdef01234567"}`,
		`{"provider":"github","host":"github.com","owner":"owner","repository":"repo","number":42,"url":"https://github.com/owner/repo/pull/42","head_sha":"short"}`,
		`{"provider":"github","host":"github.com","owner":"owner","repository":"repo","number":42,"url":"https://github.com/owner/repo/pull/42","head_sha":"0123456789abcdef0123456789abcdef01234567","unexpected":true}`,
	} {
		if _, err := ParsePullRequestInput(json.RawMessage(invalid)); err == nil {
			t.Fatalf("accepted invalid pull request input: %s", invalid)
		}
	}
}

func TestParsePullRequestURL(t *testing.T) {
	host, owner, repo, number, err := ParsePullRequestURL("https://GHE.EXAMPLE/Owner/Repo/pull/7/")
	if err != nil || host != "ghe.example" || owner != "owner" || repo != "repo" || number != 7 {
		t.Fatalf("parsed = %s/%s/%s#%d err=%v", host, owner, repo, number, err)
	}
	for _, invalid := range []string{
		"http://github.com/o/r/pull/1",
		"https://user@github.com/o/r/pull/1",
		"https://github.com/o/r/issues/1",
		"https://github.com/o/r/pull/1/files",
		"https://github.com/o/r/pull/0",
		"https://github.com/o/../pull/1",
	} {
		if _, _, _, _, err := ParsePullRequestURL(invalid); err == nil {
			t.Fatalf("accepted invalid URL %q", invalid)
		}
	}
}
