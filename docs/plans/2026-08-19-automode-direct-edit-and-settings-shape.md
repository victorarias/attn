# Auto mode direct editing, and a settings page grouped by intent

Status: in progress (2026-08-19)

Two connected changes in the app, shipped as one PR.

## Piece 1 — a human edits the allow / hard-deny lists in the app

Today the only write path to auto mode's pattern lists is an agent-filed
proposal plus a human promotion. That protects a real boundary: an agent must
not be able to write its own leash. It also means a human who simply wants to
add `git status *` has to file a proposal from a shell and then promote it in
the app.

The app is already the trust boundary — promotion lives there and nowhere else.
So direct editing goes there too, and nowhere else. Nothing about the boundary
moves: `attn automode` still only ever records a proposal, `ShippedHardDeny`
still blocks a supervised session from reaching the write verbs or the WS port.

### Surfaces

- `internal/automode`: `ValidateDenyPattern` lifted out of `ValidateProposal`,
  so the direct path and the proposal path refuse the same things for the same
  reasons.
- `internal/store/automode.go`: `AddAutoModePattern` / `RemoveAutoModePattern`
  over the existing `mutateAutoModeConfig`. That read/write pair already
  resolves shipped denies on the way in and strips them on the way out, so the
  invariant holds without a second implementation of it.
- `internal/protocol`: `automode_pattern_add` / `automode_pattern_remove`
  commands and their results, plus `shipped_hard_deny` on `AutoModeConfigInfo`.
  The shipped list embeds this profile's WS port, so the frontend cannot derive
  which entries are built-in; the daemon has to say.
- `internal/daemon/ws_automode.go` + `websocket.go` + `command_meta.go`: the two
  handlers, WebSocket-only. They are absent from `daemon.go`'s unix-socket
  switch on purpose, exactly like promote and discard.
- App: `AutoModeSettings.tsx` grows the editors; `useAutoModePolicy` grows
  `addPattern` / `removePattern`.

### Rules the editors enforce

- An allow pattern goes through `ValidateAllowPattern`: a blanket allow keeps
  being refused with the message it already has.
- A deny pattern must name something (non-empty), matching what a deny proposal
  has always been checked for.
- A pattern already in the list is refused by name rather than silently
  swallowed, so the input can say what happened.
- A shipped hard-deny cannot be removed. The UI does not offer the button, and
  the store refuses it anyway — a stale UI must not be able to shorten the
  leash.

### Live sessions

Unchanged, and deliberately so. A session reads the config when it spawns, so a
direct edit reaches the next session that launches and a running one keeps the
policy it started with — identical to what a promotion does today. The section
says this in place.

## Piece 2 — group the settings by what the user is doing

`SettingsModal.tsx` grew a section at a time, and the nav shows it: auto mode is
filed under "Background Tasks" because both were added around the same subsystem
work, and "Reviewer model" is a whole nav entry holding a single text input.

### Moves

| Section | Was | Is | Why |
| --- | --- | --- | --- |
| `general` | Appearance + sent files + auto-settle + projects dir + Notebook root | Appearance: theme and font sizes | One question: how attn looks. |
| `workspace` (new) | — | Projects directory, Notebook folder, sent files | One question: where the user's things live and how attn opens them. |
| `hygiene` | Muted repos and authors | Attention queue: auto-settle, muted repos, muted authors | All three answer what shows up in the queue and when it leaves. |
| `agents` | Everything agent-shaped, keeper included | Executables, defaults, models and effort, reviewer model, context caps, capabilities, PTY runtime | Which binary, which model, which limit. |
| `keeper` (new) | inside `agents` | Context maintenance: the duty roster and its background-task switch | A roster of long-running duties is not "which model does an agent use". |
| `review` | own section | folded into `agents` | It was one model override sitting apart from the other model overrides. |
| `autoMode` | under the "Background Tasks" group | under the "Agents" group | It is what an agent may do, not a durable task runner. |

`connectivity`, `plugins`, `backgroundTasks`, `eventBus` and `data` keep their
content; only their nav grouping changes.

### Auto-save

Applying on change (or on blur for free text) is the house rule. Every field
that had a Save button is converted except one.

- **Endpoint edit keeps its Save.** It sits behind an explicit Edit/Cancel mode
  over an SSH target and profile; committing per keystroke would tear down and
  re-bootstrap a live remote on the way to a valid value.
- Plugin priority commits on blur.
- Keeper duty commits when the agent or model select changes, and on blur for a
  custom model id. Its "Use default" / "Disable" button stays — that is an
  action, not a save.

Every commit raises a quiet, self-clearing "Saved" flag beside the field
(`useSavedFlash`), so a blur-commit is visible without a modal.

### IDs

`SETTINGS_SECTION_IDS` gains `workspace` and `keeper` and loses `review`. Every
reference migrates in the same change: the `SettingsSectionID` union, the
automation registry, and `app/e2e/settings-visual.spec.ts`.
