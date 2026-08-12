package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/prosegate"
)

// refusal is what the gate remembers between the message it refused and the
// rewrite that follows: enough to recognise a resubmission, and enough to tell
// the author what the rewrite dropped.
type refusal struct {
	Digest    string              `json:"digest"`
	Structure prosegate.Structure `json:"structure"`
}

// proseGateOutcome keeps the decision separate from the exit, so the one-shot
// rule can be tested without a subprocess.
type proseGateOutcome struct {
	Refused bool
	Stderr  string
}

// gateProse hands dense prose back to its author once, before it lands where a
// reader who cannot ask a clarifying question will find it.
func gateProse(command, sessionID, text string) {
	outcome := evaluateProseGate(command, sessionID, text)
	if outcome.Stderr != "" {
		fmt.Fprint(os.Stderr, outcome.Stderr)
	}
	if outcome.Refused {
		// Its own code: 2 already means "you called this wrong", and a rewrite is
		// a different thing to do about it.
		os.Exit(proseRefusedExitCode)
	}
}

const proseRefusedExitCode = 3

// evaluateProseGate refuses at most once per session, not once per text. The
// moment a refusal is on record the next write goes through, whatever it says.
// An agent whose rewrite still reads densely makes progress instead of trading
// versions with a gate that never runs out of objections.
func evaluateProseGate(command, sessionID, text string) proseGateOutcome {
	if prior, ok := readRefusal(sessionID); ok {
		clearRefusal(sessionID)
		return proseGateOutcome{Stderr: lostStructureWarning(command, prior, text)}
	}

	verdict := prosegate.Check(text, prosegate.Default())
	if !verdict.Tripped {
		return proseGateOutcome{}
	}
	// The one-shot depends on remembering the refusal. If it cannot be written,
	// let the text through rather than refuse the retry too.
	if !recordRefusal(sessionID, text) {
		return proseGateOutcome{}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s: %s\n\n", command, verdict.Nudge)
	fmt.Fprintf(&b, "Rewrite and run the command again — the next version goes through either way.\n")
	fmt.Fprintf(&b, "(%d words; %s)\n", verdict.Words, describeGates(verdict.Gates))
	return proseGateOutcome{Refused: true, Stderr: b.String()}
}

func describeGates(gates []prosegate.GateHit) string {
	parts := make([]string, 0, len(gates))
	for _, g := range gates {
		parts = append(parts, fmt.Sprintf("%s %.2f over %.2f", g.Name, g.Value, g.Threshold))
	}
	return strings.Join(parts, ", ")
}

// lostStructureWarning is the way to see what the nudge cost. Rewriting for
// plainness is exactly when a diagram or a table gets dropped, and only the
// author can tell whether that loss was deliberate — so this reports and never
// blocks.
func lostStructureWarning(command string, prior refusal, text string) string {
	if prior.Digest == textDigest(text) {
		return "" // resubmitted unchanged; nothing was rewritten
	}
	lost := prior.Structure.Lost(prosegate.StructureOf(text))
	if len(lost) == 0 {
		return ""
	}
	return fmt.Sprintf("%s: the rewrite dropped %s. Put back anything that carried "+
		"information the prose does not.\n", command, strings.Join(lost, ", "))
}

// refusalPath is per session, so two agents writing at once never see each
// other's refusal.
func refusalPath(sessionID string) string {
	if sessionID == "" {
		sessionID = "unbound"
	}
	return filepath.Join(config.DataDir(), "prosegate", sessionID)
}

func textDigest(text string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(text)))
	return hex.EncodeToString(sum[:])
}

func readRefusal(sessionID string) (refusal, bool) {
	raw, err := os.ReadFile(refusalPath(sessionID))
	if err != nil {
		return refusal{}, false
	}
	var r refusal
	if err := json.Unmarshal(raw, &r); err != nil {
		return refusal{}, false
	}
	return r, true
}

// recordRefusal reports whether the refusal was durably recorded. False means
// the caller must let the text through, because a refusal it cannot remember
// would repeat on every retry.
func recordRefusal(sessionID, text string) bool {
	raw, err := json.Marshal(refusal{Digest: textDigest(text), Structure: prosegate.StructureOf(text)})
	if err != nil {
		return false
	}
	path := refusalPath(sessionID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false
	}
	return os.WriteFile(path, raw, 0o644) == nil
}

func clearRefusal(sessionID string) {
	os.Remove(refusalPath(sessionID))
}
