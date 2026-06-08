package panels

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/cuppojoe/feather/internal/tui/shared"
)

// CategoryFormPanel is a one-field modal for creating or renaming a category.
type CategoryFormPanel struct {
	expanded bool
	rename   bool
	origName string
	input    textinput.Model
}

// CategoryFormResult communicates the form outcome.
type CategoryFormResult struct {
	Save     bool
	Rename   bool
	Name     string
	OrigName string
	Cmd      tea.Cmd
}

// NewCategoryFormPanel constructs the modal (closed).
func NewCategoryFormPanel() *CategoryFormPanel {
	ti := textinput.New()
	ti.CharLimit = 60
	ti.Placeholder = "category name"
	return &CategoryFormPanel{input: ti}
}

func (p *CategoryFormPanel) IsExpanded() bool { return p.expanded }
func (p *CategoryFormPanel) IsEditing() bool  { return p.expanded }

// OpenCreate opens the form to create a new category.
func (p *CategoryFormPanel) OpenCreate() {
	p.expanded = true
	p.rename = false
	p.origName = ""
	p.input.SetValue("")
	p.input.Focus()
}

// OpenRename opens the form to rename an existing category.
func (p *CategoryFormPanel) OpenRename(name string) {
	p.expanded = true
	p.rename = true
	p.origName = name
	p.input.SetValue(name)
	p.input.CursorEnd()
	p.input.Focus()
}

// Update handles input while open.
func (p *CategoryFormPanel) Update(msg tea.Msg) CategoryFormResult {
	if !p.expanded {
		return CategoryFormResult{}
	}
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "esc":
			p.expanded = false
			return CategoryFormResult{}
		case "enter":
			name := strings.TrimSpace(p.input.Value())
			if name == "" {
				return CategoryFormResult{}
			}
			p.expanded = false
			return CategoryFormResult{Save: true, Rename: p.rename, Name: name, OrigName: p.origName}
		}
	}
	var cmd tea.Cmd
	p.input, cmd = p.input.Update(msg)
	return CategoryFormResult{Cmd: cmd}
}

// ViewModal renders the form.
func (p *CategoryFormPanel) ViewModal(screenWidth, screenHeight int) string {
	modalWidth := min(50, screenWidth-8)
	if modalWidth < 20 {
		modalWidth = 20
	}
	contentWidth := modalWidth - 6

	title := "New Category"
	if p.rename {
		title = "Rename Category"
	}
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(shared.ColorPrimary).
		Width(contentWidth).Align(lipgloss.Center)
	divider := lipgloss.NewStyle().Foreground(shared.ColorBorder).Render(strings.Repeat("─", contentWidth))
	p.input.Width = contentWidth - 2
	hint := lipgloss.NewStyle().Foreground(shared.ColorMuted).Width(contentWidth).Align(lipgloss.Center).
		Render("[enter] save  •  [esc] cancel")

	content := lipgloss.JoinVertical(lipgloss.Left, titleStyle.Render(title), divider, p.input.View(), "", hint)
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(shared.ColorPrimary).
		Padding(1, 2).
		Width(modalWidth).
		Render(content)
}
