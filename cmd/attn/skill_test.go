package main

import (
	"strings"
	"testing"
)

func TestWriteSkillPrintsBundledSkill(t *testing.T) {
	var stdout, stderr strings.Builder
	if code := writeSkill(&stdout, &stderr, nil); code != 0 {
		t.Fatalf("writeSkill exit code = %d, stderr: %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "name: attn") {
		t.Fatalf("skill output missing frontmatter: %q", stdout.String()[:200])
	}
}

func TestWriteSkillListsReferences(t *testing.T) {
	var stdout, stderr strings.Builder
	if code := writeSkill(&stdout, &stderr, []string{"--list"}); code != 0 {
		t.Fatalf("writeSkill --list exit code = %d, stderr: %q", code, stderr.String())
	}
	for _, name := range []string{"tickets", "delegation", "workflow"} {
		if !strings.Contains(stdout.String(), name+"\n") {
			t.Fatalf("reference list missing %q: %q", name, stdout.String())
		}
	}
}

func TestWriteSkillPrintsOneReference(t *testing.T) {
	var stdout, stderr strings.Builder
	if code := writeSkill(&stdout, &stderr, []string{"--reference", "tickets"}); code != 0 {
		t.Fatalf("writeSkill --reference tickets exit code = %d, stderr: %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "# Writing a good ticket") {
		t.Fatalf("tickets reference output unexpected: %q", stdout.String()[:200])
	}

	stdout.Reset()
	if code := writeSkill(&stdout, &stderr, []string{"--reference", "tickets.md"}); code != 0 {
		t.Fatalf("writeSkill --reference tickets.md exit code = %d, stderr: %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "# Writing a good ticket") {
		t.Fatalf("tickets.md reference output unexpected: %q", stdout.String()[:200])
	}
}

func TestWriteSkillUnknownReferenceNamesTheBundledOnes(t *testing.T) {
	var stdout, stderr strings.Builder
	if code := writeSkill(&stdout, &stderr, []string{"--reference", "nope"}); code != 1 {
		t.Fatalf("unknown reference exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), `"nope"`) || !strings.Contains(stderr.String(), "tickets") {
		t.Fatalf("unknown-reference error does not name the ask and the bundled names: %q", stderr.String())
	}
}

func TestWriteSkillReferenceWithoutNameFails(t *testing.T) {
	var stdout, stderr strings.Builder
	if code := writeSkill(&stdout, &stderr, []string{"--reference"}); code != 2 {
		t.Fatalf("bare --reference exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "--list") {
		t.Fatalf("bare --reference error does not point at --list: %q", stderr.String())
	}
}
