package workdelivery

import (
	"context"
	"fmt"

	"github.com/victorarias/attn/internal/automation"
)

// Ports is the daemon-owned adapter for visible attn work. Stable IDs make every
// operation an ensure/adopt operation so the whole sequence can be replayed.
type Ports interface {
	EnsureTicket(context.Context, automation.WorkRequest) error
	PrepareLocation(context.Context, automation.WorkRequest) (automation.PreparedLocation, error)
	BindTicketLocation(context.Context, automation.WorkRequest, automation.PreparedLocation) error
	EnsureWorkspace(context.Context, automation.WorkRequest, string) error
	EnsurePane(context.Context, automation.WorkRequest) error
	EnsureSession(context.Context, automation.WorkRequest, string) error
	VerifyDelivery(context.Context, automation.WorkRequest, string) error
}

type Service struct{ Ports Ports }

func (s Service) Deliver(ctx context.Context, req automation.WorkRequest) (automation.DeliveryResult, error) {
	if s.Ports == nil {
		return automation.DeliveryResult{}, fmt.Errorf("work delivery ports unavailable")
	}
	if err := s.Ports.EnsureTicket(ctx, req); err != nil {
		return automation.DeliveryResult{}, fmt.Errorf("ensure ticket: %w", err)
	}
	location, err := s.Ports.PrepareLocation(ctx, req)
	if err != nil {
		return automation.DeliveryResult{}, fmt.Errorf("prepare location: %w", err)
	}
	if err := s.Ports.BindTicketLocation(ctx, req, location); err != nil {
		return automation.DeliveryResult{}, fmt.Errorf("bind ticket location: %w", err)
	}
	if err := s.Ports.EnsureWorkspace(ctx, req, location.Directory); err != nil {
		return automation.DeliveryResult{}, fmt.Errorf("ensure workspace: %w", err)
	}
	if err := s.Ports.EnsurePane(ctx, req); err != nil {
		return automation.DeliveryResult{}, fmt.Errorf("ensure pane: %w", err)
	}
	if err := s.Ports.EnsureSession(ctx, req, location.Directory); err != nil {
		return automation.DeliveryResult{}, fmt.Errorf("ensure session: %w", err)
	}
	if err := s.Ports.VerifyDelivery(ctx, req, location.Directory); err != nil {
		return automation.DeliveryResult{}, fmt.Errorf("verify delivery: %w", err)
	}
	return automation.DeliveryResult{TicketID: req.IDs.TicketID, SessionID: req.IDs.SessionID, WorkspaceID: req.IDs.WorkspaceID, Directory: location.Directory, Revision: location.Revision, Resolved: location.Resolved, Mode: "created"}, nil
}
