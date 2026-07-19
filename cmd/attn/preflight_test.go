package main

import "testing"

func TestParsePreflightArgsResolvesExplicitAndEnvironmentValues(t *testing.T) {
	env := map[string]string{"ATTN_AGENT": "claude", "ATTN_MODEL": "env-model", "ATTN_EFFORT": "low"}
	getenv := func(key string) string { return env[key] }
	opts, jsonOutput, help, err := parsePreflightArgs([]string{"--model", "flag-model", "--json"}, getenv)
	if err != nil || help || !jsonOutput {
		t.Fatalf("parse result: json=%v help=%v err=%v", jsonOutput, help, err)
	}
	if opts.Agent != "claude" || opts.AgentSource != "environment" {
		t.Fatalf("agent = %+v", opts)
	}
	if opts.Model != "flag-model" || opts.ModelSource != "explicit" {
		t.Fatalf("model = %+v", opts)
	}
	if opts.Effort != "low" || opts.EffortSource != "environment" {
		t.Fatalf("effort = %+v", opts)
	}
}

func TestParsePreflightArgsUsesDocumentedDefaults(t *testing.T) {
	opts, _, _, err := parsePreflightArgs(nil, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if opts.Agent != "codex" || opts.AgentSource != "default" {
		t.Fatalf("agent = %+v", opts)
	}
	if opts.Model != "" || opts.ModelSource != "agent_default" || opts.EffortSource != "agent_default" {
		t.Fatalf("model/effort = %+v", opts)
	}
}
