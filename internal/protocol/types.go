package protocol

import (
	"encoding/json"
	"errors"
	"time"
)

// Commands
const (
	CmdRegister     = "register"
	CmdUnregister   = "unregister"
	CmdState        = "state"
	CmdTodos        = "todos"
	CmdQuery        = "query"
	CmdHeartbeat    = "heartbeat"
	CmdMute         = "mute"
	CmdQueryPRs     = "query_prs"
	CmdMutePR       = "mute_pr"
	CmdMuteRepo     = "mute_repo"
	CmdCollapseRepo = "collapse_repo"
	CmdQueryRepos   = "query_repos"
)

// States
const (
	StateWorking = "working"
	StateWaiting = "waiting"
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

// RegisterMessage registers a new session with the daemon
type RegisterMessage struct {
	Cmd   string `json:"cmd"`
	ID    string `json:"id"`
	Label string `json:"label"`
	Dir   string `json:"dir"`
	Tmux  string `json:"tmux"`
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

// Session represents a tracked Claude session
type Session struct {
	ID         string    `json:"id"`
	Label      string    `json:"label"`
	Directory  string    `json:"directory"`
	TmuxTarget string    `json:"tmux_target"`
	State      string    `json:"state"`
	StateSince time.Time `json:"state_since"`
	Todos      []string  `json:"todos,omitempty"`
	LastSeen   time.Time `json:"last_seen"`
	Muted      bool      `json:"muted"`
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

	default:
		return "", nil, errors.New("unknown command: " + peek.Cmd)
	}
}
