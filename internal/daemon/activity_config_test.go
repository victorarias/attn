package daemon

import (
	"errors"
	"testing"
)

// The one rule that separates this from every other agent-backed duty: there is
// no default agent. Claude and Codex differ enough in speed, price, and which
// account pays that picking one would be choosing how the user's money is spent.
func TestActivityConfigRefusesToPickAnAgent(t *testing.T) {
	for _, raw := range []string{"", "   ", `{}`, `{"model":"claude-haiku-4-5"}`, `{"agent":"  "}`} {
		if _, err := parseActivityConfig(raw); !errors.Is(err, errActivityAgentUnset) {
			t.Errorf("parseActivityConfig(%q) error = %v, want the unset-agent error", raw, err)
		}
	}
}

func TestActivityConfigFillsInTheAgentsDefaults(t *testing.T) {
	claude, err := parseActivityConfig(`{"agent":"claude"}`)
	if err != nil {
		t.Fatalf("claude: %v", err)
	}
	if claude.Model != activityClaudeDefaultModel {
		t.Errorf("claude model = %q, want %q", claude.Model, activityClaudeDefaultModel)
	}
	// Effort is left unset on Claude on purpose: it measured inert on
	// claude-haiku-4-5, so pinning one would be a setting that does nothing.
	if claude.Effort != "" {
		t.Errorf("claude effort = %q, want unset", claude.Effort)
	}

	codex, err := parseActivityConfig(`{"agent":"codex"}`)
	if err != nil {
		t.Fatalf("codex: %v", err)
	}
	if codex.Model != activityCodexDefaultModel {
		t.Errorf("codex model = %q, want %q", codex.Model, activityCodexDefaultModel)
	}
	if codex.Effort != activityCodexDefaultEffort {
		t.Errorf("codex effort = %q, want %q", codex.Effort, activityCodexDefaultEffort)
	}
}

func TestActivityConfigKeepsAnExplicitChoice(t *testing.T) {
	config, err := parseActivityConfig(`{"agent":"codex","model":"gpt-5.6","effort":"medium"}`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if config.Model != "gpt-5.6" || config.Effort != "medium" {
		t.Errorf("config = %+v, want the explicit model and effort", config)
	}
}

func TestActivityConfigRejectsWhatItCannotRun(t *testing.T) {
	cases := map[string]string{
		"an agent that is not installed": `{"agent":"nonesuch","model":"m"}`,
		"a field nobody defined":         `{"agent":"claude","mdoel":"typo"}`,
		"trailing junk after the object": `{"agent":"claude"} and more`,
		"not JSON at all":                `claude`,
	}
	for name, raw := range cases {
		if _, err := parseActivityConfig(raw); err == nil {
			t.Errorf("%s: parseActivityConfig(%q) accepted it", name, raw)
		}
	}
}

func TestActivityIntervalsDefaultAndClamp(t *testing.T) {
	defaults, err := parseActivityIntervals("")
	if err != nil {
		t.Fatalf("blank: %v", err)
	}
	if defaults.Watching != defaultActivityWatchingSeconds || defaults.Present != defaultActivityPresentSeconds {
		t.Errorf("defaults = %+v", defaults)
	}

	// Out of range is a settings-pane typo. Clamping keeps generating; rejecting
	// would stop the feature over a stray zero.
	clamped, err := parseActivityIntervals(`{"watching":1,"present":99999}`)
	if err != nil {
		t.Fatalf("out of range: %v", err)
	}
	if clamped.Watching != activityIntervalMinSeconds {
		t.Errorf("watching = %d, want clamped to %d", clamped.Watching, activityIntervalMinSeconds)
	}
	if clamped.Present != activityIntervalMaxSeconds {
		t.Errorf("present = %d, want clamped to %d", clamped.Present, activityIntervalMaxSeconds)
	}

	if _, err := parseActivityIntervals(`{"watching":120,"away":0}`); err == nil {
		t.Error("an `away` interval was accepted; away is a stop, not a rate")
	}
}

// The one distinction that has to be right: away means generate nothing, and
// zero is how every caller reads that.
func TestActivityIntervalIsZeroWhenAway(t *testing.T) {
	d := &Daemon{}
	if got := d.activityInterval(PresenceAway); got != 0 {
		t.Errorf("away interval = %v, want zero", got)
	}
	if got := d.activityInterval(PresenceWatching); got == 0 {
		t.Error("watching interval is zero, which would read as away")
	}
	if d.activityInterval(PresenceWatching) >= d.activityInterval(PresencePresent) {
		t.Error("watching must refresh faster than present")
	}
}

func TestActivityIsOffWithoutAStore(t *testing.T) {
	d := &Daemon{}
	if d.activityEnabled() {
		t.Error("activity reported enabled with no settings to read")
	}
	if _, err := d.activityConfigured(); err == nil {
		t.Error("activityConfigured succeeded with no settings to read")
	}
}
