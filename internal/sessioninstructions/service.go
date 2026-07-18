// Package sessioninstructions answers bounded questions from one native Codex
// conversation. It deliberately contains no authorization, workflow, or
// resource taxonomy: the model interprets the question and attn validates only
// transcript identity and the excerpts it returns.
package sessioninstructions

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/victorarias/attn/internal/protocol"
)

const (
	ModelName              = "gpt-5.6-luna"
	lowEffort              = "low"
	mediumEffort           = "medium"
	defaultSnapshotMax     = 8 << 20
	defaultConversationMax = 120_000
)

// Error is a stable, safe-to-render command failure. Its Message never
// includes transcript or question content.
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Message }

var ErrInvalidResponse = errors.New("invalid structured model response")

type ConversationTurn struct {
	ID        string
	Author    string
	Text      string
	Timestamp string
}

type CandidateEvidence struct {
	TurnID string `json:"turn_id"`
	Quote  string `json:"quote"`
}

type ModelAnswer struct {
	Answer   string              `json:"answer"`
	Evidence []CandidateEvidence `json:"evidence"`
}

type ModelRequest struct {
	Question                 string
	Conversation             []ConversationTurn
	Effort                   string
	PreviousValidationErrors []string
}

// ModelRunner runs a no-tools model call. The runner receives only the bounded
// projection and must return the complete candidate, never a partial repair.
type ModelRunner interface {
	Run(context.Context, ModelRequest) (ModelAnswer, error)
}

type SessionLookup interface {
	Get(string) *protocol.Session
	GetResumeSessionID(string) string
}

type TranscriptFinder interface {
	FindTranscriptForResume(string) string
}

type Service struct {
	Store                SessionLookup
	Finder               TranscriptFinder
	Model                ModelRunner
	SnapshotMaxBytes     int
	ConversationMaxChars int
}

type Request struct {
	TargetSessionID string
	Question        string
}

func (s Service) Ask(ctx context.Context, req Request) (*protocol.SessionInstructionsResult, error) {
	if strings.TrimSpace(req.TargetSessionID) == "" {
		return nil, &Error{Code: "session_not_found", Message: "The target session was not found"}
	}
	if strings.TrimSpace(req.Question) == "" {
		return nil, &Error{Code: "invalid_response", Message: "A non-blank question is required"}
	}
	if s.Store == nil || s.Finder == nil || s.Model == nil {
		return nil, &Error{Code: "model_unavailable", Message: "Session instructions are unavailable"}
	}
	session := s.Store.Get(req.TargetSessionID)
	if session == nil {
		return nil, &Error{Code: "session_not_found", Message: "The target session was not found"}
	}
	if string(session.Agent) != protocol.SessionAgentCodex {
		return nil, &Error{Code: "transcript_unavailable", Message: "The target transcript is unavailable"}
	}
	resumeID := strings.TrimSpace(s.Store.GetResumeSessionID(req.TargetSessionID))
	if resumeID == "" {
		return nil, &Error{Code: "transcript_unavailable", Message: "The target transcript is unavailable"}
	}
	path := strings.TrimSpace(s.Finder.FindTranscriptForResume(resumeID))
	if path == "" {
		return nil, &Error{Code: "transcript_unavailable", Message: "The target transcript is unavailable"}
	}
	raw, err := readSnapshot(path, s.snapshotLimit())
	if err != nil {
		return nil, &Error{Code: "transcript_unavailable", Message: "The target transcript is unavailable"}
	}
	turns, err := projectCodexConversation(raw)
	if err != nil || len(turns) == 0 {
		return nil, &Error{Code: "transcript_unavailable", Message: "The target transcript is unavailable"}
	}
	if conversationChars(turns) > s.conversationLimit() {
		return nil, &Error{Code: "conversation_too_large", Message: "The target conversation is too large to inspect"}
	}
	fingerprint := "sha256:" + hash(raw)

	first, err := s.Model.Run(ctx, ModelRequest{Question: req.Question, Conversation: turns, Effort: lowEffort})
	if err != nil && !errors.Is(err, ErrInvalidResponse) {
		return nil, &Error{Code: "model_unavailable", Message: "The session-instructions model is unavailable"}
	}
	firstErrs, _ := validateCandidate(first, req.Question, turns)
	if errors.Is(err, ErrInvalidResponse) {
		firstErrs = []string{"the first response was not valid structured output"}
	}
	if len(firstErrs) == 0 {
		return resultFromCandidate(first, req.TargetSessionID, path, fingerprint, lowEffort, turns), nil
	}

	retry, err := s.Model.Run(ctx, ModelRequest{
		Question:                 req.Question,
		Conversation:             turns,
		Effort:                   mediumEffort,
		PreviousValidationErrors: firstErrs,
	})
	if err != nil && !errors.Is(err, ErrInvalidResponse) {
		return nil, &Error{Code: "model_unavailable", Message: "The session-instructions model is unavailable"}
	}
	retryErrs, retryMalformed := validateCandidate(retry, req.Question, turns)
	if errors.Is(err, ErrInvalidResponse) {
		return nil, &Error{Code: "invalid_response", Message: "The model did not return valid structured output"}
	}
	if len(retryErrs) > 0 {
		if retryMalformed {
			return nil, &Error{Code: "invalid_response", Message: "The model did not return valid structured output"}
		}
		return nil, &Error{Code: "invalid_evidence", Message: "The model did not return verifiable evidence"}
	}
	return resultFromCandidate(retry, req.TargetSessionID, path, fingerprint, mediumEffort, turns), nil
}

func (s Service) snapshotLimit() int {
	if s.SnapshotMaxBytes > 0 {
		return s.SnapshotMaxBytes
	}
	return defaultSnapshotMax
}

func (s Service) conversationLimit() int {
	if s.ConversationMaxChars > 0 {
		return s.ConversationMaxChars
	}
	return defaultConversationMax
}

func readSnapshot(path string, max int) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil || info.Size() < 1 || info.Size() > int64(max) {
		return nil, errors.New("snapshot unavailable")
	}
	return os.ReadFile(path)
}

type codexRecord struct {
	Timestamp string `json:"timestamp"`
	Type      string `json:"type"`
	Payload   struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"payload"`
}

func projectCodexConversation(raw []byte) ([]ConversationTurn, error) {
	var turns []ConversationTurn
	lines := bytes.Split(raw, []byte{'\n'})
	valid := false
	for _, line := range lines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var record codexRecord
		if err := json.Unmarshal(line, &record); err != nil {
			continue
		}
		valid = true
		if record.Type != "event_msg" {
			continue
		}
		author := ""
		switch record.Payload.Type {
		case "user_message":
			author = "user"
		case "agent_message":
			author = "assistant"
		default:
			continue
		}
		text := strings.TrimSpace(record.Payload.Message)
		if text == "" {
			continue
		}
		turns = append(turns, ConversationTurn{ID: fmt.Sprintf("turn-%d", len(turns)+1), Author: author, Text: text, Timestamp: record.Timestamp})
	}
	if !valid {
		return nil, errors.New("not JSONL")
	}
	return turns, nil
}

func conversationChars(turns []ConversationTurn) int {
	n := 0
	for _, turn := range turns {
		n += len(turn.Text)
	}
	return n
}

func hash(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }

func validateCandidate(candidate ModelAnswer, question string, turns []ConversationTurn) ([]string, bool) {
	answer := safeAnswer(candidate.Answer)
	malformed := false
	var errs []string
	if answer == "" {
		errs = append(errs, "answer was empty")
		malformed = true
	}
	if len(candidate.Evidence) == 0 {
		errs = append(errs, "evidence was missing")
		malformed = true
	}
	byID := make(map[string]ConversationTurn, len(turns))
	for _, turn := range turns {
		byID[turn.ID] = turn
	}
	seenUser := false
	for _, evidence := range candidate.Evidence {
		turn, ok := byID[evidence.TurnID]
		if !ok {
			errs = append(errs, "an evidence turn id was not found")
			continue
		}
		if !uniqueQuoteMatch(turn.Text, evidence.Quote) {
			errs = append(errs, "an evidence quote was not uniquely found")
			continue
		}
		if turn.Author == "user" {
			seenUser = true
		}
	}
	if requiresUserEvidence(question, answer) && !seenUser {
		errs = append(errs, "a user-instructions question needs user evidence")
	}
	return errs, malformed
}

func requiresUserEvidence(question, answer string) bool {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(answer)), "unclear.") {
		return false
	}
	return !isExplicitSpeechReportingQuestion(question)
}

var explicitSpeechReportingQuestion = regexp.MustCompile(
	`^(?:(?:did|does|do|has|have)\s+(?:the\s+)?(?:agent|assistant|codex)|what\s+(?:did|does|has|have)\s+(?:the\s+)?(?:agent|assistant|codex))\s+(?:say|said|tell|told|write|wrote|state|stated|claim|claimed|report|reported)\b|^(?:is|was|were)\s+(?:the\s+)?(?:agent|assistant|codex)\s+(?:saying|writing|stating|claiming|reporting)\b`,
)

func isExplicitSpeechReportingQuestion(question string) bool {
	q := strings.ToLower(strings.TrimSpace(question))
	// Assistant evidence can establish only what the assistant said. Mentioning
	// an agent is not enough: "Was the agent authorized?" remains a claim about
	// Victor's authorization and needs a user-authored instruction.
	return explicitSpeechReportingQuestion.MatchString(q)
}

func uniqueQuoteMatch(text, hint string) bool {
	hint = strings.TrimSpace(hint)
	if hint == "" {
		return false
	}
	if strings.Count(text, hint) == 1 {
		return true
	}
	normalizedText, normalizedHint := normalize(text), normalize(hint)
	return normalizedHint != "" && strings.Count(normalizedText, normalizedHint) == 1
}

func normalize(value string) string {
	var b strings.Builder
	space := false
	for _, r := range strings.ToLower(value) {
		switch r {
		case '“', '”', '„', '‟':
			r = '"'
		case '‘', '’', '‚', '‛':
			r = '\''
		}
		if unicode.IsSpace(r) {
			space = b.Len() > 0
			continue
		}
		if space {
			b.WriteByte(' ')
			space = false
		}
		b.WriteRune(r)
	}
	return b.String()
}

func resultFromCandidate(candidate ModelAnswer, sessionID, path, fingerprint, effort string, turns []ConversationTurn) *protocol.SessionInstructionsResult {
	byID := make(map[string]ConversationTurn, len(turns))
	for _, turn := range turns {
		byID[turn.ID] = turn
	}
	evidence := make([]protocol.EvidenceExcerpt, 0, len(candidate.Evidence))
	for _, item := range candidate.Evidence {
		turn := byID[item.TurnID]
		quote := exactQuote(turn.Text, item.Quote)
		evidence = append(evidence, protocol.EvidenceExcerpt{TurnID: turn.ID, Author: turn.Author, Quote: quote, Timestamp: optionalTimestamp(turn.Timestamp)})
	}
	sort.SliceStable(evidence, func(i, j int) bool { return turnPosition(evidence[i].TurnID) < turnPosition(evidence[j].TurnID) })
	return &protocol.SessionInstructionsResult{Answer: safeAnswer(candidate.Answer), Evidence: evidence, SessionID: sessionID, TranscriptPath: path, TranscriptFingerprint: fingerprint, Model: ModelName, ReasoningEffort: effort}
}

func turnPosition(id string) int {
	var position int
	_, _ = fmt.Sscanf(id, "turn-%d", &position)
	return position
}

func exactQuote(text, hint string) string {
	if at := strings.Index(text, hint); at >= 0 {
		return text[at : at+len(hint)]
	}
	// Normalized matching has no byte-stable offset. Returning the whole original
	// turn still preserves source bytes and cannot fabricate a quoted fragment.
	return text
}

func optionalTimestamp(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

// Prompt returns the fixed, tool-less instruction given to the configured model.
func Prompt(request ModelRequest) string {
	var b strings.Builder
	b.WriteString("Answer the question only from the labeled conversation. Return JSON with answer and evidence; evidence entries need turn_id and a short exact quote hint. For a yes/no question, answer must begin exactly Yes., No., or Unclear. Do not infer external facts from silence. An assistant turn can provide context, but it cannot independently establish what the user authorized; include the preceding assistant question when it is needed to interpret a terse user reply.\n\nQuestion:\n")
	b.WriteString(request.Question)
	b.WriteString("\n\nConversation:\n")
	for _, turn := range request.Conversation {
		fmt.Fprintf(&b, "[%s %s] %s\n", turn.ID, turn.Author, turn.Text)
	}
	if len(request.PreviousValidationErrors) > 0 {
		b.WriteString("\nThe prior response was rejected. Produce a complete replacement. Validation errors:\n- ")
		b.WriteString(strings.Join(request.PreviousValidationErrors, "\n- "))
	}
	return b.String()
}

// ParseModelAnswer accepts the JSON-only final answer requested by Prompt.
func ParseModelAnswer(text string) (ModelAnswer, error) {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "```") {
		text = strings.TrimPrefix(text, "```json")
		text = strings.TrimPrefix(text, "```")
		text = strings.TrimSuffix(strings.TrimSpace(text), "```")
	}
	var answer ModelAnswer
	if err := json.Unmarshal([]byte(text), &answer); err != nil {
		return ModelAnswer{}, ErrInvalidResponse
	}
	return answer, nil
}

func safeAnswer(value string) string {
	var b strings.Builder
	for _, r := range value {
		if unicode.IsControl(r) {
			if r == '\n' || r == '\t' {
				b.WriteByte(' ')
			}
			continue
		}
		b.WriteRune(r)
	}
	answer := strings.TrimSpace(b.String())
	switch strings.ToLower(answer) {
	case "yes":
		return "Yes."
	case "no":
		return "No."
	case "unclear":
		return "Unclear."
	default:
		return answer
	}
}
