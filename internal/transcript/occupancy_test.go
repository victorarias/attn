package transcript

import "testing"

// The captured fixtures are the same redacted Claude Code 2.1.233 and Codex
// 0.147.0 transcripts the usage tests read, so the two quantities are proven
// against one set of real records.

func TestFollowerReadsClaudeContextOccupancy(t *testing.T) {
	follower, err := NewFollower(usageFixture(t, "claude-2.1.233.jsonl"), "claude", 0)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := follower.Read()
	if err != nil {
		t.Fatal(err)
	}

	var seen []int64
	for _, record := range batch.Records {
		if record.Context != nil {
			seen = append(seen, record.Context.Tokens)
		}
	}
	// 2 + 7391 + 4709 on the first message, repeated by its content-block
	// records, then 2 + 262963 + 4111 from the iteration on the second.
	want := []int64{12102, 12102, 12102, 267076}
	if len(seen) != len(want) {
		t.Fatalf("occupancy readings = %v, want %v", seen, want)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("occupancy readings = %v, want %v", seen, want)
		}
	}
	if batch.Context == nil || batch.Context.Tokens != 267076 {
		t.Fatalf("batch occupancy = %+v, want the newest reading", batch.Context)
	}
	if batch.Context.Window != 0 {
		t.Fatalf("claude window = %d, want 0: the transcript never states one", batch.Context.Window)
	}
}

func TestFollowerReadsCodexContextOccupancyAndWindow(t *testing.T) {
	follower, err := NewFollower(usageFixture(t, "codex-0.147.0.jsonl"), "codex", 0)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := follower.Read()
	if err != nil {
		t.Fatal(err)
	}
	if batch.Context == nil {
		t.Fatal("no occupancy read from the codex capture")
	}
	// Codex's input_tokens already includes the cached prefix, so it is the whole
	// prompt, and the record states the window outright.
	if batch.Context.Tokens != 39323 || batch.Context.Window != 258400 {
		t.Fatalf("codex occupancy = %+v", batch.Context)
	}
}

// Occupancy is the LAST request's prompt, never the sum. A message that made
// three requests carries the same context through all of them; summing would
// report a session as full at a third of its real fill.
func TestClaudeOccupancyTakesTheLastIterationNotTheSum(t *testing.T) {
	line := []byte(`{"type":"assistant","message":{"id":"msg_1","usage":{
		"input_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":0,
		"iterations":[
			{"input_tokens":2,"cache_read_input_tokens":100000,"cache_creation_input_tokens":500},
			{"input_tokens":2,"cache_read_input_tokens":100500,"cache_creation_input_tokens":700},
			{"input_tokens":2,"cache_read_input_tokens":101200,"cache_creation_input_tokens":300}
		]}}}`)
	got, ok := ContextOccupancy("claude", line)
	if !ok || got.Tokens != 101502 {
		t.Fatalf("occupancy = %+v ok=%v, want the last iteration's 101502", got, ok)
	}
}

func TestContextOccupancyRejectsRecordsThatSayNothing(t *testing.T) {
	cases := []struct {
		name  string
		agent string
		line  string
	}{
		{"a harness attn cannot read", "copilot", `{"type":"assistant","message":{"usage":{"input_tokens":10}}}`},
		{"not JSON", "claude", `not json at all`},
		{"a user record", "claude", `{"type":"user","message":{"content":"hi"}}`},
		{"an assistant record with no usage", "claude", `{"type":"assistant","message":{"id":"msg_1"}}`},
		{"claude's synthetic zero-token message", "claude", `{"type":"assistant","message":{"id":"msg_1","usage":{"input_tokens":0,"cache_read_input_tokens":0,"cache_creation_input_tokens":0,"output_tokens":0}}}`},
		{"a negative count", "claude", `{"type":"assistant","message":{"id":"msg_1","usage":{"input_tokens":-5,"cache_read_input_tokens":10,"cache_creation_input_tokens":0}}}`},
		{"codex's info:null rate-limit update", "codex", `{"type":"event_msg","payload":{"type":"token_count","info":null}}`},
		{"a codex event that is not token_count", "codex", `{"type":"event_msg","payload":{"type":"agent_message"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got, ok := ContextOccupancy(tc.agent, []byte(tc.line)); ok {
				t.Fatalf("occupancy = %+v, want no reading", got)
			}
		})
	}
}

// A window attn cannot trust is no window rather than a negative budget.
func TestCodexNegativeWindowReadsAsUnstated(t *testing.T) {
	line := []byte(`{"type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":900},"model_context_window":-1}}}`)
	got, ok := ContextOccupancy("codex", line)
	if !ok || got.Tokens != 900 || got.Window != 0 {
		t.Fatalf("occupancy = %+v ok=%v", got, ok)
	}
}

func TestSupportsContextOccupancyTracksSupportsUsage(t *testing.T) {
	for _, agent := range []string{"claude", "Codex", "copilot", "", "opencode"} {
		if SupportsContextOccupancy(agent) != SupportsUsage(agent) {
			t.Fatalf("%q: occupancy support and usage support disagree", agent)
		}
	}
}
