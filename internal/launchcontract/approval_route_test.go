package launchcontract

import "testing"

func TestResolveApprovalRoute(t *testing.T) {
	unattended := UnattendedLaunchSpec{
		Agent:               "codex",
		ApprovalProductMode: ApprovalAuto,
		ApprovalDriverMode:  ApprovalAutoReview,
		DirectoryTrust:      TrustConfiguredDirectory,
		Recovery:            RecoveryAdoptOrRestartFresh,
	}
	for _, tc := range []struct {
		name        string
		yolo        bool
		autoApprove bool
		unattended  UnattendedLaunchSpec
		want        ApprovalRoute
	}{
		{name: "user", want: ApprovalRouteUser},
		{name: "reviewer", autoApprove: true, want: ApprovalRouteReviewer},
		{name: "bypass outranks reviewer", yolo: true, autoApprove: true, want: ApprovalRouteBypass},
		{name: "unattended reviewer", unattended: unattended, want: ApprovalRouteReviewer},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolveApprovalRoute(tc.yolo, tc.autoApprove, tc.unattended); got != tc.want {
				t.Fatalf("ResolveApprovalRoute() = %q, want %q", got, tc.want)
			}
		})
	}
}
