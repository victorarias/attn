# Native Launcher Versus Tauri Dialog Analysis

Date: 2026-05-25
Status: Decisions captured; ready to refine into implementation slices

## Purpose

The Swift native launcher currently proves that the client can create a
workspace and add panes through the daemon. It is not yet a faithful version
of the Tauri new-session experience.

This document identifies:

- which differences are confirmed implementation gaps;
- which differences are deliberate first-slice limitations;
- where workspace-first semantics require new product decisions;
- whether a new pane can start from the current directory of an existing
  terminal;
- the decisions made before implementing dialog parity.

This remains an analysis document rather than the implementation checklist.
The major product ambiguities have now been resolved.

## Material Reviewed

- Existing native proposal: `docs/plans/2026-05-24-native-workspace-pane-launcher.md`
- Native launcher model: `native-ui/Sources/AttnNative/Views/Launcher/WorkspaceLauncherModel.swift`
- Native launcher view: `native-ui/Sources/AttnNative/Views/Launcher/WorkspaceLauncherView.swift`
- Native app keyboard commands: `native-ui/Sources/AttnNative/App/AttnNativeApp.swift`
- Tauri launcher: `app/src/components/LocationPicker.tsx`
- Tauri path input: `app/src/components/NewSessionDialog/PathInput.tsx`
- Tauri repository destination picker:
  `app/src/components/NewSessionDialog/RepoOptions.tsx`
- Tauri launcher tests: `app/src/components/LocationPicker.test.tsx` and
  `app/src/components/NewSessionDialog/RepoOptions.test.tsx`
- Daemon settings handler: `internal/daemon/ws_settings.go`
- Protocol schema: `internal/protocol/schema/main.tsp`
- Ghostty fork C header and terminal PWD handling in
  `~/projects/victor/ghostty`

## Executive Findings

1. The session-agent preference is persisted in the daemon. The native
   Add Pane dialog ignores it and explicitly resets to Terminal.
2. The native YOLO control does not match Tauri. Tauri treats YOLO as state on
   the selected target and gates it through agent capability; native exposes a
   separate toggle and enables it for every non-terminal pane kind.
3. The native dialog has launcher invocation shortcuts, but it does not have
   the keyboard interaction model inside the dialog that exists in Tauri.
4. Location and worktree lists are visually present but are not keyboard
   navigable. The footer currently advertises navigation that is not wired.
5. The worktree form loses important context: a new workspace and a new pane
   do not mean the same thing when selecting or creating a checkout.
6. Starting a new pane at the active shell's post-`cd` directory is not
   available through the current Swift/Ghostty bridge. Ghostty already tracks
   terminal PWD from shell integration, so this is feasible through our fork,
   with limitations and an explicit fallback.
7. The existing native plan stated a Tauri-compatible target but accepted a
   reduced first slice. The current launcher has reached that reduced slice;
   its unchecked parity work now needs to be made explicit rather than being
   treated as polish.

## Decisions Captured

1. New Workspace and Add Pane must store and restore the last selected pane
   type, including Terminal as well as a coding agent.
2. Add Pane should use the focused Ghostty terminal's reported current
   directory when it is available and reliable, falling back to the
   workspace's configured default starting location.
3. A workspace may contain panes and agents in multiple directories and
   multiple worktrees. Its location is the default starting location for new
   panes, not an ownership or checkout boundary. It may become editable later.
4. Local-only targets are acceptable as a small initial slice, provided the
   target abstraction is preserved so remote targets can be added without
   replacing the launcher model.
5. Keyboard behavior must be modelled as configurable semantic actions, with
   bindings eventually persisted by the daemon. The native implementation
   must not embed product behavior in unchangeable key literals.

## Vocabulary And Semantics

The native UI now exposes a concept that the older new-session dialog did not
need to distinguish as sharply: a workspace is a layout and navigation
container with a default launch location. Each pane can run in another
directory or worktree.

| Action | Stable object being created | What the selected directory means | What it must not do silently |
| --- | --- | --- | --- |
| New Workspace | A new workspace plus its initial pane | The workspace default starting location and the initial pane working directory | Treat later terminal `cd` changes as edits to the saved default |
| Add Pane | A new pane inside the selected workspace | The new pane's initial working directory, which may be another worktree | Change the workspace default merely because this pane starts elsewhere |
| Quick shell split | A terminal pane inside the selected workspace | A fast default working directory chosen without dialog interaction | Require the full launcher flow |

The copy and labels should reflect this distinction:

- New Workspace should ask for `Workspace location`.
- Add Pane should ask for `Pane starting directory` or `Start this pane in`.
- The pane type selector should not be labelled only as `Session agent` when
  Terminal is one of its choices.
- The workspace sidebar/details may eventually expose and edit its default
  starting location; it should not imply all panes share that directory.

## Confirmed Differences

### Preference And Default Pane Type

The daemon already stores an agent preference under `new_session_agent`. In
the dev profile database at review time:

```text
new_session_agent       codex
new_session_yolo_local  true
```

The native model behaves differently depending on the entry point:

- New Workspace reads `new_session_agent` through `preferredChoice()`.
- Add Pane resets its choice to `.terminal`.
- Selecting a coding agent persists `new_session_agent`.
- Selecting Terminal clears local YOLO in view state but does not persist
  Terminal as a preferred type.

This explains the observed behavior: opening Add Pane repeatedly goes back to
Terminal even when the stored coding-agent preference is Codex.

There is also a data-model issue: Tauri's `new_session_agent` setting
represents a coding agent. Terminal did not exist as a selectable new-session
agent in that UI. Persisting Terminal into the same setting would alter the
meaning of an existing shared setting.

Decision:

- Add a daemon-persisted launcher choice setting, for example
  `new_pane_choice`, whose values can include `terminal` and supported agent
  identifiers.
- New Workspace and Add Pane both read and update this setting.
- When a coding agent is selected, continue updating `new_session_agent` as
  the compatible agent preference for Tauri and other existing clients.
- If `new_pane_choice` is absent, migrate behavior by using
  `new_session_agent` when valid and available, then persist the user's next
  explicit selection.
- `Cmd+D` and `Cmd+Shift+D` remain immediate Terminal split actions and do not
  overwrite the remembered dialog choice.

### YOLO Behavior

Tauri behavior:

- Agent capability determines whether YOLO is available.
- Local and remote targets store separate YOLO preferences.
- Selecting the currently active target again toggles YOLO.
- The visual state is presented on the selected target card.

Native behavior:

- A standalone `Toggle("YOLO")` is displayed next to the Local target card.
- Every non-terminal enum choice is assumed to support YOLO.
- The UI currently represents only the local target.
- New Workspace loads the local preference; Add Pane resets to Terminal before
  the YOLO preference can matter.

The first two items are parity gaps. Local-only target support was already an
intentional first-slice limitation in the existing native plan, but it should
not be mistaken for completed target behavior.

Baseline parity behavior to consider:

- Put the YOLO indication and toggle behavior on the Local target card.
- Use daemon-provided agent capabilities rather than pane-kind assumptions.
- Make the interaction identical in New Workspace and Add Pane when the
  selected pane type is a supported coding agent.

### Keyboard Shortcuts And Navigation

The native application does have top-level commands:

| Command | Native behavior |
| --- | --- |
| `Cmd+Shift+N` | Open New Workspace |
| `Cmd+N` | Open Add Pane with vertical direction |
| `Cmd+Option+N` | Open Add Pane with horizontal direction |
| `Cmd+D` / `Cmd+Shift+D` | Quick shell split |
| `Cmd+Option+Arrow` | Pane/workspace navigation |

The missing keyboard support is inside the dialogs.

| Interaction | Tauri | Native now | Classification |
| --- | --- | --- | --- |
| `Option+1..9` choose agent | Implemented | Missing | Parity gap |
| `Option+letter` choose target | Implemented | Missing | Parity gap, once more than Local exists |
| Arrow keys select path suggestion/recent location | Implemented | Missing | Parity gap |
| `Tab` complete selected path suggestion | Implemented | Completes an inferred first match only | Incomplete parity |
| Arrow keys navigate destination/worktree list | Implemented | Missing | Parity gap |
| Number keys open destination/create row | Implemented | Missing | Parity gap |
| `R` refresh repository state | Implemented | Missing | Parity gap |
| `D` delete worktree with confirmation | Implemented | Missing | Deferred functionality in existing plan |
| `Tab` toggle create-worktree source branch | Implemented | Missing | Parity gap |
| Nested `Escape` dismissal/back behavior | Implemented | Partial/simple stage back | Needs verification and parity tests |

The native footer currently says `Up/Down Navigate` in stages where the model
does not implement navigation. That is a visible correctness issue independent
of the broader parity work.

### Configurable Shortcut Architecture

Tauri already routes app actions through a central shortcut registry, but its
registry is currently a static in-client map. The native app currently
registers top-level commands directly with literal SwiftUI
`.keyboardShortcut(...)` declarations. Neither is yet the desired daemon-backed
configuration model.

Decision:

- Implement launcher keyboard behavior in terms of semantic action
  identifiers, not raw key checks in the launcher model or view.
- Store user overrides in daemon settings later; defaults may initially ship
  in the client, but should flow through the same resolved-binding model.
- Keep contextual actions contextual. A binding such as
  `launcher.selection.next` should only operate while an appropriate launcher
  list owns interaction, and should never leak keystrokes into Ghostty.
- Render shortcut hints from resolved bindings, so the UI never advertises a
  hard-coded key that the user has remapped.

Suggested semantic action surface:

| Action ID | Default binding | Context |
| --- | --- | --- |
| `workspace.new` | `Cmd+Shift+N` | Application |
| `pane.addVertical` | `Cmd+N` | Workspace |
| `pane.addHorizontal` | `Cmd+Option+N` | Workspace |
| `pane.quickShellVertical` | `Cmd+D` | Workspace |
| `pane.quickShellHorizontal` | `Cmd+Shift+D` | Workspace |
| `launcher.paneChoice.terminal` | To decide | Launcher type selector |
| `launcher.paneChoice.slot1...slotN` | Tauri-compatible number defaults | Launcher type selector |
| `launcher.target.slot1...slotN` | Tauri-compatible letter defaults | Launcher target selector |
| `launcher.selection.previous` | `Up` | Active launcher list |
| `launcher.selection.next` | `Down` | Active launcher list |
| `launcher.selection.open` | `Enter` | Active launcher list |
| `launcher.complete` | `Tab` | Location input |
| `launcher.worktree.toggleBase` | `Tab` | Worktree creation form |
| `launcher.repository.refresh` | `R` | Repository destinations |
| `launcher.repository.delete` | `D` | Repository destinations |
| `launcher.cancel` | `Escape` | Active launcher stage |

The exact daemon representation can be decided with the settings UI work. A
reasonable boundary is a validated override map keyed by action ID, leaving
client defaults versioned with the feature implementation. Native menu item
key equivalents and dialog footer hints must both be derived from the resolved
binding set.

Avoid binding numbered shortcuts directly to fixed agent names. Agents are
already capability/availability driven in Tauri and may become dynamic; slot
actions preserve keyboard selection without requiring a new daemon setting
whenever an agent is added.

### Location Picker Behavior

Tauri:

- Initializes local new-session location using the configured projects
  directory when available.
- Keeps selected/highlighted rows for keyboard use.
- Supports recent paths, filesystem browsing, ghost completion and Tab.
- Scopes path behavior to the current target.

Native:

- Initializes a new workspace path as `~`.
- Shows recent locations and directory candidates.
- Offers ghost completion from an inferred match.
- Does not track a keyboard-highlighted candidate.
- Labels the browsing context from its home base rather than a true
  navigated/selected directory state.
- Is currently Local-only.

The missing selection model is the underlying reason multiple keyboard
interactions do not work. It should be treated as a launcher state-machine
gap, not patched by adding isolated key handlers to static list rows.

### Repository And Worktree Destination Behavior

Tauri repository destination selection includes:

- main repository and worktree rows;
- highlighted selection with arrow/number/Enter navigation;
- refresh;
- worktree deletion with confirmation;
- new worktree creation;
- explicit source choice for new worktree: current branch or default branch;
- `Tab` to switch source while the create form is active.

Native currently includes:

- main repository and worktree rows;
- pointer selection/opening;
- a separate create-worktree stage;
- a visible source-branch toggle;
- no keyboard destination navigation;
- no Tab behavior in the form;
- no refresh or delete path;
- no persisted selected destination context when entering creation.

The last point matters beyond parity. If creation begins while the user was
considering an existing worktree, `start from current branch` needs a clear
definition: current relative to which checkout?

## Workspace-First Semantics

### Selecting A Worktree For New Workspace

Decision:

- Selecting an existing worktree creates the workspace rooted in that
  checkout as its initial/default location.
- Creating a new worktree creates a new checkout and then creates a workspace
  whose initial/default location is there.
- `Start from current branch` can refer to the repository/destination from
  which creation was initiated, provided the UI retains that context.

### Selecting A Different Directory For Add Pane

Decision:

- A workspace keeps a default starting location, initially chosen when it is
  created and editable in a future feature.
- A new pane may start elsewhere, including in another worktree.
- A shell `cd` within a pane is transient state and must not overwrite the
  workspace default.

The Add Pane dialog should initially suggest the focused terminal's current
directory when available; otherwise it suggests the workspace default.

### Creating A Worktree From Add Pane

Decision:

- Permit selecting or creating a worktree when adding a pane.
- The created pane belongs to the active workspace while its initial working
  directory points at the selected/new worktree.
- The workspace default starting location remains unchanged unless the user
  explicitly edits it through a future workspace-setting affordance.
- The worktree form must say what it branches from. `Current branch` means the
  branch of the currently selected repository destination, not merely the
  workspace default or the first repository discovered.

## Starting From The Active Terminal Directory

### Prior Behavior

Add Pane previously defaulted to the selected workspace's stored directory. The
daemon knows pane/session launch directories, but it does not learn arbitrary
shell `cd` changes after a terminal starts.

Before this implementation:

- a shell pane can `cd` into another directory;
- opening Add Pane cannot reliably default to that new directory;
- using the stored workspace or original session directory is not equivalent
  to using the visible shell's current directory.

### Implemented Ghostty Path

Ghostty already processes terminal PWD changes through shell integration,
including OSC 7 reporting, and its internal surface implementation has access
to terminal PWD.

The attn Ghostty fork now exposes `ghostty_surface_read_pwd`, using the same
ownership contract as surface text readback, so the native launcher reads
Ghostty's validated current terminal state at the moment Add Pane opens.
`GhosttyRuntime.action_cb` also consumes `GHOSTTY_ACTION_PWD` as a cached
fallback associated with the target surface. This keeps terminal escape
sequence parsing in Ghostty; the Swift app does not implement a second OSC
parser.

The background terminal harness creates a workspace directly through the
daemon, sends a real shell `cd`, waits for Ghostty-reported PWD, and asserts
Add Pane proposes the changed directory while keeping the workspace location
unchanged. It intentionally does not print a synthetic OSC-7: after a fake
report, the shell's next prompt emits the truthful current directory and
correctly supersedes it.

Important limits:

- PWD reporting depends on shell integration or an equivalent trusted terminal
  sequence. It will not be reliable for every process state.
- Agent TUIs may not report their effective directory as a shell would.
- A reported active-pane directory should influence a new pane suggestion
  only; it must not mutate the workspace's saved default location.

Decision:

- Consume GhosttyKit's existing `GHOSTTY_ACTION_PWD` action and associate its
  validated PWD with the pane surface that produced it.
- When Add Pane is opened from a focused terminal that reports PWD, initialize
  its starting-directory suggestion from that PWD.
- Fall back to the workspace default starting location when PWD is absent or
  untrusted.
- Do not substitute last-launched pane directory and describe it as current
  directory; that becomes stale as soon as a shell changes directory.

## Additional Tauri Differences To Account For

The listed issues are not the complete parity surface.

| Area | Tauri behavior | Native status |
| --- | --- | --- |
| Agent discovery | Fixed known agents plus availability/dynamic capability behavior | Fixed Swift choices, gated by daemon availability settings |
| Capability-based controls | UI reflects daemon capabilities such as YOLO support | Local YOLO is gated by explicit daemon capability settings |
| Remote targets | Target cards and target-scoped state | Local-only by approved small first slice |
| Loading/error feedback | Async action feedback in picker and destination interactions | Basic flow; requires full parity audit |
| Worktree deletion | Confirmation and keyboard behavior | Not included in first native dialog |
| Preference migration | Tauri uses existing daemon setting meanings | Native must avoid silently redefining them |

## What To Avoid

1. Do not add isolated key handlers without building a real selected-row and
   focus-state model for location and destination stages.
2. Do not redefine `new_session_agent` to mean Terminal; add the persisted
   pane-choice setting while retaining compatible coding-agent preference.
3. Do not present the stored workspace root or launch directory as the
   terminal's current directory after the user has run `cd`.
4. Do not let a terminal PWD update mutate the workspace default location.
5. Do not make cross-worktree panes a special error path: they are an intended
   workspace capability.
6. Do not expose YOLO for agents unless their daemon capability allows it.
7. Do not claim keyboard parity while the footer advertises controls that are
   not wired.
8. Do not hard-code new native key handling in views or menus in a way that
   cannot be driven by a future daemon-backed resolved shortcut map.

## Shortcuts And Slicing

### Forbidden Shortcuts

- Encoding launcher navigation as scattered SwiftUI key handlers without a
  selected/focused item state model.
- Reusing `new_session_agent` for Terminal and thereby changing existing
  Tauri setting semantics.
- Approximating active shell PWD using a stale launch directory.
- Restricting panes to the workspace's default location in order to avoid the
  multi-worktree case.
- Wiring fixed keyboard literals into the new native behavior without a
  semantic action boundary.

### Intentional Slice: Local Target First

Local-only target support is acceptable during this parity pass. It is a small
slice, not the desired final launcher.

The slice is complete only if:

- Local target YOLO follows the same capability-gated behavior as the Tauri
  launcher.
- Pane-choice persistence, list navigation, worktree behavior and directory
  semantics work without depending on Local being the only possible target.
- The model represents a target selection and uses target-scoped setting keys,
  so remote target cards and settings can be added later without replacing the
  launcher state machine.
- Shortcut action IDs use target slots or selection actions rather than
  hard-coded `Local` operations.

Removal/extension trigger: when remote daemon/endpoints are exposed in native,
add their cards and target-specific settings to this model, then remove any
temporary one-card presentation assumptions.

## Implementation Direction

The implementation direction is:

1. Treat Tauri keyboard and target/YOLO behavior as the launcher baseline,
   extended only where native panes add Terminal as a first-class type.
2. Add a daemon-persisted pane-choice preference shared by New Workspace and
   Add Pane while keeping `new_session_agent` compatible for coding agents.
3. Model keyboard input as semantic actions resolved from bindings so daemon
   persisted customization can be added without replacing launcher behavior.
4. Use the workspace's stored location as a default, not a pane-directory
   constraint; allow panes and agents in multiple worktrees.
5. Implement Ghostty-reported active terminal PWD as the preferred Add Pane
   initial path, with workspace default as the honest fallback.

## Implementation Work

Work can proceed in this order:

1. Add behavior-level tests that demonstrate the current preference, YOLO and
   missing keyboard-state failures.
2. Add and validate the daemon pane-choice setting, including migration from a
   stored coding-agent preference, then correct capability-driven YOLO
   behavior.
3. Introduce a shortcut action/resolution boundary and a common
   keyboard-selection/focus model for location and destination options; bind
   the Tauri-default keys through that boundary.
4. Apply workspace-default versus pane-starting-directory labels and permit
   existing/new worktree pane launches without changing the workspace default.
5. Implement full worktree form keyboard behavior, refresh and approved
   deletion behavior.
6. Handle GhosttyKit PWD actions and add focused-terminal default-directory
   integration.
7. Add persisted custom shortcut overrides and their settings UI when that
   configuration surface is scheduled; the launcher behavior should already
   consume resolved bindings.
8. Add remote targets and remaining cross-client parity once local target
   behavior is stable.

## Verification Expectations

Implementation should be tested at two levels:

- Model/state tests for preferred pane type, YOLO settings, keyboard selection,
  worktree form focus transitions, resolved shortcut action dispatch and
  mode-specific directory semantics.
- Background native automation tests for actual shortcuts, visible modal
  content, launching a pane/workspace through the daemon, and preserving the
  workspace default location.

For active terminal PWD behavior, a focused integration test should:

1. create a shell pane in the dev daemon;
2. issue `cd` in the terminal;
3. wait for Ghostty-reported PWD state;
4. open Add Pane;
5. verify the proposed starting directory uses the reported directory and
   that the workspace default location is unchanged.

Additional tests required by the captured decisions:

1. Select Terminal in New Workspace, reopen Add Pane, and verify Terminal is
   restored from daemon settings.
2. Select Codex in Add Pane, reopen New Workspace, and verify Codex is
   restored while `new_session_agent` remains compatible for Tauri.
3. Create a pane in a second worktree and verify it remains in the same
   workspace without changing the workspace default location.
4. Override a launcher binding in the resolved shortcut test fixture and
   verify both dispatch and visible key hint use the override rather than a
   literal default.

## Remaining Design Detail

The captured decisions are sufficient to start implementing parity. One
detail can be chosen while building the settings contract: the final key and
wire format for the remembered pane choice and future shortcut override map.
They should be daemon-validated settings with semantic meanings, rather than
native-only local preferences.
