# Changelog

All notable changes to this project are documented in this file.

Format: `[YYYY-MM-DD]` entries with categories: Added, Changed, Fixed, Removed.

---

## [2026-07-19]

### Added
- **Waiting on a pull request returns as soon as something needs you.** `attn pr
  wait-ready` watches checks, reviews, and comments in one request and stops at
  the first actionable update: a failing check, a review requesting changes, a
  new comment from a person, or approval on a green exact head. Each outcome has
  its own exit code, with `--json` for the full detail. Comments from bots are
  ignored, as are comments already on the pull request when the wait begins.
- **Live agent transcripts are inspectable from the CLI.** `attn session
  transcript <session-id>` prints timestamped conversation, tool, and error
  events across supported agents, with `--follow` for live updates and opaque
  cursors for safe resumption. Provider-specific records stay behind the daemon
  boundary, and secrets are redacted before output.
- **Environment failures can be diagnosed before a run starts.** `attn
  preflight` checks required tools and writable paths, verifies the active
  profile's app, daemon, socket, routing, and protocol versions, and reports the
  selected agent model and effort without changing the environment.

### Fixed
- **Delegated worktrees no longer land in an unrelated repository.** When `attn
  delegate --workspace <id> --worktree <branch>` ran without `--repo`, it chose
  the repository from the workspace's recorded directory — a value that is
  rewritten every time the workspace is registered and never updated when its
  sessions move, so it could name a repository the workspace had nothing to do
  with. The repository now comes from the sessions already in that workspace,
  and a workspace whose sessions span several repositories asks for `--repo`
  instead of silently picking one. Delegating into an existing workspace now
  starts the branch from the repository's default branch: those sessions may sit
  in several worktrees of one repository, and starting from whichever one the
  daemon happened to list first was arbitrary. Pass `--from` to start elsewhere.
- **Retried delegations no longer create duplicate agents or tickets.** `attn
  delegate` now prints a durable request and operation identity before slow
  worktree preparation, shows concise progress while it waits, and lets callers
  retry or inspect an unknown outcome by that identity. Concurrent retries
  converge on the same session, interrupted preparation resumes after a daemon
  restart, terminal failures stay terminal, and sharing an active worktree now
  requires the explicit `--allow-worktree-reuse` override.

## [2026-07-18]

### Fixed
- **`attn daemon stop` now actually stops the daemon.** Previously the
  subcommand didn't exist: running it would try to *start* a daemon instead —
  silently launching one in the foreground if none was running, or erroring
  "daemon already running" if one was. It now stops the current profile's
  daemon, matching the guidance `attn db restore` gives. Unknown `attn daemon`
  subcommands now fail with an error instead of starting a daemon.
- **Orphaned session workers now clean themselves up.** A per-session PTY worker
  whose daemon is gone for good (e.g. a torn-down test profile, or a daemon that
  was killed and never restarted) previously kept running forever, accumulating
  stray `attn pty-worker` and agent processes. Workers now stop themselves after
  12 hours with no daemon attached and no terminal output, ending their agent
  process and removing their registry/socket files. Daemon restarts and upgrades
  are unaffected — a reattaching daemon cancels the countdown, and a busy agent
  producing output defers it.

### Added
- **Manual PR-review automations now work from an exact isolated checkout.** Run
  a repository-worktree definition with `attn automation run <id> --pr-url
  <url>` to resolve the current PR head through a read-only GitHub request and
  launch the local reviewer in a detached per-session worktree. Definitions can
  use a profile-managed repository cache or explicit validated local-clone
  overrides; the run records the source, main repository, worktree, and pinned
  revision without posting or changing anything on GitHub.
- **CLI-driven automations can prepare visible work before it needs attention.**
  Apply a durable manual definition with `attn automation apply`, run it on
  demand, and inspect its immutable run history. Each run creates an ordinary
  chief-owned ticket and steerable agent session with fixed automatic approval,
  stable provenance, idempotent request handling, and restart recovery. Reuse
  `--request-id` when retrying a run whose result was interrupted or uncertain.
- **Automatic rotating database backups.** The daemon now snapshots its SQLite
  database every 6 hours (keeping the last 12) and takes an extra safety
  snapshot right before any database migration, so session, ticket, and
  workspace state can be recovered if the database is ever lost or corrupted.
- **Restore the database from a backup.** `attn db restore [path|latest]`
  restores `attn.db` from a rotating snapshot (the newest one by default),
  refusing while the daemon is running and always preserving the previous
  database file rather than deleting it. Settings now also surface the
  timestamp of the most recent successful backup.

### Changed
- **The Notebook tile is now an Editor that can open any folder.** ⌘⌥N opens
  it on the active workspace's directory, the tile header shows the open root
  with a switcher (workspace / Notebook / browse), and ⌘P fuzzy-finds across
  the whole open root. Notebook-only affordances — backlinks and send-to-chief
  — still appear only when you're browsing your actual Notebook.
- **Delegated plans now have one explicit source of truth.** `attn ticket
  attach-plan` keeps committed plans in repositories that already document work in
  Git and adds a provenance reference to the ticket; otherwise it promotes the plan
  into the Notebook and retires the verified untracked staging file. Monorepo scopes
  can be named explicitly, tracked files are never deleted automatically, and
  byte-identical copies from the old lifecycle are retired during migration while
  divergent copies are preserved.
- **Ticket completion now follows clear outcome evidence instead of a confirmation
  ritual.** Agents may complete work that Victor accepted, whose requested PR
  merged, or that has an equivalent objective terminal signal; finished work still
  awaiting review remains in review.
- **Ticket notifications now protect attention without delaying handoffs.** The current assignee and successful ticket completions keep immediate delivery, while chiefs, subscribers, and other participants coalesce ordinary unread activity into one 30-minute observer-wide window. Explicit inbox reads remain immediate, and inbox watches share delivery with nudges without duplicate wake-ups.
- **Ticket updates now make agents read intervening activity before writing.** Status, comment, and attachment commands show unread updates and require a deliberate retry; app edits reject stale ticket details and refresh them before retry. Taking or subscribing to a ticket reports unread history without consuming it.

### Fixed
- **Switching profiles from inside attn no longer keeps talking to the inherited
  daemon.** `attn profile-env` clears explicit socket and resource overrides before
  selecting the profile, so the printed profile and the daemon receiving commands
  cannot silently disagree.

## [2026-07-16]

### Added
- **Black or stuck UI failures now leave useful evidence on disk.** Agent switches capture delayed app-shell health snapshots alongside the existing terminal renderer diagnostics, including frontend crashes, event-loop stalls, and native browser visibility. A React render failure now shows a persistent error screen instead of leaving an unexplained black window.
- **Ask a bounded question about another Codex session.** `attn session instructions <session-id> --question "..."` reads that session's conversation and returns a concise Luna answer with exact supporting excerpts, transcript identity, and a fingerprint. It fails closed when the transcript, model response, or evidence cannot be verified.
## [2026-07-15]

### Added
- **Tickets can now carry any regular file.** Attach HTML prototypes, images,
  archives, and other artifacts alongside Markdown; recognized text formats open
  as source in the Notebook, while opaque files remain non-executing previews.
- **OpenCode is now an opt-in plugin included with the app.** Each profile can
  install it from Settings or the CLI without an attn checkout, Bun, or a daemon
  restart. Uninstalling preserves OpenCode run data and is blocked while the
  plugin owns an active session.
- **Installed plugins now recover automatically after crashes and lost connections.**
  Attn restarts them with bounded backoff, shows their recovery state in
  Settings, and lets OpenCode resume monitoring the same native sessions without
  launching replacement TUIs.
- **OpenCode prose questions now surface as waiting for input.** Native question
  and permission requests remain authoritative, while an explicit idle turn can
  also distinguish ordinary assistant questions from completed answers without
  changing the visible OpenCode conversation.
- **OpenCode sessions now receive attn's launch guidance.** Ordinary sessions
  get their workspace context plus workflow and ticket instructions; OpenCode
  chiefs get Notebook guidance. Promotion, demotion, and resume refresh that
  guidance without changing the native OpenCode conversation or modifying the
  user's repository and global OpenCode configuration.
- Markdown tiles can now be annotated: select text to comment, redline, or apply
  quick-labels (with keyboard shortcuts), add document-wide comments, and review
  everything in a sidebar. Annotations survive file edits by re-anchoring to the
  moved text (flagged in the sidebar if the text disappears), and drafts persist
  across tile close and app restart.
- Markdown annotations can now be sent to an agent: every markdown tile is bound
  to the session it was opened from (shown in the tile header, with a dropdown to
  retarget to any session in the workspace). "Send" (or Cmd+Enter) formats your
  comments, redlines, and quick labels into a single feedback message, types it
  into the target session, and clears the draft. If the target is waiting on an
  approval prompt, nothing is sent and your annotations are kept so you can retry.

### Changed
- **OpenCode upgrades no longer require an attn plugin update.** The plugin now
  accepts stable OpenCode releases at or above 1.17.16 and can resume existing
  sessions after a forward upgrade. Older, preview, and downgrade versions are
  still rejected, while a newer incompatible server reports the concrete API
  failure through plugin health.
- **Markdown tiles now render documents on a proper reading surface.** Files open on a centered card with book-quality typography, syntax-highlighted code blocks with a hover copy button, clickable heading anchors that scroll smoothly within the tile, and a metadata card for YAML frontmatter. Dangerous links (`javascript:`, `data:`) render as plain text instead of being clickable.
- **Markdown tiles got the full GitHub-flavored polish.** `[!NOTE]`/`[!TIP]`/`[!IMPORTANT]`/`[!WARNING]`/`[!CAUTION]` callouts render as tinted alert blocks with icons, task lists show real checkboxes, wide tables scroll in place with header and hover tinting, and images referenced by relative paths load inline — click one for a full-screen lightbox (Escape or click-out to close). Prose gets smart quotes, dashes, and `:emoji:` shortcodes (command-line `--flags` are left alone), inline HTML like collapsible `<details>` blocks renders safely, and live reload no longer re-renders a document whose content hasn't changed.

### Fixed
- **Opening a new session no longer intermittently fails with "Session spawn arguments were not prepared."** When several sessions were busy, a status update arriving mid-create could drop the just-created session before its terminal was launched, aborting the create and rolling it back. The session's launch details are now captured the instant it's created, so a concurrent update can't derail it.
- **Copilot sessions no longer get stuck mid-selection or with scroll landing in the wrong place after a reload.** Copilot's TUI independently enables its own mouse tracking, and a dropped mouse-release event (e.g. releasing outside the terminal pane) could leave it believing the button was still held, producing a runaway text selection or desynced scrolling after a refresh. attn now launches Copilot with mouse support disabled since attn already owns selection and scrolling itself.
- **Commands sent to remote (SSH) endpoints work again.** The hub's connection to a remote endpoint never introduced itself to the remote daemon, which therefore rejected every command forwarded over it — registering workspaces, spawning sessions, and other remote actions failed and dropped the connection. The hub now performs the required handshake as soon as it connects.
- **The chief of staff keeps reporting agent activity even when you say you're driving.** Its guidance framed a recommended next step as a move the chief itself was staging for your approval, so "I'm driving / don't intervene" read as "your move is rejected — go quiet," and it would stop relaying what agents reported. A recommendation is now clearly advice for you to act on, not an action the chief holds pending sign-off, so it keeps surfacing regardless of who acts.

## [2026-07-14]

### Added
- **Cmd+click a `.md` path in a session terminal to open it in a markdown tile.** Instead of launching an external app, markdown files now open right in the workspace, docked beside your terminals with live reload.
- **Multiple markdown tiles.** Each file gets its own tile, so you can keep several documents open side by side. Opening a file that's already showing reuses its tile instead of stacking a duplicate, and every markdown tile remembers which session it was opened from.

## [2026-07-13]

### Fixed
- **The Present review window opens instantly on large changesets.** The tour used to show "Loading tour…" until every file's diff had been fetched and parsed, which took several seconds on big diffs. It now opens immediately — each file appears as a lightweight card that fills in with its diff as it loads, so you can start reading the first file right away.
- **The Present review window no longer lags on large changesets.** Typing a comment re-parsed and re-rendered every file's diff on each keystroke, making characters take a second or more to appear and J/K navigation land on the wrong file. Each file now only re-renders when its own content or comments change, so typing echoes instantly and navigation tracks keypresses correctly.
- **Ticket nudges now submit in Codex instead of stopping in the composer.** The
  bounded inbox prompt is explicitly framed before Enter, so the nudge starts a
  turn while retaining the approval-prompt safety fence.
- **Terminal content stays inside its pane even when the editable input host gains browser-managed content.** The rendered canvas is now anchored to the pane origin, preventing composition or editing layout from pushing its bottom rows offscreen.

## [2026-07-12]

### Added
- **Find in a note.** ⌘F inside the Notebook editor opens an in-editor search panel (matches highlighted, Enter/⇧Enter to step). Esc closes the panel first; ⌘F still opens terminal search when you're not in a text field.
- **Richer live-preview rendering in the Notebook editor.** Code fences now get language syntax highlighting, and blockquotes and horizontal rules render styled instead of as raw markers.
- **Markdown tables in the Notebook editor** now render as real tables, with raw source revealed for editing when the cursor is inside (click a row to edit it).
- **⌘B/⌘I/⌘E toggle bold, italic, and inline code in the Notebook editor.** Wraps the selection or the word under the cursor; press again to remove.
- **Images now render inline in the Notebook editor.** A `![alt](path)` on its own line shows the picture itself, with a "not found" placeholder if the file can't be located; click the image to see (and edit) its raw markdown.

### Fixed
- **Terminal panes no longer get stuck at a stale size.** Some panes could stay rendered at a stale, historical size after a session reattach — bottom or right edges painted outside the pane — until the pane was reopened. They now recover on their own.
- Terminals that still end up painted outside their pane now self-heal within seconds — the app detects the mismatch and refits the terminal automatically, and records what it repaired.
- **Undo and redo now work in the Notebook editor.** ⌘Z and ⇧⌘Z were swallowed by the native Edit menu in the packaged app and never reached the editor; they now undo/redo your edits when the editor has focus. ⇧⌘Z still zooms the active pane when you're not in a text field.
- **⌘⌥N and ⇧⌘⌥N open the Notebook again.** With Option held, macOS reports a dead-key character instead of the letter, so the notebook shortcuts never matched in the installed app and the keystroke fell through into the terminal. They now match on the physical key.
- **Three Notebook editor interaction fixes.** The "Send to chief" pill now hangs below your selection instead of covering the line above it; typing works immediately after resolving a save conflict, with no extra click needed; and a note's YAML frontmatter no longer shows stray markdown list bullets while you're editing it.
- **Relative links between notes now work in the Notebook editor.** A link like `[sibling](foo.md)` now resolves against the linking note's own folder instead of being treated as an external URL, and `#heading` links jump the editor to that heading.
- **Shell panes no longer print `^[]11;rgb:...` garbage or break interactive prompts.** Tools that query the terminal's colors (`gh pr create`'s template chooser, `glow`, and others) now get an instant answer from the session itself, instead of racing the app to respond.

## [2026-07-11]

### Added
- **OpenCode can now run as an installable attn agent.** The server-backed `attn-opencode` plugin launches an interactive OpenCode TUI inside attn, using OpenCode's configured defaults for ordinary sessions and an authenticated local server for delegated prompts. It retains the native session for resume and keeps attn’s working/idle state in sync. Delegations can pin the OpenCode provider/model and the contract-tested `low` or `max` variant, including isolated concurrent runs.
- **OpenCode sessions now surface native questions and permissions in attn.** Question-tool requests show as waiting for input, permission requests show as pending approval, and reconnecting recovers either state from OpenCode before declaring the session idle.

### Fixed
- **Closed sessions no longer leave workspace tiles that reappear in the sidebar.** Closing now removes the pane before announcing the session's departure, stale agent panes cannot be moved into another workspace, and startup repairs any orphan panes left by older runs.
- **Ticket nudges now reach active, new, and unknown sessions too.** Any live chief or delegated agent receives the same bounded ticket nudge unless it is waiting for an approval prompt, where the doorbell could answer on its behalf. Claude's optional inbox watch can still consume an update before the countdown fires.

### Changed
- **Ticket artifacts use attachment language throughout.** `attn ticket attach` now names the durable operation for adding Markdown artifacts to a ticket; its multi-file, optional state/comment, retry-safe receipt, and canonical Notebook behavior are unchanged.
- **Agent guidance now distinguishes native subagents from user-steered delegations.** “Subagent” consistently means a native worker that reports to the calling agent, while `attn delegate` creates a visible session the user can inspect and steer directly. Requests to delegate or dispatch subagents stay on the native subagent path.

### Changed
- **Ticket artifacts use attachment language throughout.** `attn ticket attach` now names the durable operation for adding Markdown artifacts to a ticket; its multi-file, optional state/comment, retry-safe receipt, and canonical Notebook behavior are unchanged.

## [2026-07-10]

### Added
- **Durable ticket attachments keep plans alive after the producing agent stops.** `attn ticket attach` copies one or more Markdown files into a visible `tickets/<ticket-id>/` Notebook directory, can change ticket state in the same operation, records a retry-safe attachment receipt, and returns the canonical paths. Ticket reads derive their current artifact list directly from that directory, so ordinary edits, renames, and deletions are reflected without reconciliation. The ticket panel can attach files, open them in the Notebook editor, copy their paths, rename them, and delete them. Chiefs and delegated agents receive role-specific guidance for continuing work from the same canonical plan.

### Fixed
- **The Present window's summary card now actually collapses when you scroll the code.** Wheel-scrolling the diff — including wheel gestures over the card itself — folds the summary to a slim strip, which you can also collapse or expand by hand at any time; previously, scrolling never folded it.

---

## [2026-07-09]

### Changed
- **The Present review window reclaims vertical space for the diff.** The title, kind, repo path, round info, drift notice, and comment count now share one compact line at the top instead of a tall stacked header. The summary card folds away once you scroll into the tour and reappears when you jump back to the Summary stop, and the redundant "End of tour" strip — which just repeated the drive bar's reviewed count — is gone.

### Fixed
- **Reloading a crashed session moves its ticket back to Working automatically.** When a delegated session died mid-run its ticket was stamped Crashed — but bringing the session back to life (reloading the dead pane, resuming the ticket, or the daemon re-adopting a still-live worker after a restart) left the ticket sitting in the Crashed column until you moved it by hand. Reviving the session now flips the ticket back to Working on its own, and crash detection is re-armed so a later genuine crash is still stamped. Intentional closes are still never treated as crashes.

---

## [2026-07-08]

### Fixed
- **Your chief keeps every delegated ticket when you switch chief sessions.** Unread ticket activity now belongs to the durable chief-of-staff role instead of the session that happened to create the ticket. The new chief picks up at the same unread position and receives future nudges, while the retired chief stops receiving role notifications. Existing active delegations are carried forward automatically.
- **Present reader polish.** The keyboard (`K`) now reaches the summary stop from the first file, the summary is the initial stop when a round opens, and jumping to an annotation in a far file no longer under-scrolls on the first press.
- **Closing a delegated session no longer marks its ticket as Crashed.** Closing an agent's pane (or tearing down its workspace) while the session still looked busy used to be treated like a process death: the bound ticket — often already sitting In Review with finished work — was stamped Crashed. An intentional close now leaves the ticket exactly where the agent last reported it, and the reconciliation verdict still posts, framed as a clean close. The close is also remembered durably, so it is honored even when the reconciliation runs late — after a daemon restart, for example. Genuine unexpected agent deaths are still detected and stamped Crashed exactly as before.

### Added
- **`attn present --wait` blocks until the review is handed back and prints the feedback inline.** An authoring agent can run it as a foreground call so its turn stays alive (the session shows as working, not falsely idle) until the reviewer submits the round, with the feedback markdown printed straight to stdout — no separate `attn present feedback` step needed. If the reviewer closes the presentation instead of reviewing, `--wait` also returns immediately with a note that the drafts were discarded, rather than blocking forever on a handback that will never arrive.
- **`attn ticket status` can now move any ticket by id.** Add `--ticket <id>` to move a ticket other than your own bound one — from any session, no ownership required. The existing form (no `--ticket`) still just moves your own bound ticket, unchanged.
- **Present now tracks your review progress and gives you a fuller keyboard model.** Mark a file reviewed with `R`, or just keep moving with `J`/`K` — leaving a file with `J` marks it reviewed automatically, jaunt-style. Progress shows in the file rail (a header count and thin progress bar, plus a checkmark and dimmed styling on reviewed rows) and in a new bottom drive bar with its own progress meter, keyboard hints, and the "Submit review" button (now bound to `S`). Each file's diff header also gets its own Reviewed toggle. The submit dialog lists any files you haven't walked yet as an advisory note — it never blocks submission. Marks are saved locally per round, so they survive a window reload but start fresh on a new round.
- **Present's file rail is more informative.** Each file row now shows its line change count (`+added`/`−removed`) and a chip with its comment count, when it has any. A new pinned "Summary" row at the top of the rail jumps you back to the round's summary card.
- **Present now groups files so big rounds stay navigable.** The rail splits into Tour, Other, and Skipped sections. "Other" covers files the round actually changed but the presenter didn't call out in the tour — they're appended to the end of the document as regular diff cards, with full stats and comments like any tour file. Skipped files render de-emphasized at the very end. Only Tour and Other files count toward review progress and the submit dialog's coverage note; skipped files are excluded from both.
- **Present manifests can now pin author annotations to specific lines.** A file entry can add `annotations`, each pinning a note or an ordered thread to a substring anchor, a single line, or a line range. attn resolves anchors against the pinned head commit when the presentation opens, so a broken anchor is caught immediately instead of silently failing later — `attn present validate` checks anchors locally too. An anchor that matches more than one line still opens, with a warning naming the ambiguity.
- **Agents now get built-in guidance for authoring Present reviews.** The attn skill gained a present reference covering manifest authoring, voice, reading order, annotations, and the validate/open/feedback loop.
- **Author annotations now render inline in the Present reader.** They show up as read-only comment threads right at their pinned line, ahead of any reviewer comments at the same spot, and count toward the file rail's comment chip. A "Reply" button lets you answer one without hunting for the gutter control. An annotation pinned to a line outside the visible diff still shows up, re-anchored to the nearest visible line with a small caption noting where it actually points. `N`/`P` jump between every annotation in the round, in document order.
- **Mermaid diagrams now render in presentation summaries, file notes, and annotation/comment bodies.** A fenced ` ```mermaid ` code block in a round summary, a file note, or a review/annotation comment now draws as a diagram instead of raw text, matching the app's light or dark theme; a bad diagram falls back to showing the original code rather than breaking the view. Ticket descriptions and comments also now render as markdown.
- **Present's submit dialog now offers Approve, Submit feedback, and Close.** Approve hands the round back marked approved (comments are still welcome alongside — approve-with-nits) and marks the presentation approved. Submit feedback is the existing handback, now explicitly recorded as feedback rather than approval. Close lets you dismiss a presentation without reviewing it — it discards any draft comments and closes the presentation with no handback to the author. Opening a new round on an approved or closed presentation reopens it.
- **Mermaid diagrams now render in notebook and dock markdown tiles.** A fenced ` ```mermaid ` code block in a markdown tile now draws as a diagram instead of raw text, matching notebook/dock tiles onto the same shared markdown renderer used elsewhere in the app.

### Fixed
- **Codex chiefs now wake reliably when delegated work reports back.** Claude chiefs still monitor ticket updates live, while Codex chiefs rely on attn's existing ticket nudge instead of starting a background watcher that consumed the update without waking their turn.

## [2026-07-07]

### Changed
- **Present is now a continuous tour, not a file-by-file viewer.** The Present window renders every file in the round as one scrollable sequence, in the presenter's reading order — scroll (or click a file in the rail) to move through the whole change set instead of clicking each file to swap the view. Per-file presenter notes appear as callouts above their file, the round summary stays pinned at the top, and an end-of-tour footer confirms how many files you've reviewed. Inline commenting works exactly as before, including multiple open comment boxes across different files at once.

### Added
- **Comment boxes in Present are now GitHub-style.** Multiple comments can now be open at once: the hover "+" button keeps working while a draft is open, each comment saves or cancels independently, and Escape closes the most recently opened box first.
- **The terminal now opens hidden-URL hyperlinks with Cmd+click.** Label-style links like Claude Code's "Learn more" — where the visible text gives no hint of the destination — now open the URL hidden behind them, including links whose target is a file path.

### Fixed
- **The Present window now opens scrolled to the top.** A freshly opened diff no longer arrives scrolled away from the beginning, and the first click won't jump and misplace a comment — the diff stays anchored until you scroll it.
- **Copilot sessions now receive the attn skill.** The bundled attn skill (delegation, tickets, workspace context, notebook, workflow, chief-of-staff guidance) was only ever installed to `~/.claude/skills/attn` and `~/.agents/skills/attn`, so a Copilot-driven session had no way to learn what "chief of staff" or the rest of attn's vocabulary meant. It's now also installed to `~/.copilot/skills/attn`, kept identical to the Claude/Codex copies and pruned of stale files the same way.
- **Selecting terminal text no longer occasionally gets stuck extending.** A drag-selection relied on the mouse button's release event reaching the terminal to stop, plus a `buttons`-bitmask check as a backstop. If the release happened outside the app's window (alt-tabbing away mid-drag, releasing over a native dialog, a context menu interrupting the gesture) that bitmask could go stale, so the selection kept growing on the next mouse movement even with no button held. Losing window focus or an interrupted pointer gesture now force-finalizes the selection immediately, matching the same safeguard already used for pane drag-and-drop.

## [2026-07-05]

### Removed
- **Standalone diff-review panels and comment popups are gone — review now happens in the Present window.** The diff changes panel (⌘⇧G) and its detail panel (⌘⇧E), which held the inline review-comment UI (create, edit, resolve, delete), have been removed from the main window. The comment popup that appeared on diff clicks is gone too. Guided review already lives in the Present window (shipped earlier), so this is a cleanup of dead code — no loss of capability.

### Added
- **Ticket chip on the agent pane.** A delegated agent's pane header now carries a chip for its bound ticket — the ticket title, a status-colored marker, and an unread-activity dot. Clicking it opens the full ticket over the terminal so you can change status, add a comment, edit the description, or resume the agent right there; clicking the chip again, pressing Escape, or closing the panel returns you to the terminal. Steering an agent by editing its ticket is now one click instead of a trip through the ticket board.
- **`attn ticket show <ticket-id>` for agents.** Agents can now pull a ticket's full record over the socket — description, the complete activity thread (status changes, comments, verdicts) with full bodies, and attachments — the same detail the app's ticket panel shows, but non-consuming: unlike `ticket inbox`, it never advances any session's unread cursor, so it can be re-read any time.
- **Delegate: `--cwd` and `--worktree` now compose.** `attn delegate --cwd <dir> --worktree <branch>` creates a worktree of the repo at `<dir>` and places the new workspace there, instead of being rejected. Over-long auto-derived names (e.g. from a worktree folder like `attn--feat-some-long-branch`) are now truncated instead of failing the delegation; an explicit `--name` that is too long still errors as before.
- **`attn journal append` CLI command.** Appends an entry to the notebook's daily journal (`journal/<date>.md`) through the daemon instead of editing the file directly, so an agent (or the chief of staff) can add a journal entry without racing the daemon's own journal writes.
- **Ticket inbox now reports when the user was last active in the app**, so watching agents can decide whether to notify or hold.
- **Reconciliation verdicts now get a ground-truth check on the PRs they mention.** When an orphaned-ticket reconciliation verdict mentions a pull request that turns out to be already merged or closed — checked against attn's tracked PRs, with a quick GitHub lookup for ones attn isn't tracking — the posted comment gets an extra "Ground-truth check" line flagging that the verdict text may be stale on that point. A verdict claiming "merge pending" hours after the PR actually merged no longer stands unchallenged.

### Fixed
- **Resuming a ticket whose session was closed now reliably reopens the agent.** Pressing **Resume** on a ticket whose bound session had been closed (or after an app restart) could fail with "Session spawn arguments were not prepared." and roll the whole operation back — a race between the frontend seeding the new session and the daemon's own state broadcasts. The daemon now owns the entire resume, so Resume reliably reopens the agent in the ticket's directory with its prior conversation restored. If the session is still open, Resume just focuses it instead of opening a duplicate.
- **Reloading an agent no longer crashes its ticket.** Reloading a delegated agent's session (session actions → Reload) used to look like a process death to the daemon: the bound ticket was stamped Crashed and a pointless reconciliation verdict was posted against a perfectly healthy session. A reload is now a recognized lifecycle transition — the ticket stays in its column and no reconciliation runs. Real crashes are still detected exactly as before.
- **Reload no longer races the pane teardown.** The reload's own kill could be mistaken for the agent quitting cleanly, closing the pane (and its workspace) out from under the respawn and failing the reload with "unknown workspace". The session now stays put for the whole kill → respawn window.
- **The presentation review window now closes when you submit.** Submitting a review left the window open; it now closes automatically once your review is handed back.

## [2026-07-04]

### Added
- **`attn debug` CLI.** New `attn debug ls|incidents|diagnostics|daemon-log` subcommands read the frontend's disk-based debug logs and the daemon log for the active profile, with `--tail`, `--grep`, and (for `daemon-log`) `--since` filters — no more hand-rolled `cd`+`tail`+`jq` into `~/Library/Application Support/com.attn.manager*/debug` and `daemon.log`.
- **`attn vision-check <image> <question>` CLI command.** Answers a question about a screenshot or image with a single, tool-less LLM call, so an agent can ask about an image without pulling it into its own context.
- **Pin the chief-of-staff's reasoning effort, per agent.** Settings → Agents → "Chief-of-staff model & effort" now has an effort selector next to each agent's model override (Claude: low, medium, high, xhigh, max; Codex: minimal, low, medium, high, xhigh). Leave it on "Agent default" to use the agent's own default. Only chief-of-staff launches are affected — regular sessions are unaffected.

### Fixed
- **Changing font size no longer risks blank or misrendered terminals when many panes are open.** Adjusting the terminal font size used to rebuild every open pane's terminal (visible and backgrounded), which under enough open panes could exhaust the app's GPU rendering resources and leave a pane permanently blank or garbled until reopened. Terminals now update in place to the new font size instead of being rebuilt.
- **Terminals now recover automatically if the app's GPU rendering context is lost.** Previously a pane hit by a GPU context loss showed a permanent error asking you to reopen it; it now rebuilds its renderer in place within a fraction of a second, keeping the session's content and scrollback.
- **Switching back to a workspace after resizing the window no longer leaves the terminal cut off at the bottom.** If the window shrank while a workspace was in the background, revealing it could land an oversized terminal grid that never corrected itself; the app now detects and fixes this right after the workspace becomes visible.

## [2026-07-03]

### Added
- **Notifications when background work fails.** attn now has a notifications feed — the bell in the sidebar, with an unread count — that tells you when a background task gives up after exhausting its retries (context compaction, session summaries, workspace narration, or ticket reconciliation). Open a notification to read what was running and the underlying error, and retry it right there. Notifications persist across restarts and are marked read as you view them (with a "Mark all read").
- **Background tasks moved to Settings.** The durable task runner's list — every queued, running, failed, or dead background task with its attempt count, next attempt, last error, and a Retry — now lives in its own **Settings → Background Tasks** section. It used to hang off the Notebook's sidebar, but these tasks are a global concern, not a notebook one, so the Notebook's Tasks section is gone.

### Fixed
- **Orphaned-ticket reconciliation no longer fails on large transcripts.** The background classifier that judges a dead session's work now builds its verdict from a small, deterministically extracted slice of the conversation (the original request, any re-scoping, and the agent's last status turns) inlined into a single cheap model call, instead of agentically reading the whole transcript file. A multi-megabyte transcript used to blow the run's budget cap and post a misleading failure note; now it costs the same as any other.

## [2026-07-02]

### Added
- **Font size settings, with an independent taskboard size.** Settings → General → Appearance now has Font Size controls: an App size (the same scale ⌘+ / ⌘− adjust, now visible and adjustable in one place) and a separate Taskboard size for the ticket board and ticket details. The taskboard matches the app size until you change it, and a "Match app" button reverts it. Both persist across restarts.
- **Cap the context window for the chief of staff and headless runs.** Two new settings under Settings → Agents ("Context window caps") let you set the token threshold at which auto-compaction kicks in for the chief-of-staff session and for headless runs (keeper narration, ticket reconciliation, workflow subagents), instead of waiting for the model's full window. Both default to 128,000 tokens. This keeps the event-driven chief cheaper on every cache-cold wake — its durable state lives on disk, so compaction is nearly free — and keeps one-shot background runs from ballooning. Regular delegated interactive sessions are never capped: their in-context state is yours to keep. Works for both Claude and Codex agents.

### Fixed
- **The orphaned-ticket verdict no longer breaks on your MCP setup — and when it does fail, it tells you why.** The background classifier that judges a dead session's work was inheriting the user's claude.ai account connectors (Slack, Gmail, Drive, Calendar), so a connector that was slow or needed re-authentication could sink an otherwise-healthy run; classifier runs are now fully isolated from user MCP configuration (they only read the dead session's transcript). And a failed run's "could not determine the outcome" comment now includes the actual error output instead of an opaque generic label, so a failure is diagnosable from the ticket itself. The classifier is also told to judge against what you explicitly re-scoped or authorized in-session, not just the original brief's wording.
- **You can post comments from the ticket detail panel again.** The panel's comment box had no usable way to submit — the "Add comment" button was rendered in an undefined accent color that painted it invisibly against the panel, and there was no keyboard shortcut — so a comment typed in the app couldn't be posted (only the `attn ticket comment` CLI worked). The button is now visible, and **⌘Return** (Ctrl+Return off-mac) posts the comment without leaving the field; plain Enter still adds a newline. The button stays disabled until you've typed something and while a post is in flight, clears on success, and shows an error if the post fails. The same missing accent/error colors also left ticket status accents and error text uncolored throughout the panel; those now render as intended.

## [2026-07-01]

### Added
- **The board now tells you when a ticket lost its agent — and what the agent left behind.** When a session dies while its ticket is still open (in any column short of Done/Failed/Crashed), attn no longer leaves the board silently wrong. The ticket gets a red "orphaned" badge on its board card and in its detail panel, and a small background classifier reads the dead session's transcript against the ticket's brief and posts a verdict comment on the ticket: whether the work looks done, partially done, interrupted, or blocked — with what's left and the evidence. Your chief of staff is notified through the usual ticket inbox. Nothing moves columns automatically: crashes still get their Crashed stamp (now with a verdict comment explaining what was in flight), and everything else stays put for you or your chief to decide. If the outcome can't be determined (missing transcript, classifier failure), the ticket says so instead of staying silent. The badge clears on its own when the ticket is reassigned or its agent session is resumed. A periodic sweep also catches orphans from before this feature or from daemon restarts.
- **Pin a delegated agent's model and effort.** `attn delegate` accepts `--model <alias|id>` and `--effort <level>` to launch that one delegation with a specific model and reasoning effort (Claude's native `--effort` levels, Codex's `model_reasoning_effort`), instead of silently inheriting the machine's default. Omit them and nothing changes; agents without a native mechanism (e.g. Copilot) reject the flags up front.

### Removed
- **The automated review loop is gone.** The standalone review-loop feature — the SDK-managed iterating loop, its session sidebar bar, `attn review-loop` CLI, and related settings — has been removed. Diff comments, PR reviews, and the workflow engine are unaffected.

## [2026-06-30]

### Fixed
- **A delegated agent stays focused on its own task instead of handing it off again.** A delegated agent could misread attn's guidance as a license to delegate onward — spinning up a *third* agent for one of its own subtasks, in a session you weren't watching. Now every delegated session is told up front that it's a worker, not a coordinator: it does the assigned work itself and uses its own internal helpers for subtasks, only creating a new visible attn agent if you explicitly ask it to. (attn also now cleans up stale guidance files from older installs that could keep teaching the old behavior.)

## [2026-06-29]

### Added
- **Run agents unattended, and pin your chief's model.** Settings › Agents adds two controls. **Auto-approve** launches managed agents in their native auto-approve mode so they keep working without stopping at every permission prompt — off by default, and yolo sessions already bypass approvals regardless. **Chief-of-staff model** pins the model a chief-of-staff session launches with, per agent (Claude or Codex); leave it blank to use the agent's own default. Both apply to sessions started afterward.
- **Agents can read the board and comment on any ticket.** Agents can now see every ticket, not just their own: `attn ticket list` prints the board (id, column, assignee, title; `--json` adds each ticket's description), which is how a chief surveys all its threads and finds a ticket's id. With an id in hand, `attn ticket comment <ticket-id> -m "<note>"` leaves a one-shot note on any ticket — not just the one an agent is working — so a delegate can chime in on a sibling's work or flag something for the chief. The ticket's people (its assignee and your chief) are notified, but commenting does not subscribe the commenter: it won't then be pinged about that ticket's future activity.
- **Agents can subscribe to a ticket to follow it.** `attn ticket subscribe <ticket-id>` opts an agent into a ticket's notifications — the standing-interest counterpart to a one-shot comment — so future activity on a ticket it doesn't own nudges it and lands in its inbox (and the first inbox after subscribing also catches it up on the ticket's history). Useful for a chief tracking a thread it didn't create, or an agent whose work depends on another's. `attn ticket unsubscribe <ticket-id>` opts back out.
- **Agents can take a ticket.** `attn ticket take <ticket-id>` claims a ticket — the agent becomes its assignee — for picking up an unassigned backlog ticket or handing work to itself. Taking one already assigned to someone else requires `--confirm`, so an agent can't silently take over a sibling's active work; without it the command refuses and names the current assignee. The displaced assignee is notified of the handover, and the taker's next inbox catches it up on the ticket's history.

### Changed
- **Ticket nudges now wait, show themselves, and never land mid-sentence.** When an idle agent has unread ticket activity, attn no longer types the nudge into it instantly. Instead a short countdown (default 30s) runs first, shown as a bar on that session's sidebar row and a strip across its tile, so you can see a nudge coming. The session you're currently looking at never auto-fires — its countdown pauses, and switching away resumes it — and a nudge is never typed into a session you've touched in the last few seconds, so it can no longer splice onto a prompt you're still typing and submit the garbled line. The automatic countdown only ever runs while an agent is idle, but the session you're focused on always offers a "deliver now" button so you can hand it the nudge on demand whatever it's doing — including a busy or just-restarted agent. The one hold-back is an agent stopped at an approval prompt, where the nudge's Enter could answer the prompt, so there it stays a quiet "unread activity" marker.
- **Delegated agents confirm with you before completing a ticket.** A delegated agent now treats closing its ticket as your call: when it believes the work is done it asks you to confirm and waits for your go-ahead before reporting `completed`, instead of closing the ticket on its own. It still reports the other states (in progress, needs input, ready for review, failed) as they happen.

### Fixed
- **Delegated agents no longer nudge themselves about their own brief.** A freshly delegated agent already receives its brief when it starts, so it no longer gets a spurious "new ticket activity — check your inbox" prompt about that same brief the moment it goes idle. Agents are now only nudged about genuine new activity on their own ticket (a steer, or a status change they didn't make).

## [2026-06-28]

### Added
- **Create backlog tickets without delegating.** `attn ticket new --title <t> [--description <d>] [--id <slug>]` files an unbound ticket in the Todo column — for capturing work you're not handing to an agent yet. Agents now know that tickets are something they can create on your request.
- **Start a session as your chief of staff.** The new-session/workspace dialog has a "create as chief of staff" toggle — shown only when you don't already have a chief, and only for Claude and Codex — so a brand-new agent launches already running the chief operating guidance, instead of creating it first and then promoting it.

### Changed
- **Your chief of staff reacts the moment delegated work changes.** Previously, when an agent the chief delegated finished, got blocked, or crashed, the chief found out only the next time you prompted it — the update sat unseen on the board. Now the chief watches its delegated tickets and surfaces a completion, a question, or a failure to you proactively, with a recommended next step, instead of going quiet after handing work off. As a safety net, attn also pokes an idle agent that has unread ticket activity it isn't already watching, so nothing stays silently unread.
- **Making a running session your chief of staff now takes effect immediately.** Turning the chief role on (or off) reloads that agent in place — keeping its conversation intact — so the chief operating guidance actually applies to the running agent right away, instead of taking hold only the next time it happened to restart.

## [2026-06-27]

### Added
- **Track delegated work as tickets, on a board.** Delegating an agent now opens a durable **ticket** bound to that session, so handed-off work has a tracked home instead of vanishing into a running agent. The agent reports its own progress — working, needs input, ready for review, done, or failed — with `attn ticket status`, and you watch it move on a new full-window **ticket board** (open it from the sidebar, the ⌘K menu, or ⌘⇧T): columns for Todo · Working · Blocked · In Review · Done, with filters for what's **blocked**, **in review**, or **closed today**, and finished-bad work (failed or crashed) collected in a Closed lane under Done. Open any ticket to see its description, status, full history, and attachments updating live; from there you can comment, re-brief the agent, change its status, **resume** a stopped session right where it left off, and pick up files the agent handed over with `attn ticket attach`. Your chief of staff runs this hands-off: once it has delegated, recorded the work, and reported back, it follows progress on the board instead of hovering over a working agent — surfacing what an agent reports and teeing up the next step for you to decide, rather than signing off on the work itself. This replaces the former `attn dispatch` commands and their separate coordination store; delegating works as before, it just opens a ticket now.

### Fixed
- **Keyboard shortcuts work on the dashboard and empty workspaces again.** When no terminal was on screen — the home dashboard, or a pinned/empty workspace — the app could miss keystrokes until you clicked into the window, so shortcuts (even ⌘K) did nothing. The app now keeps keyboard focus on those views, so shortcuts respond immediately on launch and after switching back to the app.
- **A terminal's last rows no longer get cut off outside the window.** Under some conditions — most often after splitting a workspace into several stacked panes, or making the window short — the bottom one or two lines of a terminal could render past the edge of its pane and stay there: invisible, and impossible to scroll into view, until you restarted the app or cycled away and back to the workspace. A safeguard meant to ignore transient/garbage size measurements was also rejecting legitimately small panes, leaving the terminal larger than the space it had. It now keeps the terminal sized to its pane so every row stays on screen (the same fix covers the right-most columns being clipped in very narrow side-by-side splits).

## [2026-06-26]

### Fixed
- **`attn list` now shows pinned workspaces even when they are empty.** The command returns workspace snapshots alongside live sessions, so chief-of-staff agents can see a pinned workspace's ID, name, directory, and pinned state before delegating into it instead of creating a duplicate workspace.

## [2026-06-24]

### Added
- **Pin workspaces to keep them in the sidebar even when empty.** A workspace can now be toggled pinned/unpinned from the sidebar. A pinned workspace survives its last session closing and daemon restarts — it stays visible and ready for a new agent launch. Existing tile-only and context-based workspace retention continues to work alongside pinning.

### Changed
- **Delegated agents and their workspaces now get real, readable names.** `attn delegate` takes `--name` (replacing `--label`); it names the agent and, when the delegation creates a new workspace, the workspace too — so a delegated workspace shows a human name in the sidebar instead of its worktree folder. Omit `--name` to default to the directory name. Names are capped at 16 characters and must be unique (a workspace name across all workspaces, a session name within its workspace); when a name is too long or already taken — including a too-long worktree-folder default — the command fails with a clear message telling you to pass `--name`.
- **Delegated agents now report explicit outcomes instead of assembling coordination JSON.** The new `attn dispatch update`, `block`, `review`, `complete`, and `fail` commands capture the work state directly, keep concise summaries separate from optional detailed reports, check unread chief messages before handoffs, and return a short confirmation instead of echoing the entire assignment. Notebook handoffs likewise require an explicit `--review` or `--complete` outcome.

### Removed
- **Removed `attn dispatch report` and `--coordination-file`.** Raw coordination envelopes and unstructured reports are no longer accepted; every delegated-agent update now has a typed outcome.

## [2026-06-23]

### Added
- **The chief of staff can now delegate to GitHub Copilot.** Copilot joins Claude and Codex as a delegation target: the chief spins up a Copilot session with the brief, and the agent starts in an interactive session that runs the brief and stays open for follow-up steering. Ordinary (non-delegated) Copilot sessions are unaffected.

### Changed
- **A delegated agent that crashes or is killed mid-work now surfaces as a failure to its chief.** When a chief-of-staff dispatch's session ends, attn now tells a clean stop from a real crash: a session cut off while it was still working, starting up, or waiting on a tool approval is reported as a failure (`[failed]`), while a session that closed from a settled rest — or one attn couldn't classify — stays the neutral "ended" introduced with `dispatch watch`. attn still never claims a success the agent didn't report; this only makes a genuine crash visible instead of letting it look like an uneventful end.
- **Your chief of staff no longer looks busy while it's only watching.** When the chief of staff arms a background monitor to watch the agents it delegated (or a background polling loop), attn used to show it as actively working — a solid green/active dot — even while it was just waiting for something to happen. A chief watching several delegations therefore looked permanently busy, and the "is the chief actually working?" glance lost its meaning. The chief now settles back to idle once its turn ends with only background watching left (or stays the quiet "scheduled" blue if it's parked on a timed wake-up), and shows as working only when it's genuinely doing something. This applies to the chief of staff only; every other session behaves as before.

### Fixed
- **Long Notebook notes keep their rendered formatting and cursor position while you edit.** Moving the cursor beyond the first few thousand characters no longer turns the rest of a note back into raw Markdown or sends ArrowUp to the properties card. Clicking into the body also leaves the frontmatter card stable; raw YAML appears only when you click the card to edit it.
- **Chief-of-staff delegations no longer disappear into muted workspaces.** Delegating into an existing muted workspace now brings that workspace back into the sidebar, and `attn list` marks sessions whose workspace is muted so the chief can see that state before choosing a target.
- **Delegated Codex agents keep their attn identity when working in child directories.** An agent launched in a shared parent directory could lose its session ID and attn wrapper path after running tools from a branch-specific child worktree, so tracked reports, inbox access, and workspace-context publication failed with "no session." Managed Codex launches now preserve the correct session routing across tool working directories and workspace placement modes.

## [2026-06-22]

### Added
- **See which sessions your chief of staff started.** Sessions the chief of staff delegated now carry a small "↳" marker in the sidebar, next to where the chief's own badge sits — so at a glance you can tell the agents the chief spun up from the ones you started yourself. Only delegations that came from the chief get it; a plain hand-off between two regular sessions doesn't.
- **Delegated agents can hand a large artifact back through the Notebook.** A delegated agent's report to its chief is meant to be a short update, so an agent that built up a full report or document with you had no way to forward it. It can now stash that artifact in the Notebook and report back just a reference (`attn dispatch handoff --file <artifact> --to <notebook-path>`); the chief decides whether to leave it, move it, or promote it into the knowledge base. The agent only writes where the chief (or you) designated — it never invents a location.
- **`attn dispatch watch <id>` streams a dispatch's meaningful events and exits when it ends.** A new blocking command that prints one line per event worth waking a chief-of-staff agent — an explicit completion (with the agent's summary and next action), a reviewable handoff, a genuine blocker/decision-request, or an explicit failure — and stays silent for routine mid-work tool-permission prompts. It always emits and exits when a dispatch reaches a terminal state, so it never hangs; but silence implies neither success nor failure: a dispatch that simply stops without reporting an outcome surfaces as a neutral "ended", not a failure. It's the event primitive a chief watches per delegation instead of polling.

### Changed
- **The chief of staff is now blue.** The chief-of-staff badge (in the sidebar and the dashboard) and its related controls — the "Make chief of staff" action and the transfer confirmation — moved from the app's orange accent to a distinct blue, giving the chief its own identity and setting the new delegated-from-chief marker (which shares that blue) apart from everything else.

## [2026-06-21]

### Fixed
- **`⌘W` in a docked Notebook tile now closes the Notebook, not the terminal beside it.** When a Notebook tile sat next to a terminal pane, pressing `⌘W` while reading or editing a note closed the previously-focused terminal pane (ending that session) instead of the note you were looking at. `⌘W` now closes the focused tile.
- **The last line of a terminal is no longer cut off at the bottom of the window.** When a session's terminal was sized one row taller than the window could actually show — for example after the window's height landed on an awkward boundary, or when the session was opened at a size different from where its grid was last set — the bottom line (your prompt, or attn's `auto mode on` status line) got clipped at the window edge. The terminal now re-asserts a row count that fits the visible area, so the last line stays fully on screen at any window size.

### Added
- **Dock the Notebook beside your terminals.** The Notebook no longer only opens fullscreen — you can now dock it as a tile in a workspace, next to your panes, with `⌘⌥N` or the action menu ("Open Notebook tile"). It's the same live notebook — folder tree, live editor, context rail, send-to-chief — and it folds its side panels away as the tile narrows so a note still fits. Each tile remembers the note it was showing and reopens to it, you can have more than one tile open on different notes, and the layout survives restarts. (`⌘⌥⇧N` still opens the fullscreen view.)
- **Jump to any note from a Notebook tile with `⌘P`.** A fresh notebook tile opens straight into a fuzzy file finder — start typing and it matches notes by name or title across the whole notebook, even with scattered letters (e.g. "kbidx" finds `knowledge/index.md`). Arrow keys move, Enter (or a click) opens the note in that tile, and Esc drops you back to the folder tree. Already reading a note? `⌘P` re-summons the finder for just that tile.
- **Fold the Notebook's side panels away to focus on a note.** The file tree and a note's context rail each gained an edge handle — click it to collapse that panel to nothing so the note fills the width, and click again to bring it back. Folding keeps your place: the editor and your scroll position are preserved and nothing reloads.
- **The Notebook header tells you more at a glance.** A small "chief" indicator lights up while your chief-of-staff agent is working, and each note's title now carries a kind badge (note or journal).
- **Broken Notebook links are flagged as you write.** When a note links to another note that doesn't exist — a typo in the path, or a note you haven't created yet — the link now shows in red with a ⚠ instead of looking like a working link. Links to real notes stay normal, and external links (http/https, email) are never flagged. If an agent or you create the missing note, its links clear their flag once the Notebook refreshes.
- **Open the Notebook from the sidebar, and find notes with `⌘P` there too.** A Notebook button now sits in the sidebar toolbar (next to the diff and PRs buttons) to open the fullscreen Notebook — `⌘⌥⇧N` still works as well. And the `⌘P` fuzzy file finder, previously only in notebook tiles, now works in the fullscreen Notebook: press `⌘P` to jump to any note by name or title, with Esc closing the finder first (a second Esc closes the Notebook).
- **Your chief-of-staff session can't be closed by accident.** The chief of staff — the profile-wide orchestrator that coordinates your other agents — is now protected: `⌘W` and the close-session action no longer close it, so a stray keystroke or misclick can't tear it down. A brief hint reminds you it's protected; to close it deliberately, unset its chief-of-staff role first. Every other session still closes exactly as before.

### Changed
- **The command palette (`⌘K`) now lists only the Notebook tile.** "Browse the Notebook" was removed from `⌘K` in favor of the new sidebar button; "Open Notebook tile" stays.
- **New worktrees start from the latest origin/main by default.** When you create a worktree in the new-session dialog, "Start from origin/&lt;default branch&gt;" is now the pre-selected, leading choice — so your new branch is cut from a freshly-fetched upstream main, not from whatever branch the source location happened to be sitting on (which could be days stale). You can still pick "Start from &lt;current branch&gt;" to branch off your in-progress work. A repo with no matching remote branch falls back to the current checkout instead of failing.

### Fixed
- **A Notebook note no longer jumps to the top while you're reading it.** When a file changed in your Notebook — an agent editing the very note you're reading, or any unrelated background write — the note used to scroll back to the top and lose your place. Now your scroll position is kept: an unrelated change leaves the open note untouched, and when the note you're reading is updated on disk its new text is woven in without moving you.
- **attn's background AI runs no longer clutter your `~/.claude` folder.** The short, headless Claude calls attn makes in the background — classifying whether a session is waiting on you, the review loop, and the Notebook's session summaries and journal narration — were each leaving leftovers in Claude Code's `~/.claude` store: a full session transcript per classification/review run, and a brand-new throwaway project folder (with spilled tool output) on every Notebook summary. These one-shot runs now disable session persistence and share a single stable scratch folder, so they stop accumulating files there. attn never deletes anything inside `~/.claude`.
- **A finished session now ends cleanly instead of looking like a crash.** When a session is stopped, closed, or wraps up its work, attn tears it down with a normal termination signal — but the terminal used to report that as a bare `[Process exited with code 143]`, which reads like something broke. Expected endings (a clean exit, or a normal stop/close) now show a calm `[Session ended]`. Genuine failures — a non-zero exit code, or a crash/force-kill — still show the exit code so real problems stay visible.

## [2026-06-20]

### Changed
- **Read and edit Notebook notes in one place — the Edit mode is gone.** Opening a note now shows it as rendered markdown you can type straight into, the way Obsidian's live preview works: headings, **bold**, *italic*, `inline code`, and links render as you read, and the raw markdown of the line your cursor is on reveals itself so you can edit it. There's no more View/Edit toggle, and your changes save automatically (no Save button) — you still get a heads-up if the note changed on disk underneath you, and notes stay plain `.md` files.
- **The Notebook sidebar is now a real folder tree.** Instead of fixed groups, the sidebar mirrors the actual folders and files under your Notebook folder — expand and collapse directories to find anything, and the tree refreshes as agents and external edits land. You can now open and edit any text file there, not just `.md` notes: plain text files open in a simple editor that autosaves the same way, while images and other binary files show a "preview not available" placeholder for now. Markdown notes keep their live editor, backlinks, and "send to chief".
- **A markdown note now has a context rail — its outline and backlinks.** Open a note and a panel on the right lists its headings as an outline; click any heading to jump straight to it. The "linked from" backlinks moved off the bottom of the page into this rail too, each showing the linking note's title and path, and both sections fold away when you want more room. Plain-text and image files don't have a rail.
- **Notebook notes render lists, checkboxes, and code blocks inline.** Bullet lists show as real bullets, `- [ ]` / `- [x]` task items render as checkboxes you can click to tick off (it writes the change straight back to the note), and fenced code blocks render as a monospace panel — all in the same live editor, with the raw markdown still revealing on whatever line your cursor is on.
- **A note's frontmatter shows as a properties card, not raw YAML.** When a note opens with a `---` block, it now renders as a tidy card at the top — type, summary, tags, sources, and dates — instead of a wall of `key: value` lines. The note's title is its first `# heading`; the card carries the properties only, so a note has one title, not three. Click the card to drop into the raw YAML and edit it; click back into the note and the card returns. The file on disk stays exactly the same plain markdown.
- **A note's title is its first `# heading`.** Backlinks and note lists now show a note's real title — its leading `# heading` — instead of falling back to the filename. A note has one title (the heading); frontmatter holds properties only. Journals and the chief inbox already lead with a heading, so they're unaffected.

### Added
- **Choose where your Notebook folder lives.** Settings → General now has a Notebook Folder picker, so you can point attn's durable Notebook — your dated journals and knowledge base — at any folder you own instead of the default `~/attn-notebook` (separate per profile). Browse to a folder or type a path, and the picker shows where the Notebook currently resolves to; leave it blank to fall back to the default. attn starts using the new folder right away — your existing notes aren't moved, so move or sync the folder yourself if you want the current contents to come along.
- **Configure the keeper's background work — and switch it off with one toggle.** Settings → Agents now has a single Keeper section that gathers all of the keeper's background duties: session summaries, journal narration, and workspace-context compaction. Each duty has its own agent and model picker (with recommended defaults — Haiku for summaries, Sonnet for narration), so you can dial cost and quality per duty. A new **Background tasks** master switch at the top turns every keeper duty on or off together; while it's off the keeper queues and runs no background work, though your picks stay saved, and turning it off won't interrupt a run already in flight.

### Fixed
- **Clicking a Notebook note opens it instantly.** Switching notes no longer briefly leaves the previous note's text — or its "linked from" backlinks — on screen under the newly-selected one. The note you click now renders right away; its backlinks (which scan the whole notebook) show a brief "finding backlinks" state and fill in a moment later, instead of holding the content back or lingering from the note you just left.
- **Background work (journal entries, summaries, context compaction) no longer stalls behind a stuck task.** A leftover task record from an earlier version of attn could be picked up by the background worker but never completed, so it was re-run on a tight endless loop — spiking CPU, flooding logs, and starving every other queued job behind it. attn now ignores these stale, unrunnable records so the queue drains and your journal and summary jobs run again.

## [2026-06-19]

### Added
- **attn can now run durable multi-agent workflows — off by default.** When you turn it on (Settings → Agents → Workflows), a managed agent can orchestrate a *workflow*: a script that fans work out across several sub-agents — in parallel or in ordered stages — and records every step, so you can watch it run, come back to it later, and resume it where it left off. It's opt-in twice over: the feature ships off, and even with it on an agent only starts a workflow when you ask for one per task ("attn workflow …") or hand it the whole session ("hypercode"). While it's off, attn won't start a workflow and agents aren't told the capability exists — and turning it off never interrupts a run that's already going.

### Changed
- **A finished workspace's knowledge-base project is filed away automatically.** When you remove a workspace, the keeper now moves that workspace's linked knowledge-base project folder into `knowledge/archive/`, so your active `knowledge/projects/` view stays focused on work that's still live. Anything promoted into `areas/` stays put and nothing is deleted — the project is just archived.

## [2026-06-18]

### Changed
- **The Notebook's "memory" became a knowledge base the chief keeps by editing files directly.** The durable Notebook's distilled-notes layer is reframed from "memory" into a **knowledge base** organized the PARA way — `projects/`, `areas/`, `resources/`, and `archive/` under `knowledge/`, each with its own index. Notes now carry an open `type:` label instead of a fixed set of kinds, so you (or any markdown tool, like Obsidian) can organize them however you like. The chief of staff maintains the knowledge base and the journal the same way it writes everything else: by reading and editing the markdown files on disk with native tools.

### Removed
- **The `attn notebook` command line was removed.** The browsable Notebook in the app, sending a selection to the chief, and the background-task panel are all unchanged. Agents and the chief now read and write the notebook as plain files on disk instead of through `attn notebook init/show/list/journal/memory/tasks/guide`.

## [2026-06-15]

### Added
- **See what attn's background work is doing, from the app or `attn notebook tasks`.** A new Tasks section in the Notebook browser — and a matching command — lists attn's durable background tasks — the context-compaction, session-digest, and journal-narration jobs — showing each one's state, what it's working on, how many attempts it has taken, when it next runs, and the last error if it failed. The panel refreshes live as tasks change, and a stuck (failed or dead) task gets a Retry button to run it again now. It's a window into work that used to be invisible, so you can tell at a glance whether something is queued, running, retrying, or stuck.
- **Your work-journal now writes itself from the work your agents actually do.** As sessions finish, attn quietly digests each one, and when a workspace is active — or the moment it's removed — it composes a curated, dated journal entry for that workspace: the real decisions and why, the fights and how they resolved, what shipped, the dead-ends, and what's still open. It grounds the story in what actually happened (passing tests, real commits) rather than what an agent claimed, surfaces silent wins and abandoned plans, and continues the narrative across days instead of repeating itself. A removed workspace gets a final retrospective so the whole effort is captured before it's gone. Entries are written safely, one per workspace per day, and the keeper is told to keep secrets and tokens out of them.
- **Active long-lived workspaces now get a daily journal entry even when no session ends that day.** A nightly pass writes a fresh entry for any workspace you actually worked in that day — a session finished or its shared context changed — so a workspace that runs for weeks stays journaled day to day instead of only being written up when it's removed. Workspaces you didn't touch are skipped, so the pass never spends effort on idle ones.

### Fixed
- **A workspace or session can no longer corrupt your journal or other files.** Every internal narration path — the context snapshot taken at removal, each per-session digest, and the journal-narration pass itself — now refuses any workspace or session identifier that is not a plain name, so a crafted identifier can no longer steer a write out of attn's internal holding area and overwrite your curated journal or another file, nor point an internal narration pass at a file outside its sandbox. Normal workspaces and sessions are unaffected.
- **The journal-narration pass no longer skips or drops entries.** Each session's digest is now kept under its own workspace, so a workspace's journal entry is composed only from its own sessions instead of being contaminated by unrelated work. The pass also no longer falsely reports success when a run leaves the journal untouched (so a removal-day retrospective is retried instead of silently dropped when an earlier entry already exists that day), and one workspace's entry can no longer be mistaken for a differently-named workspace whose id shares its prefix.
- **A removed single-session workspace's retrospective now includes that session's actual work.** When a workspace was torn down right after its only session finished, the final journal entry could be written from the leftover context overlay alone, missing the grounded digest of what that last session really did. attn now carries the finished session's work into its digest even after the workspace is gone and rewrites the retrospective once it lands, so the closing entry reflects the real work rather than just the aspirational context.

### Changed
- **Your curated journal stays curated; raw delegated-work outcomes now feed it behind the scenes.** Auto-captured chief-of-staff dispatch outcomes no longer land directly in your daily `journal/<date>.md`. They are now recorded to an internal holding area the upcoming narration pass reads from, so the journal you read keeps only curated entries instead of machine-raw blocks. Dispatch outcomes are still captured reliably and exactly once; existing journal entries are left untouched.
- **A workspace's shared context is preserved when the workspace goes away.** Removing a workspace used to discard its shared `context.md` overlay entirely. attn now snapshots that context the moment a workspace is torn down, keeping it as durable raw material for the journal-narration pass — so the decisions and current picture an agent recorded in a workspace are no longer lost when the workspace closes.

## [2026-06-14]

### Added
- **Every agent now helps keep your work-journal, not just the chief of staff.** Whenever attn launches an agent in a workspace, it now teaches it to jot a short journal entry when something is worth remembering — a decision and why, a hard-won fix, something meaningful it finished, or a durable gotcha — so your journal reflects work across the whole workspace, not only delegated chief-of-staff dispatches. Entries stay short and factual, are written through attn (so they're safe and observable), and the keeper is told to leave secrets and tokens out.
- **Your Notebook journal now writes itself when delegated work finishes.** When a chief-of-staff dispatch completes or fails — or its session simply ends — attn automatically records it in that day's journal: a dated, human-readable entry with what was done, any decision that was reached, and how it was verified. You get a durable history of what was built and decided without anyone having to remember to write it down, and each entry links back to the dispatch it came from. Entries are recorded once, even if attn restarts.

### Changed
- **Automatic workspace-context compaction is now durable across restarts.** When a workspace's shared context grows large enough to compact, the pending compaction is now queued on attn's durable task engine instead of an in-memory timer, so it survives a daemon restart and is no longer lost if attn closes during the wait. The compaction agent now works with ordinary file tools instead of a constrained tool pair; what you see is unchanged — oversized context still gets compacted, validated, and applied safely, and is cancelled cleanly when its workspace goes away.

### Fixed
- **attn no longer opens to a black screen when restoring sessions after launch.** A recovered session could briefly arrive before its in-memory workspace link was rebuilt, causing the entire interface to stop rendering. attn now preserves the persisted workspace ownership during recovery and can reconstruct it from the saved workspace layout.
- **Workspaces no longer disappear or time out during creation after switching between development builds.** A migration-number collision could leave existing databases marked as current without the workspace-ordering column newer builds require. attn now repairs and backfills that schema automatically, and workspace creation reports setup errors immediately instead of waiting 30 seconds.
- **Agents and shells launched by attn no longer inherit a stale Claude Code session identity.** When attn itself was started from inside a Claude Code session (for example an agent running `make install`), that session's per-session environment — including its session ID — leaked into the daemon and then into every agent or shell attn spawned, which broke things like transcript generation. attn now drops those inherited per-session identifiers before launching agents and shells, while still honoring any Claude Code settings you've configured in your shell profile.
- **Multi-part emoji now render as a single emoji instead of their separate pieces.** Family and couple emoji (👨‍👩‍👧‍👦), country flags (🇺🇸), skin-tone variants (👍🏽), and keycaps (1️⃣) were splitting into their components — four separate people, two boxed flag letters, a thumb next to a color swatch. They now combine into the one intended glyph.

## [2026-06-13]

### Added
- **Keyboard shortcuts are now customizable.** A new shortcut editor (open it from the keyboard-shortcuts cheatsheet's "Edit shortcuts" button, or the command menu's "Customize keyboard shortcuts") lets you rebind any shortcut by clicking it and pressing the new keys. Binding a combo that's already taken offers to reassign it, freeing the previous shortcut. A few essential shortcuts (Quit, Settings, and the cheatsheet) can be rebound but not left unbound so you can't lock yourself out, and "Restore Defaults" resets everything. Your bindings are saved automatically and apply everywhere, including inside the terminal.
- **The sidebar dock is now yours to arrange.** Pick exactly which shortcuts appear in the dock with the ☆ pin next to any shortcut in the editor, reorder them, and collapse the whole dock with the toggle in its header when you want more room. Dock chips for the diff, review-loop, and PRs panels are now clickable, and every chip updates instantly when you rebind its shortcut.
- **Shortcuts can now be leader-key chords.** Bind an action to a two-step sequence — press a leader like ⌘K, then a follow key — using the "chord" button next to any shortcut in the editor. A small indicator appears after the leader to show attn is waiting for the second key; press it to fire the action, or wait/press Esc to cancel. Chords work everywhere single shortcuts do, including inside the terminal, and several chords can share one leader.
- **The shortcut editor is easier to navigate.** A filter box at the top narrows the list as you type (matching names and keys); the reset button now names the default it will restore (e.g. "Reset to ⌘N"); and shortcuts that only do something while a terminal workspace is open are marked "Needs terminal" so you know why a key seems inert on the dashboard.
- **Sessions parked on a schedule now have their own "Scheduled" state.** When Claude Code stops a turn but is waiting to auto-resume later — parked on a `/loop` or a scheduled wakeup — the session shows a calm blue, slowly-pulsing "Scheduled" indicator instead of being mistaken for finished or needing attention. It tells you at a glance the agent will pick itself back up on its timer, and it stays quiet until it actually needs you.
- **You can now reorder workspaces in the sidebar.** Drag a workspace by its header and drop it between others to set the order you want — an insertion line shows where it will land. Workspaces still start in the order you opened them, and your custom order sticks across restarts.
- **You can now drag a session straight from the sidebar to reorganize your workspaces.** Press and drag any session in the sidebar list — a ghost follows your cursor — and drop it on another workspace to move it there, or on the "New workspace" zone at the foot of the list to split it into a brand-new workspace of its own. (Dragging a session's pane in the main view does the same thing; now the sidebar list is a drag handle too.) Splitting a session into a new workspace adds it at the end of your list, and if the move empties the workspace it came from, that empty one is cleaned up for you.
- **A durable Notebook for memory that outlives a workspace.** attn now keeps a plain-markdown notebook — dated journals plus distilled notes and decisions — in a folder you own (`~/attn-notebook`, separate per profile), browsable in any editor. Create and read it from the command line with `attn notebook init`, `attn notebook list`, and `attn notebook show <path>`. It's the foundation of an in-app notebook and an automatic nightly consolidation pass landing in upcoming releases.
- **Agents can now write to the Notebook, and the chief of staff calls it home.** Agents append to the day's journal (`attn notebook journal append`) and write durable, grounded notes (`attn notebook memory write`) — all through the daemon, so writes stay safe and observable. When you promote a session to chief of staff, attn now points it at the Notebook as its durable memory in place of a single workspace's shared context, and nudges it to pick up its operating guidance right away.
- **Read the Notebook inside attn.** Open "Browse the Notebook" from the command menu (⌘K) to read your journals and notes in a clean rendered view, with the notes organized by journal, memory, and decisions. Links between notes are clickable so you can follow a train of thought, each note shows which other notes link to it, and the view refreshes on its own as agents write — no reload needed.
- **Edits made outside attn show up live too.** Because your notebook is just a folder of markdown files, you can edit it in any other app — your editor, a markdown tool, or a sync client pointed at the folder. The in-app Notebook browser now watches that folder and reflects those changes as they happen, the same way it follows attn's own writes. If a note you're viewing is deleted on disk, the view clears instead of showing stale text.
- **Edit notes right inside attn.** The Notebook browser now has an Edit button: change a note's markdown and save it back, no separate editor needed. If the note changed on disk since you opened it — an agent or a sync client wrote it meanwhile — attn tells you and offers to reload the latest version or overwrite it with your edits, so you never silently clobber someone else's changes or lose your own.
- **Send a passage from the Notebook to your chief of staff.** Highlight any text in a note and a "Send to chief" button appears — click it and attn drops the selection (with a link back to its source note) into the chief of staff's inbox, and gives a live chief a gentle nudge to go read it. It's a quick way to hand the chief a decision or a bit of context without switching to its terminal; if you haven't picked a chief yet, the note waits in the inbox for whoever you promote next.

### Fixed
- **Zooming the active pane (⇧⌘Z) now works again.** The macOS Redo menu item was claiming the key equivalent before it could reach the app, so the shortcut did nothing in the installed app. attn no longer registers that menu entry, freeing ⇧⌘Z for the zoom shortcut — including any key you've rebound it to.
- **Emoji and terminal icons now render in the terminal.** Color emoji show in their real colors instead of a flat one-color blob, and the file-type and Git icons that tools like `eza --icons`, `starship`, and powerline prompts draw (which need a "Nerd Font") now appear instead of blank gaps — attn bundles the icon font, so it works without installing anything.
- **A session running work in the background no longer flips to a confusing "unknown" state.** When Claude Code hands off to a background workflow or background shell command and pauses its turn, the session now stays shown as working until the background work finishes and the turn resumes — instead of briefly being mis-detected as "unknown" because it paused before its transcript was written.
- **A workspace's status dot now matches the sessions inside it.** A workspace whose only session was in the "unknown" state used to show a settled gray dot that disagreed with the session's own indicator; the unknown state now rolls up to the workspace so the two agree. The workspace dot also stays correct right after the app reopens — restored sessions no longer leave the workspace showing a stale status from before the restart.
- **Shell sessions no longer show a permanent green "working" dot.** A plain terminal isn't running an agent turn, so it now starts (and stays) idle instead of appearing busy forever until you close it.

### Removed
- **Quick Find is gone.** The ⌘F "thumbs" picker that pulled URLs, paths, and addresses out of the terminal into a hint list has been retired. Find in terminal (⌘F) and the clickable, Cmd-clickable file paths and URLs in terminal output now cover the same ground more reliably.

## [2026-06-12]

### Changed
- **The new-session location picker is faster to use and easier to scan.** Recent locations now always show the folder name instead of session names like "shell" or manual renames, so the list reads as a list of places again. The list is ranked by how often *and* how recently you use each location, keeping your main projects in stable slots near the top, and all worktrees of a repository collapse into a single entry for its main checkout. The top recent location is pre-highlighted when the picker opens, so pressing Enter immediately opens your most-used project — typing or arrow keys take over as before, and Escape still closes the dialog right away.

### Fixed
- **Resizing or splitting a pane while its history is still restoring no longer wipes the terminal.** A geometry change landing mid-restore used to silently discard the queued history, leaving the pane blank (after an app relaunch) or without scrollback (after a split). A resize that matches where the restore already ends now lets it finish, and a genuine size change re-requests the history at the new size — including the command blocks, which come back clickable.
- **The first output after reconnecting to a session no longer goes missing, and new panes no longer intermittently get stuck loading.** Reconnecting (relaunch, split, or recovery) could silently swallow the next chunk of terminal output — typed commands would run but never appear. Separately, two rapid attachments to the same session could pair a response with the wrong request and leave the pane waiting forever. Both reconnect handoffs are now exact: output resumes with no gap, and attachments run one at a time per session.

---

## [2026-06-11]

### Fixed
- **Opening or resizing a workspace with deep terminal history no longer blanks or stalls the app.** attn now keeps the daemon connection stable while navigating, restores terminal history only when a workspace becomes visible, limits each synchronous replay to 64 KiB, uses a fixed Ghostty terminal core, preserves OSC 8 hyperlink metadata, compacts redundant replay work, coalesces split-drag geometry updates, and returns visible panes to their live size after replay.
- **New Codex sessions now use the visible pane size immediately.** Their first measured terminal geometry is retained until attachment completes instead of leaving the PTY at `80x24` until the window is resized.
- **Command-block selection stays correct across resizes and app restarts.** Selecting a long command's block (e.g. `make`) no longer draws a box around the whole terminal — off-screen edges read as side rails that say "this block continues above/below" instead. Blocks now survive an app restart: reconnecting rebuilds them from the replayed history, so clicking an old command still selects exactly that command. Changing a pane's width makes old block positions unrecoverable — those blocks now clear instead of drawing boxes in the wrong place, and new commands track correctly in the new size; height-only changes keep every block. Reconnecting also no longer briefly forces the live shell back to a default terminal size, which used to redraw prompts at the wrong width and shift the restored history.
- **Terminal output is no longer dropped when the app reconnects to a session.** A chunk of output emitted right as the app reloaded — or as a command relaunched it (e.g. `make install`) — could fall into a gap between the replayed history and the resumed live stream and vanish. This made long-running commands look stuck and, with command blocks, merged two separate commands into one block. Reconnects now hand off history and live output with no gap, and block tracking recovers cleanly even if a boundary marker is ever lost.
- **The shell prompt no longer hangs for several seconds after the app reconnects.** When the app reattached to a shell session — including when a command like `make install` quits and relaunches it — the prompt could freeze for up to ~30 seconds before redrawing, even though the command had already finished. The shell was waiting on terminal probes (cursor position and device attributes) that the reconnecting app could miss while it was still painting; attn now answers those probes itself, so the prompt comes back immediately.

---

## [2026-06-10]

### Added
- **File paths and URLs in terminal output are clickable.** Hovering a path or URL underlines it; Cmd+click opens URLs in the browser and existing files in their default app. Paths with `:line:col` suffixes, `~/` prefixes, and paths relative to the session's directory all resolve — including long paths that wrap across terminal lines — and detection only runs for the text under the pointer so heavy output streams stay as fast as before.
- **Right-click in the terminal opens a context menu.** Copy the selection or the command block under the pointer (whole block, command only, or output only), paste from the clipboard, jump to find, or scroll to the top or bottom of a block. On a command block the menu also offers **Filter block output**: type a query to see just the output lines that match, with highlighted hits — click a line to jump the terminal to it.
- **Cmd+F finds text in the terminal.** Search covers the full scrollback with live match highlighting, a match counter, Enter / Shift+Enter navigation that scrolls matches into view, and an optional case-sensitive mode. The previous quick-find shortcut moved to Cmd+Shift+F.
- **Commands and their output are copyable as blocks (fish).** When the shell announces command boundaries (fish does this out of the box), clicking anywhere in a command's output selects the whole block: Cmd+C copies the command together with its output, Cmd+Shift+C copies just the command — exactly as typed, with no prompt decoration. Clicking the command line itself highlights just the command, and triple-click selects a whole row.
- **Large workspace contexts can compact themselves.** Choose Codex or Claude and a recommended model preset in Settings, or enter a custom model, to let attn occasionally summarize contexts above 12 KiB after a quiet period. Claude can use normal OAuth, keychain, or organization-managed authentication without loading user or project customizations. Compaction runs without an interactive session, leaves existing working copies stale for the normal refresh/conflict workflow, publishes only against the revision it read, and keeps the latest pre-compaction version available through `attn workspace context rollback`. Use `attn workspace context compact` to run it immediately.

### Changed
- **Terminals keep much more scrollback.** Live panes now hold roughly 8× more history, and switching back to a workspace (or restarting the app) restores up to 8 MB of terminal history instead of just the visible screen. This especially helps Codex sessions, which rely on the terminal's own scrollback and previously came back from a restart with no history at all.
- **Workspace context now describes an area of work instead of one goal.** New agents orient from a required Area and Current Picture, optional semantic Threads, and a small sourced Timeline of turning points, so related goals and the story connecting them can coexist without treating sessions as tasks.
- **Heavy terminal output uses much less memory.** Sustained output (long builds, tests, log floods) is now batched into far fewer, larger messages on its way to the app, cutting the app's memory growth during heavy streaming by roughly 40% and reducing CPU overhead. Typing and interactive prompts are unaffected — keystroke echo still arrives immediately.
- **Streaming terminal output is cheaper.** Live terminal output now travels from the daemon to the app as compact binary messages instead of base64-encoded JSON, cutting per-chunk encode/decode work and message size by a third. This lowers CPU and bandwidth during sustained heavy output (busy agents, long builds) and modestly reduces the app's memory high-water.

### Fixed
- **Delegated worktrees stay in the current workspace by default.** `attn delegate --worktree` now controls branch isolation independently from workspace placement; add `--new-workspace` when the delegated session should also get a separate workspace.
- **Provider-managed worktree creation has more time to finish.** Worktree providers now get up to two minutes to create and bootstrap worktrees in large repositories, avoiding false timeout failures after the provider has already created the worktree. Hooks and worktree deletion keep their existing 30-second deadline.

---

## [2026-06-09]

### Added
- **⌘K opens a searchable action menu.** The first action opens a fullscreen browser for workspace contexts stored on this Mac, with search, keyboard navigation, revision metadata, and rendered Markdown. The existing attention drawer remains available from the menu and its dedicated ⌘⇧P shortcut.
- **Claude and Codex receive workspace-context guidance without cluttering their terminals.** attn gives each session a local checkout and concise hidden instructions to read it, keep durable goals and decisions current, publish edits, and reconcile revision conflicts without copying the shared context itself into the prompt.
- **Promote one running session to chief of staff.** Session actions in the sidebar can assign, transfer, or remove the profile-wide role, with confirmation before replacing the current chief and clear badges in the sidebar and dashboard.
- **Chiefs can track delegated work separately from agent runtime state.** Delegated agents can attach structured work state, next ownership, constraints, artifact-bound verification, and one decision request to the existing narrative report. Chiefs can resolve that request durably, agents can read the response with `attn dispatch status`, and the dashboard highlights actionable work without showing the full brief.
- **Chiefs can send durable mailbox messages to delegated agents.** Agents check unread mail before finishing or waiting, mark messages read, and acknowledge them after acting. The dashboard shows unread counts and offers an explicit wake action for idle agents without injecting message contents into their terminal.

### Changed
- **Workspace context guidance is safer and less noisy.** Agents now publish only durable shared changes, keep each fact in one appropriate section, treat copied context as untrusted data, and save local edits before refreshing a conflicting revision.
- **Delegation guidance distinguishes visible collaborators from internal subagents.** Agents use attn delegation for full interactive sessions the user wants to inspect and steer, while internal research, adversarial analysis, verification, and parallel reasoning default to native subagents.

### Changed
- **Terminals use less graphics memory.** Every live terminal used to preallocate a large fixed GPU texture to cache rendered characters. It now starts small and grows only if a session actually displays many distinct characters (such as heavy CJK or emoji output). Text looks identical, and with several sessions open this frees roughly 90 MB of graphics memory.

### Fixed
- **Closing a workspace now removes its shared context.** Workspace context no longer keeps an otherwise empty workspace alive, and startup cleanup removes context left behind by older closed workspaces. Workspaces with docked tiles still remain available.
- **Closed delegated sessions no longer remain on the Chief of Staff dashboard.** Closing a delegated session now removes it from the active home view while retaining its dispatch report as history.
- **Returning to a resized workspace no longer lets busy split terminals freeze the app.** Hidden workspaces keep their terminal state current without continuously repainting WebGL surfaces, live output paints at most once per animation frame, and duplicate same-size PTY resize acknowledgements no longer trigger full redraws.
- **Closed sessions no longer flicker back into the sidebar as launching.** The daemon now publishes authoritative layout updates as final panes close, and the app also discards cached layouts that still reference an explicitly closed session.

## [2026-06-08]

### Added
- **Agents can delegate a task without leaving their current work.** `attn delegate` starts Claude, Codex, or a plugin agent that explicitly supports delegated prompts with a focused brief from `--brief` or `--brief-file`. The delegated agent can join the current or another existing workspace, start in a new workspace at a chosen directory, or get an isolated branch in a newly created worktree. The bundled attn skill uses a concise capability index and loads separate delegation, review-loop, markdown, or browser guidance only when needed.
- **Agents can maintain shared context for a workspace.** `attn workspace context show` returns a semi-persistent markdown file that an agent can edit in place, while `update` publishes revision-checked changes and `status` reports local edits or newer shared revisions. Context-bearing workspaces survive after their last session closes, and clients receive `workspace_context_changed` when another session publishes an update.

### Changed
- **Grid view shows session state with colored borders and subtle tile tints.** Waiting-for-input sessions flash amber, while zoom focus remains clearly separated from session status.
- **attn uses noticeably less memory per session.** Each running session's terminal backend used to reserve 8 MB of scrollback up front and grow a second 8 MB replay buffer, even though only the most recent 256 KB is ever replayed when you switch back to a session — both are now capped at 1 MB, freeing roughly 7–14 MB of RAM per session. Separately, diagnostic terminal capture (which quietly held up to ~20 MB of recent output in memory for every Claude and Codex session) is now off unless you explicitly turn it on with `ATTN_DEBUG_PTY_CAPTURE`. With several sessions open this reclaims hundreds of megabytes.
- **Background workspaces no longer hold onto graphics memory.** Only the workspace you're in plus your few most-recent ones keep their terminals fully live; the rest release their GPU/terminal resources and instantly restore from history the moment you switch back. With many workspaces open this frees hundreds of megabytes of RAM and GPU memory and reduces background CPU.
- **Lower background CPU while agents stream output.** attn was doing a lock-held synchronous log write — plus building a text preview of the bytes — for every chunk of terminal output, on both the daemon and each session's PTY worker, even with debug logging turned off. These verbose per-chunk logs are now written only when you opt into `DEBUG` logging, removing tens-to-hundreds of disk writes per second on a busy session and keeping each session's worker log file from growing without bound during normal use.

## [2026-06-07]

### Added
- **Agents can open and control a persistent browser inside attn.** `attn browser open <url>` docks a real browser beside the current session, with an address bar, Reload and Close controls, sidebar presence, correct pane focus and dialog layering, and a durable cookie and local-storage profile so logins survive app restarts. Agents can navigate and inspect pages using semantic locators and stable element references; interact through forms, keyboard and pointer actions, frames, shadow DOM, cookies, alerts, and popups; wait for page state; and capture screenshots or PDFs without switching to a separate automation browser. Browser control is authenticated, isolated from page scripts, responsive while attn is in the background, and reports capture or transport failures without losing the session.

## Release Highlights — Workspaces

attn is now organized around **workspaces**. The sidebar lists workspaces instead of loose sessions, and each workspace holds the sessions and terminals for one piece of work. This is the headline of the latest release; the dated entries below carry the full detail.

- **⌘N now opens a session inside the current workspace.** It used to start a separate session with its own sidebar row. Press ⌘T when you want a new workspace (a new sidebar row). This is the change most likely to catch you off guard.
- **Workspaces in the sidebar.** Sessions live inside workspaces. Muting, switching, and the muted section all operate on whole workspaces.
- **Many sessions per workspace.** Open several sessions and terminals side by side, split a pane, zoom or maximize one, and move focus between panes with the keyboard.
- **Shells are first-class sessions.** Open a plain shell from the same new-session dialog you use for coding agents — it lives in the workspace like any other session.
- **A keyboard model for workspaces:**
  - `⌘T` — new workspace (with an initial session)
  - `⌘N` — new session in the current workspace
  - `⌘⇧N` — new session, split sideways
  - `⌘⌥` + arrow keys — move focus between panes; push past an edge to cross into the next workspace
  - `⌘1`–`⌘9` — jump straight to a workspace
  - `⌘/` — open the full, always-current keyboard shortcuts cheatsheet

The app shows a one-time "What's new" summary after this upgrade, and `⌘/` brings up the complete shortcut list any time.

---

## [2026-06-07]

### Fixed
- **Resizing workspace splits is less likely to stall active agent panes.** Split drags still update the visible terminal layout live, but attn now coalesces the PTY resize sent to each running agent while the drag is in progress so Claude and Codex are not forced to repaint repeatedly mid-drag.

## [2026-06-06]

### Changed
- **Rebuilt the diff viewer.** The review panel now renders diffs with a faster syntax highlighter and a cleaner layout, and you can switch between **unified** and **split** views (unified stays the default). Everything you rely on while reviewing carries over: inline comments on added and deleted lines, edit/resolve/delete, per-file comment counts, sending a comment or a selection to the running session, and viewed / changed-since-viewed tracking. Add a comment by hovering a line and clicking the **+** in the gutter to jump straight to the comment box, or click anywhere on a line (or select a range of line numbers) and pick **Add comment** or **Send to Claude** from the popup. Comments hold up while the agent keeps working: a comment you're typing is never lost when the file changes underneath, and a saved comment that doesn't appear in the current diff — its line was removed, or it sits inside collapsed unchanged code — gathers into a banner at the top (click to expand) instead of silently disappearing.

## [2026-06-05]

### Added
- **Grid view (⌘G).** A global "mission control" that shows every active session at once as a live terminal tile, each outlined as its own panel. Every tile shows the session's current screen the moment the grid opens — no waiting for the next keystroke — and then stays live. Click a tile to zoom it full-screen and type straight into that session; the focused tile gets a highlighted border so it's clear where your keystrokes go, and switching tiles moves your input with it. Press Esc to leave the zoom, or ⌘G to close the grid. Sessions that need you pulse with an orange border so they're easy to spot across all your workspaces. Size the grid your way: a button in the sidebar opens a small picker — sweep across the squares to set rows and columns, or pick Auto to keep the automatic fit (and if a fixed size can't hold everything, the grid tells you how many sessions aren't shown). Every session is on the grid by default; hover a tile and click the × in its corner to take it off, and a "N hidden" button lists what you've removed so you can put any of it back. Removals stick across restarts, so you can pare mission control down to just the agents you're actively watching.

### Changed
- **Home is now ⌘⇧H.** Jumping to the dashboard moved off ⌘H — which macOS reserves for Hide — to ⌘⇧H, freeing ⌘G for the new grid view.

## [2026-06-04]

### Fixed
- **Selecting terminal text to the edge of a split pane no longer gets stuck.** When you dragged a selection up to the divider between split panes, the cursor flipped to the resize arrow and the selection froze — and the text wasn't copied until you clicked again. Selections now finish and copy normally even when you release the mouse right at a pane edge.
- **Closing a shell no longer leaves a fully black screen.** When a shell exited while its cached pane was still marked as starting, the app could keep an invisible stale session and suppress the normal empty state. Exited shells now disappear cleanly.

## [2026-06-03]

### Added
- **Rename workspaces and sessions.** Hover a workspace or session in the sidebar and click the pencil to give it a name that means something to you. When a workspace has more than one pane, each pane header has the same pencil so you can rename its session right where you're looking. A small popup opens with the current name selected — type to replace it, or press → to keep it and append. Renames stick: they survive reconnects and reloading a session.
- **Move panes and tiles between workspaces.** Drag a pane or docked tile toward another workspace in the sidebar to switch to it, then drop on the target layout for exact placement. Dropping directly on the sidebar workspace moves it to the left side of that workspace.

### Changed
- **Docked tiles read a little nicer.** A markdown tile's header now shows the document's title (its first heading) rather than the file name, falling back to the file name when there's no heading. And when you open a tile-only workspace, the keyboard lands on the tile right away, so the arrow keys scroll it without an extra click.

### Fixed
- **Clicking a pane or tile header no longer rearranges the layout.** A plain click on a pane's title bar used to be read as a completed drag and could split the workspace in half. Headers now only move a pane once you actually drag it a few pixels; a click does nothing.
- **Opening new panes no longer freezes the app over a long session.** Each terminal pane uses a GPU (WebGL) drawing surface, and the browser allows only a limited number at once. Closed and rebuilt panes used to hold onto their surface until the system got around to cleaning up, so opening and closing panes over a working session could pile them up until the next new agent or terminal pushed past the limit — at which point the UI could freeze (a restart cleared it). Panes now release their drawing surface the moment they close, so the count stays bounded.
- **Approving a permission request clears the attention light right away.** When an agent is blocked on a permission prompt and you approve it, the session now returns to "working" within about a second — even while the approved command keeps running, and even for a second prompt back-to-back. It used to stay in the flashing "needs you" state for the entire duration of the approved tool, which defeated the point of the light. Works for both Claude and Codex.
- **Codex sessions no longer get stuck in the purple "unknown" state.** When a Codex turn finished outside a git repository (for example in `/tmp`), attn could not classify it and fell back to "unknown." Codex sessions now settle into the correct done/waiting state regardless of where they run. The end-of-turn check is also leaner and quieter — it no longer loads your Codex MCP servers or tools.

## [2026-06-02]

### Changed
- **Closing the last terminal no longer throws away a docked tile.** If you leave a markdown tile open and close the workspace's last terminal, the workspace now stays around as a tile-only workspace instead of disappearing with your doc. Tile-only workspaces are hidden from the sidebar by default; turn on "Tile-only workspaces" in the sidebar settings (the gear menu) to see them, where they show a neutral marker instead of a session-state dot. Selecting one opens it and shows your docked tile, just like any other workspace.

## [2026-06-01]

### Added
- **Drag any pane or tile to rearrange a workspace.** Grab a terminal pane's header (or a markdown tile's header) and drop it on another pane's edge to re-dock it — left, right, top, or bottom. How far in you drop sets the new split's size, snapping to a quarter, third, or half; drop into the workspace's outer edge to span the whole side. Dropping a pane on itself does nothing.

### Changed
- **`attn` is clearer about being its own command.** `attn --help`, unknown commands, and unknown flags now print attn's own usage (with its version) instead of quietly handing them to the coding agent. `attn` understands only `-s`, `--resume`, and `--yolo`; it no longer forwards other flags — or anything after `--` — through to the agent.
- **Stale-`attn` warning.** When the `attn` on your `PATH` is a different version from the running app, attn prints a one-line warning so you can catch an out-of-date binary shadowing the current one (`which -a attn`).

### Fixed
- **Workspace Sessions**: Creating a worktree from the `⌘N` new-session picker now adds the session to the current workspace instead of creating a separate workspace.
- **Worktree Cleanup**: Provider-managed worktrees with local changes now offer force delete after the normal delete attempt is rejected.
- **Daemon Logging**: `daemon.log` now automatically keeps a bounded recent tail so long-running installs do not accumulate an unbounded log file.

---

## [2026-05-31]

### Added
- **Keyboard Shortcuts Cheatsheet**: Press `⌘/` to open a searchable overview of every keyboard shortcut, grouped by workspaces, panes, review, and app actions. The list is generated from the app's shortcut definitions, so it always matches the real bindings.
- **What's New**: After upgrading, attn shows a one-time summary of the workspace model and its key shortcuts, with a link into the full cheatsheet.
- **Resizable Splits**: Drag the divider between any two workspace panes to resize the split. The size you set is remembered per split and persists across reconnects and restarts (previously panes were always forced to an equal split).
- **Docked Tiles**: Tiles can now dock directly into a workspace's layout as a real, resizable pane instead of floating on top. Drag a tile's title bar onto any terminal to re-dock it on that pane's edge — drop it between two terminals and it slots in between them. Tile placement and size are remembered by the daemon, so they survive restarts and follow you to other clients, just like terminal splits. (The slide-in diff, review, and git-status panels are unchanged.)
- **Open Markdown In A Tile**: Run `attn open <file.md>` to render a markdown file in a docked tile next to your session. The tile live-reloads as the file changes on disk, and like other docked tiles its placement is remembered by the daemon across restarts and clients. Agents running in attn can use this too (via the bundled attn skill) to show you a plan, summary, or report as rendered markdown.
- **Attn Presence Check**: Run `attn presence` to check whether the current shell is running inside an attn-managed agent session. Run `attn help` to see the available CLI commands.

### Changed
- **Auto-Close On Exit**: When a session's process exits cleanly (exit code 0), the session now closes itself instead of leaving a dead `[Process exited]` pane around. Sessions that crash or are killed (non-zero exit) stay open so you can still read the error.
- **Workspace Muting**: Muting now applies to whole workspaces instead of individual sessions. Muted workspaces move to the muted section together with all of their sessions, and session rows no longer expose mute controls.

### Fixed
- **Workspace Sidebar**: Startup recovery now removes stale empty workspaces left behind by sessions that no longer have a live terminal, preventing old workspace rows from reappearing in the sidebar.

### Removed
- **Session Muting**: Session-level mute state was removed from the app, daemon protocol, and session database schema.
- **Footer Debug Buttons**: The dashboard footer no longer shows the developer debug toggles (resize debug, runtime trace, pane debug). The footer now points to the `⌘/` shortcuts cheatsheet next to `⌘,` settings.

---

## [2026-05-30]

### Changed
- **Workspace Sidebar**: The session sidebar now presents workspaces as the primary navigation unit, keeps existing sidebar tools visible, and adds local display options for open, tight, and boxed workspace layouts.
- **Workspace Shortcuts**: Command-N opens the new-session picker for the current workspace, Command-Shift-N opens it as a sideways split, and Command-T opens the new-workspace picker.

### Removed
- **Non-Session Terminal Panes**: Workspace panes are now always backed by sessions; legacy standalone terminal panes and first-pane special handling were removed from the app, daemon, protocol, and database migration path.

### Fixed
- **Session Creation**: Creating sessions from new worktrees now runs in the background with the same compact progress surface as worktree cleanup, so slow repository/plugin setup no longer traps the app in the picker.
- **Terminal URL Clicks**: Command-clicking URLs in Ghostty terminals now uses the rendered terminal canvas position, preventing clicks on the row above a URL from opening it when the canvas is vertically offset.
- **Session Startup**: New workspace sessions and split sessions now appear as daemon-owned loading panes until the shell/agent actually starts, and failed starts remain visible with the failure reason instead of leaving blank panes.
- **Workspace Splits**: Command-D and Command-Shift-D now create session-backed shell splits that appear in the workspace sidebar.
- **Workspace Closing**: Closing a session removes only that session from its workspace, including when the closed session is occupying the first pane; closing the final session closes the workspace too.
- **Workspace Sidebar**: Empty workspaces no longer appear in the sidebar, display options stay open while switching modes, and session controls remain aligned at the row edge.
- **Codex Resize Redraws**: Attn now coalesces Codex's synchronized terminal redraws during pane resizes, preventing old scrollback from visibly streaming through the embedded terminal when layouts change.
- **Ghostty Shift-Tab**: Shift-Tab in embedded Ghostty terminals now reaches agents as reverse-tab input instead of being treated like a prompt submit.
- **Worktree Cleanup**: Failed worktree deletes now keep attn state intact, explain forceable local-change failures, and let you explicitly force-delete the local worktree and local branch without touching remotes.
- **Empty Git Repositories**: Selecting an empty Git repository now loads repo metadata instead of failing while resolving the current branch.

---

## [2026-05-29]

### Fixed
- **Codex Activity State**: Codex sessions now use hooks exclusively for live activity state, preventing completed sessions from remaining green due to stale transcript activity.
- **Terminal Selection and Link Activation**: Ghostty terminals now select words on double-click, preserve Option-drag selection inside mouse-tracking applications, and open URLs only with Command-click while showing a pointer cursor when Command is held.
- **Terminal Screenshot Paste**: Ghostty terminal sessions now forward macOS Control-V image-paste triggers and translate image paste events so Claude and Codex can read pasted clipboard images again without breaking text paste on other platforms.
- **Session Switching Shortcuts**: Command-number session switching no longer writes the selected session number into the focused Ghostty terminal.
- **Terminal Click Selection**: Small pointer movement during an ordinary terminal click no longer creates and copies a single-cell text selection.
- **Shell Terminal Input**: Shell panes now answer common terminal capability and color queries, preventing prompts from delaying typed input while they wait for a terminal response.
- **Location Picker Background Work**: Hidden location pickers no longer keep sending directory-browse requests, reducing avoidable daemon and terminal UI load while working in sessions.

## [2026-05-27]

### Changed
- **Ghostty Terminal Rendering**: Terminal panes now use the Ghostty WASM parser with GPU-backed rendering directly in the app, replacing the previous terminal implementation.
- **Stable Local macOS Signing**: Development app builds now use an installed Apple Development identity when available, sign the enclosing app bundle, and expose an app-owned native screenshot path so Screen Recording grants survive source rebuilds during visual terminal validation.

### Fixed
- **Terminal Block Graphics**: Colored cell fills and Unicode block graphics, including the Claude startup mark, now render continuously instead of showing faint seams between cells.
- **Codex Resize Redraws**: Attn-managed Codex terminals now enable Codex's resize-reflow mode so resizing a Ghostty-rendered pane redraws the inline UI instead of leaving wrapped or duplicated header fragments.
- **Terminal Scrolling**: Ghostty terminal panes now scroll in proportion to wheel or trackpad movement, keep a manually scrolled view steady while new output is arriving, keep selections attached to their text while browsing local or application-owned history, and forward wheel input to interactive agents such as Claude when they request terminal mouse tracking.
- **Terminal Links**: Visible URLs in Ghostty terminal panes now open when clicked, including modifier-clicks while an interactive application has terminal mouse tracking enabled.
- **Dev App Profile Isolation**: Relaunching `attn-dev.app` from a terminal that inherited production routing variables now keeps the dev app on its isolated daemon and port instead of starting or replacing a production daemon.

---

## [2026-05-25]

### Added
- **Agent Driver Plugins**: Installed plugins can now register coding agents, launch or resume them inside attn-owned terminals, report session state and stop verdicts, and persist opaque agent session metadata for future resumes.
- **Plugin Installation Sources**: Plugins can now be installed from a pasted Git repository URL in Settings as well as from a local source directory.

### Changed
- **External Agent Selection**: Plugin-provided agents are advertised dynamically in local and remote session pickers. Unshipped in-tree external-agent placeholders are removed in favor of standalone plugins.

### Fixed
- **Plugin Session Reporting**: Reports emitted immediately while a plugin launches or resumes a session are retained during terminal startup; per-launch run IDs, persisted plugin ownership, and sequenced reports prevent stale or replacement-plugin updates after newer activity or relaunch. Plugins are notified only after their attn-owned PTY has actually closed or been killed.

---

## [2026-05-23]

### Changed
- **Input Assistance Disabled**: attn no longer allows browser or operating-system autocorrection, auto-capitalization, or spellchecking to rewrite typed paths, branches, commands, prompts, comments, or searches.
- **GitHub Host Visibility**: Settings now lists daemon-discovered authenticated GitHub hosts even when a host has no currently visible pull requests.
- **Terminal Colors**: Interactive terminals no longer inherit `NO_COLOR` from the process that launched attn, so shells and coding agents can emit their normal colored output.
- **Tauri Workspaces**: Terminal layouts are now owned by workspaces rather than individual sessions, so workspace close cleans up member agent and shell terminals together and persisted split layouts migrate automatically.

### Fixed
- **Codex Resize Stability**: Long Codex sessions no longer jump back through earlier conversation content when resizing a pane or opening a split in attn's embedded terminal.
- **Dev App Isolation**: Running `make dev` from an active attn session no longer carries production daemon socket settings into `attn-dev.app`, which prevented the isolated dev daemon from starting.

---

## [2026-05-22]

### Added
- **Plugin Healthchecks**: The daemon now pings connected plugins, records healthy/unhealthy/unknown state separately from process and socket state, and shows that health in Settings.

### Changed
- **Settings Workbench**: Settings now opens as a larger workbench with separate navigation for general preferences, connectivity, plugins, agents, review settings, and muted items instead of one long mixed page.

---

## [2026-05-21]

### Added
- **Plugin Registration UI**: Settings now lists installed plugins, installs user-owned plugins from a local directory, removes them, and lets users set provider priority so daemon dispatch order is explicit instead of plugin-defined.
- **Plugin Lifecycle CLI**: `attn plugin install --path`, `attn plugin list`, and `attn plugin remove` now manage user-owned plugins under attn's configured plugin directory and report when a daemon restart is required.
- **Plugin SDK Foundation**: attn now includes an initial `@victorarias/attn-plugin` TypeScript SDK for worktree plugin surfaces, covering the daemon handshake, typed surface registration, request routing, structured provider results, and typed worktree provider/lifecycle contracts.

### Changed
- **Worktree Provider Coverage**: Worktree providers now participate when attn creates a worktree from an existing branch, including preserving the source branch/ref context for user-owned tooling.
- **Codex State Detection**: Codex sessions now rely on Codex hooks, including `PreToolUse`, instead of PTY output heuristics for live state changes.

### Fixed
- **Daemon Isolation Guard**: attn now refuses to start a daemon with an alternate socket/PID root while still pointing at the active profile's normal session database, preventing auxiliary daemon runs from pruning live sessions they do not own.

---

## [2026-05-17]

### Changed
- **Changes Panel Refreshes**: The Changes panel now avoids branch-diff refreshes while closed, refreshes once when opened, and coalesces status-triggered refreshes so slow monorepos do not stack repeated Git diff work behind every status update.
- **Diff Detail Loading**: The Diff detail view now shows a foreground pending state when the selected file diff is slow, keeps cached content visible while it refreshes, and caps background viewed-file checks so status bursts do not fan out into repeated Git diff work.
- **Active Git Status Refreshes**: Active-session Git status now uses a coalesced scheduler instead of a hot two-second poll loop, with slower fallback refreshes after expensive status runs.
- **Large Repository Git Status**: Active-session Git status now caps full untracked scans, falls back to tracked-file status when the full scan is slow, and marks the Changes panel as tracked-only while background refresh is limited.
- **Repository Git Coordination**: The daemon now routes status, branch-diff, and file-diff reads through a repo-scoped coordinator that shares in-flight work and daemon-owned branch-diff snapshots across connected clients.

### Fixed
- **Codex Session Resume**: Reloading a Codex session from attn now records Codex's native session id from hooks and resumes the same Codex conversation instead of handing Codex attn's wrapper session id.
- **Codex Activity State**: Reloaded or completed Codex sessions no longer stay green just because the Codex TUI process is alive at an idle prompt.

---

## [2026-05-16]

### Changed
- **Worktree Cleanup Prompt**: Deleting a closed session's worktree now stays visible while Git runs, collapses into a resumable bottom-right cleanup job when the operation takes longer than an instant, and keeps failures actionable with retry/keep choices instead of closing silently.
- **Git Operations**: The daemon now emits lifecycle events for worktree deletion so future UI surfaces can use daemon-owned progress instead of frontend timers.

### Fixed
- **Worktree Cleanup Prompt**: Finishing a slow worktree delete can no longer clear a newer cleanup prompt for another worktree.

### Removed
- **Session Forking**: Removed the local Fork Session dialog, its keyboard shortcut, and the `fork_session` spawn path so attn no longer exposes or forwards session-fork launches.

---

## [2026-05-15]

### Changed
- **Large Repository Git Operations**: Git worktree, status, diff, fetch, clone, and repository metadata operations now use shared long-running command handling with slow-command daemon logging. UI waits for these operations are much longer, and background git polling is less aggressive so giant monorepos are less likely to look failed while Git is still working.
- **Changes Panel Refreshes**: The Changes panel now keeps the last successful diff visible while background refreshes run or fail, using small stale/refresh indicators instead of replacing existing results with loading placeholders.
- **New Session Repository Picker**: The location picker now shows inline progress while inspecting paths, loading repository options, refreshing repo metadata, creating worktrees, and deleting worktrees so slow git operations do not leave the dialog looking frozen.
- **Opening Pull Requests**: Opening a PR in a new worktree now shows launcher-level progress while attn fetches PR details, syncs the local repository, creates the worktree, and starts the session.

---

## [2026-05-03]

### Added
- **Native Settings Parity**: The native Settings window now mirrors the Tauri settings surface with sections for appearance, projects, agents, review-loop models and prompts, mobile web, remote endpoints, GitHub hosts, muted filters, PTY backend state, and native sidebar controls.
- **Native Sidebar Settings Entry**: The native workspace sidebar now has a bottom settings cog and `Cmd+,` shortcut that open a native Settings surface with a sidebar display control.
- **Native Canvas Trackpad Zoom**: The native canvas now responds to trackpad pinch/magnify gestures and precise control-scroll zoom, keeping the cursor's world point anchored while zooming.
- **Native Canvas Panel Status**: Native canvas panel headers now show each session's live state, including working, waiting for input, approval-needed, idle, and long-run review status.

### Changed
- **Native Terminal Engine**: Native canvas terminals now use Ghostty's `libghostty-vt` terminal emulator for VT parsing, screen state, cursor position, wide-cell handling, and ANSI color resolution while keeping the existing daemon PTY transport.
- **Native Sidebar Collapse**: The native workspace sidebar can now collapse with `Cmd+B`, keeping workspace status colors visible in the narrow rail while freeing canvas space.

### Fixed
- **Native Canvas Shell Splits**: `Cmd+D` and `Cmd+Shift+D` now skip over already-occupied adjacent panels instead of opening a new Shell panel on top of an existing neighbor.

---

## [2026-05-02]

### Changed
- **Native Canvas Focus Treatment**: Keyboard-focused panels now get the stronger sodium highlight ring and glow, while selected-but-not-focused panels use a quieter selected border with a small moving light around the edge so selection and typing focus read as distinct states.
- **Native Canvas Shell Splits**: `Cmd+D` now opens a new Shell panel to the right of the selected native-canvas panel, and `Cmd+Shift+D` opens one below it. Split panels inherit the anchor panel's size, persist their daemon-owned geometry, and take input focus once attached.

### Fixed
- **Native Location Dialog Crash**: Opening a new native-canvas session from the location dialog no longer crashes when the dialog submit is triggered by an asynchronous daemon path inspection or worktree result while the root app is already processing that daemon event.

---

## [2026-05-01]

### Changed
- **Native New Session And Workspace Picker**: The native canvas now opens a shared Observatory-styled location dialog for both "+ Session" and "+ New Workspace". The dialog supports agent selection for sessions, path entry with directory suggestions, repository destination selection, existing worktrees, and creating a new worktree from either the current branch or the default branch before opening it.
- **Native Sidebar Polish**: The workspace rail now shows a count beside the section header, and the active workspace gets a thin sodium-orange accent stripe along its left edge so the selection reads at a glance even when the row's background is otherwise unchanged. The "+ New Workspace" affordance is set apart by a faint divider so it no longer competes with live workspaces. Panel title bars across the canvas now carry the same hairline + primary-text treatment for visual consistency. The opt-in FPS overlay (`ATTN_NATIVE_FPS=1`) is reorganized into a labeled card where only the live frame-rate number takes the working-state hue, so the eye lands on the one number that matters.
- **Observatory Design System**: Extended the native theme module with hairline (`theme::line`), translucent sodium tints (`theme::sodium::soft` / `glow` / `hush`), and motion-duration tokens (`theme::motion`). New invariants are guarded by tests so the design rules can't drift: the hairline scale is monotone, the sodium tints share the canonical hue, and halted breath cycles faster than live breath. Updated visual plates at `docs/prototypes/native-design-system-observatory.html` with editorial polish — folio marks at the bottom of each plate, a continuous "spine" hairline running through the bound set, a sodium scroll-progress hairline, and a refined three-star brand mark.
- **Native Canvas Panel Snapping**: Moving or resizing native-canvas panels now magnetically aligns nearby edges and centers to other panels, keeping a small gap between neighboring panels and showing short contextual guide lines while a lock is active.
- **Native Canvas Focus Modes**: `Cmd+Enter` now focuses the selected panel and fits the canvas viewport around it without changing panel geometry. `Cmd+Shift+Enter` toggles a temporary window-wide panel fullscreen mode that covers the sidebar without resizing or persisting the panel.
- **Native Canvas Clipping And Keyboard Panning**: Zoomed native-canvas panels are now clipped to the canvas area instead of painting over the sidebar. `Shift+h/j/k/l` now pan the canvas alongside `Shift+Arrow`, and new native terminal panels are taller by default.
- **Native Canvas Panel Placement**: New native-canvas sessions now use larger default terminal panels and place newly spawned panels in the visible canvas when space is available. When a panel is selected, new panels prefer clockwise slots around it before falling back to other visible space or the closest non-overlapping off-screen slot. `Shift+Arrow` now pans the canvas from the keyboard without changing panel selection.

---

## [2026-04-30]

### Added
- **Native Canvas Session Lifecycle**: The native canvas now spawns and tears down sessions inline. A "+ Session" pill at the top-left of the canvas expands to a Claude / Codex / Shell agent picker; clicking an agent spawns a new session in the selected workspace's directory and the panel appears on the canvas as soon as the daemon broadcasts it. Each panel's title bar gains an `x` close button that asks the daemon to unregister the session, after which the panel is pruned automatically. Spawn failures are recorded as structured events (`session_spawn_failed`) carrying the wire error so they're discoverable from the automation tail. Two new automation actions — `spawn_session` and `unregister_session` — let the test harness drive the same path as the UI; the canvas scenario covers them end to end.
- **Native Canvas Keyboard Panel Navigation**: When the canvas has focus, `Tab` / `Shift+Tab` cycle panels in reading order and arrow keys or `h`/`j`/`k`/`l` select the nearest panel in that direction. The bindings only run in canvas-selection mode; once a terminal panel has input focus, keystrokes continue to route to the PTY until `Esc` releases focus.

### Changed
- **Native UI Architecture Conventions**: Reorganized the native canvas crate into four named layers — `adapters/` (websocket, automation sidecar), `state/` (workspaces, panels, terminal models, the new `WorkspaceRegistry`), `views/` (sidebar, canvas, terminal view, FPS overlay), and `domain/` (pure logic — viewport math). Conventions and decision rules are written down in `native-ui/AGENTS.md` (with `CLAUDE.md` symlinked) so future changes have an obvious home. Also extracted `WorkspaceRegistry` from `NativeApp`, pulling workspace ownership, selection, and pending wire-ack tracking out of the root view and giving `NativeApp` only the wiring job. No behavior changes — file moves + dependency reshape only.
- **Native UI — DaemonClient is now a pure adapter**: Removed the `sessions` and `workspaces` caches that lived inside `DaemonClient` and exposed them through accessors. The adapter now parses the wire and emits granular events (`InitialState`, `SessionRegistered`, `SessionUnregistered`, `SessionStateChanged`, `SessionsReplaced`); a new `SessionRegistry` on the state side owns the canonical session list, mirroring `WorkspaceRegistry`. Views and automation read sessions through `NativeApp::sessions_snapshot()` instead of reaching into the adapter. The reconnect-diff (workspaces removed during a daemon restart) moved out of the adapter into `NativeApp::apply_initial_state` so the rule "domain state lives in state registries" holds end-to-end. No user-visible behavior change.
- **Native Canvas Panel Geometry Is Daemon-Owned**: Workspace snapshots now include daemon-owned terminal panels with stable panel IDs and world-space geometry. The daemon creates, persists, restores, updates, and removes those panels as sessions join or leave workspaces; the native canvas renders only the panel list it receives from the daemon and sends drag/resize commits back through `update_workspace_panel_geometry`. Protocol version moves 57 → 58.
- **Native Canvas Focus Is Two-Stage**: Terminal panels now distinguish canvas selection from terminal input focus. Clicking a title bar or empty canvas keeps keyboard control with the canvas, clicking the terminal body enters input focus, `Esc` releases input focus, and `Enter` re-enters the selected panel. The automation snapshot exposes both states so the native canvas scenario can assert that terminal keystrokes are blocked while a panel is only selected.

### Removed
- **Native Synthetic Load Harness**: Removed the local-only native canvas synthetic workspace and placeholder panel scaffolding, so native workspaces and terminal panels now come from daemon-backed state only.

---

## [2026-04-29]

### Changed
- **Smarter Commit Checks**: The pre-commit hook now chooses validation by staged file path and auto-formats staged Go, native Rust, and Tauri Rust files when safe. Native canvas changes run daemon checks plus native Rust format, clippy, and tests; Tauri changes run daemon checks plus Tauri Rust format, clippy, and tests; frontend, shell, and daemon-only changes stay scoped to their relevant suites.

### Fixed
- **Codex Sessions No Longer Hang On Startup**: When a Codex session was opened, Codex's TUI emitted terminal capability queries (cursor position, device attributes, kitty keyboard, OSC 10) and waited for the responses before drawing anything. If those queries arrived at the daemon's PTY stream before the frontend terminal had attached, the responses never came back and Codex stayed stuck on a blank screen forever. Two coordinated changes fix this end-to-end: the daemon now replays early scrollback for Codex sessions on fresh-spawn and same-app-remount attaches, and the frontend now applies that replay (instead of always discarding it on non-relaunch policies), so the terminal processes the buffered queries and emits the responses Codex is waiting for.
- **Codex Sessions No Longer Strand The Input Prompt After Resize**: Switching sessions, opening side panels, or changing the UI scale could push tiny intermediate measurements (e.g. 10×6) into a Codex pane's PTY. Codex's inline TUI redraws at the small size and then can't recover when the pane returns to full size — the input prompt ends up far above the cursor with dozens of blank rows below it, and any keystroke auto-scrolls the prompt out of view. Two changes fix the root cause: (1) `getScaledDimensions` now rejects sub-usable grid measurements (≤20 cols or ≤10 rows) so transient layout states never reach the PTY at all, and (2) inactive sessions no longer forward terminal resizes — their PTY keeps its previous geometry until the user actually selects the session.
- **Shell Panes In Tight Splits No Longer Stay Dead, And The Codex Floor Is No Longer A Layout Filter**: The codex small-grid protection above was doing two unrelated jobs at once — filtering transient layout-state measurements (panel animations, grid tracks resolving mid-mount, sidebar collapse layout shifts) AND enforcing codex's "don't poke me with a sub-20×10 SIGWINCH" requirement — at the same `getScaledDimensions` choke point. That conflation meant a legitimate 14-col shell pane in a 3-way split was rejected as "transient garbage", `terminal.ready` never fired, and the new pane silently received no PTY data. The two concerns are now separated: `getScaledDimensions` rejects measurements that are implausibly small relative to the pane's "fair share" of the window (window size ÷ pane count × 30%), which catches transient layout states for any pane regardless of kind; codex's 20×10 SIGWINCH protection moves to the resize-send point and applies only to main panes. A 3-way split shell pane at 17 cols is now well above its layout-aware floor and initializes normally; a 1-pane window reporting 10 cols mid-animation is still rejected as a transient regardless of which kind of pane it is.
- **Native Workspace Deletion Polish**: The native workspace sidebar now asks for an inline confirmation before deleting a workspace, keeps the row in a pending state while the daemon unregisters it, surfaces command-send failures in the sidebar, and automatically selects another workspace after the selected one is deleted.

---

## [2026-04-28]

### Added
- **Native Canvas UI Test Automation Sidecar**: The native GPUI canvas app (`attn-spike5`) now exposes a TCP automation sidecar matching the Tauri app's bridge format, so external test scripts can drive both apps with the same client. Actions cover liveness (`ping`), full state inspection (`get_state`), session enumeration (`list_sessions`), window geometry (`get_window_geometry`), workspace selection (`select_workspace`), panel manipulation (`move_panel`), PTY input/inspection (`send_pty_input`, `read_pane_text`), and a structured event tap (`tail_events`) for cursor-based polling of in-process events when behavior needs verification beyond what get_state can show. The sidecar writes a discovery manifest at `~/Library/Application Support/com.attn.native[.<profile>]/debug/ui-automation.json` mirroring the Tauri layout but namespaced per profile. A `make scenario-native-canvas` target runs an end-to-end harness scenario that connects to the live binary and exercises the bridge.
- **Client Capability Handshake**: New `client_hello` daemon command lets clients identify themselves and advertise capabilities at connection time. The native canvas app sends `shell_as_session` (treats shell agents as first-class workspace sessions); the Tauri app sends an empty capability set and continues to filter shell-as-session panels out of its sidebar. Lets the daemon serve different behaviors per client without making one client's mental model leak into the other. Protocol version bumped 56 → 57, which forces the standard daemon/app version-mismatch reconnect on next install.

### Changed
- **UI Automation Gating Is Now Runtime, Not Build-Time**: Both the Tauri app and the new native sidecar decide whether to run the automation bridge at startup based on `ATTN_AUTOMATION` (explicit `1`/`0`) and `ATTN_PROFILE` (defaults on for `dev`, off for prod). Previously the Tauri side was gated by `ATTN_UI_AUTOMATION` / `VITE_UI_AUTOMATION` baked in at compile time, which meant every `make install` shipped a build with the bridge always-on. The `make build-app-ui-automation` target is gone; both `make install` and `make dev` now build the same binary and the runtime decides. **Breaking**: anyone relying on automation against a prod source install must now launch with `ATTN_AUTOMATION=1` to enable it. The bridge token has also moved from a predictable pid+nanos string to 32 OS-random bytes hex-encoded.

### Fixed
- **Automation Server Now Echoes Wire ID On Malformed Requests**: When a request fails JSON deserialization the server now best-effort extracts the raw `id` field (string, number, or bool) and echoes it back on the error response. Previously a malformed request was answered with a synthetic `ui-automation-{counter}` id that no client could correlate, so a multiplexing client looking up its own id would never match the response and the request would hang silently until timeout.

---

## [2026-04-26]

### Added
- **Daemon Workspace Entity (Native Canvas-UI Plumbing)**: The daemon now tracks a top-level `Workspace` concept distinct from sessions — a directory + multiple agents + panels that the upcoming native canvas UI groups together. Two new WebSocket commands (`register_workspace`, `unregister_workspace`) and three new events (`workspace_registered`, `workspace_unregistered`, `workspace_state_changed`) cover the lifecycle. Each workspace carries a rolled-up status computed from its member sessions (priority: working > waiting_input > pending_approval > idle > launching), recomputed and broadcast whenever any owned session's state changes. Sessions gain an optional `workspace_id` field, populated from the `workspace_id` you can now pass on `spawn_session`, and `initial_state` includes the current `workspaces` array. Workspaces and their session associations are durable: a new `workspaces` SQLite table plus a `workspace_id` column on `sessions` survive daemon restart, and the in-memory registry is rebuilt from disk on every start. Closing a workspace (via `unregister_workspace`) cascade-closes its sessions with a graceful SIGTERM, the same path the unix-socket `unregister` command uses. Registering a workspace upserts its directory into recent locations alongside CLI-spawned session directories. The Tauri app ignores all of this (the new events fall through its event switch as no-ops); only the protocol version moves from 55 to 56, which forces the standard daemon/app version-mismatch reconnect when you next install.

---

## [2026-04-25]

### Added
- **Per-Endpoint Profile For Remote Daemons**: Remote endpoints now carry a profile (the same knob as local `ATTN_PROFILE`), so a dev-profile attn can drive a dev-profile remote daemon side-by-side with prod. Set per endpoint in Settings (defaults to the calling daemon's own profile); threads through binary install path (`~/.local/bin/attn-<profile>`), data dir (`~/.attn-<profile>/`), socket, log, and WebSocket port on the remote. Existing endpoints stay on the default profile and behave exactly as before.

---

## [2026-04-23]

### Added
- **Side-by-Side Dev Install For Safe Attn-On-Attn Testing**: You can now run a development copy of attn alongside your live install without either stepping on the other. `make dev` builds and installs a sibling `~/Applications/attn-dev.app` with its own bundle identifier (`com.attn.manager.dev`), data directory (`~/.attn-dev/`), socket, log, and WebSocket port (`29849`). The dev app runs its own daemon, which never touches the prod daemon's state. The prod `make install` / `make install-daemon` targets refuse at parse time if `ATTN_PROFILE` is set in your shell, so you can't accidentally reinstall the live app while iterating on dev. For CLI work, `eval "$(attn profile-env dev)"` scopes every subsequent `attn` command to the dev daemon (`attn profile-env --fish dev | source` for fish); `attn profile-env --unset` reverts. The underlying knob is `ATTN_PROFILE=<name>`, which derives all paths and ports per profile; the default profile keeps today's behavior exactly. Dial failures now tell you which profile and socket they tried, and point at the other profile's daemon if it happens to be running.

---

## [2026-04-22]

### Changed
- **Internal Rename To Free The "Workspace" Name For A New Concept**: The daemon's split-pane-layout concept is now called "session layout" everywhere. The Go package moved from `internal/workspace` to `internal/sessionlayout`, the `WorkspaceSnapshot` type became `SessionLayout`, and the daemon↔client protocol renamed all `workspace_*` commands and events to `session_layout_*` (e.g. `workspace_split_pane` → `session_layout_split_pane`, `workspace_snapshot` → `session_layout`). Protocol version bumped from 53 to 54 so a stale daemon or client fails the version check instead of silently drifting. No user-visible behavior changes — this frees "workspace" for the upcoming top-level container that groups multiple sessions together.

### Fixed
- **Closing A Session Restores The Previous Selection**: Closing the active session used to leave the UI with nothing selected because the daemon sync cleared `activeSessionId` as soon as the session disappeared and the local removal path fell back to an arbitrary first-in-list. The store now tracks recently-active session IDs as an MRU history; when the active one goes away (either via explicit close or a daemon-driven removal such as a remote session dropping), the UI re-selects the most recent still-existing entry. Ghost IDs are pruned on every removal and filtered again when choosing a fallback, so sessions that were closed without ever being focused can't resurrect themselves.

---

## [2026-04-21]

### Fixed
- **Window Resize Scenario No Longer Depends On AppleScript Seeing The Tauri Window**: `scenario-tr401-local-window-resize`'s `normalize_window_bounds` step relied on AppleScript `System Events` for both reading and setting the window's position and size, which is unreliable for Tauri/wry windows — `count of windows of targetProcess` can return 0 even with a visible window, especially when attn was launched with `open -g` (background). The get path already had a UI Automation bridge (`get_window_bounds`), but its Rust shortcut returned a shape (`{x, y, width, height}` in physical pixels) the harness didn't understand (it expected `{logicalBounds: {...}}`), so every call silently fell through to the AppleScript fallback. The set path had no UI Automation option at all. Align the Rust `get_window_bounds` shape with the JS bridge, add a matching `set_window_bounds` that drives Tauri's window API directly, and let the harness try UI Automation before reaching for AppleScript.
- **Terminal Ready Gate No Longer Gives Up Silently When Layout Isn't Settled**: A freshly-split pane's `ResizeObserver` fires one RAF to measure the terminal and call `onReady`. If the container isn't positioned yet, `getScaledDimensions` returns null; if the renderer hasn't measured cells yet, the result has zero cols/rows. Both cases previously dropped out of the callback without scheduling another attempt, and no later ResizeObserver entry was guaranteed to re-run the check — the pane stuck at `runtimeAttached: false` and downstream scenario gates (`waitForPaneAttached`) timed out with no surviving clue. The gate now retries the RAF for up to ~1 s with a 50 ms backoff on null/zero dims, logs the bail branch (container disconnect vs null vs zero) on disk, and gives up loudly after the ceiling. Same symptom was intermittently hitting tr204 and deterministically hitting tr401 after the AppleScript fallback was removed.

- **Terminal Goes Blank After Closing A Split In A Relaunched Remote Session**: The websocket subscriber id was derived from `<client_ptr>:<sessionID>`, which stays stable when the same client reattaches to the same session during remount/relaunch. The PTY session's subscriber map is keyed by that id, so a second attach silently overwrote the first subscriber's callback in place; then when the first stream closed it emitted a `Detach` RPC that removed the subscriber id — now pointing at the *new* stream — leaving the session with no one to forward output to. Claude's post-SIGWINCH redraw bytes continued to be generated but had no subscriber, so the pane kept showing the stale narrow frame from before the close. Each attach now gets a monotonically unique subscriber id, so a dying stream's detach removes only its own subscription. Verified with the tr205-claude scenario (previously 0/2 passes, now 3/3) and a dedicated byte-level PTY probe that confirmed claude does emit full redraws on every resize.

### Changed
- **`scenario-tr201-local-relaunch-existing-split` Waits For Post-Split Reflow Before Baseline Capture**: After `split_pane`, the terminal keeps the pre-split wide buffer until the agent (claude) responds to SIGWINCH with a fresh narrow redraw. Sampling the main pane's visible content during that window captured the stale wide frame as the baseline, then after relaunch the restored pane showed the correct narrow frame. Baseline capture now waits for `visibleContent.summary.maxLineLength <= visibleContent.cols` before recording the baseline, eliminating the race.

---

## [2026-04-20]

### Changed
- **Harness Typing Is Focus-Agnostic**: `typeTextViaInput` no longer short-circuits when the target pane's input surface is not active, and no longer changes focus just to dispatch text. Pre-typing `waitForPaneInputFocus` waits that were only there to satisfy the old focus guard were removed from tr201, tr204, tr301 setup, tr401, and tr502. The two calls that actually assert focus as product behavior stay: tr301's "utility regains focus after session switch" and tr303's "click_pane focuses main."
- **Harness Restores The Caller's Frontmost App On Exit**: `UiAutomationClient` captures the caller's frontmost bundle at `launchApp()` time and re-activates it when `quitApp()` returns. Scenarios still run attn in the foreground because `open -g` puts the Tauri WKWebView into `NSWindowOcclusionState.occluded`, where rAF and ResizeObserver throttle and freshly-split panes stall at `runtimeAttached: false`. A small visible-but-not-key "corner panel" variant was prototyped (`#parkWindowAsPanel` in `uiAutomationClient.mjs`) but 3/3 tr204 runs under parking timed out on `waitForPaneAttached` vs 2/3 under floor on the same day, so we didn't ship it. The `#parkWindowAsPanel` helper is retained unused for follow-up experiments (e.g. always-on-top panel via a Tauri-side window config). With the restore in place, the visible effect of the current ship: attn comes forward when the scenario starts, runs, then the caller's original app (Slack, Ghostty, iTerm, etc.) automatically comes back when the scenario quits attn.

### Fixed
- **Harness No Longer Writes To Unattached Panes**: `scenario-tr401-local-window-resize` and `scenario-tr204-local-relaunch-formatting` now wait for `runtimeAttached=true` before writing into a freshly split pane. Previously, the focus gate could return while the pane was still finishing `attach_session`, leaving output only in worker scrollback. A companion `runtimeAttached` field is exposed through `get_pane_state` for scenarios that assert on it directly.

---

## [2026-04-19]

### Changed
- **Pane Paint-Coverage Assertions Are Focus-Free**: Harness scenarios (tr401, tr402, tr205, tr502) no longer screencap the attn window to assert post-close/post-resize paint coverage. Coverage is now computed from the terminal buffer through `analyzePaneTextCoverage`. The pixel path required attn frontmost because WKWebView serves a stale backing store while occluded; the text path works regardless of compositor state.
- **Real-App Harness Preserves Focus**: The `pnpm --dir app run real-app:smoke` flow now drives the packaged app entirely through the in-webview automation bridge — splitting, focusing, and typing into panes no longer synthesize `CGEvent` keystrokes or call `NSRunningApplication.activate(...)`. The packaged app is also launched with `open -g` (background) so it never steals frontmost on boot. Contributors can keep using another app while the harness runs.
- **`repro-older-pane-writability` Runs Focus-Free**: The older-pane repro is now bridge-only: splits, focus moves, and typed input all route through the automation bridge while still checking input ownership.
- **`scenario-tr502-remote-relaunch-splits` Typing Is Focus-Free**: The three remote-shell typing phases now go through `type_pane_via_ui` instead of `CGEvent` keystrokes, dropping three activations per run. The scenario still activates attn briefly on launch (the remote harness needs env-var delivery via spawn); the per-assertion activation is gone as of the text-coverage change above.
- **Spawn-With-Env Launch Bounces Focus Back To The Caller**: When a real-app scenario launches the packaged app via spawn (currently tr502, because LaunchServices and `open -g` don't propagate env vars into Tauri's window-creation path), the harness now records the caller's frontmost bundle before spawning, waits for attn's main window to appear via `CGWindowListCopyWindowInfo` (an AppleScript `count of windows` gate is unreliable for Tauri/wry — it returns 0 even with a visible window), then reactivates the caller. Attn is frontmost for roughly 300 ms per launch instead of the whole scenario.

### Added
- **`real-app:focus-probe` Scenario**: A synthetic scenario that activates Terminal.app as a witness, then exercises a split via both the legacy CGEvent path and the bridge-only path, recording `NSWorkspace.frontmostApplication` before and after each. It fails fast if the bridge path steals focus, and emits a `focus-probe.json` artifact for regression tracking.
- **AX-Based Input Driver Subcommands**: `InputDriver.swift` gained `activate_background`, `menu --path "File>New Session"`, and `frontmost` subcommands. The menu walker uses `AXUIElementPerformAction(kAXPressAction)` against native AppKit menus so window-chrome actions no longer require the app to be frontmost.

### Removed
- **`capture_window_screenshot` Automation Action**: The Tauri-side action (and its underlying `focus_main_window` helper) is gone. It called `window.set_focus()` + `screencapture -R` to feed harness pixel-coverage analysis; that pipeline has been replaced by the in-process text-coverage assertion described above. Diagnostic scripts that still want a PNG of the attn window continue to call `captureFrontWindowScreenshot` from `nativeWindowCapture.mjs`, which resolves bounds without activating the app. The webview-internal `capture_screenshot` action (used elsewhere for DOM-level snapshots) is unchanged.

---

## [2026-04-18]

### Added
- **OSC 52 Clipboard Forwarding**: When a remote program (Claude Code, vim, tmux, etc.) asks the terminal to copy text via the OSC 52 escape sequence, attn now writes that text to your Mac clipboard. This works over SSH to a Linux host without xclip or an X server — the copy happens in the Mac-side terminal emulator, not on the remote. Read requests (`OSC 52 ; c ; ?`) are refused to avoid leaking your clipboard to whatever is running on the remote.

### Changed
- **Selecting Text While An Agent Is Running**: Agents that enable terminal mouse tracking (notably Claude Code) forward click-drag to the agent instead of creating a selection, so attn's copy-on-select never fires. Hold **Option** while dragging to force a native selection — the same convention iTerm2, Terminal.app, and kitty use. This is now documented in the README.

### Fixed
- **Terminal Link Clicks**: Cmd/Ctrl-clicking a URL in the terminal opens it once, whether the TUI renders the URL as an OSC 8 hyperlink or as plain text. Fixes a regression where cmd-click stopped opening plain-text URLs, and where URLs that matched both renderings could open twice.
- **Diff Panel Font Scaling**: Cmd+=/Cmd+- now resizes the diff panel's file list and editor regardless of which pane is focused. The panel previously kept its own local font size that only updated when the panel itself had focus, and the file list ignored the global UI scale entirely.
- **Diff Panel `]` Advances Consistently**: Pressing `]` in the diff panel now marks the current file viewed and advances to the next file that still needs review. Previously `]` always searched from the top of the list, so once it landed on the first unreviewed file repeated presses were no-ops until `j`/`k` moved the selection elsewhere.
- **Diff Panel Keyboard Shortcuts On Open**: Opening the diff panel now moves focus into the panel so `j`/`k`, `]`, `e`, and friends work immediately. Previously the terminal kept focus and its hidden textarea swallowed the first keystrokes.
- **ChangesPanel → Diff Panel Clicks Always Apply**: Clicking a file in the ChangesPanel while the diff panel is already open now switches the diff view to that file (and re-clicking the same file after in-panel `j`/`k` navigation jumps back to it). Previously the panel only honored the first click on open.
- **Diff Panel `e` Toggles Hunks/Full**: `e` now flips between the Hunks and Full views that the UI actually supports. The previous handler tried to progressively widen context with `+10` per press, but the editor hardcoded three lines of context, so presses after the first did nothing visible and the "Hunks" button silently lost its highlight.
- **New Untracked Folders Show Individual Files**: When a branch adds a brand-new directory full of new files, the diff panel now lists each file instead of collapsing the whole folder into a single unusable entry. The branch-diff query was using `git status --porcelain` with its default `--untracked-files=normal`, which reports new directories as one `?? dir/` line.

## [2026-04-17]

### Fixed
- **Remote Endpoint Zombie Leak**: Failed WebSocket dials to remote endpoints over SSH no longer leave `<defunct>` `ssh` children behind. On macOS a slow or flapping remote could accumulate thousands of zombies over a day and exhaust `kern.maxprocperuid`, producing `fork: Resource temporarily unavailable` across the whole user session.

## [2026-04-16]

### Added
- **Send To Claude From Diff Panel**: Unresolved review comments can again be sent to the active agent session from the diff panel — individually or grouped as "Send unresolved (N)". The panel closes on send so the terminal is visible.
- **Multi-Line Comment Ranges**: Selecting multiple lines and adding a comment now preserves the full range and includes it in send-to-Claude references (e.g. `L5-L10`).
- **Dashboard Pane Debug Toggle**: A toggle in the Dashboard footer enables pane runtime debug logging for diagnosing terminal focus and render issues.

### Fixed
- **Comment Form Focus Theft**: Typing in a diff-panel comment or search form no longer loses focus as the terminal emits output in the background. Overlapping focus-retry chains and an unstable workspace binder were letting terminal focus preempt active form input.
- **Ghost Click Targets In Closed Dock Panels**: Closed side panels no longer intercept clicks on the diff editor. Descendants with `pointer-events: auto` could override the panel's `pointer-events: none`; closed panels are now fully `inert`.
- **Duplicate Terminal Link Opens**: Cmd-clicking a link in the terminal no longer opens the URL twice. The inline link handlers were redundant with the Tauri opener plugin.
- **Orphan Comment Cross-Branch Cleanup**: Switching branches while the diff panel was open could misclassify valid comments on the new branch as orphaned and auto-delete them. Cached diff data is now cleared on branch change so cleanup waits for fresh data before running.

### Removed
- **Won't Fix**: The "won't fix" review-comment state has been removed end to end — UI, protocol, daemon handlers, and store fields. Migration 33 drops the related columns. Comments are now strictly resolved or unresolved.

## [2026-04-14]

### Fixed
- **Mute Remote Sessions**: Muting a session on a remote endpoint now works correctly. The mute command was not being forwarded to the remote daemon, causing it to silently no-op.
- **Sync Button Version Re-Prompt**: After successfully bootstrapping a remote endpoint, the home screen no longer re-shows the version-mismatch banner on the next connection attempt.
- **Inline Comment Form Stability**: Pasting into a comment textarea now works correctly (keystrokes were being intercepted by the editor). Opening a comment on one file, switching to another file, and switching back now restores the open form and any typed draft. Pressing Escape to dismiss a form now clears the draft so stale text cannot be submitted later.

## [2026-04-13]

### Added
- **Muted Sessions**: Sessions can now be muted to park them out of active attention surfaces without closing them. A 🔕 button appears on session rows in the sidebar on hover; muted sessions move to a collapsed "Muted Sessions" section at the sidebar bottom. The dashboard shows a "Muted Sessions (N)" summary group that navigates to the sessions view and expands the muted section on click. Unmuting restores the session to the normal list in its current state. Muted state persists across restarts.

## [2026-04-12]

### Fixed
- **Remote Endpoint Sync Detection**: When connecting to a remote daemon built by a different machine, the home screen now shows a warning banner with a Sync button instead of silently connecting. Clicking Sync reinstalls the remote binary from your local build and restarts the daemon. The endpoint is shown as unavailable (greyed out in the location picker) until synced.
- **Remote Endpoint Bootstrap Loop**: Reconnecting to a remote endpoint after a clean disconnect no longer re-bootstraps the daemon binary. Bootstrap now runs only on the first connection attempt per loop lifetime, or when the daemon is actually unreachable — eliminating the ping-pong between two machines sharing the same remote daemon.
- **Protocol Version Mismatch Banner**: Connecting to a remote daemon running a different protocol version surfaces the same Sync banner instead of auto-reinstalling. A "version ahead" warning is shown when the remote is newer than the local client.

## [2026-04-12]

### Added
- **Native UI Spike 4 — Terminal Panels on Canvas**: New `attn-spike4` binary merges the terminal surface (spike 2) with the infinite canvas (spike 3). Up to three live agent terminals attach to daemon sessions and appear as draggable, resizable panels on the canvas. Click a terminal body to focus it for keyboard input; drag its title bar to move it; drag corner handles to resize it and reflow the PTY. Zoom changes update visible cell counts automatically. A blue border highlights the focused panel.
- **Native UI Spike 3 — Infinite Canvas**: New `attn-canvas` binary proves the GPUI-based canvas interaction model. Pan by dragging empty space, zoom toward cursor with Cmd+scroll or regular scroll, drag panels by their title bars, and resize panels by dragging their corner handles. Eight dummy panels are laid out at fixed world positions across an infinite dot-grid canvas. Viewport culling skips off-screen panels every frame.

### Fixed
- **Location Picker Viewport Shift**: Closing the location picker no longer shifts the entire viewport left. WebKit's `scrollIntoView` was walking past the fixed overlay and scrolling the app shell; `overflow: clip` on `.app` prevents this.

## [2026-04-11]

### Changed
- **App-Owned Daemon Startup**: Desktop app startup now routes through a bundled-binary `attn daemon ensure` flow so the app asks its own runtime to reconcile, replace, and health-check the local daemon before connecting instead of treating any live socket as good enough.
- **Source Dev Install Flow**: `make install` now updates the installed app's bundled `attn` sidecar and runs `daemon ensure`, while `make install-all` becomes a deprecated shim to the simpler default source install path.
- **Install Surface Simplification**: Remove the CLI-only Homebrew formula from the supported install and release surface, update cask/docs accordingly, and stop steering app users toward a standalone `attn` install as part of normal setup or upgrades.
- **New Session Location Picker**: The location picker now keeps the typed path and highlighted result separate, so arrow keys and clicks move a transient highlight without rewriting the query, hover stays passive throughout the flow, dialog shortcuts are owned by the picker instead of a global key trap, and `Tab` completion stays an explicit accept action instead of silently becoming the path that launches.
- **Repo And Worktree Chooser**: The repo chooser now opens only for actual repo or worktree roots, preselects the exact worktree path you typed even when Git reports canonicalized filesystem paths, keeps remove/create flows on physical destinations, and focuses on openable destinations plus worktree actions instead of mixing in branch rows.
- **YOLO Target Toggle UI**: The repo chooser no longer shows a separate launch-mode row or `⌥Y` affordance; YOLO is once again exposed only by selecting the same target again, with that behavior explained in the picker copy.

### Fixed
- **Packaged-App Daemon Upgrade Reconciliation**: App startup now verifies that the live local daemon matches the daemon binary the current install would launch, so Homebrew and packaged-app upgrades replace stale daemons from older install paths instead of silently reusing them until session startup fails.
- **macOS Worker Binary Fallback**: Worker-side PTY recovery now checks both `~/Applications/attn.app` and `/Applications/attn.app`, so packaged installs can recover the bundled `attn` binary even when Homebrew placed the app in the system Applications folder.
- **Location Picker Path And Chooser Stability**: Submitting `/` now inspects the filesystem root instead of collapsing to the current directory, and stale inspect or repo-info responses no longer reopen old chooser state after you change targets, go back, or close and reopen the picker.

## [2026-04-10]

### Added
- **Packaged-App Terminal Validation**: Added a serial packaged-app regression matrix plus relaunch, resize, focus, redraw, and remote cleanup scenarios, along with deterministic replay fixtures and richer visible-content and native-render diagnostics for validating real terminal behavior instead of relying on screenshots or full-buffer text alone.

### Changed
- **Terminal Runtime And Harness Discipline**: Reorganized PTY attach, replay, geometry, and pane lifecycle handling into clearer frontend runtime helpers, and tightened packaged-app preflight, build fingerprint checks, scenario locking, and remote isolation so the app and harness consistently exercise the intended binaries and session state.

### Fixed
- **Split, Relaunch, And Replay Stability**: Improved split-close recovery, focus handoff, relaunch restore behavior, Codex header preservation, same-direction split sizing, hidden-session geometry handoff, remote split readiness, and worker cleanup so terminals keep the right content, width, and responsiveness across close, remount, resize, and app-reopen paths.

### Removed
- **Temporary Relaunch Fuzz Canary**: Removed the packaged-app `TR-206` relaunch fuzz scenario after replacing it with more deterministic replay-level coverage.

## [2026-04-07]

### Fixed
- **Codex Idle Classification On Remote Daemons**: Stop-time Codex classification now runs from the session repository directory instead of the daemon's ambient working directory, so remote Codex sessions no longer fall back to `unknown` just because the classifier subprocess hit the CLI trust check.
- **Remote Split Freeze On Codex Sessions**: WebSocket attach replay now drops redundant full scrollback when a fresh screen snapshot is available, so opening a split on a remote Codex session no longer has to push multi-megabyte replay payloads through the UI just to redraw the current screen.
- **Remote Split Shell Reattach Routing**: Split-pane utility runtimes now inherit their parent session's endpoint and are treated as daemon-known PTYs, so reopening or remounting a remote split attaches to the existing remote shell before falling back to respawn instead of hanging on a misrouted local `spawn_session`.
- **App Launch Terminal Replay Weight**: WebSocket attaches now derive a visible-frame snapshot from buffered PTY output when no live snapshot exists, so reopening the app no longer has to replay multi-megabyte hidden-session scrollback just to restore the current screen.

### Added
- **Endpoint Re-bootstrap Action**: Remote endpoint cards in Settings now include a `Re-bootstrap` action that disables and re-enables the endpoint in one click, making it easier to force the local daemon to re-push and reconnect a remote daemon after local installs.

### Changed
- **`Cmd+W` Close Semantics**: `Cmd+W` now stays inside the app and closes the active utility pane when one is selected, otherwise closes the active session, so it no longer falls through to closing the whole app window just because focus is outside a split terminal.
- **Split Session Close Confirmation**: Closing a session that still has split terminals open now asks for confirmation before tearing down the whole session, with `Enter`, `Space`, or `Y` confirming and `N` or `Esc` canceling.
- **PR Review Provider**: Replace the patched Hodor GitHub workflow with `victorarias/shitty-reviewing-agent`, running on OpenRouter with `minimax/minimax-m2.7` and the `OPEN_ROUTER_API_KEY` secret. Removes the `.github/hodor` patch and `.hodor` skill directory along with it.

---

## [2026-04-06]

### Changed
- **Embedded Phone Terminal**: Rebuild the daemon-served mobile web client around `ghostty-web` with a dedicated session list, a terminal-first phone layout, embedded build metadata, and the existing PTY websocket protocol.

### Fixed
- **Embedded Phone Terminal Stability**: Real-phone scrolling, typing, quick actions, keyboard open/close, and keyboard-dismiss behavior now work without depending on attach replay hacks, including a dedicated `Ctrl+C` shortcut and no-store asset serving.
- **Embedded Phone Terminal Layout**: The web terminal now commits the visual viewport width as well as height, and exposes `A-` / `A+` controls in the top bar through a settled resize transaction so narrow phone viewports fit on-screen and text size can be tuned live without destabilizing the initial attach geometry.
- **Embedded Phone Terminal Touch Scroll**: Touch drags now distinguish real scrollback, alternate-screen mouse tracking, and alternate-scroll mode instead of always faking `Up` / `Down`, so supported apps can still scroll while prompt-driven sessions stop accidentally walking history.
- **Embedded Phone Terminal Diagnostics**: Add daemon-backed viewport instrumentation plus PTY rendering guidance so focus and viewport bugs can be diagnosed from real-device traces instead of browser emulation alone.
- **Build Metadata Injection**: Share version and build-time metadata across `attn --version`, daemon health output, source/bootstrap builds, Homebrew packaging, and build/install flows so it is easier to identify the running daemon build and `install-all` leaves the newest daemon running.
- **Pane Runtime Binder Ordering**: Preserve early split-pane terminal input during initial mount and keep pane drain sequencing tied to render flush callbacks so shell startup probes and terminal automation no longer race the pane lifecycle.

## [2026-04-04]

### Added
- **Launch YOLO Preference**: The new-session location picker and repo/worktree picker now expose a keyboard-friendly `YOLO` toggle that launches sessions with each agent's approval-bypass equivalent, and the chosen value is remembered per target daemon so local and remote hosts keep independent defaults.

### Changed
- **Release Signing & Notarization**: The GitHub macOS release workflow now imports a `Developer ID Application` certificate from GitHub Actions secrets, signs release builds with the real Apple identity, notarizes both the packaged app and the rebuilt DMG, and staples the notarization tickets before publishing the Homebrew cask artifacts.
- **Tailscale Web Access**: The mobile web client now uses the machine&apos;s existing Tailscale device via `tailscale serve` instead of registering a second embedded tailnet node, and Settings now report the host device URL/login state rather than a separate attn hostname.
- **Remote Endpoint Web Access**: Connected remote daemons now surface their own Tailscale Serve status, URL, DNS name, and errors in endpoint capabilities, and the Settings modal can toggle each remote host&apos;s `tailscale_enabled` state through the remote daemon instead of proxying web access through the local machine.

### Fixed
- **Remote Sidebar Session Actions**: Remote-host sessions now show the same hover-only reload and close buttons as local sessions in the left sidebar.
- **Hidden Session Terminal Repaint**: Switching back to a previously hidden session now forces a real size bounce when the measured cols/rows are unchanged, so main panes no longer stay rendered as a narrow stale column until a later resize.

## [2026-04-03]

### Added
- **Remote Daemon Hub**: Connect to remote machines over SSH, bootstrap and manage remote `attn` daemons, and surface remote sessions alongside local ones in the dashboard with endpoint badges. Remote sessions get full PTY interactivity, git status, diff/review panels, and review-loop support routed through the local hub.
- **Remote Session Creation**: The new-session picker can target connected remote endpoints, browse remote directories, show per-endpoint recents, and create remote worktrees — all through daemon websocket commands instead of local filesystem access.
- **Packaged Remote Smoke Coverage**: End-to-end packaged-app harness that bootstraps a real SSH endpoint, exercises remote session creation, diff/review panels, review-loop flows, and picker parity against `ai-sandbox`.
- **Terminal Copy-on-Select**: Selecting text in terminals copies it automatically with whitespace cleanup. `Cmd+Shift+C` copies as markdown, detecting colored text as inline code.
- **UI Perf Harness**: Packaged-app benchmark that samples CPU, RSS, and frontend terminal/diff/review perf at repeatable checkpoints, including PTY transport mode comparisons.

### Changed
- **PTY Output Backpressure**: Pace live terminal output with write completion and websocket acknowledgements so the daemon stops flooding terminals faster than they can render.
- **Linux Release Artifacts**: Tagged releases now ship standalone `attn-linux-amd64` and `attn-linux-arm64` daemon binaries, with a release-preflight workflow for branch-safe verification.
- **Compiled Versioning**: The daemon reports an explicit compiled version via `attn --version`, and the app preflights binary protocol compatibility before restarting a mismatched daemon.
- **Cross-Compile Path**: Use `zig cc` for macOS-to-Linux cgo builds with fingerprint-based dev caching, so remote-daemon iteration produces SQLite-capable Linux binaries without a Linux box.

### Fixed
- **Terminal Scroll & Cursor**: Fix viewport scroll jump during fast TUI output, ghost cursor reappearing after resize, and scroll pin not resetting on session respawn.
- **Remote Daemon Robustness**: Wider startup readiness windows for SSH-bootstrapped daemons and packaged-app cold boot, cgo-less in-memory fallback that actually retains state, correct `~` expansion on the remote host, and Linux PTY spawn no longer fails with EPERM.

### Removed
- **Terminal Coalescing Experiment**: Remove the frontend write-coalescing config and debug plumbing after it repeatedly caused rendering artifacts in both DOM and WebGL renderers.
- **Unused Monaco Dependencies**: Remove stale Monaco editor packages after the review UI fully moved to CodeMirror.

## [2026-03-16]

### Fixed
- **`attn` in PATH for cask-only installs**: Prepend the wrapper binary's directory to PATH when spawning agent sessions so Claude Code skills can find `attn` as a bare command even when installed only via the Homebrew cask.

## [2026-03-15]

### Removed
- **Ad-hoc code signing**: Remove `codesign -s -` from `make install` and always strip the bundled sidecar signature in app builds. Quarantine removal (`xattr -d`) is still applied.

## [2026-03-14]

### Changed
- **Pane Zoom Mode**: Add `Cmd+Shift+Z` as a transient workspace zoom that expands the active pane across nested splits without hiding the surrounding panes, allows you to arm zoom before a split exists, retargets automatically when you move focus to another pane, and lights up the sidebar shortcut hint while zoom is active.
- **PR Review Provider**: Switch the advisory Hodor GitHub workflow from Vertex Gemini to OpenRouter, now targeting `qwen/qwen3-coder-next` with the `open_router_api_key` secret.
- **Sidebar Dock Shortcut Hints**: Show the split-pane and pane-navigation shortcuts (`Cmd+D`, `Cmd+Shift+D`, `Cmd+Alt+Arrow`) in the left sidebar’s Dock footer so the new session workspace controls are visible where you browse sessions.
- **Hodor Review Runtime Budget**: Install `pnpm` and the app dependencies before Hodor runs, remove verbose logging, and lower review reasoning effort to medium so PR reviews spend fewer turns on missing-tooling dead ends.

### Fixed
- **Worker Binary Override Semantics**: Keep `ATTN_PTY_WORKER_BINARY` authoritative when explicitly configured, so a missing override now fails closed instead of silently spawning some other `attn` binary from fallback search paths.
- **Worker Binary Re-Resolution Safety**: Cache implicitly discovered worker binary paths behind a dedicated lock and keep the recovery path for daemon-owned installs covered by a unit test.
- **Claude Repaint After Closing Splits**: Closing the active split pane now re-fits the surviving session terminal after the workspace collapses, so the main Claude pane redraws immediately instead of staying visually stale until a window resize or focus-mode change.

## [2026-03-10]

### Changed
- **GitHub PR Review Automation**: Replaced the `victorarias/shitty-reviewing-agent` PR review workflow with Hodor on Vertex AI using `google-vertex/gemini-3-flash-preview`, while keeping the workflow advisory and fork-safe.

### Added
- **Hodor Review Guidance**: Added a repository-specific Hodor skill and maintainer docs for the PR review workflow, including the local patch required for Google/Vertex model parsing in upstream Hodor `v0.3.4`.
- **Real App Smoke Harness**: Add a packaged-macOS automation harness that launches `/Applications/attn.app`, creates a real session through the deep-link path, splits panes with `Cmd+D`, types into the utility shell, and verifies PTY scrollback through the production daemon websocket.
- **Older-Pane Writability Repro Harness**: Add a second packaged-app automation scenario that creates two split utility panes, refocuses the older pane with a real window click, and checks whether it still accepts shell input after the newer pane exists.
- **Dev-Only UI Automation Bridge**: Add a build-gated localhost automation bridge for the Tauri app so packaged-app tests can create sessions, split/focus panes, write to runtimes, and inspect workspace state without taking over the user’s keyboard and mouse.

### Changed
- **Bridge Repro Stability**: The packaged-app UI automation bridge now supports fresh app relaunches, server-side request logging, frontend responsiveness checks, pre-launch daemon cleanup of stale full-flow sessions, and diff-based shell-pane selection so the main-pane return repro fails much more consistently in the real bug region instead of drifting during bootstrap.

### Fixed
- **Main Pane Return After Split**: Returning from a split shell to the main Claude pane no longer forces a fresh PTY reattach on every remount, so typing in the main pane continues to render after the split instead of going visually dead until another reconnect.
- **Queued Pane Output on Remount**: Split panes now replay PTY data that arrived while their terminal view was temporarily unmounted, so output generated during layout switches or session hops is not silently dropped before the pane reattaches.
- **`Ctrl+W` Terminal Editing**: Terminal panes no longer treat `Ctrl+W` like the macOS close-panel shortcut, so shells and line editors can use it to delete the previous word as expected.

## [2026-03-09]

### Added
- **Persistent Split Workspaces**: Persist each session’s split-pane workspace in the daemon/store so nested layouts, active panes, pane titles, and shell-pane runtime mappings survive app relaunches and daemon recovery.

### Changed
- **Daemon-Owned Workspace Control Plane**: Move split-pane creation, close, focus, and rename authority into the daemon with dedicated workspace protocol messages and snapshot/update events, while the frontend now renders daemon snapshots instead of mutating the canonical split tree locally.
- **Attached Shell Runtime Recovery**: Reconcile recovered shell-pane PTY runtimes against persisted workspace metadata at startup, prune missing panes from saved layouts, and clean up orphaned shell runtimes that no longer belong to any workspace.
- **Spatial Pane Navigation**: `Cmd+Alt+Arrow` now moves focus by panel geometry instead of creation order, with `Up/Down` respecting stacked panes and `Left/Right` respecting side-by-side panes; when there is no pane in that direction, focus falls through to the previous or next session.
- **Unified Pane Runtime Binding**: Main session panes and split shell panes now share the same workspace-level runtime binding, so remount, restore, resize, and keyboard wiring follow one terminal lifecycle path instead of separate store and UI implementations.
- **Centralized PTY Event Routing**: The session workspace UI now routes PTY output through one app-level runtime registry instead of letting every mounted workspace subscribe to the PTY event bus independently, which makes pane/runtime delivery a single explicit ownership graph.
- **Explicit Workspace vs View-State Model**: Frontend sessions now store daemon-owned workspace topology separately from the daemon’s last-active-pane hint, while live pane selection stays client-local in `App`, which makes the UI ownership boundary clearer for future remote or multi-client work.
- **Dedicated Workspace View Controller**: The client-side pane-selection and topology-reconciliation logic now lives in a dedicated frontend hook instead of being embedded inline in `App`, which gives the workspace UI a cleaner controller boundary and direct unit coverage.
- **Dedicated Workspace Debug Harness**: The workspace-specific test helpers and pane debug globals now live in their own hook instead of inside `App`, which keeps the top-level app component focused on orchestration rather than dev-only terminal diagnostics.
- **Composed Workspace Controller**: The frontend workspace layer now exposes one composed controller hook for pane selection, workspace handle registration, PTY event routing, fit/text/size access, and debug wiring, so `App` consumes the workspace system instead of owning its internals.

### Fixed
- **Main Session Focus After Split Return**: Clicking back into the main Claude/Codex pane after working in a split now reclaims keyboard focus immediately, without needing a session refresh or waiting on the daemon workspace round-trip.
- **Workspace Quick-Find and Reload Pane Context**: Session-level quick find and reload sizing now read from the active workspace pane/runtime instead of a store-owned main-terminal ref, keeping those actions aligned with split-pane focus.
- **Source Install Daemon Startup Path**: `make install` and `make install-app` now point both the local CLI and the packaged app sidecar at a stable installed daemon binary instead of a transient Tauri release artifact path that could disappear after rebuilds and leave the daemon unable to start.

## [2026-03-08]

### Changed
- **Split Session Workspace**: Let attached session terminals open directly inside the main session area as split panes beside the primary session terminal, with `Cmd+D` and `Cmd+Shift+D` creating new vertical or horizontal splits in the active session.
- **Shortcut Remap for Split Workflow**: Move dashboard navigation to `Cmd+Option+D` and the diff dock to `Cmd+Shift+G` so the split-terminal shortcuts can own the `D` key family in session view.
- **Right Dock Default Layout**: Start the session view with the compact layout by leaving the diff dock closed until you explicitly open it with `Cmd+Shift+G`.
- **Review Loop Summary Space**: Let the latest-summary card in the review-loop sidebar grow substantially taller before it starts scrolling, so longer round summaries are easier to read in place.

### Fixed
- **`install-all` Daemon Startup Race**: Stop restarting the local `~/.local/bin/attn daemon` as part of `make install-all`, so opening the packaged app no longer races between a transient local daemon and the bundled app daemon during worker-backend startup.
- **App Bundle Install Corruption**: Stop replacing `/Applications/attn.app` with a plain `cp -r` while the packaged app may still be running; `install-app` now shuts down the existing app/daemon first and uses `ditto` so the bundled `attn` sidecar is preserved in the installed app.
- **Source Install Binary Stability**: Source-based installs now symlink both `~/.local/bin/attn` and the packaged app’s `Contents/MacOS/attn` sidecar to the stable Tauri release binary, avoiding the disappearing or invalid copied binaries seen in direct install locations on this machine.
- **Worker PTY Startup Probe Flakiness**: Give worker-sidecar startup more time during daemon boot and add clearer worker startup logs so slow post-install launches are less likely to fall back to embedded mode and mark live sessions only as recoverable.
- **Diff Detail Panel Exit Animation**: Keep dock panels in the right-dock stack through their close transition, so the detailed diff/review panel now slides out smoothly on `Cmd+Shift+E` and `Esc` instead of disappearing abruptly.

## [2026-03-07]

### Changed
- **Session Sidebar Action Strip**: Move review-loop access into a new icon-based sidebar header tool row alongside editor, diff-panel, and PR-drawer controls, and let the diff panel be shown or hidden from that strip.
- **Review Loop Session Overlay**: Remove the full-width review-loop bar above the active session and keep the review-loop drawer as an app-controlled overlay with its primary actions in the drawer header.
- **Shared Sliding Side Panels**: Refactor the review-loop drawer and PR attention drawer onto one shared side-panel shell so they both anchor to the right edge, animate with the same slide-in/slide-out behavior, and stack beside the diff panel instead of overlapping it.
- **Unified Session Right Dock**: Replace the old mix of fixed layout panels, one-off drawers, and separate review view with a single dock-managed panel system for diff, review loop, PR attention, and the in-app review/editor panel.
- **Diff Detail Cleanup**: Rename the old review-oriented diff panel to a diff-detail surface, remove the legacy in-panel AI review workflow, and keep the main review loop as the only review automation path in the app UI.
- **Live Review Loop Detail Updates**: The review-loop panel now widens further, renders the latest summary as markdown, auto-opens the log while running, and updates the visible log and touched-file list incrementally during a live iteration instead of only after completion.

## [2026-03-06]

### Added
- **SDK Review Loop Data Model**: Add dedicated review-loop run, iteration, and interaction types plus SQLite tables so the SDK-based loop can persist append-only history without reusing the old session-scoped PTY loop table.
- **SDK Review Loop Execution Path**: Add daemon-side SDK review-loop orchestration with autonomous iteration, structured outcomes, `awaiting_user` pauses, and same-loop answer/resume handling.
- **Review Loop Handoff CLI Path**: Add `attn review-loop answer` plus `--handoff-file` and `ATTN_SESSION_ID` inference support for `attn review-loop start`, so the main agent can trigger loops without the old `advance` callback model.
- **Deterministic Review Loop Harness**: Add a scripted daemon-level review-loop harness with timeline capture so SDK loop scenarios can be exercised without real Claude or PTY automation.

### Changed
- **Review Loop Persistence Foundation**: Add store APIs and tests for run-oriented review-loop records, active-run lookup by source session, and same-loop question/answer interaction history to support the SDK pivot.
- **Review Loop App Contract**: Replace the PTY-era review-loop UI/socket status flow with run-oriented `running`, `awaiting_user`, `stopped`, `completed`, and `error` states, and add an in-app answer flow for blocked loops.
- **Claude attn Skill**: Update the installed Claude skill to use review-loop start and answer commands instead of the removed PTY-era `advance` instruction.

## [2026-03-05]

### Added
- **Session Review Loop**: Add daemon/store/CLI support for session-level review loops, including persisted loop state, iteration limits, explicit `attn review-loop advance` handoff, and best-effort stop via PTY `ESC`.
- **Session Review Loop Controls**: Add active-session UI for starting, stopping, inspecting, and editing review-loop iteration limits without using `ReviewPanel`.
- **Saved Review Loop Prompts**: Add prompt preset persistence in settings so custom review-loop prompts can be saved and reused from the active-session loop UI.
- **Review Loop Session Indicators**: Add loop-status badges in the session sidebar and dashboard so active/completed/stopped loops are visible without selecting the session.
- **Review Loop Planning Docs**: Add an implementation plan in `docs/plans/2026-03-05-review-loop.md` and track the work in `TASKS.md`.

### Changed
- **Protocol Update**: Extend the protocol and generated types for review-loop commands, loop update/result events, PTY input source tagging, and persisted loop state, and bump protocol version to `34`.
- **Manual User Takeover Handling**: Manual user prompt submission now stops active review loops instead of letting automation schedule another pass behind the user's back.

## [2026-02-26]

### Added
- **Agent Transcript Watcher Behavior Interface**: Add `TranscriptWatcherBehaviorProvider` in the agent driver layer so each agent can define its own transcript lifecycle parsing, activity policy, dedupe behavior, and classification guard logic.
- **Agent Daemon Policy Interfaces**: Add driver-level policy hooks for startup recovery behavior, PTY state filtering, resume-ID lifecycle, transcript classification extraction, and executable-aware classifier dispatch.

### Changed
- **Watcher Loop Separation**: Daemon transcript watcher now runs a generic loop and delegates all agent-specific decisions to driver-provided watcher behaviors, removing hardcoded Claude/Codex/Copilot branches from daemon watcher code.
- **Built-in Agent Watcher Policies**: Move Claude hook-freshness classification guard, Codex lifecycle/working heuristics, and Copilot pending-approval turn policy into `internal/agent` behavior implementations.
- **Daemon Agent Branch Removal**: Move remaining daemon agent conditionals (recoverability, PTY-state acceptance, stop-hook resume extraction, and stop-time transcript/classifier strategy) behind agent policies and helpers so daemon logic remains agent-agnostic.

## [2026-02-24]

### Fixed
- **False Idle Classification During Claude Tool Execution**: Claude sessions running long tools (e.g., nolo production queries) were falsely classified as idle because the PTY working detector only recognized 4 specific glyphs. Expanded to the full Dingbats decorative star/asterisk range (U+2722–U+274B).
- **Transcript Watcher Guard for Active Claude Sessions**: The transcript watcher no longer triggers classification when hooks confirm a Claude session is actively working or pending approval, preventing the classifier from overriding authoritative hook state during tool execution.
- **Timestamp Precision Races**: State timestamps now use RFC3339Nano (nanosecond precision) instead of RFC3339, preventing same-second races where stale classifier results could overwrite fresher hook-driven state updates.

## [2026-02-22]

### Added
- **Agent Driver Abstraction**: Add a new `internal/agent` driver layer (registry + per-agent driver files) with opt-in capabilities for hooks, transcript discovery/watching, classifier, state detector, resume, and fork support.
- **Capability Env Overrides**: Add per-agent capability toggles via environment variables (for example `ATTN_AGENT_CLAUDE_TRANSCRIPT=0`) so features can be turned on/off without code changes.
- **Generic Launch Preparation Hook**: Add optional driver pre-launch setup (`LaunchPreparer`) so agent-specific prep (like Claude resume transcript copy) is encapsulated in the agent driver.
- **Generic Settings Writer**: Add `wrapper.WriteSettingsConfig()` for writing driver-provided settings/hook files (not Claude-specific anymore).
- **Minimal Pi Driver**: Add an initial `pi` driver with transcript/hook/classifier/state-detector capabilities disabled by default, so Pi can be integrated incrementally.
- **Dynamic Agent Settings Surface**: Settings now carry per-agent availability and executable keys for attn-owned drivers.
- **Copilot Resume Transcript Discovery API**: Add `transcript.FindCopilotTranscriptForResume()` to expose resume-ID transcript lookup as shared transcript package functionality.

### Changed
- **Protocol Update**: Expand protocol payloads for generic executable + `pi` compatibility fields, and align app/daemon handshake on protocol version `32`.
- **Unified In-App Agent Launcher**: Replace per-agent direct launch duplication in `cmd/attn/main.go` with a shared `runAgentDirectly()` path that uses driver capabilities.
- **Agent Selection UI is No Longer Hardcoded to 3 Agents**: New-session picker and settings modal now render agent choices dynamically from availability/settings keys rather than fixed Codex/Claude/Copilot button sets.
- **Dynamic Executable Wiring from UI to Spawn**: Frontend now sends agent-specific executable overrides through a generic spawn field, enabling non-hardcoded agents (including Pi) without bespoke frontend plumbing.
- **Transcript Watch Eligibility**: Daemon transcript watcher now checks driver capabilities instead of hard-coded agent-name allowlists.
- **Transcript Discovery + Bootstrap**: Transcript watcher now resolves transcript path and bootstrap tail size strictly through agent drivers.
- **Stop-Time Classification Gate**: When transcript capability is disabled for an agent, stop-time classification now skips transcript parsing and marks the session idle instead of forcing transcript-dependent logic.
- **PTY Spawn Executable Plumbing**: Add generic `Executable` plumbing through daemon -> PTY backend -> worker runtime so selected CLI paths are passed per agent, while keeping existing agent-specific executable fields for compatibility.
- **Agent Resolution for Spawn/Register**: Daemon now preserves/accepts registered agent-driver names instead of always coercing unknown values to built-in agents.
- **Crash-Recovery Session Handling**: After daemon restart recovery, stale sessions without a live PTY are now handled by agent capability: Claude sessions are marked recoverable and can be reopened, while non-recoverable sessions are automatically reaped.
- **New-Session Resume UI Simplification**: Remove the Location Picker resume toggle and shortcut so new sessions always start with a fresh attn-managed session ID; resume behavior remains dedicated to recoverable crash-restart flows.
- **Session Reload Control**: Sidebar session rows now show a small reload button on hover (stacked below close) to restart the underlying PTY for the same session ID.

### Fixed
- **Protocol Handshake Version Drift**: Frontend WebSocket protocol constant now matches daemon protocol `32`, preventing immediate disconnects after upgrading.
- **Classifier Capability Enforcement**: Stop-time classification now honors per-agent `classifier` capability toggles and skips LLM classification when disabled.
- **Dead Agent Driver Abstractions**: Remove unused transcript-handler and state-detector provider interfaces from the driver layer to reduce indirection and avoid stale integration paths.
- **Agent Isolation Cleanup**: Remove legacy transcript helper wrappers in `cmd/attn/main.go` and remove daemon-side hardcoded transcript discovery/bootstrap fallbacks so transcript behavior now comes from drivers.
- **Executable Override Injection**: PTY spawn now avoids forcing default `ATTN_*_EXECUTABLE` env vars, preserving login-shell/env-based executable selection unless an explicit override is set.
- **Todo Priority over Transcript Capability**: Stop-time state classification now evaluates pending todos before transcript-capability short-circuits, so unfinished todo lists still surface as `waiting_input`.
- **Location Picker Shortcuts**: Agent keyboard shortcuts now index only available agents.
- **Claude Session Reopen After Crash**: Opening a recoverable Claude session now re-spawns it with the same session ID, allowing Claude to resume conversation history instead of failing with a missing-PTY error.
- **Claude Recoverable Resume Path**: Recoverable Claude sessions now respawn with `--resume <session-id>` (instead of a plain same-ID spawn), matching the first-run/resume contract and reducing same-ID startup conflicts.
- **Resume-Picker Recovery ID Drift**: Hook events now sync Claude’s actual `session_id` back to the daemon (`set_session_resume_id`), persist it in session state, and reuse it during recoverable spawns so restart recovery resumes the real Claude conversation even when attn ID and Claude ID differ.
- **Claude Reopen Guardrail**: Reopening known Claude sessions now attempts resume recovery even when a stale `recoverable=false` flag slips through after daemon churn, and spawn-time ID mapping now still prefers stored `resume_session_id`.
- **Recoverable Flag Consistency**: Recoverable markers are now cleared once a live worker session is confirmed, preventing stale recovery badges.
- **Worker Probe Early-Exit Detection**: Worker spawn now detects when the sidecar process exits before becoming ready and returns an explicit early-exit error instead of waiting for a socket timeout, making PTY backend probe failures faster and easier to diagnose.
- **Reload Kill/Spawn Race**: Session reload now waits for `session_exited` before resolving kill, preventing first-click reload from attaching to a stale PTY and immediately disconnecting.
- **Sidebar Session Actions Alignment**: Reload/close action stack now stays right-aligned in session rows.
- **Location Picker Agent Shortcut Stability**: Agent ordering and keybindings are now fixed in the picker (`Claude=⌥1`, `Codex=⌥2`, `Copilot=⌥3`) instead of reordering on selection changes.
- **Repo Options Keyboard Selection Freshness**: Repo-options keyboard handlers now use up-to-date selection callbacks, so agent changes made while choosing branches/worktrees are reflected in the final session launch.

## [2026-02-21]

### Changed
- **Long-Run Session Review Gate**: Sessions that run for 5+ minutes now finish in a review-required yellow state (`needs_review_after_long_run`) instead of immediately classifying to idle. Classification resumes only after the user visualizes that session (5s stable selection, or immediate when already focused at completion).

### Fixed
- **Review Diff Light Theme Support**: Unified diff editor now follows the app’s resolved dark/light theme, including syntax highlighting, gutters, added/deleted line backgrounds, inline comment widgets, and selection popup styling in light mode.
- **Contributors**: Thanks to @dakl for the PR that delivered theme toggle and light mode improvements.

## [2026-02-19]

### Fixed
- **Claude Working-State PTY Heartbeats**: Claude sessions now emit `working` pulses from the live animated status line (`✻ ... (Xm Ys · ...)`) so green/running state stays accurate during long turns.
- **Claude Final Summary Guard**: PTY state detection now excludes terminal final summary lines (`✻ <verb> for ...`) from working animation matching, avoiding false “still running” signals at turn completion.

## [2026-02-18]

### Changed
- **Unknown State Diagnostics**: Stop-time classification now logs explicit unknown reason codes (for example `transcript_parse_error`, `classifier_error`, and `classifier_unknown_response`) so purple-state transitions can be traced from runtime evidence.
- **Classifier SDK Dependency**: Upgrade `claude-agent-sdk-go` to include first-class `rate_limit_event` parsing and avoid aborting classifier queries on that stream event.
- **Restart Recovery Default State**: Worker session reconciliation after daemon restart now defaults live-running sessions to `launching` (emoji) instead of `working`, unless runtime metadata explicitly indicates `pending_approval` or `waiting_input`.

### Fixed
- **Classifier Flow Cleanup**: Remove daemon-side retry logic that depended on brittle `rate_limit_event` error-string matching, now that SDK parsing handles the event directly.

## [2026-02-17]

### Fixed
- **Terminal Emoji Width**: Emoji and CJK characters are correctly treated as double-width, fixing misaligned columns in status bars and context displays.
- **Login Shell Environment Capture**: PTY sessions now source `.zshrc` when capturing the login shell environment, fixing missing PATH entries (e.g. Google Cloud SDK) that are configured in `.zshrc` rather than `.zprofile`.
- **Session List Ordering Stability**: Daemon session listing now sorts by `label` with `id` as a deterministic tie-breaker, preventing same-label sessions from swapping order between refreshes.

## [2026-02-16]

### Fixed
- **Codex Mid-Turn Idle Regression**: Codex transcript watching now uses turn/tool lifecycle events (`task_started`, `task_complete`, `turn_aborted`, tool call start/complete) to keep active turns in `working` and defer stop-time classification until turn-close quiet windows.
- **Codex No-Output Turn Handling**: Turns that end without assistant output now resolve to `waiting_input` instead of lingering in a stale running state.
- **Codex Watcher Bootstrap Gap**: Codex transcript watchers now bootstrap from a recent transcript tail instead of attaching strictly at EOF, so restored/reopened sessions can still classify to `idle`/`waiting_input` when no new assistant lines arrive.
- **Codex Bootstrap No-Output Guard**: The no-assistant-output `waiting_input` heuristic now requires an observed `turn_start` in the current watcher window, preventing bootstrap-tail truncation from falsely marking long in-progress turns as waiting.
- **Codex Working Animation Liveness**: PTY detector now treats ANSI carriage-return animation frames as `working` heartbeat pulses, and worker backend forwards throttled repeated `working` pulses so active Codex runs can recover quickly from accidental `idle` demotions.
- **Codex Pulse False Positives**: Working pulses now require explicit working-status keywords (`working`, `thinking`, `running`, `executing`) in animated redraw frames, reducing prompt-redraw misclassification as active work.
- **Codex Stop-Time Backend Selection**: Codex sessions now use the Codex CLI classifier path (instead of Claude SDK), matching the agent/runtime used by the session itself.
- **Codex Executable Consistency**: Codex classification now uses the same configured `codex_executable` setting as session launch (with `ATTN_CODEX_EXECUTABLE` env override still taking precedence), avoiding classifier failures when `codex` is not on `PATH`.
- **Codex JSON Mode Parsing**: Codex classifier now treats `--output-last-message` as the primary verdict source and falls back to JSONL `item.completed` parsing, so stderr rollout noise no longer pollutes verdict extraction.
- **Codex JSONL Large-Line Parsing**: Codex classifier JSONL parsing now handles lines beyond scanner token limits, preventing missed verdict/error extraction on large event payloads.
- **Codex Model Fallback**: Codex classifier now attempts configured models in order (default: `gpt-5.3-codex-spark` then `gpt-5.3-codex`) with low reasoning effort, and falls through automatically when the first model is unavailable.
- **Temporary PTY Capture for Work→Stop Debugging**: Worker runtime now records a rolling 90-second PTY stream window (output + input + state transitions) for Codex sessions and dumps JSONL captures automatically on `working -> waiting_input|idle`, plus on exit/shutdown, under `<data_root>/workers/<daemon_instance_id>/captures/`.

## [2026-02-15]

### Fixed
- **Claude Transcript Parsing**: Removed `bufio.Scanner` token-size limitations when reading JSONL transcripts, preventing stop-time classification from erroring and sessions from flashing/sticking `unknown` due to very long lines.
- **Worker PTY Restart Survival**: Worker backend recovery now accepts legacy socket-path filenames and restores prior `socket_path_mismatch` quarantine entries when they match supported formats, improving daemon restart resilience.
- **Worker PTY Recovery Safety**: Socket-path mismatch quarantine no longer unlinks the registry-reported worker socket path, preventing accidental orphaning of live worker sessions.
- **Worker Lifecycle Monitor CPU Spike**: Lifecycle watch no longer relies on socket read deadlines; it blocks on the watch stream and stops by closing the connection, preventing immediate-timeout loops that could peg CPU.
- **Source App Daemon Selection**: Source-built apps now prefer `~/.local/bin/attn daemon` (when it is at least as new as the bundled daemon) so `make install` daemon changes take effect without rebuilding the app.
- **macOS Shortcut Regression**: `Ctrl+W` no longer closes sessions; closing is now `Cmd+W` only (so terminals keep standard delete-word behavior).

## [2026-02-11]

### Changed
- **Source Build Update Checks**: Source-installed app builds now set `source` install channel and skip GitHub release update polling/banner noise, while tagged release builds keep update notifications.
- **Session Startup State**: New sessions now start in `launching` (emoji indicator) instead of immediately showing `working` green, then transition once runtime signals arrive.
- **Classifier Turn Budget**: Claude SDK classifier now runs with `maxTurns=2` for more reliable structured verdict extraction.
- **PTY Backend Visibility in Settings**: Settings now displays the active PTY runtime mode (`External worker sidecar` vs `Embedded in daemon`) so restart-survival behavior is visible in the UI.

### Fixed
- **Release Banner Dismissal**: Added an explicit dismiss control (`×`) for the GitHub release banner and persist dismissal per release version, so a dismissed banner stays hidden until a newer release is published.
- **Worktree Close Cleanup Prompt**: Restored the delete/keep prompt when closing worktree sessions even if an old persisted "always keep" preference exists; "always keep" now applies only for the current app run.
- **Copilot Permission Prompt State**: Copilot numbered command-approval dialogs (for example, "Do you want to run this command?" with `1/2/3` choices) are now recognized as `pending_approval` instead of falling through to stale idle/gray state.
- **Copilot Transcript Pending Latch**: Transcript watcher now tracks unresolved Copilot tool calls and keeps sessions in `pending_approval` while a stalled approval-gated tool call is outstanding, then clears back to `working` on completion.
- **Copilot Mid-Turn Idle Regression**: Transcript watcher now treats `assistant.turn_start`/`assistant.turn_end` as authoritative turn boundaries and suppresses stop-time classification while a turn is open, preventing active Copilot sessions from flashing/sticking gray during ongoing tool work.
- **Copilot Pending Approval Stability**: While Copilot is in `pending_approval`, noisy PTY redraws that heuristically look like `working` no longer override state; pending now clears only when transcript evidence indicates the approval gate has resolved.
- **Copilot Long-Running Tool Stability**: Transcript-based pending promotion now only elevates non-working states (`idle`, `waiting_input`, `launching`, `unknown`), preventing long-running approved tools from being mislabeled as pending approval.
- **Claude Classifier Diagnostics**: When Claude classifier output cannot be parsed into a verdict, daemon logs now include a structured dump of returned SDK messages (or explicit empty-response marker) to diagnose false `waiting_input` fallbacks.
- **Claude Structured Result Parsing Compatibility**: Bumped `claude-agent-sdk-go` to include merged parser fixes on `main` so classifier flows can reliably consume structured/result payload fields from SDK `result` messages.
- **Unknown Classification Handling**: Added explicit `unknown` session state (purple) for transcript/classifier uncertainty or errors; removed implicit fallback to `waiting_input`.
- **Claude Stop-Time Transcript Race**: Claude classification now ignores stale assistant messages that occur before the latest user turn, preventing off-by-one misclassification when the newest assistant response has not flushed to transcript yet.
- **Claude Stop-Time Freshness Guard**: Claude classification now also enforces assistant-message recency relative to the current stop event, so a previous turn cannot be reused when the latest turn has not fully flushed to transcript.
- **Claude Turn-Scoped Classification Idempotency**: Stop-time classification now tracks and de-duplicates by Claude assistant turn UUID, preventing repeated LLM classification on the same assistant message when hooks fire faster than transcript flush.
- **Claude Concurrent Classification De-Duplication**: Added an in-flight Claude turn guard so stop-hook and transcript-watcher triggers cannot classify the same assistant turn concurrently, eliminating duplicate classifier calls for a single turn.
- **Claude Transcript Watcher**: Claude sessions now use transcript-tail quiet-window monitoring (like Codex/Copilot) as a second classification trigger, so delayed transcript flushes still converge to the correct post-turn state even if stop hooks arrive early.
- **Local Install Daemon Restart**: `make install` now always `pkill`s existing daemon processes, restarts `~/.local/bin/attn daemon`, and fails fast if the local daemon process is not detected.
- **E2E Port Cleanup on macOS**: `make test-e2e` now prefers `lsof` on Darwin when clearing stale Vite port `1421`, avoiding noisy `fuser` usage output from incompatible flags.
- **Ownership-Mismatch Worker Reclaim Safety**: Worker registry entries now include daemon owner-lease metadata (`owner_pid`, `owner_started_at`, `owner_nonce`), and recovery now reclaims ownership-mismatched workers only when the recorded owner is provably stale via authenticated worker RPC removal. Conservative quarantine-only behavior remains when ownership cannot be proven stale.

## [2026-02-10]

### Fixed
- **Terminal Cmd+Click Link Open**: Terminal hyperlinks now open directly via Tauri opener for both plain URLs and OSC 8 links, removing an extra warning prompt and fixing links that previously failed to open after confirmation.

### Added
- **Worker PTY Sidecar Runtime (Feature Rollout)**: Add restart-survivable PTY execution by moving session runtime into per-session worker sidecars, with daemon recovery/reconnect flow and embedded-backend fallback for compatibility.
- **PTY Backend Abstraction (Phase A)**: Introduce `internal/ptybackend` with an embedded adapter so daemon PTY flows route through a backend interface instead of directly through the in-process PTY manager.
- **Persistent Daemon Instance Identity**: Daemon now creates and reuses `<data_root>/daemon-id` and includes `daemon_instance_id` in `initial_state`.
- **Recovery Barrier Scaffold**: Daemon now tracks a startup recovery barrier, defers `initial_state` until recovery completes, and returns `command_error` (`daemon_recovering`) for PTY commands during the barrier window.
- **Per-Session PTY Worker Runtime (Phase B)**: Add `attn pty-worker` and `internal/ptyworker` with JSONL RPC (`hello`, `info`, `attach`, `detach`, `input`, `resize`, `signal`, `remove`, `health`) plus atomic worker registry files.
- **Worker Backend Adapter (Phase C)**: Add daemon-side worker backend implementation (`internal/ptybackend/worker.go`) with worker spawn/attach/input/resize/kill/remove routing and registry-based recovery scan.
- **Worker Cleanup TTL Coverage**: Add worker runtime tests to verify exited-session cleanup timing when daemon attachments are absent.
- **Worker Restart-Recovery Integration Coverage**: Add an opt-in integration test that simulates backend restart and verifies recovered worker sessions remain attachable and interactive.

### Changed
- **Protocol Version**: Bump daemon/app protocol version to `28`.
- **Daemon PTY Routing**: PTY command handling (`spawn`, `attach`, `input`, `resize`, `kill`) and startup PTY session reconciliation now route through the backend seam.
- **Backend Selection Defaults (Phase E)**: Worker backend is now the default startup mode; `ATTN_PTY_BACKEND=embedded` remains available as fallback/override.
- **Worker Recovery Reconciliation**: On worker backend startup, daemon now reconciles recovered runtime sessions into store state (create missing live sessions, preserve waiting/approval states, mark missing-running sessions idle).
- **Classifier SDK Runtime**: Upgrade Claude Agent SDK dependency to `v1.0.0-beta`.

### Fixed
- **Worker Backend Selection Ordering**: Daemon instance ID is now initialized before worker backend selection, so worker backend activation is deterministic.
- **Worker Poller Exit Deadlock Risk**: Poller exit callbacks are now asynchronous, preventing re-entrant `Remove()`/`stopPoller()` deadlocks.
- **Attach Stream Deadline Handling**: Worker attach handshake now clears per-RPC connection deadlines before long-lived stream forwarding to avoid premature idle disconnects.
- **PTY Stream Cleanup on Backpressure**: PTY forwarder now closes streams when client outbound buffers overflow, preventing orphaned worker attachments.
- **Worker Stream Backpressure Deadlock**: Worker stream event publishing now handles overflow without blocking indefinitely.
- **Worker RPC Hang Risk**: Worker backend RPC calls now run with context/time bounds to avoid indefinite blocking on stalled sockets.
- **Worker Recovery Ownership Handling**: Ownership-mismatched worker registry entries are quarantined instead of left in the active registry path.
- **Worker Recovery Transient Handling**: Recovery now retries transient worker RPC failures before deferring them and surfaces partial-recovery warnings.
- **Recovery Startup Bound**: Daemon recovery scan now runs with a bounded startup timeout to avoid unbounded barrier delays.
- **Worker Session ID Path Safety**: Session IDs are validated before worker registry/socket path derivation to avoid unsafe path traversal patterns.
- **Reconnect Reattach Race**: Frontend PTY reattach now waits for `initial_state`, avoiding `attach_session` failures during recovery barrier windows.
- **Daemon Identity Reset Hygiene**: Frontend clears PTY runtime caches when `daemon_instance_id` changes to avoid stale stream replay after endpoint identity changes.
- **Daemon Identity Reattach Continuity**: Frontend now preserves the attached-session set across daemon instance changes so terminal streams reattach automatically after recovery.
- **Worker Runtime Observability**: Worker stdout/stderr is now captured to per-session logs under `<data_root>/workers/<daemon_instance_id>/log/`.
- **Worker Session Reattach Idempotency**: Re-attaching an already attached session now closes the previous stream first, preventing duplicate PTY subscriptions and repeated output delivery.
- **Reattach Failure Safety**: PTY re-attach now keeps the existing stream if replacement attach fails, avoiding transient detach/data-loss windows.
- **Recovered Session State Accuracy**: Worker reconciliation now treats recovered sessions with non-running child processes as `idle` instead of incorrectly forcing `working`.
- **Embedded Stream Close Safety**: Embedded PTY stream close/publish path is now synchronized to prevent close/send races during detach and shutdown.
- **Worker Recovery Stabilization**: Startup now performs bounded recovery retries before demoting sessions, reducing false `idle` transitions during transient worker unavailability.
- **Worker Stream Close Boundedness**: Worker stream detach now uses a short write deadline so close/shutdown paths do not hang when the peer socket is stalled.
- **Spawn Failure Worker Cleanup**: Worker backend now terminates and reaps unready worker sidecars when spawn readiness fails/timeouts, preventing orphaned worker processes.
- **Registry Socket Path Validation**: Recovery and lazy session lookup now reject/quarantine registry entries with unexpected socket paths and avoid deleting arbitrary filesystem paths from untrusted metadata.
- **Deferred Recovery Convergence**: Daemon now runs deferred recovery reconciliation after partial startup recovery so stale sessions eventually converge to accurate idle/running state.
- **Forced-Demotion Safety Check**: Session demotion now probes worker liveness signals (registry + PID + managed socket path) to avoid incorrectly idling sessions during prolonged control-plane outages.
- **Liveness Uncertainty Handling**: Ambiguous liveness probe failures now defer idle demotion instead of treating unknown as dead, reducing false idle transitions during transient worker/socket failures.
- **Recovery Demotion Cutoff**: Startup reconciliation now skips idle demotion for sessions updated after recovery began, reducing startup state flapping for freshly active sessions.
- **Recovery Deferred Reconcile Triggering**: Missing worker metadata now triggers deferred reconciliation retries, improving eventual convergence for transient info-read failures.
- **Recovery Clear Sessions Semantics**: `clear_sessions` is now blocked during startup recovery barrier to prevent worker-recovered sessions from immediately reappearing after a clear.
- **Startup Recovery Flow Decomposition**: Daemon startup now delegates PTY recovery/reconciliation into focused helpers, reducing coupling in `Start()` while preserving behavior.
- **Terminal Cmd+Click Link Open**: Terminal hyperlinks now open directly via Tauri opener for both plain URLs and OSC 8 links, removing an extra warning prompt and fixing links that previously failed to open after confirmation.
- **Classifier WAITING/DONE Parsing**: Stop-time state classification now handles multiline/model-explanatory outputs correctly (including responses that start with `WAITING` and then add rationale), preventing false `idle` states when user input is still required.
- **Classifier Structured Output Handling**: Claude classifier requests a JSON-schema verdict (`WAITING`/`DONE`) and consumes structured/result payloads when available, with robust fallback parsing for plain-text outputs.

## [2026-02-09]

### Fixed
- **Dashboard Session Visibility**: Home dashboard now renders `pending_approval` sessions in a dedicated "Pending approval" group, so active sessions waiting on tool/permission approval are no longer hidden.

## [2026-02-08]

### Added
- **Copilot Session Agent**: Add first-class `copilot` session support across protocol, daemon PTY spawn, wrapper launch flow, and session picker/default-agent settings.
- **Copilot Executable Override**: Add `copilot_executable` setting and plumb it through frontend spawn requests, daemon validation, and PTY environment (`ATTN_COPILOT_EXECUTABLE`).
- **Copilot Transcript Parsing**: Add support for parsing Copilot `events.jsonl` (`assistant.message`) in transcript extraction.
- **No-UI Real-Agent Harness Test**: Add opt-in integration harness test that spawns and attaches real agent sessions over daemon WebSocket, streams PTY output, and prints live `session_state_changed` transitions without opening the app UI.
- **Homebrew Formula**: Added `Formula/attn.rb` so `attn` can be installed via Homebrew tap.
- **Homebrew Cask**: Added `Casks/attn.rb` so `attn.app` can be installed via `brew install --cask`.
- **Release Workflow**: Added `.github/workflows/release.yml` to build Apple Silicon macOS release artifacts on tags.
- **Release Script**: Added `scripts/release.sh` and `make release VERSION_TAG=vX.Y.Z` to automate version bump, commit/tag/push, and Homebrew formula refresh.
- **GitHub Release Checker**: App now periodically checks GitHub latest release and surfaces a non-automatic update notice in the UI.
- **Release Docs**: Added `docs/RELEASE.md` with the maintainer release runbook.
- **Agent Availability Detection**: Daemon now checks `claude`/`codex`/`copilot` availability in `PATH` (respecting executable overrides) and publishes `claude_available`, `codex_available`, and `copilot_available` in settings events.

### Changed
- **Classifier Backend**: Add Copilot CLI classifier support (`copilot -p ... --model claude-haiku-4.5`) while keeping Claude SDK classification for Claude/Codex sessions.
- **Classifier Backend Selection**: Classifier backend is now selected by session agent:
  - Claude/Codex sessions classify with Claude SDK (Haiku)
  - Copilot sessions classify with Copilot CLI (Haiku model)
- **PTY Live State Detection**: Extend PTY output state heuristics to Copilot sessions (in addition to Codex) for color/state updates during active runs.
- **Codex/Copilot Turn Completion Source**: Daemon-managed Codex/Copilot sessions now use transcript-tail quiet-window detection (instead of PTY prompt heuristics) to trigger stop-time classification during active sessions.
- **Protocol Version**: Bump daemon/app protocol version to `27`.
- **App Update UX**: Replaced one-click in-app auto-update install with a **View Release** banner that links to GitHub releases.
- **Release Artifacts**: Release workflow now uploads a stable `attn_aarch64.dmg` alias so Homebrew cask can target a fixed latest-download path.
- **Docs IA**: README now includes full install, update, and build-from-source guidance directly (self-sufficient setup instructions), while release procedure lives in `docs/RELEASE.md`.
- **Agent Picker UX**: Location picker and settings default-agent controls now disable unavailable agents and show PATH availability status; PR-open fallback now selects an available agent when the configured default is unavailable.
- **Agent Fallback Persistence**: Availability fallback now applies at runtime for session launch/open flows without silently rewriting the saved default agent setting.

### Fixed
- **Bundled Daemon Preference in App Runtime**: Desktop app startup now prefers the bundled `attn` daemon binary by default (with `ATTN_PREFER_LOCAL_DAEMON=1` opt-in for local dev), preventing stale `~/.local/bin/attn` installs from breaking cask-launched sessions.
- **Cask Runtime Wrapper Resolution**: Daemon-managed session spawn and Claude hooks now use an explicit wrapper path (`ATTN_WRAPPER_PATH`) instead of relying on `attn` being in shell `PATH`, so Homebrew cask installs work without separately installing the formula.
- **Release Artifact macOS Signature Integrity**: Release workflow now re-signs and verifies the built `attn.app`, rebuilds the DMG from the signed app, and replaces uploaded release assets from CI before publishing cask artifacts.
- **Release CI Reliability**: Release workflow now installs `pnpm` before enabling pnpm cache in `setup-node`, and supports manual `workflow_dispatch` retries for existing tags so failed tagged runs can be rebuilt and published entirely from CI.
- **Copilot Stop Classification Path**: Add Copilot transcript discovery under `~/.copilot/session-state/*/events.jsonl` (matched by cwd + recent activity) so Copilot sessions classify on stop without hooks.
- **Copilot Resume Transcript Matching**: When launching Copilot with `--resume <session-id>`, stop-time classification now first checks `~/.copilot/session-state/<session-id>/events.jsonl` before falling back to heuristic cwd/timing discovery.
- **Copilot Classifier Safety Isolation**: Copilot classification now disables custom instructions and avoids tool auto-approval, and runs from an isolated temp cwd so classifier sessions do not contaminate cwd-based transcript matching.
- **Copilot Transcript Selection Robustness**: Copilot transcript discovery now prefers session-state candidates whose `session.start` timestamp is closest to the launched session time, with safe modtime fallback.
- **Session Indicator Reliability**: Remove stale Codex-only "unknown transcript" indicator fallback so Codex/Copilot sessions render normal color-based states in sidebar/drawer.
- **Classifier Audit Logging**: Classifier logs now include full input text and full model output text so classification decisions can be reviewed later in daemon logs.
- **PTY Live-State Stability**: Prompt remnants in recent terminal output no longer force `idle` while new assistant output is still streaming, improving Codex/Copilot working-state transitions.
- **Codex/Copilot State Source-of-Truth**: PTY-derived `waiting_input`/`idle` transitions are now ignored for Codex/Copilot sessions so final idle/waiting colors come from transcript + classifier, reducing noisy false transitions.
- **Session Restore E2E Coverage**: Session restore/reconnect Playwright assertions now validate actual sidebar session state/selection markers instead of removed `state unknown` indicators.
- **Settings Validation Feedback**: Invalid executable settings now surface an explicit UI error toast, and the client re-syncs to daemon settings after validation failure instead of leaving stale optimistic values.
- **Git Status/Review Command Chatter**: Frontend now avoids redundant git-status subscribe/unsubscribe cycles when the active session directory is unchanged, and de-duplicates in-flight branch-diff requests for the same repo directory.
- **Claude First-Turn State Detection**: Stop-time classification now retries Claude transcript reads briefly and falls back to transcript discovery by session ID when the provided path is missing/stale, preventing first-turn empty-transcript misclassification.

### Removed
- **Tauri Updater Runtime Wiring**: Removed updater/process plugin wiring and updater signing requirements from the desktop app release path.

## [2026-02-07]

### Added
- **Daemon PTY Manager**: PTY session lifecycle now lives in Go (`internal/pty`) with spawn, attach/detach, input, resize, kill, scrollback ring buffer, per-session sequence numbers, and UTF-8/ANSI-safe output chunking.
- **Codex Live State Detection in Daemon**: Ported output-based codex prompt/approval heuristics into Go PTY reader path so codex sessions update `working` / `waiting_input` / `pending_approval` without Rust PTY code.
- **Codex Visible-Frame Snapshot Restore**: Daemon now maintains a virtual terminal screen for codex sessions and includes a rendered screen snapshot in `attach_result`, so reconnect/reattach restores what was visible (including alternate-screen UIs) before live stream resumes.
- **PTY WebSocket Protocol**: Added daemon commands/events for terminal transport:
  - Commands: `spawn_session`, `attach_session`, `detach_session`, `pty_input`, `pty_resize`, `kill_session`
  - Events: `spawn_result`, `attach_result`, `pty_output`, `session_exited`, `pty_desync`
- **WebSocket Command Error Event**: Unknown/invalid WebSocket commands now return a structured `command_error` event instead of failing silently.
- **Managed Wrapper Mode**: `ATTN_DAEMON_MANAGED=1` support in the wrapper to skip daemon auto-start and register/unregister side effects when sessions are daemon-spawned.
- **Session Recovery Test Harness**: Added Playwright daemon lifecycle controls (`start/stop/restart`) plus a dedicated session restore/reconnect spec to guard app restart + daemon restart behavior.
- **Real-PTY Utility Focus Regression Coverage**: Added Playwright real-PTY checks for utility terminal keyboard input on `Cmd+T` and after switching sessions away/back.

### Changed
- **Terminal Transport Path**: Frontend terminal I/O now routes through daemon WebSocket PTY commands/events instead of Tauri PTY IPC.
- **Session Persistence Behavior**: App no longer clears daemon sessions on startup; existing daemon-managed sessions can survive UI restart and be reattached.
- **Session Agent Typing**: Protocol schema now models session agent as a strict `claude|codex` enum, with shared normalization helpers for register/spawn/store paths.
- **Restore Scrollback Depth**: Increased daemon PTY replay buffer to `8 MiB` per session and frontend terminal scrollback to `50,000` lines so restored sessions recover much deeper history.
- **PTY Restore Semantics**: Frontend now attempts `attach_session` first and only spawns when the daemon does not already know the session ID, avoiding accidental respawn of missing PTYs after daemon restarts.
- **Daemon Startup Safety**: Daemon now refuses to replace an already-running daemon instance instead of SIGTERM/SIGKILL takeover.
- **Connection Recovery**: Frontend removed daemon auto-restart on WebSocket failure; it now reconnects and surfaces a manual-retry path if daemon stays offline.
- **Upgrade Messaging**: Version-mismatch banner now includes active-session impact guidance for manual daemon restart timing.
- **Unregister Semantics**: `unregister` is now a hard-stop path (terminate process/session resources, then remove session metadata); `detach_session` remains the keep-running path.
- **Spawn Consistency**: Session registration now happens only after PTY spawn succeeds, avoiding temporary/stale session entries on spawn failures.
- **PTY Shell Startup**: PTY spawn now captures login-shell environment (`shell -l -c 'env -0'`) and reuses it for session commands, so daemon-spawned sessions better match the user's interactive shell environment.
- **WebSocket Ordering**: `pty_input` now follows the same ordered command path as other WebSocket commands.
- **Protocol Schema Coverage**: TypeSpec now explicitly models all daemon WebSocket events and reviewer streaming payloads used in runtime.
- **Protocol Version**: Bumped daemon/app protocol version to `26`.

### Fixed
- **Daemon Spawn Wrapper Path Resolution**: PTY-launched sessions now validate candidate `attn` executable paths before invoking them, preventing `fish: Unknown command` failures when a stale or missing binary path is discovered.
- **Utility Terminal Shell Bootstrap**: `Cmd+T` utility terminals now wire input before replaying buffered PTY output, so early terminal capability queries receive responses and interactive login shells (like fish) initialize their prompt correctly.
- **Daemon Socket Detection (Tauri)**: Frontend daemon health/start checks now use `~/.attn/attn.sock` (and `ATTN_SOCKET_PATH` override), matching daemon defaults.
- **Stale Daemon Socket Recovery**: App startup now verifies the daemon socket is connectable (not just present), removes stale socket files, and waits for a live socket before reporting daemon startup success.
- **Persistence Degraded Visibility**: When SQLite open/migrations fail and daemon falls back to in-memory state, the app now receives a persistent warning banner that includes the DB path and points to daemon logs for recovery details.
- **Terminal Output Races**: Buffered PTY output until terminals are ready to prevent dropped initial prompt/scrollback in main and utility terminals.
- **Reconnect Attach Hygiene**: Exited/unregistered sessions are removed from frontend reattach tracking to avoid repeated failed `attach_session` attempts.
- **Exited PTY Cleanup**: Daemon now removes exited PTY sessions from the manager to prevent stale in-memory session accumulation.
- **Login Shell Exec Failures**: PTY spawn now retries with safe fallback shells (`/bin/zsh`, `/bin/bash`, `/bin/sh`) when the preferred login shell cannot be executed (e.g. macOS `operation not permitted`).
- **macOS PTY Launch EPERM**: PTY spawn no longer requests `Setpgid` on Darwin with `forkpty`, fixing `fork/exec ... operation not permitted` for all shells.
- **Daemon PTY Log Noise**: WebSocket command logging now skips high-frequency PTY traffic (`pty_input`, `pty_resize`, `attach_session`, `detach_session`), and expected PTY `session not found` races are no longer logged as errors.
- **SQLite Migration Resilience**: Migration 20 (`prs.host`) is now idempotent when the column already exists, preventing DB-open failure and in-memory fallback that caused session/PR state loss after daemon restarts.
- **Session Restore on App Reopen**: UI session store now hydrates from daemon sessions after initial state, so tracked sessions reappear after closing/reopening the app.
- **Reattach Existing PTYs**: Frontend PTY spawn path now treats `session already exists` as attachable, preventing restore sessions from failing when terminal views reconnect.
- **Session Agent Persistence**: Sessions now persist and restore `agent` (`claude`/`codex`) across daemon/app restarts, including wrapper-registered sessions and daemon-spawned sessions.
- **Session Duplication on Reconnect**: Session lists are now upserted/deduplicated by ID, and daemon re-register/spawn updates emit `session_state_changed` for existing IDs instead of duplicate `session_registered` events.
- **Stale PTY Auto-Respawn**: Restored sessions with missing PTYs no longer auto-create fresh agents with the same ID, preventing confusing "blank restored terminal" behavior and inconsistent close flows.
- **Codex Replay Robustness**: Terminal write path now uses `writeUtf8` when available and surfaces a warning when Codex replay is truncated.
- **Ghost Sessions After Daemon Restart**: Daemon startup now prunes persisted sessions that have no live PTY and surfaces a warning, preventing stale sessions from reappearing after daemon restarts.
- **Empty State Sync on Reconnect**: Frontend now treats missing `sessions/prs/repos/authors/settings` in `initial_state` as empty values, preventing stale UI data when daemon responses omit empty arrays/maps.
- **Utility Terminal Focus After Session Switch**: Selecting a session no longer forcibly steals focus back to the main terminal when that session already has an open utility tab, preventing “blinking cursor but no visible typing” regressions.
- **Utility Terminal Output Restore After Dashboard Roundtrip**: Returning from dashboard/home now re-attaches existing utility PTYs and replays scrollback into the remounted terminal view, so prior output remains visible instead of showing an empty prompt.

### Removed
- **Rust PTY Manager**: Removed `app/src-tauri/src/pty_manager.rs` and PTY Tauri command registrations (`pty_spawn`, `pty_write`, `pty_resize`, `pty_kill`).
- **Rust PTY-Only Dependencies**: Removed `portable-pty`, `base64`, and `nix` from Tauri dependencies.
- **Unused Tauri Greeting Command**: Removed unused Rust `greet` command wiring.

---

## [2026-02-05]

### Added
- **Review Panel Harness Coverage**: New Playwright harness spec validates non-blocking review loading, failed remote sync fallback, and selection persistence across background refreshes.

### Changed
- **Review Panel Remote Sync**: Opening review now shows branch diff from local refs immediately, then refreshes in the background after remote fetch completes.
- **Review Panel Sync Feedback**: Header now shows `Syncing with origin...` during background refresh and a non-blocking warning when remote sync fails.

### Fixed
- **Fork Worktree Naming**: Creating a fork worktree from inside an existing worktree now resolves to the main repo before generating branch/worktree paths, so custom names like `fun` no longer get appended to an existing generated suffix.

---

## [2026-02-02]

### Added
- **PATH Recovery for GUI App Launches**: New `pathutil` package ensures external tools like `gh` can be found when app is launched from Finder/Dock (macOS only)

---

## [2026-02-01]

### Changed
- **Location Picker Search**: Directory search now uses "contains" matching instead of "starts with", so typing "proxy" matches "metadata-proxy"
- **Location Picker Sort Order**: Directories starting with the search term appear first, followed by directories that contain it elsewhere
- **Location Picker Navigation**: Arrow key navigation now scrolls the selected item into view

---

## [2026-01-31]

### Added
- **Multi-Host GitHub Support**: Discover authenticated gh hosts and poll PRs across github.com + GHES
- **Host Badges + Connected Hosts**: Show host badges when a repo spans multiple hosts and list detected hosts in Settings
- **Mute by Author**: Hide all PRs from specific authors (e.g., dependabot, renovate)
  - 👤 button on PR rows to mute author (🤖 for bot authors)
  - Muted Authors section in Settings to view and unmute
  - Undo toast supports author mutes

### Changed
- **PR ID Format**: IDs now include host prefixes (e.g., github.com:owner/repo#123) for correct routing
- **PR Actions Routing**: Approve/merge/fetch details route by PR ID to the correct host
- **GitHub CLI Requirement**: Requires gh v2.81.0+ for host discovery

### Fixed
- **Per-Host Rate Limits**: Rate limiting is isolated per host so one host doesn't block others
- **PR Detail Refresh**: Detail refresh runs per host to avoid cross-host mixups

### Removed
- **GitHub Env Overrides**: `GITHUB_API_URL`/`GITHUB_TOKEN` configuration removed (gh discovery only)

---

## [2026-01-19]

### Added
- **PRs Panel Harness**: Playwright test harness for the dashboard PRs panel
- **PRs Harness Scenarios**: Additional test cases for PR action wiring and error flows (fetch details, missing projects dir, fetch remotes, worktree creation)
- **Default Session Agent Setting**: Configure Codex/Claude in Settings and use it for PR opens
- **Claude Default Agent**: Default to Claude when no session agent setting exists

### Fixed
- **Open PR Worktrees**: Fetch missing PR branch details on demand before creating worktrees
- **macOS PATH Recovery**: Rebuild PATH via `path_helper` for Finder-launched daemon so `gh`/`git` are available
- **Fetch Remotes Errors**: Surface underlying git error details when fetch fails
- **Projects Directory Fallback**: Resolve repos one level deeper under the projects directory when needed
- **Repo Safety Checks**: Validate git worktree status and prefer matches whose `origin` repo name matches the PR repo
- **PR Title Links**: Open PR URLs from the dashboard title click
- **PTY Mock Detection**: Use Tauri runtime detection to avoid accidental mock PTY sessions

---

## [2026-01-17]

### Added
- **Mock PTY Mode**: Optional PTY stub for tests and development when real agent terminals aren't available

### Fixed
- **Session Agent Persistence**: "New session" agent choice (Codex/Claude) now saves in daemon settings so it survives app restarts

### Changed
- **Review Mode**: Review panel now opens as a full-screen focus view with animated transition and clearer keyboard dismissal

---

## [2026-01-06]

### Added
- **Won't Fix Action**: New comment action for marking comments as "won't fix"
  - Mutually exclusive with Resolved (setting one clears the other)
  - Visual indicator with amber styling
  - Available in both Review Panel and reviewer agent
- **Markdown Support**: Comment content now renders Markdown
  - Supports code blocks, links, lists, bold/italic, blockquotes
  - Uses ReactMarkdown for saved comments, marked for CodeMirror widgets
- **Session Agent Picker**: Choose Codex or Claude when starting a new session
  - Codex is the default selection
  - Keyboard shortcuts for quick switching
- **PTY State Detection**: Infer session states from PTY output for non-hook agents (e.g. Codex)

### Changed
- **PR-like Branch Diff**: Review Panel now shows all changes vs origin/main instead of just uncommitted changes
  - File list shows all files changed on the branch (committed + uncommitted)
  - Diffs compare against base branch, not HEAD
  - After committing, panel still shows all branch work
  - Files with uncommitted changes marked with indicator
  - Auto-fetches remotes before computing diff
- **New Session Agent**: The Codex/Claude selection now persists across app restarts
- Default to Codex for in-app sessions while testing

### Fixed
- **Font Size Shortcuts**: Cmd+/- no longer loses collapsed regions or comments
  - Added fontSize to effect dependencies so decorations rebuild after editor recreation
- **Font Size Scaling**: Comment UI elements now scale with font size changes
  - Author badges, action buttons, textarea, collapsed regions all respect zoom level
- **Git Status Parsing**: Fixed bug where file paths were truncated in uncommitted changes detection

---

## [2026-01-05]

### Added
- **Reviewer Agent**: AI-powered code review using Claude Agent SDK
  - Streams tool calls in real-time as agent reviews code
  - MCP tools: `get_changed_files`, `get_diff`, `list_comments`, `add_comment`, `resolve_comment`
  - Re-review context: agent sees previous comments and their resolution status
  - "Resolved by Claude/you" badges on comments
- **Selection Actions**: Select code in diff to send to Claude or add comment
  - Popup appears on text selection with "Send to Claude" and "Add Comment" buttons
- **Clickable File References**: File paths in reviewer output are clickable
  - Supports backtick-wrapped filenames, table entries, and suffix matching
  - Clicking jumps to file diff and scrolls to relevant line
- **UI Improvements**:
  - Auto-scroll review brief as content streams in
  - Font size persists across sessions
  - Animated progress line during review
  - Centered loading spinner

### Fixed
- **Comment Interaction**: Keyboard events in comment textarea no longer trigger panel shortcuts
- **Tool Call Navigation**: Clicking add_comment tool call switches to correct file and scrolls to line
- **Cursor in Read-only Editor**: Cursor no longer appears in diff view

---

## [2026-01-04]

### Added
- **Reviewer Agent Foundation**: Phase 3 implementation
  - Walking skeleton with daemon integration
  - Mock transport for testing without real Claude API
  - Resolution tracking via MCP tools

## [2026-01-03]

### Added
- **UnifiedDiffEditor**: New diff component replacing DiffOverlay
  - Deleted lines are real document lines (not DOM injected)
  - Single comment mechanism works for all line types
  - Visual hunks mode with collapsible unchanged regions
- **Keyboard Shortcuts**: `⌘Enter` to save, `Escape` to cancel comments
- **Component Test Harness**: Playwright-based testing for CodeMirror components
  - Real browser environment for accurate DOM testing
  - Mock API for isolated component testing

### Fixed
- **Daemon Race Condition**: flock-based PID lock prevents multiple daemons
- **Scroll Position**: Preserved when saving/canceling comments
- **Editor Performance**: Eliminated flash on comment state changes
- **Deleted Line Comments**: Now appear at correct position in diff

---

## [2026-01-02]

### Added
- **Review Panel**: New full-screen diff review interface
  - File list with "NEEDS REVIEW" and "AUTO-SKIP" sections
  - CodeMirror 6 with One Dark theme for syntax highlighting
  - Unified diff view with clear red/green highlighting
  - Auto-skip detection for lockfiles (pnpm-lock.yaml, package-lock.json, etc.)
  - Hunks/Full toggle to collapse unchanged regions
  - Keyboard navigation: `j`/`k` navigate, `]` next unreviewed, `e`/`E` expand
  - Font size controls: `⌘+`/`⌘-` zoom, `⌘0` reset
  - Entry point: "Review" button in Changes panel header
- **Inline Comments**: Add comments on any line in the diff
  - Delete button for removing comments
  - Correct positioning for deleted line comments
