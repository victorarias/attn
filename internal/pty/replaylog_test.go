package pty

import "testing"

func TestReplayLogSnapshotPreservesGeometrySegments(t *testing.T) {
	log := NewReplayLog(64)
	log.Write([]byte("wide-a"), 118, 48)
	log.Write([]byte("wide-b"), 118, 48)
	log.Write([]byte("narrow"), 58, 46)

	segments, truncated := log.Snapshot()
	if truncated {
		t.Fatal("expected replay log snapshot to remain untruncated")
	}
	if len(segments) != 3 {
		t.Fatalf("segments = %d, want 3", len(segments))
	}
	if segments[0].Cols != 118 || segments[0].Rows != 48 {
		t.Fatalf("segment[0] geometry = %dx%d, want 118x48", segments[0].Cols, segments[0].Rows)
	}
	if string(segments[2].Data) != "narrow" {
		t.Fatalf("segment[2].data = %q, want narrow", segments[2].Data)
	}
}

func TestLimitReplaySegmentsTailKeepsNewestGeometryTransitions(t *testing.T) {
	segments, truncated := LimitReplaySegmentsTail([]ReplaySegment{
		{Cols: 118, Rows: 48, Data: []byte("wide-history")},
		{Cols: 58, Rows: 46, Data: []byte("narrow-tail")},
	}, len("history")+len("narrow-tail"))
	if !truncated {
		t.Fatal("expected replay segments tail to be marked truncated")
	}
	if len(segments) != 2 {
		t.Fatalf("segments = %d, want 2", len(segments))
	}
	if string(segments[0].Data) != "history" {
		t.Fatalf("segments[0].data = %q, want history", segments[0].Data)
	}
	if segments[1].Cols != 58 || segments[1].Rows != 46 {
		t.Fatalf("segment[1] geometry = %dx%d, want 58x46", segments[1].Cols, segments[1].Rows)
	}
}
