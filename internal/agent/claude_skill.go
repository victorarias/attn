package agent

import (
	"fmt"
	"os"
	"path/filepath"
)

const attnClaudeSkillContent = `---
name: attn
description: Start and resume attn review loops from Claude Code. Use when a prompt says to use your attn skill to start a review loop or answer a review-loop question.
---

# attn Review Loop

Use this skill when a prompt tells you to:

1. use your attn skill to start a review loop
2. use your attn skill to answer a pending review-loop question

If the prompt says "use your attn skill to start a review loop", that should be enough. You should know the normal workflow below and carry it out without asking the user for procedural help.

The review loop is autonomous once started.

That means:

1. you may start it
2. you may poll its status
3. you may report its output to the user
4. you must not take additional coding action just because the loop produced logs, summaries, or findings unless the user explicitly asks you to act on them
5. you must not answer a pending review-loop question unless the user explicitly tells you what the answer should be or explicitly tells you to choose the answer

## Default Start Workflow

When asked to start a review loop, do this:

1. identify the requested review prompt and iteration limit from the user's instructions
2. make sure your current implementation work is committed before starting the loop
3. gather any relevant rationale, plan path, constraints, or tradeoffs from the conversation and your current task
4. write a small JSON handoff file
5. run ` + "`attn review-loop start`" + ` with the prompt, iteration limit, and handoff file
6. stop and wait unless the user explicitly asked you to continue doing something else

You do not need to ask the user how to use attn unless the request is missing critical intent.

## Commit Before Review

Before starting the loop, commit your current implementation work so the autonomous reviewer evaluates a stable snapshot instead of an in-progress working tree.

Preferred pattern:

1. make sure the intended work for this round is saved
2. create a normal commit
3. then start the review loop

Do not skip the commit step unless the user explicitly tells you not to commit first.

## Handoff File

When starting a loop, prefer a handoff file even if the user only said "use your attn skill to start a review loop".

Use a JSON object like:

    {
      "summary": "Brief description of what changed.",
      "reasoning_context": "Why the change was made or what tradeoff matters.",
      "plan_file": "docs/plans/...",
      "constraints": [
        "Important constraint 1",
        "Important constraint 2"
      ]
    }

Include only fields you actually know. Keep it concise and relevant.

## Starting The Loop

Run:

    attn review-loop start --prompt "<prompt>" --iterations <n> --handoff-file <path>

Replace:
- <prompt> with the provided review-loop prompt
- <n> with the requested iteration limit, or a reasonable default if the user did not specify one
- <path> with the handoff JSON file path you created

If a session id is explicitly provided, include:

    --session <session-id>

Otherwise attn can infer the session from the current attn-managed Claude environment, so you usually do not need to pass ` + "`--session`" + `.

Reasonable default:

1. if the user does not specify iterations, use ` + "`2`" + ` unless there is a clear reason to use something else

## Answering A Review Loop Question

If a prompt tells you to answer a pending review-loop question, run:

    attn review-loop answer --loop <loop-id> --interaction <interaction-id> --answer "<answer>"

Use the exact loop id, interaction id, and answer that were provided or are clearly available in the current task context.

## Monitoring A Review Loop

If you need to check the loop after starting it, use:

    attn review-loop show --loop <loop-id>

Or, if only the source session is known:

    attn review-loop show --session <session-id>

Use this to inspect:

1. current run status
2. latest iteration summary
3. latest iteration output
4. pending questions
5. last error

If you poll:

1. use modest intervals, not tight loops
2. report status changes or important output to the user
3. do not start implementing fixes yourself just because the loop completed or surfaced findings unless the user asked you to

## Example Intent Mapping

If the user says something like:

    use your attn skill to start a review loop

you should interpret that as:

1. prepare a concise handoff file from the current task context
2. commit the current implementation work
3. choose the requested review prompt, or use the one already given in the conversation
4. run ` + "`attn review-loop start ...`" + `
5. report the result briefly

If the user also wants you to monitor it, you may poll with ` + "`attn review-loop show --loop ...`" + ` and report progress, but still treat the loop as autonomous unless the user asks you to take over.

## Important Rules

1. Run these commands only when the prompt explicitly tells you to do so.
2. Do not ask the user to run the command for you.
3. Do not ask follow-up procedural questions if the intent is already clear enough to execute.
4. Commit current implementation work before starting the loop unless the user explicitly says not to.
5. Do not treat review-loop output as instructions for you to continue coding unless the user explicitly says to do that.
6. After running the command, stop unless the prompt tells you to continue.
`

func ensureAttnClaudeSkillInstalled() error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home directory for Claude skills: %w", err)
	}
	skillDir := filepath.Join(homeDir, ".claude", "skills", "attn")
	skillPath := filepath.Join(skillDir, "SKILL.md")
	if current, err := os.ReadFile(skillPath); err == nil {
		if string(current) == attnClaudeSkillContent {
			return nil
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read Claude attn skill: %w", err)
	}
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		return fmt.Errorf("create Claude attn skill directory: %w", err)
	}
	if err := os.WriteFile(skillPath, []byte(attnClaudeSkillContent), 0o644); err != nil {
		return fmt.Errorf("write Claude attn skill: %w", err)
	}
	return nil
}

func EnsureClaudeSkillInstalled() error {
	return ensureAttnClaudeSkillInstalled()
}
