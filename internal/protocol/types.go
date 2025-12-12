package protocol

import (
	"encoding/json"
	"errors"
	"time"
)

// ProtocolVersion is the version of the daemon-client protocol.
// Increment this when making breaking changes to the protocol.
// Client and daemon must have matching versions.
const ProtocolVersion = "5"

// Commands
const (
	CmdRegister        = "register"
	CmdUnregister      = "unregister"
	CmdState           = "state"
	CmdStop            = "stop"
	CmdTodos           = "todos"
	CmdQuery           = "query"
	CmdHeartbeat       = "heartbeat"
	CmdMute            = "mute"
	CmdQueryPRs        = "query_prs"
	CmdMutePR          = "mute_pr"
	CmdMuteRepo        = "mute_repo"
	CmdCollapseRepo    = "collapse_repo"
	CmdQueryRepos      = "query_repos"
	CmdFetchPRDetails  = "fetch_pr_details"
	CmdRefreshPRs      = "refresh_prs"
	CmdClearSessions   = "clear_sessions"
	CmdPRVisited       = "pr_visited"
	CmdListWorktrees   = "list_worktrees"
	CmdCreateWorktree  = "create_worktree"
	CmdDeleteWorktree  = "delete_worktree"
	MsgApprovePR          = "approve_pr"
	MsgMergePR            = "merge_pr"
	MsgInjectTestPR       = "inject_test_pr"
	MsgInjectTestSession  = "inject_test_session"
)

// WebSocket Events (daemon -> client)
const (
	EventSessionRegistered   = "session_registered"
	EventSessionUnregistered = "session_unregistered"
	EventSessionStateChanged = "session_state_changed"
	EventSessionTodosUpdated = "session_todos_updated"
	EventSessionsUpdated     = "sessions_updated"
	EventPRsUpdated          = "prs_updated"
	EventReposUpdated        = "repos_updated"
	EventInitialState        = "initial_state"
	MsgPRActionResult        = "pr_action_result"
	EventRefreshPRsResult    = "refresh_prs_result"
	EventBranchChanged       = "branch_changed"
	EventWorktreeCreated       = "worktree_created"
	EventWorktreeDeleted       = "worktree_deleted"
	EventWorktreesUpdated      = "worktrees_updated"
	EventCreateWorktreeResult  = "create_worktree_result"
	EventDeleteWorktreeResult  = "delete_worktree_result"
)

// States
const (
	StateWorking      = "working"
	StateWaitingInput = "waiting_input"
	StateIdle         = "idle"
	StateWaiting      = "waiting" // Keep for backward compat
)

// PR reasons (why it needs attention)
const (
	PRReasonReadyToMerge     = "ready_to_merge"
	PRReasonCIFailed         = "ci_failed"
	PRReasonChangesRequested = "changes_requested"
	PRReasonReviewNeeded     = "review_needed"
)

// PR roles
const (
	PRRoleAuthor   = "author"
	PRRoleReviewer = "reviewer"
)

// PR heat states (for detail refresh scheduling)
const (
	HeatStateHot  = "hot"
	HeatStateWarm = "warm"
	HeatStateCold = "cold"
)

// Heat state timing constants
const (
	HeatHotDuration  = 3 * time.Minute  // Stay hot for 3 min after activity
	HeatWarmDuration = 10 * time.Minute // Stay warm for 10 min total
	HeatHotInterval  = 30 * time.Second // Refresh hot PRs every 30s
	HeatWarmInterval = 2 * time.Minute  // Refresh warm PRs every 2 min
	HeatColdInterval = 10 * time.Minute // Refresh cold PRs every 10 min
)

// RegisterMessage registers a new session with the daemon
type RegisterMessage struct {
	Cmd   string `json:"cmd"`
	ID    string `json:"id"`
	Label string `json:"label"`
	Dir   string `json:"dir"`
}

// UnregisterMessage removes a session from tracking
type UnregisterMessage struct {
	Cmd string `json:"cmd"`
	ID  string `json:"id"`
}

// StateMessage updates a session's state
type StateMessage struct {
	Cmd   string `json:"cmd"`
	ID    string `json:"id"`
	State string `json:"state"`
}

// StopMessage triggers classification of idle vs waiting_input
type StopMessage struct {
	Cmd            string `json:"cmd"`
	ID             string `json:"id"`
	TranscriptPath string `json:"transcript_path"`
}

// TodosMessage updates a session's todo list
type TodosMessage struct {
	Cmd   string   `json:"cmd"`
	ID    string   `json:"id"`
	Todos []string `json:"todos"`
}

// QueryMessage queries sessions from daemon
type QueryMessage struct {
	Cmd    string `json:"cmd"`
	Filter string `json:"filter,omitempty"` // "waiting", "working", or empty for all
}

// HeartbeatMessage keeps session alive
type HeartbeatMessage struct {
	Cmd string `json:"cmd"`
	ID  string `json:"id"`
}

// MuteMessage toggles a session's muted state
type MuteMessage struct {
	Cmd string `json:"cmd"`
	ID  string `json:"id"`
}

// QueryPRsMessage queries PRs from daemon
type QueryPRsMessage struct {
	Cmd    string `json:"cmd"`
	Filter string `json:"filter,omitempty"` // "waiting", "working", or empty for all
}

// MutePRMessage toggles a PR's muted state
type MutePRMessage struct {
	Cmd string `json:"cmd"`
	ID  string `json:"id"`
}

// MuteRepoMessage toggles repo muted state
type MuteRepoMessage struct {
	Repo string `json:"repo"`
}

// CollapseRepoMessage sets repo collapsed state
type CollapseRepoMessage struct {
	Repo      string `json:"repo"`
	Collapsed bool   `json:"collapsed"`
}

// QueryReposMessage requests repo states
type QueryReposMessage struct {
	Filter string `json:"filter,omitempty"`
}

// FetchPRDetailsMessage requests daemon to fetch PR details for a repo
type FetchPRDetailsMessage struct {
	Cmd  string `json:"cmd"`
	Repo string `json:"repo"`
}

// RefreshPRsMessage requests daemon to refresh all PRs from GitHub
type RefreshPRsMessage struct {
	Cmd string `json:"cmd"`
}

// ClearSessionsMessage requests daemon to clear all tracked sessions
type ClearSessionsMessage struct {
	Cmd string `json:"cmd"`
}

// PRVisitedMessage marks a PR as visited by the user
type PRVisitedMessage struct {
	Cmd string `json:"cmd"`
	ID  string `json:"id"` // PR ID (owner/repo#number)
}

// ListWorktreesMessage requests worktrees for a repo
type ListWorktreesMessage struct {
	Cmd      string `json:"cmd"`
	MainRepo string `json:"main_repo"`
}

// CreateWorktreeMessage creates a new worktree
type CreateWorktreeMessage struct {
	Cmd      string `json:"cmd"`
	MainRepo string `json:"main_repo"`
	Branch   string `json:"branch"`
	Path     string `json:"path,omitempty"` // Auto-generated if empty
}

// DeleteWorktreeMessage removes a worktree
type DeleteWorktreeMessage struct {
	Cmd  string `json:"cmd"`
	Path string `json:"path"`
}

// WorktreeCreatedEvent is broadcast when a worktree is created
type WorktreeCreatedEvent struct {
	Path     string `json:"path"`
	Branch   string `json:"branch"`
	MainRepo string `json:"main_repo"`
}

// CreateWorktreeResultMessage is sent back after worktree creation
type CreateWorktreeResultMessage struct {
	Event   string `json:"event"`
	Path    string `json:"path,omitempty"` // Path if successful
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

// DeleteWorktreeResultMessage is sent back after worktree deletion
type DeleteWorktreeResultMessage struct {
	Event   string `json:"event"`
	Path    string `json:"path"`
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

// ApprovePRMessage requests approval of a PR
type ApprovePRMessage struct {
	Cmd    string `json:"cmd"`
	Repo   string `json:"repo"`
	Number int    `json:"number"`
}

// MergePRMessage requests merging of a PR
type MergePRMessage struct {
	Cmd    string `json:"cmd"`
	Repo   string `json:"repo"`
	Number int    `json:"number"`
	Method string `json:"method"` // "squash", "merge", "rebase"
}

// InjectTestPRMessage injects a test PR into the daemon (for E2E tests)
type InjectTestPRMessage struct {
	Cmd string `json:"cmd"`
	PR  *PR    `json:"pr"`
}

// InjectTestSessionMessage injects a test session into the daemon (for E2E tests)
type InjectTestSessionMessage struct {
	Cmd     string   `json:"cmd"`
	Session *Session `json:"session"`
}

// PRActionResultMessage is sent back to client after PR action completes
type PRActionResultMessage struct {
	Event   string `json:"event"`
	Action  string `json:"action"` // "approve" or "merge"
	Repo    string `json:"repo"`
	Number  int    `json:"number"`
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

// RefreshPRsResultMessage is sent back to client after PR refresh completes
type RefreshPRsResultMessage struct {
	Event   string `json:"event"`
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

// Session represents a tracked Claude session
type Session struct {
	ID             string    `json:"id"`
	Label          string    `json:"label"`
	Directory      string    `json:"directory"`
	Branch         string    `json:"branch,omitempty"`      // Current git branch
	IsWorktree     bool      `json:"is_worktree,omitempty"` // True if in a git worktree
	MainRepo       string    `json:"main_repo,omitempty"`   // Path to main repo if worktree
	State          string    `json:"state"`
	StateSince     time.Time `json:"state_since"`
	StateUpdatedAt time.Time `json:"state_updated_at"` // For race condition prevention
	Todos          []string  `json:"todos,omitempty"`
	LastSeen       time.Time `json:"last_seen"`
	Muted          bool      `json:"muted"`
}

// PR represents a tracked GitHub pull request
type PR struct {
	ID          string    `json:"id"`           // "owner/repo#number"
	Repo        string    `json:"repo"`         // "owner/repo"
	Number      int       `json:"number"`
	Title       string    `json:"title"`
	URL         string    `json:"url"`
	Role        string    `json:"role"`         // "author" or "reviewer"
	State       string    `json:"state"`        // "waiting" or "working"
	Reason      string    `json:"reason"`       // why it needs attention
	LastUpdated time.Time `json:"last_updated"`
	LastPolled  time.Time `json:"last_polled"`
	Muted       bool      `json:"muted"`
	// Detailed status (fetched on-demand via gh api)
	DetailsFetched   bool      `json:"details_fetched"`    // true if details have been loaded
	DetailsFetchedAt time.Time `json:"details_fetched_at"` // when details were fetched
	Mergeable        *bool     `json:"mergeable"`          // nil=unknown, true/false
	MergeableState   string    `json:"mergeable_state"`    // clean, blocked, dirty, unstable
	CIStatus         string    `json:"ci_status"`          // success, failure, pending, none
	ReviewStatus     string    `json:"review_status"`      // approved, changes_requested, pending, none
	// Interaction tracking (Plan 2)
	HeadSHA       string `json:"head_sha"`        // current commit SHA for change detection
	CommentCount  int    `json:"comment_count"`   // for change detection
	ApprovedByMe  bool   `json:"approved_by_me"`  // true if user approved this PR
	HasNewChanges bool   `json:"has_new_changes"` // true if PR changed since last visit
	// Heat state for detail refresh scheduling
	HeatState          string    `json:"heat_state"`            // hot, warm, cold
	LastHeatActivityAt time.Time `json:"last_heat_activity_at"` // when heat was last triggered
}

// NeedsDetailRefresh returns true if PR details should be re-fetched
func (pr *PR) NeedsDetailRefresh() bool {
	if !pr.DetailsFetched {
		return true
	}
	// Invalidate if PR was updated after we fetched details
	if pr.LastUpdated.After(pr.DetailsFetchedAt) {
		return true
	}
	// Invalidate if details are older than 5 minutes
	if time.Since(pr.DetailsFetchedAt) > 5*time.Minute {
		return true
	}
	return false
}

// Worktree for protocol (used in WebSocket events)
type Worktree struct {
	Path      string `json:"path"`
	Branch    string `json:"branch"`
	MainRepo  string `json:"main_repo"`
	CreatedAt string `json:"created_at"`
}

// RepoState tracks per-repo UI state
type RepoState struct {
	Repo      string `json:"repo"`
	Muted     bool   `json:"muted"`
	Collapsed bool   `json:"collapsed"`
}

// Response from daemon
type Response struct {
	OK       bool         `json:"ok"`
	Error    string       `json:"error,omitempty"`
	Sessions []*Session   `json:"sessions,omitempty"`
	PRs      []*PR        `json:"prs,omitempty"`
	Repos    []*RepoState `json:"repos,omitempty"`
}

// WebSocketEvent is sent from daemon to connected WebSocket clients
type WebSocketEvent struct {
	Event           string       `json:"event"`
	ProtocolVersion string       `json:"protocol_version,omitempty"`
	Session         *Session     `json:"session,omitempty"`
	Sessions        []*Session   `json:"sessions,omitempty"`
	PRs             []*PR        `json:"prs,omitempty"`
	Repos           []*RepoState `json:"repos,omitempty"`
	Worktrees       []*Worktree  `json:"worktrees,omitempty"`
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

	case MsgApprovePR:
		var msg ApprovePRMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case MsgMergePR:
		var msg MergePRMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case MsgInjectTestPR:
		var msg InjectTestPRMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case MsgInjectTestSession:
		var msg InjectTestSessionMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	default:
		return "", nil, errors.New("unknown command: " + peek.Cmd)
	}
}
