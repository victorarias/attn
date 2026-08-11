Model capture is an explicit, local-only opt-in: visible terminal text
can contain secrets.

SettingQueueModeEnabled selects the sidebar arrangement (workspace tree
alone vs chief slot + "Your turn" band). The daemon stamps turns either way.

SettingAutoApproveEnabled launches interactive agents in their native
auto-approve mode. Off by default; yolo overrides it.

SettingAutoSettleEnabled closes a turn for the user once they have steered
the agent back to work. Off by default — it mutates state unasked.

SettingAutoSettleArmSeconds: how long a session holds `working` before the
countdown starts. Empty => defaultAutoSettleArmSeconds.

SettingAutoSettleCountdownSeconds: the visible cancel window before the
turn settles. Empty => defaultAutoSettleCountdownSeconds.

SettingNewSessionDestinationPrefix + repo scope remembers where that repo's
last new session went (DestinationNewWorktree / DestinationMainRepo).
Empty => new worktree; opening an existing worktree writes nothing.

SettingChiefModelPrefix + agent pins the model a chief-of-staff launch
uses (--model). Empty => agent default; chief launches only.

SettingChiefEffortPrefix + agent pins a chief launch's reasoning effort
via the agent's native mechanism. Empty => agent default.

SettingDefaultModelPrefix + agent pins the model EVERY interactive launch
uses; per-spawn pins and chief_model_<agent> outrank it (resolveLaunchModel).

SettingNotebookRoot overrides the notebook's filesystem root. Empty =>
the profile-derived default (~/attn-notebook[-profile]).

SettingNotebookRootEffective is READ-ONLY and daemon-computed (never
stored, never accepted by set_setting): the folder the notebook resolves to.

SettingNotebookCronFrequency: cron expression for the notebook's nightly
maintenance slot. Empty => "0 3 * * *".

SettingNotebookCronTimezone: IANA timezone the frequency is evaluated in.
Empty => local time.

SettingNotebookSummarizeSessionEnabled gates the summarize pass. Default
ON; only an explicit "false" disables. The keeper master switch outranks it.

SettingNotebookNarrateWorkspaceEnabled gates every narrate path. Default
ON; only an explicit "false" disables.

SettingActivityEnabled gates session activity (one present-tense line per
session from its transcript). Off by default: it spends money per session
per refresh and sends transcript excerpts to a model.

SettingActivityPresenceIdleSeconds is how long after the last app input
the `present` tier survives. Default 90, UNMEASURED; safe because `away`
is self-healing.

SettingChiefContextWindowCap caps the chief session's effective context
window (tokens) so each cache-cold wake re-reads less. Empty =>
DefaultContextWindowCap; chief launches only.

SettingHeadlessContextWindowCap caps every headless run the same way; a
run that grows past it is a bug, not accommodated. Empty =>
DefaultContextWindowCap.

SettingDefaultContextWindowCapPrefix + agent caps EVERY interactive launch of
that agent. Empty => uncapped; chief_context_window_cap outranks it.

SettingNotebookTasksEnabled is the master switch for ALL keeper background
duties. Default ON (blank means enabled); only an explicit "false"
disables the group.

SettingDBLastBackupAt is READ-ONLY and daemon-computed: UTC RFC3339 stamp
of the last successful rotating backup this process lifetime.

Off must stop a countdown already on screen; on must reach sessions
already working. Durations are re-read per arm and need neither.

Turning session activity off has to take the lines with it. They are
stored on the session and would otherwise keep sitting on home describing
work from whenever the feature was last on — a switch that stops the
spending but not the claim is the worst of both.

publishSettingsFact refreshes the tailscale serve state, then publishes.
Every settings-re-pushing fact goes through here so none forgets the refresh.

projectSettingsUpdated pushes the settings snapshot; changedKey is empty when
what moved was not a user-set setting.

These are default-ON: send EFFECTIVE values so the app never mistakes an
absent key for off.

Session activity. The toggle and the intervals are surfaced EFFECTIVE, so
the pane shows the concrete defaults rather than absent keys. activity.config
is deliberately NOT normalized: blank means no agent has been chosen, and
substituting one here would be the app quietly choosing how the user's money
gets spent. The pane must require the choice.

The presence tier is deliberately NOT here. It is live state, and settings
are only re-pushed when a setting changes, so a copy parked in this
snapshot goes stale within seconds of the user moving. `attn activity`
computes it per request, which is the only way to read it honestly.

chiefLaunchModel returns chief_model_<agent>, or "" when not a chief launch
or unconfigured.

chiefLaunchEffort returns chief_effort_<agent>, or "" when not a chief launch
or unconfigured.

resolveLaunchModel: per-spawn pin, then chief_model_<agent> for chief
launches, then default_model_<agent>, then "" (the agent's own default).

launchContextWindowCap returns the effective token cap for an interactive
launch, or 0. Precedence: per-session pin, then chief_context_window_cap for
chief launches, then default_context_window_cap_<agent> (unset => uncapped).

applyHeadlessContextWindowCap pushes headless_context_window_cap into the
process-global the headless spawn seam reads; called at startup and on change.

Model names and effort levels are free-form / agent-native: accept
anything and let the agent reject bad ones.

validateAutoSettleSeconds accepts empty (the built-in default) or whole
seconds inside the bounds; label names which of the two windows failed.

validateNewSessionDestination accepts empty (no remembered choice) or one of
the two destinations the picker can record.

contextWindowCap bounds. The knob can only REDUCE the window, so the ceiling
is a fat-finger guard; the floor keeps compaction from thrashing.

resolveContextWindowCap turns a stored value into an effective token cap,
defaulting when unset/blank/unparseable.

validateNotebookRoot accepts empty (the profile-derived default) or an
absolute path outside the attn data dir; see normalizeExternalRoot.

normalizeExternalRoot expands ~/, requires an absolute path, and rejects a path
at or inside the attn data dir. Empty returns ("", nil); errors are unprefixed
so each caller adds its own vocabulary.

Symlinked roots are permitted, so a symlink can defeat the lexical check
above. The canonical form is used ONLY for comparison — the cleaned path is
returned, so legitimate symlinked roots keep their spelling.

canonicalizeForComparison resolves symlinks in the deepest existing ancestor
and re-joins the rest lexically. Used ONLY for containment comparison; never
returned to callers as the root.

validateNotebookCronFrequency rejects an embedded CRON_TZ=/TZ= prefix (competes
with notebook.cron.timezone) and a never-occurring date like Feb 30 — robfig
returns the zero time for those, which the scheduler re-fires in a loop.

validateKeybindingsConfig only guarantees parseable JSON (or empty); the
frontend owns the shortcut schema.
