package panels

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/cuppojoe/feather/internal/openapi"
	"github.com/cuppojoe/feather/internal/overlay"
	"github.com/cuppojoe/feather/internal/scripting"
	"github.com/cuppojoe/feather/internal/tui/shared"
)

// scriptsCallerID tags EditExternalMsg results so they reach this panel's
// editor. The user can't switch scope/phase while $EDITOR is open (the
// child process blocks the TUI), so a single ID is enough — when the
// result lands, editor.SetValue updates the live editor and the next
// applyEditor() pushes it into the right buffer slot on save.
const scriptsCallerID = "scripts_panel"

// ScriptsPanel is the modal editor for pre/post-request JS hooks at the
// profile / tag / operation scopes. It holds an in-memory buffer for each
// (scope, phase) combination and flushes them to the overlay on Ctrl+S.
type ScriptsPanel struct {
	keys     shared.KeyMap
	overlay  *overlay.Overlay
	profile  string
	endpoint *openapi.Endpoint // may be nil → only profile scope available
	tag      string            // first tag of endpoint, when relevant

	expanded bool
	editor   shared.TextEditor
	scope    scripting.Scope
	phase    scripting.Phase
	status   string // transient feedback shown above the editor

	// Local buffers — in-progress edits per (scope, phase). Flushed to the
	// overlay only on Save; discarded on Esc.
	buffers map[bufKey]string
}

func (s *ScriptsPanel) dirty() bool {
	return s.editor.Dirty()
}

type bufKey struct {
	scope scripting.Scope
	phase scripting.Phase
}

// ScriptsPanelResult mirrors the other panel-result types.
type ScriptsPanelResult struct {
	Save bool
	Cmd  tea.Cmd
}

// NewScriptsPanel returns an empty panel; populate context with Open().
func NewScriptsPanel(keys shared.KeyMap) *ScriptsPanel {
	ed := shared.NewTextEditor(40, 10)
	ed.SetLanguage("javascript")
	ed.SetPlaceholder("// pre/post-request JavaScript")
	ed.SetExternalEditorID(scriptsCallerID)
	ed.SetExternalEditorExt(".js")

	return &ScriptsPanel{
		keys:    keys,
		editor:  ed,
		scope:   scripting.ScopeProfile,
		phase:   scripting.PhasePre,
		buffers: map[bufKey]string{},
	}
}

// Open configures the panel for the current request/profile and expands it.
// endpoint may be nil; in that case only the Profile scope is available.
func (p *ScriptsPanel) Open(ov *overlay.Overlay, profile string, endpoint *openapi.Endpoint) tea.Cmd {
	p.overlay = ov
	p.profile = profile
	p.endpoint = endpoint
	p.tag = ""
	if endpoint != nil && len(endpoint.Tags) > 0 {
		p.tag = endpoint.Tags[0]
	}
	// Reset local buffers each open so cancelling discards stale edits.
	p.buffers = map[bufKey]string{}
	p.editor.Clean()
	p.status = ""
	if endpoint == nil {
		p.scope = scripting.ScopeProfile
	}
	if p.scope == scripting.ScopeTag && p.tag == "" {
		p.scope = scripting.ScopeProfile
	}
	if p.scope == scripting.ScopeOperation && endpoint == nil {
		p.scope = scripting.ScopeProfile
	}
	p.expanded = true
	p.loadCurrent()
	return p.editor.Focus()
}

func (p *ScriptsPanel) IsExpanded() bool { return p.expanded }
func (p *ScriptsPanel) IsEditing() bool  { return p.expanded && p.editor.Focused() }

// Toggle closes the modal; opening uses Open() with context.
func (p *ScriptsPanel) Toggle() {
	if p.expanded {
		p.expanded = false
		p.editor.Blur()
	}
}

// Update handles keys and mouse for the modal. Esc closes without saving.
// Ctrl+S flushes local buffers to the overlay and signals the host (via
// Save) to call overlay.Save().
func (p *ScriptsPanel) Update(msg tea.Msg) ScriptsPanelResult {
	if !p.expanded {
		return ScriptsPanelResult{}
	}

	// The editor's language picker takes input when open.
	if p.editor.PickerOpen() {
		cmd := p.editor.Update(msg)
		return ScriptsPanelResult{Cmd: cmd}
	}

	km, ok := msg.(tea.KeyMsg)
	if !ok {
		cmd := p.editor.Update(msg)
		// Non-key messages forward into the editor (blink ticks,
		// EditExternalMsg returns from ctrl+v, etc.). When the editor
		// absorbs an external-edit result it SetValues itself, so mark
		// the panel dirty so the next ctrl+s flushes the change.
		if _, isEdit := msg.(shared.EditExternalMsg); isEdit && p.dirty() {
			p.status = ""
		}
		return ScriptsPanelResult{Cmd: cmd}
	}

	switch {
	case km.String() == "esc":
		// While the editor is mid-search, esc cancels the search prompt.
		// Otherwise esc closes the modal immediately (first press, no
		// two-step blur dance).
		if p.editor.IsSearching() {
			cmd := p.editor.Update(msg)
			return ScriptsPanelResult{Cmd: cmd}
		}
		p.expanded = false
		p.editor.Blur()
		return ScriptsPanelResult{}
	case km.String() == "ctrl+s":
		p.applyEditor()
		p.flush()
		p.editor.Clean()
		p.status = "saved"
		return ScriptsPanelResult{Save: true}
	case km.String() == "ctrl+right" || km.String() == "alt+right":
		p.applyEditor()
		p.cycleScope(+1)
		return ScriptsPanelResult{}
	case km.String() == "ctrl+left" || km.String() == "alt+left":
		p.applyEditor()
		p.cycleScope(-1)
		return ScriptsPanelResult{}
	case km.String() == "ctrl+p":
		p.applyEditor()
		p.togglePhase()
		return ScriptsPanelResult{}
	}

	// Otherwise the keypress belongs to the editor.
	cmd := p.editor.Update(msg)
	if _, ok := msg.(tea.KeyMsg); ok {
		if p.editor.Focused() && p.editor.Dirty() {
			p.status = ""
		}
	}
	return ScriptsPanelResult{Cmd: cmd}
}

// ViewModal renders the modal box.
func (p *ScriptsPanel) ViewModal(screenWidth, screenHeight int) string {
	modalWidth := min(100, screenWidth-8)
	modalHeight := min(30, screenHeight-6)
	contentWidth := modalWidth - 6
	if contentWidth < 1 {
		contentWidth = 1
	}

	var b strings.Builder
	title := shared.TitleStyle.Render("Scripts")
	closeHint := shared.DimStyle.Render("[esc] close")
	gap := max(0, contentWidth-lipgloss.Width(title)-lipgloss.Width(closeHint))
	b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, title,
		strings.Repeat(" ", gap), closeHint))
	b.WriteString("\n")
	b.WriteString(shared.DimStyle.Render(strings.Repeat("─", contentWidth)))
	b.WriteString("\n")

	// Scope row.
	b.WriteString(shared.DimStyle.Render("Scope:  "))
	for i, s := range p.availableScopes() {
		label := p.scopeLabel(s)
		if s == p.scope {
			b.WriteString(shared.ActiveTabStyle.Render(label))
		} else {
			b.WriteString(shared.InactiveTabStyle.Render(label))
		}
		if i < len(p.availableScopes())-1 {
			b.WriteString(" ")
		}
	}
	b.WriteString("\n")

	// Phase row.
	b.WriteString(shared.DimStyle.Render("Phase:  "))
	for i, ph := range []scripting.Phase{scripting.PhasePre, scripting.PhasePost} {
		label := strings.Title(string(ph))
		if ph == p.phase {
			b.WriteString(shared.ActiveTabStyle.Render(label))
		} else {
			b.WriteString(shared.InactiveTabStyle.Render(label))
		}
		if i == 0 {
			b.WriteString(" ")
		}
	}

	// Status / dirty marker.
	if p.dirty() {
		b.WriteString("  " + shared.WarningStyle.Render("● unsaved"))
	} else if p.status != "" {
		b.WriteString("  " + shared.SuccessStyle.Render(p.status))
	}
	b.WriteString("\n")
	b.WriteString(shared.DimStyle.Render(strings.Repeat("─", contentWidth)))
	b.WriteString("\n")

	// Editor area, then a single shortcut hint row at the very bottom.
	editorHeight := modalHeight - 8
	if editorHeight < 3 {
		editorHeight = 3
	}
	p.editor.SetSize(contentWidth, editorHeight)
	// The host translates modal mouse events into modal-content coords
	// before forwarding (see translateModalMouse in app.go), so the
	// editor's origin within that frame is column 0, row N — where N is
	// the number of \n already in the builder before the editor starts
	// (title row + divider + scope row + phase row + divider).
	editorStartRow := strings.Count(b.String(), "\n")
	p.editor.SetMouseOrigin(0, editorStartRow)
	b.WriteString(p.editor.View())
	b.WriteString("\n")

	var hintBar shared.HintBar
	b.WriteString(hintBar.Render(
		[]shared.Hint{
			{Key: "ctrl+s", Label: "save"},
			{Key: "ctrl+v", Label: "external editor"},
			{Key: "ctrl+right", Display: "ctrl+←/→", Label: "scope"},
			{Key: "ctrl+p", Label: "pre/post"},
			{Key: "esc", Label: "close"},
		},
		0, 0, true, "  ", shared.DimStyle,
	))

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(shared.ColorPrimary).
		Padding(1, 2).
		Width(modalWidth).
		Render(b.String())
}

// --- internal helpers -----------------------------------------------------

// availableScopes returns the scopes the user can switch into for the
// current endpoint context.
func (p *ScriptsPanel) availableScopes() []scripting.Scope {
	out := []scripting.Scope{scripting.ScopeProfile}
	if p.tag != "" {
		out = append(out, scripting.ScopeTag)
	}
	if p.endpoint != nil {
		out = append(out, scripting.ScopeOperation)
	}
	return out
}

func (p *ScriptsPanel) scopeLabel(s scripting.Scope) string {
	switch s {
	case scripting.ScopeProfile:
		return "Profile (" + p.profile + ")"
	case scripting.ScopeTag:
		return "Tag: " + p.tag
	case scripting.ScopeOperation:
		if p.endpoint != nil {
			return p.endpoint.Method + " " + p.endpoint.Path
		}
		return "Operation"
	}
	return string(s)
}

func (p *ScriptsPanel) cycleScope(dir int) {
	scopes := p.availableScopes()
	for i, s := range scopes {
		if s == p.scope {
			next := (i + dir + len(scopes)) % len(scopes)
			p.scope = scopes[next]
			break
		}
	}
	p.loadCurrent()
}

func (p *ScriptsPanel) togglePhase() {
	if p.phase == scripting.PhasePre {
		p.phase = scripting.PhasePost
	} else {
		p.phase = scripting.PhasePre
	}
	p.loadCurrent()
}

// loadCurrent populates the editor from the buffer if one exists, otherwise
// from the overlay.
func (p *ScriptsPanel) loadCurrent() {
	key := bufKey{scope: p.scope, phase: p.phase}
	if v, ok := p.buffers[key]; ok {
		p.editor.SetValue(v)
		return
	}
	p.editor.SetValue(p.overlayValue(p.scope, p.phase))
}

func (p *ScriptsPanel) overlayValue(s scripting.Scope, ph scripting.Phase) string {
	if p.overlay == nil {
		return ""
	}
	var pair overlay.Scripts
	switch s {
	case scripting.ScopeProfile:
		pair = p.overlay.ProfileScripts()
	case scripting.ScopeTag:
		pair = p.overlay.TagScripts(p.tag)
	case scripting.ScopeOperation:
		if p.endpoint != nil {
			pair = p.overlay.OperationScripts(p.endpoint.Method, p.endpoint.Path)
		}
	}
	if ph == scripting.PhasePre {
		return pair.Pre
	}
	return pair.Post
}

// applyEditor stashes the editor's current value into the local buffer for
// the active (scope, phase). Called before switching scope/phase or saving.
func (p *ScriptsPanel) applyEditor() {
	p.buffers[bufKey{scope: p.scope, phase: p.phase}] = p.editor.Value()
}

// flush commits all local buffers to the overlay. The host is responsible
// for persisting (Save: true result triggers it).
func (p *ScriptsPanel) flush() {
	if p.overlay == nil {
		return
	}
	// Per scope, build the (pre, post) pair from the buffer + overlay
	// fallback for whichever phase wasn't edited locally.
	for s := range p.scopesTouchedByBuffers() {
		pre := p.bufferOrOverlay(s, scripting.PhasePre)
		post := p.bufferOrOverlay(s, scripting.PhasePost)
		pair := overlay.Scripts{Pre: pre, Post: post}
		switch s {
		case scripting.ScopeProfile:
			p.overlay.SetProfileScripts(pair)
		case scripting.ScopeTag:
			if p.tag != "" {
				p.overlay.SetTagScripts(p.tag, pair)
			}
		case scripting.ScopeOperation:
			if p.endpoint != nil {
				p.overlay.SetOperationScripts(p.endpoint.Method, p.endpoint.Path, pair)
			}
		}
	}
	// Once flushed, the overlay is the source of truth.
	p.buffers = map[bufKey]string{}
}

func (p *ScriptsPanel) scopesTouchedByBuffers() map[scripting.Scope]struct{} {
	out := map[scripting.Scope]struct{}{}
	for k := range p.buffers {
		out[k.scope] = struct{}{}
	}
	return out
}

func (p *ScriptsPanel) bufferOrOverlay(s scripting.Scope, ph scripting.Phase) string {
	if v, ok := p.buffers[bufKey{scope: s, phase: ph}]; ok {
		return v
	}
	return p.overlayValue(s, ph)
}

// shutUpUnused keeps the imports in the file consistent — fmt is needed by
// scopeLabel sprintf calls in future variants. `key` is reserved for when we
// wire shared.KeyMap bindings here.
var _ = fmt.Sprintf
var _ key.Binding
