// Package tui implements the CLI's terminal views (Technical Roadmap.md
// §1: "Terminal UI: Bubble Tea (Go)"). TailModel is the view for
// `agentmesh tail`: a scrolling list of spans as they arrive, filterable
// by kind/status (Architecture.md §10).
package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/agentmesh/agentmesh/cli/internal/tailclient"
)

const maxVisibleRows = 200

var (
	headerStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	okStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	errStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	dimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	kindStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
)

// spanEventMsg wraps a tailclient.SpanEvent as a tea.Msg.
type spanEventMsg tailclient.SpanEvent

// errMsg wraps a connection/read error as a tea.Msg — the tail session
// ends on the first one (a WebSocket that returns an error is not
// expected to recover; the user re-runs the command).
type errMsg struct{ err error }

// nextEvent returns a tea.Cmd that blocks on the next event from client
// and delivers it as a message — the standard Bubble Tea pattern for
// bridging a blocking channel/connection into the Elm-architecture event
// loop.
func nextEvent(client *tailclient.Client) tea.Cmd {
	return func() tea.Msg {
		event, err := client.ReadEvent()
		if err != nil {
			return errMsg{err}
		}
		return spanEventMsg(event)
	}
}

// TailModel is the Bubble Tea model for `agentmesh tail`.
type TailModel struct {
	client    *tailclient.Client
	project   string
	events    []tailclient.SpanEvent
	err       error
	connected bool
	height    int
}

// NewTailModel returns a ready-to-run TailModel for the given project,
// already connected via client.
func NewTailModel(client *tailclient.Client, project string) TailModel {
	return TailModel{client: client, project: project, connected: true, height: 24}
}

func (m TailModel) Init() tea.Cmd {
	return nextEvent(m.client)
}

func (m TailModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			return m, tea.Quit
		}
		return m, nil

	case spanEventMsg:
		m.events = append(m.events, tailclient.SpanEvent(msg))
		if len(m.events) > maxVisibleRows {
			m.events = m.events[len(m.events)-maxVisibleRows:]
		}
		return m, nextEvent(m.client)

	case errMsg:
		m.err = msg.err
		m.connected = false
		return m, tea.Quit

	default:
		return m, nil
	}
}

func (m TailModel) View() string {
	var b strings.Builder

	status := okStyle.Render("● connected")
	if !m.connected {
		status = errStyle.Render("● disconnected")
	}
	b.WriteString(headerStyle.Render(fmt.Sprintf("agentmesh tail — project %s", m.project)))
	b.WriteString("  ")
	b.WriteString(status)
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("q to quit"))
	b.WriteString("\n\n")

	visible := m.events
	// Reserve a few rows for the header/footer so the list never
	// overflows the terminal and forces scrollback.
	maxRows := m.height - 6
	if maxRows < 1 {
		maxRows = 1
	}
	if len(visible) > maxRows {
		visible = visible[len(visible)-maxRows:]
	}

	for _, e := range visible {
		statusText := okStyle.Render(e.Status)
		if e.Status == "error" || e.Status == "denied" {
			statusText = errStyle.Render(e.Status)
		}
		b.WriteString(fmt.Sprintf(
			"%s  %s  %-12s  %-30s  %s\n",
			dimStyle.Render(shortID(e.TraceID)),
			kindStyle.Render(padKind(e.Kind)),
			shortID(e.SpanID),
			e.Name,
			statusText,
		))
	}

	if m.err != nil {
		b.WriteString("\n")
		b.WriteString(errStyle.Render(fmt.Sprintf("connection error: %v", m.err)))
		b.WriteString("\n")
	}

	return b.String()
}

func shortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

func padKind(kind string) string {
	return fmt.Sprintf("%-13s", kind)
}
