package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ProtocolVersion is the version of the daemon-client protocol.
// Increment this when making breaking changes to the protocol.
// Client and daemon must have matching versions.
const ProtocolVersion = "25"

// Commands
const (
	CmdRegister                 = "register"
	CmdUnregister               = "unregister"
	CmdState                    = "state"
	CmdStop                     = "stop"
	CmdTodos                    = "todos"
	CmdQuery                    = "query"
	CmdHeartbeat                = "heartbeat"
	CmdMute                     = "mute"
	CmdQueryPRs                 = "query_prs"
	CmdMutePR                   = "mute_pr"
	CmdMuteRepo                 = "mute_repo"
	CmdMuteAuthor               = "mute_author"
	CmdCollapseRepo             = "collapse_repo"
	CmdQueryRepos               = "query_repos"
	CmdQueryAuthors             = "query_authors"
	CmdFetchPRDetails           = "fetch_pr_details"
	CmdRefreshPRs               = "refresh_prs"
	CmdClearSessions            = "clear_sessions"
	CmdPRVisited                = "pr_visited"
	CmdListWorktrees            = "list_worktrees"
	CmdCreateWorktree           = "create_worktree"
	CmdDeleteWorktree           = "delete_worktree"
	CmdGetSettings              = "get_settings"
	CmdSetSetting               = "set_setting"
	CmdApprovePR                = "approve_pr"
	CmdMergePR                  = "merge_pr"
	CmdInjectTestPR             = "inject_test_pr"
	CmdInjectTestSession        = "inject_test_session"
	CmdGetRecentLocations       = "get_recent_locations"
	CmdListBranches             = "list_branches"
	CmdDeleteBranch             = "delete_branch"
	CmdSwitchBranch             = "switch_branch"
	CmdCreateWorktreeFromBranch = "create_worktree_from_branch"
	CmdCreateBranch             = "create_branch"
	CmdCheckDirty               = "check_dirty"
	CmdStash                    = "stash"
	CmdStashPop                 = "stash_pop"
	CmdCheckAttnStash           = "check_attn_stash"
	CmdCommitWIP                = "commit_wip"
	CmdGetDefaultBranch         = "get_default_branch"
	CmdFetchRemotes             = "fetch_remotes"
	CmdListRemoteBranches       = "list_remote_branches"
	CmdEnsureRepo               = "ensure_repo"
	CmdSubscribeGitStatus       = "subscribe_git_status"
	CmdUnsubscribeGitStatus     = "unsubscribe_git_status"
	CmdGetFileDiff              = "get_file_diff"
	CmdGetBranchDiffFiles       = "get_branch_diff_files"
	CmdGetRepoInfo              = "get_repo_info"
	CmdGetReviewState           = "get_review_state"
	CmdMarkFileViewed           = "mark_file_viewed"
	CmdAddComment               = "add_comment"
	CmdUpdateComment            = "update_comment"
	CmdResolveComment           = "resolve_comment"
	CmdWontFixComment           = "wont_fix_comment"
	CmdDeleteComment            = "delete_comment"
	CmdGetComments              = "get_comments"
	CmdStartReview              = "start_review"
	CmdCancelReview             = "cancel_review"
	CmdSpawnSession             = "spawn_session"
	CmdAttachSession            = "attach_session"
	CmdDetachSession            = "detach_session"
	CmdPtyInput                 = "pty_input"
	CmdPtyResize                = "pty_resize"
	CmdKillSession              = "kill_session"
)

// WebSocket Events (daemon -> client)
const (
	EventSessionRegistered        = "session_registered"
	EventSessionUnregistered      = "session_unregistered"
	EventSessionStateChanged      = "session_state_changed"
	EventSessionTodosUpdated      = "session_todos_updated"
	EventSessionsUpdated          = "sessions_updated"
	EventPRsUpdated               = "prs_updated"
	EventReposUpdated             = "repos_updated"
	EventAuthorsUpdated           = "authors_updated"
	EventInitialState             = "initial_state"
	EventPRActionResult           = "pr_action_result"
	EventRefreshPRsResult         = "refresh_prs_result"
	EventFetchPRDetailsResult     = "fetch_pr_details_result"
	EventBranchChanged            = "branch_changed"
	EventWorktreeCreated          = "worktree_created"
	EventWorktreeDeleted          = "worktree_deleted"
	EventWorktreesUpdated         = "worktrees_updated"
	EventCreateWorktreeResult     = "create_worktree_result"
	EventDeleteWorktreeResult     = "delete_worktree_result"
	EventSettingsUpdated          = "settings_updated"
	EventRateLimited              = "rate_limited"
	EventRecentLocationsResult    = "recent_locations_result"
	EventBranchesResult           = "branches_result"
	EventDeleteBranchResult       = "delete_branch_result"
	EventSwitchBranchResult       = "switch_branch_result"
	EventCreateBranchResult       = "create_branch_result"
	EventCheckDirtyResult         = "check_dirty_result"
	EventStashResult              = "stash_result"
	EventStashPopResult           = "stash_pop_result"
	EventCheckAttnStashResult     = "check_attn_stash_result"
	EventCommitWIPResult          = "commit_wip_result"
	EventGetDefaultBranchResult   = "get_default_branch_result"
	EventFetchRemotesResult       = "fetch_remotes_result"
	EventListRemoteBranchesResult = "list_remote_branches_result"
	EventEnsureRepoResult         = "ensure_repo_result"
	EventGitStatusUpdate          = "git_status_update"
	EventFileDiffResult           = "file_diff_result"
	EventBranchDiffFilesResult    = "branch_diff_files_result"
	EventGetRepoInfoResult        = "get_repo_info_result"
	EventGetReviewStateResult     = "get_review_state_result"
	EventMarkFileViewedResult     = "mark_file_viewed_result"
	EventAddCommentResult         = "add_comment_result"
	EventUpdateCommentResult      = "update_comment_result"
	EventResolveCommentResult     = "resolve_comment_result"
	EventWontFixCommentResult     = "wont_fix_comment_result"
	EventDeleteCommentResult      = "delete_comment_result"
	EventGetCommentsResult        = "get_comments_result"
	EventReviewStarted            = "review_started"
	EventReviewChunk              = "review_chunk"
	EventReviewFinding            = "review_finding"
	EventReviewCommentResolved    = "review_comment_resolved"
	EventReviewToolUse            = "review_tool_use"
	EventReviewComplete           = "review_complete"
	EventReviewCancelled          = "review_cancelled"
	EventPtyOutput                = "pty_output"
	EventSpawnResult              = "spawn_result"
	EventAttachResult             = "attach_result"
	EventSessionExited            = "session_exited"
	EventPtyDesync                = "pty_desync"
	EventCommandError             = "command_error"
)

// Session states (values for SessionState enum)
const (
	StateWorking         = "working"
	StateWaitingInput    = "waiting_input"
	StateIdle            = "idle"
	StatePendingApproval = "pending_approval"
)

// Agent values
const (
	AgentShellValue = "shell"
)

// PR states (values for PR.State field, distinct from session states)
const (
	PRStateWaiting = "waiting" // PR needs attention
)

// PR reasons (why it needs attention)
const (
	PRReasonReadyToMerge     = "ready_to_merge"
	PRReasonCIFailed         = "ci_failed"
	PRReasonChangesRequested = "changes_requested"
	PRReasonReviewNeeded     = "review_needed"
)

// Heat state timing constants
const (
	HeatHotDuration  = 3 * time.Minute  // Stay hot for 3 min after activity
	HeatWarmDuration = 10 * time.Minute // Stay warm for 10 min total
	HeatHotInterval  = 30 * time.Second // Refresh hot PRs every 30s
	HeatWarmInterval = 2 * time.Minute  // Refresh warm PRs every 2 min
	HeatColdInterval = 10 * time.Minute // Refresh cold PRs every 10 min
)

// NeedsDetailRefresh returns true if PR details should be re-fetched
func (pr *PR) NeedsDetailRefresh() bool {
	if !pr.DetailsFetched {
		return true
	}
	// Parse timestamps for comparison
	lastUpdated := Timestamp(pr.LastUpdated).Time()
	detailsFetchedAt := Timestamp(Deref(pr.DetailsFetchedAt)).Time()

	// Invalidate if PR was updated after we fetched details
	if lastUpdated.After(detailsFetchedAt) {
		return true
	}
	// Invalidate if details are older than 5 minutes
	if time.Since(detailsFetchedAt) > 5*time.Minute {
		return true
	}
	return false
}

// ParseMessage parses a JSON message and returns the command type and parsed message
func ParseMessage(data []byte) (string, interface{}, error) {
	// First, extract just the command
	var peek struct {
		Cmd string `json:"cmd"`
	}
	if err := json.Unmarshal(data, &peek); err != nil {
		return "", nil, err
	}
	if peek.Cmd == "" {
		return "", nil, errors.New("missing cmd field")
	}

	// Parse based on command type
	switch peek.Cmd {
	case CmdRegister:
		var msg RegisterMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdUnregister:
		var msg UnregisterMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdState:
		var msg StateMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdStop:
		var msg StopMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdTodos:
		var msg TodosMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdQuery:
		var msg QueryMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdHeartbeat:
		var msg HeartbeatMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdMute:
		var msg MuteMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdQueryPRs:
		var msg QueryPRsMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdMutePR:
		var msg MutePRMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdMuteRepo:
		var msg MuteRepoMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdMuteAuthor:
		var msg MuteAuthorMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdCollapseRepo:
		var msg CollapseRepoMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdQueryRepos:
		var msg QueryReposMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdQueryAuthors:
		var msg QueryAuthorsMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdFetchPRDetails:
		var msg FetchPRDetailsMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdRefreshPRs:
		var msg RefreshPRsMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdClearSessions:
		var msg ClearSessionsMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdPRVisited:
		var msg PRVisitedMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdListWorktrees:
		var msg ListWorktreesMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdCreateWorktree:
		var msg CreateWorktreeMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdDeleteWorktree:
		var msg DeleteWorktreeMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdGetSettings:
		var msg GetSettingsMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdSetSetting:
		var msg SetSettingMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdApprovePR:
		var msg ApprovePRMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdMergePR:
		var msg MergePRMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdInjectTestPR:
		var msg InjectTestPRMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdInjectTestSession:
		var msg InjectTestSessionMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdGetRecentLocations:
		var msg GetRecentLocationsMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdListBranches:
		var msg ListBranchesMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdDeleteBranch:
		var msg DeleteBranchMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdSwitchBranch:
		var msg SwitchBranchMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdCreateWorktreeFromBranch:
		var msg CreateWorktreeFromBranchMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdCreateBranch:
		var msg CreateBranchMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdCheckDirty:
		var msg CheckDirtyMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdStash:
		var msg StashMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdStashPop:
		var msg StashPopMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdCheckAttnStash:
		var msg CheckAttnStashMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdCommitWIP:
		var msg CommitWIPMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdGetDefaultBranch:
		var msg GetDefaultBranchMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdFetchRemotes:
		var msg FetchRemotesMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdListRemoteBranches:
		var msg ListRemoteBranchesMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdEnsureRepo:
		var msg EnsureRepoMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdSubscribeGitStatus:
		var msg SubscribeGitStatusMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdUnsubscribeGitStatus:
		var msg UnsubscribeGitStatusMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdGetFileDiff:
		var msg GetFileDiffMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdGetBranchDiffFiles:
		var msg GetBranchDiffFilesMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdGetRepoInfo:
		var msg GetRepoInfoMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdGetReviewState:
		var msg GetReviewStateMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdMarkFileViewed:
		var msg MarkFileViewedMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdAddComment:
		var msg AddCommentMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal add_comment: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdUpdateComment:
		var msg UpdateCommentMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal update_comment: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdResolveComment:
		var msg ResolveCommentMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal resolve_comment: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdWontFixComment:
		var msg WontFixCommentMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal wont_fix_comment: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdDeleteComment:
		var msg DeleteCommentMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal delete_comment: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdGetComments:
		var msg GetCommentsMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal get_comments: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdStartReview:
		var msg StartReviewMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal start_review: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdCancelReview:
		var msg CancelReviewMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal cancel_review: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdSpawnSession:
		var msg SpawnSessionMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal spawn_session: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdAttachSession:
		var msg AttachSessionMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal attach_session: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdDetachSession:
		var msg DetachSessionMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal detach_session: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdPtyInput:
		var msg PtyInputMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal pty_input: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdPtyResize:
		var msg PtyResizeMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal pty_resize: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdKillSession:
		var msg KillSessionMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal kill_session: %w", err)
		}
		return peek.Cmd, &msg, nil

	default:
		return "", nil, errors.New("unknown command: " + peek.Cmd)
	}
}
