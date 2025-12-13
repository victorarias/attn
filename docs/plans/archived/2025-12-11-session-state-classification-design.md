# Session State Classification Design

## Problem

Currently, sessions have two states: `working` and `waiting`. The `waiting` state doesn't distinguish between:
- Claude asking a question and waiting for user response
- Claude finished work and sitting idle

Users want visual distinction between these states to prioritize which sessions need attention.

## Solution

Introduce three states with async LLM-based classification:

| State | Meaning | Visual Priority |
|-------|---------|-----------------|
| `working` | Claude is actively processing | Low (busy) |
| `waiting_input` | Claude needs user response | High (action needed) |
| `idle` | Claude finished, nothing pending | Low (done) |

## Classification Logic

When the Stop hook fires:

```
1. Check pending todos → if any exist: "waiting_input"
2. Otherwise: classify last assistant message via LLM → "waiting_input" or "idle"
```

## Hook Changes

### UserPromptSubmit (unchanged)
Sets state to `working`.

### Stop (modified)
Currently sends state directly. New behavior sends classification request:

```json
{"cmd": "stop", "id": "<session-id>", "transcript_path": "<path>"}
```

The daemon handles classification asynchronously.

### PostToolUse/TodoWrite (unchanged)
Updates todos list.

## Protocol Changes

### New Command
```go
const CmdStop = "stop"

type StopMessage struct {
    Cmd            string `json:"cmd"`
    ID             string `json:"id"`
    TranscriptPath string `json:"transcript_path"`
}
```

### New States
```go
const (
    StateWorking      = "working"
    StateWaitingInput = "waiting_input"
    StateIdle         = "idle"
)
```

Increment `ProtocolVersion` since state values are changing.

## Daemon Classification Flow

```
1. Receive "stop" message with transcript_path
2. Spawn goroutine for async classification:
   a. Read transcript JSONL file
   b. Extract last assistant message
   c. Take final 500 characters
   d. Check if session has pending todos → "waiting_input"
   e. Otherwise call Claude CLI for classification
   f. Parse response → update session state
   g. Broadcast state change to WebSocket clients
```

## Transcript Parsing

The transcript is a JSONL file at `transcript_path`. Each line is a JSON object. Extract messages where role is "assistant", take the last one, get its text content, slice final 500 chars.

## LLM Classification

### Command
```bash
echo "<prompt>" | claude -p --print
```

### Prompt
```
Analyze this text from an AI assistant and determine if it's waiting for user input.

Reply with exactly one word: WAITING or DONE

WAITING means:
- Asks a question
- Requests clarification
- Offers choices requiring selection
- Asks for confirmation to proceed

DONE means:
- States completion
- Provides information without asking
- Reports results
- No question or request for input

Text to analyze:
"""
{last_500_chars}
"""
```

### Response Parsing
- Contains "WAITING" → `waiting_input`
- Otherwise → `idle`

## Cleanup

Remove all tmux-related code:
- `TmuxTarget` field from `Session` struct
- `Tmux` field from `RegisterMessage`
- Any tmux references in hooks, wrapper, or documentation

## Implementation Order

1. Add new states to protocol, increment version
2. Remove tmux code
3. Update hooks.go to send "stop" command with transcript_path
4. Add transcript parsing to daemon
5. Add LLM classification to daemon (async)
6. Update frontend to handle new states with distinct visuals

## Testing

- Session with pending todos → `waiting_input`
- Claude asks explicit question → LLM returns WAITING → `waiting_input`
- Claude says "Done!" → LLM returns DONE → `idle`
- Classification timeout/error → fallback to `waiting_input` (safer default)
