package daemon

import (
	"context"
	"encoding/base64"
	"os"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestParseScriptedReviewLoopConfig(t *testing.T) {
	cfg, err := parseScriptedReviewLoopConfig([]byte(`{
  "scenarios": [
    {
      "name": "await-user",
      "match_prompt_contains": "QUESTION_SCENARIO",
      "iterations": [
        {
          "outcome": {
            "loop_decision": "needs_user_input",
            "summary": "Need input",
            "changes_made": false,
            "files_touched": ["tracked.txt"],
            "questions_for_user": ["Ship it?"],
            "blocking_reason": "",
            "suggested_next_focus": "Await answer"
          }
        }
      ]
    }
  ]
}`))
	if err != nil {
		t.Fatalf("parseScriptedReviewLoopConfig() error = %v", err)
	}
	if got := len(cfg.Scenarios); got != 1 {
		t.Fatalf("len(cfg.Scenarios) = %d, want 1", got)
	}
	if got := cfg.Scenarios[0].MatchPromptContains; got != "QUESTION_SCENARIO" {
		t.Fatalf("MatchPromptContains = %q, want %q", got, "QUESTION_SCENARIO")
	}
}

func TestScriptedReviewLoopConfigFromEnvB64(t *testing.T) {
	t.Setenv(reviewLoopScriptedEnvJSON, "")
	payload := base64.StdEncoding.EncodeToString([]byte(`{
  "scenarios": [
    {
      "name": "smoke",
      "match_prompt_contains": "SMOKE",
      "iterations": [
        {
          "outcome": {
            "loop_decision": "converged",
            "summary": "Done",
            "changes_made": true,
            "files_touched": [],
            "questions_for_user": [],
            "blocking_reason": "",
            "suggested_next_focus": ""
          }
        }
      ]
    }
  ]
}`))
	t.Setenv(reviewLoopScriptedEnvB64, payload)

	cfg, err := scriptedReviewLoopConfigFromEnv()
	if err != nil {
		t.Fatalf("scriptedReviewLoopConfigFromEnv() error = %v", err)
	}
	if cfg == nil || len(cfg.Scenarios) != 1 {
		t.Fatalf("scriptedReviewLoopConfigFromEnv() = %#v, want one scenario", cfg)
	}
}

func TestScriptedReviewLoopExecutorMatchesAnswer(t *testing.T) {
	cfg := &scriptedReviewLoopConfig{
		Scenarios: []scriptedReviewLoopScenario{
			{
				Name:                "question",
				MatchPromptContains: "QUESTION_SCENARIO",
				Iterations: []scriptedReviewLoopIteration{
					{
						Outcome: reviewLoopOutcome{
							LoopDecision:       protocol.ReviewLoopDecisionNeedsUserInput,
							Summary:            "Need input",
							QuestionsForUser:   []string{"Ship it?"},
							SuggestedNextFocus: "Wait",
						},
					},
					{
						ExpectAnswer: "Answer: yes",
						Outcome: reviewLoopOutcome{
							LoopDecision:       protocol.ReviewLoopDecisionConverged,
							Summary:            "Completed after answer",
							ChangesMade:        true,
							FilesTouched:       []string{"tracked.txt"},
							SuggestedNextFocus: "Done",
						},
					},
				},
			},
		},
	}
	exec := newScriptedReviewLoopExecutor(cfg, nil)
	run := &protocol.ReviewLoopRun{LoopID: "loop-1"}

	first, _, _, _, err := exec(context.Background(), run, "QUESTION_SCENARIO initial prompt")
	if err != nil {
		t.Fatalf("first exec() error = %v", err)
	}
	if first == nil || first.LoopDecision != protocol.ReviewLoopDecisionNeedsUserInput {
		t.Fatalf("first decision = %#v, want needs_user_input", first)
	}

	second, _, _, _, err := exec(context.Background(), run, "QUESTION_SCENARIO follow-up\nUser clarification:\nQuestion: Ship it?\nAnswer: yes")
	if err != nil {
		t.Fatalf("second exec() error = %v", err)
	}
	if second == nil || second.LoopDecision != protocol.ReviewLoopDecisionConverged {
		t.Fatalf("second decision = %#v, want converged", second)
	}
}

func TestScriptedReviewLoopConfigFromEnvUnset(t *testing.T) {
	os.Unsetenv(reviewLoopScriptedEnvB64)
	os.Unsetenv(reviewLoopScriptedEnvJSON)
	cfg, err := scriptedReviewLoopConfigFromEnv()
	if err != nil {
		t.Fatalf("scriptedReviewLoopConfigFromEnv() error = %v", err)
	}
	if cfg != nil {
		t.Fatalf("scriptedReviewLoopConfigFromEnv() = %#v, want nil", cfg)
	}
}
