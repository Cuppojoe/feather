package screens

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/cuppojoe/feather/internal/openapi"
	"github.com/cuppojoe/feather/internal/overlay"
	"github.com/cuppojoe/feather/internal/tui/shared"
)

// Tab indices for the main menu (tag/category root view).
const (
	MainMenuTabCategories = iota
	MainMenuTabScripts
)

// Click-target IDs for the main menu tab bar. Negative IDs are tabs;
// non-negative IDs are tag row indices (so they share one ClickMap).
const (
	clickIDMainMenuTabCategories = -1
	clickIDMainMenuTabScripts    = -2
)

// External-editor caller IDs for the inline profile-scope script editors.
const (
	editorCallerProfileScriptPre  = "main_menu_profile_script_pre"
	editorCallerProfileScriptPost = "main_menu_profile_script_post"
)

// MainMenu displays the tag-based endpoint categories, plus inline editors
// for the active profile's pre/post-request scripts (the same script slots
// the global Scripts modal edits under "profile" scope).
type MainMenu struct {
	tags      []openapi.TagGroup
	filtered  []openapi.TagGroup
	cursor    int
	keys      shared.KeyMap
	search    textinput.Model
	searching bool
	width     int
	height    int
	clickMap  shared.ClickMap

	// Tab state. tabRowY/tabBounds let click hit-tests resolve against the
	// rendered tab positions.
	activeTab int
	tabRowY   int
	tabBounds []int

	// Scripts tab — mirrors the request-builder and endpoint-list patterns
	// so ctrl+p / ctrl+s / ctrl+v all behave identically.
	scriptPhase      int
	scriptPreEditor  shared.TextEditor
	scriptPostEditor shared.TextEditor
	overlay          *overlay.Overlay

	// statusMsg is a transient confirmation rendered next to the phase
	// indicator after a save. Cleared on the next render.
	statusMsg string
}

// MainMenuResult is the result of a main menu update
type MainMenuResult struct {
	Selected *openapi.TagGroup

	// SaveProfileScripts asks the host to persist the inline profile-scope
	// script edits — overlay.SetProfileScripts(Scripts) + write to disk.
	SaveProfileScripts bool
	Scripts            overlay.Scripts

	Cmd tea.Cmd
}

// NewMainMenu creates a new main menu. ov is the active overlay (used to
// seed the profile's pre/post-script editors); may be nil.
func NewMainMenu(tags []openapi.TagGroup, keys shared.KeyMap, ov *overlay.Overlay) *MainMenu {
	search := textinput.New()
	search.Placeholder = "Filter tags..."
	search.CharLimit = 50

	preEditor := shared.NewTextEditor(40, 10)
	preEditor.SetLanguage("javascript")
	preEditor.SetPlaceholder("// JS pre-request hook, runs for every request this profile sends")
	preEditor.SetExternalEditorID(editorCallerProfileScriptPre)
	preEditor.SetExternalEditorExt(".js")
	postEditor := shared.NewTextEditor(40, 10)
	postEditor.SetLanguage("javascript")
	postEditor.SetPlaceholder("// JS post-request hook, runs after every response this profile receives")
	postEditor.SetExternalEditorID(editorCallerProfileScriptPost)
	postEditor.SetExternalEditorExt(".js")
	if ov != nil {
		s := ov.ProfileScripts()
		preEditor.SetValue(s.Pre)
		postEditor.SetValue(s.Post)
	}

	return &MainMenu{
		tags:             tags,
		filtered:         tags,
		keys:             keys,
		search:           search,
		overlay:          ov,
		scriptPreEditor:  preEditor,
		scriptPostEditor: postEditor,
	}
}

// IsSearching reports whether the tag list is capturing a search query.
func (m *MainMenu) IsSearching() bool { return m.searching }

// IsEditing returns true while the user is mid-edit inside the search
// input or one of the inline script editors — host uses this to keep
// global single-key bindings out of the way.
func (m *MainMenu) IsEditing() bool {
	if m.searching {
		return true
	}
	if m.activeTab == MainMenuTabScripts && m.activeScriptEditor().Focused() {
		return true
	}
	return false
}

// activeScriptEditor returns a pointer to whichever script editor is
// selected by the current phase.
func (m *MainMenu) activeScriptEditor() *shared.TextEditor {
	if m.scriptPhase == ScriptPhasePost {
		return &m.scriptPostEditor
	}
	return &m.scriptPreEditor
}

// SelectedTag returns the currently highlighted tag group, or nil when empty.
func (m *MainMenu) SelectedTag() *openapi.TagGroup {
	if m.cursor < 0 || m.cursor >= len(m.filtered) {
		return nil
	}
	return &m.filtered[m.cursor]
}

// Update handles input for the main menu
func (m *MainMenu) Update(msg tea.Msg) MainMenuResult {
	var cmd tea.Cmd

	// External editor returned — each editor watches its own caller ID
	// (set via SetExternalEditorID in NewMainMenu), so forwarding the
	// message into the active script editor is enough.
	if _, ok := msg.(shared.EditExternalMsg); ok {
		if m.activeTab == MainMenuTabScripts {
			cmd = m.activeScriptEditor().Update(msg)
		}
		return MainMenuResult{Cmd: cmd}
	}

	// Editor cursor-blink ticks flow into the active script editor.
	if m.activeTab == MainMenuTabScripts {
		if _, ok := msg.(shared.EditorBlinkMsg); ok {
			cmd = m.activeScriptEditor().Update(msg)
			return MainMenuResult{Cmd: cmd}
		}
	}

	if m.searching {
		switch msg := msg.(type) {
		case tea.KeyMsg:
			switch msg.String() {
			case "enter", "esc":
				// Just blur the search, keep the filter text
				m.searching = false
				m.search.Blur()
				return MainMenuResult{Cmd: cmd}
			}
		}

		m.search, cmd = m.search.Update(msg)
		m.filterTags(m.search.Value())
		return MainMenuResult{Cmd: cmd}
	}

	// Scripts tab: forward to the active phase editor, with the same
	// shortcuts as the request-builder/endpoint-list Scripts tabs.
	if m.activeTab == MainMenuTabScripts {
		ed := m.activeScriptEditor()
		if ed.PickerOpen() {
			cmd = ed.Update(msg)
			return MainMenuResult{Cmd: cmd}
		}
		if km, ok := msg.(tea.KeyMsg); ok {
			switch {
			case km.String() == "ctrl+p":
				ed.Blur()
				if m.scriptPhase == ScriptPhasePre {
					m.scriptPhase = ScriptPhasePost
				} else {
					m.scriptPhase = ScriptPhasePre
				}
				return MainMenuResult{}
			case key.Matches(km, m.keys.Save):
				return m.saveScripts()
			case key.Matches(km, m.keys.Tab), key.Matches(km, m.keys.ShiftTab):
				// fall through to the outer handler for tab nav
			case km.String() == "1", km.String() == "2":
				// Editor swallows the digit when focused; the keys only
				// act as tab shortcuts while the editor is blurred.
				if ed.Focused() {
					cmd = ed.Update(msg)
					return MainMenuResult{Cmd: cmd}
				}
				// fall through for tab nav
			case key.Matches(km, m.keys.Enter) && !ed.Focused():
				cmd = ed.Update(msg)
				return MainMenuResult{Cmd: cmd}
			default:
				if ed.Focused() {
					cmd = ed.Update(msg)
					return MainMenuResult{Cmd: cmd}
				}
			}
		} else if mm, isMouse := msg.(tea.MouseMsg); isMouse {
			switch mm.Button {
			case tea.MouseButtonWheelUp, tea.MouseButtonWheelDown:
				if ed.Focused() {
					cmd = ed.Update(msg)
					return MainMenuResult{Cmd: cmd}
				}
			case tea.MouseButtonLeft:
				cmd = ed.Update(msg)
				if mm.Action == tea.MouseActionRelease {
					// Let the outer click handler hit-test the tab bar.
					break
				}
				return MainMenuResult{Cmd: cmd}
			}
		}
	}

	switch msg := msg.(type) {
	case tea.MouseMsg:
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			if m.activeTab == MainMenuTabCategories && m.cursor > 0 {
				m.cursor--
			}
		case tea.MouseButtonWheelDown:
			if m.activeTab == MainMenuTabCategories && m.cursor < len(m.filtered)-1 {
				m.cursor++
			}
		case tea.MouseButtonLeft:
			if msg.Action == tea.MouseActionRelease {
				if id, ok := m.clickMap.Hit(msg.X, msg.Y); ok {
					switch id {
					case clickIDMainMenuTabCategories:
						m.activeTab = MainMenuTabCategories
						return MainMenuResult{}
					case clickIDMainMenuTabScripts:
						m.activeTab = MainMenuTabScripts
						return MainMenuResult{}
					default:
						if id >= 0 && m.activeTab == MainMenuTabCategories {
							m.cursor = id
							return MainMenuResult{Selected: &m.filtered[m.cursor]}
						}
					}
				}
			}
		}

	case tea.KeyMsg:
		switch {
		case key.Matches(msg, m.keys.Tab):
			if m.activeTab < MainMenuTabScripts {
				m.activeTab++
			} else {
				m.activeTab = MainMenuTabCategories
			}
			return MainMenuResult{}
		case key.Matches(msg, m.keys.ShiftTab):
			if m.activeTab > 0 {
				m.activeTab--
			} else {
				m.activeTab = MainMenuTabScripts
			}
			return MainMenuResult{}
		case msg.String() == "1":
			m.activeTab = MainMenuTabCategories
			return MainMenuResult{}
		case msg.String() == "2":
			m.activeTab = MainMenuTabScripts
			return MainMenuResult{}
		case key.Matches(msg, m.keys.Left):
			if m.activeTab > 0 {
				m.activeTab--
			}
			return MainMenuResult{}
		case key.Matches(msg, m.keys.Right):
			if m.activeTab < MainMenuTabScripts {
				m.activeTab++
			}
			return MainMenuResult{}
		}

		// Categories-tab-only navigation.
		if m.activeTab != MainMenuTabCategories {
			return MainMenuResult{Cmd: cmd}
		}
		switch {
		case key.Matches(msg, m.keys.Up):
			if m.cursor > 0 {
				m.cursor--
			}
		case key.Matches(msg, m.keys.Down):
			if m.cursor < len(m.filtered)-1 {
				m.cursor++
			}
		case key.Matches(msg, m.keys.Enter):
			if len(m.filtered) > 0 {
				return MainMenuResult{Selected: &m.filtered[m.cursor]}
			}
		case key.Matches(msg, m.keys.Search):
			m.searching = true
			m.search.Focus()
			return MainMenuResult{Cmd: textinput.Blink}
		case key.Matches(msg, m.keys.Home):
			m.cursor = 0
		case key.Matches(msg, m.keys.End):
			m.cursor = len(m.filtered) - 1
			if m.cursor < 0 {
				m.cursor = 0
			}
		}
	}

	return MainMenuResult{Cmd: cmd}
}

// saveScripts captures the inline pre/post buffers as an overlay.Scripts
// pair and asks the host to persist them at profile scope.
func (m *MainMenu) saveScripts() MainMenuResult {
	s := overlay.Scripts{
		Pre:  m.scriptPreEditor.Value(),
		Post: m.scriptPostEditor.Value(),
	}
	m.statusMsg = "scripts saved"
	return MainMenuResult{
		SaveProfileScripts: true,
		Scripts:            s,
	}
}

// filterTags filters tags by search query
func (m *MainMenu) filterTags(query string) {
	if query == "" {
		m.filtered = m.tags
		m.cursor = 0
		return
	}

	query = strings.ToLower(query)
	var filtered []openapi.TagGroup
	for _, tag := range m.tags {
		if strings.Contains(strings.ToLower(tag.Name), query) ||
			strings.Contains(strings.ToLower(tag.Description), query) {
			filtered = append(filtered, tag)
		}
	}
	m.filtered = filtered
	m.cursor = 0
}

// View renders the main menu
func (m *MainMenu) View(width, height int) string {
	m.width = width
	m.height = height
	m.clickMap.Reset()

	var b strings.Builder

	// Header (title removed — the profile bar at the top of the TUI now
	// serves as the app's brand surface).
	subtitle := shared.SubtitleStyle.Render("Select an endpoint category")
	b.WriteString(subtitle)
	b.WriteString("\n\n")

	// Tab bar — Categories | Scripts. Register click ranges so the mouse
	// can jump between tabs.
	tabs := []string{"1:Categories", "2:Scripts"}
	tabIDs := []int{clickIDMainMenuTabCategories, clickIDMainMenuTabScripts}
	var tabViews []string
	for i, t := range tabs {
		if i == m.activeTab {
			tabViews = append(tabViews, shared.ActiveTabStyle.Render(t))
		} else {
			tabViews = append(tabViews, shared.InactiveTabStyle.Render(t))
		}
	}
	tabBar := lipgloss.JoinHorizontal(lipgloss.Bottom, tabViews...)
	m.tabRowY = strings.Count(b.String(), "\n")
	m.tabBounds = m.tabBounds[:0]
	xCursor := 0
	for i, tv := range tabViews {
		w := lipgloss.Width(tv)
		m.clickMap.AddRange(m.tabRowY, xCursor, xCursor+w, tabIDs[i])
		m.tabBounds = append(m.tabBounds, xCursor+w)
		xCursor += w
	}
	b.WriteString(tabBar)
	b.WriteString("\n")
	b.WriteString(shared.DimStyle.Render(strings.Repeat("─", width-4)))
	b.WriteString("\n")

	// Scripts tab takes over the remaining vertical space with its editor.
	if m.activeTab == MainMenuTabScripts {
		contentStartRow := strings.Count(b.String(), "\n")
		b.WriteString(m.renderScriptsTab(contentStartRow))
		return lipgloss.NewStyle().Width(width).Render(b.String())
	}

	// Search bar if active
	if m.searching {
		b.WriteString(m.search.View())
		b.WriteString("\n\n")
	} else if m.search.Value() != "" {
		b.WriteString(shared.DimStyle.Render(fmt.Sprintf("Filter: %s", m.search.Value())))
		b.WriteString("\n\n")
	}

	authorHint := shared.DimStyle.Render("n:new request  N:new category  e:rename  d:delete")

	// Empty state
	if len(m.filtered) == 0 {
		b.WriteString(shared.DimStyle.Render("  No categories yet"))
		b.WriteString("\n\n")
		b.WriteString(authorHint)
		b.WriteString("\n")
		return lipgloss.NewStyle().Width(width).Render(b.String())
	}

	// Calculate available height for list. Tab bar + divider take 2 extra
	// rows beyond the original layout.
	headerHeight := 6
	if m.searching || m.search.Value() != "" {
		headerHeight += 2
	}
	listHeight := height - headerHeight - 1 // reserve a row for the hint

	// Render tag list with scrolling
	startIdx := 0
	if m.cursor >= listHeight {
		startIdx = m.cursor - listHeight + 1
	}

	// Build table rows
	var rows [][]string
	for i := startIdx; i < len(m.filtered) && i < startIdx+listHeight; i++ {
		tag := m.filtered[i]
		cursor := " "
		if i == m.cursor {
			cursor = ">"
		}

		rows = append(rows, []string{
			cursor,
			tag.Name,
			fmt.Sprintf("%d endpoints", len(tag.Endpoints)),
		})
	}

	// Create table without borders (panel already has borders)
	t := table.New().
		Border(lipgloss.HiddenBorder()).
		Rows(rows...).
		Width(width - 2).
		StyleFunc(func(row, col int) lipgloss.Style {
			// Check if this is the selected row
			actualIdx := startIdx + row
			isSelected := actualIdx == m.cursor

			switch col {
			case 0: // Cursor column - minimal width, no padding
				style := lipgloss.NewStyle().Width(2)
				if isSelected {
					return style.Foreground(shared.ColorPrimary).Bold(true)
				}
				return style
			case 1: // Tag name column
				style := lipgloss.NewStyle().PaddingRight(2)
				if isSelected {
					return style.Foreground(shared.ColorPrimary).Bold(true)
				}
				return style.Foreground(lipgloss.Color("#E5E7EB"))
			case 2: // Endpoints count column
				return lipgloss.NewStyle().Foreground(shared.ColorMuted)
			}

			return lipgloss.NewStyle()
		})

	// Where the table starts on screen, in panel-relative rows. The hidden
	// border adds 1 row above the first data row. Don't reset the click
	// map here — the tab bar registered its ranges earlier.
	tableStartRow := strings.Count(b.String(), "\n")
	firstItemRow := tableStartRow + 1
	for i := startIdx; i < startIdx+len(rows); i++ {
		m.clickMap.AddRow(firstItemRow+(i-startIdx), i)
	}

	b.WriteString(t.String())
	b.WriteString("\n")
	b.WriteString(authorHint)

	return lipgloss.NewStyle().Width(width).Render(b.String())
}

// renderScriptsTab hosts inline pre/post-request script editors scoped to
// the whole profile. Same shortcuts as the request-builder / endpoint-list
// Scripts tabs: ctrl+p toggles phase, ctrl+s saves, ctrl+v hands the
// active buffer to $EDITOR.
func (m *MainMenu) renderScriptsTab(startScreenRow int) string {
	var b strings.Builder

	preLabel := "Pre"
	postLabel := "Post"
	if m.scriptPhase == ScriptPhasePre {
		b.WriteString(shared.ActiveTabStyle.Render(preLabel))
		b.WriteString("  ")
		b.WriteString(shared.InactiveTabStyle.Render(postLabel))
	} else {
		b.WriteString(shared.InactiveTabStyle.Render(preLabel))
		b.WriteString("  ")
		b.WriteString(shared.ActiveTabStyle.Render(postLabel))
	}
	if m.statusMsg != "" {
		b.WriteString("  ")
		b.WriteString(shared.SuccessStyle.Render(m.statusMsg))
		m.statusMsg = ""
	}
	b.WriteString("\n")
	b.WriteString(shared.DimStyle.Render(strings.Repeat("─", m.width-4)))
	b.WriteString("\n")

	editorStartRow := startScreenRow + strings.Count(b.String(), "\n")
	editorHeight := m.height - editorStartRow - 1
	if editorHeight < 3 {
		editorHeight = 3
	}

	ed := m.activeScriptEditor()
	ed.SetSize(m.width-2, editorHeight)
	ed.SetMouseOrigin(0, editorStartRow)
	b.WriteString(ed.View())
	b.WriteString("\n")
	b.WriteString(ed.Footer(
		[]shared.Hint{
			{Key: "ctrl+s", Label: "save"},
			{Key: "ctrl+p", Label: "pre/post"},
			{Key: "ctrl+v", Label: "external editor"},
		},
		m.width-2,
	))

	return b.String()
}
