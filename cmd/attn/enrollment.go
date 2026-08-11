package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/enrollment"
)

// `attn enrollment` is the operator surface for the one relationship a daemon
// has with another daemon: whose home-level state it holds. It reads and writes
// the two files in the profile data dir directly rather than going through the
// daemon's IPC protocol — deliberately, on both counts. The record has to be
// readable and writable when the daemon is not running (a home enrolls a remote
// right after installing the binary, before starting it), and the daemon re-reads
// the file on every ask, so a change here takes effect without a restart.
//
// A home enrolls its outposts over ssh by running `enrollment enroll` there, so
// the refusal wording an outpost produces is what lands in the home's log.
func runEnrollment() {
	if len(os.Args) < 3 {
		runEnrollmentStatus(nil)
		return
	}
	switch os.Args[2] {
	case "-h", "--help", "help":
		writeEnrollmentHelp(os.Stdout)
	case "status":
		runEnrollmentStatus(os.Args[3:])
	case "enroll":
		runEnrollmentEnroll(os.Args[3:])
	case "leave":
		runEnrollmentLeave(os.Args[3:])
	case "--json":
		runEnrollmentStatus(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "enrollment: unknown command %q\n", os.Args[2])
		writeEnrollmentHelp(os.Stderr)
		os.Exit(2)
	}
}

func writeEnrollmentHelp(w io.Writer) {
	fmt.Fprint(w, `usage: attn enrollment <command>

Every daemon has exactly one home. A home daemon owns its garden and its crew;
an outpost is a daemon enrolled to a home and owns none of that — it keeps its
own sessions, and passes home-level asks upward.

commands:
  status [--json]
        show this daemon's id, the home that owns its garden and crew, and
        whether home-level state may live here.

  enroll --home <daemon-id> [--json]
        record <daemon-id> as this daemon's home. A daemon that is its own home
        enrolls; one already enrolled to that same home is unchanged; one
        enrolled to a different home refuses, because re-homing is a decision,
        never a silent overwrite. A home runs this over ssh when it syncs a
        remote, so you rarely type it.

  leave [--json]
        make this daemon its own home again. This is the way out of enrollment,
        and what has to happen here before a different home may take it.
`)
}

func enrollmentDataRoot() string {
	return config.DataDir()
}

func runEnrollmentStatus(args []string) {
	fs := flag.NewFlagSet("enrollment status", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "print the status as JSON")
	_ = fs.Parse(args)

	root := enrollmentDataRoot()
	status, err := enrollment.Load(root)
	if err != nil && *asJSON {
		printJSON(map[string]any{
			"daemon_id": status.DaemonID,
			"error":     err.Error(),
		})
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "enrollment: %v\n", err)
		if errors.Is(err, enrollment.ErrNoRecord) {
			fmt.Fprintf(os.Stderr, "No daemon has started in %s yet; the record is written at startup.\n", root)
		}
		os.Exit(1)
	}

	if *asJSON {
		printJSON(map[string]any{
			"daemon_id":      status.DaemonID,
			"home_daemon_id": status.HomeDaemonID,
			"is_home":        status.IsHome(),
			"enrollment":     status.Describe(),
		})
		return
	}
	writeEnrollmentStatus(os.Stdout, status)
}

// writeEnrollmentStatus shows the relationship and then the fence's own answer,
// so the refusal an outpost gives to a garden or crew command is readable
// before anyone runs one.
func writeEnrollmentStatus(w io.Writer, status enrollment.Status) {
	fmt.Fprintf(w, "daemon:     %s\n", status.DaemonID)
	fmt.Fprintf(w, "enrollment: %s\n", status.Describe())
	if fenceErr := status.RequireHome("the garden and the crew"); fenceErr != nil {
		fmt.Fprintf(w, "garden and crew: refused here\n\n%v\n", fenceErr)
		return
	}
	fmt.Fprintf(w, "garden and crew: this daemon owns them\n")
}

func runEnrollmentEnroll(args []string) {
	fs := flag.NewFlagSet("enrollment enroll", flag.ExitOnError)
	home := fs.String("home", "", "daemon id of the home this daemon belongs to")
	asJSON := fs.Bool("json", false, "print the result as JSON")
	_ = fs.Parse(args)

	if strings.TrimSpace(*home) == "" {
		fmt.Fprintln(os.Stderr, "enrollment enroll: --home <daemon-id> is required")
		os.Exit(2)
	}

	result, err := enrollment.Enroll(enrollmentDataRoot(), strings.TrimSpace(*home))
	var foreign *enrollment.ForeignHomeError
	if errors.As(err, &foreign) {
		// A refusal is a result, not a crash: the home reads it back over ssh and
		// shows the wording to whoever asked for the sync.
		emitEnrollmentResult(os.Stdout, os.Stderr, result, *asJSON)
		os.Exit(enrollmentRefusedExitCode)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "enrollment enroll: %v\n", err)
		os.Exit(1)
	}
	emitEnrollmentResult(os.Stdout, os.Stderr, result, *asJSON)
}

func runEnrollmentLeave(args []string) {
	fs := flag.NewFlagSet("enrollment leave", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "print the result as JSON")
	_ = fs.Parse(args)

	result, err := enrollment.Leave(enrollmentDataRoot())
	if err != nil {
		fmt.Fprintf(os.Stderr, "enrollment leave: %v\n", err)
		if errors.Is(err, enrollment.ErrNoDaemonID) {
			fmt.Fprintf(os.Stderr, "No daemon has started in %s yet, so there is nothing to release.\n", enrollmentDataRoot())
		}
		os.Exit(1)
	}
	emitEnrollmentResult(os.Stdout, os.Stderr, result, *asJSON)
}

// enrollmentRefusedExitCode is the exit code a re-home refusal ends with. The
// hub reads it over ssh to tell "this remote belongs to another home" apart from
// "this remote could not answer" — see internal/hub/bootstrap.go.
const enrollmentRefusedExitCode = 3

// emitEnrollmentResult always puts a refusal's wording on stderr, JSON or not:
// the hub's ssh call keeps stderr as the message it shows the user, and stdout
// as the machine-readable half.
func emitEnrollmentResult(out, errOut io.Writer, result enrollment.Result, asJSON bool) {
	if result.Status == "refused" {
		fmt.Fprintln(errOut, result.Message)
	}
	if asJSON {
		if err := fprintJSON(out, result); err != nil {
			fmt.Fprintf(errOut, "error encoding json: %v\n", err)
		}
		return
	}
	if result.Status != "refused" {
		fmt.Fprintln(out, result.Message)
	}
}
