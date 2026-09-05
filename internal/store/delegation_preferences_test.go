package store

import (
	"errors"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/victorarias/attn/internal/delegationprefs"
)

func configuredDelegationPreferences() delegationprefs.Config {
	return delegationprefs.Config{Enabled: true, Roles: []delegationprefs.Role{{ID: "build", Name: "Build", Enabled: true, Description: "Implement changes", Instructions: "Keep {{literal}} intact", DefaultChoiceID: "default", Choices: []delegationprefs.Choice{{ID: "default", Name: "Everyday", Selection: delegationprefs.Selection{Harness: "codex", Model: "test-model", Effort: "medium"}}}}}, Fallback: delegationprefs.Fallback{Selection: delegationprefs.Selection{Harness: "copilot"}}}
}

func TestDelegationPreferencesRoundTripAndDisable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preferences.db")
	s, err := NewWithDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cfg, err := s.GetDelegationPreferences()
	if err != nil || cfg.Enabled || cfg.Revision != 0 || len(cfg.Roles) != 0 {
		t.Fatalf("fresh config: %+v, %v", cfg, err)
	}
	cfg, err = s.SaveDelegationPreferences(configuredDelegationPreferences())
	if err != nil {
		t.Fatal(err)
	}
	saved := cfg
	cfg.Enabled = false
	cfg, err = s.SaveDelegationPreferences(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Revision != 2 || !reflect.DeepEqual(saved.Roles, cfg.Roles) {
		t.Fatalf("disable changed saved roles: %+v", cfg)
	}
	other, err := NewWithDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	got, err := other.GetDelegationPreferences()
	if err != nil || !reflect.DeepEqual(got, cfg) {
		t.Fatalf("persisted: %+v, %v", got, err)
	}
	if out := delegationprefs.Active(got); len(out.Roles) != 0 || out.Fallback != nil {
		t.Fatalf("disabled configuration leaked: %+v", out)
	}
}

func TestDelegationPreferencesConcurrentEditsRefuseLostUpdate(t *testing.T) {
	s := New()
	defer s.Close()
	cfg, err := s.SaveDelegationPreferences(configuredDelegationPreferences())
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() { _, err := s.SaveDelegationPreferences(cfg); results <- err })
	}
	wg.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		if err == nil {
			successes++
		} else if errors.Is(err, delegationprefs.ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d", successes, conflicts)
	}
}

func TestDelegationPreferencesInvalidEditsDoNotPersist(t *testing.T) {
	s := New()
	defer s.Close()
	for _, mutate := range []func(*delegationprefs.Config){
		func(c *delegationprefs.Config) { c.Roles[0].DefaultChoiceID = "missing" },
		func(c *delegationprefs.Config) { c.Roles = append(c.Roles, c.Roles[0]) },
		func(c *delegationprefs.Config) {
			c.Roles[0].Choices = append(c.Roles[0].Choices, c.Roles[0].Choices[0])
		},
		func(c *delegationprefs.Config) {
			c.Fallback.Selection.Harness = ""
			c.Fallback.Selection.Model = "orphan"
		},
	} {
		c := configuredDelegationPreferences()
		mutate(&c)
		if _, err := s.SaveDelegationPreferences(c); err == nil {
			t.Fatalf("accepted %+v", c)
		}
	}
	c, err := s.GetDelegationPreferences()
	if err != nil || c.Revision != 0 {
		t.Fatalf("invalid edit persisted: %+v %v", c, err)
	}
	if _, err := s.db.Exec(`INSERT INTO delegation_preferences(id,config) VALUES (1,'broken')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetDelegationPreferences(); err == nil {
		t.Fatal("corrupt config silently became defaults")
	}
}
