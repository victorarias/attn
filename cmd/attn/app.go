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
        point the app at a version it already has — the one before the current
        one, or the one you name. Builds nothing: the artifact is still on disk.

  dev <path>
        apply on every change. Shows apply results and build errors.

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
	fmt.Printf("  history:    %d version(s), %d invocation(s)\n", result.Versions, result.Invocations)
	if len(result.Recent) == 0 {
		fmt.Println("  recent:     no invocations recorded")
		return
	}
	fmt.Println("  recent:")
	w := tabwriter.NewWriter(os.Stdout, 4, 0, 2, ' ', 0)
	fmt.Fprintln(w, "\tSTARTED\tVERSION\tSEQ\tEVENT\tHANDLER\tSTATUS\tMS\tERROR")
	for _, inv := range result.Recent {
		fmt.Fprintf(w, "\t%s\t%d\t%d\t%s\t%s\t%s\t%d\t%s\n",
			inv.StartedAt, inv.VersionID, inv.EventSeq, inv.EventName, inv.Handler,
			inv.Status, inv.DurationMs, inv.Error)
	}
	w.Flush()
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
