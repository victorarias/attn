package daemon

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

const (
	reviewLoopScriptedEnvB64  = "ATTN_REVIEW_LOOP_SCRIPT_B64"
	reviewLoopScriptedEnvJSON = "ATTN_REVIEW_LOOP_SCRIPT_JSON"
)

type scriptedReviewLoopConfig struct {
	Scenarios []scriptedReviewLoopScenario `json:"scenarios"`
}

type scriptedReviewLoopScenario struct {
	Name                string                        `json:"name"`
	MatchPromptContains string                        `json:"match_prompt_contains"`
	Iterations          []scriptedReviewLoopIteration `json:"iterations"`
}

type scriptedReviewLoopIteration struct {
	DelayMs        int               `json:"delay_ms"`
	Outcome        reviewLoopOutcome `json:"outcome"`
	AssistantTrace string            `json:"assistant_trace"`
	StructuredJSON string            `json:"structured_json"`
	ResultText     string            `json:"result_text"`
	ExpectAnswer   string            `json:"expect_answer"`
}

type scriptedReviewLoopState struct {
	scenario scriptedReviewLoopScenario
	step     int
}

func scriptedReviewLoopConfigFromEnv() (*scriptedReviewLoopConfig, error) {
	if raw := strings.TrimSpace(os.Getenv(reviewLoopScriptedEnvB64)); raw != "" {
		decoded, err := base64.StdEncoding.DecodeString(raw)
		if err != nil {
			return nil, fmt.Errorf("decode %s: %w", reviewLoopScriptedEnvB64, err)
		}
		return parseScriptedReviewLoopConfig(decoded)
	}
	if raw := strings.TrimSpace(os.Getenv(reviewLoopScriptedEnvJSON)); raw != "" {
		return parseScriptedReviewLoopConfig([]byte(raw))
	}
	return nil, nil
}

func parseScriptedReviewLoopConfig(raw []byte) (*scriptedReviewLoopConfig, error) {
	var cfg scriptedReviewLoopConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, err
	}
	if len(cfg.Scenarios) == 0 {
		return nil, fmt.Errorf("scripted review loop config requires at least one scenario")
	}
	for index, scenario := range cfg.Scenarios {
		if strings.TrimSpace(scenario.MatchPromptContains) == "" {
			return nil, fmt.Errorf("scenario %d missing match_prompt_contains", index)
		}
		if len(scenario.Iterations) == 0 {
			return nil, fmt.Errorf("scenario %d has no iterations", index)
		}
		for stepIndex, step := range scenario.Iterations {
			if strings.TrimSpace(string(step.Outcome.LoopDecision)) == "" {
				return nil, fmt.Errorf("scenario %d iteration %d missing outcome.loop_decision", index, stepIndex)
			}
		}
	}
	return &cfg, nil
}

func newScriptedReviewLoopExecutor(cfg *scriptedReviewLoopConfig, logf func(format string, args ...interface{})) ReviewLoopExecutor {
	if cfg == nil {
		return nil
	}
	if logf == nil {
		logf = func(string, ...interface{}) {}
	}

	states := make(map[string]*scriptedReviewLoopState)
	var mu sync.Mutex

	return func(ctx context.Context, run *protocol.ReviewLoopRun, prompt string) (*reviewLoopOutcome, string, string, string, error) {
		mu.Lock()
		state, ok := states[run.LoopID]
		if !ok {
			scenario, err := matchScriptedReviewLoopScenario(cfg, prompt)
			if err != nil {
				mu.Unlock()
				return nil, "", "", "", err
			}
			state = &scriptedReviewLoopState{scenario: scenario}
			states[run.LoopID] = state
			logf("[review-loop-scripted] loop=%s scenario=%s", run.LoopID, scenario.Name)
		}
		if state.step >= len(state.scenario.Iterations) {
			mu.Unlock()
			return nil, "", "", "", fmt.Errorf(
				"scripted review loop %q exhausted at iteration %d",
				state.scenario.Name,
				state.step+1,
			)
		}
		step := state.scenario.Iterations[state.step]
		state.step++
		if state.step >= len(state.scenario.Iterations) {
			delete(states, run.LoopID)
		}
		mu.Unlock()

		if delay := time.Duration(step.DelayMs) * time.Millisecond; delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, "", "", "", ctx.Err()
			case <-timer.C:
			}
		}

		if expected := strings.TrimSpace(step.ExpectAnswer); expected != "" {
			if !strings.Contains(prompt, expected) {
				return nil, "", "", "", fmt.Errorf(
					"scripted review loop expected prompt to contain answer %q; prompt=%q",
					expected,
					truncateReviewLoopLog(prompt, 320),
				)
			}
		}

		outcome := step.Outcome
		return &outcome, step.AssistantTrace, step.StructuredJSON, step.ResultText, nil
	}
}

func matchScriptedReviewLoopScenario(cfg *scriptedReviewLoopConfig, prompt string) (scriptedReviewLoopScenario, error) {
	for _, scenario := range cfg.Scenarios {
		if strings.Contains(prompt, scenario.MatchPromptContains) {
			return scenario, nil
		}
	}
	return scriptedReviewLoopScenario{}, fmt.Errorf(
		"no scripted review loop scenario matched prompt %q",
		truncateReviewLoopLog(prompt, 200),
	)
}
