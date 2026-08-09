package daemon

import (
	"context"
	"fmt"

	"github.com/victorarias/attn/internal/launchcontract"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/store"
)

// recoveredApprovalRoute reads the surviving worker first because it records
// what actually launched. The session's launch intent is the durable fallback.
// A known worker route also repairs a stale intent so a later machine-level
// revive preserves the same behavior.
func (d *Daemon) recoveredApprovalRoute(sessionID string) (launchcontract.ApprovalRoute, bool) {
	if provider, ok := d.ptyBackend.(ptybackend.SessionLaunchParamsProvider); ok {
		params, err := provider.SessionLaunchParams(context.Background(), sessionID)
		if err == nil && params.Recorded {
			route, known, routeErr := recordedApprovalRoute(params.ApprovalRoute, params.YoloMode, params.UnattendedLaunch)
			if routeErr != nil {
				d.logf("recovery: ignoring invalid worker approval route for %s: %v", sessionID, routeErr)
				return "", false
			}
			if known {
				if intent, exists := d.store.LaunchIntent(sessionID); exists && intent.ApprovalRoute != route {
					intent.ApprovalRoute = route
					d.store.SetLaunchIntent(sessionID, intent)
				}
				return route, true
			}
		}
	}
	intent, ok := d.store.LaunchIntent(sessionID)
	if !ok {
		return "", false
	}
	route, known, err := recordedApprovalRoute(intent.ApprovalRoute, intent.YoloMode, intent.UnattendedLaunch)
	if err != nil {
		d.logf("recovery: ignoring invalid stored approval route for %s: %v", sessionID, err)
		return "", false
	}
	return route, known
}

// recordedApprovalRoute interprets a persisted route without consulting mutable
// daemon settings. Yolo and unattended contracts make legacy records
// unambiguous; an otherwise empty route remains unknown so callers can try a
// second durable source before conservatively treating approvals as user-owned.
func recordedApprovalRoute(route launchcontract.ApprovalRoute, yoloMode bool, unattended launchcontract.UnattendedLaunchSpec) (launchcontract.ApprovalRoute, bool, error) {
	if route != "" {
		if !route.Valid() {
			return "", false, fmt.Errorf("invalid recorded approval route %q", route)
		}
		return route, true, nil
	}
	if !unattended.IsZero() {
		if err := unattended.Validate(); err != nil {
			return "", false, fmt.Errorf("invalid recorded unattended launch contract: %w", err)
		}
		return launchcontract.ResolveApprovalRoute(false, false, unattended), true, nil
	}
	if yoloMode {
		return launchcontract.ApprovalRouteBypass, true, nil
	}
	return "", false, nil
}

// applyApprovalRoute makes the persisted effective route authoritative over the
// mutable settings from which a fresh launch would normally be composed.
func applyApprovalRoute(opts *ptybackend.SpawnOptions, route launchcontract.ApprovalRoute) error {
	if opts == nil || !route.Valid() {
		return fmt.Errorf("invalid approval route %q", route)
	}
	opts.YoloMode = false
	opts.AutoApprove = false
	opts.ApprovalRoute = route
	if !opts.UnattendedLaunch.IsZero() {
		if route != launchcontract.ApprovalRouteReviewer {
			return fmt.Errorf("unattended launch requires reviewer approval route, got %q", route)
		}
		return nil
	}
	switch route {
	case launchcontract.ApprovalRouteReviewer:
		opts.AutoApprove = true
	case launchcontract.ApprovalRouteBypass:
		opts.YoloMode = true
	}
	return nil
}

func launchIntentFromSpawnOptions(opts ptybackend.SpawnOptions, chiefOfStaff bool) store.LaunchIntent {
	return store.LaunchIntent{
		YoloMode:               opts.YoloMode,
		ApprovalRoute:          opts.ApprovalRoute,
		Executable:             opts.Executable,
		Model:                  opts.Model,
		Effort:                 opts.Effort,
		ChiefOfStaff:           chiefOfStaff,
		ResumeConversationFile: opts.ResumeConversationFile,
		UnattendedLaunch:       opts.UnattendedLaunch,
	}
}
