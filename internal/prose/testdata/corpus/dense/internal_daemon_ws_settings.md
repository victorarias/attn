SettingTicketBoardScale scales fonts on the ticket board and ticket
detail surfaces independently of the app-wide uiScale. Empty/unset =>
the board follows uiScale.

Model capture is an explicit, local-only opt-in because visible terminal
text can contain source code, conversations, and secrets.

SettingQueueModeEnabled selects the sidebar arrangement: off (the default)
is the workspace tree alone; on adds the anchored chief slot and the "Your
turn" band above it. It is daemon-owned because which arrangement is in
effect is policy, not a rendering preference — later it changes what a turn
is, not just how one is drawn. The daemon stamps turns and broadcasts
turn_owed either way; only the app's rendering reads this.

SettingAutoApproveEnabled, when true, launches interactive agents in their
native auto-approve mode (Claude `--permission-mode auto`, Codex
`approvals_reviewer=auto_review`) so they can run unattended without
stalling on permission gates. Off by default. Yolo overrides it.

SettingAutoSettleEnabled turns on closing a turn for the user once they
have steered the agent and it has gone back to work. Off by default: it
mutates state nobody asked it to, so it ships opt-in like the queue itself.

SettingAutoSettleArmSeconds is how long a session must hold `working`
before the visible countdown starts — the delay that proves the steering
took. Empty/unset => defaultAutoSettleArmSeconds.

SettingAutoSettleCountdownSeconds is how long the countdown on the terminal
tile runs before the turn is settled — the window the user has to cancel.
Empty/unset => defaultAutoSettleCountdownSeconds.

SettingNewSessionDestinationPrefix + a scope naming one repository on one
target (e.g. "new_session_destination_local_/Users/v/projects/attn")
remembers where that repository's last new session went: a fresh worktree
or the main checkout. The picker opens on the remembered one, because a
repository is habitually one or the other. Empty/unset => new worktree,
which is what the picker has always defaulted to. Values are
DestinationNewWorktree / DestinationMainRepo; opening an existing worktree
is a one-off and writes nothing.

SettingChiefModelPrefix + agent (e.g. "chief_model_claude") pins the model a
chief-of-staff launch uses, passed through as --model. Empty/unset => the
agent's own default model. Only consulted for chief launches.

SettingChiefEffortPrefix + agent (e.g. "chief_effort_claude") pins the
reasoning effort a chief-of-staff launch uses, passed through as the
agent's native effort mechanism (Claude --effort, Codex
model_reasoning_effort). Empty/unset => the agent's own default. Only
consulted for chief launches.

SettingDefaultModelPrefix + agent (e.g. "default_model_claude") pins the
model EVERY interactive launch of that agent uses (chief or not), passed
through as --model. Empty/unset => the agent's own default. A per-spawn
pin (delegation) or a chief_model_<agent> override still takes priority
over this; see resolveLaunchModel.

SettingDefaultEffortPrefix + agent (e.g. "default_effort_claude") pins
the reasoning effort EVERY interactive launch of that agent uses (chief
or not), passed through as the agent's native effort mechanism (Claude
--effort, Codex model_reasoning_effort). Empty/unset => the agent's own
default. A per-spawn pin (delegation) or a chief_effort_<agent> override
still takes priority over this; see resolveLaunchEffort.

SettingNotebookRoot overrides the notebook's filesystem root. Empty =>
the profile-derived default (~/attn-notebook[-profile]).

SettingNotebookRootEffective is a READ-ONLY, daemon-computed key surfaced in
the settings payload (never stored, never accepted by set_setting): the
absolute folder the notebook currently resolves to, so the UI can show where
the notebook lives even when SettingNotebookRoot is blank (the default).

SettingNotebookCronFrequency is the 5-field cron expression for the
notebook's nightly maintenance slot (currently the daily-narrate backstop).
Empty => the default ("0 3 * * *").

SettingNotebookCronTimezone is the IANA timezone the frequency is
evaluated in. Empty => the machine's local time.

SettingNotebookSummarizeSessionEnabled independently gates the per-session
summarize pass. Default ON preserves existing installs; only an explicit
"false" stops new summaries and retires queued summaries without launching
their agent. The keeper master switch still takes precedence over every duty.

SettingNotebookNarrateWorkspaceEnabled independently gates every curated-
journal narrate path. Default ON preserves existing installs; only an explicit
"false" stops new narrations and retires queued narrations without launching
their agent. Raw session summaries and context compaction remain independent.

SettingActivityEnabled gates session activity: one short present-tense line
per session saying what its agent is doing right now, generated from the
transcript. Off by default — it spends money per session per refresh and
sends transcript excerpts to a model, so it is an explicit opt-in.

SettingActivityPresenceIdleSeconds is how long after the last input in the
app the `present` tier survives. Default 90 and UNMEASURED; safe because
`away` is self-healing.

SettingChiefContextWindowCap caps the chief-of-staff session's effective
context window (in tokens): auto-compaction triggers at this threshold
instead of at the model's full window, so each cache-cold chief wake
re-reads less context. Empty/unset => DefaultContextWindowCap. Applied only
to chief launches; delegated interactive agents are never capped.

SettingHeadlessContextWindowCap caps every headless run (keeper narration,
ticket reconciliation, workflow subagents) the same way. Headless runs are
one-shot and cache-cold by construction; one that grows past this is treated
as a bug, not accommodated. Empty/unset => DefaultContextWindowCap.

SettingDefaultContextWindowCapPrefix + agent (e.g.
"default_context_window_cap_claude") caps the effective context window of
EVERY interactive launch of that agent (Claude:
CLAUDE_CODE_AUTO_COMPACT_WINDOW; Codex: model_auto_compact_token_limit),
so long-lived sessions compact at a chosen threshold instead of the
agent's own default. Unlike the chief cap there is no built-in fallback:
empty/unset => uncapped, preserving the agent's native behavior. A chief
launch still takes chief_context_window_cap over this; see
launchContextWindowCap.

SettingNotebookTasksEnabled is the master switch for ALL keeper async
background duties (per-session summarize, workspace narrate, context
compaction). Default ON: a blank/unset value means enabled, so existing
installs keep running the keeper without an opt-in. Only an explicit "false"
disables the whole group; the per-duty agent/model settings stay configurable
but produce no background work while off. See notebookTasksEnabled and the
enqueue/executor gates that honor it.

SettingDBLastBackupAt is a READ-ONLY, daemon-computed key surfaced in the
settings payload (never stored, never accepted by set_setting): the UTC
RFC3339 timestamp of the most recently successful rotating database
backup (see performDatabaseBackup). Absent/empty before the first
successful backup this process lifetime.

Turning auto-settle off must stop a countdown already on screen rather than
let it run out; turning it on has to reach the sessions already working,
since there is no state transition coming to arm them. Both windows are
re-read per arm, so a duration change needs neither.

Turning session activity off has to take the lines with it. They are
stored on the session and would otherwise keep sitting on home describing
work from whenever the feature was last on — a switch that stops the
spending but not the claim is the worst of both.

publishSettingsFact re-derives the tailscale serve state, which the settings
payload carries, and then publishes the caller's fact. Every fact that
re-pushes settings goes through here so none of them can forget the refresh.

projectSettingsUpdated pushes the settings snapshot. changedKey is empty when
what moved was not a setting the user set — a plugin leaving, a backup
landing — and the wire message then carries no changed_key, as before.

Surface the EFFECTIVE auto-settle policy so the UI shows the concrete
defaults (off, 30s, 15s) rather than absent keys it would have to guess at.

Normalize the keeper master switch to its EFFECTIVE value so the UI toggle
reflects the default-ON semantics (blank/unset => "true") rather than an
absent key the frontend would read as off.

The summary duty is independently opt-out while remaining default-on for
existing profiles. Surface its effective value for the same reason as the
keeper master switch: the app should never mistake an absent key for off.

The narration duty follows the same default-on, independently opt-out
contract as summaries. Always send its effective value to the app.

Surface the EFFECTIVE token caps so the UI shows the concrete default
(128000) rather than an absent key when the operator has not set one.

Session activity. The toggle and the intervals are surfaced EFFECTIVE, so
the pane shows the concrete defaults rather than absent keys. activity.config
is deliberately NOT normalized: blank means no agent has been chosen, and
substituting one here would be the app quietly choosing how the user's money
gets spent. The pane must require the choice.

The presence tier is deliberately NOT here. It is live state, and settings
are only re-pushed when a setting changes, so a copy parked in this
snapshot goes stale within seconds of the user moving. `attn activity`
computes it per request, which is the only way to read it honestly.

chiefLaunchModel returns the configured model for a chief-of-staff launch of
the given agent (from chief_model_<agent>), or "" — the agent's own default —
when this is not a chief launch or no model is configured.

chiefLaunchEffort returns the configured reasoning effort for a
chief-of-staff launch of the given agent (from chief_effort_<agent>), or
"" — the agent's own default — when this is not a chief launch or no
effort is configured.

defaultLaunchModel returns the configured default model for EVERY
interactive launch of the given agent (from default_model_<agent>), chief
or not, or "" — the agent's own default — when none is configured.

defaultLaunchEffort returns the configured default reasoning effort for
EVERY interactive launch of the given agent (from default_effort_<agent>),
chief or not, or "" — the agent's own default — when none is configured.

resolveLaunchModel picks the model an interactive launch of the given agent
should use, honoring precedence: an explicit per-spawn pin (requested,
e.g. a delegation's --model) wins outright; otherwise a chief-of-staff
launch takes chief_model_<agent>; otherwise every launch (chief or not)
falls back to the operator-configured default_model_<agent>; otherwise the
agent's own built-in default (an empty string, meaning no --model flag).

resolveLaunchEffort mirrors resolveLaunchModel for reasoning effort:
explicit per-spawn pin, then chief_effort_<agent> for chief launches, then
the operator-configured default_effort_<agent> for any launch, then the
agent's own default.

launchContextWindowCap returns the effective context-window token cap for an
interactive launch of sessionID's agent, or 0 (no cap). Mirrors
resolveLaunchModel: the policy lives here, the driver only applies it. A
per-session pin (set_session_context_window_cap) wins outright — it is the
user's explicit act on this one session, so it outranks even the chief cap.
Otherwise a chief launch takes chief_context_window_cap (which defaults to
DefaultContextWindowCap, so the chief is always capped); every other launch
takes default_context_window_cap_<agent>, whose unset state means uncapped —
the agent's own compaction behavior.

applyHeadlessContextWindowCap pushes the headless_context_window_cap setting
into the process-global that the headless spawn seam reads. Called at startup
and on every settings change so headless runs always use the current value.

Model names/aliases are free-form (like the reviewer_model
setting); accept any value and let the agent reject bad ones.

Effort levels are agent-native (claude: low/medium/high/xhigh/max,
codex: minimal/low/medium/high/xhigh); accept any value and let
the agent reject bad ones. The UI constrains input.

Same bounds as the chief/headless caps; blank here means uncapped
rather than DefaultContextWindowCap (see launchContextWindowCap).

validateAutoSettleSeconds accepts an empty value (meaning the built-in default)
or a whole number of seconds inside the bounds. label names which of the two
windows failed, since the two settings sit side by side in the UI.

validateNewSessionDestination accepts an empty value (no remembered choice,
so the picker keeps its new-worktree default) or one of the two destinations
the picker can record.

contextWindowCap bounds. The knob can only REDUCE the effective window (a value
above the model's real limit is clamped/ignored by the agent), so the ceiling
is a fat-finger guard rather than a hard limit; the floor keeps compaction from
thrashing on a pathologically small window.

validateContextWindowCap accepts an empty value (meaning DefaultContextWindowCap)
or a whole number of tokens within [contextWindowCapMin, contextWindowCapMax].

resolveContextWindowCap turns a stored setting value into an effective token
cap, applying DefaultContextWindowCap when unset/blank/unparseable.

validateNotebookRoot accepts an empty value (meaning the profile-derived
default) or an absolute path (a leading ~/ is expanded). It refuses a path
inside the attn data dir: the notebook must live OUTSIDE ~/.attn[-profile] so
it stays a plain, externally-syncable directory a dotfile-skipping scanner
won't miss.

normalizeExternalRoot expands a leading "~/" against the user's home
directory, requires the result to be an absolute path, cleans it, and
rejects a path that is (or is inside) the attn data dir — an external root
must live OUTSIDE ~/.attn[-profile] so it stays a plain, externally-syncable
directory a dotfile-skipping scanner won't miss. Empty input is the
caller's concern: it returns ("", nil) unchanged.

Errors are unprefixed (e.g. "must be an absolute path") so each caller can
prefix them with its own vocabulary (notebook.root vs fs root).

fsdoc.Store deliberately permits symlinked roots, so a purely lexical
comparison above can be defeated by a symlink into the data dir (e.g. a
root under /tmp whose target is ~/.attn). Re-check on canonicalized
forms; the canonical value is used ONLY for this comparison — we still
return the original cleaned (non-canonical) path below so legitimate
symlinked roots keep their own spelling.

canonicalizeForComparison resolves path to its canonical form for
containment checks: symlinks in the deepest existing ancestor are resolved
and any not-yet-existing remainder is re-joined lexically. Used ONLY for
comparison against the (equally canonicalized) attn data dir — the returned
value is never handed to callers as the root, so legitimate symlinked roots
keep their original spelling.

Reached the root without finding an existing ancestor; fall back
to the lexically cleaned input.

validateNotebookCronFrequency accepts an empty value (use the default) or a
cron expression the scheduler can fire. It rejects two parseable-but-wrong
forms: an embedded CRON_TZ=/TZ= prefix (a second timezone source that would
silently compete with notebook.cron.timezone) and a schedule whose date can
never occur (e.g. "0 0 30 2 *", Feb 30) — robfig cron returns the zero time for
those, which the scheduler would treat as perpetually due and re-fire in a
tight loop.

hasCronTZPrefix reports whether a cron string carries a leading TZ=/CRON_TZ=
timezone prefix (the form robfig/cron's ParseStandard honors).

validateNotebookCronTimezone accepts an empty value (local time) or an IANA
timezone name loadable on this machine.

validateKeybindingsConfig keeps daemon validation light: the frontend owns the
shortcut schema and tolerates anything unrecognized, so the daemon only
guarantees the stored blob is parseable JSON (or empty).
