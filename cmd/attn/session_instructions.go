package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/daemon"
	"github.com/victorarias/attn/internal/protocol"
)

type sessionInstructionsArgs struct {
	target   string
	question string
	json     bool
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
	default:
		fmt.Fprintf(os.Stderr, "session: unknown command %q\n", os.Args[2])
		writeSessionHelp(os.Stderr)
		os.Exit(2)
	}
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
	fmt.Fprint(w, `usage: attn session instructions <target-session-id> --question <text> [--json]

commands:
  instructions <id> --question <text> [--json]
        answer a free-form question from the target session's bounded Codex
        conversation, returning validated exact excerpts. --json changes only
        presentation. Unclear is a successful answer.
`)
}
