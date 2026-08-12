package hub

import (
	"context"
	"errors"
	"testing"
	"time"
)

// EnsureRemoteReady is two phases, an order, and two budgets. These pin all
// three: shipping the ~93.7MB sidecar used to run first and out of the same
// budget as everything else, so a remote whose daemon was down and whose link
// was slow spent its whole budget on the upload and never reached the step that
// would have revived it.

func TestEnsureRemoteReadyRevivesTheDaemonBeforeShippingTheSidecar(t *testing.T) {
	b := NewBootstrapper(nil)
	var order []string

	b.makeReady = func(ctx context.Context, sshTarget, profile, homeDaemonID string) (readyRemote, error) {
		order = append(order, "ready")
		return readyRemote{remoteInstallPath: "/home/x/.local/bin/attn"}, nil
	}
	b.shipAppRuntime = func(ctx context.Context, sshTarget, profile string, ready readyRemote) error {
		order = append(order, "ship")
		if ready.remoteInstallPath != "/home/x/.local/bin/attn" {
			t.Errorf("ship phase got %q, want what the ready phase resolved", ready.remoteInstallPath)
		}
		return nil
	}

	if err := b.EnsureRemoteReady(context.Background(), "host", "", "home-1"); err != nil {
		t.Fatalf("EnsureRemoteReady() = %v, want nil", err)
	}
	if len(order) != 2 || order[0] != "ready" || order[1] != "ship" {
		t.Fatalf("phases ran %v, want [ready ship]", order)
	}
}

func TestEnsureRemoteReadySkipsTheSidecarWhenTheRemoteNeverBecameReady(t *testing.T) {
	b := NewBootstrapper(nil)
	shipped := false

	b.makeReady = func(ctx context.Context, sshTarget, profile, homeDaemonID string) (readyRemote, error) {
		return readyRemote{}, errors.New("daemon did not become ready")
	}
	b.shipAppRuntime = func(ctx context.Context, sshTarget, profile string, ready readyRemote) error {
		shipped = true
		return nil
	}

	if err := b.EnsureRemoteReady(context.Background(), "host", "", "home-1"); err == nil {
		t.Fatal("EnsureRemoteReady() = nil, want the readiness failure")
	}
	if shipped {
		t.Fatal("shipped the sidecar to a remote whose daemon is not running")
	}
}

// The sidecar's budget must be its own. Deriving it from the ready phase's
// context — or sharing one deadline across both — is the shape of the bug: the
// upload inherits whatever the daemon start left over, which on a slow link is
// nothing.
func TestEnsureRemoteReadyGivesTheSidecarItsOwnBudget(t *testing.T) {
	b := NewBootstrapper(nil)
	var readyDeadline, shipDeadline time.Time
	var shipCtxErr error

	b.makeReady = func(ctx context.Context, sshTarget, profile, homeDaemonID string) (readyRemote, error) {
		readyDeadline, _ = ctx.Deadline()
		return readyRemote{}, nil
	}
	b.shipAppRuntime = func(ctx context.Context, sshTarget, profile string, ready readyRemote) error {
		shipDeadline, _ = ctx.Deadline()
		shipCtxErr = ctx.Err()
		return nil
	}

	if err := b.EnsureRemoteReady(context.Background(), "host", "", "home-1"); err != nil {
		t.Fatalf("EnsureRemoteReady() = %v, want nil", err)
	}

	if readyDeadline.IsZero() || shipDeadline.IsZero() {
		t.Fatal("both phases must run under a deadline")
	}
	// Cancelling the ready phase must not reach the ship phase: if it does, the
	// two share a lineage and the sidecar is spending the daemon's budget.
	if shipCtxErr != nil {
		t.Fatalf("ship phase context was already %v; it is not independent of the ready phase", shipCtxErr)
	}
	if !shipDeadline.After(readyDeadline) {
		t.Fatalf("ship deadline %v is not later than ready deadline %v", shipDeadline, readyDeadline)
	}
	if remaining := time.Until(shipDeadline); remaining <= remoteReadyBudget {
		t.Fatalf("ship phase had %v, which is no more than the whole ready budget %v — it inherited rather than got its own", remaining, remoteReadyBudget)
	}
}

// A remote that cannot be given a sidecar still runs sessions, so the sync
// succeeds and the failure is reported instead of returned.
func TestEnsureRemoteReadySurvivesASidecarThatCannotBeShipped(t *testing.T) {
	var logged []string
	b := NewBootstrapper(func(format string, args ...interface{}) {
		logged = append(logged, format)
	})

	b.makeReady = func(ctx context.Context, sshTarget, profile, homeDaemonID string) (readyRemote, error) {
		return readyRemote{}, nil
	}
	b.shipAppRuntime = func(ctx context.Context, sshTarget, profile string, ready readyRemote) error {
		return errors.New("the app runtime host is missing")
	}

	if err := b.EnsureRemoteReady(context.Background(), "host", "", "home-1"); err != nil {
		t.Fatalf("EnsureRemoteReady() = %v, want nil — the daemon is up and sessions work", err)
	}
	if len(logged) == 0 {
		t.Fatal("a sidecar that could not be shipped must still be reported")
	}
}

// The budgets are tripwires, not fits: a healthy sync must never be near them.
// The receipts they are sized from live beside their declaration.
//
// What this fails on is a budget that stops covering the sizes recorded *here* —
// it does not measure the artifacts, so it cannot notice one growing on its own.
// That is deliberate and it is where the tripwire lands: a sidecar that outgrows
// its budget has to update these constants to make this test pass, and updating
// them is the moment someone reads the arithmetic below.
func TestRemoteBudgetsCoverTheArtifactsTheyCarry(t *testing.T) {
	const slowLinkBitsPerSecond = 5_000_000
	const attnBinaryBytes = 58_934_608
	const appRuntimeBytes = 93_694_096

	binaryTransfer := time.Duration(float64(attnBinaryBytes*8) / slowLinkBitsPerSecond * float64(time.Second))
	if want := binaryTransfer + remoteDaemonReadyTimeout; remoteReadyBudget <= want {
		t.Errorf("remoteReadyBudget %v does not cover a %d-byte binary at 5 Mbit/s plus the %v readiness wait (%v)",
			remoteReadyBudget, attnBinaryBytes, remoteDaemonReadyTimeout, want)
	}

	runtimeTransfer := time.Duration(float64(appRuntimeBytes*8) / slowLinkBitsPerSecond * float64(time.Second))
	if appRuntimeShipBudget <= runtimeTransfer {
		t.Errorf("appRuntimeShipBudget %v does not cover a %d-byte sidecar at 5 Mbit/s (%v)",
			appRuntimeShipBudget, appRuntimeBytes, runtimeTransfer)
	}
}
