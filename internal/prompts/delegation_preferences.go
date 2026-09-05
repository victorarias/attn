package prompts

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/victorarias/attn/internal/protocol"
)

func delegationPreferencesRecipient() Recipient {
	fields := func(names ...string) []Field {
		out := []Field{}
		for _, name := range names {
			out = append(out, TextField(name, name))
		}
		return out
	}
	events := []Event{}
	for _, spec := range []struct {
		id    string
		names []string
	}{
		{"guidance", []string{"revision"}}, {"empty", nil},
		{"role", []string{"name", "id", "description", "instructions", "stopping_point", "choices"}},
		{"choice", []string{"kind", "name", "id", "selection", "condition"}},
		{"fallback", []string{"selection", "instructions"}},
		{"execution", []string{"name", "instructions", "stopping_point"}},
		{"fallback-execution", []string{"instructions"}},
		{"opening", []string{"task", "guidance"}},
	} {
		events = append(events, On(spec.id, "message_fragment", "Delegation preference guidance supplied on demand.", template("delegation-preferences."+spec.id, "content/delegation-preferences/"+spec.id+".md", fields(spec.names...)...)))
	}
	for _, role := range []string{"scout", "design", "build", "ship", "review"} {
		for _, field := range []string{"description", "instructions", "stopping-point"} {
			id := role + "-" + field
			events = append(events, On(id, "message_fragment", "Editable starting guidance for a delegation role.", Use("delegation-preferences."+id, "content/delegation-preferences/"+id+".md")))
		}
	}
	return Recipient{ID: "delegation-preferences", Description: "On-demand configured roles and selected execution guidance.", Events: events}
}

func preferenceText(event string, values Values) string {
	result, err := builtin.Render("delegation-preferences", event, values)
	if err != nil {
		panic(err)
	}
	return result.Text
}

func DelegationRoleTemplates() []protocol.DelegationRole {
	result := []protocol.DelegationRole{}
	for _, item := range []struct{ id, name, icon string }{{"scout", "Scout", "search"}, {"design", "Design", "diamond"}, {"build", "Build", "code"}, {"ship", "Ship", "arrow"}, {"review", "Review", "list"}} {
		result = append(result, protocol.DelegationRole{ID: item.id, Name: item.name, Icon: item.icon, Enabled: true, Description: preferenceText(item.id+"-description", nil), Instructions: preferenceText(item.id+"-instructions", nil), StoppingPoint: preferenceText(item.id+"-stopping-point", nil), DefaultChoiceID: "default", Choices: []protocol.DelegationChoice{{ID: "default", Name: "Default"}}})
	}
	return result
}

func DelegationRoutingGuidance(revision int) string {
	return preferenceText("guidance", Values{"revision": strconv.Itoa(revision)})
}

func DelegationRolesText(result protocol.DelegationRolesResult) string {
	if len(result.Roles) == 0 && result.Fallback == nil {
		return preferenceText("empty", nil)
	}
	parts := []string{result.Guidance}
	selection := func(value protocol.DelegationSelection) string { raw, _ := json.Marshal(value); return string(raw) }
	for _, r := range result.Roles {
		choices := []string{}
		for _, c := range r.Choices {
			kind, condition := "Alternative", c.When
			if c.ID == r.DefaultChoiceID {
				kind = "Default"
				condition = "When no alternative fits."
			}
			choices = append(choices, preferenceText("choice", Values{"kind": kind, "name": c.Name, "id": c.ID, "selection": selection(c.Selection), "condition": condition}))
		}
		parts = append(parts, preferenceText("role", Values{"name": r.Name, "id": r.ID, "description": r.Description, "instructions": r.Instructions, "stopping_point": r.StoppingPoint, "choices": strings.Join(choices, "\n")}))
	}
	if f := result.Fallback; f != nil {
		parts = append(parts, preferenceText("fallback", Values{"selection": selection(f.Selection), "instructions": f.Instructions}))
	}
	return strings.Join(parts, "\n\n")
}

func DelegationExecutionGuidance(name, instructions, stoppingPoint string) string {
	if name == "" {
		if strings.TrimSpace(instructions) == "" {
			return ""
		}
		return preferenceText("fallback-execution", Values{"instructions": instructions})
	}
	return preferenceText("execution", Values{"name": name, "instructions": instructions, "stopping_point": stoppingPoint})
}

func DelegationOpeningWithGuidance(task, guidance string) string {
	return preferenceText("opening", Values{"task": task, "guidance": guidance})
}
