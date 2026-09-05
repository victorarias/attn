package daemon

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"syscall"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/garden"
	attngit "github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

const (
	branchStateLocal      = "local"
	branchStateRemoteOnly = "remote_only"
	branchStateGone       = "gone"
	branchStateMerged     = "merged"
	branchStateUnknown    = "unknown"

	reopenPlaceReuse  = "reuse"
	reopenPlaceCreate = "create"
	reopenPlaceAdd    = "add"
)

// What one inspect_branch saw about a saved branch in its repository. Every
// field here costs a git command, which is why it is observed off the request path.
type branchInspection struct {
	State             string
	Remote            string
	AlreadyCheckedOut bool
	RepoMissing       bool
	// The saved worktree is still registered though its directory is gone; git
	// refuses to recreate it until a prune clears the registration.
	StaleRegistration bool
}

// The verdict for one session in the ledger: whether it comes back, what it
// would take, and why not when it does not.
type sessionReopenVerdict struct {
	SessionID      string
	Entry          *protocol.SessionLedgerEntry
	Execution      garden.Dispatch
	Live           bool
	Reopenable     bool
	Reason         string
	Warning        string
	Actions        []protocol.SessionReopenAction
	Checking       bool
	DirectoryState string
	BranchState    string
	WorkspaceID    string
	WorkspacePlan  string
	PanePlan       string
	// Where an offered worktree action would put the checkout back.
	RecreatePath string
}

func (v *sessionReopenVerdict) offers(action protocol.SessionReopenAction) bool {
	for _, offered := range v.Actions {
		if offered == action {
			return true
		}
	}
	return false
}

func (v *sessionReopenVerdict) toProtocol() *protocol.SessionReopen {
	out := &protocol.SessionReopen{
		Reopenable:     v.Reopenable,
		Actions:        v.Actions,
		Checking:       v.Checking,
		DirectoryState: v.DirectoryState,
		WorkspaceID:    v.WorkspaceID,
		WorkspacePlan:  v.WorkspacePlan,
		PanePlan:       v.PanePlan,
	}
	if out.Actions == nil {
		out.Actions = []protocol.SessionReopenAction{}
	}
	if v.Reason != "" {
		out.Reason = protocol.Ptr(v.Reason)
	}
	if v.Warning != "" {
		out.Warning = protocol.Ptr(v.Warning)
	}
	if v.BranchState != "" {
		out.BranchState = protocol.Ptr(v.BranchState)
	}
	return out
}

// The saved execution, richest source first: the dispatch document knows what git
// discovery found, the ledger row knows what the session itself reported.
func (d *Daemon) reopenExecution(entry *protocol.SessionLedgerEntry) garden.Dispatch {
	execution, _ := d.gardenDispatch(entry.ID)
	execution.SessionID = entry.ID
	if execution.Cwd == "" {
		execution.Cwd = entry.Directory
	}
	if execution.Agent == "" {
		execution.Agent = entry.Agent
	}
	if execution.Branch == "" {
		execution.Branch = protocol.Deref(entry.Branch)
	}
	if execution.RepositoryRoot == "" {
		execution.RepositoryRoot = protocol.Deref(entry.MainRepo)
	}
	if execution.HostKind == "" {
		execution.HostKind = garden.HostLocal
	}
	if execution.Resume == "" {
		execution.Resume = d.store.GetResumeSessionID(entry.ID)
	}
	return execution
}

// reopenVerdict answers from stored state alone. A branch it has not inspected
// yet leaves the verdict `checking` and starts one tracked inspect_branch.
func (d *Daemon) reopenVerdict(sessionID string) (*sessionReopenVerdict, bool) {
	sessionID = strings.TrimSpace(sessionID)
	entry := d.store.SessionLedgerEntry(sessionID)
	if entry == nil {
		return nil, false
	}

	verdict := &sessionReopenVerdict{
		SessionID: sessionID,
		Entry:     entry,
		Execution: d.reopenExecution(entry),
		Live:      protocol.Deref(entry.ClosedAt) == "",
	}
	verdict.DirectoryState = inspectContinuationDirectory(verdict.Execution)
	d.planReopenPlacement(verdict)

	if verdict.Live {
		verdict.Reason = fmt.Sprintf("session %s is running; focus it instead of reopening it", sessionID)
		return verdict, true
	}
	if !decideReopenHost(verdict, d.endpointInfos()) {
		return verdict, true
	}
	d.decideReopenPlace(verdict)
	return verdict, true
}

// A reopen lands in the workspace the session ran in when it is still there,
// and in one named after the session when it is not.
func (d *Daemon) planReopenPlacement(verdict *sessionReopenVerdict) {
	workspaceID := strings.TrimSpace(verdict.Entry.WorkspaceID)
	if workspaceID != "" && d.store.GetWorkspace(workspaceID) != nil {
		verdict.WorkspaceID = workspaceID
		verdict.WorkspacePlan = reopenPlaceReuse
	} else {
		verdict.WorkspaceID = reopenWorkspaceID(verdict.SessionID)
		verdict.WorkspacePlan = reopenPlaceCreate
		if d.store.GetWorkspace(verdict.WorkspaceID) != nil {
			verdict.WorkspacePlan = reopenPlaceReuse
		}
	}
	verdict.PanePlan = reopenPlaceAdd
	if d.workspaceLayoutHasSessionPane(verdict.WorkspaceID, verdict.SessionID) {
		verdict.PanePlan = reopenPlaceReuse
	}
}

func reopenWorkspaceID(sessionID string) string {
	return "workspace-" + sessionID
}

func (d *Daemon) workspaceLayoutHasSessionPane(workspaceID, sessionID string) bool {
	holder, paneID, ok := d.store.FindWorkspaceLayoutPaneBySessionID(sessionID)
	return ok && paneID != "" && holder == workspaceID
}

// An outpost's ledger row lives there, so this daemon names the host to go to.
// Reports whether the verdict can go on being decided here.
func decideReopenHost(verdict *sessionReopenVerdict, endpoints []protocol.EndpointInfo) bool {
	if strings.TrimSpace(verdict.Execution.HostKind) != garden.HostRemote {
		return true
	}
	name, reachable := endpointReachable(endpoints, strings.TrimSpace(verdict.Execution.EndpointID))
	if reachable {
		verdict.Reason = fmt.Sprintf(
			"session %s ran on %s; its ledger row lives on that daemon, so reopen it there",
			verdict.SessionID, name)
		return false
	}
	verdict.Reason = fmt.Sprintf(
		"session %s ran on %s, which is not reachable now; retry when it is",
		verdict.SessionID, name)
	return false
}

func endpointReachable(endpoints []protocol.EndpointInfo, endpointID string) (string, bool) {
	name := endpointID
	if name == "" {
		name = "another host"
	}
	for _, endpoint := range endpoints {
		if endpoint.ID != endpointID {
			continue
		}
		if named := strings.TrimSpace(endpoint.Name); named != "" {
			name = named
		}
		return name, endpoint.Status == "connected"
	}
	return name, false
}

func (d *Daemon) endpointInfos() []protocol.EndpointInfo {
	if d.hubManager == nil {
		return nil
	}
	return d.hubManager.List()
}

// The conversation and directory rows of the state map, in that order: without a
// conversation only a fresh start is left, and where depends on the directory.
func (d *Daemon) decideReopenPlace(verdict *sessionReopenVerdict) {
	conversation, conversationReason := d.reopenConversation(verdict.Execution)

	switch verdict.DirectoryState {
	case directoryPresent:
		if !conversation {
			verdict.Reason = conversationReason
			verdict.Actions = []protocol.SessionReopenAction{protocol.SessionReopenActionStartFreshSamePlace}
			return
		}
		verdict.Reopenable = true
		verdict.Actions = []protocol.SessionReopenAction{protocol.SessionReopenActionReopen}
		verdict.Warning = d.reopenBranchWarning(verdict.Execution)
		return
	case directoryMissing:
		d.decideMissingDirectory(verdict, conversation, conversationReason)
		return
	case directoryUnknown:
		verdict.Reason = fmt.Sprintf("session %s saved no directory to reopen in", verdict.SessionID)
		verdict.Actions = []protocol.SessionReopenAction{protocol.SessionReopenActionStartFreshElsewhere}
		return
	default:
		verdict.Reason = fmt.Sprintf("the directory %s cannot be opened", verdict.Execution.Cwd)
		return
	}
}

func (d *Daemon) decideMissingDirectory(verdict *sessionReopenVerdict, conversation bool, conversationReason string) {
	gone := fmt.Sprintf("the directory %s no longer exists", verdict.Execution.Cwd)
	repo := strings.TrimSpace(verdict.Execution.RepositoryRoot)
	branch := strings.TrimSpace(verdict.Execution.Branch)
	target, targetOK := savedWorktreeRoot(verdict.Execution)

	if repo == "" || branch == "" || !targetOK {
		verdict.Reason = gone + ", and it was not a worktree attn can put back"
		verdict.Actions = []protocol.SessionReopenAction{protocol.SessionReopenActionStartFreshElsewhere}
		return
	}
	verdict.RecreatePath = target

	inspection, known := d.branchInspection(verdict.SessionID, repo, branch)
	if !known {
		verdict.Checking = true
		verdict.BranchState = branchStateUnknown
		verdict.Reason = gone + "; checking whether its branch can be put back"
		verdict.Actions = []protocol.SessionReopenAction{protocol.SessionReopenActionStartFreshElsewhere}
		return
	}
	if inspection.RepoMissing {
		verdict.Reason = fmt.Sprintf("%s, and its repository %s is gone too", gone, repo)
		return
	}
	verdict.BranchState = inspection.State

	if !conversation {
		verdict.Reason = conversationReason + ", and " + gone
		verdict.Actions = d.freshActionsForMissingDirectory(verdict, inspection)
		return
	}
	if inspection.AlreadyCheckedOut {
		verdict.Reason = fmt.Sprintf("%s, and branch %s is checked out somewhere else already", gone, branch)
		verdict.Actions = []protocol.SessionReopenAction{protocol.SessionReopenActionStartFreshElsewhere}
		return
	}

	switch inspection.State {
	case branchStateLocal:
		verdict.Reason = fmt.Sprintf("%s; branch %s is still here, so the worktree can be put back", gone, branch)
		verdict.Actions = []protocol.SessionReopenAction{protocol.SessionReopenActionRecreateWorktreeAndReopen}
	case branchStateRemoteOnly:
		verdict.Reason = fmt.Sprintf("%s; branch %s is only on %s, so it has to be fetched first",
			gone, branch, inspection.Remote)
		verdict.Actions = []protocol.SessionReopenAction{protocol.SessionReopenActionFetchRecreateAndReopen}
	default:
		verdict.BranchState, verdict.Reason = d.goneBranchVerdict(verdict, gone, branch)
		verdict.Actions = []protocol.SessionReopenAction{
			protocol.SessionReopenActionStartFreshDefaultBranch,
			protocol.SessionReopenActionStartFreshElsewhere,
		}
	}
}

// A conversation that cannot be resumed leaves only fresh starts, and a
// worktree action would promise a resume it cannot deliver.
func (d *Daemon) freshActionsForMissingDirectory(
	verdict *sessionReopenVerdict, inspection branchInspection,
) []protocol.SessionReopenAction {
	if inspection.State == branchStateGone && !inspection.AlreadyCheckedOut {
		verdict.BranchState, _ = d.goneBranchVerdict(verdict, "", strings.TrimSpace(verdict.Execution.Branch))
		return []protocol.SessionReopenAction{
			protocol.SessionReopenActionStartFreshDefaultBranch,
			protocol.SessionReopenActionStartFreshElsewhere,
		}
	}
	return []protocol.SessionReopenAction{protocol.SessionReopenActionStartFreshElsewhere}
}

// A branch nobody carries any more reads differently when the work reached the
// default branch: the same offer, labeled so the caller knows nothing was lost.
func (d *Daemon) goneBranchVerdict(verdict *sessionReopenVerdict, gone, branch string) (string, string) {
	merged := d.branchMerged(verdict.SessionID, branch)
	state := branchStateGone
	tail := fmt.Sprintf("branch %s is gone from this repository and its remotes", branch)
	if merged {
		state = branchStateMerged
		tail = fmt.Sprintf("branch %s was merged and is gone from this repository and its remotes", branch)
	}
	if gone == "" {
		return state, tail
	}
	return state, gone + "; " + tail
}

// The first rung of the merged ladder from the spike: what the session's own
// pull request record already says. Ancestry checks belong to the worktree sweep.
func (d *Daemon) branchMerged(sessionID, branch string) bool {
	if branch == "" {
		return false
	}
	for _, record := range d.store.ListSessionPullRequests(sessionID) {
		if strings.TrimSpace(record.HeadBranch) == branch && strings.EqualFold(record.State, "merged") {
			return true
		}
	}
	return false
}

// Reports whether the saved conversation can be resumed, and why not when it cannot.
func (d *Daemon) reopenConversation(execution garden.Dispatch) (bool, string) {
	resumeID := strings.TrimSpace(execution.Resume)
	agentName := strings.TrimSpace(execution.Agent)
	if resumeID == "" {
		return false, "no conversation id was saved for this session, so there is nothing to resume"
	}
	driver := agentdriver.Get(agentName)
	if driver == nil {
		return false, fmt.Sprintf("agent %q is not installed here, so its conversation cannot be resumed", agentName)
	}
	if !agentdriver.ResumeAvailable(driver, resumeID) {
		return false, fmt.Sprintf("conversation %s is no longer in %s's storage", resumeID, agentName)
	}
	return true, ""
}

// A directory that outlived its session may be on another branch by now. Reading
// the checked-out branch is a local git call on a path that already exists.
func (d *Daemon) reopenBranchWarning(execution garden.Dispatch) string {
	saved := strings.TrimSpace(execution.Branch)
	if saved == "" {
		return ""
	}
	info, err := attngit.GetBranchInfo(execution.Cwd)
	if err != nil || info == nil {
		return ""
	}
	if current := strings.TrimSpace(info.Branch); current != "" && current != saved {
		return fmt.Sprintf("%s is on branch %s now; the session ran on %s", execution.Cwd, current, saved)
	}
	return ""
}

// branchInspection serves the last observation and starts a new one when there
// is none. The request never waits on git.
func (d *Daemon) branchInspection(sessionID, repo, branch string) (branchInspection, bool) {
	key := branchInspectionKey(repo, branch)
	d.branchInspectionsMu.Lock()
	inspection, known := d.branchInspections[key]
	d.branchInspectionsMu.Unlock()
	if !known {
		d.inspectBranchInBackground(sessionID, repo, branch)
	}
	return inspection, known
}

func branchInspectionKey(repo, branch string) string {
	return attngit.CanonicalizePath(repo) + "\x00" + strings.TrimSpace(branch)
}

// inspectBranchInBackground runs at most one inspect_branch per repository and
// branch at a time; the returned channel closes when that one lands.
func (d *Daemon) inspectBranchInBackground(sessionID, repo, branch string) <-chan struct{} {
	key := branchInspectionKey(repo, branch)
	d.branchInspectionsMu.Lock()
	if d.branchInspections == nil {
		d.branchInspections = make(map[string]branchInspection)
	}
	if d.branchInspectionsRunning == nil {
		d.branchInspectionsRunning = make(map[string]chan struct{})
	}
	if running := d.branchInspectionsRunning[key]; running != nil {
		d.branchInspectionsMu.Unlock()
		return running
	}
	done := make(chan struct{})
	d.branchInspectionsRunning[key] = done
	d.branchInspectionsMu.Unlock()

	go func() {
		defer func() {
			d.branchInspectionsMu.Lock()
			delete(d.branchInspectionsRunning, key)
			d.branchInspectionsMu.Unlock()
			close(done)
		}()
		finishOperation := d.beginGitOperation(protocol.GitOperationKindInspectBranch, repo, nil)
		inspection, err := inspectBranch(repo, branch)
		finishOperation(err)
		if err != nil {
			d.logf("reopen: inspecting branch %s in %s: %v", branch, repo, err)
			return
		}

		d.branchInspectionsMu.Lock()
		d.branchInspections[key] = inspection
		d.branchInspectionsMu.Unlock()

		if verdict, found := d.reopenVerdict(sessionID); found {
			d.publishFact(FactSessionReopenRefreshed, sessionID, verdict.toProtocol())
		}
	}()
	return done
}

// A verdict is served from the last inspection, which a fetch or a branch created
// outside attn can outdate; asking refreshes it for the next ask.
func (d *Daemon) refreshReopenBranch(verdict *sessionReopenVerdict) {
	if verdict.BranchState == "" || verdict.BranchState == branchStateUnknown {
		return
	}
	d.inspectBranchInBackground(verdict.SessionID, verdict.Execution.RepositoryRoot, verdict.Execution.Branch)
}

// forgetBranchInspections drops what a repository write just made stale.
func (d *Daemon) forgetBranchInspections(repo string) {
	prefix := attngit.CanonicalizePath(repo) + "\x00"
	d.branchInspectionsMu.Lock()
	defer d.branchInspectionsMu.Unlock()
	for key := range d.branchInspections {
		if strings.HasPrefix(key, prefix) {
			delete(d.branchInspections, key)
		}
	}
}

func inspectBranch(repo, branch string) (branchInspection, error) {
	if _, err := os.Stat(repo); err != nil {
		return branchInspection{State: branchStateGone, RepoMissing: true}, nil
	}
	inspection := branchInspection{State: branchStateGone}
	if attngit.RefExists(repo, branch) {
		inspection.State = branchStateLocal
	} else {
		remotes, err := attngit.ListRemotes(repo)
		if err != nil {
			return branchInspection{}, fmt.Errorf("read remotes of %s: %w", repo, err)
		}
		for _, remote := range remotes {
			if attngit.RefExists(repo, remote+"/"+branch) {
				inspection.State = branchStateRemoteOnly
				inspection.Remote = remote
				break
			}
		}
	}
	// Observe rather than list: listing prunes, and deciding a verdict must not
	// write to the repository.
	worktrees, err := attngit.ObserveWorktrees(repo)
	if err != nil {
		return branchInspection{}, fmt.Errorf("read worktrees of %s: %w", repo, err)
	}
	for _, worktree := range worktrees {
		if strings.TrimSpace(worktree.Branch) != branch {
			continue
		}
		if worktree.Prunable {
			inspection.StaleRegistration = true
			continue
		}
		inspection.AlreadyCheckedOut = true
	}
	return inspection, nil
}

// ---------------------------------------------------------------------------
// Performing a reopen

type sessionReopenOutcome struct {
	SessionID       string
	WorkspaceID     string
	Directory       string
	Action          protocol.SessionReopenAction
	AlreadyRunning  bool
	WorktreeCreated string
}

// reopenSession is the door for the session ledger. Garden Resume runs the same
// spawn through reopenSessionRuntime with its own seed bookkeeping.
func (d *Daemon) reopenSession(
	sessionID string, action protocol.SessionReopenAction, directory string,
) (*sessionReopenOutcome, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}
	verdict, found := d.reopenVerdict(sessionID)
	if !found {
		return nil, fmt.Errorf(
			"no ledger row for session %s on this daemon; a session that ran before the ledger, or on another "+
				"daemon, is not here. Its seed may still hand the work over from its saved dispatch", sessionID)
	}
	if verdict.Live {
		return &sessionReopenOutcome{
			SessionID:      sessionID,
			WorkspaceID:    verdict.Entry.WorkspaceID,
			Directory:      verdict.Entry.Directory,
			Action:         protocol.SessionReopenActionReopen,
			AlreadyRunning: true,
		}, nil
	}
	if action == "" {
		action = protocol.SessionReopenActionReopen
	}
	// A branch check still in flight decides which worktree action is offered, so an
	// explicit ask waits for it rather than being refused by a verdict that is not final.
	if verdict.Checking && !verdict.offers(action) {
		<-d.inspectBranchInBackground(sessionID, verdict.Execution.RepositoryRoot, verdict.Execution.Branch)
		verdict, found = d.reopenVerdict(sessionID)
		if !found {
			return nil, fmt.Errorf("session %s left the ledger while its branch was being checked", sessionID)
		}
	}
	if !verdict.offers(action) {
		return nil, reopenRefusal(verdict, action)
	}
	return d.performReopen(verdict, action, directory)
}

func reopenRefusal(verdict *sessionReopenVerdict, action protocol.SessionReopenAction) error {
	reason := strings.TrimSpace(verdict.Reason)
	if reason == "" {
		reason = "the saved execution does not allow it"
	}
	if len(verdict.Actions) == 0 {
		return fmt.Errorf("%s cannot be reopened: %s", verdict.SessionID, reason)
	}
	offered := make([]string, 0, len(verdict.Actions))
	for _, offer := range verdict.Actions {
		offered = append(offered, string(offer))
	}
	return fmt.Errorf("%s cannot be reopened with %s: %s. Offered instead: %s",
		verdict.SessionID, action, reason, strings.Join(offered, ", "))
}

func (d *Daemon) performReopen(
	verdict *sessionReopenVerdict, action protocol.SessionReopenAction, directory string,
) (*sessionReopenOutcome, error) {
	plan := sessionReopenPlan{
		SessionID:   verdict.SessionID,
		Directory:   verdict.Execution.Cwd,
		Agent:       strings.TrimSpace(verdict.Execution.Agent),
		ResumeID:    strings.TrimSpace(verdict.Execution.Resume),
		Title:       verdict.Entry.Label,
		WorkspaceID: verdict.WorkspaceID,
	}
	rollback := d.newDelegationRollback()
	created := ""

	switch action {
	case protocol.SessionReopenActionReopen:
	case protocol.SessionReopenActionStartFreshSamePlace:
		plan.ResumeID, plan.FreshConversation = "", true
	case protocol.SessionReopenActionStartFreshElsewhere:
		plan.ResumeID, plan.FreshConversation = "", true
		if strings.TrimSpace(directory) == "" {
			return nil, fmt.Errorf("start_fresh_elsewhere needs a directory to start in; pass --cwd <path>")
		}
		chosen, err := validateDelegationDirectory(strings.TrimSpace(directory))
		if err != nil {
			return nil, fmt.Errorf("start_fresh_elsewhere needs a directory to start in: %w", err)
		}
		plan.Directory = chosen
	case protocol.SessionReopenActionRecreateWorktreeAndReopen,
		protocol.SessionReopenActionFetchRecreateAndReopen,
		protocol.SessionReopenActionStartFreshDefaultBranch:
		if action == protocol.SessionReopenActionStartFreshDefaultBranch {
			plan.ResumeID, plan.FreshConversation = "", true
		}
		path, err := d.recreateReopenWorktree(verdict, action)
		if err != nil {
			return nil, err
		}
		created = path
		rollback.onWorktreeCreated(path)
		plan.Directory = reopenDirectoryInsideWorktree(path, verdict.Execution)
	default:
		return nil, fmt.Errorf("%q is not a reopen action", action)
	}

	outcome, err := d.reopenSessionRuntime(plan, rollback, nil)
	if err != nil {
		return nil, err
	}
	return &sessionReopenOutcome{
		SessionID:       outcome.SessionID,
		WorkspaceID:     outcome.WorkspaceID,
		Directory:       plan.Directory,
		Action:          action,
		WorktreeCreated: created,
	}, nil
}

// The session may have run in a subdirectory of its worktree; the recreated
// checkout puts that path back too.
func reopenDirectoryInsideWorktree(worktree string, execution garden.Dispatch) string {
	subdir := strings.TrimSpace(execution.RepositorySubdir)
	if subdir == "" || subdir == "." {
		return worktree
	}
	candidate := attngit.CanonicalizePath(worktree + string(os.PathSeparator) + subdir)
	if info, err := os.Stat(candidate); err == nil && info.IsDir() {
		return candidate
	}
	return worktree
}

// The only repository write in reopen, and it happens because an action named it.
func (d *Daemon) recreateReopenWorktree(
	verdict *sessionReopenVerdict, action protocol.SessionReopenAction,
) (string, error) {
	repo := strings.TrimSpace(verdict.Execution.RepositoryRoot)
	branch := strings.TrimSpace(verdict.Execution.Branch)
	path := verdict.RecreatePath
	defer d.forgetBranchInspections(repo)

	// The saved registration outlives a directory somebody deleted, and git refuses
	// to put the worktree back until it goes. Only an explicit action gets here.
	if inspection, known := d.branchInspection(verdict.SessionID, repo, branch); known && inspection.StaleRegistration {
		if err := attngit.PruneWorktrees(repo); err != nil {
			return "", fmt.Errorf("clear the stale worktree registration in %s: %w", repo, err)
		}
	}

	switch action {
	case protocol.SessionReopenActionRecreateWorktreeAndReopen:
		return d.doCreateWorktreeFromBranch(&protocol.CreateWorktreeFromBranchMessage{
			Cmd: protocol.CmdCreateWorktreeFromBranch, MainRepo: repo, Branch: branch, Path: protocol.Ptr(path),
		})
	case protocol.SessionReopenActionFetchRecreateAndReopen:
		inspection, _ := d.branchInspection(verdict.SessionID, repo, branch)
		if inspection.Remote == "" {
			return "", fmt.Errorf("no remote carries branch %s any more", branch)
		}
		return d.doCreateWorktreeFromBranch(&protocol.CreateWorktreeFromBranchMessage{
			Cmd:      protocol.CmdCreateWorktreeFromBranch,
			MainRepo: repo,
			Branch:   inspection.Remote + "/" + branch,
			Path:     protocol.Ptr(path),
		})
	default:
		base, err := attngit.GetDefaultBranch(repo)
		if err != nil || strings.TrimSpace(base) == "" {
			return "", fmt.Errorf("%s has no default branch to start from: %w", repo, err)
		}
		return d.doCreateWorktree(&protocol.CreateWorktreeMessage{
			Cmd:          protocol.CmdCreateWorktree,
			MainRepo:     repo,
			Branch:       branch,
			Path:         protocol.Ptr(path),
			StartingFrom: protocol.Ptr(base),
		})
	}
}

// ---------------------------------------------------------------------------
// The shared runtime path: Garden Resume and session reopen both run this.

type sessionReopenPlan struct {
	SessionID   string
	Directory   string
	Agent       string
	ResumeID    string
	Title       string
	WorkspaceID string
	// Start the agent without the conversation the closed run was bound to.
	FreshConversation bool
}

type sessionRuntimeReopened struct {
	SessionID   string
	WorkspaceID string
}

// Brings one session id back: lifts the ledger close, puts the workspace and pane
// back, spawns. Every step undoes itself on a later failure, the close included.
func (d *Daemon) reopenSessionRuntime(
	plan sessionReopenPlan,
	rollback *delegationRollback,
	afterSpawn func() error,
) (*sessionRuntimeReopened, error) {
	directory, err := validateDelegationDirectory(plan.Directory)
	if err != nil {
		return nil, rollback.fail(err)
	}
	agent := strings.TrimSpace(plan.Agent)
	if agent == "" {
		return nil, rollback.fail(fmt.Errorf("session %s saved no agent to start", plan.SessionID))
	}
	workspaceID := strings.TrimSpace(plan.WorkspaceID)
	if workspaceID == "" {
		workspaceID = reopenWorkspaceID(plan.SessionID)
	}

	d.waitForSessionTeardown(plan.SessionID)
	d.store.ClearSessionIntentionalClose(plan.SessionID)

	// The store refuses a spawn that would re-register a closed row, so the close
	// comes off first and goes back on if anything below fails.
	priorClose := d.sessionCloseAttribution(plan.SessionID)
	reopened, err := d.store.ReopenSession(plan.SessionID)
	if err != nil {
		return nil, rollback.fail(err)
	}
	if reopened {
		rollback.onSessionReopened(plan.SessionID, priorClose)
	}

	// The spawn falls back to the binding the closed run left behind, so starting
	// fresh means forgetting it here rather than leaving the resume out of the spawn.
	if plan.FreshConversation {
		prior := d.store.GetSessionConversation(plan.SessionID)
		d.store.SetResumeSessionID(plan.SessionID, "")
		d.forgetDispatchResume(plan.SessionID)
		rollback.onConversationForgotten(plan.SessionID, prior)
	}

	// Unregister on rollback only if this call created the workspace — a re-register is
	// idempotent and preserves a stored rename (handleRegisterWorkspace's title guard).
	if d.store.GetWorkspace(workspaceID) == nil {
		d.handleRegisterWorkspace(nil, &protocol.RegisterWorkspaceMessage{
			Cmd:       protocol.CmdRegisterWorkspace,
			ID:        workspaceID,
			Title:     plan.Title,
			Directory: directory,
		})
		if d.store.GetWorkspace(workspaceID) == nil {
			return nil, rollback.fail(fmt.Errorf("create reopen workspace"))
		}
		rollback.onWorkspaceCreated(workspaceID)
	}

	paneClient := newInternalWSClient()
	d.handleWorkspaceLayoutAddSessionPane(paneClient, &protocol.WorkspaceLayoutAddSessionPaneMessage{
		Cmd:         protocol.CmdWorkspaceLayoutAddSessionPane,
		WorkspaceID: workspaceID,
		PaneID:      protocol.Ptr("pane-" + plan.SessionID),
		SessionID:   plan.SessionID,
		Title:       protocol.Ptr(plan.Title),
	})
	if _, err := readInternalActionResult(paneClient); err != nil {
		return nil, rollback.fail(fmt.Errorf("create reopen pane: %w", err))
	}
	rollback.onPaneCreated(plan.SessionID)

	spawn := &protocol.SpawnSessionMessage{
		Cmd:         protocol.CmdSpawnSession,
		ID:          plan.SessionID,
		Cwd:         directory,
		WorkspaceID: workspaceID,
		Agent:       agent,
		Cols:        80,
		Rows:        24,
		Label:       protocol.Ptr(plan.Title),
	}
	if resumeID := strings.TrimSpace(plan.ResumeID); resumeID != "" {
		spawn.ResumeSessionID = protocol.Ptr(resumeID)
	}
	spawnClient := newInternalWSClient()
	d.handleSpawnSession(spawnClient, spawn)
	if _, err := readInternalActionResult(spawnClient); err != nil {
		return nil, rollback.fail(fmt.Errorf("spawn reopened session: %w", err))
	}
	if session := d.store.Get(plan.SessionID); session == nil {
		return nil, rollback.fail(fmt.Errorf("reopened session was not persisted"))
	}
	if !reopened {
		rollback.onSessionSpawned(plan.SessionID)
	}

	if afterSpawn != nil {
		if err := afterSpawn(); err != nil {
			return nil, rollback.fail(err)
		}
	}
	rollback.abandon()
	d.logf("reopen: session %s is back in %s (workspace %s)", plan.SessionID, directory, workspaceID)
	return &sessionRuntimeReopened{SessionID: plan.SessionID, WorkspaceID: workspaceID}, nil
}

// A resumed session predates this call, so a rollback returns it to the ledger
// under its original closer instead of reaping the row and its history with it.
func (r *delegationRollback) onSessionReopened(sessionID string, closed store.SessionClose) {
	r.undo = append(r.undo, func() error {
		r.d.terminateSession(sessionID, syscall.SIGTERM)
		r.d.closeSession(sessionID, closed)
		return nil
	})
}

func (r *delegationRollback) onConversationForgotten(sessionID string, prior store.SessionConversation) {
	r.undo = append(r.undo, func() error {
		if strings.TrimSpace(prior.NativeID) == "" {
			return nil
		}
		if strings.TrimSpace(prior.TranscriptPath) != "" {
			_, err := r.d.store.TransitionSessionConversation(sessionID, prior.NativeID, prior.TranscriptPath)
			return err
		}
		_, err := r.d.store.TransitionSessionResumeID(sessionID, prior.NativeID)
		return err
	})
}

// The dispatch doc mirrors the resume id for a session that closed; a fresh
// start drops it so a later verdict does not offer a conversation nobody wants.
func (d *Daemon) forgetDispatchResume(sessionID string) {
	if _, err := d.updateGardenDispatch(sessionID, func(current garden.Dispatch) (garden.Dispatch, bool, error) {
		if strings.TrimSpace(current.Resume) == "" {
			return current, false, nil
		}
		current.Resume = ""
		return current, true, nil
	}); err != nil {
		d.logf("reopen: forgetting the resume id of session %s: %v", sessionID, err)
	}
}

func (d *Daemon) sessionCloseAttribution(sessionID string) store.SessionClose {
	entry := d.store.SessionLedgerEntry(sessionID)
	if entry == nil {
		return store.SessionClose{}
	}
	return store.SessionClose{
		By:     protocol.Deref(entry.ClosedBy),
		Reason: protocol.Deref(entry.CloseReason),
	}
}

// ---------------------------------------------------------------------------

func (d *Daemon) handleSessionReopen(conn net.Conn, msg *protocol.SessionReopenMessage) {
	action := protocol.SessionReopenAction("")
	if msg.Action != nil {
		action = *msg.Action
	}
	outcome, err := d.reopenSession(msg.SessionID, action, protocol.Deref(msg.Directory))
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	result := &protocol.SessionReopenResult{
		SessionID:   outcome.SessionID,
		WorkspaceID: outcome.WorkspaceID,
		Directory:   outcome.Directory,
		Action:      outcome.Action,
	}
	if outcome.AlreadyRunning {
		result.AlreadyRunning = protocol.Ptr(true)
	}
	if outcome.WorktreeCreated != "" {
		result.WorktreeCreated = protocol.Ptr(outcome.WorktreeCreated)
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, SessionReopenResult: result})
}
