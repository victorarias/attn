package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/victorarias/attn/internal/apps"
	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/protocol"
)

// `attn app` is the whole surface an app has: what is installed and what state
// it is in (`list`, `status`), the lifecycle verbs (`enable`, `disable`,
// `remove`), and the pipeline that produces a version in the first place
// (`new`, `apply`, `rollback`, `dev` — those live in app_build.go).
//
// An app is a manifest-declared automation running in attn's shared runtime: it
// consumes facts from the durable bus as `app:<name>`, keeps its state in the
// document namespace `app/<name>`, and is versioned by the content hash of its
// built artifact. A plugin is a different thing — an integration with its own
// supervised process — and shares no surface with this one.
//
// Everything here goes through the daemon, like `attn doc` and unlike `attn
// bus`: removing an app has to stop a delivery loop before its row goes, and
// flipping the enabled bit publishes a fact the runtime hears. The
// daemon-independent kill switch still exists one level down, on purpose: an
// app's enabled bit IS its bus consumer's, so `attn bus disable app:<name>`
// kills an app straight against the database, whether or not a daemon is up.

func runApp() {
	if len(os.Args) < 3 || os.Args[2] == "-h" || os.Args[2] == "--help" {
		writeAppHelp(os.Stdout)
		return
	}
	args := os.Args[3:]
	switch os.Args[2] {
	case "new":
		runAppNew(args)
	case "apply":
		runAppApply(args)
	case "rollback":
		runAppRollback(args)
	case "dev":
		runAppDev(args)
	case "list":
		runAppList(args)
	case "status":
		runAppStatus(args)
	case "enable":
		runAppSetEnabled(args, true)
	case "disable":
		runAppSetEnabled(args, false)
	case "remove":
		runAppRemove(args)
	case "logs":
		runAppLogs(args)
	case "runtime":
		runAppRuntime(args)
	default:
		fmt.Fprintf(os.Stderr, "app: unknown command %q\n", os.Args[2])
		writeAppHelp(os.Stderr)
		os.Exit(2)
	}
}

func writeAppHelp(w io.Writer) {
	fmt.Fprint(w, `usage: attn app <command>

An app is an automation attn runs for you: it reacts to facts from the event
bus as the consumer app:<name>, and keeps its own documents under app/<name>.

commands:
  new <path> [--name <name>] [--description <text>]
        scaffold an app in <path>: manifest, entrypoint, generated types and an
        AGENTS.md (with a CLAUDE.md symlink) that is a complete brief on its own.
        The name defaults to the directory's. The result applies as-is — nothing
        has to be edited first. attn does not remember where the directory is.

  apply <path> [--json]
        build and install: parse the manifest, regenerate src/generated.ts and
        src/attn-app.d.ts, typecheck, bundle, then record the version and point
        the app at it. It stops at the first failure with nothing installed, and
        it never runs your code — a module that throws at import still applies.

        A version is identified by the content it was built from, so applying
        byte-identical content again is the same version, not a new one.

  rollback <name> [version]
        point the app at a version it already has — the one you name, or with no
        version, the one that was serving immediately before the current one.
        That is a recorded pointer, not the next id down. If a broken version
        was rolled off before the current one was applied, the next id down is
        that broken version; what was serving is the one you kept running.
        Rolling back again returns to where you started, because every move
        records what it replaced.

        Builds nothing: the artifact is still on disk.

  dev <path>
        apply on every change, and print every handler invocation as it runs.
        Shows apply results and build errors too, so one window is the whole
        edit-run-read loop.

  list [--json]
        every registered app: the version it runs, whether it is enabled, and
        how far behind the event log its consumer is.

  status <name> [--json]
        one app in full — its current version, its bus consumer, how many
        versions and invocations it has recorded, and its most recent runs.
        Reports only what exists: an app with no consumer says so rather than
        showing a default.

  enable <name>
        resume delivery to the app, from wherever its consumer's cursor stands.

  disable <name>
        stop delivering facts to the app. Its cursor is preserved, but a
        disabled consumer no longer holds the event log's retention window open:
        once trimming passes its cursor, enabling resumes it at head.

        This flips the app's bus consumer bit, which IS its enabled state. When
        the daemon is not running, attn bus disable app:<name> does the same
        thing straight against the database.

  remove <name>
        uninstall the app: stop and delete its bus consumer, delete its registry
        row. Version history, the invocation log and every document under
        app/<name> survive — deleting your data is a separate act, and this is
        not it.

  logs <name> [--lines N]
        what the app printed. Every app's handlers run in one shared process, so
        its output is tagged per app and this reads the tag back. The name
        "runtime" means the whole log, tags and all — that is where a runtime
        that will not start says why.

  runtime status [--json]
        the shared runtime every app's handlers run in: whether it is up, which
        binary it launches, and how many apps are installed and enabled.

  runtime restart
        kill the running runtime and start a fresh one. Also the way back from
        "parked", which is where a runtime that crash-looped ends up. There is
        one runtime for every app, so this takes no app name.
`)
}

func appClient() *client.Client { return client.New(config.SocketPath()) }

func appFail(verb string, err error) {
	fmt.Fprintf(os.Stderr, "app %s: %v\n", verb, err)
	os.Exit(1)
}

// appName reads the single <name> argument the per-app commands take, and
// validates it here rather than only at the daemon: a typo should not need a
// round trip to be told it is one.
func appName(verb string, args []string) (string, []string) {
	rest := make([]string, 0, len(args))
	name := ""
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			rest = append(rest, a)
			continue
		}
		if name != "" {
			appFail(verb, fmt.Errorf("takes one app name; got %q and %q", name, a))
		}
		name = a
	}
	if name == "" {
		fmt.Fprintf(os.Stderr, "usage: attn app %s <name>\n", verb)
		os.Exit(2)
	}
	if err := apps.ValidateName(name); err != nil {
		appFail(verb, err)
	}
	return name, rest
}

func runAppList(args []string) {
	asJSON := appOutputFlags("list", args)
	result, err := appClient().AppList()
	if err != nil {
		appFail("list", err)
	}
	if asJSON {
		writeJSON(result.Apps)
		return
	}
	if len(result.Apps) == 0 {
		fmt.Println("no apps installed")
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "APP\tVERSION\tENABLED\tLAG\tCONSUMER")
	for _, app := range result.Apps {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			app.Name, appVersionCell(app), appEnabledCell(app), appLagCell(app), appConsumerCell(app))
	}
	w.Flush()
}

// The four cells `list` renders say "none" rather than a blank or a guess: an
// app without a version and an app without a consumer are both real states, and
// an empty column reads as a rendering bug.
func appVersionCell(app protocol.AppSummary) string {
	if app.CurrentVersion == nil {
		return "none"
	}
	return fmt.Sprintf("%d", app.CurrentVersion.ID)
}

func appEnabledCell(app protocol.AppSummary) string {
	if app.Consumer == nil {
		return "no consumer"
	}
	if app.Consumer.Enabled {
		return "yes"
	}
	return "no"
}

func appLagCell(app protocol.AppSummary) string {
	if app.Consumer == nil {
		return "-"
	}
	return fmt.Sprintf("%d", app.Consumer.Lag)
}

func appConsumerCell(app protocol.AppSummary) string {
	if app.Consumer == nil {
		return apps.ConsumerName(app.Name) + " (not registered)"
	}
	return app.Consumer.Name
}

func runAppStatus(args []string) {
	name, rest := appName("status", args)
	asJSON := appOutputFlags("status", rest)
	result, err := appClient().AppStatus(name)
	if err != nil {
		appFail("status", err)
	}
	if asJSON {
		writeJSON(result)
		return
	}
	app := result.App
	fmt.Printf("app %s\n", app.Name)
	if app.CurrentVersion != nil {
		fmt.Printf("  version:    %d (%s)\n", app.CurrentVersion.ID, app.CurrentVersion.ContentHash)
		fmt.Printf("  artifact:   %s\n", app.CurrentVersion.ArtifactPath)
	} else {
		fmt.Printf("  version:    none applied yet\n")
	}
	if app.Consumer != nil {
		state := "disabled"
		if app.Consumer.Enabled {
			state = "enabled"
		}
		filter := app.Consumer.Filter
		if filter == "" {
			filter = "everything"
		}
		fmt.Printf("  consumer:   %s — %s, cursor %d, %d event(s) behind\n",
			app.Consumer.Name, state, app.Consumer.Cursor, app.Consumer.Lag)
		fmt.Printf("  subscribes: %s\n", filter)
	} else {
		// Naming the consumer that is missing is what makes this actionable:
		// `attn bus status` is where the reader looks next.
		fmt.Printf("  consumer:   none — %s is not registered, so no facts are delivered to this app\n",
			apps.ConsumerName(app.Name))
	}
	fmt.Printf("  documents:  %s\n", apps.Namespace(app.Name))
	fmt.Printf("  runtime:    %s\n", appRuntimeCell(result.Runtime))
	if result.Stall != nil {
		// The stall clock is the only thing here that ends with the app being
		// switched off, so it says when, not just that.
		fmt.Printf("  stalled:    on event %d (%s) since %s, %d attempt(s)\n",
			result.Stall.EventSeq, result.Stall.EventName, result.Stall.Since, result.Stall.Attempts)
		fmt.Print(indentBlock("              ", result.Stall.LastError))
		fmt.Printf("              disables itself at %s unless it succeeds first\n", result.Stall.DisablesAt)
	}
	fmt.Printf("  history:    %d version(s), %d invocation(s)\n", result.Versions, result.Invocations)
	if len(result.Recent) == 0 {
		fmt.Println("  recent:     no invocations recorded")
		return
	}
	fmt.Println("  recent:")
	w := tabwriter.NewWriter(os.Stdout, 4, 0, 2, ' ', 0)
	fmt.Fprintln(w, "\tSTARTED\tVERSION\tSEQ\tEVENT\tHANDLER\tSTATUS\tMS\tERROR")
	for _, inv := range result.Recent {
		// One line per invocation, so the error is its first line only — a
		// JavaScript stack pasted into a column destroys the table it is in. The
		// stall block above carries the whole thing for the failure that matters,
		// and `attn app logs <name>` has what the handler printed.
		fmt.Fprintf(w, "\t%s\t%d\t%d\t%s\t%s\t%s\t%d\t%s\n",
			inv.StartedAt, inv.VersionID, inv.EventSeq, inv.EventName, inv.Handler,
			inv.Status, inv.DurationMs, firstErrorLine(inv.Error))
	}
	w.Flush()
}

// firstErrorLine is one row's worth of an error, with a marker when there is
// more. Without the marker a reader has no way to know a stack was cut.
func firstErrorLine(text string) string {
	line, rest, found := strings.Cut(strings.TrimRight(text, "\n"), "\n")
	if found && strings.TrimSpace(rest) != "" {
		return line + " …"
	}
	return line
}

// indentBlock puts prefix in front of every line of text, so a multi-line value
// in an aligned block stays inside its column. A handler's error carries the
// JavaScript stack that threw it, which is the most useful thing on the screen
// and also the only value here that is never one line.
func indentBlock(prefix, text string) string {
	var b strings.Builder
	for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		b.WriteString(prefix)
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

// appRuntimeNeverStarted is what a daemon that has never started a runtime says.
//
// "no *enabled* app", because a disabled app is never due a fact: a daemon whose
// every app is switched off will never start a runtime, and the sentence has to
// read as the settled state it is rather than as something that has not happened
// yet. `attn app runtime status` carries the enabled count in the same answer, so
// a reader who wants the number has it.
const appRuntimeNeverStarted = "not started — no enabled app has been due a fact since this daemon came up"

// appRuntimeCell says where this app's handlers actually run. It is one line
// because `attn app runtime status` is the full picture; what belongs here is
// the answer to "is my app not running because of my app, or because of the
// runtime" — and a parked runtime is the loudest form of the second.
func appRuntimeCell(info *protocol.AppRuntimeInfo) string {
	if info == nil {
		return appRuntimeNeverStarted
	}
	switch {
	case info.Phase == "parked":
		return "PARKED — it crash-looped and attn stopped restarting it, so no app's handlers run. `attn app runtime restart`"
	case info.Connected:
		return fmt.Sprintf("running (%s), generation %d", info.Phase, info.Generation)
	default:
		return fmt.Sprintf("%s — not connected yet", info.Phase)
	}
}

func runAppSetEnabled(args []string, enabled bool) {
	verb := "disable"
	if enabled {
		verb = "enable"
	}
	name, rest := appName(verb, args)
	appOutputFlags(verb, rest)
	result, err := appClient().AppSetEnabled(name, enabled)
	if err != nil {
		appFail(verb, err)
	}
	if enabled {
		fmt.Printf("app %s enabled: %s resumes from its cursor\n", result.Name, result.Consumer)
		return
	}
	fmt.Printf("app %s disabled: %s stops receiving facts and no longer holds the event log open\n",
		result.Name, result.Consumer)
}

func runAppRemove(args []string) {
	name, rest := appName("remove", args)
	appOutputFlags("remove", rest)
	result, err := appClient().AppRemove(name)
	if err != nil {
		appFail("remove", err)
	}
	consumer := "it had no bus consumer"
	if result.ConsumerRemoved {
		consumer = "stopped and deleted its bus consumer " + apps.ConsumerName(result.Name)
	}
	fmt.Printf("removed app %s: %s\n", result.Name, consumer)
	// Saying what survived is the point: removal is not a data-deleting act, and
	// a reader who wanted it to be needs to know it did not happen.
	fmt.Printf("kept: %d version(s), %d invocation(s), and every document under %s\n",
		result.VersionsKept, result.InvocationsKept, result.NamespaceKept)
}

// appOutputFlags reads the flags shared by these commands. There is one, and it
// is rejected loudly where it means nothing rather than being ignored.
func appOutputFlags(verb string, args []string) bool {
	asJSON := false
	for _, a := range args {
		switch a {
		case "--json":
			if verb != "list" && verb != "status" {
				appFail(verb, fmt.Errorf("--json is only for list and status; %s reports what it did", verb))
			}
			asJSON = true
		case "-h", "--help":
			writeAppHelp(os.Stdout)
			os.Exit(0)
		default:
			appFail(verb, fmt.Errorf("unknown flag %q", a))
		}
	}
	return asJSON
}
