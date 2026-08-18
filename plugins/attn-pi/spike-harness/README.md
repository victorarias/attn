# pi SDK spike harness — the pin-bump gate

Seven scenarios that drive pi's SDK directly, without attn. They are the evidence
behind the headless host's design, and they are the gate a pi version bump has
to pass: run them against the new pin, diff the receipts against the ones
recorded in `docs/grounding/pi-plugins.md` and
`docs/plans/2026-08-05-pi-headless-host.md`, and treat any change as a breaking
change until proven otherwise. pi ships breaking changes every few releases and
has no compat gate — an extension built against old types loads silently and
fails at the first missing call site — so nothing else tells you what moved.

Run them from a checkout, never from a packaged app:

```bash
cd plugins/attn-pi/spike-harness
bun install
bun s1-smoke.js
```

| Scenario | What it pins down |
| --- | --- |
| `s1-smoke.js` | `createAgentSession` + `bindExtensions`, where the session file lands and when, event ordering around `agent_end`/`agent_settled` |
| `s2-delta-rate.js` | assistant delta rate — the receipt behind the host's ~30 ms coalescing window |
| `s3-steer.js` | steering a live run (slice 2) |
| `s4-abort.js` | aborting a run mid-flight |
| `s5-child.js` | what happens to pi's tool subprocesses |
| `s5-crash-revive.js`, `s5b-crash-revive.js` | the orphan-on-hard-kill receipt: killing the host strands the tool subprocesses, which is why attn's daemon owns the host's process group and kills the group |
| `s6-memory-slope.js` | memory over a long session |
| `s7-classifier-receipt.js` | auto-mode classifier latency/cost/quality across candidate models (receipt behind the auto-mode plan's model defaults) |

Each scenario writes JSONL to `logs/` and pi session files to `sessions/`, both
gitignored: the scripts are the gate, what they produce is per-run. Session
storage is redirected under this directory and asserted to be — the harness
refuses to run if it would write into `~/.pi/agent/sessions`. The real
`~/.pi/agent` is still read, for auth and resource discovery, exactly the way a
bare `pi` invocation reads it.

The model is pinned in `common.js` and costs real money. Keep the scenarios
short.
