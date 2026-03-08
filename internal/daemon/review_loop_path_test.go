package daemon

import (
	"reflect"
	"testing"
)

func TestNormalizeReviewLoopPaths(t *testing.T) {
	repoPath := "/Users/victor.arias/projects/nolo-mcp"

	got := normalizeReviewLoopPaths(
		repoPath,
		"/Users/victor.arias/projects/nolo-mcp/internal/database/query.go",
		"/root/repo/internal/database/query.go",
		"internal/database/query_test.go",
	)

	want := []string{
		"internal/database/query.go",
		"internal/database/query.go",
		"internal/database/query_test.go",
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("normalizeReviewLoopPaths() = %#v, want %#v", got, want)
	}
}
