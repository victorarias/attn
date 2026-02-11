package daemon

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestEnsureDaemonInstanceID_PersistsAcrossCalls(t *testing.T) {
	root := t.TempDir()

	first, err := ensureDaemonInstanceID(root)
	if err != nil {
		t.Fatalf("ensureDaemonInstanceID first call: %v", err)
	}
	if !validDaemonInstanceID(first) {
		t.Fatalf("first daemon instance id %q is invalid", first)
	}

	second, err := ensureDaemonInstanceID(root)
	if err != nil {
		t.Fatalf("ensureDaemonInstanceID second call: %v", err)
	}
	if first != second {
		t.Fatalf("daemon instance id changed across calls: first=%q second=%q", first, second)
	}
}

func TestEnsureDaemonInstanceID_RewritesCorruptFile(t *testing.T) {
	root := t.TempDir()
	idPath := filepath.Join(root, daemonIDFileName)
	if err := os.WriteFile(idPath, []byte("corrupt\n"), 0600); err != nil {
		t.Fatalf("seed corrupt daemon id file: %v", err)
	}

	id, err := ensureDaemonInstanceID(root)
	if err != nil {
		t.Fatalf("ensureDaemonInstanceID: %v", err)
	}
	if !validDaemonInstanceID(id) {
		t.Fatalf("rewritten daemon instance id %q is invalid", id)
	}

	storedBytes, err := os.ReadFile(idPath)
	if err != nil {
		t.Fatalf("read daemon id file: %v", err)
	}
	stored := string(storedBytes)
	if stored != id+"\n" {
		t.Fatalf("stored daemon id = %q, want %q", stored, id+"\\n")
	}
}

func TestEnsureDaemonInstanceID_ConcurrentCallsStable(t *testing.T) {
	root := t.TempDir()
	const workers = 16
	results := make(chan string, workers)
	errs := make(chan error, workers)

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := ensureDaemonInstanceID(root)
			if err != nil {
				errs <- err
				return
			}
			results <- id
		}()
	}
	wg.Wait()
	close(results)
	close(errs)

	for err := range errs {
		t.Fatalf("ensureDaemonInstanceID concurrent call: %v", err)
	}

	var first string
	for id := range results {
		if !validDaemonInstanceID(id) {
			t.Fatalf("invalid daemon instance id %q", id)
		}
		if first == "" {
			first = id
			continue
		}
		if id != first {
			t.Fatalf("daemon instance id mismatch across concurrent calls: first=%q current=%q", first, id)
		}
	}
}
