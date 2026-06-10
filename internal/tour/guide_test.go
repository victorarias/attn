package tour

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	attngit "github.com/victorarias/attn/internal/git"
)

func TestCreateGuidePathUsesProfileDataDirectory(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(t.TempDir(), "repo")
	t.Setenv("HOME", home)
	t.Setenv("ATTN_PROFILE", "")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}

	path, err := CreateGuidePath(repo, "session-1", "My Tour")
	if err != nil {
		t.Fatalf("CreateGuidePath() error = %v", err)
	}
	if !IsSystemGuidePath(path) {
		t.Fatalf("guide path %q is not recognized as system-owned", path)
	}
	if strings.HasPrefix(path, repo+string(filepath.Separator)) {
		t.Fatalf("guide path %q was created inside repository %q", path, repo)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("guide path does not exist: %v", err)
	}
}

func TestBuildSnapshotPreservesTourOrderAndGroupsRemainingFiles(t *testing.T) {
	guide := &Guide{
		Version: GuideVersion,
		Summary: "Read this first.",
		Files: []GuideFile{
			{
				Path: "b.go",
				View: "diff",
				Note: "Second alphabetically, first conceptually.",
				Annotations: []GuideAnnotation{
					{Anchor: "important()", Note: "This is the key call."},
				},
			},
		},
		Skip: []string{"generated.go"},
	}
	changed := []attngit.DiffFileInfo{
		{Path: "a.go", Status: "modified"},
		{Path: "b.go", Status: "modified", Additions: 2},
		{Path: "generated.go", Status: "modified"},
	}
	content := map[string]string{
		"a.go":         "package example\n",
		"b.go":         "package example\nfunc run() { important() }\n",
		"generated.go": "package generated\n",
	}

	snapshot, err := BuildSnapshot(guide, changed, func(path, _ string) (string, string, error) {
		return "", content[path], nil
	})
	if err != nil {
		t.Fatalf("BuildSnapshot() error = %v", err)
	}
	if got := []string{snapshot.Files[0].Path, snapshot.Files[1].Path, snapshot.Files[2].Path}; strings.Join(got, ",") != "b.go,generated.go,a.go" {
		t.Fatalf("file order = %v", got)
	}
	if snapshot.Files[0].Group != "tour" || snapshot.Files[1].Group != "skip" || snapshot.Files[2].Group != "other" {
		t.Fatalf("groups = %q, %q, %q", snapshot.Files[0].Group, snapshot.Files[1].Group, snapshot.Files[2].Group)
	}
	annotation := snapshot.Files[0].Annotations[0]
	if annotation.LineStart != 2 || annotation.LineEnd != 2 || annotation.Comments[0].Body != "This is the key call." {
		t.Fatalf("annotation = %+v", annotation)
	}
}

func TestBuildSnapshotRejectsMissingTourFile(t *testing.T) {
	_, err := BuildSnapshot(&Guide{
		Version: GuideVersion,
		Files:   []GuideFile{{Path: "missing.go", View: "diff"}},
	}, nil, func(string, string) (string, string, error) {
		return "", "", nil
	})
	if err == nil || !strings.Contains(err.Error(), "not in the current changeset") {
		t.Fatalf("BuildSnapshot() error = %v", err)
	}
}

func TestBuildSnapshotPreservesChaptersAndRiskNotes(t *testing.T) {
	guide := &Guide{
		Version: GuideVersion,
		Chapters: []GuideChapter{{
			Title:   "Protocol and persistence",
			Summary: "Understand the durable contract first.",
			Files: []GuideFile{{
				Path: "internal/protocol.go",
				View: "diff",
				Note: "Read the contract.",
				Risk: "Old clients must reject the new shape.",
			}},
		}},
	}
	changed := []attngit.DiffFileInfo{{Path: "internal/protocol.go", Status: "modified"}}

	snapshot, err := BuildSnapshot(guide, changed, func(path, _ string) (string, string, error) {
		return "", "package internal\n", nil
	})
	if err != nil {
		t.Fatalf("BuildSnapshot() error = %v", err)
	}
	file := snapshot.Files[0]
	if file.ChapterTitle != "Protocol and persistence" || file.ChapterSummary != "Understand the durable contract first." {
		t.Fatalf("chapter metadata = %+v", file)
	}
	if file.ChapterID == "" || file.RiskNote != "Old clients must reject the new shape." {
		t.Fatalf("chapter id/risk metadata = %+v", file)
	}
}
