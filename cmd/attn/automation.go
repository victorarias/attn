package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/protocol"
)

func automationUsage() {
	fmt.Fprint(os.Stderr, "usage: attn automation <apply|validate|list|show|run|runs|enable|disable|delete|cleanup>\n")
}

func runAutomationCommand() {
	if len(os.Args) < 3 {
		automationUsage()
		os.Exit(2)
	}
	c := client.New(client.DefaultSocketPath())
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
		var result *protocol.AutomationApplyResultMessage
		result, err = c.AutomationApply(string(raw))
		if err == nil {
			printJSON(result.Definition)
		}
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
		if err = c.AutomationValidate(string(raw)); err == nil {
			printJSON(map[string]bool{"valid": true})
		}
	case "list":
		var result *protocol.AutomationDefinitionsResultMessage
		result, err = c.AutomationDefinitions()
		if err == nil {
			printJSON(result.Definitions)
		}
	case "show":
		if len(os.Args) != 4 {
			err = fmt.Errorf("usage: attn automation show <definition-id>")
			break
		}
		var result *protocol.AutomationDefinitionResultMessage
		result, err = c.AutomationDefinition(os.Args[3])
		if err == nil && result.SpecYaml != nil {
			fmt.Print(*result.SpecYaml)
		}
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
		var result *protocol.AutomationRunResultMessage
		if *prURL != "" {
			result, err = c.AutomationRunPullRequest(definitionID, *requestID, *prURL)
		} else {
			result, err = c.AutomationRun(definitionID, *requestID, input)
		}
		if err == nil {
			printJSON(result.Run)
		}
	case "runs":
		if len(os.Args) != 4 {
			err = fmt.Errorf("usage: attn automation runs <definition-id>")
			break
		}
		var result *protocol.AutomationRunsResultMessage
		result, err = c.AutomationRuns(os.Args[3])
		if err == nil {
			printJSON(result.Runs)
		}
	case "enable":
		if len(os.Args) != 4 {
			err = fmt.Errorf("usage: attn automation enable <definition-id>")
			break
		}
		var result *protocol.AutomationSetEnabledResultMessage
		result, err = c.AutomationSetEnabled(os.Args[3], true)
		if err == nil {
			printJSON(result.Definition)
		}
	case "disable":
		if len(os.Args) != 4 {
			err = fmt.Errorf("usage: attn automation disable <definition-id>")
			break
		}
		var result *protocol.AutomationSetEnabledResultMessage
		result, err = c.AutomationSetEnabled(os.Args[3], false)
		if err == nil {
			printJSON(result.Definition)
		}
	case "delete":
		if len(os.Args) != 4 {
			err = fmt.Errorf("usage: attn automation delete <definition-id>")
			break
		}
		if err = c.AutomationDelete(os.Args[3]); err == nil {
			printJSON(map[string]string{"deleted": os.Args[3]})
		}
	case "cleanup":
		if len(os.Args) != 4 {
			err = fmt.Errorf("usage: attn automation cleanup <definition-id>")
			break
		}
		var result *protocol.AutomationCleanupResultMessage
		result, err = c.AutomationCleanup(os.Args[3])
		if err == nil {
			printJSON(map[string][]string{
				"cleaned":     result.Cleaned,
				"kept_dirty":  result.KeptDirty,
				"kept_active": result.KeptActive,
			})
		}
	default:
		err = fmt.Errorf("unknown automation command %q", os.Args[2])
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "automation: %v\n", err)
		os.Exit(1)
	}
}
