package store

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/automode"
	"github.com/victorarias/attn/internal/config"
)

// userHardDeny drops the shipped entries every read resolves in, leaving what a
// promotion actually put there.
func userHardDeny(resolved []string) []string {
	return automode.StripShippedHardDeny(config.WSPort(), resolved)
}

func TestAutoModeConfigDefaultsOnAFreshDatabase(t *testing.T) {
	s := New()
	cfg, err := s.GetAutoModeConfig()
	if err != nil {
		t.Fatalf("get config: %v", err)
	}
	if cfg.ClassifierModel != automode.DefaultClassifierModel {
		t.Errorf("classifier model = %q, want the shipped default", cfg.ClassifierModel)
	}
	if cfg.EscalationModel != automode.DefaultEscalationModel {
		t.Errorf("escalation model = %q, want the shipped default", cfg.EscalationModel)
	}
	if !cfg.EnabledDefault {
		t.Error("enabled_default = false on a fresh database, want true")
	}
	if len(cfg.Allow) != 0 || len(cfg.Environment) != 0 {
		t.Errorf("fresh config is not empty: %+v", cfg)
	}
	if diff := len(cfg.HardDeny) - len(automode.ShippedHardDeny(config.WSPort())); diff != 0 {
		t.Errorf("hard deny = %v, want exactly the shipped denies", cfg.HardDeny)
	}
}

func TestAutoModeEnvironmentRoundTrips(t *testing.T) {
	s := New()
	now := time.Now().UTC()
	cfg, err := s.SetAutoModeEnvironment([]string{"pushing to origin is fine", "never touch prod"}, now)
	if err != nil {
		t.Fatalf("set environment: %v", err)
	}
	if len(cfg.Environment) != 2 {
		t.Fatalf("environment = %v, want two entries", cfg.Environment)
	}
	read, err := s.GetAutoModeConfig()
	if err != nil {
		t.Fatalf("get config: %v", err)
	}
	if read.Environment[1] != "never touch prod" {
		t.Errorf("environment[1] = %q", read.Environment[1])
	}
	// Editing prose must not disturb the models a promote set.
	if read.ClassifierModel != automode.DefaultClassifierModel {
		t.Errorf("classifier model drifted to %q", read.ClassifierModel)
	}
}

// The whole point of the split: recording a proposal changes nothing a session
// launches with.
func TestAutoModeProposalDoesNotChangeTheConfig(t *testing.T) {
	s := New()
	now := time.Now().UTC()
	before, err := s.GetAutoModeConfig()
	if err != nil {
		t.Fatalf("get config: %v", err)
	}
	if _, err := s.CreateAutoModeProposal(automode.KindAllow, "", "git push origin*", "session-1", now); err != nil {
		t.Fatalf("create proposal: %v", err)
	}
	if _, err := s.CreateAutoModeProposal(automode.KindModel, automode.TargetClassifier, "opencode-go/other-model", "", now); err != nil {
		t.Fatalf("create model proposal: %v", err)
	}
	after, err := s.GetAutoModeConfig()
	if err != nil {
		t.Fatalf("get config: %v", err)
	}
	if len(after.Allow) != 0 {
		t.Errorf("allow list changed to %v", after.Allow)
	}
	if after.ClassifierModel != before.ClassifierModel {
		t.Errorf("classifier model changed to %q", after.ClassifierModel)
	}
	pending, err := s.ListAutoModeProposals(automode.StatePending)
	if err != nil {
		t.Fatalf("list proposals: %v", err)
	}
	if len(pending) != 2 {
		t.Fatalf("pending proposals = %d, want 2", len(pending))
	}
	if pending[0].ProposedBy != "session-1" {
		t.Errorf("proposed_by = %q, want the recorded session", pending[0].ProposedBy)
	}
}

func TestAutoModeCreateProposalRefusesABroadAllow(t *testing.T) {
	s := New()
	if _, err := s.CreateAutoModeProposal(automode.KindAllow, "", "*", "", time.Now()); err == nil {
		t.Fatal("a broad allow proposal was recorded")
	}
	pending, err := s.ListAutoModeProposals("")
	if err != nil {
		t.Fatalf("list proposals: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("a refused proposal reached the table: %+v", pending)
	}
}

func TestAutoModePromoteAppliesAndClosesTheProposal(t *testing.T) {
	s := New()
	now := time.Now().UTC()
	allow, err := s.CreateAutoModeProposal(automode.KindAllow, "", "git push origin*", "", now)
	if err != nil {
		t.Fatalf("create allow: %v", err)
	}
	model, err := s.CreateAutoModeProposal(automode.KindModel, automode.TargetEscalation, "opencode-go/kimi-k3", "", now)
	if err != nil {
		t.Fatalf("create model: %v", err)
	}

	promoted, cfg, err := s.PromoteAutoModeProposal(allow.ID, now)
	if err != nil {
		t.Fatalf("promote allow: %v", err)
	}
	if promoted.State != automode.StatePromoted || promoted.ResolvedAt.IsZero() {
		t.Errorf("promoted proposal = %+v", promoted)
	}
	if len(cfg.Allow) != 1 || cfg.Allow[0] != "git push origin*" {
		t.Fatalf("allow list = %v", cfg.Allow)
	}
	if _, cfg, err = s.PromoteAutoModeProposal(model.ID, now); err != nil {
		t.Fatalf("promote model: %v", err)
	}
	if cfg.EscalationModel != "opencode-go/kimi-k3" {
		t.Errorf("escalation model = %q", cfg.EscalationModel)
	}
	if cfg.ClassifierModel != automode.DefaultClassifierModel {
		t.Errorf("classifier model = %q, want it untouched", cfg.ClassifierModel)
	}

	read, err := s.GetAutoModeConfig()
	if err != nil {
		t.Fatalf("get config: %v", err)
	}
	if len(read.Allow) != 1 || read.EscalationModel != "opencode-go/kimi-k3" {
		t.Fatalf("promoted config did not survive the read: %+v", read)
	}
	pending, err := s.ListAutoModeProposals(automode.StatePending)
	if err != nil {
		t.Fatalf("list proposals: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("promoted proposals are still pending: %+v", pending)
	}
}

func TestAutoModePromoteIsIdempotentlyRefusedTwice(t *testing.T) {
	s := New()
	now := time.Now().UTC()
	p, err := s.CreateAutoModeProposal(automode.KindDeny, "", "rm -rf /*", "", now)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, _, err := s.PromoteAutoModeProposal(p.ID, now); err != nil {
		t.Fatalf("first promote: %v", err)
	}
	if _, _, err := s.PromoteAutoModeProposal(p.ID, now); err == nil {
		t.Fatal("a second promote was accepted")
	}
	cfg, err := s.GetAutoModeConfig()
	if err != nil {
		t.Fatalf("get config: %v", err)
	}
	if got := userHardDeny(cfg.HardDeny); len(got) != 1 {
		t.Fatalf("promoted hard deny = %v, want one entry after two promotes", got)
	}
}

func TestAutoModeDiscardLeavesTheConfigAlone(t *testing.T) {
	s := New()
	now := time.Now().UTC()
	p, err := s.CreateAutoModeProposal(automode.KindAllow, "", "curl https://example.com*", "", now)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	discarded, err := s.DiscardAutoModeProposal(p.ID, now)
	if err != nil {
		t.Fatalf("discard: %v", err)
	}
	if discarded.State != automode.StateDiscarded {
		t.Errorf("state = %q", discarded.State)
	}
	cfg, err := s.GetAutoModeConfig()
	if err != nil {
		t.Fatalf("get config: %v", err)
	}
	if len(cfg.Allow) != 0 {
		t.Fatalf("allow list = %v after a discard", cfg.Allow)
	}
	if _, _, err := s.PromoteAutoModeProposal(p.ID, now); err == nil {
		t.Fatal("a discarded proposal was promoted")
	}
}

func TestAutoModeDenialsReadNewestFirst(t *testing.T) {
	s := New()
	now := time.Now().UTC()
	for _, signature := range []string{"bash curl evil.example", "write /etc/hosts", "bash git push --force"} {
		if _, err := s.RecordAutoModeDenial("session-1", "bash", signature, "outside the envelope", now); err != nil {
			t.Fatalf("record denial: %v", err)
		}
	}
	denials, err := s.ListAutoModeDenials(2)
	if err != nil {
		t.Fatalf("list denials: %v", err)
	}
	if len(denials) != 2 {
		t.Fatalf("denials = %d, want the limit of 2", len(denials))
	}
	if denials[0].Signature != "bash git push --force" {
		t.Errorf("newest denial = %q", denials[0].Signature)
	}
}

// Migration 109 is what every one of these tests runs against; this asserts it
// by name so a renumbering that skips it fails here rather than in production.
func TestAutoModeMigrationCreatesItsTables(t *testing.T) {
	s := New()
	for _, table := range []string{"automode_config", "automode_proposals", "automode_denials"} {
		var name string
		err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name)
		if err != nil {
			t.Fatalf("table %s is missing: %v", table, err)
		}
	}
	var applied int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version = 109`).Scan(&applied); err != nil {
		t.Fatalf("read schema_migrations: %v", err)
	}
	if applied != 1 {
		t.Fatalf("migration 109 applied %d times, want once", applied)
	}
}

func TestAutoModeShippedHardDeniesSurviveAPromotedRow(t *testing.T) {
	s := New()
	now := time.Now().UTC()
	p, err := s.CreateAutoModeProposal(automode.KindDeny, "", "ssh prod*", "", now)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, _, err := s.PromoteAutoModeProposal(p.ID, now); err != nil {
		t.Fatalf("promote: %v", err)
	}
	cfg, err := s.GetAutoModeConfig()
	if err != nil {
		t.Fatalf("get config: %v", err)
	}
	for _, want := range automode.ShippedHardDeny(config.WSPort()) {
		if !containsString(cfg.HardDeny, want) {
			t.Errorf("hard deny %v is missing the shipped entry %q", cfg.HardDeny, want)
		}
	}
	if got := userHardDeny(cfg.HardDeny); len(got) != 1 || got[0] != "ssh prod*" {
		t.Errorf("stored hard deny = %v, want only the promoted pattern", got)
	}
	// Resolved at read means resolved at read: the row a write left behind must
	// not have frozen today's shipped list into it.
	var stored string
	if err := s.db.QueryRow(`SELECT hard_deny FROM automode_config WHERE id = 1`).Scan(&stored); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if stored != `["ssh prod*"]` {
		t.Errorf("persisted hard_deny = %s, want only the promoted pattern", stored)
	}
}

func TestAutoModeProposalDedupesAnIdenticalPendingOne(t *testing.T) {
	s := New()
	now := time.Now().UTC()
	first, err := s.CreateAutoModeProposal(automode.KindAllow, "", "git push origin*", "session-a", now)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	again, err := s.CreateAutoModeProposal(automode.KindAllow, "", "git push origin*", "session-a", now)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if again.ID != first.ID {
		t.Errorf("second proposal id = %d, want the existing %d", again.ID, first.ID)
	}
	pending, err := s.ListAutoModeProposals(automode.StatePending)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(pending) != 1 {
		t.Errorf("pending = %v, want one row", pending)
	}
	// A resolved proposal is not a duplicate: the same ask after a discard is a
	// new ask.
	if _, err := s.DiscardAutoModeProposal(first.ID, now); err != nil {
		t.Fatalf("discard: %v", err)
	}
	third, err := s.CreateAutoModeProposal(automode.KindAllow, "", "git push origin*", "session-a", now)
	if err != nil {
		t.Fatalf("third: %v", err)
	}
	if third.ID == first.ID {
		t.Error("a discarded proposal was reused instead of a new one being recorded")
	}
}

func TestAutoModeProposalCapNamesTheLimitAndTheAsk(t *testing.T) {
	s := New()
	now := time.Now().UTC()
	for i := 0; i < automode.MaxPendingProposalsPerProposer; i++ {
		if _, err := s.CreateAutoModeProposal(
			automode.KindAllow, "", fmt.Sprintf("curl https://example.com/%d*", i), "session-a", now,
		); err != nil {
			t.Fatalf("proposal %d: %v", i, err)
		}
	}
	_, err := s.CreateAutoModeProposal(automode.KindAllow, "", "curl https://example.com/last*", "session-a", now)
	if err == nil {
		t.Fatal("the proposal past the cap was accepted")
	}
	for _, want := range []string{
		"session-a",
		fmt.Sprintf("%d", automode.MaxPendingProposalsPerProposer),
		"curl https://example.com/last*",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("cap error %q does not name %q", err, want)
		}
	}
	// The cap is per proposer, and resolving one frees a slot.
	if _, err := s.CreateAutoModeProposal(automode.KindAllow, "", "curl https://example.com/last*", "session-b", now); err != nil {
		t.Errorf("another proposer was capped too: %v", err)
	}
	pending, _ := s.ListAutoModeProposals(automode.StatePending)
	if _, err := s.DiscardAutoModeProposal(pending[0].ID, now); err != nil {
		t.Fatalf("discard: %v", err)
	}
	if _, err := s.CreateAutoModeProposal(automode.KindAllow, "", "curl https://example.com/after*", "session-a", now); err != nil {
		t.Errorf("a freed slot was still refused: %v", err)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
