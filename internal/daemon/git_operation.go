package daemon

import (
	"github.com/victorarias/attn/internal/bus"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) beginGitOperation(kind protocol.GitOperationKind, path string, endpointID *string) func(error) {
	startedAt := time.Now()
	operation := protocol.GitOperation{
		ID:         uuid.NewString(),
		Kind:       kind,
		Status:     protocol.GitOperationStatusRunning,
		Path:       protocol.Ptr(path),
		EndpointID: endpointID,
		StartedAt:  startedAt.Format(time.RFC3339),
	}

	d.publishFact(FactGitOperationStarted, operation.ID, operation)

	return func(err error) {
		finishedAt := time.Now()
		operation.Status = protocol.GitOperationStatusSucceeded
		if err != nil {
			operation.Status = protocol.GitOperationStatusFailed
			operation.Error = protocol.Ptr(err.Error())
		}
		operation.FinishedAt = protocol.Ptr(finishedAt.Format(time.RFC3339))
		operation.DurationMs = protocol.Ptr(int(finishedAt.Sub(startedAt).Milliseconds()))

		d.publishFact(FactGitOperationFinished, operation.ID, operation)
		d.refreshGitStatusSubscribersForPath(path)
	}
}

// projectGitOperation carries the operation in the payload: the daemon does not
// keep a git-operation registry, so the fact is the only record of it.
func (d *Daemon) projectGitOperation(ev bus.Event) {
	operation, ok := decodeFact[protocol.GitOperation](d, ev)
	if !ok {
		return
	}
	event := protocol.EventGitOperationStarted
	if ev.Name == FactGitOperationFinished {
		event = protocol.EventGitOperationFinished
	}
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event:     event,
		Operation: &operation,
	})
}
