package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessioninstructions"
)

const sessionInstructionsTimeout = 45 * time.Second

func (d *Daemon) handleSessionInstructions(conn net.Conn, msg *protocol.SessionInstructionsMessage) {
	service := sessioninstructions.Service{
		Store:  d.store,
		Finder: agentdriver.Get("codex").(agentdriver.TranscriptFinder),
		Model:  daemonSessionInstructionsModel{daemon: d},
	}
	ctx, cancel := context.WithTimeout(context.Background(), sessionInstructionsTimeout)
	defer cancel()
	result, err := service.Ask(ctx, sessioninstructions.Request{TargetSessionID: msg.TargetSessionID, Question: msg.Question})
	if err != nil {
		var sessionErr *sessioninstructions.Error
		if errors.As(err, &sessionErr) {
			d.sendError(conn, sessionErr.Code)
			return
		}
		d.sendError(conn, "model_unavailable")
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, SessionInstructionsResult: result})
}

type daemonSessionInstructionsModel struct{ daemon *Daemon }

func (m daemonSessionInstructionsModel) Run(ctx context.Context, request sessioninstructions.ModelRequest) (sessioninstructions.ModelAnswer, error) {
	driver := agentdriver.Get("codex")
	provider, ok := driver.(agentdriver.HeadlessTaskProvider)
	if !ok {
		m.daemon.logf("session instructions model failed: Codex headless provider unavailable")
		return sessioninstructions.ModelAnswer{}, errors.New("codex headless provider unavailable")
	}
	executable, err := exec.LookPath(driver.ResolveExecutable(m.daemon.store.GetSetting(canonicalExecutableSettingKey("codex"))))
	if err != nil {
		m.daemon.logf("session instructions model failed: Codex executable unavailable")
		return sessioninstructions.ModelAnswer{}, fmt.Errorf("resolve luna executable: %w", err)
	}
	workDir, err := os.MkdirTemp("", "attn-session-instructions-*")
	if err != nil {
		m.daemon.logf("session instructions model failed: scratch directory unavailable")
		return sessioninstructions.ModelAnswer{}, err
	}
	defer os.RemoveAll(workDir)
	result, err := provider.RunHeadlessTask(ctx, agentdriver.HeadlessTaskRequest{
		Executable:      executable,
		Model:           sessioninstructions.ModelName,
		ReasoningEffort: request.Effort,
		Prompt:          sessioninstructions.Prompt(request),
		WorkDir:         workDir,
		DisableTools:    true,
	})
	if err != nil {
		diagnostic := strings.TrimSpace(result.Diagnostics)
		if diagnostic == "" {
			diagnostic = "unknown headless failure"
		}
		m.daemon.logf("session instructions model failed: %s", diagnostic)
		return sessioninstructions.ModelAnswer{}, err
	}
	return sessioninstructions.ParseModelAnswer(result.Text)
}

// SessionInstructionsErrorMessage is the stable, transcript-free CLI surface.
func SessionInstructionsErrorMessage(code string) string {
	switch strings.TrimSpace(code) {
	case "session_not_found":
		return "The target session was not found"
	case "transcript_unavailable":
		return "The target transcript is unavailable"
	case "conversation_too_large":
		return "The target conversation is too large to inspect"
	case "model_unavailable":
		return "The session-instructions model is unavailable"
	case "invalid_response":
		return "The model did not return valid structured output"
	case "invalid_evidence":
		return "The model did not return verifiable evidence"
	default:
		return "Session instructions failed"
	}
}
