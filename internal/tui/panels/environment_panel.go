package panels

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/cuppojoe/feather/internal/config"
	"github.com/cuppojoe/feather/internal/tui/shared"
)

// envView is the screen the environment modal is currently on.
type envView int

const (
	envViewList envView = iota
	envViewEdit
)

// envEditField marks which one-line prompt is active in the list view.
type envEditField int

const (
	envEditNone envEditField = iota
	envEditCreate
	envEditCopy
	envEditRename
)

// EnvironmentPanel is the modal picker + editor for swappable named
// contexts (Postman-style environments). The list view manages the env
// catalogue (switch active / create / copy / rename / delete) and the
// edit view embeds an inline key/value editor for the selected env's
// values.
type EnvironmentPanel struct {
	envs       []*config.Environment
	activeName string // the env name the host currently has applied
	cursor     int

	expanded bool
	view     envView

	// Inline name prompt (create / copy / rename). Only one is active at
	// a time and identified by editing.
	field   envEditField // which prompt is live
	input   textinput.Model
	confirm bool   // delete confirmation pending
	status  string // transient banner under the list

	// Key/value editor — used when view == envViewEdit. Saves on ctrl+s,
	// which asks the host to persist + reapply.
	kv       shared.KVEditor
	editName string // env being edited

	keys shared.KeyMap
}

// EnvironmentPanelResult mirrors the other panel-result types. The host
// reads these to persist changes and rebuild the live context.
type EnvironmentPanelResult struct {
	// SetActive carries the env name the user just selected (empty means
	// "no env" — fall back to bare profile context).
	SetActive     bool
	ActiveName    string
	ActiveCleared bool

	// Saved carries a save-and-apply intent for the editor.
	Saved    bool
	SavedEnv *config.Environment

	Cmd tea.Cmd
}

// NewEnvironmentPanel constructs an empty (collapsed) panel. The host
// calls Open() to populate it with the current state.
func NewEnvironmentPanel(keys shared.KeyMap) *EnvironmentPanel {
	ti := textinput.New()
	ti.CharLimit = 100
	return &EnvironmentPanel{
		keys:  keys,
		input: ti,
		kv:    shared.NewKVEditor(),
	}
}

// Open populates the modal with the current env catalogue + active name
// and expands it. activeName is the env the host considers live (may be
// empty for "none").
func (p *EnvironmentPanel) Open(activeName string) {
	p.activeName = activeName
	p.reload()
	p.expanded = true
	p.view = envViewList
	p.confirm = false
	p.status = ""
	p.field = envEditNone
	p.input.Blur()
	p.input.SetValue("")
	// Land the cursor on the active env so the user sees a familiar
	// position when the modal pops.
	for i, env := range p.envs {
		if env.Name == activeName {
			p.cursor = i
			return
		}
	}
	p.cursor = 0
}

func (p *EnvironmentPanel) reload() {
	envs, _ := config.ListEnvironments()
	p.envs = envs
	if p.cursor >= len(p.envs) {
		p.cursor = len(p.envs) - 1
	}
	if p.cursor < 0 {
		p.cursor = 0
	}
}

// IsExpanded reports whether the modal is rendered.
func (p *EnvironmentPanel) IsExpanded() bool { return p.expanded }

// IsEditing reports when keyboard input should bypass the host's global
// single-key bindings (an inline prompt is open or the K/V editor is
// focused).
func (p *EnvironmentPanel) IsEditing() bool {
	if !p.expanded {
		return false
	}
	if p.field != envEditNone {
		return true
	}
	if p.view == envViewEdit {
		return true
	}
	return false
}

// Update handles a single bubbletea message while the modal is open.
func (p *EnvironmentPanel) Update(msg tea.Msg) EnvironmentPanelResult {
	if !p.expanded {
		return EnvironmentPanelResult{}
	}

	if p.field != envEditNone {
		return p.updatePrompt(msg)
	}

	if p.view == envViewEdit {
		return p.updateEdit(msg)
	}

	if mouse, ok := msg.(tea.MouseMsg); ok {
		return p.updateMouse(mouse)
	}

	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return EnvironmentPanelResult{}
	}
	return p.updateList(km)
}

func (p *EnvironmentPanel) updateMouse(mm tea.MouseMsg) EnvironmentPanelResult {
	switch mm.Button {
	case tea.MouseButtonWheelUp:
		if p.cursor > 0 {
			p.cursor--
		}
	case tea.MouseButtonWheelDown:
		if p.cursor < len(p.envs)-1 {
			p.cursor++
		}
	}
	return EnvironmentPanelResult{}
}

func (p *EnvironmentPanel) updateList(km tea.KeyMsg) EnvironmentPanelResult {
	// Any non-confirm keystroke cancels a pending delete.
	if p.confirm && km.String() != "x" && km.String() != "esc" {
		p.confirm = false
		p.status = ""
	}

	switch {
	case km.String() == "esc":
		if p.confirm {
			p.confirm = false
			p.status = ""
			return EnvironmentPanelResult{}
		}
		p.expanded = false
		return EnvironmentPanelResult{}

	case key.Matches(km, p.keys.Up):
		if p.cursor > 0 {
			p.cursor--
		}
	case key.Matches(km, p.keys.Down):
		if p.cursor < len(p.envs)-1 {
			p.cursor++
		}
	case km.String() == "home", km.String() == "g":
		p.cursor = 0
	case km.String() == "end", km.String() == "G":
		p.cursor = len(p.envs) - 1
		if p.cursor < 0 {
			p.cursor = 0
		}

	case key.Matches(km, p.keys.Enter):
		// Switch the active env to the highlighted one.
		sel := p.selected()
		if sel == nil {
			return EnvironmentPanelResult{}
		}
		if sel.Name == p.activeName {
			// Already active — close.
			p.expanded = false
			return EnvironmentPanelResult{}
		}
		p.activeName = sel.Name
		res := EnvironmentPanelResult{
			SetActive:  true,
			ActiveName: sel.Name,
		}
		p.expanded = false
		return res

	case km.String() == "0":
		// Clear active environment — back to bare profile context.
		if p.activeName == "" {
			p.expanded = false
			return EnvironmentPanelResult{}
		}
		p.activeName = ""
		res := EnvironmentPanelResult{
			SetActive:     true,
			ActiveCleared: true,
		}
		p.expanded = false
		return res

	case km.String() == "n":
		p.field = envEditCreate
		p.input.Placeholder = "new environment name"
		p.input.SetValue("")
		p.input.Focus()
		return EnvironmentPanelResult{Cmd: textinput.Blink}

	case km.String() == "y":
		sel := p.selected()
		if sel == nil {
			return EnvironmentPanelResult{}
		}
		p.field = envEditCopy
		p.input.Placeholder = "name for the copy"
		p.input.SetValue(sel.Name + "-copy")
		p.input.CursorEnd()
		p.input.Focus()
		return EnvironmentPanelResult{Cmd: textinput.Blink}

	case km.String() == "r":
		sel := p.selected()
		if sel == nil {
			return EnvironmentPanelResult{}
		}
		p.field = envEditRename
		p.input.Placeholder = "new name"
		p.input.SetValue(sel.Name)
		p.input.CursorEnd()
		p.input.Focus()
		return EnvironmentPanelResult{Cmd: textinput.Blink}

	case km.String() == "e":
		sel := p.selected()
		if sel == nil {
			return EnvironmentPanelResult{}
		}
		p.openEditor(sel)
		return EnvironmentPanelResult{Cmd: p.kv.Focus()}

	case km.String() == "x":
		sel := p.selected()
		if sel == nil {
			return EnvironmentPanelResult{}
		}
		if !p.confirm {
			p.confirm = true
			p.status = shared.WarningStyle.Render(
				fmt.Sprintf("Press x again to delete %q (esc to cancel)", sel.Name))
			return EnvironmentPanelResult{}
		}
		p.confirm = false
		if err := config.DeleteEnvironment(sel.Name); err != nil {
			p.status = shared.ErrorStyle.Render("Delete failed: " + err.Error())
			return EnvironmentPanelResult{}
		}
		// If the deleted env was active, clear it.
		var res EnvironmentPanelResult
		if sel.Name == p.activeName {
			p.activeName = ""
			res = EnvironmentPanelResult{SetActive: true, ActiveCleared: true}
		}
		p.reload()
		p.status = shared.SuccessStyle.Render(fmt.Sprintf("Deleted %q", sel.Name))
		return res
	}
	return EnvironmentPanelResult{}
}

func (p *EnvironmentPanel) updatePrompt(msg tea.Msg) EnvironmentPanelResult {
	if km, ok := msg.(tea.KeyMsg); ok {
		switch km.String() {
		case "esc":
			p.cancelPrompt()
			return EnvironmentPanelResult{}
		case "enter":
			return p.commitPrompt()
		}
	}
	var cmd tea.Cmd
	p.input, cmd = p.input.Update(msg)
	return EnvironmentPanelResult{Cmd: cmd}
}

func (p *EnvironmentPanel) cancelPrompt() {
	p.field = envEditNone
	p.input.Blur()
	p.input.SetValue("")
}

func (p *EnvironmentPanel) commitPrompt() EnvironmentPanelResult {
	newValue := strings.TrimSpace(p.input.Value())
	field := p.field
	p.cancelPrompt()

	switch field {
	case envEditCreate:
		if newValue == "" {
			p.status = shared.ErrorStyle.Render("Environment name cannot be empty")
			return EnvironmentPanelResult{}
		}
		if _, err := config.LoadEnvironment(newValue); err == nil {
			p.status = shared.ErrorStyle.Render(
				fmt.Sprintf("%q already exists", newValue))
			return EnvironmentPanelResult{}
		}
		env := config.NewEnvironment(newValue)
		if err := env.Save(); err != nil {
			p.status = shared.ErrorStyle.Render("Create failed: " + err.Error())
			return EnvironmentPanelResult{}
		}
		p.reload()
		for i, e := range p.envs {
			if e.Name == newValue {
				p.cursor = i
				break
			}
		}
		p.openEditor(env)
		return EnvironmentPanelResult{Cmd: p.kv.Focus()}

	case envEditCopy:
		sel := p.selected()
		if sel == nil {
			return EnvironmentPanelResult{}
		}
		if newValue == "" {
			p.status = shared.ErrorStyle.Render("Environment name cannot be empty")
			return EnvironmentPanelResult{}
		}
		if err := config.CopyEnvironment(sel.Name, newValue); err != nil {
			p.status = shared.ErrorStyle.Render("Copy failed: " + err.Error())
			return EnvironmentPanelResult{}
		}
		p.reload()
		for i, e := range p.envs {
			if e.Name == newValue {
				p.cursor = i
				break
			}
		}
		p.status = shared.SuccessStyle.Render(
			fmt.Sprintf("Copied %q to %q", sel.Name, newValue))

	case envEditRename:
		sel := p.selected()
		if sel == nil {
			return EnvironmentPanelResult{}
		}
		if newValue == "" {
			p.status = shared.ErrorStyle.Render("Environment name cannot be empty")
			return EnvironmentPanelResult{}
		}
		if newValue == sel.Name {
			return EnvironmentPanelResult{}
		}
		oldName := sel.Name
		if err := config.RenameEnvironment(oldName, newValue); err != nil {
			p.status = shared.ErrorStyle.Render("Rename failed: " + err.Error())
			return EnvironmentPanelResult{}
		}
		p.reload()
		for i, e := range p.envs {
			if e.Name == newValue {
				p.cursor = i
				break
			}
		}
		// If we renamed the active env, notify the host so it can update
		// the profile's stored active-env name.
		var res EnvironmentPanelResult
		if oldName == p.activeName {
			p.activeName = newValue
			res = EnvironmentPanelResult{SetActive: true, ActiveName: newValue}
		}
		p.status = shared.SuccessStyle.Render(
			fmt.Sprintf("Renamed to %q", newValue))
		return res
	}
	return EnvironmentPanelResult{}
}

func (p *EnvironmentPanel) openEditor(env *config.Environment) {
	p.view = envViewEdit
	p.editName = env.Name
	p.kv.SetValues(env.PlainValues())
	p.kv.SetSensitive(env.SensitiveKeys())
	p.kv.Focus()
	p.status = ""
}

func (p *EnvironmentPanel) updateEdit(msg tea.Msg) EnvironmentPanelResult {
	if km, ok := msg.(tea.KeyMsg); ok {
		// Esc backs out only when we're not mid-edit on a cell — let the
		// K/V editor swallow the keystroke first if it has one in flight.
		if km.String() == "esc" && !p.kv.IsEditing() {
			p.view = envViewList
			p.kv.Blur()
			return EnvironmentPanelResult{}
		}
	}
	cmd := p.kv.Update(msg)
	// Auto-save after every commit. KVEditor only flips IsDirty at the
	// commit points (Enter / Tab on an inline edit, `c` to clear, `d` to
	// delete), so we can persist eagerly without thrashing every
	// keystroke. The host (App.updateEnvironmentModal) re-applies if this
	// env is currently active.
	if p.kv.IsDirty() {
		res := p.saveEditor()
		if res.Cmd == nil {
			res.Cmd = cmd
		} else if cmd != nil {
			res.Cmd = tea.Batch(res.Cmd, cmd)
		}
		return res
	}
	return EnvironmentPanelResult{Cmd: cmd}
}

func (p *EnvironmentPanel) saveEditor() EnvironmentPanelResult {
	values := p.kv.Values()
	sensitive := make(map[string]bool, len(p.kv.SensitiveKeys()))
	for _, k := range p.kv.SensitiveKeys() {
		sensitive[k] = true
	}
	envValues := make(map[string]config.EnvValue, len(values))
	for k, v := range values {
		envValues[k] = config.EnvValue{Value: v, Sensitive: sensitive[k]}
	}
	env := &config.Environment{Name: p.editName, Values: envValues}
	if err := env.Save(); err != nil {
		p.status = shared.ErrorStyle.Render("Save failed: " + err.Error())
		return EnvironmentPanelResult{}
	}
	p.kv.MarkClean()
	p.status = shared.SuccessStyle.Render("saved")
	p.reload()
	return EnvironmentPanelResult{Saved: true, SavedEnv: env}
}

func (p *EnvironmentPanel) selected() *config.Environment {
	if p.cursor < 0 || p.cursor >= len(p.envs) {
		return nil
	}
	return p.envs[p.cursor]
}

// ViewModal renders the modal box.
func (p *EnvironmentPanel) ViewModal(screenWidth, screenHeight int) string {
	modalWidth := min(92, screenWidth-8)
	modalHeight := min(26, screenHeight-6)
	contentWidth := modalWidth - 6
	if contentWidth < 1 {
		contentWidth = 1
	}

	var body string
	if p.view == envViewEdit {
		body = p.renderEdit(contentWidth, modalHeight)
	} else {
		body = p.renderList(contentWidth)
	}

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(shared.ColorPrimary).
		Padding(1, 2).
		Width(modalWidth).
		Render(body)
}

func (p *EnvironmentPanel) renderList(contentWidth int) string {
	var b strings.Builder

	title := shared.TitleStyle.Render("Environments")
	close := shared.DimStyle.Render("[esc] close")
	gap := max(0, contentWidth-lipgloss.Width(title)-lipgloss.Width(close))
	b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, title,
		strings.Repeat(" ", gap), close))
	b.WriteString("\n")
	b.WriteString(shared.DimStyle.Render(strings.Repeat("─", contentWidth)))
	b.WriteString("\n")

	if p.activeName != "" {
		b.WriteString(shared.DimStyle.Render("Active: "))
		b.WriteString(shared.SuccessStyle.Render(p.activeName))
	} else {
		b.WriteString(shared.DimStyle.Render("Active: "))
		b.WriteString(shared.DimStyle.Render("(none)"))
	}
	b.WriteString("\n\n")

	if len(p.envs) == 0 {
		b.WriteString(shared.DimStyle.Render("  No environments yet — press [n] to create one"))
		b.WriteString("\n")
	} else {
		for i, env := range p.envs {
			b.WriteString(p.renderRow(i, env, contentWidth))
			b.WriteString("\n")
		}
	}

	b.WriteString("\n")
	if p.field != envEditNone {
		switch p.field {
		case envEditCreate:
			b.WriteString(shared.DimStyle.Render("New environment: "))
		case envEditCopy:
			b.WriteString(shared.DimStyle.Render("Copy to: "))
		case envEditRename:
			b.WriteString(shared.DimStyle.Render("Rename to: "))
		}
		b.WriteString(p.input.View())
		b.WriteString("\n")
	}

	if p.status != "" {
		b.WriteString(p.status)
		b.WriteString("\n")
	}

	if p.field != envEditNone {
		b.WriteString(shared.DimStyle.Render("[enter] confirm  [esc] cancel"))
	} else {
		b.WriteString(shared.DimStyle.Render(
			"[↑/↓] move  [enter] activate  [0] clear  [n] new  [e] edit  [r] rename  [y] copy  [x] delete  [esc] close"))
	}
	return b.String()
}

func (p *EnvironmentPanel) renderRow(idx int, env *config.Environment, contentWidth int) string {
	cursor := "  "
	nameStyle := shared.NormalStyle
	if idx == p.cursor {
		cursor = shared.CursorStyle.Render("> ")
		nameStyle = shared.SelectedStyle
	}
	marker := ""
	if env.Name == p.activeName {
		marker = " " + shared.SuccessStyle.Render("●")
	}
	name := nameStyle.Render(env.Name) + marker
	count := shared.DimStyle.Render(fmt.Sprintf("%d vars", len(env.Values)))
	pad := max(1, contentWidth-lipgloss.Width(cursor)-lipgloss.Width(name)-lipgloss.Width(count)-2)
	return cursor + name + strings.Repeat(" ", pad) + count
}

func (p *EnvironmentPanel) renderEdit(contentWidth, modalHeight int) string {
	var b strings.Builder
	title := shared.TitleStyle.Render(fmt.Sprintf("Environment — %s", p.editName))
	close := shared.DimStyle.Render("[esc] back")
	gap := max(0, contentWidth-lipgloss.Width(title)-lipgloss.Width(close))
	b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, title,
		strings.Repeat(" ", gap), close))
	b.WriteString("\n")
	b.WriteString(shared.DimStyle.Render(strings.Repeat("─", contentWidth)))
	b.WriteString("\n")

	// Reserve two rows for the hint + status/dirty line; everything else
	// belongs to the K/V editor.
	used := strings.Count(b.String(), "\n")
	kvHeight := modalHeight - used - 3
	if kvHeight < 4 {
		kvHeight = 4
	}
	p.kv.SetSize(contentWidth, kvHeight)
	b.WriteString(p.kv.View())
	b.WriteString("\n")

	// Hint row from the editor itself — changes when an inline edit is
	// in progress. Saves happen on every commit, so no Ctrl+S to show.
	b.WriteString(p.kv.Hint())

	if p.status != "" {
		b.WriteString("\n")
		b.WriteString(p.status)
	}
	return b.String()
}
