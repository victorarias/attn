package daemon

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/apps"
	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/store"
)

func TestAppPreDrainRecordsOneGapAndFencesFactsUntilCompletion(t *testing.T) {
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
	if request.Reason != store.AppReconcileGap || request.Cursor != 1 || request.Earliest != 4 || request.Missed != 2 || request.ThroughSeq != 6 {
		t.Fatalf("gap request = %+v", request)
	}
	if err := d.store.CompleteAppReconcile("approval-gate", claim.ThroughRequestID, claim.ThroughSeq, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := hook(context.Background(), bus.Consumer{Name: apps.ConsumerName("approval-gate")}, nil); err != nil {
		t.Fatalf("pre-drain after completion: %v", err)
	}
	consumer, ok, err := d.store.GetBusConsumer(apps.ConsumerName("approval-gate"))
	if err != nil || !ok || consumer.Cursor != 6 {
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
