package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/apps"
	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/store"
)

func TestFoldAppReconcileReasonSortsCausesAndDeduplicatesPreviousVersions(t *testing.T) {
	claim := store.AppReconcileClaim{
		ThroughSeq: 18,
		Requests: []store.AppReconcileRequest{
			{Reason: store.AppReconcileVersionChange, PreviousVersionID: 3},
			{Reason: store.AppReconcileGap, Cursor: 4, Earliest: 8, Missed: 3},
			{Reason: store.AppReconcileVersionChange, PreviousVersionID: 3},
			{Reason: store.AppReconcileVersionChange, PreviousVersionID: 7},
		},
	}
	got := foldAppReconcileReason(11, claim)
	if !reflect.DeepEqual(got.Causes, []string{"gap", "version_changed"}) {
		t.Fatalf("causes = %v", got.Causes)
	}
	if got.Version != 11 || got.ThroughSeq != 18 {
		t.Fatalf("reason = %+v", got)
	}
	if got.Gap == nil || *got.Gap != (appReconcileGap{Cursor: 4, Earliest: 8, Missed: 3}) {
		t.Fatalf("gap = %+v", got.Gap)
	}
	if !reflect.DeepEqual(got.PreviousVersions, []int64{3, 7}) {
		t.Fatalf("previous versions = %v", got.PreviousVersions)
	}
}

func TestAppPreDrainDispatchesReconcileAndCompletesItsClaim(t *testing.T) {
	d := newDaemonForTest(t)
	now := time.Now().UTC()
	version, _, err := d.store.CommitAppVersion(store.AppVersion{
		AppName: "approval-gate", ContentHash: "sha256:approval-gate",
		Declaration:  `{"name":"approval-gate","reconcile":true,"subscribe":[{"events":["ticket.*"]}],"collections":[{"name":"decisions"}]}`,
		ArtifactPath: "bundle.js",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	seedAppConsumer(t, d, "approval-gate", true, 1)
	for i := 0; i < 6; i++ {
		if _, err := d.store.AppendBusEvent(store.BusEvent{Name: "ticket.updated", Subject: "t-1"}, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := d.store.RequestAppReconcileGap("approval-gate", 1, 4, now); err != nil {
		t.Fatal(err)
	}
	runtime := startFakeAppRuntime(t, d, nil)
	snapshots := make(chan appCurrentStateSnapshot, 1)
	runtime.reconcile = func(f *fakeAppRuntime, req appReconcileRequest) error {
		data, err := f.call("app.current.snapshot", appCurrentStateParams{Dispatch: req.Dispatch})
		if err != nil {
			return err
		}
		var snapshot appCurrentStateSnapshot
		if err := json.Unmarshal(data, &snapshot); err != nil {
			return err
		}
		snapshots <- snapshot
		return nil
	}

	hook := d.appPreDrain("approval-gate")
	if err := hook(context.Background(), bus.Consumer{Name: apps.ConsumerName("approval-gate")}, nil); err != nil {
		t.Fatalf("pre-drain reconcile: %v", err)
	}
	log := runtime.reconcileLog()
	if len(log) != 1 {
		t.Fatalf("reconciles = %+v", log)
	}
	request := log[0]
	if request.App != "approval-gate" || request.VersionID != version.ID || request.Artifact != "bundle.js" {
		t.Fatalf("request = %+v", request)
	}
	if !reflect.DeepEqual(request.Collections, []string{"decisions"}) || !reflect.DeepEqual(request.Reason.Causes, []string{"gap"}) {
		t.Fatalf("request = %+v", request)
	}
	if request.Reason.Gap == nil || request.Reason.ThroughSeq != 3 {
		t.Fatalf("reason = %+v", request.Reason)
	}
	snapshot := <-snapshots
	if snapshot.AsOfSeq < request.Reason.ThroughSeq || snapshot.Apps == nil || snapshot.Crew == nil {
		t.Fatalf("current snapshot = %+v", snapshot)
	}
	if claim, err := d.store.AppReconcilePending("approval-gate"); err != nil || len(claim.Requests) != 0 {
		t.Fatalf("pending after success = %+v, %v", claim, err)
	}
	consumer, _, err := d.store.GetBusConsumer(apps.ConsumerName("approval-gate"))
	if err != nil || consumer.Cursor != 3 {
		t.Fatalf("consumer = %+v, %v", consumer, err)
	}
}

func TestAppPreDrainRetriesTheSameClaimAfterAThrow(t *testing.T) {
	d := newDaemonForTest(t)
	now := time.Now().UTC()
	_, _, err := d.store.CommitAppVersion(store.AppVersion{
		AppName: "approval-gate", ContentHash: "sha256:approval-gate",
		Declaration:  `{"name":"approval-gate","reconcile":true,"subscribe":[{"events":["ticket.*"]}]}`,
		ArtifactPath: "bundle.js",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	seedAppConsumer(t, d, "approval-gate", true, 1)
	if _, err := d.store.RequestAppReconcileGap("approval-gate", 1, 4, now); err != nil {
		t.Fatal(err)
	}
	runtime := startFakeAppRuntime(t, d, nil)
	var attempts atomic.Int32
	runtime.reconcile = func(_ *fakeAppRuntime, _ appReconcileRequest) error {
		if attempts.Add(1) == 1 {
			return errors.New("rebuild failed")
		}
		return nil
	}

	hook := d.appPreDrain("approval-gate")
	consumer := bus.Consumer{Name: apps.ConsumerName("approval-gate")}
	if err := hook(context.Background(), consumer, nil); err == nil || !strings.Contains(err.Error(), "rebuild failed") {
		t.Fatalf("first reconcile = %v", err)
	}
	claim, err := d.store.AppReconcilePending("approval-gate")
	if err != nil || len(claim.Requests) != 1 {
		t.Fatalf("claim after throw = %+v, %v", claim, err)
	}
	stored, _, err := d.store.GetBusConsumer(consumer.Name)
	if err != nil || stored.Cursor != 1 {
		t.Fatalf("cursor after throw = %d, %v", stored.Cursor, err)
	}

	if err := hook(context.Background(), consumer, nil); err != nil {
		t.Fatalf("retry: %v", err)
	}
	log := runtime.reconcileLog()
	if len(log) != 2 || !reflect.DeepEqual(log[0].Reason, log[1].Reason) {
		t.Fatalf("retry reasons = %+v", log)
	}
	claim, err = d.store.AppReconcilePending("approval-gate")
	if err != nil || len(claim.Requests) != 0 {
		t.Fatalf("claim after retry = %+v, %v", claim, err)
	}
	stored, _, err = d.store.GetBusConsumer(consumer.Name)
	if err != nil || stored.Cursor != 3 {
		t.Fatalf("cursor after retry = %d, %v", stored.Cursor, err)
	}
}

func TestAppPreDrainFencesAGapBeforeTheEarliestRetainedFact(t *testing.T) {
	d := newDaemonForTest(t)
	seedApp(t, d, "approval-gate", true)
	gap := &bus.Gap{Cursor: 1, Earliest: 4, Head: 6, Missed: 2}
	hook := d.appPreDrain("approval-gate")
	for attempt := 0; attempt < 2; attempt++ {
		err := hook(context.Background(), bus.Consumer{Name: apps.ConsumerName("approval-gate")}, gap)
		if err == nil || !strings.Contains(err.Error(), "reconciliation is owed") {
			t.Fatalf("attempt %d error = %v", attempt+1, err)
		}
	}
	claim, err := d.store.AppReconcilePending("approval-gate")
	if err != nil || len(claim.Requests) != 1 {
		t.Fatalf("pending = %+v, %v", claim, err)
	}
	request := claim.Requests[0]
	if request.Reason != store.AppReconcileGap || request.Cursor != 1 || request.Earliest != 4 || request.Missed != 2 || request.ThroughSeq != 3 {
		t.Fatalf("gap request = %+v", request)
	}
	if err := d.store.CompleteAppReconcile("approval-gate", claim.ThroughRequestID, claim.ThroughSeq, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := hook(context.Background(), bus.Consumer{Name: apps.ConsumerName("approval-gate")}, nil); err != nil {
		t.Fatalf("pre-drain after completion: %v", err)
	}
	consumer, ok, err := d.store.GetBusConsumer(apps.ConsumerName("approval-gate"))
	if err != nil || !ok || consumer.Cursor != 3 {
		t.Fatalf("consumer after completion = %+v, found=%t, err=%v", consumer, ok, err)
	}
}

func TestAppPreDrainKeepsNoSubscriptionAppsOnGapParity(t *testing.T) {
	d := newDaemonForTest(t)
	now := time.Now()
	if _, _, err := d.store.CommitAppVersion(store.AppVersion{
		AppName: "clock", ContentHash: "sha256:clock-1",
		Declaration: `{"name":"clock"}`, ArtifactPath: "bundle.js",
	}, now); err != nil {
		t.Fatal(err)
	}
	seedAppConsumer(t, d, "clock", true, 1)
	if _, _, err := d.store.CommitAppVersion(store.AppVersion{
		AppName: "clock", ContentHash: "sha256:clock-2",
		Declaration: `{"name":"clock"}`, ArtifactPath: "bundle-2.js",
	}, now); err != nil {
		t.Fatal(err)
	}
	hook := d.appPreDrain("clock")
	if err := hook(context.Background(), bus.Consumer{Name: apps.ConsumerName("clock")},
		&bus.Gap{Cursor: 1, Earliest: 4, Head: 6, Missed: 2}); err != nil {
		t.Fatal(err)
	}
	if claim, err := d.store.AppReconcilePending("clock"); err != nil || len(claim.Requests) != 0 {
		t.Fatalf("no-subscription app left reconciliation owed: %+v, %v", claim, err)
	}
	consumer, _, err := d.store.GetBusConsumer(apps.ConsumerName("clock"))
	if err != nil || consumer.Cursor != 6 {
		t.Fatalf("consumer = %+v, %v; want ordinary gap resume at 6", consumer, err)
	}
}
