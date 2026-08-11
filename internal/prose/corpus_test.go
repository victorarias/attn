package prose

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The calibration corpus is three labelled sets. See
// testdata/corpus/README.md for where the labels come from.
const (
	// setHealthy is prose Victor has accepted: the plan and vision docs on
	// main. A threshold that fires on it is wrong.
	setHealthy = "healthy"
	// setDense is comment prose as it stood before the #818 sweep — prose
	// Victor judged too dense and deleted.
	setDense = "dense"
	// setSwept is the same twelve files after that sweep: what he replaced it
	// with. It is a second healthy set, and the one that shares its authors
	// and its subject matter with the dense set.
	setSwept = "swept"
)

// repoRoot locates the checkout from this file's own path, so the corpus loads
// the same wherever the test was started.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate this test file")
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}

func corpus(t *testing.T, set string) []*Document {
	t.Helper()
	var dirs []string
	switch set {
	case setHealthy:
		dirs = []string{"docs/plans", "docs/vision"}
	case setDense:
		dirs = []string{"internal/prose/testdata/corpus/dense"}
	case setSwept:
		dirs = []string{"internal/prose/testdata/corpus/swept"}
	default:
		t.Fatalf("unknown corpus set %q", set)
	}

	root := repoRoot(t)
	var docs []*Document
	for _, dir := range dirs {
		entries, err := os.ReadDir(filepath.Join(root, dir))
		if err != nil {
			t.Fatalf("read corpus dir %s: %v", dir, err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") || entry.Name() == "README.md" {
				continue
			}
			path := filepath.Join(root, dir, entry.Name())
			source, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			doc, err := Parse(filepath.Join(dir, entry.Name()), source)
			if err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}
			docs = append(docs, doc)
		}
	}
	if len(docs) == 0 {
		t.Fatalf("corpus set %q is empty", set)
	}
	return docs
}

func healthyCorpus(t *testing.T) []*Document { return corpus(t, setHealthy) }
