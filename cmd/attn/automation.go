package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/client"
)

func automationUsage() {
	fmt.Fprint(os.Stderr, "usage: attn automation <apply|validate|list|show|run|runs|delete|cleanup>\n")
}
func runAutomationCommand() {
	if len(os.Args) < 3 {
		automationUsage()
		os.Exit(2)
	}
	c := client.New(client.DefaultSocketPath())
	var data json.RawMessage
	var err error
	switch os.Args[2] {
	case "apply":
		fs := flag.NewFlagSet("automation apply", flag.ContinueOnError)
		file := fs.String("file", "", "definition YAML")
		if e := fs.Parse(os.Args[3:]); e != nil {
			os.Exit(2)
		}
		if *file == "" {
			err = fmt.Errorf("--file is required")
			break
		}
		raw, e := os.ReadFile(*file)
		if e != nil {
			err = e
			break
		}
		data, err = c.AutomationApply(string(raw))
	case "validate":
		fs := flag.NewFlagSet("automation validate", flag.ContinueOnError)
		file := fs.String("file", "", "definition YAML")
		if e := fs.Parse(os.Args[3:]); e != nil {
			os.Exit(2)
		}
		if *file == "" {
			err = fmt.Errorf("--file is required")
			break
		}
		raw, e := os.ReadFile(*file)
		if e != nil {
			err = e
			break
		}
		data, err = c.AutomationValidate(string(raw))
		if err == nil && data == nil {
			data, _ = json.Marshal(map[string]bool{"valid": true})
		}
	case "list":
		data, err = c.AutomationList()
	case "show":
		if len(os.Args) != 4 {
			err = fmt.Errorf("usage: attn automation show <definition-id>")
			break
		}
		data, err = c.AutomationShow(os.Args[3])
	case "run":
		if len(os.Args) < 4 {
			err = fmt.Errorf("usage: attn automation run <definition-id> [--input-file <file> | --pr-url <url>] [--request-id <id>]")
			break
		}
		definitionID := os.Args[3]
		fs := flag.NewFlagSet("automation run", flag.ContinueOnError)
		inputFile := fs.String("input-file", "", "structured occurrence JSON")
		prURL := fs.String("pr-url", "", "resolve one GitHub pull request into structured occurrence input")
		requestID := fs.String("request-id", "", "stable idempotency key to reuse when retrying an uncertain request")
		if e := fs.Parse(os.Args[4:]); e != nil {
			os.Exit(2)
		}
		if len(fs.Args()) != 0 {
			err = fmt.Errorf("usage: attn automation run <definition-id> [--input-file <file> | --pr-url <url>] [--request-id <id>]")
			break
		}
		if *inputFile != "" && *prURL != "" {
			err = fmt.Errorf("--input-file and --pr-url are mutually exclusive")
			break
		}
		input := "{}"
		if *inputFile != "" {
			raw, e := os.ReadFile(*inputFile)
			if e != nil {
				err = e
				break
			}
			input = string(raw)
		}
		if *requestID == "" {
			*requestID = uuid.NewString()
		}
		if *prURL != "" {
			data, err = c.AutomationRunPullRequest(definitionID, *requestID, *prURL)
		} else {
			data, err = c.AutomationRun(definitionID, *requestID, input)
		}
	case "runs":
		if len(os.Args) != 4 {
			err = fmt.Errorf("usage: attn automation runs <definition-id>")
			break
		}
		data, err = c.AutomationRuns(os.Args[3])
	case "delete":
		if len(os.Args) != 4 {
			err = fmt.Errorf("usage: attn automation delete <definition-id>")
			break
		}
		if err = c.AutomationDelete(os.Args[3]); err == nil {
			data, _ = json.Marshal(map[string]string{"deleted": os.Args[3]})
		}
	case "cleanup":
		if len(os.Args) != 4 {
			err = fmt.Errorf("usage: attn automation cleanup <definition-id>")
			break
		}
		data, err = c.AutomationCleanup(os.Args[3])
	default:
		err = fmt.Errorf("unknown automation command %q", os.Args[2])
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "automation: %v\n", err)
		os.Exit(1)
	}
	var pretty any
	if json.Unmarshal(data, &pretty) == nil {
		encoded, _ := json.MarshalIndent(pretty, "", "  ")
		fmt.Println(string(encoded))
		return
	}
	fmt.Println(string(data))
}
