// Package automode is attn's half of pi's auto mode: the config value the
// pi-side decision tree is evaluated against, and the rules about what may be
// written into it.
//
// It is the Go mirror of plugins/attn-pi/automode/config.ts, and the JSON tags
// here ARE that file's `RawAutoModeConfig` — a Config marshalled by this package
// is what `loadAutoModeConfig` parses on the other side. The two must stay in
// lockstep; a field renamed here without renaming it there silently drops to the
// pi-side default.
//
// Design: docs/plans/2026-08-16-pi-auto-mode.md.
package automode

import (
	"fmt"
	"strings"
)

// Defaults from the classifier receipt in the plan doc. They are settings, not
// pins: a stored empty model means "whichever default ships", so changing these
// reaches every user who never picked one.
const (
	DefaultClassifierModel = "opencode-go/glm-5.3"
	DefaultEscalationModel = "opencode-go/qwen3.8-max"
)

// Config is auto mode's promoted policy — what a session actually launches
// with. Everything in it has been through the app; a proposal is not part of it
// until a human promotes one.
type Config struct {
	// Whether an attn session starts with auto mode on.
	EnabledDefault bool `json:"enabled_default"`
	// Prose the classifier reads to learn what this machine may do.
	Environment []string `json:"environment"`
	// Narrow patterns that skip the classifier and run.
	Allow []string `json:"allow"`
	// Patterns refused before anything else looks at the call.
	HardDeny []string `json:"hard_deny"`
	// Layer 2a, and layer 2b for what 2a cannot decide. Always resolved: a
	// caller never has to know what the built-in default is.
	ClassifierModel string `json:"classifier_model"`
	EscalationModel string `json:"escalation_model"`
}

// Defaults is the config a machine that has never been configured runs on.
func Defaults() Config {
	return Config{
		EnabledDefault:  true,
		Environment:     []string{},
		Allow:           []string{},
		HardDeny:        []string{},
		ClassifierModel: DefaultClassifierModel,
		EscalationModel: DefaultEscalationModel,
	}
}

// ShippedHardDeny is auto mode's own leash: the patterns every machine gets
// whether or not anyone configured one. A session under auto mode reaching for
// the surfaces that decide what auto mode permits is denied by policy here, not
// only by which transport carries which verb.
//
// Two surfaces, matched against a call signature (the bare command, for bash):
// the `attn automode` verbs that write — environment prose lands in the
// classifier's own prompt, and a proposal the agent files is a line in the
// human's review list — and the app's WebSocket port, where promotion lives.
// The read-only verbs (`show`, `denials`) stay reachable on purpose: a denied
// agent explaining what stopped it is the behavior the plan asks for.
//
// wsPort is the daemon's own port, which is per-profile, so the deny names the
// port this machine actually listens on rather than a hardcoded 9849.
func ShippedHardDeny(wsPort string) []string {
	patterns := []string{
		"*attn automode env*",
		"*attn automode allow*",
		"*attn automode deny *",
		"*attn automode model*",
	}
	if wsPort = strings.TrimSpace(wsPort); wsPort != "" {
		patterns = append(patterns,
			"*localhost:"+wsPort+"*",
			"*127.0.0.1:"+wsPort+"*",
			"*[::1]:"+wsPort+"*",
		)
	}
	return patterns
}

// ResolveHardDeny is what a caller reads: the shipped denies first, then
// whatever a human promoted. Shipped entries are resolved at read rather than
// written into anyone's row, the same way an unset model resolves to the
// default — so changing this list reaches every machine, and no stored row can
// drop an entry from it.
func ResolveHardDeny(wsPort string, stored []string) []string {
	resolved := ShippedHardDeny(wsPort)
	for _, pattern := range stored {
		resolved = appendUnique(resolved, pattern)
	}
	return resolved
}

// StripShippedHardDeny is ResolveHardDeny's inverse, for the write path: a
// config read, changed, and written back must not persist the shipped entries
// it was handed.
func StripShippedHardDeny(wsPort string, resolved []string) []string {
	shipped := map[string]bool{}
	for _, pattern := range ShippedHardDeny(wsPort) {
		shipped[pattern] = true
	}
	stored := []string{}
	for _, pattern := range resolved {
		if !shipped[pattern] {
			stored = append(stored, pattern)
		}
	}
	return stored
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

// MaxPendingProposalsPerProposer caps how many unresolved proposals one
// proposer can hold. The receipt is pi's own circuit breaker: a session is
// stopped for a human question at 20 denials (docs/plans/2026-08-16-pi-auto-mode.md),
// and a denial is what prompts a proposal, so no healthy session reaches this.
// A proposer that does has stopped asking and started burying the review list.
const MaxPendingProposalsPerProposer = 20

// Proposal kinds and model targets. A proposal names one change; promotion in
// the app is what applies it.
const (
	KindAllow = "allow"
	KindDeny  = "deny"
	KindModel = "model"

	TargetClassifier = "classifier"
	TargetEscalation = "escalation"

	StatePending   = "pending"
	StatePromoted  = "promoted"
	StateDiscarded = "discarded"
)

// IsBroadPattern reports whether a pattern names nothing: with the wildcards and
// whitespace removed there is no literal left, so it matches every call it is
// asked about. Mirrors isBroadPattern in config.ts.
func IsBroadPattern(pattern string) bool {
	stripped := strings.Map(func(r rune) rune {
		switch r {
		case '*', '?', ' ', '\t', '\n', '\r':
			return -1
		}
		return r
	}, pattern)
	return stripped == ""
}

// ValidateAllowPattern refuses an allow entry that names nothing. Only allow is
// checked: a broad hard-deny refuses everything, which is safe, while a broad
// allow is the leash removing itself.
func ValidateAllowPattern(pattern string) error {
	if strings.TrimSpace(pattern) == "" {
		return fmt.Errorf("allow pattern is empty")
	}
	if IsBroadPattern(pattern) {
		return fmt.Errorf(
			"broad allow pattern %q is refused: an allow entry must name something. "+
				"A blanket allow is what the classifier exists to replace", pattern)
	}
	return nil
}

// ValidateProposal checks one proposed change before it is recorded. Refusing at
// submission is what keeps an unpromotable entry out of the app's review list.
func ValidateProposal(kind, target, value string) error {
	value = strings.TrimSpace(value)
	switch kind {
	case KindAllow:
		if target != "" {
			return fmt.Errorf("an %s proposal takes no target", kind)
		}
		return ValidateAllowPattern(value)
	case KindDeny:
		if target != "" {
			return fmt.Errorf("a %s proposal takes no target", kind)
		}
		if value == "" {
			return fmt.Errorf("deny pattern is empty")
		}
		return nil
	case KindModel:
		if target != TargetClassifier && target != TargetEscalation {
			return fmt.Errorf("model target must be %q or %q, got %q", TargetClassifier, TargetEscalation, target)
		}
		if value == "" {
			return fmt.Errorf("model id is empty")
		}
		if !strings.Contains(value, "/") {
			return fmt.Errorf("model %q is not a provider/id pair", value)
		}
		return nil
	default:
		return fmt.Errorf("unknown proposal kind %q (want %s, %s or %s)", kind, KindAllow, KindDeny, KindModel)
	}
}
