package launchcontract

// ApprovalRoute records who actually receives an agent's approval requests.
// The empty value is reserved for launch records written before this field was
// persisted; it must never be interpreted from a later global setting.
type ApprovalRoute string

const (
	ApprovalRouteUser     ApprovalRoute = "user"
	ApprovalRouteReviewer ApprovalRoute = "reviewer"
	ApprovalRouteBypass   ApprovalRoute = "bypass"
)

func (r ApprovalRoute) Valid() bool {
	switch r {
	case ApprovalRouteUser, ApprovalRouteReviewer, ApprovalRouteBypass:
		return true
	default:
		return false
	}
}

func (r ApprovalRoute) ReviewerInLoop() bool {
	return r == ApprovalRouteReviewer
}

// ResolveApprovalRoute derives the effective approval route from a final launch
// contract. Call it after unattended policy has replaced the attended flags.
func ResolveApprovalRoute(yoloMode, autoApprove bool, unattended UnattendedLaunchSpec) ApprovalRoute {
	if !unattended.IsZero() {
		// Every currently supported unattended driver mode delegates the decision:
		// Claude's "auto" classifier and Codex's "auto_review" guardian.
		return ApprovalRouteReviewer
	}
	if yoloMode {
		return ApprovalRouteBypass
	}
	if autoApprove {
		return ApprovalRouteReviewer
	}
	return ApprovalRouteUser
}
