package prompts

import "sort"

func Definitions() []Recipient {
	s := session
	s.Events = append(append([]Event(nil), s.Events...), sessionNudges()...)
	s.Events = append(s.Events, gardenReadyEvent(), gardenRowEvent())
	s.Events = append(s.Events, feedbackEvents()...)
	s.Events = append(s.Events, On("garden-update", "inbox_content", "A watched seed moved; returned by the durable inbox.", template("session.garden-update", "content/session/garden-update.md", seedID, TextField("event_kind", "Garden event kind."))))
	recipients := append(append([]Recipient{s}, lifecycleRecipients()...), originRecipients()...)
	recipients = append(recipients, delegationPreferencesRecipient(), gardenAdvisorRecipient(), piRecipient(), piEnvironmentRecipient(), piSessionRecipient(), piSecurityRecipient(), activityRecipient(), skillRecipient(), resourceRecipient(), annotationLabelRecipient(), evidenceRecipient())
	recipients = append(recipients, annotationRecipients()...)
	order := []string{"session", "crew", "chief", "delegation", "automation", "pi-permission", "pi-session", "activity", "turn-classifier", "session-title", "session-instructions", "keeper", "ticket-reconciler", "workflow-agent", "attn-skill"}
	rank := func(id string) int {
		for i, name := range order {
			if name == id {
				return i
			}
		}
		return len(order)
	}
	sort.SliceStable(recipients, func(i, j int) bool { return rank(recipients[i].ID) < rank(recipients[j].ID) })
	return recipients
}
