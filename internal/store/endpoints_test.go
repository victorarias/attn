package store

import (
	"testing"
)

func TestStoreEndpointCRUD(t *testing.T) {
	s := New()

	record, err := s.AddEndpoint("gpu-box", "user@example")
	if err != nil {
		t.Fatalf("AddEndpoint() error = %v", err)
	}
	if record.ID == "" {
		t.Fatal("AddEndpoint() returned empty ID")
	}
	if !record.Enabled {
		t.Fatal("AddEndpoint() should default enabled=true")
	}

	got := s.GetEndpoint(record.ID)
	if got == nil {
		t.Fatal("GetEndpoint() returned nil")
	}
	if got.Name != "gpu-box" {
		t.Fatalf("GetEndpoint().Name = %q, want gpu-box", got.Name)
	}
	if got.SSHTarget != "user@example" {
		t.Fatalf("GetEndpoint().SSHTarget = %q, want user@example", got.SSHTarget)
	}

	name := "gpu-box-2"
	target := "dev@example"
	enabled := false
	updated, err := s.UpdateEndpoint(record.ID, EndpointUpdate{
		Name:      &name,
		SSHTarget: &target,
		Enabled:   &enabled,
	})
	if err != nil {
		t.Fatalf("UpdateEndpoint() error = %v", err)
	}
	if updated.Name != name {
		t.Fatalf("UpdateEndpoint().Name = %q, want %q", updated.Name, name)
	}
	if updated.SSHTarget != target {
		t.Fatalf("UpdateEndpoint().SSHTarget = %q, want %q", updated.SSHTarget, target)
	}
	if updated.Enabled {
		t.Fatal("UpdateEndpoint().Enabled = true, want false")
	}

	list := s.ListEndpoints()
	if len(list) != 1 {
		t.Fatalf("ListEndpoints() len = %d, want 1", len(list))
	}
	if list[0].ID != record.ID {
		t.Fatalf("ListEndpoints()[0].ID = %q, want %q", list[0].ID, record.ID)
	}

	if err := s.RemoveEndpoint(record.ID); err != nil {
		t.Fatalf("RemoveEndpoint() error = %v", err)
	}
	if got := s.GetEndpoint(record.ID); got != nil {
		t.Fatalf("GetEndpoint() after remove = %+v, want nil", got)
	}
}
