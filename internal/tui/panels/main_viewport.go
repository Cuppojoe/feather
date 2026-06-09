package panels

import (
	"charm.land/lipgloss/v2"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/cuppojoe/feather/internal/http"
	"github.com/cuppojoe/feather/internal/models"
	"github.com/cuppojoe/feather/internal/openapi"
	"github.com/cuppojoe/feather/internal/overlay"
	"github.com/cuppojoe/feather/internal/tui/screens"
	"github.com/cuppojoe/feather/internal/tui/shared"
)

// ViewType represents the current view in the main viewport
type ViewType int

const (
	ViewTagList ViewType = iota
	ViewEndpointList
	ViewRequestBuilder
)

// MainViewport contains the main content area (tag list, endpoints, request builder)
type MainViewport struct {
	view           ViewType
	tagList        *screens.MainMenu
	endpointList   *screens.EndpointList
	requestBuilder *screens.RequestBuilder

	// For navigation
	currentTag      *openapi.TagGroup
	currentEndpoint *openapi.Endpoint

	// Context reference for auto-fill
	context *models.Context

	focused bool
	keyMap  shared.KeyMap
	spec    *openapi.ParsedSpec
	overlay *overlay.Overlay
	width   int
	height  int
}

// MainViewportResult is the result of a main viewport update
type MainViewportResult struct {
	ExecuteRequest bool
	Request        *http.Request
	Endpoint       *openapi.Endpoint

	// SaveOverride is set when the user saves the current request builder state
	// to the overlay. Override holds the captured override for Endpoint.
	SaveOverride bool
	Override     *overlay.OpOverride

	// SaveTagScripts / SaveProfileScripts are set when the user saves the
	// inline tag- or profile-scope scripts respectively. The Scripts field
	// is shared because the two intents are mutually exclusive per result.
	SaveTagScripts     bool
	TagName            string
	SaveProfileScripts bool
	Scripts            overlay.Scripts

	// Authoring intents surfaced to the app.
	OpenNewOp      bool   // open the editor to create a request
	NewOpTag       string // category to prefill on create
	EditOp         *openapi.Endpoint
	DeleteOp       *openapi.Endpoint
	DuplicateOp    *openapi.Endpoint
	NewCategory    bool
	RenameCategory string // category name to rename
	DeleteCategory string // category name to delete

	Cmd tea.Cmd
}

// NewMainViewport creates a new main viewport
func NewMainViewport(spec *openapi.ParsedSpec, ctx *models.Context, keys shared.KeyMap, ov *overlay.Overlay) *MainViewport {
	return &MainViewport{
		view:    ViewTagList,
		tagList: screens.NewMainMenu(spec.Tags, keys, ov),
		context: ctx,
		keyMap:  keys,
		spec:    spec,
		overlay: ov,
	}
}

// RefreshSpec swaps in an updated spec and rebuilds navigation. When focusTag
// names a non-empty category it reopens that category's endpoint list so the
// user stays oriented after an edit; otherwise it returns to the tag list.
func (m *MainViewport) RefreshSpec(spec *openapi.ParsedSpec, focusTag string) {
	m.spec = spec
	m.tagList = screens.NewMainMenu(spec.Tags, m.keyMap, m.overlay)
	m.endpointList = nil
	m.requestBuilder = nil
	m.currentTag = nil
	m.currentEndpoint = nil
	m.view = ViewTagList

	if focusTag != "" {
		for i := range spec.Tags {
			if spec.Tags[i].Name == focusTag && len(spec.Tags[i].Endpoints) > 0 {
				m.currentTag = &spec.Tags[i]
				m.endpointList = screens.NewEndpointList(&spec.Tags[i], m.keyMap, m.overlay)
				m.view = ViewEndpointList
				break
			}
		}
	}
}

// SetFocused sets the focus state
func (m *MainViewport) SetFocused(focused bool) {
	m.focused = focused
}

// IsFocused returns whether the panel is focused
func (m *MainViewport) IsFocused() bool {
	return m.focused
}

// ID returns the panel identifier
func (m *MainViewport) ID() string {
	return "main"
}

// IsVisible returns whether the panel is visible (main viewport is always visible)
func (m *MainViewport) IsVisible() bool {
	return true
}

// CurrentView returns the current view type
func (m *MainViewport) CurrentView() ViewType {
	return m.view
}

// IsEditing returns true if any inner screen is mid-edit on an inline
// editor or input — the request builder (params/body/scripts) or the
// endpoint list's inline tag-script editors.
func (m *MainViewport) IsEditing() bool {
	switch m.view {
	case ViewRequestBuilder:
		return m.requestBuilder != nil && m.requestBuilder.IsEditing()
	case ViewEndpointList:
		return m.endpointList != nil && m.endpointList.IsEditing()
	}
	return false
}

// CurrentEndpoint returns the endpoint the user is currently looking at in
// the main viewport (the one whose request builder is showing). Returns nil
// when the user is at the tag/category list level.
func (m *MainViewport) CurrentEndpoint() *openapi.Endpoint { return m.currentEndpoint }

// IsCapturingInput reports whether the inner screen wants all key input
// (editing a field OR running a search), so the app can bypass its global
// single-key bindings.
func (m *MainViewport) IsCapturingInput() bool {
	switch m.view {
	case ViewRequestBuilder:
		return m.requestBuilder != nil && m.requestBuilder.IsCapturingInput()
	case ViewTagList:
		return m.tagList != nil && m.tagList.IsEditing()
	case ViewEndpointList:
		return m.endpointList != nil && m.endpointList.IsEditing()
	}
	return false
}

// RequestComplete signals the request builder that an in-flight request
// has finished so it can stop its spinner.
func (m *MainViewport) RequestComplete() {
	if m.requestBuilder != nil {
		m.requestBuilder.SetExecuting(false)
	}
}

// RefreshContext tells whichever inner screen is showing to re-pull
// values from the live context. Called by the host after an environment
// switch / edit so an already-open request reflects the new variables
// without forcing the user to exit and re-enter the request.
func (m *MainViewport) RefreshContext() {
	if m.requestBuilder != nil {
		m.requestBuilder.RefreshFromContext()
	}
}

// NavigateBack navigates to the previous view
func (m *MainViewport) NavigateBack() bool {
	switch m.view {
	case ViewEndpointList:
		m.view = ViewTagList
		return true
	case ViewRequestBuilder:
		m.view = ViewEndpointList
		return true
	}
	return false
}

// handleAuthoringKey maps an authoring shortcut in a navigation list to an
// intent surfaced to the app. Returns nil when the key isn't an authoring
// shortcut for the current view.
func (m *MainViewport) handleAuthoringKey(key string) *MainViewportResult {
	switch m.view {
	case ViewTagList:
		switch key {
		case "n": // new request, defaulting to the highlighted category
			tag := ""
			if t := m.tagList.SelectedTag(); t != nil {
				tag = t.Name
			}
			return &MainViewportResult{OpenNewOp: true, NewOpTag: tag}
		case "N": // new category
			return &MainViewportResult{NewCategory: true}
		case "e": // rename highlighted category
			if t := m.tagList.SelectedTag(); t != nil {
				return &MainViewportResult{RenameCategory: t.Name}
			}
		case "d": // delete highlighted category
			if t := m.tagList.SelectedTag(); t != nil {
				return &MainViewportResult{DeleteCategory: t.Name}
			}
		}
	case ViewEndpointList:
		switch key {
		case "n": // new request in the current category
			tag := ""
			if m.currentTag != nil {
				tag = m.currentTag.Name
			}
			return &MainViewportResult{OpenNewOp: true, NewOpTag: tag}
		case "e":
			if ep := m.endpointList.Selected(); ep != nil {
				return &MainViewportResult{EditOp: ep}
			}
		case "d":
			if ep := m.endpointList.Selected(); ep != nil {
				return &MainViewportResult{DeleteOp: ep}
			}
		case "y":
			if ep := m.endpointList.Selected(); ep != nil {
				return &MainViewportResult{DuplicateOp: ep}
			}
		}
	}
	return nil
}

// Update handles input for the main viewport
func (m *MainViewport) Update(msg tea.Msg) MainViewportResult {
	var cmd tea.Cmd

	if !m.focused {
		return MainViewportResult{}
	}

	// Authoring shortcuts in the navigation lists (guarded against list search,
	// where these letters are literal input).
	if km, ok := msg.(tea.KeyMsg); ok && !m.IsCapturingInput() {
		if r := m.handleAuthoringKey(km.String()); r != nil {
			return *r
		}
	}

	// The viewport wraps inner screens with BorderLeft(1) + PaddingLeft(1),
	// so the inner content's column 0 sits at screen column 2. Translate
	// mouse X so screens see content-relative coordinates and their
	// ClickMap registrations line up.
	if mm, ok := msg.(tea.MouseMsg); ok {
		mm.X -= 2
		msg = mm
	}

	switch m.view {
	case ViewTagList:
		result := m.tagList.Update(msg)
		if result.SaveProfileScripts {
			return MainViewportResult{
				SaveProfileScripts: true,
				Scripts:            result.Scripts,
				Cmd:                result.Cmd,
			}
		}
		if result.Selected != nil {
			m.currentTag = result.Selected
			m.endpointList = screens.NewEndpointList(result.Selected, m.keyMap, m.overlay)
			m.view = ViewEndpointList
		}
		return MainViewportResult{Cmd: result.Cmd}

	case ViewEndpointList:
		result := m.endpointList.Update(msg)
		if result.SaveTagScripts {
			return MainViewportResult{
				SaveTagScripts: true,
				TagName:        result.TagName,
				Scripts:        result.Scripts,
				Cmd:            result.Cmd,
			}
		}
		if result.Selected != nil {
			m.currentEndpoint = result.Selected
			// Create request builder with the live context, the spec's
			// schemas (for example generation) and any overlay override
			// for this operation (saved body/params/headers).
			ovr := m.overlay.EffectiveOverride(result.Selected.Method, result.Selected.Path)
			m.requestBuilder = screens.NewRequestBuilder(result.Selected, m.context, m.keyMap, m.spec.Schemas, ovr, m.overlay)
			m.view = ViewRequestBuilder
		}
		return MainViewportResult{Cmd: result.Cmd}

	case ViewRequestBuilder:
		result := m.requestBuilder.Update(msg)
		if result.Execute {
			return MainViewportResult{
				ExecuteRequest: true,
				Request:        result.Request,
				Endpoint:       result.Endpoint,
				Cmd:            result.Cmd,
			}
		}
		if result.Save && result.Override != nil && result.Endpoint != nil {
			return MainViewportResult{
				SaveOverride: true,
				Override:     result.Override,
				Endpoint:     result.Endpoint,
				Cmd:          result.Cmd,
			}
		}
		return MainViewportResult{Cmd: result.Cmd}
	}

	return MainViewportResult{Cmd: cmd}
}

// View renders the main viewport
func (m *MainViewport) View(width, height int) string {
	m.width = width
	m.height = height

	// Account for padding (1 char each side) and focus indicator (1 char left)
	innerWidth := width - 3
	innerHeight := height

	var content string
	switch m.view {
	case ViewTagList:
		content = m.tagList.View(innerWidth, innerHeight)
	case ViewEndpointList:
		content = m.endpointList.View(innerWidth, innerHeight)
	case ViewRequestBuilder:
		content = m.requestBuilder.View(innerWidth, innerHeight)
	}

	// Style with horizontal padding to match other panels
	// Use a left border as focus indicator
	style := lipgloss.NewStyle().
		Width(width).
		Height(height).
		PaddingLeft(1).
		PaddingRight(1).
		BorderLeft(true).
		BorderStyle(lipgloss.Border{Left: "│"})

	if m.focused {
		style = style.BorderForeground(shared.ColorPrimary)
	} else {
		style = style.BorderForeground(shared.ColorBorder)
	}

	return style.Render(content)
}

// GetBreadcrumb returns a breadcrumb string for the current view
func (m *MainViewport) GetBreadcrumb() string {
	style := lipgloss.NewStyle().Foreground(shared.ColorMuted)
	highlight := lipgloss.NewStyle().Foreground(shared.ColorPrimary)

	switch m.view {
	case ViewTagList:
		return highlight.Render("Tags")
	case ViewEndpointList:
		if m.currentTag != nil {
			return style.Render("Tags > ") + highlight.Render(m.currentTag.Name)
		}
		return highlight.Render("Endpoints")
	case ViewRequestBuilder:
		if m.currentTag != nil && m.currentEndpoint != nil {
			return style.Render("Tags > "+m.currentTag.Name+" > ") +
				highlight.Render(m.currentEndpoint.Method+" "+m.currentEndpoint.Path)
		}
		return highlight.Render("Request")
	}
	return ""
}
