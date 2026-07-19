package ptybackend

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/victorarias/attn/internal/launchcontract"
)

func TestUnattendedLaunchContractParityAcrossBackends(t *testing.T) {
	tests := []struct {
		name string
		spec launchcontract.UnattendedLaunchSpec
	}{
		{
			name: "codex auto review",
			spec: launchcontract.UnattendedLaunchSpec{
				Agent: "codex", Model: "gpt-test", Effort: "high", Executable: "/opt/codex",
				ApprovalProductMode: launchcontract.ApprovalAuto, ApprovalDriverMode: launchcontract.ApprovalAutoReview,
				DirectoryTrust: launchcontract.TrustConfiguredDirectory, Recovery: launchcontract.RecoveryAdoptOrRestartFresh,
			},
		},
		{
			name: "claude auto",
			spec: launchcontract.UnattendedLaunchSpec{
				Agent: "claude", Model: "sonnet", Effort: "low",
				ApprovalProductMode: launchcontract.ApprovalAuto, ApprovalDriverMode: launchcontract.ApprovalAuto,
				DirectoryTrust: launchcontract.TrustConfiguredDirectory, Recovery: launchcontract.RecoveryAdoptOrRestartFresh,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			embedded := embeddedSpawnOptions(SpawnOptions{Agent: tt.spec.Agent, UnattendedLaunch: tt.spec})
			if !reflect.DeepEqual(embedded.UnattendedLaunch, tt.spec) {
				t.Fatalf("embedded contract = %#v, want %#v", embedded.UnattendedLaunch, tt.spec)
			}

			args, err := appendUnattendedLaunchArgs(nil, tt.spec)
			if err != nil {
				t.Fatal(err)
			}
			if len(args) != 2 || args[0] != "--unattended-launch-json" {
				t.Fatalf("worker args = %#v", args)
			}
			var worker launchcontract.UnattendedLaunchSpec
			if err := json.Unmarshal([]byte(args[1]), &worker); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(worker, tt.spec) {
				t.Fatalf("worker contract = %#v, want %#v", worker, tt.spec)
			}
		})
	}
}

func TestUnattendedLaunchContractRejectsParallelPolicyFields(t *testing.T) {
	spec := launchcontract.UnattendedLaunchSpec{
		Agent: "codex", Model: "exact",
		ApprovalProductMode: launchcontract.ApprovalAuto, ApprovalDriverMode: launchcontract.ApprovalAutoReview,
		DirectoryTrust: launchcontract.TrustConfiguredDirectory, Recovery: launchcontract.RecoveryAdoptOrRestartFresh,
	}
	for _, opts := range []SpawnOptions{
		{Agent: "codex", UnattendedLaunch: spec, Model: "duplicate"},
		{Agent: "codex", UnattendedLaunch: spec, AutoApprove: true},
		{Agent: "codex", UnattendedLaunch: spec, TrustWorkingDirectory: true},
	} {
		if err := validateUnattendedSpawnOptions(opts); err == nil {
			t.Fatalf("accepted parallel unattended policy fields: %#v", opts)
		}
	}
}
