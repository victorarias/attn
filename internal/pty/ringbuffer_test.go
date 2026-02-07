package pty

import "testing"

func TestRingBufferSnapshot_NoWrap(t *testing.T) {
	r := NewRingBuffer(8)
	r.Write([]byte("abc"))
	got, truncated := r.Snapshot()
	if string(got) != "abc" {
		t.Fatalf("snapshot = %q, want %q", string(got), "abc")
	}
	if truncated {
		t.Fatalf("truncated = true, want false")
	}
}

func TestRingBufferSnapshot_Wrap(t *testing.T) {
	r := NewRingBuffer(5)
	r.Write([]byte("abcdefg"))
	got, truncated := r.Snapshot()
	if string(got) != "cdefg" {
		t.Fatalf("snapshot = %q, want %q", string(got), "cdefg")
	}
	if !truncated {
		t.Fatalf("truncated = false, want true")
	}
}
