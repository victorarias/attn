package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/victorarias/attn/internal/preflight"
)

func runPreflight() {
	opts, jsonOutput, help, err := parsePreflightArgs(os.Args[2:], os.Getenv)
	if help {
		writePreflightHelp(os.Stdout)
		return
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "preflight: %v\n", err)
		writePreflightHelp(os.Stderr)
		os.Exit(2)
	}

	report := preflight.Run(context.Background(), opts)
	if jsonOutput {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			fmt.Fprintf(os.Stderr, "preflight: encode result: %v\n", err)
			os.Exit(1)
		}
	} else {
		writePreflightReport(os.Stdout, report)
	}
	if !report.OK() {
		os.Exit(1)
	}
}

func parsePreflightArgs(args []string, getenv func(string) string) (preflight.Options, bool, bool, error) {
	fs := flag.NewFlagSet("preflight", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	agentDefault, agentSource := environmentDefault(getenv, "ATTN_AGENT", "codex")
	modelDefault, modelSource := environmentDefault(getenv, "ATTN_MODEL", "")
	effortDefault, effortSource := environmentDefault(getenv, "ATTN_EFFORT", "")
	agent := fs.String("agent", agentDefault, "agent launch to check")
	model := fs.String("model", modelDefault, "model pin to check")
	effort := fs.String("effort", effortDefault, "reasoning effort pin to check")
	jsonOutput := fs.Bool("json", false, "emit the stable JSON report")
	help := fs.Bool("help", false, "show help")
	fs.BoolVar(help, "h", false, "show help")
	if err := fs.Parse(args); err != nil {
		return preflight.Options{}, false, false, err
	}
	if fs.NArg() != 0 {
		return preflight.Options{}, false, false, fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	if flagWasSet(fs, "agent") {
		agentSource = "explicit"
	}
	if flagWasSet(fs, "model") {
		modelSource = "explicit"
	}
	if flagWasSet(fs, "effort") {
		effortSource = "explicit"
	}
	return preflight.Options{
		Agent: *agent, AgentSource: agentSource,
		Model: *model, ModelSource: modelSource,
		Effort: *effort, EffortSource: effortSource,
	}, *jsonOutput, *help, nil
}

func environmentDefault(getenv func(string) string, key, fallback string) (string, string) {
	if value := strings.TrimSpace(getenv(key)); value != "" {
		return value, "environment"
	}
	if fallback != "" {
		return fallback, "default"
	}
	return "", "agent_default"
}

func flagWasSet(fs *flag.FlagSet, name string) bool {
	found := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}

func writePreflightReport(w io.Writer, report preflight.Report) {
	failed, warned := 0, 0
	for _, check := range report.Checks {
		switch check.Status {
		case preflight.StatusFail:
			failed++
		case preflight.StatusWarn:
			warned++
		}
	}
	fmt.Fprintf(w, "attn preflight: %s (%d failed, %d warnings)\n", strings.ToUpper(report.Status), failed, warned)
	fmt.Fprintf(w, "profile: %s  socket=%s  port=%s\n", report.Routing.Label, report.Routing.Socket, report.Routing.WSPort)
	fmt.Fprintf(w, "launch: agent=%s model=%s effort=%s\n\n",
		resolvedDisplay(report.Launch.Agent), resolvedDisplay(report.Launch.Model), resolvedDisplay(report.Launch.Effort))
	for _, check := range report.Checks {
		fmt.Fprintf(w, "%-4s %-25s %s\n", strings.ToUpper(check.Status), check.Name, check.Summary)
		if check.Action != "" {
			fmt.Fprintf(w, "     action: %s\n", check.Action)
		}
	}
}

func resolvedDisplay(value preflight.ResolvedValue) string {
	if value.Value == "" {
		return "agent-default"
	}
	return value.Value + " (" + value.Source + ")"
}

func writePreflightHelp(w io.Writer) {
	fmt.Fprint(w, `usage: attn preflight [options]

Diagnose the active profile without changing it. The command exits non-zero
when a required tool, writable path, route, daemon, or protocol check fails.

options:
  --agent <name>   agent launch to check (ATTN_AGENT, then codex)
  --model <name>   model pin to check (ATTN_MODEL, then agent default)
  --effort <level> effort pin to check (ATTN_EFFORT, then agent default)
  --json           emit the stable machine-readable report
`)
}
