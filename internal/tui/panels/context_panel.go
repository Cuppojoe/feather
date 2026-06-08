package panels

import (
	"fmt"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/cuppojoe/feather/internal/models"
	"github.com/cuppojoe/feather/internal/tui/shared"
)

// ContextPanel displays and edits context key/value pairs
type ContextPanel struct {
	pathVars    []string          // Variables extracted from spec
	context     *models.Context   // Current context
	values      map[string]string // Edited values
	customVars  []string          // User-added custom variables
	keys        []string          // All keys for display (sorted)
	cursor      int
	editing     bool
	input       textinput.Model
	addingNew   bool
	newKeyInput textinput.Model
	focused     bool
	expanded    bool // Modal open state
	keyMap      shared.KeyMap
	width       int
	height      int
	clickMap    shared.ClickMap
	footerHints shared.HintBar // clickable shortcut hints in the modal footer
}

// FooterHintAt resolves a click in modal-content coordinates to a footer
// shortcut key, if one was hit.
func (c *ContextPanel) FooterHintAt(x, y int) (string, bool) {
	return c.footerHints.HitKey(x, y)
}

// SetPathVariables updates the spec-derived path variables shown in the
// context panel (used after overlay edits add new path params).
func (c *ContextPanel) SetPathVariables(vars []string) {
	c.pathVars = vars
	if c.cursor >= len(c.allVars()) {
		c.cursor = 0
	}
}

// ContextPanelResult is the result of a context panel update
type ContextPanelResult struct {
	Save   bool
	Values map[string]string
	Cmd    tea.Cmd
}

// NewContextPanel creates a new context panel
func NewContextPanel(pathVars []string, ctx *models.Context, keys shared.KeyMap) *ContextPanel {
	input := textinput.New()
	input.CharLimit = 200

	newKeyInput := textinput.New()
	newKeyInput.Placeholder = "variable name"
	newKeyInput.CharLimit = 50

	// Copy current values
	values := make(map[string]string)
	for k, v := range ctx.Values {
		values[k] = v
	}

	// Find custom variables (not in pathVars)
	pathVarSet := make(map[string]bool)
	for _, v := range pathVars {
		pathVarSet[v] = true
	}

	var customVars []string
	for k := range values {
		if !pathVarSet[k] {
			customVars = append(customVars, k)
		}
	}
	sort.Strings(customVars)

	return &ContextPanel{
		pathVars:    pathVars,
		context:     ctx,
		values:      values,
		customVars:  customVars,
		input:       input,
		newKeyInput: newKeyInput,
		keyMap:      keys,
	}
}

// Get retrieves a context value by key
func (c *ContextPanel) Get(key string) string {
	return c.values[key]
}

// SetFocused sets the focus state
func (c *ContextPanel) SetFocused(focused bool) {
	c.focused = focused
	if !focused {
		c.editing = false
		c.addingNew = false
		c.input.Blur()
		c.newKeyInput.Blur()
	}
}

// IsFocused returns whether the panel is focused
func (c *ContextPanel) IsFocused() bool {
	return c.focused
}

// IsEditing returns whether the panel is currently editing a field
func (c *ContextPanel) IsEditing() bool {
	return c.editing || c.addingNew
}

// IsExpanded returns whether the panel is expanded as a modal
func (c *ContextPanel) IsExpanded() bool {
	return c.expanded
}

// Toggle toggles expanded state
func (c *ContextPanel) Toggle() {
	c.expanded = !c.expanded
	if !c.expanded {
		c.editing = false
		c.addingNew = false
		c.input.Blur()
		c.newKeyInput.Blur()
	}
}

// GetValues returns all current values
func (c *ContextPanel) GetValues() map[string]string {
	result := make(map[string]string)
	for k, v := range c.values {
		result[k] = v
	}
	return result
}

// allVars returns all variables (path vars + custom vars)
func (c *ContextPanel) allVars() []string {
	result := make([]string, 0, len(c.pathVars)+len(c.customVars))
	result = append(result, c.pathVars...)
	result = append(result, c.customVars...)
	return result
}

// Update handles input for the context panel
func (c *ContextPanel) Update(msg tea.Msg) ContextPanelResult {
	var cmd tea.Cmd

	if !c.focused {
		return ContextPanelResult{}
	}

	// Handle adding new variable
	if c.addingNew {
		switch msg := msg.(type) {
		case tea.KeyMsg:
			switch msg.String() {
			case "enter":
				newKey := strings.TrimSpace(c.newKeyInput.Value())
				if newKey != "" {
					// Check if it already exists
					exists := false
					for _, v := range c.allVars() {
						if v == newKey {
							exists = true
							break
						}
					}
					if !exists {
						c.customVars = append(c.customVars, newKey)
						sort.Strings(c.customVars)
						c.values[newKey] = ""
					}
				}
				c.addingNew = false
				c.newKeyInput.SetValue("")
				c.newKeyInput.Blur()
				return ContextPanelResult{}
			case "esc":
				c.addingNew = false
				c.newKeyInput.SetValue("")
				c.newKeyInput.Blur()
				return ContextPanelResult{}
			}
		}

		c.newKeyInput, cmd = c.newKeyInput.Update(msg)
		return ContextPanelResult{Cmd: cmd}
	}

	// Handle editing value
	if c.editing {
		switch msg := msg.(type) {
		case tea.KeyMsg:
			switch msg.String() {
			case "enter":
				vars := c.allVars()
				if c.cursor < len(vars) {
					c.values[vars[c.cursor]] = c.input.Value()
					// Also update the underlying context
					c.context.Set(vars[c.cursor], c.input.Value())
				}
				c.editing = false
				c.input.Blur()
				return ContextPanelResult{Save: true, Values: c.values}
			case "esc":
				c.editing = false
				c.input.Blur()
				return ContextPanelResult{}
			}
		}

		c.input, cmd = c.input.Update(msg)
		return ContextPanelResult{Cmd: cmd}
	}

	// Normal navigation
	switch msg := msg.(type) {
	case tea.MouseMsg:
		vars := c.allVars()
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			if c.cursor > 0 {
				c.cursor--
			}
		case tea.MouseButtonWheelDown:
			if c.cursor < len(vars) {
				c.cursor++
			}
		case tea.MouseButtonLeft:
			if msg.Action == tea.MouseActionRelease {
				if idx, ok := c.clickMap.Hit(msg.X, msg.Y); ok {
					c.cursor = idx
					if idx < len(vars) {
						c.editing = true
						c.input.SetValue(c.values[vars[c.cursor]])
						c.input.Focus()
						return ContextPanelResult{Cmd: textinput.Blink}
					}
					// "+ Add variable" row
					c.addingNew = true
					c.newKeyInput.Focus()
					return ContextPanelResult{Cmd: textinput.Blink}
				}
			}
		}

	case tea.KeyMsg:
		vars := c.allVars()

		switch {
		case key.Matches(msg, c.keyMap.Up):
			if c.cursor > 0 {
				c.cursor--
			}
		case key.Matches(msg, c.keyMap.Down):
			if c.cursor < len(vars) {
				c.cursor++
			}
		case key.Matches(msg, c.keyMap.Enter):
			if c.cursor < len(vars) {
				c.editing = true
				c.input.SetValue(c.values[vars[c.cursor]])
				c.input.Focus()
				return ContextPanelResult{Cmd: textinput.Blink}
			} else {
				c.addingNew = true
				c.newKeyInput.Focus()
				return ContextPanelResult{Cmd: textinput.Blink}
			}
		case key.Matches(msg, c.keyMap.Save):
			return ContextPanelResult{Save: true, Values: c.values}

		case msg.String() == "d", msg.String() == "delete":
			// Delete custom variable
			vars := c.allVars()
			if c.cursor >= len(c.pathVars) && c.cursor < len(vars) {
				idx := c.cursor - len(c.pathVars)
				if idx >= 0 && idx < len(c.customVars) {
					varName := c.customVars[idx]
					delete(c.values, varName)
					c.context.Delete(varName)
					c.customVars = append(c.customVars[:idx], c.customVars[idx+1:]...)
					if c.cursor >= len(c.allVars()) && c.cursor > 0 {
						c.cursor--
					}
				}
			}
		}
	}

	return ContextPanelResult{Cmd: cmd}
}

// View renders nothing - context is now a modal only
func (c *ContextPanel) View(width, height int) string {
	return ""
}

// ViewModal renders the context panel as a modal
func (c *ContextPanel) ViewModal(screenWidth, screenHeight int) string {
	modalWidth := min(70, screenWidth-8)
	modalHeight := min(20, screenHeight-8)
	contentWidth := modalWidth - 6

	c.width = modalWidth
	c.height = modalHeight

	vars := c.allVars()
	maxLines := modalHeight - 8 // Account for title, hints, borders
	if maxLines < 1 {
		maxLines = 1
	}

	// Calculate starting index for scrolling
	startIdx := 0
	if c.cursor >= maxLines {
		startIdx = c.cursor - maxLines + 1
	}

	// Build rows. The modal content begins at content-relative Y=0 with
	// title/hint/divider taking rows 0..2, so the first variable row sits
	// at Y=3.
	c.clickMap.Reset()
	const firstRowY = 3
	var rows []string
	for i := startIdx; i < len(vars) && len(rows) < maxLines; i++ {
		c.clickMap.AddRow(firstRowY+(i-startIdx), i)
		varName := vars[i]
		cursor := "  "
		nameStyle := lipgloss.NewStyle().Foreground(shared.ColorMuted).Width(15)
		valueStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#E5E7EB"))

		if i == c.cursor {
			cursor = shared.CursorStyle.Render("> ")
			nameStyle = nameStyle.Foreground(shared.ColorPrimary).Bold(true)
		}

		value := c.values[varName]
		if value == "" {
			value = "(not set)"
			valueStyle = valueStyle.Foreground(shared.ColorMuted)
		}

		// Truncate value for display
		maxValueLen := contentWidth - 20
		if maxValueLen > 0 && len(value) > maxValueLen {
			value = value[:maxValueLen-3] + "..."
		}

		if c.editing && i == c.cursor {
			rows = append(rows, fmt.Sprintf("%s%s %s", cursor, nameStyle.Render(varName), c.input.View()))
		} else {
			rows = append(rows, fmt.Sprintf("%s%s %s", cursor, nameStyle.Render(varName), valueStyle.Render(value)))
		}
	}

	// Add new option
	if len(rows) < maxLines {
		addCursor := "  "
		addStyle := lipgloss.NewStyle().Foreground(shared.ColorMuted)
		if c.cursor == len(vars) {
			addCursor = shared.CursorStyle.Render("> ")
			addStyle = addStyle.Foreground(shared.ColorPrimary).Bold(true)
		}

		// Register click target for the "+ Add variable" row.
		c.clickMap.AddRow(firstRowY+len(rows), len(vars))

		if c.addingNew {
			rows = append(rows, fmt.Sprintf("%s%s", addCursor, c.newKeyInput.View()))
		} else {
			rows = append(rows, fmt.Sprintf("%s%s", addCursor, addStyle.Render("+ Add variable")))
		}
	}

	// Title and hints
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(shared.ColorPrimary).
		Width(contentWidth).
		Align(lipgloss.Center)

	hintStyle := lipgloss.NewStyle().
		Foreground(shared.ColorMuted).
		Width(contentWidth).
		Align(lipgloss.Center)

	dividerStyle := lipgloss.NewStyle().
		Foreground(shared.ColorBorder)

	title := titleStyle.Render("Context Variables")
	// Clickable hint row at content-relative Y=1 (title is Y=0). The row is
	// centered, so the click regions start at the same left pad lipgloss adds.
	hintItems := []shared.Hint{
		{Key: "esc", Label: "close"},
		{Key: "enter", Label: "edit"},
		{Key: "d", Label: "delete"},
	}
	hintLeftPad := (contentWidth - shared.HintsWidth(hintItems, true, " • ")) / 2
	if hintLeftPad < 0 {
		hintLeftPad = 0
	}
	hint := c.footerHints.Render(hintItems, 1, hintLeftPad, true, " • ", hintStyle)
	divider := dividerStyle.Render(strings.Repeat("─", contentWidth))

	// Content area
	contentStyle := lipgloss.NewStyle().
		Width(contentWidth).
		Height(maxLines)

	content := lipgloss.JoinVertical(lipgloss.Left,
		title,
		hint,
		divider,
		contentStyle.Render(strings.Join(rows, "\n")),
	)

	// Modal container. Width is the total rendered width (lipgloss v2 counts
	// padding and border inside Width). contentWidth = modalWidth - 6 is the
	// area inner elements must fit into.
	modalStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(shared.ColorPrimary).
		Padding(1, 2).
		Width(modalWidth)

	return modalStyle.Render(content)
}
