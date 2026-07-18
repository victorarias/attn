package workdelivery

import (
	"context"
	"errors"
	"github.com/victorarias/attn/internal/automation"
	"reflect"
	"testing"
)

type fakePorts struct {
	calls []string
	fail  string
}

func (f *fakePorts) step(s string) error {
	f.calls = append(f.calls, s)
	if f.fail == s {
		return errors.New("boom")
	}
	return nil
}
func (f *fakePorts) EnsureTicket(context.Context, automation.WorkRequest) error {
	return f.step("ticket")
}
func (f *fakePorts) PrepareLocation(context.Context, automation.WorkRequest) (string, error) {
	return "/tmp", f.step("location")
}
func (f *fakePorts) EnsureWorkspace(context.Context, automation.WorkRequest, string) error {
	return f.step("workspace")
}
func (f *fakePorts) EnsurePane(context.Context, automation.WorkRequest) error { return f.step("pane") }
func (f *fakePorts) EnsureSession(context.Context, automation.WorkRequest, string) error {
	return f.step("session")
}
func (f *fakePorts) VerifyDelivery(context.Context, automation.WorkRequest, string) error {
	return f.step("verify")
}
func TestDeliveryIsTicketFirstAndStopsAtFailure(t *testing.T) {
	p := &fakePorts{}
	_, err := (Service{Ports: p}).Deliver(context.Background(), automation.WorkRequest{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"ticket", "location", "workspace", "pane", "session", "verify"}
	if !reflect.DeepEqual(p.calls, want) {
		t.Fatalf("calls=%v", p.calls)
	}
	p = &fakePorts{fail: "workspace"}
	_, err = (Service{Ports: p}).Deliver(context.Background(), automation.WorkRequest{})
	if err == nil {
		t.Fatal("expected failure")
	}
	if !reflect.DeepEqual(p.calls, []string{"ticket", "location", "workspace"}) {
		t.Fatalf("calls=%v", p.calls)
	}
}
