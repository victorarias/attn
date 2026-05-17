package daemon

import (
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

	startedOperation := operation
	d.wsHub.Broadcast(&protocol.WebSocketEvent{
		Event:     protocol.EventGitOperationStarted,
		Operation: &startedOperation,
	})

	return func(err error) {
		finishedAt := time.Now()
		operation.Status = protocol.GitOperationStatusSucceeded
		if err != nil {
			operation.Status = protocol.GitOperationStatusFailed
			operation.Error = protocol.Ptr(err.Error())
		}
		operation.FinishedAt = protocol.Ptr(finishedAt.Format(time.RFC3339))
		operation.DurationMs = protocol.Ptr(int(finishedAt.Sub(startedAt).Milliseconds()))

		finishedOperation := operation
		d.wsHub.Broadcast(&protocol.WebSocketEvent{
			Event:     protocol.EventGitOperationFinished,
			Operation: &finishedOperation,
		})
		d.refreshGitStatusSubscribersForPath(path)
	}
}
