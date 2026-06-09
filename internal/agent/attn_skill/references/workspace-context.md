# Workspace Context

Workspace context is a shared, living markdown document for every agent in the
current workspace. Use it for durable goals, decisions, constraints, progress,
and handoff information that should survive individual sessions.

## Edit And Publish

Check out the current revision and capture the editable file path:

```sh
context_file="$("$ATTN_WRAPPER_PATH" workspace context show)"
```

Edit that file in place, then publish it:

```sh
"$ATTN_WRAPPER_PATH" workspace context update
```

`show` preserves local edits. When the local file is clean, it refreshes to the
latest canonical revision. Use an explicit session only when targeting another
session:

```sh
"$ATTN_WRAPPER_PATH" workspace context show --session <session-id>
```

## Check Status

```sh
"$ATTN_WRAPPER_PATH" workspace context status
```

The result reports:

- `modified`: the local file differs from its checked-out revision.
- `stale`: another session has published a newer canonical revision.
- `revision`: the local checkout revision.
- `canonical_revision`: the latest shared revision.

Updates use revision checks. If an update conflicts, keep the local edits,
inspect status, reconcile them with the latest context, and publish again.
`show --force` discards local edits and replaces the checkout; use it only when
discarding those edits is intentional.

The `workspace_context_changed` event exists for clients, but attn does not yet
interrupt or inject messages into running agents when another session updates
the context.
