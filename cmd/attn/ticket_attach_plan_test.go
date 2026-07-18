package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseTicketAttachPlanArgs(t *testing.T) {
	got, err := parseTicketAttachPlanArgs([]string{
		"--file", "docs/plans/change.md",
		"--scope", "services/catalog",
		"--authority", "repository",
		"--ticket", "catalog-change",
	})
	if err != nil {
		t.Fatalf("parseTicketAttachPlanArgs: %v", err)
	}
	if got.File != "docs/plans/change.md" || got.Scope != "services/catalog" || got.Authority != planAuthorityRepository || got.Ticket != "catalog-change" {
		t.Fatalf("parsed args = %+v", got)
	}
}

func TestInspectPlanAttachmentUsesScopedMonorepoConvention(t *testing.T) {
	repo := initPlanTestRepo(t)
	writePlanTestFile(t, filepath.Join(repo, "docs", "root.md"), "# Root docs\n")
	writePlanTestFile(t, filepath.Join(repo, "services", "catalog", "README.md"), "# Catalog\n")
	commitPlanTestFiles(t, repo, "root documentation")

	planPath := filepath.Join(repo, "scratch", "catalog-plan.md")
	writePlanTestFile(t, planPath, "# Plan\n")

	rootDecision, err := inspectPlanAttachment(planPath, repo, planAuthorityAuto)
	if err == nil || !strings.Contains(err.Error(), "commit") {
		t.Fatalf("root convention decision error = %v, want commit guidance", err)
	}
	if !rootDecision.ConventionFound && err == nil {
		t.Fatalf("root convention was not detected")
	}

	component := filepath.Join(repo, "services", "catalog")
	decision, err := inspectPlanAttachment(planPath, component, planAuthorityAuto)
	if err != nil {
		t.Fatalf("component-scoped decision: %v", err)
	}
	if decision.Authority != planAuthorityNotebook {
		t.Fatalf("component-scoped authority = %q, want notebook", decision.Authority)
	}

	writePlanTestFile(t, filepath.Join(component, "docs", "conventions.md"), "# Component docs\n")
	runPlanTestGit(t, repo, "add", "services/catalog/docs/conventions.md")
	runPlanTestGit(t, repo, "commit", "-m", "component documentation")
	_, err = inspectPlanAttachment(planPath, component, planAuthorityAuto)
	if err == nil || !strings.Contains(err.Error(), "commit") {
		t.Fatalf("component convention error = %v, want commit guidance", err)
	}
}

func TestInspectPlanAttachmentBuildsCommittedRepositoryReference(t *testing.T) {
	repo := initPlanTestRepo(t)
	planPath := filepath.Join(repo, "docs", "plans", "change.md")
	writePlanTestFile(t, planPath, "# Change\n")
	commitPlanTestFiles(t, repo, "add plan")

	decision, err := inspectPlanAttachment(planPath, repo, planAuthorityAuto)
	if err != nil {
		t.Fatalf("inspectPlanAttachment: %v", err)
	}
	if decision.Authority != planAuthorityRepository || decision.RepoPath != "docs/plans/change.md" {
		t.Fatalf("decision = %+v", decision)
	}
	if len(decision.IntroducedIn) != 40 || decision.Branch == "" || decision.ReferenceName != "change.reference.md" {
		t.Fatalf("repository provenance = %+v", decision)
	}

	reference := string(renderRepositoryPlanReference(decision))
	for _, expected := range []string{
		"attn_artifact: repository-reference",
		"authority: repository",
		`path: "docs/plans/change.md"`,
		`branch: "` + decision.Branch + `"`,
		`introduced_in: "` + decision.IntroducedIn + `"`,
		"Follow-on work should read and edit the repository file",
	} {
		if !strings.Contains(reference, expected) {
			t.Fatalf("reference missing %q:\n%s", expected, reference)
		}
	}
}

func TestInspectPlanAttachmentRejectsTrackedNotebookPromotion(t *testing.T) {
	repo := initPlanTestRepo(t)
	planPath := filepath.Join(repo, "plan.md")
	writePlanTestFile(t, planPath, "# Plan\n")
	commitPlanTestFiles(t, repo, "add plan")

	_, err := inspectPlanAttachment(planPath, repo, planAuthorityNotebook)
	if err == nil || !strings.Contains(err.Error(), "refusing to promote tracked file") {
		t.Fatalf("tracked Notebook promotion error = %v", err)
	}
}

func TestInspectPlanAttachmentRequiresCommittedRepositoryPlan(t *testing.T) {
	repo := initPlanTestRepo(t)
	planPath := filepath.Join(repo, "plan.md")
	writePlanTestFile(t, planPath, "# Plan\n")

	_, err := inspectPlanAttachment(planPath, repo, planAuthorityRepository)
	if err == nil || !strings.Contains(err.Error(), "requires") || !strings.Contains(err.Error(), "committed") {
		t.Fatalf("uncommitted repository plan error = %v", err)
	}
}

func TestRetireNotebookPlanSourceRequiresByteIdenticalCopy(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source.md")
	canonical := filepath.Join(dir, "canonical.md")
	writePlanTestFile(t, source, "# Source\n")
	writePlanTestFile(t, canonical, "# Different\n")

	retired, err := retireNotebookPlanSource(source, canonical)
	if err == nil || retired {
		t.Fatalf("mismatched retirement = (%v, %v), want retained error", retired, err)
	}
	if _, statErr := os.Stat(source); statErr != nil {
		t.Fatalf("source removed after mismatch: %v", statErr)
	}

	writePlanTestFile(t, canonical, "# Source\n")
	retired, err = retireNotebookPlanSource(source, canonical)
	if err != nil || !retired {
		t.Fatalf("verified retirement = (%v, %v), want retired", retired, err)
	}
	if _, statErr := os.Stat(source); !os.IsNotExist(statErr) {
		t.Fatalf("source still exists after verified retirement: %v", statErr)
	}
}

func TestRetireLegacyNotebookPlanCopyOnlyWhenByteIdentical(t *testing.T) {
	dir := t.TempDir()
	repoDir := filepath.Join(dir, "repo")
	notebookDir := filepath.Join(dir, "notebook")
	source := filepath.Join(repoDir, "plan.md")
	reference := filepath.Join(notebookDir, "plan.reference.md")
	legacy := filepath.Join(notebookDir, "plan.md")
	writePlanTestFile(t, source, "# Canonical\n")
	writePlanTestFile(t, reference, "# Reference\n")
	writePlanTestFile(t, legacy, "# Diverged\n")

	gotPath, retired, err := retireLegacyNotebookPlanCopy(source, reference)
	if gotPath != legacy || err == nil || retired {
		t.Fatalf("divergent legacy cleanup = (%q, %v, %v)", gotPath, retired, err)
	}
	if _, statErr := os.Stat(legacy); statErr != nil {
		t.Fatalf("divergent legacy copy was removed: %v", statErr)
	}

	writePlanTestFile(t, legacy, "# Canonical\n")
	gotPath, retired, err = retireLegacyNotebookPlanCopy(source, reference)
	if gotPath != legacy || err != nil || !retired {
		t.Fatalf("identical legacy cleanup = (%q, %v, %v)", gotPath, retired, err)
	}
	if _, statErr := os.Stat(legacy); !os.IsNotExist(statErr) {
		t.Fatalf("identical legacy copy still exists: %v", statErr)
	}
}

func initPlanTestRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	runPlanTestGit(t, repo, "init", "-b", "main")
	runPlanTestGit(t, repo, "config", "user.email", "attn-test@example.com")
	runPlanTestGit(t, repo, "config", "user.name", "attn test")
	return repo
}

func writePlanTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func commitPlanTestFiles(t *testing.T, repo, message string) {
	t.Helper()
	runPlanTestGit(t, repo, "add", ".")
	runPlanTestGit(t, repo, "commit", "-m", message)
}

func runPlanTestGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
}
