package daemon

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/modelcapture"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
)

const (
	defaultModelCaptureIntervalSeconds = 10
	modelCaptureIntervalMinSeconds     = 5
	modelCaptureIntervalMaxSeconds     = 300
	defaultModelCaptureMaxGB           = 5
	modelCaptureMaxMinGB               = 1
	modelCaptureMaxMaxGB               = 100
	modelCapturePollInterval           = time.Second
	modelCaptureSnapshotTimeout        = 3 * time.Second
)

func (d *Daemon) modelCaptureDir() string {
	return filepath.Join(d.dataRoot, "model-captures")
}

func (d *Daemon) modelCaptureEnabled() bool {
	return d.store != nil && parseBooleanSetting(d.store.GetSetting(SettingModelCaptureEnabled))
}

func (d *Daemon) modelCaptureInterval() time.Duration {
	if d.store == nil {
		return defaultModelCaptureIntervalSeconds * time.Second
	}
	seconds := resolveBoundedIntSetting(
		d.store.GetSetting(SettingModelCaptureIntervalSeconds),
		defaultModelCaptureIntervalSeconds,
		modelCaptureIntervalMinSeconds,
		modelCaptureIntervalMaxSeconds,
	)
	return time.Duration(seconds) * time.Second
}

func (d *Daemon) modelCaptureMaxBytes() int64 {
	if d.store == nil {
		return int64(defaultModelCaptureMaxGB) << 30
	}
	gb := resolveBoundedIntSetting(
		d.store.GetSetting(SettingModelCaptureMaxGB),
		defaultModelCaptureMaxGB,
		modelCaptureMaxMinGB,
		modelCaptureMaxMaxGB,
	)
	return int64(gb) << 30
}

func (d *Daemon) runModelCaptureLoop() {
	recorder := modelcapture.New(d.modelCaptureDir())
	d.modelCapturePass(recorder, time.Now())
	ticker := time.NewTicker(modelCapturePollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-d.done:
			return
		case now := <-ticker.C:
			d.modelCapturePass(recorder, now)
		}
	}
}

func (d *Daemon) modelCapturePass(recorder *modelcapture.Recorder, now time.Time) {
	if !d.modelCaptureEnabled() || d.store == nil || d.ptyBackend == nil {
		return
	}
	provider, ok := d.ptyBackend.(ptybackend.SnapshotProvider)
	if !ok {
		return
	}
	interval := d.modelCaptureInterval()
	maxBytes := d.modelCaptureMaxBytes()
	for _, sessionID := range d.ptyBackend.SessionIDs(context.Background()) {
		session := d.store.Get(sessionID)
		if session == nil || !isModelCaptureAgent(session.Agent) {
			continue
		}
		state := string(session.State)
		reason, due := recorder.Due(sessionID, state, now, interval)
		if !due {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), modelCaptureSnapshotTimeout)
		snapshot, err := provider.Snapshot(ctx, sessionID)
		cancel()
		if err != nil {
			d.logf("model capture snapshot failed: session=%s err=%v", sessionID, err)
			continue
		}
		if snapshot.Screen == nil || !snapshot.Screen.HasText {
			continue
		}
		stateReason := ""
		if resolverOwnedStates[session.State] {
			stateReason = d.stateReasons().get(sessionID)
		}
		_, err = recorder.Record(modelcapture.Observation{
			CapturedAt:    now,
			CaptureReason: reason,
			SessionID:     sessionID,
			Agent:         session.Agent,
			DaemonState:   state,
			StateReason:   stateReason,
			Running:       snapshot.Running,
			Cols:          snapshot.Screen.Cols,
			Rows:          snapshot.Screen.Rows,
			LastSeq:       snapshot.LastSeq,
			ViewportText:  snapshot.Screen.Text,
		}, maxBytes)
		if err != nil {
			d.logf("model capture write failed: session=%s err=%v", sessionID, err)
		}
	}
}

func isModelCaptureAgent(agent string) bool {
	switch strings.TrimSpace(strings.ToLower(agent)) {
	case string(protocol.SessionAgentClaude), string(protocol.SessionAgentCodex):
		return true
	default:
		return false
	}
}

func validateModelCaptureInterval(value string) error {
	return validateBoundedIntSetting(
		"model capture interval",
		value,
		modelCaptureIntervalMinSeconds,
		modelCaptureIntervalMaxSeconds,
	)
}

func validateModelCaptureMaxGB(value string) error {
	return validateBoundedIntSetting(
		"model capture storage cap",
		value,
		modelCaptureMaxMinGB,
		modelCaptureMaxMaxGB,
	)
}

func validateBoundedIntSetting(label, value string, min, max int) error {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return fmt.Errorf("%s must be a whole number", label)
	}
	if n < min || n > max {
		return fmt.Errorf("%s must be between %d and %d", label, min, max)
	}
	return nil
}

func resolveBoundedIntSetting(value string, fallback, min, max int) int {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || n < min || n > max {
		return fallback
	}
	return n
}
