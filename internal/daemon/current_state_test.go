package daemon

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/apps"
	"github.com/victorarias/attn/internal/store"
)

func TestAppCurrentStateSnapshotCapturesTheBusHeadBeforeBuildingInitialState(t *testing.T) {
	d := newDaemonForTest(t)
	seq, err := d.store.AppendBusEvent(store.BusEvent{
		Name: "ticket.updated", Subject: "t-1", Source: "test",
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	dispatch := &appDispatch{
		app: "approval-gate", namespace: apps.Namespace("approval-gate"),
		collections: map[string]struct{}{},
	}
	d.registerAppDispatch(dispatch)

	result, err := d.appRuntimeMethod(jsonRPCMessage{
		Method: "app.current.snapshot",
		Params: mustJSON(t, appCurrentStateParams{Dispatch: dispatch.id}),
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, ok := result.(appCurrentStateSnapshot)
	if !ok {
		t.Fatalf("snapshot type = %T", result)
	}
	if snapshot.AsOfSeq != seq {
		t.Fatalf("asOfSeq = %d, want bus head %d", snapshot.AsOfSeq, seq)
	}
	state := d.currentStateProjection()
	if len(snapshot.Sessions) != len(state.Sessions) || len(snapshot.Crew) != len(state.Crew) || len(snapshot.Apps) != len(state.Apps) {
		t.Fatalf("snapshot did not use the Initial State projection: snapshot=%+v state=%+v", snapshot, state)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	wantFields := []string{
		"asOfSeq", "sessions", "endpoints", "workspaces", "prs", "repos",
		"authors", "githubHosts", "tickets", "seeds", "crew", "apps",
	}
	if len(fields) != len(wantFields) {
		t.Fatalf("snapshot fields = %s", encoded)
	}
	for _, name := range wantFields {
		value, ok := fields[name]
		if !ok {
			t.Errorf("snapshot has no %s: %s", name, encoded)
		} else if name != "asOfSeq" && string(value) == "null" {
			t.Errorf("snapshot %s is null, want an array: %s", name, encoded)
		}
	}

	d.releaseAppDispatch(dispatch.id)
	_, err = d.appRuntimeMethod(jsonRPCMessage{
		Method: "app.current.snapshot",
		Params: mustJSON(t, appCurrentStateParams{Dispatch: dispatch.id}),
	})
	if err == nil || !strings.Contains(err.Error(), "not in flight") {
		t.Fatalf("snapshot after handler returned: %v", err)
	}
}
