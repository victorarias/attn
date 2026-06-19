package agent

import (
	"slices"
	"strings"
	"testing"
)

// workflowGuidanceMarker is a phrase unique to the workflow-trigger guidance —
// it appears nowhere in argv, hook commands, or workspace-context guidance, so
// its presence is a reliable signal that the workflow block was injected.
const workflowGuidanceMarker = "hypercode"

func TestClaudeBuildCommand_GatesWorkflowGuidance(t *testing.T) {
	// Disabled: no workflow guidance, and no system prompt at all without a checkout.
	off := (&Claude{}).BuildCommand(SpawnOpts{SessionID: "s", Executable: "claude"})
	if slices.Contains(off.Args, "--append-system-prompt") {
		t.Fatalf("disabled + no checkout still appended a system prompt: %v", off.Args)
	}

	// Enabled without a checkout: the system prompt carries only the workflow guidance.
	on := (&Claude{}).BuildCommand(SpawnOpts{SessionID: "s", Executable: "claude", InjectWorkflowGuidance: true})
	prompt := argvValueAfter(on.Args, "--append-system-prompt")
	if !strings.Contains(prompt, workflowGuidanceMarker) {
		t.Fatalf("enabled launch missing workflow guidance: %q", prompt)
	}

	// Enabled with a checkout: both the workspace context and the workflow guidance ride along.
	both := (&Claude{}).BuildCommand(SpawnOpts{
		SessionID:              "s",
		Executable:             "claude",
		WorkspaceContextPath:   "/tmp/context.md",
		InjectWorkflowGuidance: true,
	})
	bothPrompt := argvValueAfter(both.Args, "--append-system-prompt")
	if !strings.Contains(bothPrompt, "/tmp/context.md") || !strings.Contains(bothPrompt, workflowGuidanceMarker) {
		t.Fatalf("enabled launch with checkout missing one of context/workflow guidance: %q", bothPrompt)
	}

	// A checkout WITHOUT the flag must not leak workflow guidance.
	contextOnly := (&Claude{}).BuildCommand(SpawnOpts{
		SessionID:            "s",
		Executable:           "claude",
		WorkspaceContextPath: "/tmp/context.md",
	})
	if strings.Contains(argvValueAfter(contextOnly.Args, "--append-system-prompt"), workflowGuidanceMarker) {
		t.Fatalf("checkout without the flag leaked workflow guidance: %v", contextOnly.Args)
	}
}

func TestCodexGenerateConfigOverrides_GatesWorkflowGuidance(t *testing.T) {
	off := strings.Join((&Codex{}).GenerateConfigOverrides(SpawnOpts{SessionID: "s"}), "\n")
	if strings.Contains(off, workflowGuidanceMarker) {
		t.Fatalf("disabled codex overrides leaked workflow guidance: %q", off)
	}

	on := strings.Join((&Codex{}).GenerateConfigOverrides(SpawnOpts{SessionID: "s", InjectWorkflowGuidance: true}), "\n")
	if !strings.Contains(on, "developer_instructions=") || !strings.Contains(on, workflowGuidanceMarker) {
		t.Fatalf("enabled codex overrides missing workflow guidance: %q", on)
	}
}

// TestHeadlessSubagentArgvCarriesNoWorkflowGuidance locks the structural
// nested-workflow suppression: workflow subagents spawn through the headless
// argv builders, which have no path to the workflow-trigger guidance (only
// BuildCommand / GenerateConfigOverrides inject it). So even a fully-featured,
// writable workflow-subagent request must never carry the guidance — if a
// future change routes guidance through the headless path, this fails loudly.
func TestHeadlessSubagentArgvCarriesNoWorkflowGuidance(t *testing.T) {
	req := HeadlessTaskRequest{
		Model:            "gpt-test",
		Prompt:           "do the work",
		WorkDir:          "/tmp/work",
		CWD:              "/tmp/tree",
		Sandbox:          "workspace-write",
		MCPServerName:    "attn_workflow_result",
		MCPServerCommand: "/tmp/attn",
		ToolName:         "return_result",
		MCPServerArgs:    []string{"_workflow-result-mcp", "--result-file", "/tmp/result"},
	}

	codexArgv := buildCodexHeadlessArgs(req, "/tmp/work/last.txt")
	claudeArgv, err := buildClaudeHeadlessArgs(req)
	if err != nil {
		t.Fatalf("buildClaudeHeadlessArgs: %v", err)
	}

	for name, argv := range map[string][]string{"codex": codexArgv, "claude": claudeArgv} {
		joined := strings.Join(argv, "\x00")
		if strings.Contains(joined, workflowGuidanceMarker) {
			t.Fatalf("%s headless argv carried workflow guidance: %v", name, argv)
		}
		// The launch-only injection seams must never appear on the headless path.
		if slices.Contains(argv, "--append-system-prompt") {
			t.Fatalf("%s headless argv used --append-system-prompt: %v", name, argv)
		}
		if strings.Contains(joined, "developer_instructions=") {
			t.Fatalf("%s headless argv injected developer_instructions: %v", name, argv)
		}
	}
}
