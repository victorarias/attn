package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/store"
)

// `attn bus` is the operator surface for the durable event bus: what the log
// holds, who is reading it, how far behind each reader is, and the kill switch.
//
// It reads and writes the profile database directly rather than going through
// the daemon's IPC protocol. That is deliberate on both counts. Status is a
// diagnostic and does not deserve a protocol version bump; and the enabled bit is
// database-only BY DESIGN — a consumer is killed by flipping a row, and the
// daemon re-reads that bit on every delivery cycle, so the switch works whether
// or not the daemon is listening. (The automations discipline: a kill switch that
// depends on the thing it is killing is not a kill switch.)
//
// Live and Stalled are in-process facts and therefore absent here; the daemon log
// carries stalls.
func runBus() {
	if len(os.Args) < 3 || os.Args[2] == "-h" || os.Args[2] == "--help" {
		writeBusHelp(os.Stdout)
		return
	}
	switch os.Args[2] {
	case "status":
		runBusStatus(os.Args[3:])
	case "enable":
		runBusSetEnabled(os.Args[3:], true)
	case "disable":
		runBusSetEnabled(os.Args[3:], false)
	default:
		fmt.Fprintf(os.Stderr, "bus: unknown command %q\n", os.Args[2])
		writeBusHelp(os.Stderr)
		os.Exit(2)
	}
}

func writeBusHelp(w io.Writer) {
	fmt.Fprint(w, `usage: attn bus <command>

commands:
  status [--json]
        show the event log's live window, how many events it holds and what
        they weigh, and every registered consumer's cursor, filter, enabled
        bit, and lag (head - cursor).

  disable <consumer>
        stop delivering to a consumer. Its cursor is preserved, but a disabled
        consumer no longer holds the retention window open: once trimming
        passes its cursor, re-enabling resumes it at head and logs the gap.

  enable <consumer>
        resume delivery from wherever the consumer's cursor stands.
`)
}

type busStatusJSON struct {
	Earliest int64 `json:"earliest"`
	Head     int64 `json:"head"`
	// Rows and Bytes are what the log actually holds, as opposed to the span of
	// the seq space. They are the receipt for the invariant compaction upholds:
	// the log stays proportional to the data it describes, never to how often
	// that data is written. Bytes counts the event text — name, subject, payload,
	// source, stamp — not the size of the database file, which is shared with
	// every other table and would answer a different question.
	Rows      int64               `json:"rows"`
	Bytes     int64               `json:"bytes"`
	Consumers []busConsumerReport `json:"consumers"`
}

type busConsumerReport struct {
	Name      string `json:"name"`
	Cursor    int64  `json:"cursor"`
	Lag       int64  `json:"lag"`
	Filter    string `json:"filter"`
	Enabled   bool   `json:"enabled"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

func runBusStatus(args []string) {
	asJSON := false
	for _, a := range args {
		switch a {
		case "--json":
			asJSON = true
		case "-h", "--help":
			writeBusHelp(os.Stdout)
			return
		default:
			fmt.Fprintf(os.Stderr, "bus status: unknown flag %q\n", a)
			os.Exit(2)
		}
	}

	s, closeStore := openBusStore()
	defer closeStore()

	earliest, head, err := s.BusBounds()
	if err != nil {
		fmt.Fprintf(os.Stderr, "bus status: reading log bounds: %v\n", err)
		os.Exit(1)
	}
	rows, err := s.ListBusConsumers()
	if err != nil {
		fmt.Fprintf(os.Stderr, "bus status: listing consumers: %v\n", err)
		os.Exit(1)
	}
	logRows, logBytes, err := s.BusLogSize()
	if err != nil {
		fmt.Fprintf(os.Stderr, "bus status: measuring the log: %v\n", err)
		os.Exit(1)
	}

	report := busStatusJSON{Earliest: earliest, Head: head, Rows: logRows, Bytes: logBytes}
	for _, r := range rows {
		lag := head - r.Cursor
		if lag < 0 {
			lag = 0
		}
		entry := busConsumerReport{
			Name: r.Name, Cursor: r.Cursor, Lag: lag,
			Filter: r.Filter, Enabled: r.Enabled,
		}
		if !r.UpdatedAt.IsZero() {
			entry.UpdatedAt = r.UpdatedAt.UTC().Format(time.RFC3339)
		}
		report.Consumers = append(report.Consumers, entry)
	}

	if asJSON {
		out, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "bus status: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(string(out))
		return
	}

	fmt.Printf("log: seq %d..%d, %d event(s) holding %s\n", earliest, head, logRows, humanBytes(logBytes))
	if len(report.Consumers) == 0 {
		fmt.Println("no registered consumers")
		return
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "CONSUMER\tCURSOR\tLAG\tENABLED\tFILTER")
	for _, c := range report.Consumers {
		fmt.Fprintf(tw, "%s\t%d\t%d\t%t\t%s\n", c.Name, c.Cursor, c.Lag, c.Enabled, c.Filter)
	}
	_ = tw.Flush()
}

// humanBytes renders the log's weight at the scale an operator reads it at.
func humanBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

func runBusSetEnabled(args []string, enabled bool) {
	verb := "enable"
	if !enabled {
		verb = "disable"
	}
	if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
		fmt.Fprintf(os.Stderr, "usage: attn bus %s <consumer>\n", verb)
		os.Exit(2)
	}
	name := strings.TrimSpace(args[0])

	s, closeStore := openBusStore()
	defer closeStore()

	if _, ok, err := s.GetBusConsumer(name); err != nil {
		fmt.Fprintf(os.Stderr, "bus %s: %v\n", verb, err)
		os.Exit(1)
	} else if !ok {
		fmt.Fprintf(os.Stderr, "bus %s: no consumer named %q (see `attn bus status`)\n", verb, name)
		os.Exit(1)
	}
	if err := s.SetBusConsumerEnabled(name, enabled, time.Now()); err != nil {
		fmt.Fprintf(os.Stderr, "bus %s: %v\n", verb, err)
		os.Exit(1)
	}
	fmt.Printf("consumer %q %sd\n", name, verb)
}

func openBusStore() (*store.Store, func()) {
	s, err := store.NewWithDB(config.DBPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "bus: opening %s: %v\n", config.DBPath(), err)
		os.Exit(1)
	}
	return s, func() { _ = s.Close() }
}
