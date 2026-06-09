package store

import "testing"

func TestStoreProfileRoleAssignmentAndConditionalClear(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })

	if got := s.GetProfileRole("chief_of_staff"); got != "" {
		t.Fatalf("initial role = %q, want empty", got)
	}
	if err := s.SetProfileRole("chief_of_staff", "session-a"); err != nil {
		t.Fatalf("assign role: %v", err)
	}
	if got := s.GetProfileRole("chief_of_staff"); got != "session-a" {
		t.Fatalf("role = %q, want session-a", got)
	}

	if err := s.SetProfileRole("chief_of_staff", "session-b"); err != nil {
		t.Fatalf("transfer role: %v", err)
	}
	if err := s.ClearProfileRole("chief_of_staff", "session-a"); err != nil {
		t.Fatalf("stale clear: %v", err)
	}
	if got := s.GetProfileRole("chief_of_staff"); got != "session-b" {
		t.Fatalf("role after stale clear = %q, want session-b", got)
	}

	if err := s.ClearProfileRole("chief_of_staff", "session-b"); err != nil {
		t.Fatalf("clear role: %v", err)
	}
	if got := s.GetProfileRole("chief_of_staff"); got != "" {
		t.Fatalf("role after clear = %q, want empty", got)
	}
}
