package screens

import (
	"encoding/json"
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/cuppojoe/feather/internal/http"
	"github.com/cuppojoe/feather/internal/models"
	"github.com/cuppojoe/feather/internal/openapi"
	"github.com/cuppojoe/feather/internal/overlay"
	"github.com/cuppojoe/feather/internal/tui/shared"
)

// editorCaller* identify external-editor messages destined for one of the
// inline editors. Routed via shared.EditExternalMsg.Caller — each editor
// gets its own caller ID so the result lands back in the right buffer even
// if the user switched phases while the external editor was open.
const (
	editorCallerBody       = "request_builder_body"
	editorCallerScriptPre  = "request_builder_script_pre"
	editorCallerScriptPost = "request_builder_script_post"
)

// Script phase indices for the Scripts tab inline editors.
const (
	ScriptPhasePre = iota
	ScriptPhasePost
)

// Tab indices for request builder
const (
	TabExecute = iota
	TabParams
	TabBody
	TabHeaders
	TabScripts
)

// Row types in Execute tab
const (
	RowTypeParam = iota
	RowTypeExecuteButton
)

// RequestBuilder builds and executes API requests
type RequestBuilder struct {
	endpoint    *openapi.Endpoint
	context     *models.Context
	values      map[string]string // Parameter values
	fromContext map[string]bool   // Track which values came from context
	pathParams  []openapi.Parameter
	queryParams []openapi.Parameter
	activeTab   int // Current tab
	cursor      int // Current row within tab
	editing     bool
	input       textinput.Model
	bodyEditor  shared.TextEditor // inline editor for the request body
	keys        shared.KeyMap
	width       int
	height      int
	clickMap    shared.ClickMap
	executing   bool
	spinner     spinner.Model

	// override is the overlay entry for this operation (may be nil). It seeds
	// the body/params/headers and is the base that saveOverride extends.
	override *overlay.OpOverride

	// overlay is the full overlay for the active profile. Used by the
	// Scripts tab to show profile- and tag-scope scripts that will run
	// alongside the operation-level ones (may be nil).
	overlay *overlay.Overlay

	// scriptPhase selects which inline script editor (pre or post) is
	// active on the Scripts tab; ctrl+p toggles it.
	scriptPhase      int
	scriptPreEditor  shared.TextEditor
	scriptPostEditor shared.TextEditor

	// Declarative request-level headers. The KVEditor holds the
	// authoring view; values are stored verbatim (with ${env_var}
	// references intact) and substituted against the live context at
	// send time in buildRequest. Saved into r.override.Headers when the
	// user presses ctrl+s on this tab.
	headersEditor shared.KVEditor

	// Query parameter editor. Seeded with spec-defined query params and
	// their current values. Add/remove/edit go through here; ad-hoc rows
	// (names not declared on the endpoint) ride through to send time via
	// http.RequestBuilder.SetExtraQueryParams.
	paramsEditor shared.KVEditor
}

// Click-target IDs. Negative IDs are tab targets, non-negative are field
// row indices so we can store both in a single map without ambiguity.
const (
	clickIDTabExecute = -1
	clickIDTabParams  = -2
	clickIDTabBody    = -3
	clickIDTabHeaders = -4
	clickIDTabScripts = -5
)

// IsEditing returns true if the request builder is currently editing a field
// — either a param value (textinput), the request body, the active
// script editor on the Scripts tab, or one of the KV editors' inline cells.
func (r *RequestBuilder) IsEditing() bool {
	if r.editing {
		return true
	}
	if r.activeTab == TabBody && r.bodyEditor.Focused() {
		return true
	}
	if r.activeTab == TabScripts && r.activeScriptEditor().Focused() {
		return true
	}
	if r.activeTab == TabHeaders && r.headersEditor.IsEditing() {
		return true
	}
	if r.activeTab == TabParams && r.paramsEditor.IsEditing() {
		return true
	}
	return false
}

// activeScriptEditor returns a pointer to whichever script editor (pre or
// post) the user is currently working on. Used in render + input routing
// so the rest of the panel doesn't have to fan out into two branches.
func (r *RequestBuilder) activeScriptEditor() *shared.TextEditor {
	if r.scriptPhase == ScriptPhasePost {
		return &r.scriptPostEditor
	}
	return &r.scriptPreEditor
}

// IsCapturingInput reports whether the user is actively typing text into
// an inline editor and the app should NOT intercept single-letter global
// shortcuts. True only when characters are being typed (param textinput,
// body editor, scripts editor, or a KV editor's cell-edit textinput).
// A "merely focused" KV editor in nav mode is not text capture — its
// shortcuts are lowercase letters that don't collide with the global
// uppercase set, so globals can fire normally without disrupting kv
// navigation.
func (r *RequestBuilder) IsCapturingInput() bool {
	return r.IsEditing()
}

// RequestBuilderResult is the result of a request builder update
type RequestBuilderResult struct {
	Execute  bool
	Request  *http.Request
	Endpoint *openapi.Endpoint

	// Save is set when the user saves the current state to the overlay.
	// Override carries the captured override for Endpoint.
	Save     bool
	Override *overlay.OpOverride

	Cmd tea.Cmd
}

// NewRequestBuilder creates a new request builder. schemas is the spec's
// component schema map (used to synthesize a body skeleton when no example
// exists), ovr is the overlay override for this operation (may be nil), and
// ov is the full overlay (used by the Scripts tab to show profile- and
// tag-scope scripts; may be nil).
func NewRequestBuilder(endpoint *openapi.Endpoint, ctx *models.Context, keys shared.KeyMap, schemas map[string]*openapi.Schema, ovr *overlay.OpOverride, ov *overlay.Overlay) *RequestBuilder {
	input := textinput.New()
	input.CharLimit = 500

	// shared.TextEditor only renders the visible window of the buffer per
	// frame; bubbles/textarea iterates every line on every render and was
	// the source of the body-tab lag.
	bodyEditor := shared.NewTextEditor(40, 10) // resized in View
	bodyEditor.SetPlaceholder("JSON request body…")
	bodyEditor.SetLanguage("json")
	bodyEditor.SetExternalEditorID(editorCallerBody)
	bodyEditor.SetExternalEditorExt(".json")

	// Separate path and query params
	var pathParams, queryParams []openapi.Parameter
	for _, p := range endpoint.Parameters {
		switch p.In {
		case "path":
			pathParams = append(pathParams, p)
		case "query":
			queryParams = append(queryParams, p)
		}
	}

	// Pre-populate parameter values. Precedence per param: context value, then
	// overlay default, then an example/default derived from the schema.
	values := make(map[string]string)
	fromContext := make(map[string]bool)
	fillParam := func(p openapi.Parameter) {
		if v := ctx.Get(p.Name); v != "" {
			values[p.Name] = v
			fromContext[p.Name] = true
			return
		}
		if ovr != nil {
			if v := ovr.ParamDefaults[p.Name]; v != "" {
				values[p.Name] = v
				return
			}
		}
		if v := openapi.ExampleForParam(p, schemas); v != "" {
			values[p.Name] = v
		}
	}
	for _, p := range pathParams {
		fillParam(p)
	}
	for _, p := range queryParams {
		fillParam(p)
	}

	// Pre-populate the request body. Precedence: overlay bodyExample, then the
	// spec's explicit JSON example, then a skeleton generated from the schema.
	bodyContent := ""
	if endpoint.RequestBody != nil {
		switch {
		case ovr != nil && ovr.BodyExample != "":
			bodyContent = ovr.BodyExample
		default:
			if mt := jsonMediaType(endpoint.RequestBody); mt != nil {
				if mt.Example != nil {
					bodyContent = stringifyExample(mt.Example)
				} else if mt.Schema != nil {
					bodyContent = openapi.GenerateExampleJSON(mt.Schema, schemas)
				}
			}
		}
	}

	// Bubbles spinner uses lipgloss v1 and feather is on v2 — set the colour
	// at render time via a wrapper style rather than spinner.WithStyle.
	sp := spinner.New(spinner.WithSpinner(spinner.Dot))

	bodyEditor.SetValue(bodyContent)

	// Inline editors for the operation-scoped pre/post-request scripts.
	// Seeded from the existing override so a save round-trips. Profile- and
	// tag-scope scripts still live in the global Scripts modal (J).
	preEditor := shared.NewTextEditor(40, 10)
	preEditor.SetLanguage("javascript")
	preEditor.SetPlaceholder("// JS pre-request hook, runs before this request fires")
	preEditor.SetExternalEditorID(editorCallerScriptPre)
	preEditor.SetExternalEditorExt(".js")
	postEditor := shared.NewTextEditor(40, 10)
	postEditor.SetLanguage("javascript")
	postEditor.SetPlaceholder("// JS post-request hook, runs after the response comes back")
	postEditor.SetExternalEditorID(editorCallerScriptPost)
	postEditor.SetExternalEditorExt(".js")
	if ovr != nil {
		preEditor.SetValue(ovr.Scripts.Pre)
		postEditor.SetValue(ovr.Scripts.Post)
	}

	// Headers editor — seeded from the saved override. Values keep their
	// ${env_var} references verbatim; substitution happens at send time
	// against the live context.
	headersEditor := shared.NewKVEditor()
	if ovr != nil && len(ovr.Headers) > 0 {
		headersEditor.SetValues(ovr.Headers)
	}

	// Params editor — seeded with spec-defined query params and their
	// current values. Ad-hoc rows (names not in queryParams) ride through
	// as extras at send time.
	paramsEditor := shared.NewKVEditor()
	paramsSeed := make(map[string]string, len(queryParams))
	for _, p := range queryParams {
		paramsSeed[p.Name] = values[p.Name]
	}
	if len(paramsSeed) > 0 {
		paramsEditor.SetValues(paramsSeed)
	}

	rb := &RequestBuilder{
		endpoint:         endpoint,
		context:          ctx,
		values:           values,
		fromContext:      fromContext,
		pathParams:       pathParams,
		queryParams:      queryParams,
		input:            input,
		bodyEditor:       bodyEditor,
		scriptPreEditor:  preEditor,
		scriptPostEditor: postEditor,
		headersEditor:    headersEditor,
		paramsEditor:     paramsEditor,
		keys:             keys,
		activeTab:        TabExecute,
		spinner:          sp,
		override:         ovr,
		overlay:          ov,
	}

	// Set initial cursor position
	rb.cursor = rb.findInitialCursor()

	return rb
}

// findInitialCursor finds the right initial cursor position
// If all required path params are filled, focus on Execute button
// Otherwise, focus on first unfilled required path param.
func (r *RequestBuilder) findInitialCursor() int {
	for i, p := range r.pathParams {
		if p.Required && r.values[p.Name] == "" {
			return i
		}
	}
	return len(r.pathParams) // execute button index
}

// initialCursorForTab picks the right starting cursor for a tab — Execute
// jumps to the first unfilled required path param (or the execute button),
// every other tab starts at row 0.
func (r *RequestBuilder) initialCursorForTab(tab int) int {
	if tab == TabExecute {
		return r.findInitialCursor()
	}
	return 0
}

// setActiveTab handles a tab transition. It blurs whichever KV editor (if
// any) was attached to the old tab and auto-focuses the new tab's editor,
// so Headers / Params land the user "inside" the editor without a manual
// Enter — saving a keystroke on the edit path and letting the next Esc
// exit the screen directly instead of just defocusing.
func (r *RequestBuilder) setActiveTab(tab int) {
	if r.activeTab == tab {
		return
	}
	switch r.activeTab {
	case TabHeaders:
		r.headersEditor.Blur()
	case TabParams:
		r.paramsEditor.Blur()
	}
	r.activeTab = tab
	r.cursor = r.initialCursorForTab(tab)
	switch tab {
	case TabHeaders:
		r.headersEditor.Focus()
	case TabParams:
		r.paramsEditor.Focus()
	}
}

// executeRowIndex returns the index of the execute button row
func (r *RequestBuilder) executeRowIndex() int {
	return len(r.pathParams)
}

// tabRowCount returns the number of rows in the current tab
func (r *RequestBuilder) tabRowCount() int {
	switch r.activeTab {
	case TabExecute:
		return len(r.pathParams) + 1 // +1 for execute button
	case TabParams:
		return 1
	case TabBody:
		if r.endpoint.RequestBody != nil {
			return 1
		}
		return 0
	case TabHeaders:
		return 1
	case TabScripts:
		return 1
	}
	return 0
}

// Update handles input for the request builder
func (r *RequestBuilder) Update(msg tea.Msg) RequestBuilderResult {
	var cmd tea.Cmd

	// Spinner advance ticks. Keep ticking only while a request is in flight.
	if _, ok := msg.(spinner.TickMsg); ok {
		r.spinner, cmd = r.spinner.Update(msg)
		if !r.executing {
			cmd = nil
		}
		return RequestBuilderResult{Cmd: cmd}
	}

	// External editor returned — each editor watches its own caller ID
	// (set via SetExternalEditorID in NewRequestBuilder), so forwarding
	// the message into whichever editor backs the active tab is enough.
	if _, ok := msg.(shared.EditExternalMsg); ok {
		switch r.activeTab {
		case TabBody:
			cmd = r.bodyEditor.Update(msg)
		case TabScripts:
			cmd = r.activeScriptEditor().Update(msg)
		}
		return RequestBuilderResult{Cmd: cmd}
	}

	// Editor blink ticks always go through to keep the cursor animating.
	if r.activeTab == TabBody {
		if _, ok := msg.(shared.EditorBlinkMsg); ok {
			cmd = r.bodyEditor.Update(msg)
			return RequestBuilderResult{Cmd: cmd}
		}
	}
	if r.activeTab == TabScripts {
		if _, ok := msg.(shared.EditorBlinkMsg); ok {
			cmd = r.activeScriptEditor().Update(msg)
			return RequestBuilderResult{Cmd: cmd}
		}
	}

	// Body tab: keys and mouse wheel route into the editor while it has
	// focus; ctrl+w toggles focus so the keyboard can get back to tab nav.
	if r.activeTab == TabBody && !r.editing {
		// When the editor's language picker is open it owns every key —
		// don't let tab nav, save, etc. fire while picking.
		if r.bodyEditor.PickerOpen() {
			cmd = r.bodyEditor.Update(msg)
			return RequestBuilderResult{Cmd: cmd}
		}
		if km, ok := msg.(tea.KeyMsg); ok {
			switch {
			case key.Matches(km, r.keys.Save):
				return r.saveOverride()
			case key.Matches(km, r.keys.Tab), key.Matches(km, r.keys.ShiftTab):
				// fall through to the outer handler for tab nav
			case km.String() == "1", km.String() == "2",
				km.String() == "3", km.String() == "4", km.String() == "5":
				// Tab-switch shortcuts ONLY when the editor isn't
				// taking input — otherwise typing digits into a JSON
				// body silently teleports the user to another tab.
				if r.bodyEditor.Focused() {
					cmd = r.bodyEditor.Update(msg)
					return RequestBuilderResult{Cmd: cmd}
				}
				// fall through for tab nav
			case key.Matches(km, r.keys.Enter) && !r.bodyEditor.Focused():
				// Forward Enter to the editor so its built-in
				// focus-on-enter behaviour fires.
				cmd = r.bodyEditor.Update(msg)
				return RequestBuilderResult{Cmd: cmd}
			default:
				// ctrl+v is handled by the editor itself (via the
				// external-editor ID set at construction) — same path
				// for every other key when the editor is focused.
				if r.bodyEditor.Focused() {
					cmd = r.bodyEditor.Update(msg)
					return RequestBuilderResult{Cmd: cmd}
				}
			}
		} else if mm, isMouse := msg.(tea.MouseMsg); isMouse {
			switch mm.Button {
			case tea.MouseButtonWheelUp, tea.MouseButtonWheelDown:
				if r.bodyEditor.Focused() {
					cmd = r.bodyEditor.Update(msg)
					return RequestBuilderResult{Cmd: cmd}
				}
			case tea.MouseButtonLeft:
				// Scrollbar press / drag / release. The editor's origin
				// is set in renderBodyTab via SetMouseOrigin.
				cmd = r.bodyEditor.Update(msg)
				if mm.Action != tea.MouseActionRelease {
					return RequestBuilderResult{Cmd: cmd}
				}
			}
		}
	}

	// Headers tab: forward keystrokes to the KV editor. The editor is
	// auto-focused on tab select. In nav mode the editor only claims
	// lowercase letters and arrows; Esc, uppercase letters, and other
	// non-typing keys cascade past us to the outer app-scope handlers
	// (so Esc still pops back to the endpoint list, H still opens
	// History, etc.). Once an inline cell edit is active the editor
	// claims everything except ctrl+s.
	if r.activeTab == TabHeaders && !r.editing {
		if cmd, handled := r.routeKVTabKey(&r.headersEditor, msg); handled {
			return RequestBuilderResult{Cmd: cmd}
		}
	}

	// Params tab: same routing as Headers, against the params KV editor.
	if r.activeTab == TabParams && !r.editing {
		if cmd, handled := r.routeKVTabKey(&r.paramsEditor, msg); handled {
			return RequestBuilderResult{Cmd: cmd}
		}
	}

	// Scripts tab: same routing pattern as Body, against whichever phase
	// editor (pre or post) is active. ctrl+p toggles phase; ctrl+s saves
	// both buffers via saveOverride; ctrl+v hands the active buffer to
	// $EDITOR via a phase-specific caller ID.
	if r.activeTab == TabScripts && !r.editing {
		ed := r.activeScriptEditor()
		if ed.PickerOpen() {
			cmd = ed.Update(msg)
			return RequestBuilderResult{Cmd: cmd}
		}
		if km, ok := msg.(tea.KeyMsg); ok {
			switch {
			case km.String() == "ctrl+p":
				// Toggle pre/post. Blur the editor we're leaving so its
				// cursor doesn't keep blinking out of view.
				ed.Blur()
				if r.scriptPhase == ScriptPhasePre {
					r.scriptPhase = ScriptPhasePost
				} else {
					r.scriptPhase = ScriptPhasePre
				}
				return RequestBuilderResult{}
			case key.Matches(km, r.keys.Save):
				return r.saveOverride()
			case key.Matches(km, r.keys.Tab), key.Matches(km, r.keys.ShiftTab):
				// fall through to the outer handler for tab nav
			case km.String() == "1", km.String() == "2",
				km.String() == "3", km.String() == "4", km.String() == "5":
				// Same guard as the Body tab — let the editor have the
				// digit when it's collecting input, only treat it as a
				// tab-switch shortcut when the editor is blurred.
				if ed.Focused() {
					cmd = ed.Update(msg)
					return RequestBuilderResult{Cmd: cmd}
				}
				// fall through for tab nav
			case key.Matches(km, r.keys.Enter) && !ed.Focused():
				// Forward Enter to focus the editor (same as Body tab).
				cmd = ed.Update(msg)
				return RequestBuilderResult{Cmd: cmd}
			default:
				if ed.Focused() {
					cmd = ed.Update(msg)
					return RequestBuilderResult{Cmd: cmd}
				}
			}
		} else if mm, isMouse := msg.(tea.MouseMsg); isMouse {
			switch mm.Button {
			case tea.MouseButtonWheelUp, tea.MouseButtonWheelDown:
				if ed.Focused() {
					cmd = ed.Update(msg)
					return RequestBuilderResult{Cmd: cmd}
				}
			case tea.MouseButtonLeft:
				cmd = ed.Update(msg)
				if mm.Action != tea.MouseActionRelease {
					return RequestBuilderResult{Cmd: cmd}
				}
			}
		}
	}

	// Handle value editing (path params only — query params live on the
	// Params tab via the KV editor).
	if r.editing {
		switch msg := msg.(type) {
		case tea.KeyMsg:
			switch msg.String() {
			case "enter":
				paramIdx := r.cursor
				if paramIdx < len(r.pathParams) {
					paramName := r.pathParams[paramIdx].Name
					r.values[paramName] = r.input.Value()
					r.fromContext[paramName] = false // Mark as manually edited
				}
				r.editing = false
				r.input.Blur()
				r.cursor = r.findNextCursor(r.cursor + 1)
				return RequestBuilderResult{Cmd: cmd}
			case "esc":
				r.editing = false
				r.input.Blur()
				return RequestBuilderResult{Cmd: cmd}
			}
		}

		r.input, cmd = r.input.Update(msg)
		return RequestBuilderResult{Cmd: cmd}
	}

	// Normal navigation
	switch msg := msg.(type) {
	case tea.MouseMsg:
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			if r.cursor > 0 {
				r.cursor--
			}
		case tea.MouseButtonWheelDown:
			maxRows := r.tabRowCount()
			if r.cursor < maxRows-1 {
				r.cursor++
			}
		case tea.MouseButtonLeft:
			if msg.Action == tea.MouseActionRelease {
				if id, ok := r.clickMap.Hit(msg.X, msg.Y); ok {
					switch id {
					case clickIDTabExecute:
						r.setActiveTab(TabExecute)
					case clickIDTabParams:
						r.setActiveTab(TabParams)
					case clickIDTabBody:
						r.setActiveTab(TabBody)
					case clickIDTabHeaders:
						r.setActiveTab(TabHeaders)
					case clickIDTabScripts:
						r.setActiveTab(TabScripts)
					default:
						if id >= 0 {
							r.cursor = id
							// Click on a row = same as pressing Enter on it.
							return r.handleEnter()
						}
					}
				}
			}
		}

	case tea.KeyMsg:
		switch {
		case key.Matches(msg, r.keys.Left):
			if r.activeTab > 0 {
				r.setActiveTab(r.activeTab - 1)
			}
		case key.Matches(msg, r.keys.Right):
			if r.activeTab < TabScripts {
				r.setActiveTab(r.activeTab + 1)
			}
		case msg.String() == "1":
			r.setActiveTab(TabExecute)
		case msg.String() == "2":
			r.setActiveTab(TabParams)
		case msg.String() == "3":
			r.setActiveTab(TabBody)
		case msg.String() == "4":
			r.setActiveTab(TabHeaders)
		case msg.String() == "5":
			r.setActiveTab(TabScripts)
		case key.Matches(msg, r.keys.Up):
			if r.cursor > 0 {
				r.cursor--
			}
		case key.Matches(msg, r.keys.Down):
			maxRows := r.tabRowCount()
			if r.cursor < maxRows-1 {
				r.cursor++
			}
		case key.Matches(msg, r.keys.Enter):
			return r.handleEnter()
		case key.Matches(msg, r.keys.Save):
			return r.saveOverride()
		case msg.String() == "ctrl+e":
			// Quick execute shortcut
			return r.executeRequest()
		}
	}

	return RequestBuilderResult{Cmd: cmd}
}

// findNextCursor finds the next sensible cursor position. Walks forward
// looking for the next unfilled required path param, otherwise lands on
// the execute button.
func (r *RequestBuilder) findNextCursor(startFrom int) int {
	for i := startFrom; i < len(r.pathParams); i++ {
		if r.pathParams[i].Required && r.values[r.pathParams[i].Name] == "" {
			return i
		}
	}
	if startFrom <= len(r.pathParams) {
		return len(r.pathParams)
	}
	return startFrom
}

// routeKVTabKey is the shared key/mouse dispatch for the two KVEditor-backed
// tabs (Headers and Params). The returned bool tells the caller whether the
// message was fully handled — when false the caller should fall through to
// the outer tab-nav switch so Left/Right/Tab/digit keys can change tabs
// even from inside the editor.
func (r *RequestBuilder) routeKVTabKey(ed *shared.KVEditor, msg tea.Msg) (tea.Cmd, bool) {
	if mm, ok := msg.(tea.MouseMsg); ok {
		switch mm.Button {
		case tea.MouseButtonWheelUp, tea.MouseButtonWheelDown:
			return ed.Update(msg), true
		case tea.MouseButtonLeft:
			cmd := ed.Update(msg)
			// Forward press/drag to the editor; let release fall through
			// to the outer click map so tab-bar clicks still switch tabs.
			if mm.Action != tea.MouseActionRelease {
				return cmd, true
			}
			return cmd, false
		}
		return nil, false
	}
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil, false
	}

	// Inline cell edit: editor owns nearly everything; reserve ctrl+s so
	// users can still save mid-edit without explicitly committing first.
	if ed.IsEditing() {
		if key.Matches(km, r.keys.Save) {
			return nil, false
		}
		return ed.Update(km), true
	}

	if !ed.Focused() {
		// Cursor is on the tab bar but the editor hasn't been entered yet.
		// Let everything fall through to the outer nav — including ctrl+s
		// which is wired up in the outer switch.
		return nil, false
	}

	// Focused, not mid-cell-edit. Forward only the keys the editor itself
	// claims in nav mode (its lowercase letter shortcuts + arrows /
	// home / end / enter). Everything else — Esc, uppercase global
	// letters, ctrl-modified, digit tab shortcuts, ctrl+s — cascades up
	// to the outer scope. setActiveTab handles blur/focus on tab change.
	switch km.String() {
	case "up", "k", "down", "j", "home", "g", "end", "G",
		"a", "enter", "e", "r", "c", "s", "v", "d":
		return ed.Update(km), true
	}
	return nil, false
}

// handleEnter handles the enter key based on current tab and cursor
func (r *RequestBuilder) handleEnter() RequestBuilderResult {
	switch r.activeTab {
	case TabExecute:
		if r.cursor < len(r.pathParams) {
			// Edit path param
			r.editing = true
			r.input.SetValue(r.values[r.pathParams[r.cursor].Name])
			r.input.Focus()
			return RequestBuilderResult{Cmd: textinput.Blink}
		}
		// Execute button
		return r.executeRequest()
	case TabParams:
		// Same pattern as Headers — first Enter focuses the editor, then
		// subsequent Enters begin an inline cell edit.
		if !r.paramsEditor.Focused() {
			return RequestBuilderResult{Cmd: r.paramsEditor.Focus()}
		}
		cmd := r.paramsEditor.Update(shared.KeyMsgFromName("enter"))
		return RequestBuilderResult{Cmd: cmd}
	case TabBody:
		if r.endpoint.RequestBody != nil && !r.bodyEditor.Focused() {
			return RequestBuilderResult{Cmd: r.bodyEditor.Focus()}
		}
	case TabHeaders:
		// Enter forwards to the KV editor — when its outer cursor is on
		// a row this starts editing the value; when not focused yet it
		// makes the editor live.
		if !r.headersEditor.Focused() {
			return RequestBuilderResult{Cmd: r.headersEditor.Focus()}
		}
		// Already focused: let the kv editor handle Enter natively
		// (begin editing the highlighted row's value).
		cmd := r.headersEditor.Update(shared.KeyMsgFromName("enter"))
		return RequestBuilderResult{Cmd: cmd}
	case TabScripts:
		ed := r.activeScriptEditor()
		if !ed.Focused() {
			return RequestBuilderResult{Cmd: ed.Focus()}
		}
	}
	return RequestBuilderResult{}
}

// executeRequest builds and returns the request for execution.
// No-op when a request is already in flight — guards against terminals that
// emit Enter as both \r and \n (two KeyMsg events in a row), which would
// otherwise fire two parallel HTTP requests and two parallel spinner ticks
// before the first response comes back. (Mouse clicks dispatch a single
// MouseActionRelease event, so they don't need this guard.)
func (r *RequestBuilder) executeRequest() RequestBuilderResult {
	if r.executing {
		return RequestBuilderResult{}
	}
	// Check for missing required params
	for _, p := range r.pathParams {
		if p.Required && r.values[p.Name] == "" {
			// Can't execute with missing params
			return RequestBuilderResult{}
		}
	}
	req := r.buildRequest()
	r.executing = true
	return RequestBuilderResult{
		Execute:  true,
		Request:  req,
		Endpoint: r.endpoint,
		Cmd:      r.spinner.Tick,
	}
}

// SetExecuting flips the in-flight flag. App calls this with false when a
// response (or error) comes back so the spinner stops animating.
func (r *RequestBuilder) SetExecuting(executing bool) {
	r.executing = executing
}

// RefreshFromContext re-pulls path / query param values from the live
// context. Used when the active environment changes so an already-open
// request reflects the new variables without forcing the user to back
// out and re-enter the request.
//
// Rules:
//   - Values whose `fromContext` flag is set track the context — refresh.
//   - Values the user manually edited (fromContext flipped false) are
//     left alone so a typed-in override isn't clobbered by an env swap.
//   - Params that currently have NO value but the new context supplies
//     one get filled and marked as context-sourced, so newly added env
//     vars populate empty fields without any prompting.
//   - The Params-tab kv editor mirrors any update for spec-defined query
//     params via SetRowValue so the user sees the new value without
//     losing cursor position or ad-hoc rows.
func (r *RequestBuilder) RefreshFromContext() {
	refresh := func(name string) {
		if r.fromContext[name] {
			r.values[name] = r.context.Get(name)
			return
		}
		if r.values[name] == "" {
			if v := r.context.Get(name); v != "" {
				r.values[name] = v
				r.fromContext[name] = true
			}
		}
	}
	for _, p := range r.pathParams {
		refresh(p.Name)
	}
	for _, p := range r.queryParams {
		refresh(p.Name)
		r.paramsEditor.SetRowValue(p.Name, r.values[p.Name])
	}
}

// buildRequest builds the HTTP request
func (r *RequestBuilder) buildRequest() *http.Request {
	// Sync the latest Params-tab edits into r.values + the ad-hoc map so
	// what the user typed in the kv editor is what we send, even without
	// an explicit ctrl+s.
	r.syncParamsFromEditor()

	builder := http.NewRequestBuilder(r.endpoint)
	// Expand any ${env_var} references in path + query param values
	// against the live context so users can write things like
	// `api_key=${apiKey}` on the Params tab and have it resolve at send
	// time, matching the Headers tab's authoring model.
	builder.SetValues(r.substituteValues(r.values))
	if extras := r.adhocQueryParams(); len(extras) > 0 {
		builder.SetExtraQueryParams(r.substituteValues(extras))
	}

	if r.endpoint.RequestBody != nil {
		body := r.bodyEditor.Value()
		if body != "" {
			builder.SetBodyBytes([]byte(body))
		}
	}

	// Apply any overlay headers saved for this operation. Header values
	// may reference context entries via ${name} — substitute against the
	// current context so e.g. `Authorization: Bearer ${token}` lines up
	// with whatever feather.environment.set("token", ...) most recently left.
	if r.override != nil {
		for k, v := range r.override.Headers {
			builder.SetHeader(k, models.Substitute(v, r.context.Values))
		}
	}

	req, _ := builder.Build()
	return req
}

// substituteValues returns a copy of m with every value passed through
// models.Substitute against the live context. Used to expand ${env_var}
// references in path / query parameter values right before they're sent.
func (r *RequestBuilder) substituteValues(m map[string]string) map[string]string {
	if len(m) == 0 {
		return m
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = models.Substitute(v, r.context.Values)
	}
	return out
}

// syncParamsFromEditor pushes Params-tab kv rows back into r.values for
// any spec-defined query params. Ad-hoc rows aren't merged into r.values
// (which is name-indexed for spec params) — they're picked up separately
// by adhocQueryParams.
func (r *RequestBuilder) syncParamsFromEditor() {
	spec := r.specQueryParamNames()
	for k, v := range r.paramsEditor.Values() {
		if spec[k] {
			r.values[k] = v
			// Manual edit on the Params tab overrides any context-sourced
			// flag so a later RefreshFromContext doesn't clobber it.
			if v != "" {
				r.fromContext[k] = false
			}
		}
	}
}

// adhocQueryParams returns the kv rows whose keys aren't spec-defined
// query params. These get sent via http.RequestBuilder.SetExtraQueryParams.
func (r *RequestBuilder) adhocQueryParams() map[string]string {
	spec := r.specQueryParamNames()
	var out map[string]string
	for k, v := range r.paramsEditor.Values() {
		if spec[k] || v == "" {
			continue
		}
		if out == nil {
			out = map[string]string{}
		}
		out[k] = v
	}
	return out
}

// specQueryParamNames is the set of query parameter names declared on the
// endpoint — used to split Params-tab rows into "spec" vs. "ad-hoc".
func (r *RequestBuilder) specQueryParamNames() map[string]bool {
	out := make(map[string]bool, len(r.queryParams))
	for _, p := range r.queryParams {
		out[p.Name] = true
	}
	return out
}

// saveOverride captures the current body, parameter values, and inline
// pre/post scripts as an overlay override for this operation, preserving
// summary/description/headers/tag from the existing override, and returns
// a result the app persists.
func (r *RequestBuilder) saveOverride() RequestBuilderResult {
	// Sync the Params tab into r.values first so the persisted defaults
	// match whatever the user just typed there.
	r.syncParamsFromEditor()

	ovr := overlay.OpOverride{
		BodyExample: r.bodyEditor.Value(),
		Scripts: overlay.Scripts{
			Pre:  r.scriptPreEditor.Value(),
			Post: r.scriptPostEditor.Value(),
		},
	}
	if r.override != nil {
		ovr.Summary = r.override.Summary
		ovr.Description = r.override.Description
		ovr.Tag = r.override.Tag
	}
	// Declarative headers come from the Headers tab editor. Stored
	// verbatim so ${env_var} refs survive the round-trip; substitution
	// runs at send time in buildRequest.
	if headers := r.headersEditor.Values(); len(headers) > 0 {
		ovr.Headers = headers
	}
	// Persist every non-empty parameter value the user has set.
	for name, v := range r.values {
		if v == "" {
			continue
		}
		if ovr.ParamDefaults == nil {
			ovr.ParamDefaults = map[string]string{}
		}
		ovr.ParamDefaults[name] = v
	}
	// Keep the in-memory override in sync so a subsequent save round-trips.
	saved := ovr
	r.override = &saved
	return RequestBuilderResult{Save: true, Override: &ovr, Endpoint: r.endpoint}
}

// jsonMediaType returns the JSON media type from a request body, preferring
// "application/json" and falling back to the first available content type.
func jsonMediaType(rb *openapi.RequestBody) *openapi.MediaType {
	if rb == nil {
		return nil
	}
	if mt, ok := rb.Content["application/json"]; ok {
		return mt
	}
	for _, mt := range rb.Content {
		return mt
	}
	return nil
}

// stringifyExample renders an explicit example value as indented JSON, or as a
// plain string when it is already a string.
func stringifyExample(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	if b, err := json.MarshalIndent(v, "", "  "); err == nil {
		return string(b)
	}
	return ""
}

// View renders the request builder with tabbed layout
func (r *RequestBuilder) View(width, height int) string {
	r.width = width
	r.height = height
	r.clickMap.Reset()

	var b strings.Builder

	// Title with method and path
	method := shared.MethodStyle(r.endpoint.Method).Render(r.endpoint.Method)
	title := fmt.Sprintf("%s %s", method, r.endpoint.Path)
	b.WriteString(shared.TitleStyle.Render(title))
	b.WriteString("\n")

	// Summary
	if r.endpoint.Summary != "" {
		b.WriteString(shared.SubtitleStyle.Render(r.endpoint.Summary))
		b.WriteString("\n")
	}

	// Resolved URL preview. Path placeholders are filled from r.values
	// with ${env_var} references expanded, so what the user sees here
	// matches what would actually be sent.
	resolvedPath, _ := r.context.SubstitutePath(r.endpoint.Path)
	for k, v := range r.values {
		resolvedPath = strings.ReplaceAll(resolvedPath, "{"+k+"}", models.Substitute(v, r.context.Values))
	}
	urlPreview := r.context.BaseURL + resolvedPath
	b.WriteString(shared.DimStyle.Render("URL: "))
	b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#E5E7EB")).Render(urlPreview))
	b.WriteString("\n\n")

	// Tab bar
	tabs := []string{"1:Execute", "2:Params", "3:Body", "4:Headers", "5:Scripts"}
	var tabViews []string
	for i, tab := range tabs {
		if i == r.activeTab {
			tabViews = append(tabViews, shared.ActiveTabStyle.Render(tab))
		} else {
			tabViews = append(tabViews, shared.InactiveTabStyle.Render(tab))
		}
	}
	tabBar := lipgloss.JoinHorizontal(lipgloss.Bottom, tabViews...)
	// Record exactly where the tab bar lands and how wide each tab is.
	tabRowY := strings.Count(b.String(), "\n")
	tabIDs := []int{clickIDTabExecute, clickIDTabParams, clickIDTabBody, clickIDTabHeaders, clickIDTabScripts}
	xCursor := 0
	for i, tv := range tabViews {
		w := lipgloss.Width(tv)
		r.clickMap.AddRange(tabRowY, xCursor, xCursor+w, tabIDs[i])
		xCursor += w
	}

	b.WriteString(tabBar)
	b.WriteString("\n")
	b.WriteString(shared.DimStyle.Render(strings.Repeat("─", width-4)))
	b.WriteString("\n\n")

	// Tab content. Track the screen row at which the content begins so
	// renderExecuteTab can register click rows in absolute screen coords.
	contentStartRow := strings.Count(b.String(), "\n")
	switch r.activeTab {
	case TabExecute:
		b.WriteString(r.renderExecuteTab(contentStartRow))
	case TabParams:
		b.WriteString(r.renderParamsTab(contentStartRow))
	case TabBody:
		b.WriteString(r.renderBodyTab(contentStartRow))
	case TabHeaders:
		b.WriteString(r.renderHeadersTab(contentStartRow))
	case TabScripts:
		b.WriteString(r.renderScriptsTab(contentStartRow))
	}

	return lipgloss.NewStyle().Width(width).Render(b.String())
}

// renderExecuteTab renders the execute tab with params and execute button.
// startScreenRow is the screen-relative row where the tab content begins —
// used to register click targets for each row.
func (r *RequestBuilder) renderExecuteTab(startScreenRow int) string {
	var b strings.Builder

	row := 0

	// Path parameters section
	if len(r.pathParams) > 0 {
		b.WriteString(shared.DimStyle.Render("PATH PARAMETERS"))
		b.WriteString("\n")

		for _, p := range r.pathParams {
			r.clickMap.AddRow(startScreenRow+strings.Count(b.String(), "\n"), row)
			b.WriteString(r.renderParamRow(p, row, true))
			b.WriteString("\n")
			row++
		}
		b.WriteString("\n")
	}

	// Hint pointing users at the Params tab — only shown when the endpoint
	// has query parameters, so a path-only request doesn't get distracting
	// "go look elsewhere" text.
	if len(r.queryParams) > 0 {
		b.WriteString(shared.DimStyle.Render(
			fmt.Sprintf("QUERY PARAMETERS  (%d declared; edit on the Params tab)", len(r.queryParams))))
		b.WriteString("\n\n")
	}

	// Execute button
	executeRow := r.executeRowIndex()
	r.clickMap.AddRow(startScreenRow+strings.Count(b.String(), "\n"), executeRow)
	isSelected := r.cursor == executeRow && r.activeTab == TabExecute

	// Check for missing required params
	hasMissing := false
	for _, p := range r.pathParams {
		if p.Required && r.values[p.Name] == "" {
			hasMissing = true
			break
		}
	}

	buttonStyle := lipgloss.NewStyle().
		Padding(0, 2).
		Bold(true)

	if hasMissing {
		buttonStyle = buttonStyle.
			Foreground(shared.ColorMuted).
			Background(lipgloss.Color("#374151"))
	} else if isSelected {
		buttonStyle = buttonStyle.
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(shared.ColorSuccess)
	} else {
		buttonStyle = buttonStyle.
			Foreground(shared.ColorSuccess).
			Background(lipgloss.Color("#1F2937"))
	}

	cursor := "  "
	if isSelected {
		cursor = shared.CursorStyle.Render("> ")
	}

	buttonText := "Execute Request"
	if hasMissing {
		buttonText = "Fill required params"
	}

	b.WriteString(fmt.Sprintf("%s%s", cursor, buttonStyle.Render(buttonText)))
	if r.executing {
		spinnerStr := lipgloss.NewStyle().Foreground(shared.ColorPrimary).Render(r.spinner.View())
		b.WriteString(" ")
		b.WriteString(spinnerStr)
	}

	return b.String()
}

// renderParamRow renders a single parameter row with nicer formatting
func (r *RequestBuilder) renderParamRow(p openapi.Parameter, row int, isPath bool) string {
	isSelected := row == r.cursor && r.activeTab == TabExecute

	// Status indicator
	value := r.values[p.Name]
	var statusIndicator string
	if value == "" {
		if p.Required {
			statusIndicator = shared.WarningStyle.Render("○") // Empty required
		} else {
			statusIndicator = shared.DimStyle.Render("○") // Empty optional
		}
	} else if isPath && r.fromContext[p.Name] {
		statusIndicator = shared.SuccessStyle.Render("●") // From context
	} else {
		statusIndicator = lipgloss.NewStyle().Foreground(lipgloss.Color("#E5E7EB")).Render("●") // Manually set
	}

	// Cursor
	cursor := "  "
	if isSelected {
		cursor = shared.CursorStyle.Render("> ")
	}

	// Name styling
	nameStyle := lipgloss.NewStyle().Width(18)
	if isSelected {
		nameStyle = nameStyle.Foreground(shared.ColorPrimary).Bold(true)
	} else {
		nameStyle = nameStyle.Foreground(lipgloss.Color("#9CA3AF"))
	}

	// Required indicator
	reqMark := ""
	if p.Required {
		reqMark = shared.WarningStyle.Render("*")
	}

	// Value styling
	var valueDisplay string
	if r.editing && isSelected {
		valueDisplay = r.input.View()
	} else if value == "" {
		if p.Required {
			valueDisplay = shared.WarningStyle.Render("(required)")
		} else {
			valueDisplay = shared.DimStyle.Render("(optional)")
		}
	} else {
		// Truncate so the row stays one visual line — wrapped rows break
		// click hit-testing.
		room := r.width - 2 /* cursor */ - 2 /* status */ - 18 /* name width */ - lipgloss.Width(reqMark) - 4 /* spaces */
		display := value
		if room > 0 {
			display = shared.TruncateWithEllipsis(value, room)
		}
		valueStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#E5E7EB"))
		if isPath && r.fromContext[p.Name] {
			valueStyle = valueStyle.Foreground(shared.ColorSuccess)
		}
		valueDisplay = valueStyle.Render(display)
	}

	return fmt.Sprintf("%s%s %s%s  %s", cursor, statusIndicator, nameStyle.Render(p.Name), reqMark, valueDisplay)
}

// renderScriptsTab hosts inline editors for the request-scoped pre and
// post-request JavaScript hooks. ctrl+p toggles between phases, ctrl+s
// saves into the operation's overlay override (alongside body/params),
// ctrl+v hands the current buffer to $EDITOR. Profile- and tag-scope
// scripts live in the global Scripts modal (J).
func (r *RequestBuilder) renderScriptsTab(startScreenRow int) string {
	var b strings.Builder

	// Phase toggle row: visually styled like nested mini-tabs. Phase
	// switching is keyboard-only (ctrl+p) so labels stay plain — no
	// digit prefixes that would clash with the outer 1/2/3 tab shortcuts.
	preLabel := "Pre"
	postLabel := "Post"
	if r.scriptPhase == ScriptPhasePre {
		b.WriteString(shared.ActiveTabStyle.Render(preLabel))
		b.WriteString("  ")
		b.WriteString(shared.InactiveTabStyle.Render(postLabel))
	} else {
		b.WriteString(shared.InactiveTabStyle.Render(preLabel))
		b.WriteString("  ")
		b.WriteString(shared.ActiveTabStyle.Render(postLabel))
	}

	// A small hint on the same line that profile/tag scripts also fire
	// for this endpoint — guides users to the modal when they want to edit
	// those, without making the inline tab busy.
	if r.hasOtherScopeScripts() {
		b.WriteString("  ")
		b.WriteString(shared.DimStyle.Render(
			"profile/tag scripts also run; press [J] for the full editor"))
	}
	b.WriteString("\n")
	b.WriteString(shared.DimStyle.Render(strings.Repeat("─", r.width-2)))
	b.WriteString("\n")

	// Active editor fills the rest, leaving one row for the editor's
	// built-in footer (shortcuts + language + scroll %).
	editorStartRow := startScreenRow + strings.Count(b.String(), "\n")
	editorHeight := r.height - editorStartRow - 1
	if editorHeight < 3 {
		editorHeight = 3
	}

	ed := r.activeScriptEditor()
	ed.SetSize(r.width-2, editorHeight)
	ed.SetMouseOrigin(0, editorStartRow)
	b.WriteString(ed.View())
	b.WriteString("\n")
	b.WriteString(ed.Footer(
		[]shared.Hint{
			{Key: "ctrl+s", Label: "save"},
			{Key: "ctrl+p", Label: "pre/post"},
			{Key: "ctrl+v", Label: "external editor"},
		},
		r.width-2,
	))

	return b.String()
}

// hasOtherScopeScripts reports whether any profile- or tag-scope scripts
// (the ones edited in the global Scripts modal) are configured for this
// endpoint. Used to surface a hint on the inline Scripts tab.
func (r *RequestBuilder) hasOtherScopeScripts() bool {
	if r.overlay == nil {
		return false
	}
	if !r.overlay.Scripts.Profile.IsEmpty() {
		return true
	}
	for _, tag := range r.endpoint.Tags {
		if t, ok := r.overlay.Scripts.Tags[tag]; ok && !t.IsEmpty() {
			return true
		}
	}
	return false
}

// renderParamsTab hosts the query-parameter editor. Spec-defined params
// from the endpoint are pre-populated as rows so users can fill values
// without losing track of what the endpoint expects; new rows can be
// added for ad-hoc params, which ride through to the final URL via
// http.RequestBuilder.SetExtraQueryParams at send time.
func (r *RequestBuilder) renderParamsTab(startScreenRow int) string {
	var b strings.Builder

	used := strings.Count(b.String(), "\n")
	editorHeight := r.height - (startScreenRow + used) - 2
	if editorHeight < 4 {
		editorHeight = 4
	}
	r.paramsEditor.SetSize(r.width-2, editorHeight)
	b.WriteString(r.paramsEditor.View())
	b.WriteString("\n")
	b.WriteString(shared.DimStyle.Render("[ctrl+s] save  "))
	b.WriteString(r.paramsEditor.Hint())
	return b.String()
}

// renderHeadersTab hosts the declarative request-level header editor.
// Values are stored verbatim with any ${env_var} references intact and
// resolved against the live context at send time, so a single Authorization
// row like `Bearer ${apiToken}` adapts to whichever environment is active.
func (r *RequestBuilder) renderHeadersTab(startScreenRow int) string {
	var b strings.Builder

	used := strings.Count(b.String(), "\n")
	editorHeight := r.height - (startScreenRow + used) - 2
	if editorHeight < 4 {
		editorHeight = 4
	}
	r.headersEditor.SetSize(r.width-2, editorHeight)
	b.WriteString(r.headersEditor.View())
	b.WriteString("\n")
	b.WriteString(shared.DimStyle.Render("[ctrl+s] save  "))
	b.WriteString(r.headersEditor.Hint())
	return b.String()
}

// renderBodyTab renders the request body tab. The body is edited inline via
// a textarea; `ctrl+e` optionally hands off to an external editor.
func (r *RequestBuilder) renderBodyTab(startScreenRow int) string {
	var b strings.Builder

	if r.endpoint.RequestBody == nil {
		b.WriteString(shared.DimStyle.Render("No request body for this endpoint"))
		return b.String()
	}

	// Title row stays minimal; the editor renders its own footer with
	// shortcuts (ctrl+v / ctrl+w / ctrl+l), search status, language, and
	// scroll %.
	b.WriteString(shared.DimStyle.Render("REQUEST BODY"))
	b.WriteString("\n")

	// Reserve the last row for the editor's footer.
	editorHeight := r.height - startScreenRow - strings.Count(b.String(), "\n") - 1
	if editorHeight < 3 {
		editorHeight = 3
	}
	r.bodyEditor.SetSize(r.width-2, editorHeight)

	// Register click rows so clicks land in the editor and focus it.
	editorStartRow := startScreenRow + strings.Count(b.String(), "\n")
	for i := 0; i < editorHeight; i++ {
		r.clickMap.AddRow(editorStartRow+i, 0)
	}
	// Tell the editor where it sits so scrollbar press/drag/release
	// translates to its local coordinate system.
	r.bodyEditor.SetMouseOrigin(0, editorStartRow)
	b.WriteString(r.bodyEditor.View())
	b.WriteString("\n")
	b.WriteString(r.bodyEditor.Footer(
		[]shared.Hint{{Key: "ctrl+s", Label: "save"}},
		r.width-2,
	))

	return b.String()
}
