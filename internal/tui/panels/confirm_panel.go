package panels

import (
	"strings"

	"charm.land/lipgloss/v2"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/cuppojoe/feather/internal/tui/shared"
)

// ConfirmPanel is a reusable yes/no modal. The caller stashes an Action token
// and an opaque Key when opening, and gets them back on confirmation so it
// knows what was confirmed.
type ConfirmPanel struct {
	expanded bool
	message  string
	action   string
	key      string
}

// ConfirmResult communicates the modal outcome.
type ConfirmResult struct {
	Confirmed bool
	Action    string
	Key       string
}

// NewConfirmPanel constructs the modal (closed).
func NewConfirmPanel() *ConfirmPanel { return &ConfirmPanel{} }

func (p *ConfirmPanel) IsExpanded() bool { return p.expanded }
func (p *ConfirmPanel) IsEditing() bool  { return p.expanded }

// Open shows a confirmation. action/key are returned verbatim on confirm so the
// caller can route the result.
func (p *ConfirmPanel) Open(message, action, key string) {
	p.expanded = true
	p.message = message
	p.action = action
	p.key = key
}

// Update handles input while open. y/enter confirms; n/esc cancels.
func (p *ConfirmPanel) Update(msg tea.Msg) ConfirmResult {
	if !p.expanded {
		return ConfirmResult{}
	}
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return ConfirmResult{}
	}
	switch key.String() {
	case "y", "Y", "enter":
		p.expanded = false
		return ConfirmResult{Confirmed: true, Action: p.action, Key: p.key}
	case "n", "N", "esc":
		p.expanded = false
		return ConfirmResult{Confirmed: false, Action: p.action, Key: p.key}
	}
	return ConfirmResult{}
}

// ViewModal renders the confirmation.
func (p *ConfirmPanel) ViewModal(screenWidth, screenHeight int) string {
	modalWidth := min(60, screenWidth-8)
	if modalWidth < 24 {
		modalWidth = 24
	}
	contentWidth := modalWidth - 6

	msg := lipgloss.NewStyle().Foreground(lipgloss.Color("#E5E7EB")).
		Width(contentWidth).Align(lipgloss.Center).Render(p.message)
	hint := lipgloss.NewStyle().Foreground(shared.ColorMuted).
		Width(contentWidth).Align(lipgloss.Center).Render("[y] yes  •  [n] no")
	divider := lipgloss.NewStyle().Foreground(shared.ColorBorder).Render(strings.Repeat("─", contentWidth))

	content := lipgloss.JoinVertical(lipgloss.Left, msg, divider, hint)
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(shared.ColorWarning).
		Padding(1, 2).
		Width(modalWidth).
		Render(content)
}
