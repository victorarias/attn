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
  "classifier_model": "opencode-go/glm-5.3",
  "escalation_model": "opencode-go/qwen3.8-max"
}
```

Every field is optional; the models default to the ones the classifier receipt
in `docs/plans/2026-08-16-pi-auto-mode.md` picked. A file without
`enabled_default` configures auto mode without turning it on.
