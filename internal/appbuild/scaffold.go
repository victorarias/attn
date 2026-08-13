package appbuild

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/victorarias/attn/internal/apps"
)

// Scaffolding an app.
//
// The bar the scaffold has to clear: `attn app new <path>` then `attn app apply
// <path>` with no edit in between must succeed. A scaffold that needs a human
// touch first is a scaffold that does not work, and the test asserts exactly
// that sequence.
//
// It writes no package.json and no node_modules. An app has no runtime
// dependency to install — the SDK arrives as generated types and the compiler
// belongs to attn — so a new app is a directory of text files that builds
// offline.

// ScaffoldOptions is one `attn app new`.
type ScaffoldOptions struct {
	// Dir is the directory to create. It must not already hold a manifest.
	Dir string
	// Name defaults to the directory's base name.
	Name string
	// Description is optional prose for the manifest.
	Description string
}

// Scaffold writes a complete, appliable app into opts.Dir.
func Scaffold(opts ScaffoldOptions) (Manifest, error) {
	dir, err := filepath.Abs(strings.TrimSpace(opts.Dir))
	if err != nil {
		return Manifest{}, fmt.Errorf("resolving %s: %w", opts.Dir, err)
	}
	name := strings.TrimSpace(opts.Name)
	if name == "" {
		name = filepath.Base(dir)
	}
	if err := apps.ValidateName(name); err != nil {
		// The name usually comes from the directory, so say where it came from —
		// otherwise the reader looks for a name they never typed.
		if strings.TrimSpace(opts.Name) == "" {
			return Manifest{}, fmt.Errorf("%w (the name came from the directory %s; pass --name to choose another)", err, dir)
		}
		return Manifest{}, err
	}
	if _, err := os.Stat(filepath.Join(dir, ManifestName)); err == nil {
		return Manifest{}, fmt.Errorf("%s already holds %s, so it is already an app; `attn app apply %s` builds it", dir, ManifestName, dir)
	}
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		return Manifest{}, fmt.Errorf("creating %s: %w", dir, err)
	}

	description := strings.TrimSpace(opts.Description)
	if description == "" {
		description = "An attn app."
	}
	files := map[string]string{
		ManifestName:    scaffoldManifest(name, description),
		"src/index.ts":  scaffoldEntrypoint(),
		"tsconfig.json": scaffoldTSConfig(),
		".gitignore":    "node_modules/\n",
		"AGENTS.md":     scaffoldAgentsMD(name),
	}
	for rel, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return Manifest{}, fmt.Errorf("writing %s: %w", path, err)
		}
	}
	// CLAUDE.md is a symlink rather than a copy so the two can never disagree —
	// the repo's own convention, and the reason an agent finds the same
	// instructions whichever name its harness looks for.
	claude := filepath.Join(dir, "CLAUDE.md")
	_ = os.Remove(claude)
	if err := os.Symlink("AGENTS.md", claude); err != nil {
		return Manifest{}, fmt.Errorf("linking CLAUDE.md to AGENTS.md: %w", err)
	}

	manifest, err := LoadManifest(dir)
	if err != nil {
		return Manifest{}, fmt.Errorf("the scaffold attn wrote does not parse, which is a bug in attn: %w", err)
	}
	if err := WriteGenerated(dir, manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func scaffoldManifest(name, description string) string {
	return fmt.Sprintf(`# %s — an attn app.
#
# This file is the app's contract. It declares what wakes the app and what state
# the app owns; `+"`attn app apply`"+` derives src/generated.ts from it and the
# TypeScript compiler checks your handlers against that. Nothing here is read at
# runtime from this file — it is frozen into the version when you apply.

name = %q
description = %q

# The app contract version attn speaks. Apply refuses a manifest that asks for a
# newer one than the attn running it.
attn_app_api = %d

entrypoint = "src/index.ts"

# Every event pattern listed here becomes a required handler in src/generated.ts.
# A pattern is an exact fact name (session.state.changed) or a family (session.*).
# `+"`attn bus status`"+` lists the consumers; the fact names are attn's domain
# vocabulary — session.*, ticket.*, delegation.*, document.*.
[[subscribe]]
events = ["session.state.changed"]

# The app's own documents, under %s. Fields listed here are the ones you can
# filter and sort on; everything else in a document body is stored and read back
# untouched.
[[collections]]
name = "seen"
fields = ["state"]
`, name, name, description, APIVersion, apps.Namespace(name))
}

func scaffoldEntrypoint() string {
	return fmt.Sprintf(`import type { Ctx, Handlers } from "./generated"
import type { AppEvent } from %q

// One handler per subscription in %s. The `+"`satisfies Handlers`"+` below is
// what keeps the two in step: add an event to the manifest without a handler
// here — or give a handler the wrong shape — and `+"`attn app apply`"+` fails with
// the file and line, before anything is installed.

async function onSessionState(event: AppEvent, ctx: Ctx): Promise<void> {
  // A fact says something changed; it is not the new state. Read what you need.
  await ctx.collections.seen.put(event.subject, {
    state: String(event.name),
    seq: event.seq,
  })
}

export default {
  "session.state.changed": onSessionState,
} satisfies Handlers
`, SDKModule, ManifestName)
}

// scaffoldTSConfig carries the same flags apply typechecks with, so the author's
// editor reports what apply will. Apply does not read this file — see typecheck.
func scaffoldTSConfig() string {
	return `{
  "compilerOptions": {
    "strict": true,
    "target": "es2022",
    "module": "esnext",
    "moduleResolution": "bundler",
    "skipLibCheck": true,
    "noEmit": true
  },
  "include": ["src"]
}
`
}

func scaffoldAgentsMD(name string) string {
	return fmt.Sprintf("# %s — an attn app\n"+`
This directory is one attn app. An app is an automation attn runs for you: attn
wakes it when something happens, it keeps its own documents, and it is versioned
by the content of what it builds so you can roll it back.

You can write the whole thing from this file. Nothing else needs reading.

## The shape

    `+ManifestName+`      what wakes the app and what state it owns
    src/index.ts        your handlers — the only file you edit
    src/generated.ts    derived from the manifest; do not edit
    src/attn-app.d.ts   the SDK's types; do not edit
    tsconfig.json       so your editor sees what apply sees

## The loop

    attn app apply .        build and install this app
    attn app dev .          the same, on every save
    attn app status %s
    attn app logs %s        what the app printed
    attn app rollback %s
    attn app disable %s     stop delivering to it, keep it installed

Apply parses the manifest, regenerates the two derived files, typechecks, bundles
and installs — in that order, and it stops at the first failure with nothing
installed. **Apply never runs your code.** A module that throws at import still
applies; you find out when a handler runs.

## Writing a handler

Every pattern under `+"`[[subscribe]]`"+` becomes a required key of the `+"`Handlers`"+`
type in src/generated.ts. The default export of src/index.ts must
`+"`satisfies Handlers`"+`, so the compiler — not a convention, not a runtime check —
is what tells you the manifest and the code disagree:

    import type { Ctx, Handlers } from "./generated"
    import type { AppEvent } from %q

    export default {
      "session.state.changed": async (event: AppEvent, ctx: Ctx) => { ... },
    } satisfies Handlers

Declare a subscription with no handler and apply fails with
`+"`src/index.ts(7,1): error TS1360`"+` — the file, the line, and what is missing.

## What a handler is given

`+"`event`"+` is a fact from attn's durable event bus: `+"`name`"+` (dotted, e.g.
`+"`session.state.changed`"+`), `+"`subject`"+` (the entity it is about), `+"`seq`"+`, and
`+"`payload`"+`, typed `+"`unknown`"+` until the SDK pins that fact's shape.

A fact is an invalidation, not a payload. If you need the current state of
something, read it — the fact may already be stale by the time you run.

`+"`ctx.collections.<name>`"+` is one of the collections declared in the manifest,
scoped to this app's own namespace. `+"`get`"+`, `+"`put`"+`, `+"`delete`"+`, `+"`query`"+`,
`+"`count`"+`. Only fields you declared can be filtered or sorted on; the rest of a
document body is stored and read back untouched.

## Failure

A handler that throws fails that delivery. attn retries it with backoff rather
than skipping it, records every attempt, and if the app stays stuck on the same
event for fifteen minutes, disables the app — one broken app must not hold
everyone's event log open. `+"`attn app enable %s`"+` puts it back, with a clean
slate.

Every app's handlers run in one shared process, so anything you print goes to a
log attn tags per app: `+"`attn app logs %s`"+` reads your lines back, and
`+"`attn app logs runtime`"+` shows the whole thing — which is where a runtime
that will not start says why.

## Rules worth knowing before you start

- The manifest is the source of truth. Change what the app subscribes to there,
  never in code, and let the compiler tell you what to fix.
- Never edit src/generated.ts or src/attn-app.d.ts. Apply rewrites both.
- An unknown table in the manifest is a hard error, not a warning. An app must
  not half-load.
- Versions are content-addressed: applying the same content twice is the same
  version, and `+"`attn app rollback`"+` is a pointer move, not a rebuild.
- attn does not remember where this directory is. It is yours; keep it wherever
  you keep code.
`, name, name, name, name, name, SDKModule, name, name)
}
