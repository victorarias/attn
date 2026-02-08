package pty

import "strings"

const (
	stateWorking         = "working"
	stateWaitingInput    = "waiting_input"
	statePendingApproval = "pending_approval"
	stateIdle            = "idle"
)

type stateHeuristics struct {
	promptMarkers       []string
	statusMarkers       []string
	requestPhrases      []string
	listRequestTriggers []string
}

var defaultStateHeuristics = stateHeuristics{
	promptMarkers: []string{" › ", " > ", "❯ ", "» ", "❱ "},
	statusMarkers: []string{"context left", "for shortcuts"},
	requestPhrases: []string{
		"let me know what",
		"let me know if",
		"tell me what else",
		"tell me what to do",
		"what should i do",
		"what would you like",
		"what do you want",
		"how can i help",
		"can you",
		"could you",
		"do you want",
	},
	listRequestTriggers: []string{"pick one", "choose", "select", "tell me"},
}

type codexStateDetector struct {
	tail      string
	lastState string
}

func newCodexStateDetector() *codexStateDetector {
	return &codexStateDetector{}
}

func (d *codexStateDetector) Observe(chunk []byte) (string, bool) {
	if len(chunk) == 0 {
		return "", false
	}
	cleaned := stripANSI(string(chunk))
	if strings.TrimSpace(cleaned) == "" {
		return "", false
	}

	d.tail += cleaned
	const maxTail = 2000
	if len(d.tail) > maxTail {
		d.tail = trimToLastChars(d.tail, maxTail)
	}

	recent := tailLines(d.tail, 6)
	desired := classifyState(recent, defaultStateHeuristics)
	if desired == "" {
		return "", false
	}
	if desired == d.lastState {
		return "", false
	}
	d.lastState = desired
	return desired, true
}

func trimToLastChars(input string, maxChars int) string {
	if maxChars <= 0 {
		return ""
	}
	if len(input) <= maxChars {
		return input
	}
	chars := 0
	for i := len(input); i > 0; {
		_, size := decodeLastRuneInString(input[:i])
		i -= size
		chars++
		if chars == maxChars {
			return input[i:]
		}
	}
	return input
}

func decodeLastRuneInString(s string) (rune, int) {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i]&0xc0 != 0x80 {
			r := []rune(s[i:])
			if len(r) > 0 {
				return r[0], len(string(r[0]))
			}
			break
		}
	}
	r := []rune(s)
	if len(r) == 0 {
		return 0, 1
	}
	return r[len(r)-1], len(string(r[len(r)-1]))
}

func tailLines(text string, maxLines int) string {
	if maxLines <= 0 {
		return ""
	}
	lines := strings.Split(text, "\n")
	if len(lines) <= maxLines {
		return strings.Join(lines, "\n")
	}
	return strings.Join(lines[len(lines)-maxLines:], "\n")
}

func stripANSI(input string) string {
	var out strings.Builder
	out.Grow(len(input))

	runes := []rune(input)
	for i := 0; i < len(runes); i++ {
		if runes[i] != '\x1b' {
			out.WriteRune(runes[i])
			continue
		}

		if i+1 >= len(runes) {
			continue
		}
		next := runes[i+1]
		switch next {
		case '[':
			i += 2
			for i < len(runes) {
				b := runes[i]
				if b >= 0x40 && b <= 0x7E {
					break
				}
				i++
			}
		case ']':
			i += 2
			for i < len(runes) {
				if runes[i] == '\x07' {
					break
				}
				if runes[i] == '\x1b' && i+1 < len(runes) && runes[i+1] == '\\' {
					i++
					break
				}
				i++
			}
		default:
			// Unknown escape; skip ESC only.
		}
	}

	return out.String()
}

func isPendingApproval(text string) bool {
	lower := strings.ToLower(text)
	if strings.Contains(lower, "would you like to run the following command") {
		return true
	}

	hasKeyword := strings.Contains(lower, "approve") ||
		strings.Contains(lower, "approval") ||
		strings.Contains(lower, "permission") ||
		strings.Contains(lower, "allow") ||
		strings.Contains(lower, "confirm") ||
		strings.Contains(lower, "proceed") ||
		strings.Contains(lower, "run this command") ||
		strings.Contains(lower, "execute command") ||
		strings.Contains(lower, "run command")

	hasPrompt := strings.Contains(lower, "y/n") ||
		strings.Contains(lower, "[y/n") ||
		strings.Contains(lower, "(y/n") ||
		strings.Contains(lower, "[y/n]") ||
		strings.Contains(lower, "y or n") ||
		strings.Contains(lower, "yes/no") ||
		strings.Contains(lower, "press y") ||
		strings.Contains(lower, "type y") ||
		strings.Contains(lower, "press enter to confirm")

	hasReason := strings.Contains(lower, "reason:")
	hasOption := strings.Contains(lower, "yes, proceed") ||
		strings.Contains(lower, "don't ask again") ||
		strings.Contains(lower, "dont ask again") ||
		strings.Contains(lower, "no, and tell")

	return (hasKeyword && hasPrompt) || (hasReason && hasOption)
}

func isPromptLine(line string) bool {
	trimmed := strings.TrimLeft(line, " \t")
	if trimmed == "" {
		return false
	}
	r := []rune(trimmed)[0]
	switch r {
	case '>', '›', '❯', '»', '❱':
		return true
	default:
		return false
	}
}

func isAssistantLine(line string) bool {
	trimmed := strings.TrimLeft(line, " \t")
	if trimmed == "" {
		return false
	}
	r := []rune(trimmed)[0]
	if r != '•' && r != '·' && r != '●' {
		return false
	}
	rest := strings.ToLower(strings.TrimSpace(string([]rune(trimmed)[1:])))
	if strings.HasPrefix(rest, "working") ||
		strings.HasPrefix(rest, "thinking") ||
		strings.HasPrefix(rest, "running") ||
		strings.HasPrefix(rest, "executing") {
		return false
	}
	return true
}

func lastAssistantText(lines []string, h stateHeuristics) string {
	for i := len(lines) - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" {
			continue
		}
		if !isAssistantLine(trimmed) {
			continue
		}
		runes := []rune(trimmed)
		if len(runes) <= 1 {
			continue
		}
		text := strings.TrimSpace(string(runes[1:]))
		for _, marker := range h.promptMarkers {
			if idx := strings.Index(text, marker); idx >= 0 {
				text = text[:idx]
			}
		}
		for _, marker := range h.statusMarkers {
			if idx := strings.Index(text, marker); idx >= 0 {
				text = text[:idx]
			}
		}
		text = strings.TrimSpace(text)
		if text != "" {
			return text
		}
	}
	return ""
}

func hasPrompt(lines []string, h stateHeuristics) bool {
	for _, line := range lines {
		if isPromptLine(line) {
			return true
		}
		for _, marker := range h.promptMarkers {
			if strings.Contains(line, marker) {
				return true
			}
		}
	}
	return false
}

func lastNonEmptyLine(lines []string) string {
	for i := len(lines) - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func hasNumberedList(lines []string) bool {
	for _, line := range lines {
		trimmed := strings.TrimLeft(line, " \t")
		if len(trimmed) < 2 {
			continue
		}
		if trimmed[0] >= '0' && trimmed[0] <= '9' && trimmed[1] == '.' {
			return true
		}
	}
	return false
}

func assistantRequestsInput(assistantText, fullText string, lines []string, h stateHeuristics) bool {
	lowerAssistant := strings.ToLower(assistantText)
	lowerFull := strings.ToLower(fullText)

	if strings.Contains(assistantText, "?") {
		return true
	}
	for _, phrase := range h.requestPhrases {
		if strings.Contains(lowerAssistant, phrase) {
			return true
		}
	}
	if hasNumberedList(lines) {
		for _, phrase := range h.listRequestTriggers {
			if strings.Contains(lowerFull, phrase) {
				return true
			}
		}
	}
	return false
}

func isWaitingInput(text string, h stateHeuristics) bool {
	lower := strings.ToLower(text)
	if strings.Contains(lower, "enter your response") ||
		strings.Contains(lower, "type your response") ||
		strings.Contains(lower, "your response:") ||
		strings.Contains(lower, "your reply:") ||
		strings.Contains(lower, "input:") {
		return true
	}

	rawLines := strings.Split(text, "\n")
	nonEmpty := make([]string, 0, len(rawLines))
	for _, line := range rawLines {
		line = strings.TrimRight(line, "\r\n")
		if strings.TrimSpace(line) == "" {
			continue
		}
		nonEmpty = append(nonEmpty, line)
	}
	if len(nonEmpty) == 0 {
		return false
	}

	last := nonEmpty[len(nonEmpty)-1]
	if strings.HasSuffix(last, "You:") || strings.HasSuffix(last, "User:") {
		return true
	}

	if isPromptLine(last) {
		assistant := lastAssistantText(nonEmpty, h)
		if assistant != "" {
			return assistantRequestsInput(assistant, text, nonEmpty, h)
		}
		return true
	}

	tailStart := len(nonEmpty) - 4
	if tailStart < 0 {
		tailStart = 0
	}
	tail := nonEmpty[tailStart:]
	hasPromptLine := false
	hasStatus := false
	for _, line := range tail {
		if isPromptLine(line) {
			hasPromptLine = true
		}
		for _, marker := range h.statusMarkers {
			if strings.Contains(line, marker) {
				hasStatus = true
				break
			}
		}
	}

	if hasPromptLine && hasStatus {
		assistant := lastAssistantText(nonEmpty, h)
		if assistant != "" {
			return assistantRequestsInput(assistant, text, nonEmpty, h)
		}
		return true
	}

	return false
}

func classifyState(text string, h stateHeuristics) string {
	cleaned := stripANSI(text)
	lines := strings.Split(cleaned, "\n")
	promptShown := hasPrompt(lines, h)
	last := lastNonEmptyLine(lines)

	if isPendingApproval(cleaned) {
		return statePendingApproval
	}
	if isWaitingInput(cleaned, h) {
		return stateWaitingInput
	}
	if promptShown && isPromptLine(last) {
		return stateIdle
	}
	if strings.TrimSpace(cleaned) != "" {
		return stateWorking
	}
	return ""
}
