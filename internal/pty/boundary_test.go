package pty

import "testing"

func TestFindSafeBoundary_IncompleteUTF8(t *testing.T) {
	data := []byte{'a', 0xe2, 0x82}
	got := findSafeBoundary(data)
	if got != 1 {
		t.Fatalf("boundary = %d, want 1", got)
	}
}

func TestFindSafeBoundary_IncompleteANSI(t *testing.T) {
	data := []byte{'a', 0x1b, '[', '3'}
	got := findSafeBoundary(data)
	if got != 1 {
		t.Fatalf("boundary = %d, want 1", got)
	}
}

func TestFindSafeBoundary_CompleteANSI(t *testing.T) {
	data := []byte{'a', 0x1b, '[', '3', '1', 'm', 'b'}
	got := findSafeBoundary(data)
	if got != len(data) {
		t.Fatalf("boundary = %d, want %d", got, len(data))
	}
}
