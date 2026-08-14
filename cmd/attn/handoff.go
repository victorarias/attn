package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/protocol"
)

// `attn handoff` is how a crew member ends its day: it files the letter the
// member wrote to its successor, and the day turns over behind it. The prose is
// the member's own — attn names the file, refuses to overwrite one already
// filed, and wakes the successor with that letter as its thread.
//
// It is the member's own verb rather than `attn crew handoff` because the
// member is the only one who can run it: the calling session's binding decides
// whose day is closing, so there is no member to name.

func writeHandoffHelp(w io.Writer) {
	fmt.Fprint(w, `usage: attn handoff -m "<your letter>"

File the letter closing this crew member's day. You write it; attn files it into
~/.attn/crew/<member>/handoffs/ under a UTC-stamped name and never edits the
prose. The line is append-only: a filed letter is never overwritten, so a
correction is a new letter.

Filing ends the day. The moment the letter lands, attn closes this session and
wakes the member's successor, primed by what you just wrote — one motion.

Writing the letter and turning the day over are one motion but two acts, and
only the second one can fail. When it does, the letter stays filed and this day
keeps running: --retry runs the turnover again against the letter already on
disk, without writing a second one.

Whether filing starts the next day or ends this one is attn's call by default,
made from whether the user is around: a day that closes while nobody is there
does not start another one. --sleep and --nap decide it yourself.

A retry is retrying a turnover, so it always starts the next day, whether or not
the user is around — that is what it was asked to do. To end the day instead,
say so: --retry --sleep.

Only the session living a member's day can file one, and only on the home
daemon. The note for whoever tends a piece of work next is the other axis:
attn seed note <id> -m "…" --handoff.

flags:
  -m <text>          the letter; - reads it from stdin
  --retry            turn the day over with the letter already filed; no -m
  --sleep            end the day; do not start the next one
  --nap              start the next day even if the user is away
  --session <id>     the session closing its day (defaults to ATTN_SESSION_ID)
  --json             print the machine result as JSON
`)
}

type handoffArgs struct {
	note    string
	retry   bool
	close   protocol.CrewDayClose
	session string
	json    bool
}

func parseHandoffArgs(args []string, envSession string) (handoffArgs, error) {
	fs := flag.NewFlagSet("handoff", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	message := fs.String("m", "", "the letter; - reads it from stdin")
	retry := fs.Bool("retry", false, "turn the day over with the letter already filed")
	sleep := fs.Bool("sleep", false, "end the day rather than starting the next one")
	nap := fs.Bool("nap", false, "start the next day even if the user is away")
	session := fs.String("session", "", "the session closing its day (defaults to ATTN_SESSION_ID)")
	jsonOut := fs.Bool("json", false, "print the machine result as JSON")
	if err := fs.Parse(args); err != nil {
		return handoffArgs{}, err
	}
	if *sleep && *nap {
		return handoffArgs{}, errors.New("--sleep ends the day and --nap starts the next one; pass one or neither, and neither lets attn decide from whether the user is around")
	}
	if fs.NArg() != 0 {
		return handoffArgs{}, fmt.Errorf("handoff takes its letter in -m, not %q", strings.Join(fs.Args(), " "))
	}
	if *retry && strings.TrimSpace(*message) != "" {
		return handoffArgs{}, errors.New("--retry turns the day over with the letter you already filed, so it takes no -m; drop one of the two")
	}
	if !*retry && strings.TrimSpace(*message) == "" {
		return handoffArgs{}, errors.New(`the letter is the handoff — pass it with -m "<your letter>", or -m - to pipe it in`)
	}
	parsed := handoffArgs{note: *message, retry: *retry, session: strings.TrimSpace(*session), json: *jsonOut}
	switch {
	case *sleep:
		parsed.close = protocol.CrewDayCloseSleep
	case *nap:
		parsed.close = protocol.CrewDayCloseNap
	}
	if parsed.session == "" {
		parsed.session = strings.TrimSpace(envSession)
	}
	return parsed, nil
}

func runHandoff(args []string) {
	parsed, err := parseHandoffArgs(args, os.Getenv("ATTN_SESSION_ID"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "handoff: %v\n", err)
		writeHandoffHelp(os.Stderr)
		os.Exit(2)
	}
	note := parsed.note
	if note == "-" {
		raw, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "handoff: reading stdin: %v\n", err)
			os.Exit(1)
		}
		note = string(raw)
	}
	result, err := client.New("").CrewHandoff(parsed.session, note, parsed.retry, parsed.close)
	if err != nil {
		fmt.Fprintf(os.Stderr, "handoff: %v\n", err)
		os.Exit(1)
	}
	if parsed.json {
		printJSON(result)
		return
	}
	if parsed.retry {
		fmt.Printf("%s's letter was already filed at %s.\n", result.Member, result.Path)
	} else {
		fmt.Printf("%s's letter is filed at %s.\n", result.Member, result.Path)
	}
	if napErr := strings.TrimSpace(protocol.Deref(result.NapError)); napErr != "" {
		// The letter is on disk; only the successor is missing. Say both, and say
		// which is which — this session is still alive and still the member.
		fmt.Fprintf(os.Stderr, "handoff: no successor was woken: %s\nThis day is still running and %s is still bound to it. `attn handoff --retry` turns it over again with the letter above — it is filed, so do not write another.\n", napErr, result.Member)
		os.Exit(1)
	}
	if protocol.Deref(result.Outcome) == protocol.CrewDayCloseSleep {
		fmt.Printf("%s is asleep. Nobody was woken behind you; the sidebar has %[1]s one click from a new day.\n", result.Member)
		return
	}
	fmt.Printf("%s's next day is session %s, waking now. This one ends here.\n",
		result.Member, agentShortID(protocol.Deref(result.SessionID)))
}
