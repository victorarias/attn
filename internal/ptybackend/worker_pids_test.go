package ptybackend

import (
	"context"
	"testing"
)

func TestWorkerBackend_WorkerPIDs(t *testing.T) {
	b := &WorkerBackend{
		sessions: map[string]*workerSession{
			"spawned":     {WorkerPID: 4242},
			"also":        {WorkerPID: 99},
			"not-spawned": {WorkerPID: 0}, // no live worker yet → omitted
		},
	}

	got := b.WorkerPIDs(context.Background())
	if len(got) != 2 {
		t.Fatalf("WorkerPIDs len = %d, want 2 (%v)", len(got), got)
	}
	if got["spawned"] != 4242 || got["also"] != 99 {
		t.Errorf("WorkerPIDs = %v, want spawned=4242 also=99", got)
	}
	if _, ok := got["not-spawned"]; ok {
		t.Errorf("WorkerPIDs included a session with no live worker: %v", got)
	}
}
