package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/daemon"
	"github.com/victorarias/attn/internal/protocol"
)

type sessionInstructionsArgs struct {
	target   string
	question string
	json     bool
}

type sessionTranscriptArgs struct {
	target string
	after  string
	follow bool
	json   bool
}

func runSession() {
	if len(os.Args) < 3 || os.Args[2] == "-h" || os.Args[2] == "--help" {
		writeSessionHelp(os.Stdout)
		return
	}
	switch os.Args[2] {
	case "instructions":
		if hasHelpFlag(os.Args[3:]) {
			writeSessionHelp(os.Stdout)
			return
		}
		runSessionInstructions(os.Args[3:])
	case "transcript":
		if hasHelpFlag(os.Args[3:]) {
			writeSessionHelp(os.Stdout)
			return
		}
		runSessionTranscript(os.Args[3:])
	default:
		fmt.Fprintf(os.Stderr, "session: unknown command %q\n", os.Args[2])
		writeSessionHelp(os.Stderr)
		os.Exit(2)
	}
}

func parseSessionTranscriptArgs(args []string) (sessionTranscriptArgs, error) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return sessionTranscriptArgs{}, errors.New("exactly one target session id is required")
	}
	target := strings.TrimSpace(args[0])
	args = args[1:]
	fs := flag.NewFlagSet("session transcript", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	after := fs.String("after", "", "resume strictly after an opaque transcript cursor")
	follow := fs.Bool("follow", false, "keep polling for new transcript events until interrupted")
	jsonOut := fs.Bool("json", false, "print one transcript event as JSON per line")
	if err := fs.Parse(args); err != nil {
		return sessionTranscriptArgs{}, err
	}
	if target == "" || fs.NArg() != 0 {
		return sessionTranscriptArgs{}, errors.New("exactly one target session id is required")
	}
	return sessionTranscriptArgs{target: target, after: strings.TrimSpace(*after), follow: *follow, json: *jsonOut}, nil
}

func runSessionTranscript(args []string) {
	parsed, err := parseSessionTranscriptArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "session transcript: %v\n", err)
		writeSessionHelp(os.Stderr)
		os.Exit(2)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	cursor, err := streamSessionTranscript(ctx, os.Stdout, parsed, client.New("").SessionTranscript)
	if cursor != "" {
		fmt.Fprintf(os.Stderr, "cursor: %s\n", cursor)
	}
	if err == nil || errors.Is(err, context.Canceled) {
		return
	}
	code := sessionTranscriptErrorCode(err)
	fmt.Fprintln(os.Stderr, daemon.SessionTranscriptErrorMessage(code))
	os.Exit(1)
}

type sessionTranscriptFetcher func(string, string) (*protocol.SessionTranscriptResult, error)

func streamSessionTranscript(ctx context.Context, w io.Writer, args sessionTranscriptArgs, fetch sessionTranscriptFetcher) (string, error) {
	cursor := args.after
	for {
		result, err := fetch(args.target, cursor)
		if err != nil {
			if args.follow && sessionTranscriptErrorCode(err) == "transcript_unavailable" {
				if err := waitForTranscriptPoll(ctx); err != nil {
					return cursor, err
				}
				continue
			}
			return cursor, err
		}
		for _, event := range result.Events {
			if args.json {
				if err := json.NewEncoder(w).Encode(event); err != nil {
					return cursor, err
				}
			} else {
				printSessionTranscriptEvent(w, event)
			}
		}
		cursor = result.NextCursor

		if !result.AtEnd {
			continue
		}
		if !args.follow {
			return cursor, nil
		}
		if err := waitForTranscriptPoll(ctx); err != nil {
			return cursor, err
		}
	}
}

func waitForTranscriptPoll(ctx context.Context) error {
	timer := time.NewTimer(500 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func printSessionTranscriptEvent(w io.Writer, event protocol.SessionTranscriptEvent) {
	timestamp := "-"
	if event.Timestamp != nil && strings.TrimSpace(*event.Timestamp) != "" {
		timestamp = strings.TrimSpace(*event.Timestamp)
	}
	label := strings.ToUpper(event.Kind)
	if event.IsError != nil && *event.IsError && event.Kind != "error" {
		label += " ERROR"
	}
	fmt.Fprintf(w, "%s  %-12s", timestamp, label)
	if event.ToolName != nil && strings.TrimSpace(*event.ToolName) != "" {
		fmt.Fprintf(w, " %s", strings.TrimSpace(*event.ToolName))
	}
	if event.ToolCallID != nil && strings.TrimSpace(*event.ToolCallID) != "" {
		fmt.Fprintf(w, " (%s)", strings.TrimSpace(*event.ToolCallID))
	}
	fmt.Fprintln(w)
	if event.Text != nil && strings.TrimSpace(*event.Text) != "" {
		for _, line := range strings.Split(strings.TrimSpace(*event.Text), "\n") {
			fmt.Fprintf(w, "  %s\n", line)
		}
	}
}

func sessionTranscriptErrorCode(err error) string {
	const prefix = "daemon error: "
	message := strings.TrimSpace(err.Error())
	if strings.HasPrefix(message, prefix) {
		code := strings.TrimSpace(strings.TrimPrefix(message, prefix))
		switch code {
		case "session_not_found", "transcript_unavailable", "invalid_cursor", "cursor_mismatch", "cursor_past_end":
			return code
		}
	}
	return "transcript_unavailable"
}

func parseSessionInstructionsArgs(args []string) (sessionInstructionsArgs, error) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return sessionInstructionsArgs{}, errors.New("exactly one target session id is required")
	}
	target := strings.TrimSpace(args[0])
	args = args[1:]
	fs := flag.NewFlagSet("session instructions", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	question := fs.String("question", "", "question to answer from the target conversation")
	jsonOut := fs.Bool("json", false, "print the machine result as JSON")
	if err := fs.Parse(args); err != nil {
		return sessionInstructionsArgs{}, err
	}
	if target == "" || fs.NArg() != 0 {
		return sessionInstructionsArgs{}, errors.New("exactly one target session id is required")
	}
	if strings.TrimSpace(*question) == "" {
		return sessionInstructionsArgs{}, errors.New("--question is required")
	}
	return sessionInstructionsArgs{target: target, question: strings.TrimSpace(*question), json: *jsonOut}, nil
}

func runSessionInstructions(args []string) {
	parsed, err := parseSessionInstructionsArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "session instructions: %v\n", err)
		writeSessionHelp(os.Stderr)
		os.Exit(2)
	}
	result, err := client.New("").SessionInstructions(parsed.target, parsed.question)
	if err != nil {
		code := sessionInstructionsErrorCode(err)
		message := daemon.SessionInstructionsErrorMessage(code)
		if parsed.json {
			_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"error": map[string]string{"code": code, "message": message}})
		} else {
			fmt.Fprintln(os.Stderr, message)
		}
		os.Exit(1)
	}
	if parsed.json {
		printJSON(result)
		return
	}
	printSessionInstructions(result)
}

func sessionInstructionsErrorCode(err error) string {
	const prefix = "daemon error: "
	message := strings.TrimSpace(err.Error())
	if strings.HasPrefix(message, prefix) {
		code := strings.TrimSpace(strings.TrimPrefix(message, prefix))
		switch code {
		case "session_not_found", "transcript_unavailable", "conversation_too_large", "model_unavailable", "invalid_response", "invalid_evidence":
			return code
		}
	}
	return "model_unavailable"
}

func printSessionInstructions(result *protocol.SessionInstructionsResult) {
	printSessionInstructionsTo(os.Stdout, result)
}

func printSessionInstructionsTo(w io.Writer, result *protocol.SessionInstructionsResult) {
	fmt.Fprintln(w, result.Answer)
	for _, evidence := range result.Evidence {
		fmt.Fprintf(w, "\n%s", evidence.Author)
		if evidence.Timestamp != nil && strings.TrimSpace(*evidence.Timestamp) != "" {
			fmt.Fprintf(w, " — %s", *evidence.Timestamp)
		}
		fmt.Fprintf(w, "\n  %q\n", evidence.Quote)
	}
	fmt.Fprintf(w, "\nTranscript: %s\n", result.TranscriptPath)
}

func writeSessionHelp(w io.Writer) {
	fmt.Fprint(w, `usage: attn session <command>

commands:
  instructions <id> --question <text> [--json]
        answer a free-form question from the target session's bounded Codex
        conversation, returning validated exact excerpts. --json changes only
        presentation. Unclear is a successful answer.
  transcript <id> [--after <cursor>] [--follow] [--json]
        read provider-neutral, timestamped, redacted conversation and tool
        events. --after resumes strictly after a prior cursor; --follow polls
        until interrupted; --json emits one event per line.
`)
}
