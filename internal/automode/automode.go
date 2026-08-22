// Package automode is attn's half of pi's auto mode: the config value the
// pi-side decision tree runs on, and the rules about what may be written into
// it. The JSON tags here ARE config.ts's `RawAutoModeConfig`; a field renamed
// on one side and not the other silently drops to the pi-side default.
//
// Design: docs/plans/2026-08-16-pi-auto-mode.md.
package automode

import (
	"fmt"
	"strings"
)

// Defaults from the classifier receipt in the plan doc. Settings, not pins:
// changing these reaches every user who never picked a model.
const (
	DefaultClassifierModel = "opencode-go/glm-5.3"
	DefaultEscalationModel = "opencode-go/qwen3.8-max"
)

// Config is auto mode's promoted policy — what a session actually launches
// with. A proposal is not part of it until a human promotes one.
type Config struct {
	// Whether an attn session starts with auto mode on.
	EnabledDefault bool `json:"enabled_default"`
	// Prose the classifier reads to learn what this machine may do.
	Environment []string `json:"environment"`
	// Narrow patterns that skip the classifier and run.
	Allow []string `json:"allow"`
	// Patterns refused before anything else looks at the call.
	HardDeny []string `json:"hard_deny"`
	// Ordered, primary first: pi walks the rest only on an unreachable
	// endpoint. Always resolved and never empty.
	ClassifierModels []string `json:"classifier_models"`
	EscalationModels []string `json:"escalation_models"`
}

// Defaults is the config a machine that has never been configured runs on.
func Defaults() Config {
	return Config{
		EnabledDefault:   true,
		Environment:      []string{},
		Allow:            []string{},
		HardDeny:         []string{},
		ClassifierModels: []string{DefaultClassifierModel},
		EscalationModels: []string{DefaultEscalationModel},
	}
}

// ShippedHardDeny is auto mode's own leash: the patterns every machine gets
// whether or not anyone configured one. It covers the `attn automode` verbs
// that write and the app's WebSocket port, where promotion lives. The read-only
// verbs stay reachable so a denied agent can explain what stopped it. wsPort is
// per-profile, so the deny names the port this machine listens on.
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

// ResolveHardDeny returns the shipped denies first, then whatever a human
// promoted. Resolved at read, never stored, so no row can drop an entry.
func ResolveHardDeny(wsPort string, stored []string) []string {
	resolved := ShippedHardDeny(wsPort)
	for _, pattern := range stored {
		resolved = appendUnique(resolved, pattern)
	}
	return resolved
}

// StripShippedHardDeny is ResolveHardDeny's inverse, for the write path.
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

// MaxPendingProposalsPerProposer caps one proposer's unresolved proposals. The
// receipt is pi's own breaker, which stops a session at 20 denials, so no
// healthy session reaches this.
const MaxPendingProposalsPerProposer = 20

// Proposal kinds and model targets. Promotion in the app applies one.
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

// The two pattern lists a human edits directly in the app, named as Config's
// fields on the wire. A proposal names a Kind instead.
const (
	ListAllow    = "allow"
	ListHardDeny = "hard_deny"
)

// IsBroadPattern reports whether a pattern names nothing, so it matches every
// call. Mirrors isBroadPattern in config.ts.
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

// ValidateAllowPattern refuses an allow entry that names nothing: a broad allow
// is the leash removing itself.
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

// ValidateDenyPattern refuses a deny entry that names nothing. A broad deny is
// safe, so only an empty one is rejected.
func ValidateDenyPattern(pattern string) error {
	if strings.TrimSpace(pattern) == "" {
		return fmt.Errorf("deny pattern is empty")
	}
	return nil
}

// ValidatePattern dispatches to one of the two editable lists' validators.
func ValidatePattern(list, pattern string) error {
	switch list {
	case ListAllow:
		return ValidateAllowPattern(pattern)
	case ListHardDeny:
		return ValidateDenyPattern(pattern)
	default:
		return fmt.Errorf("unknown pattern list %q (want %s or %s)", list, ListAllow, ListHardDeny)
	}
}

// ModelListSeparator packs an ordered list into a proposal's single value
// column: `provider/id,provider/id`, primary first. Promotion REPLACES a
// layer's list rather than appending, so there is no reorder verb to miss.
const ModelListSeparator = ","

// ParseModelList reads a proposal's value into a layer's ordered list. Every
// entry must be `provider/id`, and an empty list is refused.
func ParseModelList(value string) ([]string, error) {
	models := []string{}
	for _, entry := range strings.Split(value, ModelListSeparator) {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if !strings.Contains(entry, "/") {
			return nil, fmt.Errorf("model %q is not a provider/id pair", entry)
		}
		for _, seen := range models {
			if seen == entry {
				return nil, fmt.Errorf("model %q is named twice; a layer walks each model once", entry)
			}
		}
		models = append(models, entry)
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("no model named: a layer needs at least one %s-separated provider/id", ModelListSeparator)
	}
	return models, nil
}

// FormatModelList is ParseModelList's inverse.
func FormatModelList(models []string) string {
	return strings.Join(models, ModelListSeparator)
}

// ValidateProposal checks one proposed change before it is recorded, sharing
// its pattern validators with the app's direct editor.
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
		return ValidateDenyPattern(value)
	case KindModel:
		if target != TargetClassifier && target != TargetEscalation {
			return fmt.Errorf("model target must be %q or %q, got %q", TargetClassifier, TargetEscalation, target)
		}
		_, err := ParseModelList(value)
		return err
	default:
		return fmt.Errorf("unknown proposal kind %q (want %s, %s or %s)", kind, KindAllow, KindDeny, KindModel)
	}
}
