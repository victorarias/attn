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
	"github.com/victorarias/claude-manager/internal/github"
	"github.com/victorarias/claude-manager/internal/protocol"
)

// Styles using lipgloss
var (
	// Colors
	yellowStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))  // waiting
	greenStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))  // working/success
	grayStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))  // muted
	cyanStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))  // author (your PRs)
	magentaStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("5"))  // reviewer (review requests)
	redStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))  // failure/blocked

	// Pane border styles (width set dynamically)
	focusedBorderStyle   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("4"))
	unfocusedBorderStyle = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("8"))

	// Header styles
	headerStyle = lipgloss.NewStyle().Bold(true).Padding(0, 1)

	// Legend style
	legendStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
)

// Model is the bubbletea model for the dashboard
type Model struct {
	client          *client.Client
	ghFetcher       *github.Fetcher
	sessions        []*protocol.Session
	prs             []*protocol.PR
	repoStates      map[string]*protocol.RepoState
	cursor          int
	prCursor        int  // now indexes into flattened view
	focusPane       int  // 0 = sessions, 1 = PRs
	showMutedPRs    bool
	showMutedRepos  bool
	err             error
	currentSession  string // current tmux session name
	width           int    // terminal width
	height          int    // terminal height
	loadingRepos    map[string]bool // repos currently fetching PR details
	// Confirmation dialog state
	confirmAction   string      // "approve" or "merge"
	confirmPR       *protocol.PR
	statusMessage   string      // temporary status message
}

// repoGroup represents a repository with its PRs
type repoGroup struct {
	name      string
	prs       []*protocol.PR
	muted     bool
	collapsed bool
	loading   bool // fetching PR details
}

// NewModel creates a new dashboard model
func NewModel(c *client.Client) *Model {
	return &Model{
		client:         c,
		ghFetcher:      github.NewFetcher(),
		currentSession: getCurrentTmuxSession(),
		loadingRepos:   make(map[string]bool),
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
			loading:   m.loadingRepos[repo],
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

// prDetailsMsg is sent when PR details have been fetched
type prDetailsMsg struct {
	repo    string
	details map[int]*github.PRDetails // PR number -> details
	err     error
}

// prActionMsg is sent when a PR action completes
type prActionMsg struct {
	action  string // "approve" or "merge"
	pr      *protocol.PR
	success bool
	message string
}

// Update handles messages
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case tea.KeyMsg:
		// Clear status message on any key
		m.statusMessage = ""

		// Handle confirmation dialog
		if m.confirmAction != "" {
			switch msg.String() {
			case "y", "Y":
				// Execute the confirmed action
				pr := m.confirmPR
				action := m.confirmAction
				m.confirmAction = ""
				m.confirmPR = nil
				if action == "approve" {
					return m, m.approvePR(pr)
				} else if action == "merge" {
					return m, m.mergePR(pr)
				}
			case "n", "N", "esc":
				// Cancel
				m.confirmAction = ""
				m.confirmPR = nil
			}
			return m, nil
		}

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
		case "a":
			// Approve PR
			if m.focusPane == 1 {
				if pr := m.SelectedPR(); pr != nil && !pr.Muted {
					m.confirmAction = "approve"
					m.confirmPR = pr
				}
			}
		case "g":
			// Merge PR
			if m.focusPane == 1 {
				if pr := m.SelectedPR(); pr != nil && !pr.Muted {
					m.confirmAction = "merge"
					m.confirmPR = pr
				}
			}
		}
	case sessionsMsg:
		m.sessions = msg.sessions
		// Preserve detail fields from existing PRs
		oldPRs := make(map[string]*protocol.PR)
		for _, pr := range m.prs {
			oldPRs[pr.ID] = pr
		}
		for _, pr := range msg.prs {
			if old, ok := oldPRs[pr.ID]; ok && old.DetailsFetched {
				// Preserve detail fields if still valid
				if !old.LastUpdated.Before(pr.LastUpdated) {
					pr.DetailsFetched = old.DetailsFetched
					pr.DetailsFetchedAt = old.DetailsFetchedAt
					pr.Mergeable = old.Mergeable
					pr.MergeableState = old.MergeableState
					pr.CIStatus = old.CIStatus
					pr.ReviewStatus = old.ReviewStatus
				}
			}
		}
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
	case prDetailsMsg:
		// Clear loading state
		delete(m.loadingRepos, msg.repo)
		if msg.err != nil {
			// Log error but don't fail the UI
			return m, nil
		}
		// Update PRs with fetched details
		now := time.Now()
		for _, pr := range m.prs {
			if pr.Repo == msg.repo {
				if details, ok := msg.details[pr.Number]; ok {
					pr.DetailsFetched = true
					pr.DetailsFetchedAt = now
					pr.Mergeable = details.Mergeable
					pr.MergeableState = details.MergeableState
					pr.CIStatus = details.CIStatus
					pr.ReviewStatus = details.ReviewStatus
				}
			}
		}
		return m, nil
	case prActionMsg:
		if msg.success {
			m.statusMessage = fmt.Sprintf("✓ %s: %s", msg.action, msg.message)
		} else {
			m.statusMessage = fmt.Sprintf("✗ %s failed: %s", msg.action, msg.message)
		}
		// Refresh to get updated PR state
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
	// Get current displayed state (default is collapsed when no state exists)
	state := m.repoStates[repo]
	currentlyCollapsed := state == nil || state.Collapsed
	newCollapsed := !currentlyCollapsed

	// If we're expanding, check if we need to fetch PR details
	var fetchCmd tea.Cmd
	if !newCollapsed {
		// Check if any PRs in this repo need detail refresh
		needsFetch := false
		for _, pr := range m.prs {
			if pr.Repo == repo && pr.NeedsDetailRefresh() {
				needsFetch = true
				break
			}
		}
		if needsFetch && !m.loadingRepos[repo] {
			m.loadingRepos[repo] = true
			fetchCmd = m.fetchPRDetails(repo)
		}
	}

	collapseCmd := func() tea.Msg {
		if err := m.client.SetRepoCollapsed(repo, newCollapsed); err != nil {
			return errMsg{err: err}
		}
		return m.refresh()
	}

	if fetchCmd != nil {
		return tea.Batch(collapseCmd, fetchCmd)
	}
	return collapseCmd
}

// fetchPRDetails fetches detailed status for all PRs in a repo
func (m *Model) fetchPRDetails(repo string) tea.Cmd {
	// Collect PR numbers that need fetching
	var prNumbers []int
	for _, pr := range m.prs {
		if pr.Repo == repo && pr.NeedsDetailRefresh() {
			prNumbers = append(prNumbers, pr.Number)
		}
	}

	return func() tea.Msg {
		details := make(map[int]*github.PRDetails)
		for _, num := range prNumbers {
			d, err := m.ghFetcher.FetchPRDetails(repo, num)
			if err != nil {
				continue // Skip individual failures
			}
			details[num] = d
		}
		return prDetailsMsg{repo: repo, details: details}
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

func (m *Model) approvePR(pr *protocol.PR) tea.Cmd {
	return func() tea.Msg {
		// Use gh pr review --approve
		cmd := exec.Command("gh", "pr", "review", "--approve", pr.ID)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return prActionMsg{
				action:  "Approve",
				pr:      pr,
				success: false,
				message: strings.TrimSpace(string(output)),
			}
		}
		return prActionMsg{
			action:  "Approve",
			pr:      pr,
			success: true,
			message: fmt.Sprintf("#%d approved", pr.Number),
		}
	}
}

func (m *Model) mergePR(pr *protocol.PR) tea.Cmd {
	return func() tea.Msg {
		// Use gh pr merge with default strategy
		cmd := exec.Command("gh", "pr", "merge", pr.ID)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return prActionMsg{
				action:  "Merge",
				pr:      pr,
				success: false,
				message: strings.TrimSpace(string(output)),
			}
		}
		return prActionMsg{
			action:  "Merge",
			pr:      pr,
			success: true,
			message: fmt.Sprintf("#%d merged", pr.Number),
		}
	}
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

	// Calculate pane widths (sessions: 35%, PRs: 65%)
	// Account for borders (2 chars each) and gap (1 char)
	totalWidth := m.width
	if totalWidth < 60 {
		totalWidth = 80 // fallback
	}
	sessWidth := (totalWidth - 5) * 35 / 100
	prWidth := totalWidth - sessWidth - 5
	if sessWidth < 25 {
		sessWidth = 25
	}
	if prWidth < 30 {
		prWidth = 30
	}

	// Build sessions pane content
	sessContent := m.buildSessionsContent(sessWidth)
	sessBorder := unfocusedBorderStyle.Width(sessWidth)
	if m.focusPane == 0 {
		sessBorder = focusedBorderStyle.Width(sessWidth)
	}
	sessPane := sessBorder.Render(sessContent)

	// Build PRs pane content
	prContent := m.buildPRsContent(prWidth)
	prBorder := unfocusedBorderStyle.Width(prWidth)
	if m.focusPane == 1 {
		prBorder = focusedBorderStyle.Width(prWidth)
	}
	prPane := prBorder.Render(prContent)

	// Join horizontally
	content := lipgloss.JoinHorizontal(lipgloss.Top, sessPane, " ", prPane)

	// Legend
	legend := fmt.Sprintf("%s waiting  %s working  %s muted  |  %s yours  %s review",
		yellowStyle.Render("●"),
		greenStyle.Render("○"),
		grayStyle.Render("◌"),
		cyanStyle.Render("★"),
		magentaStyle.Render("◇"))

	// Status line (confirmation dialog or status message)
	var statusLine string
	if m.confirmAction != "" {
		actionVerb := m.confirmAction
		statusLine = yellowStyle.Render(fmt.Sprintf("» %s #%d? [y]es / [n]o", actionVerb, m.confirmPR.Number))
	} else if m.statusMessage != "" {
		if strings.HasPrefix(m.statusMessage, "✓") {
			statusLine = greenStyle.Render(m.statusMessage)
		} else {
			statusLine = redStyle.Render(m.statusMessage)
		}
	}

	help := legendStyle.Render("[←/→] Switch  [m] Mute  [a] Approve  [g] Merge  [Space] Open/Expand  [R] Restart  [q] Quit")

	result := content + "\n" + legend + "\n"
	if statusLine != "" {
		result += statusLine + "\n"
	}
	result += help + "\n"
	return result
}

func (m *Model) buildSessionsContent(width int) string {
	var lines []string
	lines = append(lines, headerStyle.Render(fmt.Sprintf("Sessions (%d)", len(m.sessions))))
	lines = append(lines, "")

	if len(m.sessions) == 0 {
		lines = append(lines, grayStyle.Render("  No active sessions"))
	} else {
		// Calculate label width: width - cursor(2) - indicator(2) - state(8)
		labelWidth := width - 12
		if labelWidth < 10 {
			labelWidth = 10
		}

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

			label := truncate(session.Label, labelWidth)
			line := fmt.Sprintf("%s%s %-*s %s", cursor, indicator, labelWidth, label, stateStr)
			lines = append(lines, style.Render(line))
		}
	}

	return strings.Join(lines, "\n")
}

func (m *Model) buildPRsContent(width int) string {
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

		loadingIndicator := ""
		if group.loading {
			loadingIndicator = " ⟳"
		}

		repoLine := fmt.Sprintf("%s%s %s (%d)%s", cursor, icon, repoShort, len(group.prs), loadingIndicator)
		lines = append(lines, style.Render(repoLine))
		prIndex++

		// Render PRs if expanded
		if !group.collapsed {
			for _, pr := range group.prs {
				cursor := "    " // extra indent for PRs
				if m.focusPane == 1 && m.prCursor == prIndex {
					cursor = "  > "
				}

				// Determine style based on role and state
				var style lipgloss.Style
				var roleIcon string
				if pr.Muted {
					style = grayStyle
					roleIcon = "◌"
				} else if pr.Role == protocol.PRRoleAuthor {
					style = cyanStyle
					roleIcon = "★" // your PR
				} else {
					style = magentaStyle
					roleIcon = "◇" // review request
				}

				// Build status string from detailed info if available
				statusStr := m.buildPRStatus(pr)

				prLine := fmt.Sprintf("%s%s #%d %s", cursor, roleIcon, pr.Number, statusStr)
				lines = append(lines, style.Render(prLine))

				// PR title on next line(s), wrapped to pane width minus indent
				titleWidth := width - 6
				if titleWidth < 20 {
					titleWidth = 20
				}
				titleLines := wrapText(pr.Title, titleWidth)
				for _, tl := range titleLines {
					lines = append(lines, grayStyle.Render("      "+tl))
				}

				prIndex++
			}
		}
	}

	return strings.Join(lines, "\n")
}

// buildPRStatus builds status string from PR details
func (m *Model) buildPRStatus(pr *protocol.PR) string {
	if pr.Muted {
		return "muted"
	}

	// If details not fetched yet, show basic status
	if !pr.DetailsFetched {
		if pr.Role == protocol.PRRoleReviewer {
			return "needs review"
		}
		return "open"
	}

	// Build status from detailed info
	var parts []string

	// CI status
	switch pr.CIStatus {
	case "success":
		parts = append(parts, "✓ci")
	case "failure":
		parts = append(parts, "✗ci")
	case "pending":
		parts = append(parts, "⋯ci")
	}

	// Review status
	switch pr.ReviewStatus {
	case "approved":
		parts = append(parts, "✓rev")
	case "changes_requested":
		parts = append(parts, "✗rev")
	case "pending", "none":
		if pr.Role == protocol.PRRoleAuthor {
			parts = append(parts, "⋯rev")
		}
	}

	// Mergeable status
	if pr.Mergeable != nil {
		if *pr.Mergeable {
			if pr.MergeableState == "clean" {
				parts = append(parts, "ready")
			}
		} else {
			parts = append(parts, "conflicts")
		}
	}

	if len(parts) == 0 {
		return "open"
	}
	return strings.Join(parts, " ")
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
