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

func TestGitHubReviewDefinitionCanonicalizesAndAppliesRepositoryFilter(t *testing.T) {
	raw := `api_version: attn.dev/automations/v1alpha1
id: requested-review
name: Requested review
enabled: true
trigger:
  type: github_review_requested
  repositories:
    mode: all_accessible
    include: [GitHub.COM/Owner/Repo, github.com/owner/second]
    exclude: [github.com/owner/second]
prompt: Review locally without modifying GitHub.
launch: {driver: codex, effort: high}
location:
  type: repository_worktree
  repository_sources: {default: {type: managed_cache}}
policy: {continuity: per_subject, catch_up: latest, overlap: coalesce}
`
	if _, _, err := ParseDefinitionYAML([]byte(raw)); err == nil || !strings.Contains(err.Error(), "both included and excluded") {
		t.Fatalf("conflicting filter err = %v", err)
	}
	raw = strings.Replace(raw, "    exclude: [github.com/owner/second]", "    exclude: [github.com/owner/blocked]", 1)
	spec, canonical, err := ParseDefinitionYAML([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	filter := spec.Trigger.Repositories
	if !filter.Matches("github.com/owner/repo") || !filter.Matches("github.com/owner/second") || filter.Matches("github.com/owner/other") || filter.Matches("github.com/owner/blocked") {
		t.Fatalf("unexpected filter behavior: %#v", filter)
	}
	if strings.Contains(string(canonical), "GitHub.COM") {
		t.Fatalf("canonical definition retained display identity: %s", canonical)
	}
}

func TestGitHubReviewDefinitionRequiresAcceptedPolicyAndLocation(t *testing.T) {
	base := `api_version: attn.dev/automations/v1alpha1
id: requested-review
name: Requested review
enabled: true
trigger: {type: github_review_requested, repositories: {mode: all_accessible}}
prompt: Review locally.
launch: {driver: codex}
location:
  type: repository_worktree
  repository_sources: {default: {type: managed_cache}}
policy: {continuity: per_subject, catch_up: latest, overlap: coalesce}
`
	for name, raw := range map[string]string{
		"fresh continuity": strings.Replace(base, "continuity: per_subject", "continuity: fresh", 1),
		"skip catch up":    strings.Replace(base, "catch_up: latest", "catch_up: skip", 1),
		"directory":        strings.Replace(base, "type: repository_worktree\n  repository_sources: {default: {type: managed_cache}}", "type: directory\n  path: /tmp", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := ParseDefinitionYAML([]byte(raw)); err == nil {
				t.Fatal("accepted invalid GitHub review definition")
			}
		})
	}
}

func TestScheduledDefinitionValidation(t *testing.T) {
	dir := t.TempDir()
	base := strings.ReplaceAll(`api_version: attn.dev/automations/v1alpha1
id: nightly
name: Nightly
enabled: true
trigger:
  type: scheduled
  schedule: {cron: "0 3 * * *", time_zone: America/New_York}
prompt: Sweep.
launch: {driver: codex}
location: {type: directory, path: PATH}
policy: {continuity: fresh, catch_up: skip}
`, "PATH", dir)
	if _, _, err := ParseDefinitionYAML([]byte(base)); err != nil {
		t.Fatalf("valid scheduled definition rejected: %v", err)
	}
	for name, raw := range map[string]string{
		"missing cron":                 strings.Replace(base, `cron: "0 3 * * *", `, "", 1),
		"TZ prefix":                    strings.Replace(base, `cron: "0 3 * * *"`, `cron: "TZ=America/New_York 0 3 * * *"`, 1),
		"CRON_TZ prefix":               strings.Replace(base, `cron: "0 3 * * *"`, `cron: "CRON_TZ=America/New_York 0 3 * * *"`, 1),
		"invalid cron":                 strings.Replace(base, `cron: "0 3 * * *"`, `cron: "not a cron"`, 1),
		"never-occurring cron":         strings.Replace(base, `cron: "0 3 * * *"`, `cron: "0 0 30 2 *"`, 1),
		"missing time zone":            strings.Replace(base, ", time_zone: America/New_York", "", 1),
		"invalid time zone":            strings.Replace(base, "time_zone: America/New_York", "time_zone: Not/AZone", 1),
		"repository_worktree location": strings.Replace(base, "location: {type: directory, path: "+dir+"}", "location: {type: repository_worktree, repository_sources: {default: {type: managed_cache}}}", 1),
		"per_subject continuity":       strings.Replace(base, "continuity: fresh, catch_up: skip", "continuity: per_subject, catch_up: skip", 1),
		"missing catch_up":             strings.Replace(base, "continuity: fresh, catch_up: skip", "continuity: fresh", 1),
		"catch_up queue":               strings.Replace(base, "catch_up: skip", "catch_up: queue", 1),
	} {
		t.Run(name, func(t *testing.T) {
			raw = strings.ReplaceAll(raw, "PATH", dir)
			if _, _, err := ParseDefinitionYAML([]byte(raw)); err == nil {
				t.Fatal("accepted invalid scheduled definition")
			}
		})
	}
	for name, catchUp := range map[string]string{"skip": "skip", "latest": "latest"} {
		t.Run("catch_up "+name, func(t *testing.T) {
			raw := strings.Replace(base, "catch_up: skip", "catch_up: "+catchUp, 1)
			if _, _, err := ParseDefinitionYAML([]byte(raw)); err != nil {
				t.Fatalf("catch_up=%s rejected: %v", catchUp, err)
			}
		})
	}
	for name, continuity := range map[string]string{"fresh": "fresh", "singleton": "singleton"} {
		t.Run("continuity "+name, func(t *testing.T) {
			raw := strings.Replace(base, "continuity: fresh", "continuity: "+continuity, 1)
			if _, _, err := ParseDefinitionYAML([]byte(raw)); err != nil {
				t.Fatalf("continuity=%s rejected: %v", continuity, err)
			}
		})
	}
}

func TestManualAndGitHubTriggersRejectSchedule(t *testing.T) {
	dir := t.TempDir()
	manual := strings.ReplaceAll(`api_version: attn.dev/automations/v1alpha1
id: manual-with-schedule
name: Manual
enabled: true
trigger:
  type: manual
  schedule: {cron: "0 3 * * *", time_zone: UTC}
prompt: Inspect.
launch: {driver: codex}
location: {type: directory, path: PATH}
policy: {continuity: fresh}
`, "PATH", dir)
	if _, _, err := ParseDefinitionYAML([]byte(manual)); err == nil || !strings.Contains(err.Error(), "cannot configure schedule") {
		t.Fatalf("err = %v", err)
	}
	github := `api_version: attn.dev/automations/v1alpha1
id: requested-review
name: Requested review
enabled: true
trigger:
  type: github_review_requested
  repositories: {mode: all_accessible}
  schedule: {cron: "0 3 * * *", time_zone: UTC}
prompt: Review locally.
launch: {driver: codex}
location:
  type: repository_worktree
  repository_sources: {default: {type: managed_cache}}
policy: {continuity: per_subject, catch_up: latest}
`
	if _, _, err := ParseDefinitionYAML([]byte(github)); err == nil || !strings.Contains(err.Error(), "cannot configure schedule") {
		t.Fatalf("err = %v", err)
	}
}

// TestCanonicalJSONOmitsScheduleForNonScheduledTriggers is the regression
// check for Fix 3: encoding/json's omitempty does not omit a zero-value
// struct, so TriggerSpec.Schedule must be a pointer or every manual/
// github_review_requested definition's canonical JSON grows a spurious
// "schedule":{} key, which would bump UpsertAutomationDefinition's revision
// on every byte-identical re-apply.
func TestCanonicalJSONOmitsScheduleForNonScheduledTriggers(t *testing.T) {
	dir := t.TempDir()
	manual := strings.ReplaceAll(`api_version: attn.dev/automations/v1alpha1
id: manual-no-schedule
name: Manual
enabled: true
trigger: {type: manual}
prompt: Inspect.
launch: {driver: codex}
location: {type: directory, path: PATH}
policy: {continuity: fresh}
`, "PATH", dir)
	_, canonical, err := ParseDefinitionYAML([]byte(manual))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(canonical), `"schedule"`) {
		t.Fatalf("canonical JSON contains a schedule key for a manual trigger: %s", canonical)
	}
}

// TestScheduledDefinitionRoundTripsCronAndTimeZone locks in that
// TriggerSpec.Schedule's pointer conversion still carries cron/time_zone
// through both the YAML parse and a canonical-JSON re-decode.
func TestScheduledDefinitionRoundTripsCronAndTimeZone(t *testing.T) {
	dir := t.TempDir()
	raw := strings.ReplaceAll(`api_version: attn.dev/automations/v1alpha1
id: nightly
name: Nightly
enabled: true
trigger:
  type: scheduled
  schedule: {cron: "0 3 * * *", time_zone: America/New_York}
prompt: Sweep.
launch: {driver: codex}
location: {type: directory, path: PATH}
policy: {continuity: fresh, catch_up: skip}
`, "PATH", dir)
	spec, canonical, err := ParseDefinitionYAML([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if spec.Trigger.Schedule == nil || spec.Trigger.Schedule.Cron != "0 3 * * *" || spec.Trigger.Schedule.TimeZone != "America/New_York" {
		t.Fatalf("parsed schedule = %#v, want cron/time_zone preserved", spec.Trigger.Schedule)
	}
	var reDecoded DefinitionSpec
	if err := json.Unmarshal(canonical, &reDecoded); err != nil {
		t.Fatal(err)
	}
	if reDecoded.Trigger.Schedule == nil || reDecoded.Trigger.Schedule.Cron != "0 3 * * *" || reDecoded.Trigger.Schedule.TimeZone != "America/New_York" {
		t.Fatalf("canonical JSON round-trip schedule = %#v, want cron/time_zone preserved", reDecoded.Trigger.Schedule)
	}
}

func TestManualTriggerRejectsCatchUp(t *testing.T) {
	dir := t.TempDir()
	raw := strings.ReplaceAll(`api_version: attn.dev/automations/v1alpha1
id: manual-with-catchup
name: Manual
enabled: true
trigger: {type: manual}
prompt: Inspect.
launch: {driver: codex}
location: {type: directory, path: PATH}
policy: {continuity: fresh, catch_up: skip}
`, "PATH", dir)
	if _, _, err := ParseDefinitionYAML([]byte(raw)); err == nil || !strings.Contains(err.Error(), "cannot configure policy.catch_up") {
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
