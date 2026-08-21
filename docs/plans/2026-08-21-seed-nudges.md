# Seed nudges

Garden activity needs a doorbell for the agents with a stake in it. The bell
names the seed and move, never carries the log entry, and uses the same durable
session delivery path as `attn agent msg`.

## Shape

- `garden_seed_watches` persists an explicit `(session, watched seed)` pair.
  `watch` inserts it, `unwatch` removes it, and `show` reports whether the
  calling session holds that explicit watch.
- A dispatch record remembers the delegating session. It is an automatic watch
  on the dispatched crown and does not need a second subscription record.
- On a lifecycle move or `note --ring`, the daemon walks `part-of` edges from
  the moved seed through every ancestor. Explicit watchers and dispatchers of
  any seed on that path are the audience.
- `garden_seed_bells` holds one unread `(watcher, moved seed)` pair. The insert
  is the coalescing fence: an existing row means the watcher has already been
  rung for that seed. `show` and `notes` delete the calling session's row.
- A newly claimed bell becomes a senderless queued agent message containing
  only `<seed> moved: <event>`. The existing agent-message delivery path owns
  blocked sessions, retries, and the PTY/conversation-host route.
- The mutation's source session and the seed's tender are excluded before a
  bell is claimed. Plain notes never enter this path.

The app does not render watches. This surface is for agents; the garden panel
already gives the human live visibility.

## Verification

Daemon integration tests seed a realistic multi-level plot with live sessions
and cover dispatcher transitions, silent notes, ringing notes, transitive
bubbling, coalescing/read reset, self exclusion, and unwatch. CLI tests cover
the new verbs, `--ring`, human-readable `show`, and JSON watch state. Generate
both protocol clients, increment the protocol version, run targeted Go tests,
the full Go suite, frontend type/tests, and Linux daemon builds.

Live proof uses an isolated profile: dispatch a throwaway delegate at a seed,
have the real agent write a ringing note, read the seed from the dispatcher,
then steer the agent to harvest and capture both nudges in the delegator. The
profile is cleaned after evidence is collected.
