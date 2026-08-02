package bus

import "strings"

// Event names are dotted domain facts: `session.state.changed`, `ticket.commented`,
// `delegation.prepared`. The namespace `ext.<extension>.*` is reserved for facts
// published by extensions, so a subscription can always tell platform facts from
// extension facts by prefix alone.
//
// A Filter is a set of patterns; an event matches the filter if it matches any of
// them. Three pattern forms, and deliberately no more:
//
//	"*"                 every event
//	"session.*"         every event whose name starts with "session."
//	"ticket.commented"  exactly that event
//
// There is no mid-pattern wildcard and no regex. A subscriber that needs finer
// selection filters in its handler, where the logic is visible and testable,
// rather than in a pattern language every future extension author would have to
// learn from behavior.
type Filter []string

// All matches every event.
var All = Filter{"*"}

// ParseFilter builds a Filter from a stored comma-separated expression. An empty
// expression means All, so a consumer row written without a filter is not a
// consumer that receives nothing.
func ParseFilter(expr string) Filter {
	var out Filter
	for _, part := range strings.Split(expr, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return All
	}
	return out
}

// String renders the filter for storage and for `bus status`.
func (f Filter) String() string {
	if len(f) == 0 {
		return "*"
	}
	return strings.Join(f, ",")
}

// Matches reports whether an event name is selected by this filter. An empty
// filter matches everything, mirroring ParseFilter.
func (f Filter) Matches(name string) bool {
	if len(f) == 0 {
		return true
	}
	for _, pattern := range f {
		if matchPattern(pattern, name) {
			return true
		}
	}
	return false
}

func matchPattern(pattern, name string) bool {
	switch {
	case pattern == "*" || pattern == "":
		return true
	case strings.HasSuffix(pattern, ".*"):
		// "session.*" matches "session.state.changed" but not "sessions.updated"
		// and not the bare name "session": the dot is part of the prefix.
		return strings.HasPrefix(name, pattern[:len(pattern)-1])
	default:
		return pattern == name
	}
}
