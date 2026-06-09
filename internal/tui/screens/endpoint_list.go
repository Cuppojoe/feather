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

// Tab indices for the endpoint list view.
const (
	EndpointTabRequests = iota
	EndpointTabScripts
)

// Click-target IDs for the endpoint list tab bar. Negative IDs are tab
// targets so they don't collide with endpoint row indices (non-negative).
const (
	clickIDEndpointTabRequests = -1
	clickIDEndpointTabScripts  = -2
)

// External-editor caller IDs for the inline tag-script editors. The
// EndpointList instance is bound to a single tag, so phase is enough to
// route the returned content back to the right buffer.
const (
	editorCallerTagScriptPre  = "endpoint_list_tag_script_pre"
	editorCallerTagScriptPost = "endpoint_list_tag_script_post"
)

// EndpointList displays endpoints for a selected tag, plus inline editors
// for that tag's pre/post-request scripts (the same script slots the
// global Scripts modal edits under "tag" scope).
type EndpointList struct {
	tag       *openapi.TagGroup
	filtered  []openapi.Endpoint
	cursor    int
	keys      shared.KeyMap
	search    textinput.Model
	searching bool
	width     int
	height    int
	clickMap  shared.ClickMap

	// Tab state — Requests (the endpoint list) and Scripts (tag-scope
	// pre/post editors). tabRowY records where the tab bar lands so click
	// hit-tests resolve against the rendered position.
	activeTab int
	tabRowY   int
	tabBounds []int

	// Scripts tab state — mirrors the request-builder Scripts tab so the
	// shortcuts (ctrl+p, ctrl+s, ctrl+v) feel identical.
	scriptPhase      int
	scriptPreEditor  shared.TextEditor
	scriptPostEditor shared.TextEditor
	overlay          *overlay.Overlay

	// statusMsg is a transient confirmation rendered under the editor after
	// a save. Cleared on the next render that emits a result.
	statusMsg string
}

// EndpointListResult is the result of an endpoint list update
type EndpointListResult struct {
	Selected *openapi.Endpoint

	// SaveTagScripts asks the host to persist the inline tag-script
	// changes — overlay.SetTagScripts(TagName, Scripts) + write to disk.
	SaveTagScripts bool
	TagName        string
	Scripts        overlay.Scripts

	Cmd tea.Cmd
}

// NewEndpointList creates a new endpoint list. ov is the active overlay
// (used to seed the tag's pre/post-script editors); may be nil.
func NewEndpointList(tag *openapi.TagGroup, keys shared.KeyMap, ov *overlay.Overlay) *EndpointList {
	search := textinput.New()
	search.Placeholder = "Filter endpoints..."
	search.CharLimit = 100

	preEditor := shared.NewTextEditor(40, 10)
	preEditor.SetLanguage("javascript")
	preEditor.SetPlaceholder("// JS pre-request hook — runs for every request in this category")
	preEditor.SetExternalEditorID(editorCallerTagScriptPre)
	preEditor.SetExternalEditorExt(".js")
	postEditor := shared.NewTextEditor(40, 10)
	postEditor.SetLanguage("javascript")
	postEditor.SetPlaceholder("// JS post-request hook — runs after every request in this category")
	postEditor.SetExternalEditorID(editorCallerTagScriptPost)
	postEditor.SetExternalEditorExt(".js")
	if ov != nil {
		s := ov.TagScripts(tag.Name)
		preEditor.SetValue(s.Pre)
		postEditor.SetValue(s.Post)
	}

	return &EndpointList{
		tag:              tag,
		filtered:         tag.Endpoints,
		keys:             keys,
		search:           search,
		overlay:          ov,
		scriptPreEditor:  preEditor,
		scriptPostEditor: postEditor,
	}
}

// IsEditing reports whether the user is mid-edit inside one of the inline
// editors — used by the host to keep routing all keys to the screen so
// global single-key bindings (like 'h' for history) don't steal input.
func (e *EndpointList) IsEditing() bool {
	if e.searching {
		return true
	}
	if e.activeTab == EndpointTabScripts && e.activeScriptEditor().Focused() {
		return true
	}
	return false
}

// activeScriptEditor returns a pointer to the script editor for the
// currently selected phase. Used in render and input routing.
func (e *EndpointList) activeScriptEditor() *shared.TextEditor {
	if e.scriptPhase == ScriptPhasePost {
		return &e.scriptPostEditor
	}
	return &e.scriptPreEditor
}

// Selected returns the currently highlighted endpoint, or nil when the list is
// empty. The pointer is into the live tag's endpoint slice.
func (e *EndpointList) Selected() *openapi.Endpoint {
	if e.cursor < 0 || e.cursor >= len(e.filtered) {
		return nil
	}
	return &e.filtered[e.cursor]
}

// IsSearching reports whether the endpoint list is capturing a search query.
func (e *EndpointList) IsSearching() bool { return e.searching }

// Update handles input for the endpoint list
func (e *EndpointList) Update(msg tea.Msg) EndpointListResult {
	var cmd tea.Cmd

	// External editor returned — each editor watches its own caller ID
	// (set via SetExternalEditorID in NewEndpointList), so forwarding the
	// message into the active script editor is enough.
	if _, ok := msg.(shared.EditExternalMsg); ok {
		if e.activeTab == EndpointTabScripts {
			cmd = e.activeScriptEditor().Update(msg)
		}
		return EndpointListResult{Cmd: cmd}
	}

	// Editor cursor-blink ticks keep flowing into the active script
	// editor while it has focus.
	if e.activeTab == EndpointTabScripts {
		if _, ok := msg.(shared.EditorBlinkMsg); ok {
			cmd = e.activeScriptEditor().Update(msg)
			return EndpointListResult{Cmd: cmd}
		}
	}

	if e.searching {
		switch msg := msg.(type) {
		case tea.KeyMsg:
			switch msg.String() {
			case "enter", "esc":
				// Just blur the search, keep the filter text
				e.searching = false
				e.search.Blur()
				return EndpointListResult{Cmd: cmd}
			}
		}

		e.search, cmd = e.search.Update(msg)
		e.filterEndpoints(e.search.Value())
		return EndpointListResult{Cmd: cmd}
	}

	// Scripts tab: forward keys/mouse to the active phase editor, with
	// the same shortcuts as the request-builder Scripts tab.
	if e.activeTab == EndpointTabScripts {
		ed := e.activeScriptEditor()
		if ed.PickerOpen() {
			cmd = ed.Update(msg)
			return EndpointListResult{Cmd: cmd}
		}
		if km, ok := msg.(tea.KeyMsg); ok {
			switch {
			case km.String() == "ctrl+p":
				ed.Blur()
				if e.scriptPhase == ScriptPhasePre {
					e.scriptPhase = ScriptPhasePost
				} else {
					e.scriptPhase = ScriptPhasePre
				}
				return EndpointListResult{}
			case key.Matches(km, e.keys.Save):
				return e.saveScripts()
			case key.Matches(km, e.keys.Tab), key.Matches(km, e.keys.ShiftTab):
				// fall through to the outer handler for tab nav
			case km.String() == "1", km.String() == "2":
				// Let the editor swallow digit keys while it's focused
				// (otherwise typing 1 or 2 jumps the user out of the
				// Scripts tab mid-edit).
				if ed.Focused() {
					cmd = ed.Update(msg)
					return EndpointListResult{Cmd: cmd}
				}
				// fall through for tab nav
			case key.Matches(km, e.keys.Enter) && !ed.Focused():
				// Focus the editor so it starts taking input.
				cmd = ed.Update(msg)
				return EndpointListResult{Cmd: cmd}
			default:
				if ed.Focused() {
					cmd = ed.Update(msg)
					return EndpointListResult{Cmd: cmd}
				}
			}
		} else if mm, isMouse := msg.(tea.MouseMsg); isMouse {
			switch mm.Button {
			case tea.MouseButtonWheelUp, tea.MouseButtonWheelDown:
				if ed.Focused() {
					cmd = ed.Update(msg)
					return EndpointListResult{Cmd: cmd}
				}
			case tea.MouseButtonLeft:
				// Editor scrollbar press / drag / release.
				cmd = ed.Update(msg)
				if mm.Action == tea.MouseActionRelease {
					// Also let the outer click handler hit-test the tab
					// bar in case the user clicked a tab label.
					break
				}
				return EndpointListResult{Cmd: cmd}
			}
		}
	}

	switch msg := msg.(type) {
	case tea.MouseMsg:
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			if e.activeTab == EndpointTabRequests && e.cursor > 0 {
				e.cursor--
			}
		case tea.MouseButtonWheelDown:
			if e.activeTab == EndpointTabRequests && e.cursor < len(e.filtered)-1 {
				e.cursor++
			}
		case tea.MouseButtonLeft:
			if msg.Action == tea.MouseActionRelease {
				// Tab bar clicks first.
				if id, ok := e.clickMap.Hit(msg.X, msg.Y); ok {
					switch id {
					case clickIDEndpointTabRequests:
						e.activeTab = EndpointTabRequests
						return EndpointListResult{}
					case clickIDEndpointTabScripts:
						e.activeTab = EndpointTabScripts
						return EndpointListResult{}
					default:
						if id >= 0 && e.activeTab == EndpointTabRequests {
							e.cursor = id
							return EndpointListResult{Selected: &e.filtered[e.cursor]}
						}
					}
				}
			}
		}

	case tea.KeyMsg:
		switch {
		// Tab navigation works on both tabs.
		case key.Matches(msg, e.keys.Tab):
			if e.activeTab < EndpointTabScripts {
				e.activeTab++
			} else {
				e.activeTab = EndpointTabRequests
			}
			return EndpointListResult{}
		case key.Matches(msg, e.keys.ShiftTab):
			if e.activeTab > 0 {
				e.activeTab--
			} else {
				e.activeTab = EndpointTabScripts
			}
			return EndpointListResult{}
		case msg.String() == "1":
			e.activeTab = EndpointTabRequests
			return EndpointListResult{}
		case msg.String() == "2":
			e.activeTab = EndpointTabScripts
			return EndpointListResult{}
		case key.Matches(msg, e.keys.Left):
			// Clamped, not wrapping — Tab/Shift+Tab covers cycling and
			// arrow-key clamping matches how most TUI tab bars behave.
			if e.activeTab > 0 {
				e.activeTab--
			}
			return EndpointListResult{}
		case key.Matches(msg, e.keys.Right):
			if e.activeTab < EndpointTabScripts {
				e.activeTab++
			}
			return EndpointListResult{}
		}

		// Requests-tab-only navigation. Drop through silently when the
		// Scripts tab is active so the editor (handled above) keeps
		// ownership of the input.
		if e.activeTab != EndpointTabRequests {
			return EndpointListResult{Cmd: cmd}
		}
		switch {
		case key.Matches(msg, e.keys.Up):
			if e.cursor > 0 {
				e.cursor--
			}
		case key.Matches(msg, e.keys.Down):
			if e.cursor < len(e.filtered)-1 {
				e.cursor++
			}
		case key.Matches(msg, e.keys.Enter):
			if len(e.filtered) > 0 {
				return EndpointListResult{Selected: &e.filtered[e.cursor]}
			}
		case key.Matches(msg, e.keys.Search):
			e.searching = true
			e.search.Focus()
			return EndpointListResult{Cmd: textinput.Blink}
		case key.Matches(msg, e.keys.Home):
			e.cursor = 0
		case key.Matches(msg, e.keys.End):
			e.cursor = len(e.filtered) - 1
			if e.cursor < 0 {
				e.cursor = 0
			}
		case key.Matches(msg, e.keys.PageUp):
			e.cursor -= 10
			if e.cursor < 0 {
				e.cursor = 0
			}
		case key.Matches(msg, e.keys.PageDown):
			e.cursor += 10
			if e.cursor >= len(e.filtered) {
				e.cursor = len(e.filtered) - 1
			}
			if e.cursor < 0 {
				e.cursor = 0
			}
		}
	}

	return EndpointListResult{Cmd: cmd}
}

// saveScripts captures the inline pre/post buffers as an overlay.Scripts
// pair and asks the host to persist them under this tag's name.
func (e *EndpointList) saveScripts() EndpointListResult {
	s := overlay.Scripts{
		Pre:  e.scriptPreEditor.Value(),
		Post: e.scriptPostEditor.Value(),
	}
	e.statusMsg = "scripts saved"
	return EndpointListResult{
		SaveTagScripts: true,
		TagName:        e.tag.Name,
		Scripts:        s,
	}
}

// filterEndpoints filters endpoints by search query
func (e *EndpointList) filterEndpoints(query string) {
	if query == "" {
		e.filtered = e.tag.Endpoints
		e.cursor = 0
		return
	}

	query = strings.ToLower(query)
	var filtered []openapi.Endpoint
	for _, ep := range e.tag.Endpoints {
		if strings.Contains(strings.ToLower(ep.Path), query) ||
			strings.Contains(strings.ToLower(ep.Summary), query) ||
			strings.Contains(strings.ToLower(ep.Method), query) ||
			strings.Contains(strings.ToLower(ep.OperationID), query) {
			filtered = append(filtered, ep)
		}
	}
	e.filtered = filtered
	e.cursor = 0
}

// View renders the endpoint list
func (e *EndpointList) View(width, height int) string {
	e.width = width
	e.height = height
	e.clickMap.Reset()

	var b strings.Builder

	// Title
	title := shared.TitleStyle.Render(e.tag.Name)
	b.WriteString(title)
	b.WriteString("\n")

	// Description
	if e.tag.Description != "" {
		desc := shared.SubtitleStyle.Render(shared.TruncateWithEllipsis(e.tag.Description, width-4))
		b.WriteString(desc)
		b.WriteString("\n")
	}
	b.WriteString("\n")

	// Tab bar — Requests | Scripts. Register click ranges so users can
	// jump between tabs with the mouse.
	tabs := []string{"1:Requests", "2:Scripts"}
	tabIDs := []int{clickIDEndpointTabRequests, clickIDEndpointTabScripts}
	var tabViews []string
	for i, t := range tabs {
		if i == e.activeTab {
			tabViews = append(tabViews, shared.ActiveTabStyle.Render(t))
		} else {
			tabViews = append(tabViews, shared.InactiveTabStyle.Render(t))
		}
	}
	tabBar := lipgloss.JoinHorizontal(lipgloss.Bottom, tabViews...)
	e.tabRowY = strings.Count(b.String(), "\n")
	e.tabBounds = e.tabBounds[:0]
	xCursor := 0
	for i, tv := range tabViews {
		w := lipgloss.Width(tv)
		e.clickMap.AddRange(e.tabRowY, xCursor, xCursor+w, tabIDs[i])
		e.tabBounds = append(e.tabBounds, xCursor+w)
		xCursor += w
	}
	b.WriteString(tabBar)
	b.WriteString("\n")
	b.WriteString(shared.DimStyle.Render(strings.Repeat("─", width-4)))
	b.WriteString("\n")

	// Dispatch to per-tab renderer. The Scripts tab uses the rest of the
	// vertical space for its editor; the Requests tab keeps the existing
	// search/filter/list layout.
	if e.activeTab == EndpointTabScripts {
		contentStartRow := strings.Count(b.String(), "\n")
		b.WriteString(e.renderScriptsTab(contentStartRow))
		return lipgloss.NewStyle().Width(width).Render(b.String())
	}

	// Search bar if active
	if e.searching {
		b.WriteString(e.search.View())
		b.WriteString("\n\n")
	} else if e.search.Value() != "" {
		b.WriteString(shared.DimStyle.Render(fmt.Sprintf("Filter: %s", e.search.Value())))
		b.WriteString("\n\n")
	}

	authorHint := shared.DimStyle.Render("n:new  e:edit  d:delete  y:duplicate  enter:open")

	// Calculate available height for list. Tab bar + divider take 2 extra
	// rows beyond the original layout.
	headerHeight := 7
	if e.tag.Description != "" {
		headerHeight++
	}
	if e.searching || e.search.Value() != "" {
		headerHeight += 2
	}
	listHeight := height - headerHeight - 1 // reserve a row for the hint

	// Empty state
	if len(e.filtered) == 0 {
		b.WriteString(shared.DimStyle.Render("  No matching endpoints"))
		b.WriteString("\n\n")
		b.WriteString(authorHint)
		b.WriteString("\n")
		return lipgloss.NewStyle().Width(width).Render(b.String())
	}

	// Render endpoint list with scrolling
	startIdx := 0
	if e.cursor >= listHeight {
		startIdx = e.cursor - listHeight + 1
	}

	// Size the path column to fit the widest path in the visible window
	// (so paths aren't chopped while empty space sits to their right),
	// while reserving a minimum for the summary column. Long paths only
	// truncate when there's truly no room left.
	const (
		colCursor       = 2
		colMethod       = 8
		colPathPaddingR = 2
		minSummaryWidth = 24
		minPathWidth    = 16
	)
	available := width - colCursor - colMethod - colPathPaddingR
	if available < minPathWidth+minSummaryWidth {
		available = minPathWidth + minSummaryWidth
	}
	widestPath := minPathWidth
	for i := startIdx; i < len(e.filtered) && i < startIdx+listHeight; i++ {
		if w := lipgloss.Width(e.filtered[i].Path); w > widestPath {
			widestPath = w
		}
	}
	pathColWidth := widestPath
	if maxPath := available - minSummaryWidth; pathColWidth > maxPath {
		pathColWidth = maxPath
	}
	if pathColWidth < minPathWidth {
		pathColWidth = minPathWidth
	}
	summaryWidth := available - pathColWidth
	if summaryWidth < minSummaryWidth {
		summaryWidth = minSummaryWidth
	}

	// Build table rows
	var rows [][]string
	for i := startIdx; i < len(e.filtered) && i < startIdx+listHeight; i++ {
		ep := e.filtered[i]
		cursor := " "
		if i == e.cursor {
			cursor = ">"
		}

		summary := shared.TruncateWithEllipsis(ep.Summary, summaryWidth)
		if ep.Deprecated {
			summary = "[DEPRECATED] " + summary
		}

		rows = append(rows, []string{
			cursor,
			ep.Method,
			shared.TruncateWithEllipsis(ep.Path, pathColWidth),
			summary,
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
			isSelected := actualIdx == e.cursor

			switch col {
			case 0: // Cursor column - minimal width, no padding
				style := lipgloss.NewStyle().Width(2)
				if isSelected {
					return style.Foreground(shared.ColorPrimary).Bold(true)
				}
				return style
			case 1: // Method column
				ep := e.filtered[actualIdx]
				methodColor, ok := shared.MethodColors[ep.Method]
				if !ok {
					methodColor = shared.ColorMuted
				}
				return lipgloss.NewStyle().Width(8).Foreground(methodColor).Bold(true)
			case 2: // Path column
				style := lipgloss.NewStyle().PaddingRight(2)
				if isSelected {
					return style.Foreground(shared.ColorPrimary).Bold(true)
				}
				return style.Foreground(lipgloss.Color("#E5E7EB"))
			case 3: // Summary column
				ep := e.filtered[actualIdx]
				if ep.Deprecated {
					return lipgloss.NewStyle().Foreground(shared.ColorWarning)
				}
				return lipgloss.NewStyle().Foreground(shared.ColorMuted)
			}

			return lipgloss.NewStyle()
		})

	// Register click rows now that the header is fully written. The hidden
	// table border adds 1 row above the first data row. Note: don't reset
	// the click map here — the tab bar registered its entries earlier.
	tableStartRow := strings.Count(b.String(), "\n")
	firstItemRow := tableStartRow + 1
	for i := startIdx; i < startIdx+len(rows); i++ {
		e.clickMap.AddRow(firstItemRow+(i-startIdx), i)
	}

	b.WriteString(t.String())
	b.WriteString("\n")
	b.WriteString(authorHint)

	return lipgloss.NewStyle().Width(width).Render(b.String())
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// renderScriptsTab hosts inline pre/post-request script editors scoped to
// this tag. ctrl+p toggles phase, ctrl+s saves, ctrl+v hands the active
// buffer to $EDITOR. Matches the request-builder Scripts tab UX so the
// muscle memory carries over.
func (e *EndpointList) renderScriptsTab(startScreenRow int) string {
	var b strings.Builder

	// Phase indicator (Pre / Post). ctrl+p toggles between them; the
	// labels aren't number-prefixed to avoid colliding with the outer
	// 1/2 tab shortcuts.
	preLabel := "Pre"
	postLabel := "Post"
	if e.scriptPhase == ScriptPhasePre {
		b.WriteString(shared.ActiveTabStyle.Render(preLabel))
		b.WriteString("  ")
		b.WriteString(shared.InactiveTabStyle.Render(postLabel))
	} else {
		b.WriteString(shared.InactiveTabStyle.Render(preLabel))
		b.WriteString("  ")
		b.WriteString(shared.ActiveTabStyle.Render(postLabel))
	}
	if e.statusMsg != "" {
		b.WriteString("  ")
		b.WriteString(shared.SuccessStyle.Render(e.statusMsg))
		// Clear after rendering once so the message doesn't stick after
		// the next keystroke.
		e.statusMsg = ""
	}
	b.WriteString("\n")
	b.WriteString(shared.DimStyle.Render(strings.Repeat("─", e.width-4)))
	b.WriteString("\n")

	// Editor fills the rest of the vertical budget, reserving one row for
	// its built-in footer.
	editorStartRow := startScreenRow + strings.Count(b.String(), "\n")
	editorHeight := e.height - editorStartRow - 1
	if editorHeight < 3 {
		editorHeight = 3
	}

	ed := e.activeScriptEditor()
	ed.SetSize(e.width-2, editorHeight)
	ed.SetMouseOrigin(0, editorStartRow)
	b.WriteString(ed.View())
	b.WriteString("\n")
	b.WriteString(ed.Footer(
		[]shared.Hint{
			{Key: "ctrl+s", Label: "save"},
			{Key: "ctrl+p", Label: "pre/post"},
			{Key: "ctrl+v", Label: "external editor"},
		},
		e.width-2,
	))

	return b.String()
}
