package dashboard

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/victorarias/claude-manager/internal/client"
	"github.com/victorarias/claude-manager/internal/protocol"
)

// Styles using lipgloss
var (
	// Colors
	yellowStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))  // waiting
	greenStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))  // working
	grayStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))  // muted

	// Pane styles
	paneWidth       = 38
	focusedBorder   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("4")).Width(paneWidth)
	unfocusedBorder = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("8")).Width(paneWidth)

	// Header styles
	headerStyle = lipgloss.NewStyle().Bold(true).Padding(0, 1)

	// Legend style
	legendStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
)

// Model is the bubbletea model for the dashboard
type Model struct {
	client          *client.Client
	sessions        []*protocol.Session
	prs             []*protocol.PR
	repoStates      map[string]*protocol.RepoState
	cursor          int
	prCursor        int // now indexes into flattened view
	focusPane       int  // 0 = sessions, 1 = PRs
	showMutedPRs    bool
	showMutedRepos  bool
	err             error
	currentSession  string // current tmux session name
}

// repoGroup represents a repository with its PRs
type repoGroup struct {
	name      string
	prs       []*protocol.PR
	muted     bool
	collapsed bool
}

// NewModel creates a new dashboard model
func NewModel(c *client.Client) *Model {
	return &Model{
		client:         c,
		currentSession: getCurrentTmuxSession(),
	}
}

// getCurrentTmuxSession returns the current tmux session name
func getCurrentTmuxSession() string {
	if os.Getenv("TMUX") == "" {
		return ""
	}
	cmd := exec.Command("tmux", "display-message", "-p", "#{session_name}")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

// parseTargetSession extracts session name from tmux target (format: "session:window.pane")
func parseTargetSession(tmuxTarget string) string {
	if tmuxTarget == "" {
		return ""
	}
	// Format is "session:window.pane" or "session:window"
	parts := strings.SplitN(tmuxTarget, ":", 2)
	return parts[0]
}

// Init initializes the model
func (m *Model) Init() tea.Cmd {
	return tea.Batch(m.refresh, TickCmd())
}

// buildRepoGroups groups PRs by repository
func (m *Model) buildRepoGroups() []*repoGroup {
	// Group PRs by repo
	grouped := make(map[string][]*protocol.PR)
	for _, pr := range m.prs {
		// Skip muted PRs if not showing them
		if pr.Muted && !m.showMutedPRs {
			continue
		}
		grouped[pr.Repo] = append(grouped[pr.Repo], pr)
	}

	// Build sorted list of groups
	var repos []string
	for repo := range grouped {
		repos = append(repos, repo)
	}
	sort.Strings(repos)

	var groups []*repoGroup
	for _, repo := range repos {
		state := m.repoStates[repo]
		muted := state != nil && state.Muted
		collapsed := state == nil || state.Collapsed // default collapsed

		// Skip muted repos if not showing them
		if muted && !m.showMutedRepos {
			continue
		}

		groups = append(groups, &repoGroup{
			name:      repo,
			prs:       grouped[repo],
			muted:     muted,
			collapsed: collapsed,
		})
	}

	return groups
}

// countPRItems returns total navigable items in PR pane
func (m *Model) countPRItems() int {
	groups := m.buildRepoGroups()
	count := 0
	for _, g := range groups {
		count++ // repo header
		if !g.collapsed {
			count += len(g.prs) // individual PRs
		}
	}
	return count
}

// getPRItemAt returns what's at the given cursor position
// Returns: (isRepo bool, repoName string, pr *protocol.PR)
func (m *Model) getPRItemAt(cursor int) (bool, string, *protocol.PR) {
	groups := m.buildRepoGroups()
	idx := 0
	for _, g := range groups {
		if idx == cursor {
			return true, g.name, nil
		}
		idx++
		if !g.collapsed {
			for _, pr := range g.prs {
				if idx == cursor {
					return false, g.name, pr
				}
				idx++
			}
		}
	}
	return false, "", nil
}

// refresh fetches sessions from daemon
func (m *Model) refresh() tea.Msg {
	if m.client == nil {
		return sessionsMsg{sessions: nil, prs: nil, repos: nil}
	}
	sessions, err := m.client.Query("")
	if err != nil {
		return errMsg{err: err}
	}
	prs, _ := m.client.QueryPRs("")     // Ignore error, PRs are optional
	repos, _ := m.client.QueryRepos()   // Ignore error, repos are optional
	return sessionsMsg{sessions: sessions, prs: prs, repos: repos}
}

type sessionsMsg struct {
	sessions []*protocol.Session
	prs      []*protocol.PR
	repos    []*protocol.RepoState
}

type errMsg struct {
	err error
}

type tickMsg struct{}

// Update handles messages
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "tab":
			if m.focusPane == 0 {
				m.focusPane = 1
			} else {
				m.focusPane = 0
			}
		case "left", "h":
			m.focusPane = 0
		case "right", "l":
			m.focusPane = 1
		case "up", "k":
			if m.focusPane == 0 {
				m.moveCursor(-1)
			} else {
				m.movePRCursor(-1)
			}
		case "down", "j":
			if m.focusPane == 0 {
				m.moveCursor(1)
			} else {
				m.movePRCursor(1)
			}
		case "r":
			return m, m.refresh
		case "enter", " ":
			if m.focusPane == 0 {
				if s := m.SelectedSession(); s != nil && s.TmuxTarget != "" {
					return m, m.jumpToPane(s.TmuxTarget)
				}
			} else {
				// PR pane - check if on repo or PR
				if repo := m.SelectedRepo(); repo != "" {
					// Toggle collapse
					return m, m.toggleRepoCollapsed(repo)
				} else if pr := m.SelectedPR(); pr != nil {
					return m, m.openPRInBrowser(pr.URL)
				}
			}
		case "m":
			if m.focusPane == 0 {
				if s := m.SelectedSession(); s != nil {
					return m, m.toggleMute(s.ID)
				}
			} else {
				// PR pane - mute repo or PR
				if repo := m.SelectedRepo(); repo != "" {
					return m, m.toggleMuteRepo(repo)
				} else if pr := m.SelectedPR(); pr != nil {
					return m, m.toggleMutePR(pr.ID)
				}
			}
		case "M":
			m.showMutedPRs = !m.showMutedPRs
		case "V":
			m.showMutedRepos = !m.showMutedRepos
		case "x", "d":
			if m.focusPane == 0 {
				if s := m.SelectedSession(); s != nil {
					return m, m.deleteSession(s.ID)
				}
			}
		case "R":
			return m, m.restartDaemon
		}
	case sessionsMsg:
		m.sessions = msg.sessions
		m.prs = msg.prs
		// Build repo states map
		m.repoStates = make(map[string]*protocol.RepoState)
		for _, repo := range msg.repos {
			m.repoStates[repo.Repo] = repo
		}
		m.err = nil
		// Ensure cursors are valid
		if m.cursor >= len(m.sessions) && len(m.sessions) > 0 {
			m.cursor = len(m.sessions) - 1
		}
		maxItems := m.countPRItems()
		if m.prCursor >= maxItems && maxItems > 0 {
			m.prCursor = maxItems - 1
		}
		return m, TickCmd()
	case errMsg:
		m.err = msg.err
	case tickMsg:
		return m, m.refresh
	}
	return m, nil
}

func (m *Model) moveCursor(delta int) {
	m.cursor += delta
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(m.sessions) && len(m.sessions) > 0 {
		m.cursor = len(m.sessions) - 1
	}
}

func (m *Model) movePRCursor(delta int) {
	m.prCursor += delta
	maxItems := m.countPRItems()
	if m.prCursor < 0 {
		m.prCursor = 0
	}
	if m.prCursor >= maxItems && maxItems > 0 {
		m.prCursor = maxItems - 1
	}
}

// SelectedSession returns the currently selected session
func (m *Model) SelectedSession() *protocol.Session {
	if m.cursor >= 0 && m.cursor < len(m.sessions) {
		return m.sessions[m.cursor]
	}
	return nil
}

// SelectedPR returns the currently selected PR, or nil if on a repo header
func (m *Model) SelectedPR() *protocol.PR {
	if m.focusPane != 1 {
		return nil
	}
	isRepo, _, pr := m.getPRItemAt(m.prCursor)
	if isRepo {
		return nil
	}
	return pr
}

// SelectedRepo returns the repo name if cursor is on a repo header
func (m *Model) SelectedRepo() string {
	if m.focusPane != 1 {
		return ""
	}
	isRepo, repoName, _ := m.getPRItemAt(m.prCursor)
	if isRepo {
		return repoName
	}
	return ""
}

// getVisiblePRs returns PRs filtered by muted state
func (m *Model) getVisiblePRs() []*protocol.PR {
	if m.showMutedPRs {
		return m.prs
	}
	var visible []*protocol.PR
	for _, pr := range m.prs {
		if !pr.Muted {
			visible = append(visible, pr)
		}
	}
	return visible
}

func (m *Model) toggleMute(sessionID string) tea.Cmd {
	return func() tea.Msg {
		if err := m.client.ToggleMute(sessionID); err != nil {
			return errMsg{err: err}
		}
		return m.refresh()
	}
}

func (m *Model) toggleMutePR(prID string) tea.Cmd {
	return func() tea.Msg {
		if err := m.client.ToggleMutePR(prID); err != nil {
			return errMsg{err: err}
		}
		return m.refresh()
	}
}

func (m *Model) toggleRepoCollapsed(repo string) tea.Cmd {
	return func() tea.Msg {
		// Get current displayed state (default is collapsed when no state exists)
		state := m.repoStates[repo]
		currentlyCollapsed := state == nil || state.Collapsed
		newCollapsed := !currentlyCollapsed
		if err := m.client.SetRepoCollapsed(repo, newCollapsed); err != nil {
			return errMsg{err: err}
		}
		return m.refresh()
	}
}

func (m *Model) toggleMuteRepo(repo string) tea.Cmd {
	return func() tea.Msg {
		if err := m.client.ToggleMuteRepo(repo); err != nil {
			return errMsg{err: err}
		}
		return m.refresh()
	}
}

func (m *Model) deleteSession(sessionID string) tea.Cmd {
	return func() tea.Msg {
		if err := m.client.Unregister(sessionID); err != nil {
			return errMsg{err: err}
		}
		return m.refresh()
	}
}

func (m *Model) openPRInBrowser(url string) tea.Cmd {
	c := exec.Command("open", url) // macOS; use xdg-open on Linux
	return tea.ExecProcess(c, func(err error) tea.Msg {
		return nil
	})
}

func (m *Model) restartDaemon() tea.Msg {
	socketPath := client.DefaultSocketPath()

	// Remove socket to stop existing daemon
	os.Remove(socketPath)

	// Start new daemon in background
	cmd := exec.Command(os.Args[0], "daemon")
	if err := cmd.Start(); err != nil {
		return errMsg{err: fmt.Errorf("failed to start daemon: %w", err)}
	}

	// Wait for daemon to be ready
	for i := 0; i < 50; i++ {
		if m.client.IsRunning() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	return m.refresh()
}

func (m *Model) jumpToPane(tmuxTarget string) tea.Cmd {
	targetSession := parseTargetSession(tmuxTarget)

	// Same session: just switch to the pane
	if targetSession == m.currentSession || m.currentSession == "" {
		c := exec.Command("tmux", "switch-client", "-t", "="+tmuxTarget)
		return tea.ExecProcess(c, func(err error) tea.Msg {
			return nil
		})
	}

	// Different session: open in a popup to avoid losing dashboard
	// First select the window/pane, then attach (so we land on the right pane)
	cmd := fmt.Sprintf("tmux select-window -t '%s' && tmux select-pane -t '%s' && tmux attach-session -t '%s'",
		tmuxTarget, tmuxTarget, targetSession)
	c := exec.Command("tmux", "display-popup", "-E", "-w", "90%", "-h", "90%",
		"bash", "-c", cmd)
	return tea.ExecProcess(c, func(err error) tea.Msg {
		return nil
	})
}

// View renders the dashboard with two-column layout
func (m *Model) View() string {
	if m.err != nil {
		return fmt.Sprintf("Error: %v\n\nPress 'r' to retry, 'q' to quit", m.err)
	}

	// Build sessions pane content
	sessContent := m.buildSessionsContent()
	sessBorder := unfocusedBorder
	if m.focusPane == 0 {
		sessBorder = focusedBorder
	}
	sessPane := sessBorder.Render(sessContent)

	// Build PRs pane content
	prContent := m.buildPRsContent()
	prBorder := unfocusedBorder
	if m.focusPane == 1 {
		prBorder = focusedBorder
	}
	prPane := prBorder.Render(prContent)

	// Join horizontally
	content := lipgloss.JoinHorizontal(lipgloss.Top, sessPane, " ", prPane)

	// Legend
	legend := fmt.Sprintf("%s waiting  %s working  %s muted",
		yellowStyle.Render("●"),
		greenStyle.Render("○"),
		grayStyle.Render("◌"))
	help := legendStyle.Render("[←/→] Switch  [m] Mute  [M] Muted PRs  [V] Muted repos  [Space] Open/Expand  [R] Restart  [q] Quit")

	return content + "\n" + legend + "\n" + help + "\n"
}

func (m *Model) buildSessionsContent() string {
	var lines []string
	lines = append(lines, headerStyle.Render(fmt.Sprintf("Sessions (%d)", len(m.sessions))))
	lines = append(lines, "")

	if len(m.sessions) == 0 {
		lines = append(lines, grayStyle.Render("  No active sessions"))
	} else {
		for i, session := range m.sessions {
			cursor := "  "
			if i == m.cursor && m.focusPane == 0 {
				cursor = "> "
			}

			var style lipgloss.Style
			var indicator, stateStr string
			if session.Muted {
				style = grayStyle
				indicator = "◌"
				stateStr = "muted"
			} else if session.State == protocol.StateWaiting {
				style = yellowStyle
				indicator = "●"
				stateStr = "waiting"
			} else {
				style = greenStyle
				indicator = "○"
				stateStr = "working"
			}

			label := truncate(session.Label, 14)
			line := fmt.Sprintf("%s%s %-14s %s", cursor, indicator, label, stateStr)
			lines = append(lines, style.Render(line))
		}
	}

	return strings.Join(lines, "\n")
}

func (m *Model) buildPRsContent() string {
	var lines []string
	groups := m.buildRepoGroups()

	totalPRs := 0
	for _, g := range groups {
		totalPRs += len(g.prs)
	}

	lines = append(lines, headerStyle.Render(fmt.Sprintf("Pull Requests (%d)", totalPRs)))
	lines = append(lines, "")

	if len(groups) == 0 {
		if len(m.prs) == 0 {
			lines = append(lines, grayStyle.Render("  No PRs (gh CLI?)"))
		} else {
			lines = append(lines, grayStyle.Render("  All PRs muted"))
		}
		return strings.Join(lines, "\n")
	}

	prIndex := 0
	for _, group := range groups {
		// Render repo header
		cursor := "  "
		if m.focusPane == 1 && m.prCursor == prIndex {
			cursor = "> "
		}

		icon := "▶"
		if !group.collapsed {
			icon = "▼"
		}

		// Short repo name
		repoShort := group.name
		if idx := strings.LastIndex(group.name, "/"); idx >= 0 {
			repoShort = group.name[idx+1:]
		}

		style := lipgloss.NewStyle()
		if group.muted {
			style = grayStyle
		}

		repoLine := fmt.Sprintf("%s%s %s (%d)", cursor, icon, repoShort, len(group.prs))
		lines = append(lines, style.Render(repoLine))
		prIndex++

		// Render PRs if expanded
		if !group.collapsed {
			for _, pr := range group.prs {
				cursor := "    " // extra indent for PRs
				if m.focusPane == 1 && m.prCursor == prIndex {
					cursor = "  > "
				}

				var style lipgloss.Style
				var stateStr string
				if pr.Muted {
					style = grayStyle
					stateStr = "muted"
				} else if pr.State == protocol.StateWaiting {
					style = yellowStyle
					switch pr.Reason {
					case protocol.PRReasonReadyToMerge:
						stateStr = "merge"
					case protocol.PRReasonCIFailed:
						stateStr = "fix"
					case protocol.PRReasonChangesRequested:
						stateStr = "fix"
					case protocol.PRReasonReviewNeeded:
						stateStr = "review"
					default:
						stateStr = "open"
					}
				} else {
					style = greenStyle
					stateStr = "wait"
				}

				prLine := fmt.Sprintf("%s⬡ #%d %s", cursor, pr.Number, stateStr)
				lines = append(lines, style.Render(prLine))

				// PR title on next line(s), wrapped
				titleLines := wrapText(pr.Title, paneWidth-6)
				for _, tl := range titleLines {
					lines = append(lines, grayStyle.Render("      "+tl))
				}

				prIndex++
			}
		}
	}

	return strings.Join(lines, "\n")
}

// wrapText wraps text to maxWidth, returning lines
func wrapText(text string, maxWidth int) []string {
	if len(text) <= maxWidth {
		return []string{text}
	}

	var lines []string
	for len(text) > maxWidth {
		// Find last space before maxWidth
		breakAt := maxWidth
		for i := maxWidth - 1; i > 0; i-- {
			if text[i] == ' ' {
				breakAt = i
				break
			}
		}
		lines = append(lines, text[:breakAt])
		text = strings.TrimLeft(text[breakAt:], " ")
	}
	if len(text) > 0 {
		lines = append(lines, text)
	}
	return lines
}


func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-1] + "…"
}


// TickCmd returns a command that ticks for auto-refresh
func TickCmd() tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg {
		return tickMsg{}
	})
}
