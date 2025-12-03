package dashboard

import (
	"fmt"
	"os/exec"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/victorarias/claude-manager/internal/client"
	"github.com/victorarias/claude-manager/internal/protocol"
)

// Model is the bubbletea model for the dashboard
type Model struct {
	client   *client.Client
	sessions []*protocol.Session
	cursor   int
	err      error
}

// NewModel creates a new dashboard model
func NewModel(c *client.Client) *Model {
	return &Model{
		client: c,
	}
}

// Init initializes the model
func (m *Model) Init() tea.Cmd {
	return tea.Batch(m.refresh, TickCmd())
}

// refresh fetches sessions from daemon
func (m *Model) refresh() tea.Msg {
	if m.client == nil {
		return sessionsMsg{sessions: nil}
	}
	sessions, err := m.client.Query("")
	if err != nil {
		return errMsg{err: err}
	}
	return sessionsMsg{sessions: sessions}
}

type sessionsMsg struct {
	sessions []*protocol.Session
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
		case "up", "k":
			m.moveCursor(-1)
		case "down", "j":
			m.moveCursor(1)
		case "r":
			return m, m.refresh
		case "enter":
			if s := m.SelectedSession(); s != nil && s.TmuxTarget != "" {
				return m, m.jumpToPane(s.TmuxTarget)
			}
		}
	case sessionsMsg:
		m.sessions = msg.sessions
		m.err = nil
		// Ensure cursor is valid
		if m.cursor >= len(m.sessions) && len(m.sessions) > 0 {
			m.cursor = len(m.sessions) - 1
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

// SelectedSession returns the currently selected session
func (m *Model) SelectedSession() *protocol.Session {
	if m.cursor >= 0 && m.cursor < len(m.sessions) {
		return m.sessions[m.cursor]
	}
	return nil
}

func (m *Model) jumpToPane(tmuxTarget string) tea.Cmd {
	c := exec.Command("tmux", "switch-client", "-t", "="+tmuxTarget)
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

	s := "Claude Sessions\n\n"

	for i, session := range m.sessions {
		cursor := "  "
		if i == m.cursor {
			cursor = "> "
		}

		indicator := "○"
		if session.State == protocol.StateWaiting {
			indicator = "●"
		}

		duration := formatDuration(time.Since(session.StateSince))
		todo := "(no todos)"
		if len(session.Todos) > 0 {
			todo = session.Todos[0]
			if len(todo) > 30 {
				todo = todo[:27] + "..."
			}
		}

		s += fmt.Sprintf("%s%s %-15s %-8s %8s   %s\n",
			cursor, indicator, session.Label, session.State, duration, todo)
	}

	s += "\n● = waiting (needs input)    ○ = working\n"
	s += "\n[Enter] Jump to pane   [r] Refresh   [q] Quit\n"

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
