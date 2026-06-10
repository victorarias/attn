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

func TestReplayLogTrimDropsWholeSegmentsOnly(t *testing.T) {
	// Each write enters at a safe parse boundary; trimming must never slice a
	// retained segment or replay can open mid-escape-sequence after restore.
	log := NewReplayLog(40)
	writes := [][]byte{
		[]byte("\x1b[31moldest-frame-aaaa\x1b[0m"),
		[]byte("\x1b[32mmiddle-frame-bbb\x1b[0m"),
		[]byte("\x1b[33mnewest-frame-cc\x1b[0m"),
	}
	for _, w := range writes {
		log.Write(w, 80, 24)
	}

	segments, truncated := log.Snapshot()
	if !truncated {
		t.Fatal("expected overfilled replay log to report truncation")
	}
	total := 0
	for i, segment := range segments {
		total += len(segment.Data)
		matchesWholeWrite := false
		for _, w := range writes {
			if string(segment.Data) == string(w) {
				matchesWholeWrite = true
				break
			}
		}
		if !matchesWholeWrite {
			t.Fatalf("segment[%d] = %q is not one of the original whole writes", i, segment.Data)
		}
	}
	if total > 40 {
		t.Fatalf("retained %d bytes, want <= capacity 40", total)
	}
	if len(segments) == 0 || string(segments[len(segments)-1].Data) != string(writes[2]) {
		t.Fatal("expected the newest write to be retained")
	}
}

func TestReplayLogDropsOversizedSingleWrite(t *testing.T) {
	log := NewReplayLog(16)
	log.Write([]byte("\x1b[35mkeep-me\x1b[0m"), 80, 24)
	log.Write([]byte("this single write is larger than the whole log"), 80, 24)

	segments, truncated := log.Snapshot()
	if !truncated {
		t.Fatal("expected oversized write to report truncation")
	}
	if len(segments) != 0 {
		t.Fatalf("segments = %d, want 0 (a sliced segment could start mid-escape)", len(segments))
	}
}

func TestLimitReplaySegmentsTailDropsPartialOldestSegmentWhole(t *testing.T) {
	// A partially-fitting oldest segment must be dropped, not sliced: slicing
	// at a byte budget can open the replay mid-escape-sequence.
	segments, truncated := LimitReplaySegmentsTail([]ReplaySegment{
		{Cols: 118, Rows: 48, Data: []byte("wide-history")},
		{Cols: 58, Rows: 46, Data: []byte("narrow-tail")},
	}, len("history")+len("narrow-tail"))
	if !truncated {
		t.Fatal("expected replay segments tail to be marked truncated")
	}
	if len(segments) != 1 {
		t.Fatalf("segments = %d, want 1", len(segments))
	}
	if string(segments[0].Data) != "narrow-tail" {
		t.Fatalf("segments[0].data = %q, want narrow-tail", segments[0].Data)
	}
	if segments[0].Cols != 58 || segments[0].Rows != 46 {
		t.Fatalf("segment[0] geometry = %dx%d, want 58x46", segments[0].Cols, segments[0].Rows)
	}
}

func TestLimitReplaySegmentsTailDropsAllWhenNewestSegmentExceedsLimit(t *testing.T) {
	segments, truncated := LimitReplaySegmentsTail([]ReplaySegment{
		{Cols: 80, Rows: 24, Data: []byte("oversized-newest-segment")},
	}, 4)
	if !truncated {
		t.Fatal("expected replay segments tail to be marked truncated")
	}
	if len(segments) != 0 {
		t.Fatalf("segments = %d, want 0", len(segments))
	}
}
