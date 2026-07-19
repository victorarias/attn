package launchcontract

import "testing"

func TestUnattendedLaunchSpecLegacyDefaults(t *testing.T) {
	spec := UnattendedLaunchSpec{
		Agent:               "codex",
		ApprovalProductMode: ApprovalAuto,
		ApprovalDriverMode:  ApprovalAutoReview,
	}.WithLegacyDefaults()
	if spec.DirectoryTrust != TrustConfiguredDirectory || spec.Recovery != RecoveryAdoptOrRestartFresh {
		t.Fatalf("legacy defaults = %#v", spec)
	}
	if err := spec.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestUnattendedLaunchSpecRejectsIncompletePolicy(t *testing.T) {
	if err := (UnattendedLaunchSpec{Agent: "codex"}).Validate(); err == nil {
		t.Fatal("Validate() accepted an incomplete unattended policy")
	}
}
