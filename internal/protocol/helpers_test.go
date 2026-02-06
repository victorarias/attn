package protocol

import "testing"

func TestParsePRID(t *testing.T) {
	tests := []struct {
		id     string
		host   string
		repo   string
		number int
	}{
		{"acme/widget#42", "github.com", "acme/widget", 42},
		{"github.com:acme/widget#42", "github.com", "acme/widget", 42},
		{"ghe.corp.com:acme/widget#7", "ghe.corp.com", "acme/widget", 7},
	}
	for _, tt := range tests {
		host, repo, number, err := ParsePRID(tt.id)
		if err != nil {
			t.Fatalf("ParsePRID(%q) error: %v", tt.id, err)
		}
		if host != tt.host || repo != tt.repo || number != tt.number {
			t.Fatalf("ParsePRID(%q) = %s %s %d", tt.id, host, repo, number)
		}
	}
}

func TestParsePRIDErrors(t *testing.T) {
	bad := []string{"", "acme/widget", "acme/widget#", "acme/widget#x", "ghe:acme#1"}
	for _, id := range bad {
		if _, _, _, err := ParsePRID(id); err == nil {
			t.Fatalf("expected error for %q", id)
		}
	}
}

func TestFormatPRID(t *testing.T) {
	id := FormatPRID("ghe.corp.com", "acme/widget", 42)
	if id != "ghe.corp.com:acme/widget#42" {
		t.Fatalf("FormatPRID = %s", id)
	}
	id = FormatPRID("", "acme/widget", 42)
	if id != "github.com:acme/widget#42" {
		t.Fatalf("FormatPRID default host = %s", id)
	}
}
