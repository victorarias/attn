package main

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/victorarias/attn/internal/automode"
	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/protocol"
)

func runAutoMode() {
	if len(os.Args) < 3 || os.Args[2] == "-h" || os.Args[2] == "--help" {
		writeAutoModeHelp(os.Stdout)
		return
	}
	args := os.Args[3:]
	switch os.Args[2] {
	case "show":
		runAutoModeShow(args)
	case "env":
		runAutoModeEnv(args)
	case "allow":
		runAutoModePropose("allow", automode.KindAllow, "", args)
	case "deny":
		runAutoModePropose("deny", automode.KindDeny, "", args)
	case "model":
		runAutoModeModel(args)
	case "denials":
		runAutoModeDenials(args)
	default:
		fmt.Fprintf(os.Stderr, "automode: unknown command %q\n", os.Args[2])
		writeAutoModeHelp(os.Stderr)
		os.Exit(2)
	}
}

func writeAutoModeHelp(w io.Writer) {
	fmt.Fprint(w, `usage: attn automode <command>

pi's auto mode: a static safety envelope plus a classifier for everything that
reaches past it. This CLI proposes changes; only the attn app promotes one.

commands:
  show                              effective config and pending proposals
  env                               list the classifier's environment prose
  env add <text>                    add one prose entry
  env remove <index>                remove the entry at <index> (see env)
  allow <pattern>                   propose an allow entry
  deny <pattern>                    propose a hard-deny entry
  model classifier <provider/id>…   propose layer 2a's models, primary first
  model escalation <provider/id>…   propose layer 2b's models, primary first
  denials [--limit <n>]             recent denials, newest first

Every command takes --json.

allow, deny and model RECORD A PROPOSAL. Nothing they write changes what a
session runs under until a human promotes it in the app. A broad allow pattern —
one with no literal characters left after the wildcards — is refused outright.

Environment prose is a direct edit: it is what the classifier reads about this
machine, not a rule that skips it.

A layer's models are an ordered list — the first one judges, and the rest are
tried only when the one before it cannot be reached. Name them separated by
spaces or commas; the proposal replaces that layer's whole list.

`)
}

func autoModeClient() *client.Client {
	return client.New(config.SocketPath())
}

func autoModeFail(verb string, err error) {
	fmt.Fprintf(os.Stderr, "automode %s: %v\n", verb, err)
	os.Exit(1)
}

func runAutoModeShow(args []string) {
	asJSON := hasFlag(args, "--json")
	result, err := autoModeClient().AutoModeShow()
	if err != nil {
		autoModeFail("show", err)
	}
	if result == nil {
		autoModeFail("show", fmt.Errorf("daemon returned no result"))
	}
	if asJSON {
		writeJSON(result)
		return
	}
	cfg := result.Config
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "enabled by default\t%t\n", cfg.EnabledDefault)
	w.Flush()
	printAutoModeList("classifier models", cfg.ClassifierModels)
	printAutoModeList("escalation models", cfg.EscalationModels)
	printAutoModeList("environment", cfg.Environment)
	printAutoModeList("allow", cfg.Allow)
	printAutoModeList("hard deny", cfg.HardDeny)
	if len(result.Proposals) == 0 {
		fmt.Println("\npending proposals: none")
		return
	}
	fmt.Printf("\npending proposals (promote them in the attn app):\n")
	pw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for _, p := range result.Proposals {
		fmt.Fprintf(pw, "  %d\t%s\t%s\t%s\n", p.ID, p.Kind, autoModeProposalSubject(p), p.CreatedAt)
	}
	pw.Flush()
}

func printAutoModeList(label string, values []string) {
	if len(values) == 0 {
		fmt.Printf("\n%s: none\n", label)
		return
	}
	fmt.Printf("\n%s:\n", label)
	for i, value := range values {
		fmt.Printf("  %d  %s\n", i, value)
	}
}

func autoModeProposalSubject(p protocol.AutoModeProposalInfo) string {
	if p.Target != "" {
		return p.Target + " " + p.Value
	}
	return p.Value
}

func runAutoModeEnv(args []string) {
	if len(args) == 0 || strings.HasPrefix(args[0], "--") {
		runAutoModeEnvList(args)
		return
	}
	switch args[0] {
	case "add":
		runAutoModeEnvAdd(args[1:])
	case "remove":
		runAutoModeEnvRemove(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "automode env: unknown command %q (want add or remove)\n", args[0])
		os.Exit(2)
	}
}

func runAutoModeEnvList(args []string) {
	asJSON := hasFlag(args, "--json")
	result, err := autoModeClient().AutoModeShow()
	if err != nil {
		autoModeFail("env", err)
	}
	if result == nil {
		autoModeFail("env", fmt.Errorf("daemon returned no result"))
	}
	if asJSON {
		writeJSON(protocol.AutoModeEnvResult{Environment: result.Config.Environment})
		return
	}
	if len(result.Config.Environment) == 0 {
		fmt.Println("no environment entries")
		return
	}
	for i, entry := range result.Config.Environment {
		fmt.Printf("%d  %s\n", i, entry)
	}
}

func runAutoModeEnvAdd(args []string) {
	asJSON := hasFlag(args, "--json")
	text := strings.TrimSpace(strings.Join(stripFlags(args), " "))
	if text == "" {
		fmt.Fprintln(os.Stderr, "automode env add: needs the prose to add")
		os.Exit(2)
	}
	result, err := autoModeClient().AutoModeEnvAdd(text)
	if err != nil {
		autoModeFail("env add", err)
	}
	printAutoModeEnvResult(result, asJSON)
}

func runAutoModeEnvRemove(args []string) {
	asJSON := hasFlag(args, "--json")
	rest := stripFlags(args)
	if len(rest) != 1 {
		fmt.Fprintln(os.Stderr, "automode env remove: needs one index (see `attn automode env`)")
		os.Exit(2)
	}
	index, err := strconv.Atoi(rest[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "automode env remove: %q is not an index\n", rest[0])
		os.Exit(2)
	}
	result, err := autoModeClient().AutoModeEnvRemove(index)
	if err != nil {
		autoModeFail("env remove", err)
	}
	printAutoModeEnvResult(result, asJSON)
}

func printAutoModeEnvResult(result *protocol.AutoModeEnvResult, asJSON bool) {
	if result == nil {
		autoModeFail("env", fmt.Errorf("daemon returned no result"))
	}
	if asJSON {
		writeJSON(result)
		return
	}
	if len(result.Environment) == 0 {
		fmt.Println("no environment entries")
		return
	}
	for i, entry := range result.Environment {
		fmt.Printf("%d  %s\n", i, entry)
	}
}

func runAutoModeModel(args []string) {
	rest := stripFlags(args)
	if len(rest) < 1 {
		fmt.Fprintln(os.Stderr, "automode model: needs classifier or escalation")
		os.Exit(2)
	}
	target := rest[0]
	if target != automode.TargetClassifier && target != automode.TargetEscalation {
		fmt.Fprintf(os.Stderr, "automode model: target must be %s or %s, got %q\n",
			automode.TargetClassifier, automode.TargetEscalation, target)
		os.Exit(2)
	}

	models, err := automode.ParseModelList(strings.Join(rest[1:], automode.ModelListSeparator))
	if err != nil {
		fmt.Fprintf(os.Stderr, "automode model %s: %v\n", target, err)
		os.Exit(2)
	}
	proposeAutoMode("model "+target, automode.KindModel, target,
		automode.FormatModelList(models), hasFlag(args, "--json"))
}

func runAutoModePropose(verb, kind, target string, args []string) {
	proposeAutoMode(verb, kind, target, strings.Join(stripFlags(args), " "), hasFlag(args, "--json"))
}

func proposeAutoMode(verb, kind, target, value string, asJSON bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		fmt.Fprintf(os.Stderr, "automode %s: needs a value\n", verb)
		os.Exit(2)
	}
	result, err := autoModeClient().AutoModePropose(kind, target, value, autoModeProposer())
	if err != nil {
		autoModeFail(verb, err)
	}
	if result == nil {
		autoModeFail(verb, fmt.Errorf("daemon returned no result"))
	}
	if asJSON {
		writeJSON(result)
		return
	}
	fmt.Printf("recorded proposal %d: %s %s\n", result.Proposal.ID, result.Proposal.Kind,
		autoModeProposalSubject(result.Proposal))
	fmt.Println("This changed nothing yet. Promote it in the attn app to put it in force.")
}

func autoModeProposer() string {
	return strings.TrimSpace(os.Getenv("ATTN_SESSION_ID"))
}

func runAutoModeDenials(args []string) {
	asJSON := hasFlag(args, "--json")
	limit := 0
	rest := stripFlags(args)
	if value, ok := takeStringFlag(args, "--limit"); ok {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed <= 0 {
			fmt.Fprintf(os.Stderr, "automode denials: --limit wants a positive number, got %q\n", value)
			os.Exit(2)
		}
		limit = parsed
	}
	if len(rest) > 0 {
		fmt.Fprintf(os.Stderr, "automode denials: unexpected argument %q\n", rest[0])
		os.Exit(2)
	}
	result, err := autoModeClient().AutoModeDenials(limit)
	if err != nil {
		autoModeFail("denials", err)
	}
	if result == nil {
		autoModeFail("denials", fmt.Errorf("daemon returned no result"))
	}
	if asJSON {
		writeJSON(result)
		return
	}
	writeAutoModeDenials(os.Stdout, result.Denials, protocol.Deref(result.LedgerNote))
}

func writeAutoModeDenials(out io.Writer, denials []protocol.AutoModeDenialInfo, ledgerNote string) {
	if len(denials) == 0 {
		fmt.Fprintln(out, "no denials recorded")
	} else {
		w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		for _, denial := range denials {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
				denial.CreatedAt, denial.SessionID, denial.Rule, denial.Signature, denial.Reason)
		}
		w.Flush()
	}
	if ledgerNote != "" {
		fmt.Fprintf(out, "note: %s\n", ledgerNote)
	}
}

func stripFlags(args []string) []string {
	out := []string{}
	for i := 0; i < len(args); i++ {
		if !strings.HasPrefix(args[i], "--") {
			out = append(out, args[i])
			continue
		}
		if autoModeFlagTakesValue(args[i]) && i+1 < len(args) {
			i++
		}
	}
	return out
}

func takeStringFlag(args []string, flag string) (string, bool) {
	for i, arg := range args {
		if arg == flag && i+1 < len(args) {
			return args[i+1], true
		}
		if strings.HasPrefix(arg, flag+"=") {
			return strings.TrimPrefix(arg, flag+"="), true
		}
	}
	return "", false
}

func autoModeFlagTakesValue(flag string) bool {
	return flag == "--limit"
}
