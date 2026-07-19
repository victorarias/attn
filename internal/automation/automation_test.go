package automation

import (
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
	_ = os.RemoveAll(dir)
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
