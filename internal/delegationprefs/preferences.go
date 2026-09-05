package delegationprefs

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/victorarias/attn/internal/protocol"
)

type Config = protocol.DelegationPreferences
type Role = protocol.DelegationRole
type Choice = protocol.DelegationChoice
type Selection = protocol.DelegationSelection
type Fallback = protocol.DelegationFallback

var ErrConflict = errors.New("delegation preferences changed; reload before saving or choosing a role")
var identifier = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

func Defaults() Config { return Config{Roles: []Role{}} }

func Validate(c Config) error {
	if c.Revision < 0 {
		return fmt.Errorf("revision must not be negative")
	}
	ids := map[string]bool{}
	for _, role := range c.Roles {
		if !identifier.MatchString(role.ID) || ids[role.ID] {
			return fmt.Errorf("invalid or duplicate role id %q", role.ID)
		}
		ids[role.ID] = true
		if strings.TrimSpace(role.Name) == "" {
			return fmt.Errorf("role %q needs a name", role.ID)
		}
		choices := map[string]bool{}
		for _, choice := range role.Choices {
			if !identifier.MatchString(choice.ID) || choices[choice.ID] {
				return fmt.Errorf("role %q has an invalid or duplicate choice id %q", role.ID, choice.ID)
			}
			choices[choice.ID] = true
			if strings.TrimSpace(choice.Name) == "" {
				return fmt.Errorf("role %q choice %q needs a name", role.ID, choice.ID)
			}
			if err := ValidateSelection(choice.Selection, false); err != nil {
				return fmt.Errorf("role %q choice %q: %w", role.ID, choice.ID, err)
			}
		}
		if !choices[role.DefaultChoiceID] {
			return fmt.Errorf("role %q needs a default choice that belongs to it", role.ID)
		}
	}
	if err := ValidateSelection(c.Fallback.Selection, false); err != nil {
		return fmt.Errorf("fallback: %w", err)
	}
	return nil
}

func ValidateSelection(s Selection, ready bool) error {
	if s.Harness == "" {
		if ready {
			return fmt.Errorf("choose a harness in Settings > Delegation")
		}
		if s.Provider != "" || s.Model != "" || s.Effort != "" {
			return fmt.Errorf("model, provider and effort need a harness")
		}
		return nil
	}
	if !identifier.MatchString(s.Harness) {
		return fmt.Errorf("invalid harness %q", s.Harness)
	}
	for _, value := range []string{s.Provider, s.Model, s.Effort} {
		if strings.ContainsAny(value, " \t\r\n\x00") {
			return fmt.Errorf("provider, model and effort must be exact identifiers without whitespace")
		}
	}
	if s.Provider != "" && s.Model == "" {
		return fmt.Errorf("a provider needs an explicit model")
	}
	return nil
}

type Request struct {
	Role     string
	Choice   string
	Fallback bool
	Revision *int
	Harness  *string
	Provider *string
	Model    *string
	Effort   *string
}

type Resolved struct {
	Selection     Selection `json:"selection"`
	RoleName      string    `json:"role_name"`
	Instructions  string    `json:"instructions"`
	StoppingPoint string    `json:"stopping_point"`
	Revision      int       `json:"revision"`
}

func Resolve(c Config, r Request) (Resolved, error) {
	out := Resolved{Revision: c.Revision}
	if !c.Enabled {
		return out, fmt.Errorf("no delegation roles available; configure Settings > Delegation")
	}
	if r.Revision != nil && *r.Revision != c.Revision {
		return out, ErrConflict
	}
	if (r.Role == "") == !r.Fallback || (r.Fallback && r.Choice != "") {
		return out, fmt.Errorf("choose one role or the unmatched-work fallback")
	}
	if r.Fallback {
		out.Selection, out.Instructions = c.Fallback.Selection, c.Fallback.Instructions
	} else {
		var role *Role
		for i := range c.Roles {
			if c.Roles[i].ID == r.Role && c.Roles[i].Enabled {
				role = &c.Roles[i]
				break
			}
		}
		if role == nil {
			return out, fmt.Errorf("role %q is unavailable; run attn delegate roles", r.Role)
		}
		choiceID := r.Choice
		if choiceID == "" {
			choiceID = role.DefaultChoiceID
		}
		var choice *Choice
		for i := range role.Choices {
			if role.Choices[i].ID == choiceID {
				choice = &role.Choices[i]
				break
			}
		}
		if choice == nil {
			return out, fmt.Errorf("choice %q does not belong to role %q", choiceID, role.ID)
		}
		if choiceID != role.DefaultChoiceID && strings.TrimSpace(choice.When) == "" {
			return out, fmt.Errorf("choice %q needs a Use when condition in Settings > Delegation", choiceID)
		}
		out.Selection, out.RoleName, out.Instructions, out.StoppingPoint = choice.Selection, role.Name, role.Instructions, role.StoppingPoint
	}
	s := &out.Selection
	if r.Harness != nil && *r.Harness != s.Harness {
		*s = Selection{Harness: *r.Harness}
	}
	if r.Model != nil && *r.Model != s.Model {
		s.Model = *r.Model
		s.Effort = ""
	}
	if r.Provider != nil && *r.Provider != s.Provider {
		s.Provider = *r.Provider
		s.Effort = ""
	}
	if s.Model == "" {
		s.Provider = ""
	}
	if r.Effort != nil {
		s.Effort = *r.Effort
	}
	if err := ValidateSelection(*s, true); err != nil {
		return out, err
	}
	return out, nil
}

func Active(c Config) protocol.DelegationRolesResult {
	out := protocol.DelegationRolesResult{Roles: []Role{}}
	if !c.Enabled {
		return out
	}
	out.Revision = c.Revision
	for _, role := range c.Roles {
		if !role.Enabled {
			continue
		}
		active := role
		active.Choices = make([]Choice, 0, len(role.Choices))
		defaultReady := false
		for _, choice := range role.Choices {
			if ValidateSelection(choice.Selection, true) != nil {
				continue
			}
			if choice.ID != role.DefaultChoiceID && strings.TrimSpace(choice.When) == "" {
				continue
			}
			active.Choices = append(active.Choices, choice)
			defaultReady = defaultReady || choice.ID == role.DefaultChoiceID
		}
		if defaultReady {
			out.Roles = append(out.Roles, active)
		}
	}
	if ValidateSelection(c.Fallback.Selection, true) == nil {
		fallback := c.Fallback
		out.Fallback = &fallback
	}
	return out
}
