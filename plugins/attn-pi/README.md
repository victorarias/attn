# attn-pi

attn driver plugin for [pi](https://github.com/earendil-works/pi): pi launches,
resumes, and lives as an attn session. Pure driver with dumb state — the daemon
owns the PTY and session records; this plugin only decides what argv to run.

See `AGENTS.md` for the pi invariants this driver relies on and
`docs/plans/2026-07-19-pi-driver-plugin.md` for the implementation plan.

## Auto mode outside attn

`automode.js` is pi's permission system on its own: a safety envelope around
the working directory, a classifier for everything past it, and approval by
saying so in the conversation.

```
pi -e /path/to/attn-pi/automode.js --auto
```

`/auto` toggles it, `--auto` / `--no-auto` set it at launch, and the status
line says which it is. Without a flag it starts off unless a config file turns
it on:

```jsonc
// ~/.pi/agent/attn-automode.json  (or $PI_CODING_AGENT_DIR/attn-automode.json)
{
  "enabled_default": true,
  "environment": ["this machine has no production credentials"],
  "allow": ["git push origin*"],
  "hard_deny": ["rm -rf /*"],
  "classifier_models": ["opencode-go/glm-5.3"],
  "escalation_models": ["opencode-go/qwen3.8-max"]
}
```

Every field is optional; the models default to the ones the classifier receipt
in `docs/plans/2026-08-16-pi-auto-mode.md` picked. A file without
`enabled_default` configures auto mode without turning it on.

Each layer's models are an ordered list: the first entry judges, and the rest
are tried only when the one before it cannot be reached — a thrown request, a
provider error, an endpoint that is down. Each entry gets one immediate retry
first. A model that *answers* ends the walk whatever it said, so a deny is
never re-asked of the next model. When no model in a layer answers, the call
is blocked and the block says it was an outage rather than a judgment. The
older singular `classifier_model` / `escalation_model` still load, as a
one-entry list.
