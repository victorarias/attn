package launchcontract

import (
	"errors"
	"fmt"
	"strings"
)

const (
	ApprovalAuto       = "auto"
	ApprovalAutoReview = "auto_review"

	TrustConfiguredDirectory = "configured_directory"

	RecoveryAdoptOrRestartFresh = "adopt_or_restart_fresh"
)

// UnattendedLaunchSpec is the complete, immutable launch policy for a
// daemon-owned unattended agent. It is copied as a value through the daemon,
// PTY backend, worker registry, and wrapper boundary.
type UnattendedLaunchSpec struct {
	Agent               string `json:"driver"`
	Model               string `json:"model,omitempty"`
	Effort              string `json:"effort,omitempty"`
	Executable          string `json:"executable,omitempty"`
	ApprovalProductMode string `json:"approval_product_mode"`
	ApprovalDriverMode  string `json:"approval_driver_mode"`
	DirectoryTrust      string `json:"directory_trust"`
	Recovery            string `json:"recovery"`
}

func (s UnattendedLaunchSpec) IsZero() bool {
	return s == (UnattendedLaunchSpec{})
}

// WithLegacyDefaults upgrades Slice 1 snapshots written before directory trust
// and restart behavior were explicit parts of the contract.
func (s UnattendedLaunchSpec) WithLegacyDefaults() UnattendedLaunchSpec {
	if strings.TrimSpace(s.Agent) == "" {
		return s
	}
	if s.DirectoryTrust == "" {
		s.DirectoryTrust = TrustConfiguredDirectory
	}
	if s.Recovery == "" {
		s.Recovery = RecoveryAdoptOrRestartFresh
	}
	return s
}

func (s UnattendedLaunchSpec) Validate() error {
	if strings.TrimSpace(s.Agent) == "" {
		return errors.New("unattended launch agent is required")
	}
	if s.ApprovalProductMode != ApprovalAuto {
		return fmt.Errorf("unsupported unattended product approval mode %q", s.ApprovalProductMode)
	}
	switch s.ApprovalDriverMode {
	case ApprovalAuto, ApprovalAutoReview:
	default:
		return fmt.Errorf("unsupported unattended driver approval mode %q", s.ApprovalDriverMode)
	}
	if s.DirectoryTrust != TrustConfiguredDirectory {
		return fmt.Errorf("unsupported unattended directory trust %q", s.DirectoryTrust)
	}
	if s.Recovery != RecoveryAdoptOrRestartFresh {
		return fmt.Errorf("unsupported unattended recovery policy %q", s.Recovery)
	}
	return nil
}
