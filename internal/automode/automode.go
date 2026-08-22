package automode

import (
	"fmt"
	"strings"
)

const (
	DefaultClassifierModel = "opencode-go/glm-5.3"
	DefaultEscalationModel = "opencode-go/qwen3.8-max"
)

type Config struct {
	EnabledDefault bool `json:"enabled_default"`

	Environment []string `json:"environment"`

	Allow []string `json:"allow"`

	HardDeny []string `json:"hard_deny"`

	ClassifierModels []string `json:"classifier_models"`
	EscalationModels []string `json:"escalation_models"`
}

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

func ResolveHardDeny(wsPort string, stored []string) []string {
	resolved := ShippedHardDeny(wsPort)
	for _, pattern := range stored {
		resolved = appendUnique(resolved, pattern)
	}
	return resolved
}

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

const MaxPendingProposalsPerProposer = 20

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

const (
	ListAllow    = "allow"
	ListHardDeny = "hard_deny"
)

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

func ValidateDenyPattern(pattern string) error {
	if strings.TrimSpace(pattern) == "" {
		return fmt.Errorf("deny pattern is empty")
	}
	return nil
}

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

const ModelListSeparator = ","

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

func FormatModelList(models []string) string {
	return strings.Join(models, ModelListSeparator)
}

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
