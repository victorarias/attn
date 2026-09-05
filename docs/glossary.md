# Glossary

## Sessions

- Session: attn runtime hosting an agent through a PTY or conversation host.
- Agent conversation: provider history and resume target; can change within one session.
- Conversation session: headless runtime; its host exchanges envelopes with the daemon.
- Envelope: sequenced host message. Declaration: daemon-readable session event. Rendering: app display data.
- Run: one prompt and response, from `run_started` to `run_settled`.
- Prompt: starts a run. Steer: read at the next agent boundary. Follow-up: read before settlement.
- Parked: a run that ended with the harness's background work still running; held working for at most the parked tripwire, then settled.
- Input queue: unread messages in a conversation host.
- Session input: ordered delivery to a live session with evidence of receipt.
- Input evidence: deferred = untouched; placed = adapter-owned; taken = reading begun; indeterminate = uncertain.
- Quiet window: automated input waits 30s after the user's last keystroke in a pane; mouse and focus reports are not keystrokes. The session input lane owns the retry: every deferred delivery is re-run there when the window closes, recomputing its prompt, so a deferral is never a drop. A closed lane refuses both, so nothing armed against a stopped daemon or a replaced session runtime can still place.
- Agent mailbox: durable agent-addressed notification queue, separate from the app-wide user notification feed. Producers write here before asking for terminal input.
- Mailbox item: one durable notification. `attn agent inbox` reads a bounded FIFO batch and writes each item's exact read receipt. Domain views such as `attn seed show` can write the same receipt.
- Inbox doorbell: one generic terminal prompt for a session with unread mailbox items. A safe paste plus Enter completes the attempt; prompt-submit hooks do not hold the input lane. Another doorbell may follow a cooldown while unread items remain.
- Peer message: stored agent-to-agent body read through the agent mailbox. `attn agent inbox <message-id>` remains a single-message compatibility view.
- Turn: attention owed to an agent; viewing it does not settle it.
- Auto-settle: closes a turn after proven user-conversation input and uninterrupted working time.
- Standing dismissal: suppresses the next auto-settle for the current working stretch.
- Queue: sidebar ordering by owed turns. Pinning excludes an agent/workspace without settling turns.
- Satellite: shell pane attached to an agent. Orphan: satellite without a live parent.
- Sliver: a pane or tile suspended to a thin strip showing its name and state, when the workspace cannot give every leaf its minimum size or a drag pushes one below it. The victim is the smallest unfocused leaf, never the focused one; it expands on its own when room returns.
- Pinned sliver: folded by a drag; stays folded until clicked or a drag gives its side room. Boundaries beside a sliver resize its visible neighbors.
- Activity: generated status line. Activity cursor: transcript position already summarized.
- Presence: watching = home visible; present = recent input elsewhere in app; away = neither.
- Recoverable: runtime gone, conversation restorable. Reaped: unrestorable session removed.
- Snapshot: current conversation state. Epoch: host generation. Scroll-back: older paged history.
- Resume: copies history into a new session. Reload: reopens a recoverable session's own history.
- Launch prompt: opening message replayed only if a replacement host finds empty history.
- Session pull request: a pull request an agent opened from inside a session, reported by the tool-use hook, by a harness driver, or by `attn pr record`. Distinct from the PR inbox, which tracks pull requests waiting on the user. The daemon refreshes its status on the PR heat cadence and stops once it merges or closes.
- Provenance line: the small line under a session's name saying where the session came from and what it produced. Carries the automation that launched it and the session pull request it opened, side by side.
- nisse: attn's conversation agent, powered by pi.

## Garden and crew

- Garden: home daemon's work tracker, shared across workspaces.
- Seed: work item with stable `s-...` id, title, body, and state. Slug: readable, non-unique name.
- Plot: seed with children; its body is the plan. Packet: reusable plot template.
- Plant/tend/park/harvest/wither/replant: create/claim/pause/complete/abandon/reopen.
- Seed states: planted/open, growing/claimed, dormant/paused, harvested/done, withered/abandoned.
- Seed outcome: completion and required verification defined by the body. Harvest when both are complete.
- Tender: seed claimant; one at a time.
- Execution: last observed session, native conversation, agent, directory, host, repository, and branch for a seed.
- Resume: reopen the exact saved conversation and directory. Handover: start a new agent on the same seed, then transfer its tender.
- Send to Chief: transfer a seed and its execution receipt to the Chief with optional guidance.
- Edges: blocks orders work; part-of contains it; discovered-from records origin.
- Ready: claimable open seed, excluding plots, parked/blocked/held work, gates, packets, and packet descendants.
- Stale: open without recent activity; age alone never closes work.
- Review Garden: user-started pass over growing seeds without an active agent that may need a decision.
- Garden advisor: configurable tool-free classifier for Review Garden. It explains evidence and recommends an action; it never moves a seed.
- Keep growing: resolve one review item without changing the seed; seven quiet days must pass before it qualifies again.
- Artifact: owned file under `<Notebook>/seeds/<seed-id>/`, retained across session/workspace/seed lifecycles.
- Linked artifact: reference to an external file, Notebook document, or URL.
- Note: seed log entry. Handoff: note for the next tender. Watch: interest in change notifications.
- Delegation preferences: optional, daemon-local choices for already-authorized delegations. A role describes the work, instructions, and stopping point; its choices select a harness, provider, model, and effort. An alternative has a condition, and a separate fallback covers unmatched work. Preferences do not grant delegation authority.
- Dispatch-at-plot: delegation bound to an existing seed as its tender.
- Ticket: archived pre-Garden work item; user tickets and their history remain permanently.
- Crew member: durable named identity with a charter. Day: its current session.
- Member home: charter/handoff directory. Registry: index of member files. Binding: member's active session.
- Awareness dirs: working context directories. Priming: launch guidance.
- Wake: start a day. Sleep request: ask it to file a handoff and stop.
- Nap: replace a day using its handoff. Sleep: no live day. Heartbeat: refresh current context.
- Wake limit: cap on autonomous starts.

## Knowledge

- Workspace context: current, potentially stale account of one workspace.
- Notebook: profile-wide durable markdown; files are authoritative.
- Journal: dated work history. Knowledge base: lasting knowledge. Raw tier: machine inputs to the keeper.
- Note title: first H1 outside fenced code; filename fallback. Frontmatter `title` is ignored.
- Keeper: maintains workspace context and journal narration.
- Chief of staff: coordinates work across workspaces.

## Apps and events

- App: named automation in the shared runtime. Plugin: integration with its own supervised process.
- Version: immutable app build. Applying: build and select a version. Serving history: versions served.
- View: app React component. Tile: one mounted instance. Command: named view action.
- Event bus: ordered domain-fact log. Fact: recorded change. Subject: changed entity.
- Consumer: fact reader. Cursor: reading progress; durable cursors survive restarts, ephemeral readers start at head.
- Projection: fact-to-app traffic. Snapshot projection: whole-list update.
- Retention floor: oldest protected cursor. Pin alarm: warning of stalled reading.
- Document store: app JSON data. Namespace: owner isolation. Collection: document group.
- Document id: key within a collection. Revision: write count. Expectation: required revision, zero means absent.
- Declaration: queryable field types. Query: retrieval criteria. After cursor: pagination anchor.
- Live query: subscription delivering complete replacement results.

## Daemons and permissions

- Home: owns fleet Garden/crew. Outpost: enrolled daemon owning local sessions. Uplink: requests home.
- Enrollment: recorded home relationship. Hub: dialing side. Endpoint: SSH target. Remote: dialed machine.
- Parked endpoint: binary/protocol mismatch awaiting Sync.
- Client token: profile protocol credential. Browser host token: trusted WebView identity.
- HTTP bearer: operator credential for exposed WebSockets.
- Headless task: model run the daemon starts on its own, with no session and no PTY.
- Headless tasks switch: `ATTN_HEADLESS_TASKS` / `headless_tasks.enabled`; off refuses every headless task before it spawns. The environment wins.
- Settings snapshot for that switch: `headless_tasks.enabled` is the effective value, `.stored` the setting alone, `.override` the raw environment value when it decides.
- State marker: `<!-- attn:state=waiting_input|idle -->` in an agent's last assistant message. With the switch off it is the stop verdict, so no model runs; without one the stop settles on hook evidence. Transcript readers strip it from messages; a marker-only message is never shown.
- Auto mode: pi permissions. Config: policy/environment snapshot at launch.
- Proposal: requested policy/model change. Promotion: user applies it. Denial: refused call.
- Environment template: initial classifier context copied into config.
