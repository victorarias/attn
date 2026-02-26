package agent

import (
	"os/exec"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

type testDriver struct {
	name string
	caps Capabilities
}

func (d testDriver) Name() string                               { return d.name }
func (d testDriver) DisplayName() string                        { return d.name }
func (d testDriver) DefaultExecutable() string                  { return d.name }
func (d testDriver) ExecutableEnvVar() string                   { return "" }
func (d testDriver) ResolveExecutable(configured string) string { return configured }
func (d testDriver) BuildCommand(opts SpawnOpts) *exec.Cmd      { return exec.Command("true") }
func (d testDriver) BuildEnv(opts SpawnOpts) []string           { return nil }
func (d testDriver) Capabilities() Capabilities                 { return d.caps }

func TestEffectiveCapabilities_NilDriver(t *testing.T) {
	caps := EffectiveCapabilities(nil)
	if caps.HasHooks || caps.HasTranscript || caps.HasClassifier || caps.HasStateDetector {
		t.Fatalf("expected zero capabilities for nil driver, got %+v", caps)
	}
	if _, ok := GetTranscriptFinder(nil); ok {
		t.Fatal("expected no transcript finder for nil driver")
	}
}

func TestEffectiveCapabilities_Default(t *testing.T) {
	d := testDriver{
		name: "pi",
		caps: Capabilities{HasTranscript: true, HasTranscriptWatcher: true},
	}
	caps := EffectiveCapabilities(d)
	if !caps.HasTranscript || !caps.HasTranscriptWatcher {
		t.Fatalf("expected default caps preserved, got %+v", caps)
	}
}

func TestEffectiveCapabilities_EnvOverride(t *testing.T) {
	t.Setenv("ATTN_AGENT_PI_TRANSCRIPT", "0")
	t.Setenv("ATTN_AGENT_PI_TRANSCRIPT_WATCHER", "1")

	d := testDriver{
		name: "pi",
		caps: Capabilities{HasTranscript: true, HasTranscriptWatcher: true},
	}
	caps := EffectiveCapabilities(d)
	if caps.HasTranscript {
		t.Fatalf("expected transcript disabled by env, got %+v", caps)
	}
	if caps.HasTranscriptWatcher {
		t.Fatalf("transcript watcher should auto-disable when transcript=false, got %+v", caps)
	}
}

func TestEffectiveCapabilities_SanitizedAgentName(t *testing.T) {
	t.Setenv("ATTN_AGENT_GEMINI_CLI_CLASSIFIER", "0")
	d := testDriver{
		name: "gemini-cli",
		caps: Capabilities{HasClassifier: true},
	}
	caps := EffectiveCapabilities(d)
	if caps.HasClassifier {
		t.Fatalf("expected classifier disabled via sanitized env key, got %+v", caps)
	}
}

func TestBuiltInPiDriver_MinimalCapabilities(t *testing.T) {
	d := Get("pi")
	if d == nil {
		t.Fatal("expected pi driver to be registered")
	}
	caps := EffectiveCapabilities(d)
	if caps.HasTranscript || caps.HasHooks || caps.HasClassifier {
		t.Fatalf("pi should be minimal by default, got %+v", caps)
	}
}

type behaviorProviderDriver struct {
	testDriver
	behavior TranscriptWatcherBehavior
}

func (d behaviorProviderDriver) NewTranscriptWatcherBehavior() TranscriptWatcherBehavior {
	return d.behavior
}

type customBehavior struct{}

func (b *customBehavior) Reset() {}

func (b *customBehavior) HandleLine(line []byte, now time.Time, sessionState protocol.SessionState) WatcherLineResult {
	return WatcherLineResult{}
}

func (b *customBehavior) HandleAssistantMessage(now time.Time) {}

func (b *customBehavior) DeduplicateAssistantEvents() bool { return true }

func (b *customBehavior) QuietSince(lastAssistantAt time.Time) time.Time { return lastAssistantAt }

func (b *customBehavior) Tick(now time.Time, sessionState protocol.SessionState) WatcherTickResult {
	return WatcherTickResult{}
}

func (b *customBehavior) SkipClassification(sessionState protocol.SessionState, lastSeen string, now time.Time) (bool, string) {
	return false, ""
}

func TestGetTranscriptWatcherBehavior_DefaultFallback(t *testing.T) {
	d := testDriver{
		name: "pi",
		caps: Capabilities{
			HasTranscript:        true,
			HasTranscriptWatcher: true,
		},
	}

	behavior, ok := GetTranscriptWatcherBehavior(d)
	if !ok || behavior == nil {
		t.Fatal("expected default watcher behavior for transcript-enabled driver")
	}
}

func TestGetTranscriptWatcherBehavior_ProviderOverride(t *testing.T) {
	expected := &customBehavior{}
	d := behaviorProviderDriver{
		testDriver: testDriver{
			name: "codex",
			caps: Capabilities{
				HasTranscript:        true,
				HasTranscriptWatcher: true,
			},
		},
		behavior: expected,
	}

	behavior, ok := GetTranscriptWatcherBehavior(d)
	if !ok {
		t.Fatal("expected watcher behavior from provider")
	}
	if behavior != expected {
		t.Fatal("expected provider behavior to be returned unchanged")
	}
}
