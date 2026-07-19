# Environment preflight alignment

## Why

`attn preflight` should separate environment failures from product failures before a build, live verification run, or automation launch starts. A successful run means the selected profile is routed consistently, the required local tools and writable paths are usable, the installed app and daemon speak the same protocol, and the intended agent model and effort are visible.

## Aligned on

- The command is diagnostic and read-only: it reports named `pass`, `warn`, and `fail` checks and never repairs the environment.
- Human output is concise and actionable; `--json` exposes the same stable result for automation. Failures produce a non-zero exit status, while warnings do not.
- Profile resolution remains owned by `internal/config`. The live daemon health response is the authority for actual profile, data-dir, socket, port, and protocol routing.
- `--agent`, `--model`, and `--effort` describe the launch being checked. They fall back to active environment values and then the selected agent's native defaults, rather than guessing a model ID.

## In scope / deferred

This slice covers required source-build tools, writable working/cache/profile paths, active profile routing, installed-app/daemon protocol compatibility, and resolved launch model/effort. Autoreview-specific model, reasoning, and bundle reporting remains backlog item 4. The command will not install tools, create directories, restart daemons, or change settings.
