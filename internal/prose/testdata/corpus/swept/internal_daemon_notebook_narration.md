headlessScratchCwd is the stable shared cwd for headless narration runs.
Stable, not per-run temp: Claude spills tool outputs under
~/.claude/projects/<cwd-hash>, so unique cwds accumulate orphaned dirs attn
must never reach in to clean. Safe to share — these tasks use absolute paths.

Notebook narration: two headless agent tasks. summarize_session (cheap tier)
writes a per-session digest to RawSessionsDir/<wsID>/<sessionID>.md;
narrate_workspace (strong tier, coalesced) writes today's curated journal
entry. THE FILE IS THE LEDGER — the success gate is "did the agent write the
target file", and the agents' read-before-write CAS is the concurrency
control over a journal shared with siblings and the human. Codex's
apply-patch CAS is UNVERIFIED, so Claude is the built-in default.

notebookNarrateWorkspaceTimeout bounds one curated-journal run; wider because
the narrate pass reads many digests and writes prose on the strong model.

notebookNarrationDebounce coalesces a burst of session stops into a single
pass. The removal-boundary final narrate overrides this with ZeroDebounce.

notebookSoloSessionBucket holds digests for solo (non-workspace) sessions.
Reserved name: it can never collide with a real workspace id bucket.

summarizeSessionPayload carries the run's inputs at enqueue time, while the
session and workspace rows still exist — the debounced run can fire after a
teardown deleted both. WorkspaceID is a pointer: carried-but-empty is a solo
session, absent falls back to the live row.

narrateWorkspacePayload marks a job enqueued by the daily-narrate cron, which
relaxes the success gate so a no-op daily refresh is a clean done. Session-end
and removal passes carry no flag and keep strict "must have written" gating.

notebookNarrationAllowedTools is the native tool set both narration agents
get. Claude consumes it as --allowedTools; Codex ignores it, so Codex
writability is governed by ExtraWritableRoots instead.

summarizeSessionHandler runs one per-session digest (job subject: the session
id). It prefers the carried payload over the live row, which a teardown may
have deleted, and verifies the digest was (re)written.

A run queued before the toggle was turned off must not fire; no-op success
retires the record.

Prefer the carried inputs (correct after teardown deleted the rows); the
live-row fallback covers a manually-enqueued job with no payload.

Both id segments route through the raw-tier guard: the carried wsID is just
as client-controlled as the row's, so a crafted id cannot escape the raw tier
and steer the agent's Write at the curated journal.

Pre-run fingerprint: a coalesced re-run must not be reported done off the
PRIOR run's file, silently leaving a stale digest.

The file is the ledger: the digest must exist AND have changed since the
pre-run snapshot, whatever the agent claimed.

On a teardown the zero-debounce removal narrate ran over an EMPTY digest
bucket while this summarize was still debounced, so re-enqueue the
retrospective now. Loop-safe: narrate completion never enqueues summarize.

notebookSessionDigestPath builds the absolute path of a session's raw digest:
RawSessionsDir/<wsID or _solo>/<sessionID>.md. Both id segments route through
the raw-tier guard, so a crafted id errors instead of climbing out via "..".

notebookWorkspaceSessionsDir is the per-workspace digest subdir handed to the
narrate pass as RAW_SESSIONS_DIR; it mirrors notebookSessionDigestPath's bucket.

narrateWorkspaceHandler runs one curated-journal pass; the job subject is the
workspace id. IS_REMOVAL_PASS is derived at RUN TIME from workspace-row
absence, and success is the journal carrying today's workspace marker (the
file is the ledger).

A run queued before the toggle was turned off must not fire; no-op success
retires the record.

MkdirAll so the agent's "read every digest" step does not fault when no
member ever summarized.

Pre-run snapshot of the marker block: a coalesced re-run must not be marked
done off the PRIOR run's block, silently dropping the removal retrospective.

Widen the Codex sandbox to the whole notebook root so it can write the
curated journal (Claude ignores this).

The file is the ledger: this workspace's entry block must be present AND
changed since the pre-run snapshot.

dailyPass relaxes the gate for the daily-cron backstop ONLY: a refresh that
legitimately finds nothing new would otherwise ride the backoff to dead.
Removal and session-end passes keep retry-until-the-digest-lands.

gatherNarrateWorkspaceInputs assembles the absolute-path inputs for the
narrate agent. TRANSCRIPT_PATHS are best-effort: the digests are the durable
record and the brief only consults transcripts to chase a divergence.

The READ path uses the same guard as the writer, so it can never address a
different file, and a crafted id fails the run rather than pointing the
agent at an attacker-chosen file.

On a removal pass the member rows are gone, so this is typically empty.

narrationToday returns today's date in YYYY-MM-DD for the journal filename;
narrationNowOverride pins the clock in tests.

workspaceNarrationMarker MUST match the exact line the prompt brief tells the
agent to write. The full delimited form is load-bearing: bare `attn:wsnarr:ws-1`
is a substring of `<!-- attn:wsnarr:ws-10 -->`, so a sibling would falsely verify.

workspaceNarrationEntry is the pre/post-run snapshot of a workspace's marker
block in a day's journal, used as the freshness ledger for a narrate run.

the entry block from the marker line to the next "## "/EOF

workspaceNarrationBlock returns this workspace's entry block from the journal
at path. A missing file reports an absent entry, not an error. The body is
scoped to the workspace's own marker so the freshness check ignores a
concurrent sibling's edit elsewhere in the file.

fileFingerprint is the digest freshness ledger: existence plus a content hash.
Content, not mtime, so a no-op is never missed by coarse mtime granularity.

enqueueSummarizeSession queues a per-session digest run on session Stop,
coalesced per session. The transcript path and workspace id are stashed on
the payload here, while both rows still exist — see summarizeSessionPayload.

markNotebookWorkspaceActivity records that a workspace saw real activity (a
session end or a content-changing context write) since the last daily-narrate
fire, feeding the cron's activity gate. In-memory and best-effort.

enqueueDailyNarrateWorkspace queues the daily-cron narrate, stamped DailyPass
so the executor's success gate relaxes for a no-op refresh. Known self-healing
edge: a session-end narrate coalescing into this window inherits the daily
flag either ordering, so its no-op is marked done rather than retried.

enqueueFinalNarrateWorkspace queues the removal-boundary final narrate with
zero debounce. MUST run AFTER the context snapshot and the workspace-row
removal, so IS_REMOVAL_PASS derives true and the snapshot is on disk. A no-op
before the runner exists, so the startup reaper defers its enqueue to Start.

resolveStopWorkspaceID reads the stopped session's workspace id from the
PERSISTED row, not the in-memory registry, which can race a concurrent
dissociate-on-close.
