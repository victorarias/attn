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

	if len(m.sessions) == 0 {
		return "No active sessions\n\nPress 'r' to refresh, 'q' to quit"
	}

	s := "Claude Sessions\n"
	s += strings.Repeat("─", 60) + "\n"

	for i, session := range m.sessions {
		cursor := "  "
		if i == m.cursor {
			cursor = "> "
		}

		// Determine color and indicator based on state and muted
		var color, indicator, stateStr string
		if session.Muted {
			color = colorGray
			indicator = "◌"
			stateStr = "idle"
		} else if session.State == protocol.StateWaiting {
			color = colorYellow
			indicator = "●"
			stateStr = "waiting"
		} else {
			color = colorGreen
			indicator = "○"
			stateStr = "working"
		}

		duration := formatDuration(time.Since(session.StateSince))
		todoCount := ""
		if len(session.Todos) > 0 {
			todoCount = fmt.Sprintf("[%d todos]", len(session.Todos))
		}

		s += fmt.Sprintf("%s%s%s %-15s %-8s %8s  %s%s\n",
			cursor, color, indicator, session.Label, stateStr, duration, todoCount, colorReset)
	}

	s += strings.Repeat("─", 60) + "\n"

	// Detail panel for selected session
	if selected := m.SelectedSession(); selected != nil {
		s += fmt.Sprintf("\n%s\n", selected.Label)
		s += fmt.Sprintf("Directory: %s\n", selected.Directory)
		if selected.TmuxTarget != "" {
			s += fmt.Sprintf("Tmux: %s\n", selected.TmuxTarget)
		}

		s += "\nTodos:\n"
		if len(selected.Todos) == 0 {
			s += "  (no todos)\n"
		} else {
			for _, todo := range selected.Todos {
				s += fmt.Sprintf("  %s\n", todo)
			}
		}
	}

	s += strings.Repeat("─", 60) + "\n"
	s += fmt.Sprintf("%s●%s waiting  %s○%s working  %s◌%s idle\n",
		colorYellow, colorReset, colorGreen, colorReset, colorGray, colorReset)
	s += "[m] Idle   [x] Delete   [R] Restart   [Enter] Jump   [r] Refresh   [q] Quit\n"

	return s
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
