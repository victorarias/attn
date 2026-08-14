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
// It writes no package.json and installs nothing. An app has no runtime
// dependency of its own — the compiler belongs to attn, and the SDK is a symlink
// into a package attn materializes — so a new app is a directory of text files
// plus one link.
//
// That link is best-effort here and mandatory at apply. Materializing the SDK
// can need the network once per machine (the toolchain install), and `attn app
// new` on a plane must still produce a complete, appliable app: when it cannot
// be made, the scaffold says so and names the command that will make it.

// ScaffoldOptions is one `attn app new`.
type ScaffoldOptions struct {
	// Dir is the directory to create. It must not already hold a manifest.
	Dir string
	// Name defaults to the directory's base name.
	Name string
	// Description is optional prose for the manifest.
	Description string
	// StoreDir is the artifact root, `<data-dir>/apps`, where the SDK is
	// materialized from. Empty skips the link entirely.
	StoreDir string
	// Log receives progress lines. Optional.
	Log func(string)
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
	if err := os.MkdirAll(filepath.Join(dir, "src", "views"), 0o755); err != nil {
		return Manifest{}, fmt.Errorf("creating %s: %w", dir, err)
	}
	files := map[string]string{
		ManifestName:             scaffoldManifest(name, description),
		"src/index.ts":           scaffoldEntrypoint(),
		"src/views/Sessions.tsx": scaffoldView(),
		"tsconfig.json":          scaffoldTSConfig(),
		".gitignore":             "node_modules/\n",
		"AGENTS.md":              scaffoldAgentsMD(name),
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
	if opts.StoreDir != "" {
		if _, err := ResolveToolchain(opts.StoreDir, opts.Log); err != nil {
			logf(opts.Log, "the SDK's types are not linked yet (%v); `attn app apply %s` installs them, and your editor will show unresolved imports until then", err, dir)
		} else if _, err := EnsureSDK(opts.StoreDir, dir, opts.Log); err != nil {
			logf(opts.Log, "the SDK's types are not linked yet (%v); `attn app apply %s` installs them, and your editor will show unresolved imports until then", err, dir)
		}
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

# A view is a React component attn mounts as a tile in a workspace. The title is
# what the dock picker and the tile header show. The optional params table makes
# the dock ask for one line of text before placing the tile, and that string is
# what makes two tiles of one view show different things — it is opaque to attn.
[[views]]
name = "sessions"
kind = "tile"
title = "Sessions"
entrypoint = "src/views/Sessions.tsx"
params = { label = "Filter by session id", placeholder = "leave empty for all" }

# A command is how a view acts. Each one becomes a required handler under
# `+"`command:<name>`"+` in src/generated.ts, and it runs where every other handler
# runs — same process, same document access, same log.
[[commands]]
name = "forget"
description = "Drop one session from the list."
`, name, name, description, APIVersion, apps.Namespace(name))
}

func scaffoldEntrypoint() string {
	return fmt.Sprintf(`import type { Ctx, Handlers } from "./generated"
import type { AppEvent } from %q

// One handler per subscription and one per command in %s. The `+"`satisfies Handlers`"+` below is
// what keeps the two in step: declare either one in the manifest without a
// handler here — or give a handler the wrong shape — and `+"`attn app apply`"+` fails
// with the file and line, before anything is installed.

async function onSessionState(event: AppEvent, ctx: Ctx): Promise<void> {
  // A fact says something changed; it is not the new state. Read what you need.
  await ctx.collections.seen.put(event.subject, {
    state: String(event.name),
    seq: event.seq,
  })
}

// A command is what a view calls. The payload is whatever the view passed —
// typed unknown, because attn never looks inside it — and what this returns
// travels back to the view as the command's result.
async function forget(payload: unknown, ctx: Ctx): Promise<{ forgotten: boolean }> {
  const id = (payload as { id?: unknown } | null)?.id
  if (typeof id !== "string" || id === "") {
    // Throwing is how a command refuses. The view is told, in these words.
    throw new Error("forget needs an id: { id: \"<session>\" }")
  }
  return { forgotten: await ctx.collections.seen.delete(id) }
}

export default {
  "session.state.changed": onSessionState,
  "command:forget": forget,
} satisfies Handlers
`, SDKModule, ManifestName)
}

// scaffoldView is the tile `attn app new` ships: a live query, a command, and
// the SDK's components, so an author reads a working example of all three
// rather than a stub that renders "hello".
func scaffoldView() string {
	return fmt.Sprintf(`import {
  Button,
  EmptyState,
  List,
  ListRow,
  TextInput,
  useCommand,
  useQuery,
  useState,
  type ReactElement,
  type ViewProps,
} from %q

// A view is a React component attn mounts as a tile. It is a function of where
// it sits: workspaceId, sessionId and tileId are ambient, and params is the line
// the user typed when docking this tile.
//
// There is no react to import and no styling to write. The SDK re-exports the
// hooks and the components, and a tile inherits attn's own tokens, so a view
// that uses them looks like the rest of the app.

interface Seen {
  state: string
  seq: number
}

export default function Sessions({ params }: ViewProps): ReactElement {
  // A live query. It stays current on its own: attn re-runs it when a document
  // this window would hold changes, and re-renders only what moved.
  const { docs, live, error } = useQuery<Seen>("seen", {
    sort: { field: "updated_at", desc: true },
    limit: 50,
  })
  const [filter, setFilter] = useState(params)
  const forget = useCommand("forget")

  if (error) {
    return <EmptyState title="This query stopped" hint={error.message} />
  }

  const shown = filter === "" ? docs : docs.filter((doc) => doc.id.includes(filter))
  if (shown.length === 0) {
    // Loading is a state, not a spinner. Nothing in a view may animate forever:
    // attn is open all day beside GPU terminals, and a repaint loop is a battery
    // bug no test catches.
    return (
      <EmptyState
        title={live ? "No sessions yet" : "Connecting…"}
        hint={live ? "This fills in as sessions change state." : ""}
      />
    )
  }

  return (
    <>
      <TextInput
        value={filter}
        onChange={setFilter}
        placeholder="Filter by session id"
        ariaLabel="Filter by session id"
      />
      <List>
        {shown.map((doc) => (
          <ListRow
            key={doc.id}
            title={doc.id}
            meta={`+"`${doc.body.state} · seq ${doc.body.seq}`"+`}
            actions={
              <Button
                variant="danger"
                disabled={forget.pending}
                onClick={() => forget({ id: doc.id })}
              >
                Forget
              </Button>
            }
          />
        ))}
      </List>
      {/* A command that failed says so where the click happened. The message is
          the daemon's or the handler's own — it names the app and the command. */}
      {forget.error && <EmptyState title="That did not work" hint={forget.error} />}
    </>
  )
}
`, SDKModule)
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
    "noEmit": true,
    "jsx": "react-jsx",
    "jsxImportSource": "` + SDKModule + `"
  },
  "include": ["src"]
}
`
}

func scaffoldAgentsMD(name string) string {
	return fmt.Sprintf("# %s — an attn app\n"+`
This directory is one attn app. An app is an automation attn runs for you: attn
wakes it when something happens, it keeps its own documents, it can show a tile
in a workspace, and it is versioned by the content of what it builds so you can
roll it back.

You can write the whole thing from this file. Nothing else needs reading.

## The shape

    `+ManifestName+`         what wakes the app, what state it owns, what it shows
    src/index.ts           your handlers — subscriptions and commands
    src/views/Sessions.tsx a view: the component attn mounts as a tile
    src/generated.ts       derived from the manifest; do not edit
    tsconfig.json          so your editor sees what apply sees
    node_modules/          one symlink to the SDK, written by apply; do not commit

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

Every pattern under `+"`[[subscribe]]`"+` and every `+"`[[commands]]`"+` block becomes a
required key of the `+"`Handlers`"+` type in src/generated.ts. The default export of
src/index.ts must `+"`satisfies Handlers`"+`, so the compiler — not a convention, not a
runtime check — is what tells you the manifest and the code disagree:

    import type { Ctx, Handlers } from "./generated"
    import type { AppEvent } from %q

    export default {
      "session.state.changed": async (event: AppEvent, ctx: Ctx) => { ... },
      "command:forget": async (payload: unknown, ctx: Ctx) => { ... },
    } satisfies Handlers

Declare either one with no handler and apply fails with
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

## Views

A `+"`[[views]]`"+` block declares a component attn can mount as a tile in a
workspace. The user docks it from the command menu; the tile stays where they put
it, across restarts, until they close it.

A view is a function of where it sits. It is handed `+"`ViewProps`"+`:

    workspaceId   the workspace this tile is in
    sessionId     the session that workspace has selected, or null
    tileId        stable for the life of this docked tile
    params        the line the user typed when docking

`+"`params`"+` is what makes two tiles of one view show different things — one
filtered to a session, one showing everything. It is opaque to attn: declare a
`+"`params`"+` table on the view and the dock asks for it with your label, and hands
you the string exactly as typed (empty when the user left it blank).

## Reading in a view: useQuery

    const { docs, live, error } = useQuery<Seen>("seen", {
      filters: [{ field: "state", op: "eq", value: "idle" }],
      sort: { field: "updated_at", desc: true },
      limit: 50,
    })

It is a live window, not a fetch: attn keeps it current as documents change, and
re-renders only what moved. The collection is this app's — a view is handed its
own namespace and cannot ask for another's.

`+"`live`"+` says whether the daemon is serving the query right now. `+"`error`"+` is set
when the subscription ended and will not resume — a collection you removed from
the manifest, for instance. Render it; do not throw.

Only declared fields can be filtered or sorted on. There is no cursor: a live
query is a window, and a walk through pages is a different thing.

## Acting from a view: commands

    const forget = useCommand("forget")
    <Button variant="danger" disabled={forget.pending} onClick={() => forget({ id })}>

A command runs your handler, in the same process every other handler runs in,
with the same document access. That is the point: the view asks, the app decides.
A view never writes documents itself, so the app's rules live in one place.

`+"`forget(payload)`"+` never rejects — it resolves with `+"`{ ok: true, value }`"+` or
`+"`{ ok: false, error }`"+`, and mirrors a failure into `+"`forget.error`"+` for the
common case of rendering it. Throwing inside the handler is how a command
refuses; the message reaches the view.

The whole payload and the whole result travel as JSON, and one command may carry
256KB in either direction. A command is an action, not a data transfer — anything
larger belongs in a document the handler reads.

## Components

The SDK ships the controls a view needs to look native: `+"`Button`"+`
(primary / secondary / danger), `+"`TextInput`"+`, `+"`TextArea`"+`, `+"`List`"+` and
`+"`ListRow`"+`, `+"`EmptyState`"+`, and `+"`Markdown`"+`. Everything else you draw
inherits attn's own tokens — `+"`var(--color-text-primary)`"+`,
`+"`var(--font-size-md)`"+` and friends already resolve inside a tile — so a plain
element styled with them matches the app.

There is no spinner and no relative-time label, on purpose: attn sits open all
day beside GPU terminals, and a permanently animating element is a battery bug.
A loading state is text.

## Failure

A handler that throws fails that delivery. attn retries it with backoff rather
than skipping it, records every attempt, and if the app stays stuck on the same
event for fifteen minutes, disables the app — one broken app must not hold
everyone's event log open. `+"`attn app enable %s`"+` puts it back, with a clean
slate.

A command that throws fails that click and nothing else: the view is told, the
attempt is recorded, and the app stays enabled. A view that throws while
rendering shows the error in its own tile and leaves the rest of attn alone.

Every app's handlers run in one shared process, so anything you print goes to a
log attn tags per app: `+"`attn app logs %s`"+` reads your lines back, and
`+"`attn app logs runtime`"+` shows the whole thing — which is where a runtime
that will not start says why. A handler that never returns is abandoned after
sixty seconds; anything you await that is not one of attn's own APIs needs its
own timeout.

## Rules worth knowing before you start

- The manifest is the source of truth. Change what the app subscribes to, shows
  and answers there, never in code, and let the compiler tell you what to fix.
- Never edit src/generated.ts. Apply rewrites it.
- The SDK is `+"`"+SDKModule+"`"+`, and it is the only package you can import.
  Apply links it into node_modules; nothing is installed from a registry. There
  is no `+"`react`"+` to import — a view's JSX compiles to the SDK's own runtime,
  which is how an app and attn share one React.
- An unknown table in the manifest is a hard error, not a warning. An app must
  not half-load.
- Versions are content-addressed: applying the same content twice is the same
  version, and `+"`attn app rollback`"+` is a pointer move, not a rebuild. What a
  docked tile may call is what the *serving* version declared, so a rollback
  takes a command away with it.
- attn does not remember where this directory is. It is yours; keep it wherever
  you keep code.
`, name, name, name, name, name, SDKModule, name, name)
}
