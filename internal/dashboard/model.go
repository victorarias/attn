package dashboard

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/victorarias/claude-manager/internal/client"
	"github.com/victorarias/claude-manager/internal/protocol"
)

// ANSI color codes
const (
	colorReset  = "\033[0m"
	colorYellow = "\033[33m" // waiting
	colorGreen  = "\033[32m" // working
	colorGray   = "\033[90m" // muted/idle
)

// Model is the bubbletea model for the dashboard
type Model struct {
	client         *client.Client
	sessions       []*protocol.Session
	prs            []*protocol.PR
	cursor         int
	prCursor       int
	focusPane      int  // 0 = sessions, 1 = PRs
	showMutedPRs   bool
	err            error
	currentSession string // current tmux session name
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

// refresh fetches sessions from daemon
func (m *Model) refresh() tea.Msg {
	if m.client == nil {
		return sessionsMsg{sessions: nil, prs: nil}
	}
	sessions, err := m.client.Query("")
	if err != nil {
		return errMsg{err: err}
	}
	prs, _ := m.client.QueryPRs("") // Ignore error, PRs are optional
	return sessionsMsg{sessions: sessions, prs: prs}
}

type sessionsMsg struct {
	sessions []*protocol.Session
	prs      []*protocol.PR
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
		case "tab", "l":
			if m.focusPane == 0 {
				m.focusPane = 1
			} else {
				m.focusPane = 0
			}
		case "h":
			m.focusPane = 0
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
		case "enter":
			if m.focusPane == 0 {
				if s := m.SelectedSession(); s != nil && s.TmuxTarget != "" {
					return m, m.jumpToPane(s.TmuxTarget)
				}
			} else {
				if pr := m.SelectedPR(); pr != nil {
					return m, m.openPRInBrowser(pr.URL)
				}
			}
		case "m":
			if m.focusPane == 0 {
				if s := m.SelectedSession(); s != nil {
					return m, m.toggleMute(s.ID)
				}
			} else {
				if pr := m.SelectedPR(); pr != nil {
					return m, m.toggleMutePR(pr.ID)
				}
			}
		case "M":
			m.showMutedPRs = !m.showMutedPRs
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
		m.err = nil
		// Ensure cursors are valid
		if m.cursor >= len(m.sessions) && len(m.sessions) > 0 {
			m.cursor = len(m.sessions) - 1
		}
		visiblePRs := m.getVisiblePRs()
		if m.prCursor >= len(visiblePRs) && len(visiblePRs) > 0 {
			m.prCursor = len(visiblePRs) - 1
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
	visiblePRs := m.getVisiblePRs()
	if m.prCursor < 0 {
		m.prCursor = 0
	}
	if m.prCursor >= len(visiblePRs) && len(visiblePRs) > 0 {
		m.prCursor = len(visiblePRs) - 1
	}
}

// SelectedSession returns the currently selected session
func (m *Model) SelectedSession() *protocol.Session {
	if m.cursor >= 0 && m.cursor < len(m.sessions) {
		return m.sessions[m.cursor]
	}
	return nil
}

// SelectedPR returns the currently selected PR
func (m *Model) SelectedPR() *protocol.PR {
	visiblePRs := m.getVisiblePRs()
	if m.prCursor >= 0 && m.prCursor < len(visiblePRs) {
		return visiblePRs[m.prCursor]
	}
	return nil
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

// View renders the dashboard
func (m *Model) View() string {
	if m.err != nil {
		return fmt.Sprintf("Error: %v\n\nPress 'r' to retry, 'q' to quit", m.err)
	}

	// Get terminal width (default to 120 if can't detect)
	width := 120

	// Calculate pane widths
	leftWidth := width/2 - 2
	rightWidth := width/2 - 2

	// Build left pane (sessions)
	leftLines := m.renderSessionsPane(leftWidth)

	// Build right pane (PRs)
	rightLines := m.renderPRsPane(rightWidth)

	// Ensure both panes have same height
	maxLines := len(leftLines)
	if len(rightLines) > maxLines {
		maxLines = len(rightLines)
	}
	for len(leftLines) < maxLines {
		leftLines = append(leftLines, strings.Repeat(" ", leftWidth))
	}
	for len(rightLines) < maxLines {
		rightLines = append(rightLines, strings.Repeat(" ", rightWidth))
	}

	// Combine panes
	var s strings.Builder

	// Header
	leftHeader := " Sessions "
	rightHeader := fmt.Sprintf(" Pull Requests (%d) ", len(m.getVisiblePRs()))
	if m.focusPane == 0 {
		leftHeader = "[" + leftHeader + "]"
	} else {
		rightHeader = "[" + rightHeader + "]"
	}

	s.WriteString(fmt.Sprintf("┌─%s%s┬─%s%s┐\n",
		leftHeader, strings.Repeat("─", leftWidth-len(leftHeader)-1),
		rightHeader, strings.Repeat("─", rightWidth-len(rightHeader)-1)))

	for i := 0; i < maxLines; i++ {
		s.WriteString(fmt.Sprintf("│ %s│ %s│\n", padRight(leftLines[i], leftWidth-1), padRight(rightLines[i], rightWidth-1)))
	}

	s.WriteString(fmt.Sprintf("└%s┴%s┘\n", strings.Repeat("─", leftWidth), strings.Repeat("─", rightWidth)))

	// Legend
	s.WriteString(fmt.Sprintf("%s●%s waiting  %s○%s working  %s◌%s muted\n",
		colorYellow, colorReset, colorGreen, colorReset, colorGray, colorReset))
	s.WriteString("[Tab] Switch pane  [m] Mute  [M] Show muted PRs  [Enter] Open  [r] Refresh  [q] Quit\n")

	return s.String()
}

func (m *Model) renderSessionsPane(width int) []string {
	var lines []string

	if len(m.sessions) == 0 {
		lines = append(lines, "No active sessions")
		return lines
	}

	for i, session := range m.sessions {
		cursor := "  "
		if i == m.cursor && m.focusPane == 0 {
			cursor = "> "
		}

		var color, indicator, stateStr string
		if session.Muted {
			color = colorGray
			indicator = "◌"
			stateStr = "muted"
		} else if session.State == protocol.StateWaiting {
			color = colorYellow
			indicator = "●"
			stateStr = "waiting"
		} else {
			color = colorGreen
			indicator = "○"
			stateStr = "working"
		}

		line := fmt.Sprintf("%s%s%s %-12s %s%s",
			cursor, color, indicator, truncate(session.Label, 12), stateStr, colorReset)
		lines = append(lines, line)
	}

	return lines
}

func (m *Model) renderPRsPane(width int) []string {
	var lines []string

	visiblePRs := m.getVisiblePRs()

	if len(visiblePRs) == 0 {
		if len(m.prs) == 0 {
			lines = append(lines, "No PRs (gh CLI?)")
		} else {
			lines = append(lines, "All PRs muted")
		}
		return lines
	}

	for i, pr := range visiblePRs {
		cursor := "  "
		if i == m.prCursor && m.focusPane == 1 {
			cursor = "> "
		}

		var color, stateStr string
		if pr.Muted {
			color = colorGray
			stateStr = "muted"
		} else if pr.State == protocol.StateWaiting {
			color = colorYellow
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
				stateStr = "wait"
			}
		} else {
			color = colorGreen
			stateStr = "wait"
		}

		// Format: ⬡ repo#123  state
		repoShort := truncate(pr.Repo, 15)
		line := fmt.Sprintf("%s%s⬡ %s#%d %s%s",
			cursor, color, repoShort, pr.Number, stateStr, colorReset)
		lines = append(lines, line)
	}

	return lines
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-1] + "…"
}

func padRight(s string, length int) string {
	// Strip ANSI codes for length calculation
	visible := stripANSI(s)
	padding := length - len(visible)
	if padding <= 0 {
		return s
	}
	return s + strings.Repeat(" ", padding)
}

func stripANSI(s string) string {
	// Simple ANSI stripper for length calculation
	result := s
	for _, code := range []string{colorReset, colorYellow, colorGreen, colorGray} {
		result = strings.ReplaceAll(result, code, "")
	}
	return result
}

func formatDuration(d time.Duration) string {
	minutes := int(d.Minutes())
	seconds := int(d.Seconds()) % 60
	return fmt.Sprintf("%dm %02ds", minutes, seconds)
}

// TickCmd returns a command that ticks for auto-refresh
func TickCmd() tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg {
		return tickMsg{}
	})
}
