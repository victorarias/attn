package daemonctl

import (
	"testing"

	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/protocol"
)

func TestDaemonMatchesCurrentBinary_UsesSourceFingerprintWhenAvailable(t *testing.T) {
	previousFingerprint := buildinfo.SourceFingerprint
	t.Cleanup(func() {
		buildinfo.SourceFingerprint = previousFingerprint
	})
	buildinfo.SourceFingerprint = "tree:new"

	if !daemonMatchesCurrentBinary(healthResponse{SourceFingerprint: "tree:new", Protocol: "old"}) {
		t.Fatal("expected matching source fingerprint to win")
	}
	if daemonMatchesCurrentBinary(healthResponse{SourceFingerprint: "tree:old", Protocol: protocol.ProtocolVersion}) {
		t.Fatal("expected mismatched source fingerprint to fail")
	}
}

func TestDaemonMatchesCurrentBinary_FallsBackToProtocolWhenFingerprintUnknown(t *testing.T) {
	previousFingerprint := buildinfo.SourceFingerprint
	t.Cleanup(func() {
		buildinfo.SourceFingerprint = previousFingerprint
	})
	buildinfo.SourceFingerprint = "unknown"

	if !daemonMatchesCurrentBinary(healthResponse{Protocol: protocol.ProtocolVersion}) {
		t.Fatal("expected protocol fallback match")
	}
	if daemonMatchesCurrentBinary(healthResponse{Protocol: "999"}) {
		t.Fatal("expected protocol fallback mismatch")
	}
}

func TestMismatchReason_ReportsMissingFingerprint(t *testing.T) {
	previousFingerprint := buildinfo.SourceFingerprint
	t.Cleanup(func() {
		buildinfo.SourceFingerprint = previousFingerprint
	})
	buildinfo.SourceFingerprint = "tree:new"

	if got := mismatchReason(nil, healthResponse{}); got != "source_fingerprint_missing" {
		t.Fatalf("mismatchReason() = %q, want source_fingerprint_missing", got)
	}
}
