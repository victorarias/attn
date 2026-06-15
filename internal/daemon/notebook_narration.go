package daemon

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/notebook"
	"github.com/victorarias/attn/internal/tasks"
)

// Notebook narration: two headless agent tasks that turn raw session work into a
// curated daily work-journal.
//
//   - summarize_session (cheap tier): per-session. Reads ONE session transcript and
//     writes a faithful digest to the raw tier, partitioned by workspace
//     (RawSessionsDir/<wsID>/<sessionID>.md). Pure machine input for the narrator;
//     high frequency, so it runs on the cheap model.
//   - narrate_workspace (strong tier): per-workspace, coalesced. Reads the workspace's
//     digests + context snapshot + dispatch outcomes and writes/refreshes the curated
//     journal entry for today (journal/<today>.md). The load-bearing product surface.
//
// Both are NATIVE-TOOLS tasks: the agent uses its own file tools and writes the
// target file itself. Unlike the workspace-context janitor (which reads a candidate
// back and commits it under a CommitGuard), THE FILE IS THE LEDGER here — the
// executor's success gate is "did the agent write the target file?" (digest exists /
// journal contains the workspace marker), not a daemon read-back-and-commit. This is
// deliberate: the journal is shared and concurrently written by sibling narrators and
// the human, so the daemon must not own a serialize-and-overwrite commit; the agents'
// native read-before-write CAS is the concurrency control (see the prompt briefs).
//
// CONCURRENCY CAVEAT (Codex narrator): Claude's Write/Edit enforce read-before-write
// staleness rejection, which the shared-journal story depends on. Codex's apply-patch
// CAS is UNVERIFIED for the installed version, so Claude is the built-in default for
// both tiers (see notebook_narration_config.go). Codex is NOT hard-gated out — a user
// may configure it — but a concurrent Codex narrate could in principle clobber a
// sibling's same-day entry if its patch tooling does not detect the on-disk change.

const (
	// notebookSummarizeSessionTimeout bounds one per-session digest run. Reading and
	// summarizing a single transcript is cheap; a few minutes is ample headroom.
	notebookSummarizeSessionTimeout = 4 * time.Minute
	// notebookNarrateWorkspaceTimeout bounds one curated-journal run. The narrator
	// reads many digests + prior entries and writes prose on the strong model, so it
	// gets a wider budget than the per-session digest.
	notebookNarrateWorkspaceTimeout = 8 * time.Minute
	// notebookNarrationDebounce coalesces the burst of session stops in an active
	// workspace into a single narrate pass (and collapses a chatty session's repeated
	// stops into one digest run). The removal-boundary final narrate overrides this
	// with ZeroDebounce.
	notebookNarrationDebounce = 2 * time.Minute
	// notebookSummarizeMetaTranscript and notebookSummarizeMetaWorkspace are the
	// task.Meta keys carrying the summarize_session run's inputs onto the durable
	// record at enqueue time (handleStop), where BOTH the session row and the
	// workspace row still exist. They MUST be carried because the debounced run
	// fires AFTER a single-session-workspace teardown has deleted both rows, so the
	// executor can no longer re-derive the transcript path or the workspace bucket
	// from a live row. The transcript file itself survives under ~/.claude/~/.codex.
	notebookSummarizeMetaTranscript = "transcript"
	notebookSummarizeMetaWorkspace  = "workspace"
	// notebookNarrateMetaDailyPass marks a narrate_workspace task enqueued by the
	// daily-narrate cron (the long-lived-workspace backstop) rather than by
	// session-end or the removal boundary. It relaxes the executor's success gate so
	// a no-op daily refresh (nothing new to narrate) is a CLEAN DONE instead of a
	// retried failure (see narrateWorkspaceExecutor's dailyPass branch). Session-end
	// and removal passes carry no daily flag and keep strict "must have written"
	// gating.
	notebookNarrateMetaDailyPass = "daily_pass"
	// notebookSoloSessionBucket is the RawSessionsDir subdir holding digests for
	// solo (non-workspace) sessions. It is a reserved name (leading underscore) so
	// it can never collide with a real workspace id bucket, and rawTierSegment
	// rejects any workspace id that begins with "." — workspace ids are not allowed
	// to start with "_" either way in practice, but the underscore keeps the bucket
	// visually distinct from a workspace dir.
	notebookSoloSessionBucket = "_solo"
)

// notebookNarrationAllowedTools is the native tool set both narration agents get.
// Unlike the janitor (file tools only), the narrators may run read-only shell to
// grep large transcripts and locate prior journal markers, so Bash is included
// (the briefs explicitly tell them to use Read/Grep/Bash). Claude consumes this as
// --allowedTools; Codex ignores it (its tooling comes from the workspace-write
// sandbox), so the Codex narrator's writability is governed by ExtraWritableRoots.
var notebookNarrationAllowedTools = []string{"Read", "Write", "Edit", "Grep", "Glob", "Bash"}

// --- summarize_session ---

// summarizeSessionExecutor is the runner-registered ExecutorFunc for
// summarize_session. task.Subject is the session id. It resolves the transcript path
// and the workspace bucket PREFERRING the inputs carried on task.Meta (stashed at
// enqueue time, where both the session row and the workspace row still existed) and
// falling back to the live session row — so the run stays correct after a
// single-session-workspace teardown has deleted both rows (the transcript file
// itself survives under ~/.claude/~/.codex). It assembles the digest prompt with
// absolute paths, runs the agent, and verifies the digest file was (re)written. The
// written digest is the only success evidence (the file is the ledger): a run that
// returns without writing it is an error so the runner backs off and retries. On
// success, if the workspace has since been removed it re-enqueues the retrospective
// narrate so the late digest lands in it (see the re-narrate hook below).
func (d *Daemon) summarizeSessionExecutor(ctx context.Context, task *tasks.Task) error {
	sessionID := strings.TrimSpace(task.Subject)
	if sessionID == "" {
		return errors.New("summarize_session requires a session id")
	}

	root, err := d.notebookRoot()
	if err != nil {
		return fmt.Errorf("summarize_session: notebook root: %w", err)
	}
	if strings.TrimSpace(root) == "" {
		return errors.New("summarize_session: notebook is disabled")
	}

	config, err := d.notebookNarrationConfigFor(notebookSummarizeSessionKind)
	if err != nil {
		return err
	}
	provider, executablePath, err := d.resolveNotebookNarrationExecutable(config)
	if err != nil {
		return err
	}

	// Resolve the transcript path and workspace id, PREFERRING the inputs carried on
	// the task (stashed at enqueue time, where both the session row and workspace row
	// still existed) and falling back to the live session row. The carried inputs are
	// what make this run correct AFTER a single-session-workspace teardown: by the
	// time the debounced run fires both rows are gone, but the transcript file
	// survives on disk and the carried workspace id still routes the digest to the
	// right per-workspace bucket. The fallback covers a manually-enqueued/legacy task
	// (no Meta) whose row still exists.
	carriedTranscript := strings.TrimSpace(task.Meta[notebookSummarizeMetaTranscript])
	carriedWorkspace := strings.TrimSpace(task.Meta[notebookSummarizeMetaWorkspace])
	_, hasCarriedWorkspace := task.Meta[notebookSummarizeMetaWorkspace]

	session := d.store.Get(sessionID)
	if session == nil && carriedTranscript == "" {
		// No row AND nothing carried: the session is genuinely gone with no transcript
		// to summarize. That is a no-op success, not a failure (nothing to retry).
		d.logf("summarize_session: session %s no longer present and no carried transcript, skipping", sessionID)
		return nil
	}

	transcriptPath := carriedTranscript
	if transcriptPath == "" {
		transcriptPath = d.resolveTranscriptPathForSession(session, "")
	}

	// Workspace id for the per-session digest bucket: prefer the carried id (which
	// survives the row deletion) and fall back to the live row's. The carried id is
	// empty for a genuinely solo session, which keeps the digest in the _solo bucket.
	workspaceID := carriedWorkspace
	if !hasCarriedWorkspace && session != nil {
		workspaceID = strings.TrimSpace(session.WorkspaceID)
	}

	// Partition the per-session digest dir by the session's workspace so the
	// narrate pass for a workspace reads ONLY its own members' digests, not a flat
	// dir holding every workspace's (and every solo session's) digest. The bucket
	// survives workspace removal on disk, so the removal-pass narrate still finds the
	// scoped digests. Both segments route through the raw-tier guard so a crafted
	// session/workspace id cannot escape the raw tier and steer the agent's native
	// Write at the curated journal (the hole the base raw-floor PR closed for the
	// daemon's own snapshot write). The carried wsID is just as client-controlled as
	// the row's was, so it is guarded identically.
	digestPath, err := notebookSessionDigestPath(root, workspaceID, sessionID)
	if err != nil {
		return fmt.Errorf("summarize_session: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(digestPath), 0o755); err != nil {
		return fmt.Errorf("summarize_session: create raw sessions dir: %w", err)
	}

	workDir, err := os.MkdirTemp("", "attn-summarize-session-*")
	if err != nil {
		return fmt.Errorf("summarize_session: create scratch dir: %w", err)
	}
	defer os.RemoveAll(workDir)

	// Snapshot the digest's pre-run identity so the success gate can require the run
	// to have actually (re)written it. A coalesced re-run (Enqueue resets a done
	// record back to queued) on a session whose digest already exists must not be
	// reported done just because the PRIOR run's file is still there — a no-op agent
	// would otherwise silently leave a stale digest while reporting success.
	before := fileFingerprintOf(digestPath)

	request := agentdriver.HeadlessTaskRequest{
		Executable:   executablePath,
		Model:        config.Model,
		Prompt:       buildSummarizeSessionPrompt(transcriptPath, sessionID, digestPath),
		WorkDir:      workDir,
		AllowedTools: notebookNarrationAllowedTools,
		// The digest lives under the notebook raw tier, outside the scratch WorkDir,
		// so a Codex narrator needs that dir made writable (Claude ignores this).
		ExtraWritableRoots: []string{filepath.Dir(digestPath)},
	}

	run := d.summarizeSessionExecution
	if run == nil {
		run = func(ctx context.Context, p agentdriver.HeadlessTaskProvider, r agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
			return p.RunHeadlessTask(ctx, r)
		}
	}
	result, err := run(ctx, provider, request)
	if err != nil {
		return fmt.Errorf("summarize_session: run agent: %w (%s)", err, result.Diagnostics)
	}

	// The file is the ledger: a run that returned without (re)writing the digest
	// failed, regardless of what the agent claimed. Require the digest to exist AND
	// to have changed since the pre-run snapshot, so a no-op run over a prior run's
	// stale digest is a failure (requeued with backoff), not a false done.
	after := fileFingerprintOf(digestPath)
	if !after.exists {
		return fmt.Errorf("summarize_session: agent did not write digest %s (%s)", digestPath, result.Diagnostics)
	}
	if before.exists && after.equal(before) {
		return fmt.Errorf("summarize_session: agent left digest %s unchanged (%s)", digestPath, result.Diagnostics)
	}
	d.logf("summarize_session: session=%s agent=%s model=%s digest=%s", sessionID, config.Agent, config.Model, digestPath)

	// Re-narrate hook (closes the removal/debounce timing gap). On a
	// single-session-workspace teardown the removal-boundary final narrate already
	// ran almost immediately (zero debounce) over an EMPTY digest bucket, because
	// this summarize was still debounced. Now that the grounded digest exists, the
	// removal retrospective is stale (built from the context snapshot alone). If we
	// know a non-empty workspace id AND its row is gone (the workspace was removed),
	// re-enqueue a zero-debounce narrate so the retrospective is rewritten WITH the
	// final session's work — the narrate freshness-guard makes it actually rewrite
	// the block (the body changes). Only on removal: an active workspace's pending
	// narrate already covers a fresh digest, and re-narrating it would burn an extra
	// strong-tier run. LOOP-SAFETY: narrate completion never enqueues summarize, so
	// there is no cycle; multiple member summaries coalesce to one narrate per wsID.
	if workspaceID != "" && d.store.GetWorkspace(workspaceID) == nil {
		d.logf("summarize_session: workspace %s removed, re-narrating retrospective with fresh digest", workspaceID)
		d.enqueueFinalNarrateWorkspace(workspaceID)
	}
	return nil
}

// notebookSessionDigestPath builds the absolute path of a session's raw digest,
// partitioned by the session's workspace so a workspace's narrate pass reads only
// its own members' digests. A workspace session lands at
// RawSessionsDir/<wsID>/<sessionID>.md; a solo session (no workspace) lands at
// RawSessionsDir/<soloBucket>/<sessionID>.md. Both id segments route through the
// raw-tier guard, so a crafted session or workspace id is rejected (a hard error)
// rather than allowed to climb out of the raw tier via filepath.Join's "..".
func notebookSessionDigestPath(root, workspaceID, sessionID string) (string, error) {
	name, err := rawTierFilename(sessionID)
	if err != nil {
		return "", fmt.Errorf("unsafe session id: %w", err)
	}
	bucket := notebookSoloSessionBucket
	if workspaceID != "" {
		bucket, err = rawTierSegment(workspaceID)
		if err != nil {
			return "", fmt.Errorf("unsafe workspace id: %w", err)
		}
	}
	return filepath.Join(notebook.RawSessionsDir(root), bucket, name), nil
}

// notebookWorkspaceSessionsDir is the per-workspace digest subdir the narrate pass
// hands the narrator as RAW_SESSIONS_DIR, so it reads only this workspace's member
// digests. It mirrors the bucket notebookSessionDigestPath writes to.
func notebookWorkspaceSessionsDir(root, workspaceID string) (string, error) {
	bucket, err := rawTierSegment(workspaceID)
	if err != nil {
		return "", fmt.Errorf("unsafe workspace id: %w", err)
	}
	return filepath.Join(notebook.RawSessionsDir(root), bucket), nil
}

// --- narrate_workspace ---

// narrateWorkspaceExecutor is the runner-registered ExecutorFunc for
// narrate_workspace. task.Subject is the workspace id. It derives IS_REMOVAL_PASS
// at RUN TIME from whether the workspace row still exists (an absent row means the
// workspace was removed and this is the final retrospective pass), gathers the
// narrator's inputs, runs the agent, and verifies the journal now carries this
// workspace's marker for today (the file is the ledger).
func (d *Daemon) narrateWorkspaceExecutor(ctx context.Context, task *tasks.Task) error {
	workspaceID := strings.TrimSpace(task.Subject)
	if workspaceID == "" {
		return errors.New("narrate_workspace requires a workspace id")
	}

	root, err := d.notebookRoot()
	if err != nil {
		return fmt.Errorf("narrate_workspace: notebook root: %w", err)
	}
	if strings.TrimSpace(root) == "" {
		return errors.New("narrate_workspace: notebook is disabled")
	}

	config, err := d.notebookNarrationConfigFor(notebookNarrateWorkspaceKind)
	if err != nil {
		return err
	}
	provider, executablePath, err := d.resolveNotebookNarrationExecutable(config)
	if err != nil {
		return err
	}

	inputs, err := d.gatherNarrateWorkspaceInputs(root, workspaceID)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(inputs.JournalDir, 0o755); err != nil {
		return fmt.Errorf("narrate_workspace: create journal dir: %w", err)
	}
	// MkdirAll the per-workspace sessions bucket so the narrator's "Read every digest
	// in RAW_SESSIONS_DIR" step does not fault on a missing dir when no member ever
	// summarized (e.g. a workspace removed before any session stopped).
	if err := os.MkdirAll(inputs.RawSessionsDir, 0o755); err != nil {
		return fmt.Errorf("narrate_workspace: create raw sessions dir: %w", err)
	}

	// Snapshot this workspace's marker block before the run so the success gate can
	// require it to have actually changed. Without this a coalesced re-run (the
	// removal-boundary final narrate firing after an active-day narrate already
	// wrote today's marker) would be falsely marked done off the PRIOR run's block,
	// silently dropping the removal retrospective even when this run's agent wrote
	// nothing for the workspace.
	before, err := workspaceNarrationBlock(inputs.JournalPath, workspaceID)
	if err != nil {
		return fmt.Errorf("narrate_workspace: read journal: %w", err)
	}

	workDir, err := os.MkdirTemp("", "attn-narrate-workspace-*")
	if err != nil {
		return fmt.Errorf("narrate_workspace: create scratch dir: %w", err)
	}
	defer os.RemoveAll(workDir)

	request := agentdriver.HeadlessTaskRequest{
		Executable:   executablePath,
		Model:        config.Model,
		Prompt:       buildNarrateWorkspacePrompt(inputs),
		WorkDir:      workDir,
		AllowedTools: notebookNarrationAllowedTools,
		// The journal and raw tier live under the notebook root, outside the scratch
		// WorkDir; widen the Codex sandbox to the whole root so it can read the raw
		// inputs and write the curated journal (Claude ignores this — dontAsk is not
		// sandboxed). Reads are unrestricted under workspace-write, but transcript
		// dirs live under $HOME outside the root, so we add the root for the write.
		ExtraWritableRoots: []string{root},
	}

	run := d.narrateWorkspaceExecution
	if run == nil {
		run = func(ctx context.Context, p agentdriver.HeadlessTaskProvider, r agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
			return p.RunHeadlessTask(ctx, r)
		}
	}
	result, err := run(ctx, provider, request)
	if err != nil {
		return fmt.Errorf("narrate_workspace: run agent: %w (%s)", err, result.Diagnostics)
	}

	// The file is the ledger: success means today's journal now carries THIS
	// workspace's entry block (delimited by `<!-- attn:wsnarr:<wsID> -->`) AND that
	// block changed since the pre-run snapshot. Requiring a change (not mere
	// presence) means a coalesced re-run whose agent no-ops over a prior run's block
	// fails and is retried, instead of being falsely marked done off stale content.
	after, err := workspaceNarrationBlock(inputs.JournalPath, workspaceID)
	if err != nil {
		return fmt.Errorf("narrate_workspace: verify journal: %w", err)
	}

	// dailyPass relaxes the success gate for the daily-cron backstop ONLY. A daily
	// refresh legitimately finds nothing new — the workspace was already narrated
	// today, or only old material is on disk — and forcing a write would spam the
	// runner's backoff straight to dead. The raw material persists, so a no-op daily
	// pass loses nothing: the next trigger re-narrates. So when this is a daily pass
	// (and NOT a removal pass), "agent left the block absent" and "agent left the
	// block unchanged" are both a CLEAN DONE. Removal passes (the full retrospective)
	// and session-end routine passes (no daily flag) keep STRICT gating, which
	// preserves the retry-until-the-digest-lands property they depend on.
	dailyPass := !inputs.IsRemovalPass && strings.TrimSpace(task.Meta[notebookNarrateMetaDailyPass]) == "1"

	if !after.present {
		if dailyPass {
			d.logf("narrate_workspace: daily pass for %s found nothing new to narrate (no entry written); clean no-op", workspaceID)
			return nil
		}
		return fmt.Errorf("narrate_workspace: agent did not write %s entry to %s (%s)", workspaceNarrationMarker(workspaceID), inputs.JournalPath, result.Diagnostics)
	}
	if before.present && after.body == before.body {
		if dailyPass {
			d.logf("narrate_workspace: daily pass for %s left the entry unchanged (nothing new to narrate); clean no-op", workspaceID)
			return nil
		}
		return fmt.Errorf("narrate_workspace: agent left %s entry in %s unchanged (%s)", workspaceNarrationMarker(workspaceID), inputs.JournalPath, result.Diagnostics)
	}
	d.logf(
		"narrate_workspace: workspace=%s agent=%s model=%s removal=%t journal=%s",
		workspaceID, config.Agent, config.Model, inputs.IsRemovalPass, inputs.JournalPath,
	)
	return nil
}

// gatherNarrateWorkspaceInputs assembles the absolute-path inputs the narrate
// agent needs. IS_REMOVAL_PASS is derived from workspace-row absence (the removal
// boundary deleted the row before this run); WORKSPACE_TITLE falls back to the
// removal snapshot or the id when the row is gone. TRANSCRIPT_PATHS are the live
// member sessions' transcripts (best-effort — they may be gone after removal, which
// is fine: the digests are the durable record and the brief only consults
// transcripts to chase a divergence).
func (d *Daemon) gatherNarrateWorkspaceInputs(root, workspaceID string) (narrateWorkspacePromptInputs, error) {
	today := d.narrationToday()

	// Build the context-snapshot READ path through the same guard the WRITER
	// (snapshotWorkspaceContextOnRemove -> writeRawAtomic -> rawTierFilename) uses,
	// so the read path can never address a different file than the write path and a
	// crafted workspace id is rejected (failing the run) instead of pointing the
	// narrator's "read CONTEXT_SNAPSHOT_PATH first" step at an attacker-chosen file.
	snapshotName, err := rawTierFilename(workspaceID)
	if err != nil {
		return narrateWorkspacePromptInputs{}, fmt.Errorf("narrate_workspace: unsafe workspace id: %w", err)
	}
	// Per-workspace digest bucket — the narrator reads ONLY this workspace's member
	// digests, not a flat dir holding every workspace's and every solo session's.
	sessionsDir, err := notebookWorkspaceSessionsDir(root, workspaceID)
	if err != nil {
		return narrateWorkspacePromptInputs{}, fmt.Errorf("narrate_workspace: %w", err)
	}

	inputs := narrateWorkspacePromptInputs{
		WorkspaceID:         workspaceID,
		ContextSnapshotPath: filepath.Join(notebook.RawContextSnapshotsDir(root), snapshotName),
		RawSessionsDir:      sessionsDir,
		RawDispatchesDir:    notebook.RawDispatchesDir(root),
		JournalDir:          filepath.Join(root, notebook.DirJournal),
		JournalPath:         filepath.Join(root, notebook.DirJournal, today+".md"),
	}

	ws := d.store.GetWorkspace(workspaceID)
	inputs.IsRemovalPass = ws == nil
	if ws != nil {
		inputs.WorkspaceTitle = strings.TrimSpace(ws.Title)
	}
	if inputs.WorkspaceTitle == "" {
		inputs.WorkspaceTitle = workspaceID
	}

	// Collect the transcripts of sessions still associated with this workspace. On a
	// removal pass the member rows are gone, so this is typically empty — expected.
	for _, session := range d.store.List("") {
		if session == nil || session.WorkspaceID != workspaceID {
			continue
		}
		if path := strings.TrimSpace(d.resolveTranscriptPathForSession(session, "")); path != "" {
			inputs.TranscriptPaths = append(inputs.TranscriptPaths, path)
		}
	}

	return inputs, nil
}

// narrationToday returns today's date in YYYY-MM-DD for the journal filename. The
// narrationNowOverride test hook pins the clock so date-boundary behavior is
// deterministic.
func (d *Daemon) narrationToday() string {
	now := time.Now
	if d.narrationNowOverride != nil {
		now = d.narrationNowOverride
	}
	return now().Format("2006-01-02")
}

// workspaceNarrationMarker is the FULL hidden HTML-comment marker line the narrator
// writes to delimit (and dedup) this workspace's entry in a day's journal file. It
// MUST match the exact line the prompt brief tells the agent to write
// (`<!-- attn:wsnarr:<wsID> -->`). The full delimited form is load-bearing for the
// success ledger: the bare `attn:wsnarr:ws-1` substring is also contained in
// `<!-- attn:wsnarr:ws-10 -->`, so a prefix-related sibling's entry would falsely
// verify ws-1's run. Matching the delimited line removes that collision.
func workspaceNarrationMarker(workspaceID string) string {
	return fmt.Sprintf("<!-- attn:wsnarr:%s -->", strings.TrimSpace(workspaceID))
}

// workspaceNarrationEntry is the pre/post-run snapshot of a workspace's marker
// block in a day's journal, used as the freshness ledger for a narrate run.
type workspaceNarrationEntry struct {
	present bool   // the workspace's marker line is in the file
	body    string // the entry block from the marker line to the next "## "/EOF
}

// workspaceNarrationBlock reads the journal at path and returns this workspace's
// entry block: whether its marker line is present and, if so, the text from the
// marker line through the line just before the next workspace's "## " header (or
// end of file). A missing file is not an error (the agent simply wrote nothing):
// it reports an absent entry. The body is scoped to the workspace's own marker so
// the freshness check ignores a concurrent sibling's edit elsewhere in the file.
func workspaceNarrationBlock(path, workspaceID string) (workspaceNarrationEntry, error) {
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return workspaceNarrationEntry{}, nil
	}
	if err != nil {
		return workspaceNarrationEntry{}, err
	}
	marker := workspaceNarrationMarker(workspaceID)
	lines := strings.Split(string(content), "\n")
	start := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == marker {
			start = i
			break
		}
	}
	if start < 0 {
		return workspaceNarrationEntry{}, nil
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "## ") {
			end = i
			break
		}
	}
	return workspaceNarrationEntry{present: true, body: strings.Join(lines[start:end], "\n")}, nil
}

// fileFingerprint captures a file's identity for the digest freshness ledger: its
// existence and a content hash. A run that left a digest byte-identical to its
// pre-run state is treated as a no-op (not a success), so a coalesced re-run whose
// agent did nothing is retried rather than recorded as a false done. Content (not
// mtime) so a same-second rewrite of identical size can never read as fresh, and a
// no-op is never missed by coarse filesystem mtime granularity.
type fileFingerprint struct {
	exists bool
	hash   [sha256.Size]byte
}

func (f fileFingerprint) equal(other fileFingerprint) bool {
	return f.exists && other.exists && f.hash == other.hash
}

func fileFingerprintOf(path string) fileFingerprint {
	content, err := os.ReadFile(path)
	if err != nil {
		return fileFingerprint{}
	}
	return fileFingerprint{exists: true, hash: sha256.Sum256(content)}
}

// --- triggers ---

// enqueueSummarizeSession queues a per-session digest run. It is enqueued on every
// session Stop (the cheap tier), coalesced per session so a chatty session does not
// pile up runs. Nil/Disabled-guarded so it is safe before the runner is constructed
// and when the notebook root cannot resolve.
//
// The transcript path and workspace id are STASHED on the task (via Meta) at enqueue
// time, where both the session row and the workspace row still exist. They are
// carried because the debounced run fires AFTER a single-session-workspace teardown
// has deleted both rows: without the carried inputs the executor would resolve an
// empty workspace id and write the digest to the _solo bucket (wrong) or find no row
// at all and no-op, so the removal retrospective's per-workspace dir never sees the
// final session's grounded digest. The transcript file itself survives on disk, so
// summarize remains runnable post-removal. wsID is empty for a genuinely solo
// session, which keeps the digest in the _solo bucket exactly as before.
func (d *Daemon) enqueueSummarizeSession(sessionID, transcriptPath, workspaceID string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	runner := d.compactRunnerRef()
	if runner == nil || runner.Disabled() {
		return
	}
	meta := map[string]string{
		notebookSummarizeMetaTranscript: strings.TrimSpace(transcriptPath),
		notebookSummarizeMetaWorkspace:  strings.TrimSpace(workspaceID),
	}
	if _, err := runner.Enqueue(notebookSummarizeSessionKind, sessionID, tasks.EnqueueOptions{
		Debounce: notebookNarrationDebounce,
		Meta:     meta,
	}); err != nil {
		d.logf("summarize_session: enqueue %s: %v", sessionID, err)
	}
}

// enqueueNarrateWorkspace queues a coalesced curated-journal run for a live
// workspace. The debounce collapses the burst of session stops in an active
// workspace into a single narrate pass. Nil/Disabled-guarded.
func (d *Daemon) enqueueNarrateWorkspace(workspaceID string) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return
	}
	runner := d.compactRunnerRef()
	if runner == nil || runner.Disabled() {
		return
	}
	if _, err := runner.Enqueue(notebookNarrateWorkspaceKind, workspaceID, tasks.EnqueueOptions{
		Debounce: notebookNarrationDebounce,
	}); err != nil {
		d.logf("narrate_workspace: enqueue %s: %v", workspaceID, err)
	}
}

// markNotebookWorkspaceActivity records that a workspace saw real activity (a
// session end or a content-changing context write) since the last daily-narrate
// cron fire. It feeds the daily-narrate activity gate: the cron drains this set and
// only narrates workspaces that appear in it, so idle long-lived workspaces never
// burn a strong-tier pass. The set is in-memory, best-effort, and lazily initialized
// under the mutex (so the Daemon constructor needs no edit); an empty id is ignored.
func (d *Daemon) markNotebookWorkspaceActivity(workspaceID string) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return
	}
	d.notebookNarrateActivityMu.Lock()
	defer d.notebookNarrateActivityMu.Unlock()
	if d.notebookNarrateActivity == nil {
		d.notebookNarrateActivity = make(map[string]struct{})
	}
	d.notebookNarrateActivity[workspaceID] = struct{}{}
}

// enqueueDailyNarrateWorkspace queues the daily-cron per-workspace narrate for a
// live, active workspace. It mirrors enqueueNarrateWorkspace (nil/Disabled guard,
// notebookNarrationDebounce so it coalesces with any concurrent session-end narrate)
// but stamps notebookNarrateMetaDailyPass so the executor's success gate relaxes for
// a no-op daily refresh (see narrateWorkspaceExecutor). Nil/Disabled-guarded.
//
// Known coalescing edge (low severity, self-healing): if a session-end narrate and
// this daily narrate land on the same narrate_workspace:<ws> task within the debounce
// window — i.e. a session stops within notebookNarrationDebounce of the nightly slot —
// the merged record ends up daily-flagged in BOTH enqueue orderings (the runner's Meta
// is REPLACE-on-non-nil / leave-on-nil, so daily's flag either wins by replacing or
// survives because session-end carries no Meta). The coalesced run then takes the
// relaxed gate, so a no-op is marked DONE rather than retried. This only matters if
// the agent ALSO no-ops on a real session-end digest (a transient flake — real work
// always writes), and even then nothing is lost: the raw digest persists and the next
// trigger re-narrates. Closing it fully would require flipping the executor default to
// relaxed (a sticky "strict-wins" marker), trading this rare timing edge for a less
// safe default on the primary session-end path; not worth it.
func (d *Daemon) enqueueDailyNarrateWorkspace(workspaceID string) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return
	}
	runner := d.compactRunnerRef()
	if runner == nil || runner.Disabled() {
		return
	}
	if _, err := runner.Enqueue(notebookNarrateWorkspaceKind, workspaceID, tasks.EnqueueOptions{
		Debounce: notebookNarrationDebounce,
		Meta:     map[string]string{notebookNarrateMetaDailyPass: "1"},
	}); err != nil {
		d.logf("narrate_workspace: enqueue daily %s: %v", workspaceID, err)
	}
}

// enqueueFinalNarrateWorkspace queues the removal-boundary final narrate with a
// zero debounce so it overrides any pending active-day debounce and runs as soon as
// eligible — the workspace is gone, so this is the last chance to write its
// retrospective. It must be called AFTER the context snapshot is taken and the
// workspace row is removed, so the executor derives IS_REMOVAL_PASS=true and the
// snapshot is on disk for the narrator to read. Nil/Disabled-guarded, so the
// startup-reconciliation removal site (which runs before the runner exists) is a
// safe no-op.
func (d *Daemon) enqueueFinalNarrateWorkspace(workspaceID string) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return
	}
	runner := d.compactRunnerRef()
	if runner == nil || runner.Disabled() {
		return
	}
	if _, err := runner.Enqueue(notebookNarrateWorkspaceKind, workspaceID, tasks.EnqueueOptions{
		ZeroDebounce: true,
	}); err != nil {
		d.logf("narrate_workspace: enqueue final %s: %v", workspaceID, err)
	}
}

// resolveStopWorkspaceID returns the workspace id for a stopped session, read from
// the PERSISTED store row (not the in-memory registry). The registry can race a
// concurrent dissociate-on-close, but the persisted workspace_id survives until the
// session row itself is removed, so it is the authoritative trigger source for
// "which workspace did this stop belong to".
func (d *Daemon) resolveStopWorkspaceID(sessionID string) string {
	session := d.store.Get(sessionID)
	if session == nil {
		return ""
	}
	return strings.TrimSpace(session.WorkspaceID)
}
