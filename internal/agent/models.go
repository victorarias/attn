package agent

import (
	"fmt"
	"sort"
	"strings"
)

type modelCatalog struct {
	canonical []string
	aliases   map[string]string
}

var launchModelCatalogs = map[string]modelCatalog{
	"claude": {
		canonical: []string{"claude-fable-5", "claude-opus-5", "claude-sonnet-4-6", "claude-haiku-4-5"},
		aliases: map[string]string{
			"fable":  "claude-fable-5",
			"opus":   "claude-opus-5",
			"sonnet": "claude-sonnet-4-6",
			"haiku":  "claude-haiku-4-5",
		},
	},
	"codex": {
		canonical: []string{
			"gpt-5.6-luna",
			"gpt-5.6-terra",
			"gpt-5.6-sol",
			"gpt-5.5",
			"gpt-5.4-codex",
			"gpt-5.4",
			"gpt-5.4-mini",
		},
	},
}

// ResolveLaunchModel validates a built-in agent's model pin and returns its
// canonical identifier. Plugin and other driver-owned model names pass through.
func ResolveLaunchModel(agent, requested string) (string, error) {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return "", nil
	}
	catalog, ok := launchModelCatalogs[strings.ToLower(strings.TrimSpace(agent))]
	if !ok {
		return requested, nil
	}

	normalized := strings.ToLower(requested)
	if canonical, ok := catalog.aliases[normalized]; ok {
		return canonical, nil
	}
	for _, canonical := range catalog.canonical {
		if normalized == canonical {
			return canonical, nil
		}
	}

	suggestions := modelSuggestions(normalized, catalog)
	return "", fmt.Errorf("model %q is not supported by agent %q; supported models: %s%s%s",
		requested,
		agent,
		strings.Join(catalog.canonical, ", "),
		formatModelAliases(catalog.aliases),
		formatModelSuggestions(suggestions),
	)
}

func modelSuggestions(requested string, catalog modelCatalog) []string {
	var prefixMatches []string
	for _, canonical := range catalog.canonical {
		if strings.HasPrefix(canonical, requested+"-") {
			prefixMatches = append(prefixMatches, canonical)
		}
		trimmed := strings.TrimPrefix(canonical, "claude-")
		if requested == trimmed {
			return []string{canonical}
		}
	}
	if len(prefixMatches) > 0 {
		return prefixMatches
	}

	type candidate struct {
		model    string
		distance int
	}
	var candidates []candidate
	for _, canonical := range catalog.canonical {
		distance := editDistance(requested, canonical)
		trimmed := strings.TrimPrefix(canonical, "claude-")
		if shorter := editDistance(requested, trimmed); shorter < distance {
			distance = shorter
		}
		candidates = append(candidates, candidate{model: canonical, distance: distance})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].distance == candidates[j].distance {
			return candidates[i].model < candidates[j].model
		}
		return candidates[i].distance < candidates[j].distance
	})
	if len(candidates) > 0 && candidates[0].distance <= 3 {
		return []string{candidates[0].model}
	}
	return nil
}

func formatModelSuggestions(suggestions []string) string {
	switch len(suggestions) {
	case 0:
		return ""
	case 1:
		return fmt.Sprintf("; did you mean %q?", suggestions[0])
	default:
		quoted := make([]string, len(suggestions))
		for index, suggestion := range suggestions {
			quoted[index] = fmt.Sprintf("%q", suggestion)
		}
		return "; did you mean one of " + strings.Join(quoted, ", ") + "?"
	}
}

func formatModelAliases(aliases map[string]string) string {
	if len(aliases) == 0 {
		return ""
	}
	names := make([]string, 0, len(aliases))
	for alias := range aliases {
		names = append(names, alias)
	}
	sort.Strings(names)
	return " (aliases: " + strings.Join(names, ", ") + ")"
}

func editDistance(left, right string) int {
	leftRunes, rightRunes := []rune(left), []rune(right)
	previous := make([]int, len(rightRunes)+1)
	for index := range previous {
		previous[index] = index
	}
	for leftIndex, leftRune := range leftRunes {
		current := make([]int, len(rightRunes)+1)
		current[0] = leftIndex + 1
		for rightIndex, rightRune := range rightRunes {
			cost := 1
			if leftRune == rightRune {
				cost = 0
			}
			current[rightIndex+1] = min(
				current[rightIndex]+1,
				previous[rightIndex+1]+1,
				previous[rightIndex]+cost,
			)
		}
		previous = current
	}
	return previous[len(rightRunes)]
}
