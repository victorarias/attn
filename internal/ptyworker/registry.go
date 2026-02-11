package ptyworker

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type RegistryEntry struct {
	Version          int    `json:"version"`
	DaemonInstanceID string `json:"daemon_instance_id"`
	SessionID        string `json:"session_id"`
	WorkerPID        int    `json:"worker_pid"`
	ChildPID         int    `json:"child_pid"`
	SocketPath       string `json:"socket_path"`
	Agent            string `json:"agent"`
	CWD              string `json:"cwd"`
	StartedAt        string `json:"started_at"`
	ControlToken     string `json:"control_token"`
	OwnerPID         int    `json:"owner_pid,omitempty"`
	OwnerStartedAt   string `json:"owner_started_at,omitempty"`
	OwnerNonce       string `json:"owner_nonce,omitempty"`
}

func WriteRegistryAtomic(path string, entry RegistryEntry) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create registry dir: %w", err)
	}
	payload, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal registry entry: %w", err)
	}
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, append(payload, '\n'), 0600); err != nil {
		return fmt.Errorf("write temp registry: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename registry file: %w", err)
	}
	return nil
}

func ReadRegistry(path string) (RegistryEntry, error) {
	var entry RegistryEntry
	data, err := os.ReadFile(path)
	if err != nil {
		return entry, err
	}
	if err := json.Unmarshal(data, &entry); err != nil {
		return entry, fmt.Errorf("unmarshal registry: %w", err)
	}
	return entry, nil
}

func NewRegistryEntry(daemonInstanceID, sessionID string, workerPID, childPID int, socketPath, agent, cwd, controlToken string) RegistryEntry {
	return RegistryEntry{
		Version:          1,
		DaemonInstanceID: daemonInstanceID,
		SessionID:        sessionID,
		WorkerPID:        workerPID,
		ChildPID:         childPID,
		SocketPath:       socketPath,
		Agent:            agent,
		CWD:              cwd,
		StartedAt:        time.Now().UTC().Format(time.RFC3339),
		ControlToken:     controlToken,
	}
}
