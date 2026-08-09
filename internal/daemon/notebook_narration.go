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
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/notebook"
)

// headlessScratchCwd is the stable shared cwd for headless narration runs.
// Stable, not per-run temp: Claude spills tool outputs under
// ~/.claude/projects/<cwd-hash>, so unique cwds accumulate orphaned dirs attn
// must never reach in to clean. Safe to share — these tasks use absolute paths.
func headlessScratchCwd() (string, error) {
	dir := filepath.Join(config.DataDir(), "headless-cwd")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// Notebook narration: two headless agent tasks. summarize_session (cheap tier)
// writes a per-session digest to RawSessionsDir/<wsID>/<sessionID>.md;
// narrate_workspace (strong tier, coalesced) writes today's curated journal
// entry. THE FILE IS THE LEDGER — the success gate is "did the agent write the
// target file", and the agents' read-before-write CAS is the concurrency
// control over a journal shared with siblings and the human. Codex's
// apply-patch CAS is UNVERIFIED, so Claude is the built-in default.

const (
	// notebookSummarizeSessionTimeout bounds one per-session digest run.
	notebookSummarizeSessionTimeout = 4 * time.Minute
	// notebookNarrateWorkspaceTimeout bounds one curated-journal run; wider because
	// the narrate pass reads many digests and writes prose on the strong model.
	notebookNarrateWorkspaceTimeout = 8 * time.Minute
	// notebookNarrationDebounce coalesces a burst of session stops into a single
	// pass. The removal-boundary final narrate overrides this with ZeroDebounce.
	notebookNarrationDebounce = 2 * time.Minute
	// notebookSoloSessionBucket holds digests for solo (non-workspace) sessions.
	// Reserved name: it can never collide with a real workspace id bucket.
	notebookSoloSessionBucket = "_solo"
)

// summarizeSessionPayload carries the run's inputs at enqueue time, while the
// session and workspace rows still exist — the debounced run can fire after a
// teardown deleted both. WorkspaceID is a pointer: carried-but-empty is a solo
// session, absent falls back to the live row.
type summarizeSessionPayload struct {
	Transcript  string  `json:"transcript,omitempty"`
	WorkspaceID *string `json:"workspace,omitempty"`
}

// narrateWorkspacePayload marks a job enqueued by the daily-narrate cron, which
// relaxes the success gate so a no-op daily refresh is a clean done. Session-end
// and removal passes carry no flag and keep strict "must have written" gating.
type narrateWorkspacePayload struct {
	DailyPass bool `json:"daily_pass,omitempty"`
}

// notebookNarrationAllowedTools is the native tool set both narration agents
// get. Claude consumes it as --allowedTools; Codex ignores it, so Codex
// writability is governed by ExtraWritableRoots instead.
var notebookNarrationAllowedTools = []string{"Read", "Write", "Edit", "Grep", "Glob", "Bash"}

// --- summarize_session ---

// summarizeSessionHandler runs one per-session digest (job subject: the session
// id). It prefers the carried payload over the live row, which a teardown may
// have deleted, and verifies the digest was (re)written.
func (d *Daemon) summarizeSessionHandler(ctx context.Context, job *jobs.Job) (any, error) {
	// A run queued before the toggle was turned off must not fire; no-op success
	// retires the record.
	if !d.notebookTasksEnabled() || !d.notebookSummariesEnabled() {
		return nil, nil
	}
	sessionID := strings.TrimSpace(jobSubject(job))
	if sessionID == "" {
		return nil, errors.New("summarize_session requires a session id")
	}
	var carried summarizeSessionPayload
	if err := job.DecodePayload(&carried); err != nil {
		return nil, err
	}

	root, err := d.notebookRoot()
	if err != nil {
		return nil, fmt.Errorf("summarize_session: notebook root: %w", err)
	}
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("summarize_session: notebook is disabled")
	}

	config, err := d.notebookNarrationConfigFor(notebookSummarizeSessionKind)
	if err != nil {
		return nil, err
	}
	provider, executablePath, err := d.resolveNotebookNarrationExecutable(config)
	if err != nil {
		return nil, err
	}

	// Prefer the carried inputs (correct after teardown deleted the rows); the
	// live-row fallback covers a manually-enqueued job with no payload.
	carriedTranscript := strings.TrimSpace(carried.Transcript)
	carriedWorkspace := ""
	hasCarriedWorkspace := carried.WorkspaceID != nil
	if hasCarriedWorkspace {
		carriedWorkspace = strings.TrimSpace(*carried.WorkspaceID)
	}

	session := d.store.Get(sessionID)
	if session == nil && carriedTranscript == "" {
		// Genuinely gone with nothing to summarize: no-op success, nothing to retry.
		d.logf("summarize_session: session %s no longer present and no carried transcript, skipping", sessionID)
		return nil, nil
	}

	transcriptPath := carriedTranscript
	if transcriptPath == "" {
		transcriptPath = d.resolveTranscriptPathForSession(session, "")
	}

	workspaceID := carriedWorkspace
	if !hasCarriedWorkspace && session != nil {
		workspaceID = strings.TrimSpace(session.WorkspaceID)
	}

	// Both id segments route through the raw-tier guard: the carried wsID is just
	// as client-controlled as the row's, so a crafted id cannot escape the raw tier
	// and steer the agent's Write at the curated journal.
	digestPath, err := notebookSessionDigestPath(root, workspaceID, sessionID)
	if err != nil {
		return nil, fmt.Errorf("summarize_session: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(digestPath), 0o755); err != nil {
		return nil, fmt.Errorf("summarize_session: create raw sessions dir: %w", err)
	}

	workDir, err := headlessScratchCwd()
	if err != nil {
		return nil, fmt.Errorf("summarize_session: resolve scratch cwd: %w", err)
	}

	// Pre-run fingerprint: a coalesced re-run must not be reported done off the
	// PRIOR run's file, silently leaving a stale digest.
	before := fileFingerprintOf(digestPath)

	request := agentdriver.HeadlessTaskRequest{
		Executable:   executablePath,
		Model:        config.Model,
		Prompt:       buildSummarizeSessionPrompt(transcriptPath, sessionID, digestPath),
		WorkDir:      workDir,
		AllowedTools: notebookNarrationAllowedTools,
		// The digest is outside the scratch WorkDir; Codex needs it writable.
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
		return nil, fmt.Errorf("summarize_session: run agent: %w (%s)", err, result.Diagnostics)
	}

	// The file is the ledger: the digest must exist AND have changed since the
	// pre-run snapshot, whatever the agent claimed.
	after := fileFingerprintOf(digestPath)
	if !after.exists {
		return nil, fmt.Errorf("summarize_session: agent did not write digest %s (%s)", digestPath, result.Diagnostics)
	}
	if before.exists && after.equal(before) {
		return nil, fmt.Errorf("summarize_session: agent left digest %s unchanged (%s)", digestPath, result.Diagnostics)
	}
	d.logf("summarize_session: session=%s agent=%s model=%s digest=%s", sessionID, config.Agent, config.Model, digestPath)

	// On a teardown the zero-debounce removal narrate ran over an EMPTY digest
	// bucket while this summarize was still debounced, so re-enqueue the
	// retrospective now. Loop-safe: narrate completion never enqueues summarize.
	if workspaceID != "" && d.store.GetWorkspace(workspaceID) == nil {
		d.logf("summarize_session: workspace %s removed, re-narrating retrospective with fresh digest", workspaceID)
		d.enqueueFinalNarrateWorkspace(workspaceID)
	}
	return nil, nil
}

// notebookSessionDigestPath builds the absolute path of a session's raw digest:
// RawSessionsDir/<wsID or _solo>/<sessionID>.md. Both id segments route through
// the raw-tier guard, so a crafted id errors instead of climbing out via "..".
func notebookSessionDigestPath(root, workspaceID, sessionID string) (string, error) {
	name, err := rawTierFilename(sessionID)
	if err != nil {
		return "", fmt.Errorf("unsafe session id: %w", err)
	}
	if workspaceID == "" {
		return filepath.Join(notebook.RawSessionsDir(root), notebookSoloSessionBucket, name), nil
	}
	dir, err := notebookWorkspaceSessionsDir(root, workspaceID)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}

// notebookWorkspaceSessionsDir is the per-workspace digest subdir handed to the
// narrate pass as RAW_SESSIONS_DIR; it mirrors notebookSessionDigestPath's bucket.
func notebookWorkspaceSessionsDir(root, workspaceID string) (string, error) {
	bucket, err := rawTierSegment(workspaceID)
	if err != nil {
		return "", fmt.Errorf("unsafe workspace id: %w", err)
	}
	return filepath.Join(notebook.RawSessionsDir(root), bucket), nil
}

// --- narrate_workspace ---

// narrateWorkspaceHandler runs one curated-journal pass; the job subject is the
// workspace id. IS_REMOVAL_PASS is derived at RUN TIME from workspace-row
// absence, and success is the journal carrying today's workspace marker (the
// file is the ledger).
func (d *Daemon) narrateWorkspaceHandler(ctx context.Context, job *jobs.Job) (any, error) {
	// A run queued before the toggle was turned off must not fire; no-op success
	// retires the record.
	if !d.notebookTasksEnabled() || !d.notebookWorkspaceNarrationEnabled() {
		return nil, nil
	}
	workspaceID := strings.TrimSpace(jobSubject(job))
	if workspaceID == "" {
		return nil, errors.New("narrate_workspace requires a workspace id")
	}
	var carried narrateWorkspacePayload
	if err := job.DecodePayload(&carried); err != nil {
		return nil, err
	}

	root, err := d.notebookRoot()
	if err != nil {
		return nil, fmt.Errorf("narrate_workspace: notebook root: %w", err)
	}
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("narrate_workspace: notebook is disabled")
	}

	config, err := d.notebookNarrationConfigFor(notebookNarrateWorkspaceKind)
	if err != nil {
		return nil, err
	}
	provider, executablePath, err := d.resolveNotebookNarrationExecutable(config)
	if err != nil {
		return nil, err
	}

	inputs, err := d.gatherNarrateWorkspaceInputs(root, workspaceID)
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(inputs.JournalDir, 0o755); err != nil {
		return nil, fmt.Errorf("narrate_workspace: create journal dir: %w", err)
	}
	// MkdirAll so the agent's "read every digest" step does not fault when no
	// member ever summarized.
	if err := os.MkdirAll(inputs.RawSessionsDir, 0o755); err != nil {
		return nil, fmt.Errorf("narrate_workspace: create raw sessions dir: %w", err)
	}

	// Pre-run snapshot of the marker block: a coalesced re-run must not be marked
	// done off the PRIOR run's block, silently dropping the removal retrospective.
	before, err := workspaceNarrationBlock(inputs.JournalPath, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("narrate_workspace: read journal: %w", err)
	}

	workDir, err := headlessScratchCwd()
	if err != nil {
		return nil, fmt.Errorf("narrate_workspace: resolve scratch cwd: %w", err)
	}

	request := agentdriver.HeadlessTaskRequest{
		Executable:   executablePath,
		Model:        config.Model,
		Prompt:       buildNarrateWorkspacePrompt(inputs),
		WorkDir:      workDir,
		AllowedTools: notebookNarrationAllowedTools,
		// Widen the Codex sandbox to the whole notebook root so it can write the
		// curated journal (Claude ignores this).
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
		return nil, fmt.Errorf("narrate_workspace: run agent: %w (%s)", err, result.Diagnostics)
	}

	// The file is the ledger: this workspace's entry block must be present AND
	// changed since the pre-run snapshot.
	after, err := workspaceNarrationBlock(inputs.JournalPath, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("narrate_workspace: verify journal: %w", err)
	}

	// dailyPass relaxes the gate for the daily-cron backstop ONLY: a refresh that
	// legitimately finds nothing new would otherwise ride the backoff to dead.
	// Removal and session-end passes keep retry-until-the-digest-lands.
	dailyPass := !inputs.IsRemovalPass && carried.DailyPass

	if !after.present {
		if dailyPass {
			d.logf("narrate_workspace: daily pass for %s found nothing new to narrate (no entry written); clean no-op", workspaceID)
			return nil, nil
		}
		return nil, fmt.Errorf("narrate_workspace: agent did not write %s entry to %s (%s)", workspaceNarrationMarker(workspaceID), inputs.JournalPath, result.Diagnostics)
	}
	if before.present && after.body == before.body {
		if dailyPass {
			d.logf("narrate_workspace: daily pass for %s left the entry unchanged (nothing new to narrate); clean no-op", workspaceID)
			return nil, nil
		}
		return nil, fmt.Errorf("narrate_workspace: agent left %s entry in %s unchanged (%s)", workspaceNarrationMarker(workspaceID), inputs.JournalPath, result.Diagnostics)
	}
	d.logf(
		"narrate_workspace: workspace=%s agent=%s model=%s removal=%t journal=%s",
		workspaceID, config.Agent, config.Model, inputs.IsRemovalPass, inputs.JournalPath,
	)
	return nil, nil
}

// gatherNarrateWorkspaceInputs assembles the absolute-path inputs for the
// narrate agent. TRANSCRIPT_PATHS are best-effort: the digests are the durable
// record and the brief only consults transcripts to chase a divergence.
func (d *Daemon) gatherNarrateWorkspaceInputs(root, workspaceID string) (narrateWorkspacePromptInputs, error) {
	today := d.narrationToday()

	// The READ path uses the same guard as the writer, so it can never address a
	// different file, and a crafted id fails the run rather than pointing the
	// agent at an attacker-chosen file.
	snapshotName, err := rawTierFilename(workspaceID)
	if err != nil {
		return narrateWorkspacePromptInputs{}, fmt.Errorf("narrate_workspace: unsafe workspace id: %w", err)
	}
	sessionsDir, err := notebookWorkspaceSessionsDir(root, workspaceID)
	if err != nil {
		return narrateWorkspacePromptInputs{}, fmt.Errorf("narrate_workspace: %w", err)
	}

	inputs := narrateWorkspacePromptInputs{
		WorkspaceID:         workspaceID,
		ContextSnapshotPath: filepath.Join(notebook.RawContextSnapshotsDir(root), snapshotName),
		RawSessionsDir:      sessionsDir,
		JournalDir:          filepath.Join(root, notebook.DirJournal),
		JournalPath:         filepath.Join(root, notebook.DirJournal, today+".md"),
		KnowledgeDir:        filepath.Join(root, notebook.DirKnowledge),
	}

	ws := d.store.GetWorkspace(workspaceID)
	inputs.IsRemovalPass = ws == nil
	if ws != nil {
		inputs.WorkspaceTitle = strings.TrimSpace(ws.Title)
	}
	if inputs.WorkspaceTitle == "" {
		inputs.WorkspaceTitle = workspaceID
	}

	// On a removal pass the member rows are gone, so this is typically empty.
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

// narrationToday returns today's date in YYYY-MM-DD for the journal filename;
// narrationNowOverride pins the clock in tests.
func (d *Daemon) narrationToday() string {
	now := time.Now
	if d.narrationNowOverride != nil {
		now = d.narrationNowOverride
	}
	return now().Format("2006-01-02")
}

// workspaceNarrationMarker MUST match the exact line the prompt brief tells the
// agent to write. The full delimited form is load-bearing: bare `attn:wsnarr:ws-1`
// is a substring of `<!-- attn:wsnarr:ws-10 -->`, so a sibling would falsely verify.
func workspaceNarrationMarker(workspaceID string) string {
	return fmt.Sprintf("<!-- attn:wsnarr:%s -->", strings.TrimSpace(workspaceID))
}

// workspaceNarrationEntry is the pre/post-run snapshot of a workspace's marker
// block in a day's journal, used as the freshness ledger for a narrate run.
type workspaceNarrationEntry struct {
	present bool   // the workspace's marker line is in the file
	body    string // the entry block from the marker line to the next "## "/EOF
}

// workspaceNarrationBlock returns this workspace's entry block from the journal
// at path. A missing file reports an absent entry, not an error. The body is
// scoped to the workspace's own marker so the freshness check ignores a
// concurrent sibling's edit elsewhere in the file.
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

// fileFingerprint is the digest freshness ledger: existence plus a content hash.
// Content, not mtime, so a no-op is never missed by coarse mtime granularity.
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

// enqueueSummarizeSession queues a per-session digest run on session Stop,
// coalesced per session. The transcript path and workspace id are stashed on
// the payload here, while both rows still exist — see summarizeSessionPayload.
func (d *Daemon) enqueueSummarizeSession(sessionID, transcriptPath, workspaceID string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	if !d.notebookTasksEnabled() || !d.notebookSummariesEnabled() {
		return
	}
	runner := d.jobQueueRef()
	if runner == nil || runner.Disabled() {
		return
	}
	carriedWorkspace := strings.TrimSpace(workspaceID)
	if _, err := runner.Enqueue(notebookSummarizeSessionKind, jobs.EnqueueOptions{
		UniqueKey: sessionID,
		Delay:     notebookNarrationDebounce,
		Payload: summarizeSessionPayload{
			Transcript:  strings.TrimSpace(transcriptPath),
			WorkspaceID: &carriedWorkspace,
		},
	}); err != nil {
		d.logf("summarize_session: enqueue %s: %v", sessionID, err)
	}
}

// enqueueNarrateWorkspace queues a coalesced curated-journal run for a live
// workspace.
func (d *Daemon) enqueueNarrateWorkspace(workspaceID string) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return
	}
	if !d.notebookTasksEnabled() || !d.notebookWorkspaceNarrationEnabled() {
		return
	}
	runner := d.jobQueueRef()
	if runner == nil || runner.Disabled() {
		return
	}
	if _, err := runner.Enqueue(notebookNarrateWorkspaceKind, jobs.EnqueueOptions{
		UniqueKey: workspaceID,
		Delay:     notebookNarrationDebounce,
	}); err != nil {
		d.logf("narrate_workspace: enqueue %s: %v", workspaceID, err)
	}
}

// markNotebookWorkspaceActivity records that a workspace saw real activity (a
// session end or a content-changing context write) since the last daily-narrate
// fire, feeding the cron's activity gate. In-memory and best-effort.
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

// enqueueDailyNarrateWorkspace queues the daily-cron narrate, stamped DailyPass
// so the executor's success gate relaxes for a no-op refresh. Known self-healing
// edge: a session-end narrate coalescing into this window inherits the daily
// flag either ordering, so its no-op is marked done rather than retried.
func (d *Daemon) enqueueDailyNarrateWorkspace(workspaceID string) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return
	}
	if !d.notebookTasksEnabled() || !d.notebookWorkspaceNarrationEnabled() {
		return
	}
	runner := d.jobQueueRef()
	if runner == nil || runner.Disabled() {
		return
	}
	if _, err := runner.Enqueue(notebookNarrateWorkspaceKind, jobs.EnqueueOptions{
		UniqueKey: workspaceID,
		Delay:     notebookNarrationDebounce,
		Payload:   narrateWorkspacePayload{DailyPass: true},
	}); err != nil {
		d.logf("narrate_workspace: enqueue daily %s: %v", workspaceID, err)
	}
}

// enqueueFinalNarrateWorkspace queues the removal-boundary final narrate with
// zero debounce. MUST run AFTER the context snapshot and the workspace-row
// removal, so IS_REMOVAL_PASS derives true and the snapshot is on disk. A no-op
// before the runner exists, so the startup reaper defers its enqueue to Start.
func (d *Daemon) enqueueFinalNarrateWorkspace(workspaceID string) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return
	}
	if !d.notebookTasksEnabled() || !d.notebookWorkspaceNarrationEnabled() {
		return
	}
	runner := d.jobQueueRef()
	if runner == nil || runner.Disabled() {
		return
	}
	if _, err := runner.Enqueue(notebookNarrateWorkspaceKind, jobs.EnqueueOptions{
		UniqueKey: workspaceID,
		RunNow:    true,
	}); err != nil {
		d.logf("narrate_workspace: enqueue final %s: %v", workspaceID, err)
	}
}

// resolveStopWorkspaceID reads the stopped session's workspace id from the
// PERSISTED row, not the in-memory registry, which can race a concurrent
// dissociate-on-close.
func (d *Daemon) resolveStopWorkspaceID(sessionID string) string {
	session := d.store.Get(sessionID)
	if session == nil {
		return ""
	}
	return strings.TrimSpace(session.WorkspaceID)
}
