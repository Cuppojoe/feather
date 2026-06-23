package tui

import (
	"context"
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/cuppojoe/feather/internal/config"
	"github.com/cuppojoe/feather/internal/http"
	"github.com/cuppojoe/feather/internal/models"
	"github.com/cuppojoe/feather/internal/openapi"
	"github.com/cuppojoe/feather/internal/overlay"
	"github.com/cuppojoe/feather/internal/scripting"
	"github.com/cuppojoe/feather/internal/tui/panels"
	"github.com/cuppojoe/feather/internal/tui/shared"
)

// App is the main application model with multi-panel layout
type App struct {
	// Panel components
	mainViewport *panels.MainViewport
	requestPanel *panels.RequestPanel
	historyPanel *panels.HistoryPanel
	profilePanel *panels.ProfilePanel
	opEditor     *panels.OpEditorPanel
	categoryForm *panels.CategoryFormPanel
	scriptsPanel *panels.ScriptsPanel
	helpPanel    *panels.HelpPanel
	confirm      *panels.ConfirmPanel
	envPanel     *panels.EnvironmentPanel

	// Focus management
	focusedPanel string

	// Core state
	baseSpec        *openapi.ParsedSpec // pristine, pre-overlay
	spec            *openapi.ParsedSpec // effective (base + overlay)
	config          *config.Config
	overlay         *overlay.Overlay
	appCtx          *models.Context
	client          *http.Client
	keys            shared.KeyMap
	help            help.Model
	switchToProfile string // set when user picks a different profile

	// UI state
	width          int
	height         int
	err            error
	errFull        string // Full error message with debug info
	statusMsg      string
	showErrorModal bool

	// Pending destructive actions awaiting confirmation.
	pendingDeleteOp  *openapi.Endpoint
	pendingDeleteCat string

	// Layout dimensions (computed)
	headerHeight  int
	mainHeight    int
	requestHeight int
}

// NewApp creates a new application with multi-panel layout
func NewApp(base *openapi.ParsedSpec, cfg *config.Config, ov *overlay.Overlay) *App {
	if ov == nil {
		ov = overlay.New()
	}
	// The effective spec the UI navigates is the base with the overlay applied.
	spec := overlay.Apply(base, ov)
	ctx := models.NewContext()

	// Load the active environment's values into the runtime context.
	// Environments are the only source of variables — when one is set,
	// its Values feed every templated header / base URL / script
	// reference, and ${other} cross-references inside values are
	// resolved up front. Cycles surface via ctxErr so the user can fix
	// them without the app starting in a wedged state.
	var raw map[string]string
	if cfg.ActiveEnvironment != "" {
		if env, err := config.LoadEnvironment(cfg.ActiveEnvironment); err == nil {
			raw = env.PlainValues()
		}
	}
	var ctxErr error
	var ctxErrFull string
	if resolved, err := models.Resolve(raw); err == nil {
		for k, v := range resolved {
			ctx.Set(k, v)
		}
	} else {
		ctxErr = err
		// Stash the full text too so the error-details modal (D) has
		// something to show — it only opens when errFull is set.
		ctxErrFull = err.Error()
		for k, v := range raw {
			ctx.Set(k, v)
		}
	}

	// Set base URL, substituting context values into it (e.g. so a
	// profile can say baseURL: "https://${host}/v1" and have it resolve).
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = spec.BaseURL
	}
	baseURL = models.Substitute(baseURL, ctx.Values)
	ctx.BaseURL = baseURL

	client := http.NewClient(baseURL)
	keys := shared.DefaultKeyMap()

	// Create panels
	historyPanel := panels.NewHistoryPanel(keys)
	requestPanel := panels.NewRequestPanel(keys)
	mainViewport := panels.NewMainViewport(spec, ctx, keys, ov)
	profilePanel := panels.NewProfilePanel(cfg.Name, keys)
	opEditor := panels.NewOpEditorPanel(keys)
	categoryForm := panels.NewCategoryFormPanel()
	confirm := panels.NewConfirmPanel()
	scriptsPanel := panels.NewScriptsPanel(keys)
	helpPanel := panels.NewHelpPanel()
	envPanel := panels.NewEnvironmentPanel(keys)

	//Requet and history reference each other
	historyPanel.LinkRequestPanel(requestPanel)
	requestPanel.LinkHistoryPanel(historyPanel)

	// Set initial focus
	mainViewport.SetFocused(true)

	app := &App{
		err:          ctxErr,
		errFull:      ctxErrFull,
		mainViewport: mainViewport,
		requestPanel: requestPanel,
		historyPanel: historyPanel,
		profilePanel: profilePanel,
		opEditor:     opEditor,
		categoryForm: categoryForm,
		confirm:      confirm,
		scriptsPanel: scriptsPanel,
		helpPanel:    helpPanel,
		envPanel:     envPanel,
		focusedPanel: "main",
		baseSpec:     base,
		spec:         spec,
		config:       cfg,
		overlay:      ov,
		appCtx:       ctx,
		client:       client,
		keys:         keys,
		help:         help.New(),
	}

	return app
}

// Init implements tea.Model
func (a *App) Init() tea.Cmd {
	// EnableBracketedPaste makes the terminal wrap pasted content in
	// known markers, so bubbletea can deliver the whole paste as a single
	// KeyMsg (Paste=true) instead of streaming each character — including
	// embedded newlines — through as separate key events. Without it,
	// pasting multi-line code into an editor loses line breaks on
	// terminals that send LF for newlines.
	return tea.Batch(tea.EnterAltScreen, tea.EnableBracketedPaste)
}

// Update implements tea.Model
func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width = msg.Width
		a.height = msg.Height
		a.help.Width = msg.Width
		a.calculateSizes()

		// Forward to request panel for viewport resize
		if a.requestPanel.IsVisible() {
			a.requestPanel.Update(msg)
		}
		return a, nil

	case tea.MouseMsg:
		// If error modal is open, capture clicks to close it
		if a.showErrorModal {
			if msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionRelease {
				a.showErrorModal = false
			}
			return a, nil
		}

		// If profile modal is open, swallow mouse events.
		if a.profilePanel.IsExpanded() {
			return a, nil
		}

		// Authoring modals capture all mouse events while open.
		if a.opEditor.IsExpanded() || a.categoryForm.IsExpanded() || a.confirm.IsExpanded() {
			return a, nil
		}
		if a.scriptsPanel.IsExpanded() {
			// Forward translated mouse to the script editor so click +
			// drag selection, scrollbar drag, and wheel scroll all work
			// inside the modal — same pattern as the help modal below.
			return a.updateScriptsModal(msg)
		}
		if a.envPanel.IsExpanded() {
			return a.updateEnvironmentModal(msg)
		}
		// Help modal absorbs mouse so the body can scroll / be clicked.
		if a.helpPanel.IsExpanded() {
			return a.updateHelpModal(msg)
		}

		if msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionRelease {
			a.handleMouseFocus(msg)
		}
		// Translate mouse coordinates to panel-relative and forward
		return a.updateFocusedPanelWithMouse(msg)

	case tea.KeyMsg:
		// If error modal is open, any key closes it
		if a.showErrorModal {
			a.showErrorModal = false
			return a, nil
		}

		// If profile modal is open, capture ALL keyboard input
		if a.profilePanel.IsExpanded() {
			return a.updateProfileModal(msg)
		}

		// Authoring modals capture ALL keyboard input while open.
		if a.opEditor.IsExpanded() {
			return a.updateOpEditorModal(msg)
		}
		if a.categoryForm.IsExpanded() {
			return a.updateCategoryFormModal(msg)
		}
		if a.confirm.IsExpanded() {
			return a.updateConfirmModal(msg)
		}
		if a.scriptsPanel.IsExpanded() {
			return a.updateScriptsModal(msg)
		}
		if a.envPanel.IsExpanded() {
			return a.updateEnvironmentModal(msg)
		}
		// Help modal opens on top of everything else.
		if a.helpPanel.IsExpanded() {
			return a.updateHelpModal(msg)
		}

		// If the request panel is capturing a search pattern, route ALL keys to
		// it so pattern characters (e.g. h/r/x/n) aren't stolen by the global
		// single-key bindings below.
		if a.focusedPanel == "request" && a.requestPanel.IsSearching() {
			return a.updateFocusedPanel(msg)
		}

		// If the main viewport is capturing input (editing a field, or its
		// body pager is mid-search), route ALL keys to it so typed
		// characters aren't stolen by the global single-key bindings.
		if a.focusedPanel == "main" && a.mainViewport.IsCapturingInput() {
			return a.updateFocusedPanel(msg)
		}

		// Global keybindings (only when not editing)
		switch {
		case key.Matches(msg, a.keys.Quit):
			return a, tea.Quit

		case key.Matches(msg, a.keys.Tab):
			a.cycleFocus(1)
			return a, nil

		case key.Matches(msg, a.keys.ShiftTab):
			a.cycleFocus(-1)
			return a, nil

		case key.Matches(msg, a.keys.Profile):
			// Open profile switcher modal
			a.profilePanel.Toggle()
			a.profilePanel.SetFocused(true)
			return a, nil

		case key.Matches(msg, a.keys.Scripts):
			// Open the Scripts (JS hooks) modal. Profile scope is always
			// available; tag/op scopes light up when there's a current
			// endpoint.
			return a, a.scriptsPanel.Open(a.overlay, a.config.Name, a.mainViewport.CurrentEndpoint())

		case key.Matches(msg, a.keys.Help):
			// Global Help modal. Opens with the section list; pick a topic
			// to read.
			a.helpPanel.Toggle()
			return a, nil

		case key.Matches(msg, a.keys.EnvList):
			// Open the Environments modal — swappable named contexts that
			// overlay the profile's Context (Postman-style environments).
			a.envPanel.Open(a.config.ActiveEnvironment)
			return a, nil

		case key.Matches(msg, a.keys.ErrorDetails) && a.err != nil && a.errFull != "":
			// Show error details modal
			a.showErrorModal = true
			return a, nil

		case key.Matches(msg, a.keys.History):
			// Toggle history panel
			if a.historyPanel.IsVisible() {
				a.historyPanel.Close()
				if !a.requestPanel.IsVisible() {
					a.setFocus("main")
				}
			} else {
				a.historyPanel.SetVisible(true)
				a.setFocus("history")
			}
			a.calculateSizes()
			return a, nil

		case key.Matches(msg, a.keys.RequestPanel):
			// Toggle request panel
			if a.requestPanel.IsVisible() {
				a.requestPanel.Close()
				if !a.historyPanel.IsVisible() {
					a.setFocus("main")
				}
			} else {
				a.requestPanel.SetVisible(true)
				a.setFocus("request")
			}
			a.calculateSizes()
			return a, nil

		case key.Matches(msg, a.keys.CloseSidePanels) && (a.requestPanel.IsVisible() || a.historyPanel.IsVisible()):
			// Close both panels
			a.requestPanel.Close()
			a.historyPanel.Close()
			a.setFocus("main")
			a.calculateSizes()
			return a, nil

		case key.Matches(msg, a.keys.Back):
			// If main viewport is editing, forward to panel to handle escape
			if a.focusedPanel == "main" && a.mainViewport.IsEditing() {
				return a.updateFocusedPanel(msg)
			}
			// Let the request panel handle escape itself so it can clear an
			// active search before closing.
			if a.focusedPanel == "request" {
				return a.updateFocusedPanel(msg)
			}
			return a.handleBack()
		}

		// Forward to focused panel
		return a.updateFocusedPanel(msg)

	case responseMsg:
		a.mainViewport.RequestComplete()
		a.requestPanel.SetResponse(msg.response, msg.endpoint)
		a.requestPanel.SetScriptOutput(msg.logs, msg.scriptErr)
		a.requestPanel.SetVisible(true)
		a.setFocus("request")
		a.calculateSizes()
		return a, nil

	case errMsg:
		a.mainViewport.RequestComplete()
		a.requestPanel.SetScriptOutput(msg.logs, nil)
		a.err = msg.err
		a.errFull = msg.err.Error()
		return a, nil

	case statusMsg:
		a.statusMsg = string(msg)
		return a, nil

	case shared.EditExternalMsg:
		// An external editor (vim, etc.) just finished. Two things need fixing
		// up on resume:
		//   1. bubbletea doesn't restore mouse capture on its own.
		//   2. ExecProcess resets the rendered-line count but NOT the renderer's
		//      lastRender cache, and (in alt-screen mode) re-entering the alt
		//      screen is a no-op. Since our model is unchanged, the next frame
		//      is byte-identical to lastRender, so the renderer writes nothing
		//      and the editor's leftover (un-highlighted) screen stays visible.
		//      tea.ClearScreen forces a full repaint, restoring the view.
		var cmd tea.Cmd
		switch {
		case a.scriptsPanel.IsExpanded():
			// Scripts modal opened $EDITOR via ctrl+v — route the result
			// to it directly. updateFocusedPanel would otherwise dispatch
			// to whichever panel currently has focus, never reaching the
			// modal.
			_, cmd = a.updateScriptsModal(msg)
		default:
			_, cmd = a.updateFocusedPanel(msg)
		}
		return a, tea.Batch(cmd, tea.EnableMouseCellMotion, tea.ClearScreen)
	}

	// Forward unhandled messages (e.g. spinner.TickMsg) to the focused panel
	// so animated components nested deep in the tree keep receiving updates.
	return a.updateFocusedPanel(msg)
}

// Modal frame: outer Border(1) + Padding(1, 2). Click handlers inside the
// modal expect content-relative coordinates, so we subtract these offsets
// before forwarding mouse events.
const (
	modalTopFrame  = 2 // 1 border + 1 padding
	modalLeftFrame = 3 // 1 border + 2 padding
)

// translateModalMouse converts a screen-relative mouse event to coordinates
// relative to the modal's content area (the inside of its border+padding).
func translateModalMouse(msg tea.Msg, modalX, modalY int) tea.Msg {
	mm, ok := msg.(tea.MouseMsg)
	if !ok {
		return msg
	}
	mm.X -= modalX + modalLeftFrame
	mm.Y -= modalY + modalTopFrame
	return mm
}

// SwitchToProfile returns a non-empty profile name if the user asked to
// switch profiles. main() inspects this after Run() to decide whether to
// restart the app with a different profile.
func (a *App) SwitchToProfile() string { return a.switchToProfile }

// updateProfileModal handles input when the profile switcher modal is open.
// The panel handles esc internally (e.g. back-from-details), so we just
// forward everything and watch for a Switch signal.
func (a *App) updateProfileModal(msg tea.Msg) (*App, tea.Cmd) {
	mw := min(92, a.width-8)
	mh := min(24, a.height-8)
	msg = translateModalMouse(msg, (a.width-mw)/2, (a.height-mh)/2)
	result := a.profilePanel.Update(msg)
	if result.Switch {
		a.switchToProfile = result.ProfileName
		return a, tea.Quit
	}
	return a, result.Cmd
}

// updateHelpModal forwards translated input to the global help reader.
func (a *App) updateHelpModal(msg tea.Msg) (*App, tea.Cmd) {
	mw := min(96, a.width-8)
	mh := min(30, a.height-6)
	msg = translateModalMouse(msg, (a.width-mw)/2, (a.height-mh)/2)
	result := a.helpPanel.Update(msg)
	return a, result.Cmd
}

// updateScriptsModal handles input for the JS hooks editor. On Save the
// in-memory buffers are already flushed into the overlay; we just persist
// it to disk and surface a one-line status.
func (a *App) updateScriptsModal(msg tea.Msg) (*App, tea.Cmd) {
	mw := min(100, a.width-8)
	mh := min(30, a.height-6)
	msg = translateModalMouse(msg, (a.width-mw)/2, (a.height-mh)/2)
	result := a.scriptsPanel.Update(msg)
	if result.Save {
		if err := a.persistOverlay(); err != nil {
			a.err = fmt.Errorf("saving scripts: %w", err)
			a.errFull = a.err.Error()
		} else {
			a.statusMsg = "scripts saved"
		}
	}
	return a, result.Cmd
}

// updateEnvironmentModal forwards translated input to the Environments
// modal and reacts to its result intents (switch active env, persist a
// just-saved env's values).
func (a *App) updateEnvironmentModal(msg tea.Msg) (*App, tea.Cmd) {
	mw := min(92, a.width-8)
	mh := min(26, a.height-6)
	msg = translateModalMouse(msg, (a.width-mw)/2, (a.height-mh)/2)
	result := a.envPanel.Update(msg)
	if result.SetActive {
		name := result.ActiveName
		if result.ActiveCleared {
			name = ""
		}
		a.applyActiveEnvironment(name)
	}
	if result.Saved && result.SavedEnv != nil {
		// If the just-saved env is the active one, re-merge so the new
		// values land in the live context immediately.
		if result.SavedEnv.Name == a.config.ActiveEnvironment {
			a.applyActiveEnvironment(result.SavedEnv.Name)
		}
	}
	return a, result.Cmd
}

// applyActiveEnvironment switches the active environment by name (empty
// means "no env"), updates the profile's stored ActiveEnvironment field,
// persists the profile, and rebuilds the live context. On any failure the
// error lands in the status bar but the app keeps running.
func (a *App) applyActiveEnvironment(name string) {
	a.config.ActiveEnvironment = name
	if err := a.config.SaveDefault(); err != nil {
		a.err = fmt.Errorf("saving active environment: %w", err)
		a.errFull = a.err.Error()
	}

	var raw map[string]string
	if name != "" {
		env, err := config.LoadEnvironment(name)
		if err != nil {
			a.err = fmt.Errorf("loading environment %q: %w", name, err)
			a.errFull = a.err.Error()
			return
		}
		raw = env.PlainValues()
	}

	// Wipe + repopulate so values removed in the new env disappear.
	for k := range a.appCtx.Values {
		delete(a.appCtx.Values, k)
	}
	if resolved, err := models.Resolve(raw); err == nil {
		// The new environment resolves cleanly — clear any stale resolve
		// error (e.g. a cycle the user just fixed) so it stops lingering.
		a.err = nil
		a.errFull = ""
		for k, v := range resolved {
			a.appCtx.Set(k, v)
		}
	} else {
		a.err = err
		// errFull lets the error-details modal (D) open on the cycle report.
		a.errFull = err.Error()
		for k, v := range raw {
			a.appCtx.Set(k, v)
		}
	}

	// Re-resolve BaseURL against the fresh context so templates like
	// "https://${host}/v1" pick up env changes.
	baseURL := a.config.BaseURL
	if baseURL == "" && a.spec != nil {
		baseURL = a.spec.BaseURL
	}
	a.appCtx.BaseURL = models.Substitute(baseURL, a.appCtx.Values)

	// Push the new values into whatever request the user has open so
	// path / query params reflect the change without an exit + re-enter.
	if a.mainViewport != nil {
		a.mainViewport.RefreshContext()
	}

	if name == "" {
		a.statusMsg = "environment cleared"
	} else {
		a.statusMsg = fmt.Sprintf("environment: %s", name)
	}
}

// updateOpEditorModal handles input while the request editor is open. On save
// it writes the overlay (added op or imported override) and rebuilds.
func (a *App) updateOpEditorModal(msg tea.Msg) (*App, tea.Cmd) {
	result := a.opEditor.Update(msg)
	if !result.Save {
		return a, result.Cmd
	}
	if result.Created {
		op := result.Added
		// New op (no original key) vs edit of an existing created op.
		if result.OrigMethod == "" {
			if a.overlay.HasAdded(op.Method, op.Path) {
				a.statusMsg = "Operation already exists"
				return a, result.Cmd
			}
			a.overlay.AppendAdded(op)
			a.statusMsg = "Added request"
		} else {
			a.overlay.UpdateAdded(result.OrigMethod, result.OrigPath, op)
			a.statusMsg = "Updated request"
		}
		a.rebuild(op.Tag)
	} else {
		a.overlay.SetOverride(result.Method, result.Path, result.Override)
		a.statusMsg = "Updated request"
		a.rebuild(result.Override.Tag)
	}
	return a, result.Cmd
}

// updateCategoryFormModal handles the create/rename category form.
func (a *App) updateCategoryFormModal(msg tea.Msg) (*App, tea.Cmd) {
	result := a.categoryForm.Update(msg)
	if !result.Save {
		return a, result.Cmd
	}
	if result.Rename {
		a.renameCategory(result.OrigName, result.Name)
	} else {
		a.overlay.AddCategory(result.Name)
		a.statusMsg = "Added category"
		a.rebuild(result.Name)
	}
	return a, result.Cmd
}

// updateConfirmModal handles the reusable yes/no modal.
func (a *App) updateConfirmModal(msg tea.Msg) (*App, tea.Cmd) {
	result := a.confirm.Update(msg)
	if result.Action == "" {
		return a, nil
	}
	if !result.Confirmed {
		a.pendingDeleteOp = nil
		a.pendingDeleteCat = ""
		return a, nil
	}
	switch result.Action {
	case "delete-request":
		if ep := a.pendingDeleteOp; ep != nil {
			tag := tagOf(ep)
			if a.overlay.HasAdded(ep.Method, ep.Path) {
				a.overlay.RemoveAdded(ep.Method, ep.Path)
			} else {
				a.overlay.RemoveOverride(ep.Method, ep.Path)
			}
			a.statusMsg = "Deleted request"
			a.pendingDeleteOp = nil
			a.rebuild(tag)
		}
	case "delete-category":
		if name := a.pendingDeleteCat; name != "" {
			a.overlay.RemoveCategory(name)
			a.statusMsg = "Deleted category"
			a.pendingDeleteCat = ""
			a.rebuild("")
		}
	}
	return a, nil
}

// renameCategory renames a category: it retags overlay-added ops (via the
// overlay) and sets a Tag override on every imported op currently under the old
// category so the whole tree moves.
func (a *App) renameCategory(oldName, newName string) {
	if oldName == "" || newName == "" || oldName == newName {
		return
	}
	a.overlay.RenameCategory(oldName, newName)
	for _, tg := range a.baseSpec.Tags {
		if tg.Name != oldName {
			continue
		}
		for _, ep := range tg.Endpoints {
			ovr := overlay.OpOverride{}
			if existing := a.overlay.Get(ep.Method, ep.Path); existing != nil {
				ovr = *existing
			}
			ovr.Tag = newName
			a.overlay.SetOverride(ep.Method, ep.Path, ovr)
		}
	}
	a.statusMsg = "Renamed category"
	a.rebuild(newName)
}

// tagOf returns an endpoint's first tag (its category), or "".
func tagOf(ep *openapi.Endpoint) string {
	if ep != nil && len(ep.Tags) > 0 {
		return ep.Tags[0]
	}
	return ""
}

// handleMouseFocus determines which panel was clicked and focuses it
func (a *App) handleMouseFocus(msg tea.MouseMsg) {
	// Account for header
	y := msg.Y - a.headerHeight

	if y < 0 {
		// Clicked on header, ignore
		return
	}

	if y < a.mainHeight {
		a.setFocus("main")
		return
	}

	// Bottom panels area - check X coordinate for left/right split
	if a.historyPanel.IsVisible() || a.requestPanel.IsVisible() {
		midX := a.width / 2
		if a.historyPanel.IsVisible() && msg.X < midX {
			a.setFocus("history")
		} else if a.requestPanel.IsVisible() {
			a.setFocus("request")
		}
	}
}

// handleBack handles back navigation
func (a *App) handleBack() (*App, tea.Cmd) {
	switch a.focusedPanel {
	case "request":
		a.requestPanel.Close()
		a.setFocus("main")
		a.calculateSizes()
	case "main":
		if !a.mainViewport.NavigateBack() {
			// At top level, do nothing
		}
	}
	return a, nil
}

// saveOverlayOverride records an operation override in the overlay and writes
// it to disk, surfacing the outcome in the status bar.
func (a *App) saveOverlayOverride(endpoint *openapi.Endpoint, ovr *overlay.OpOverride) {
	if endpoint == nil || ovr == nil {
		return
	}
	a.overlay.SetOverride(endpoint.Method, endpoint.Path, *ovr)
	if err := a.persistOverlay(); err != nil {
		a.err = err
	} else {
		a.statusMsg = "Saved to overlay"
	}
}

// saveTagScripts persists tag-scope pre/post scripts (edited inline on the
// endpoint list's Scripts tab) into the overlay and writes to disk.
func (a *App) saveTagScripts(tag string, s overlay.Scripts) {
	if tag == "" {
		return
	}
	a.overlay.SetTagScripts(tag, s)
	if err := a.persistOverlay(); err != nil {
		a.err = err
	} else {
		a.statusMsg = "Saved tag scripts"
	}
}

// saveProfileScripts persists profile-scope pre/post scripts (edited inline
// on the main menu's Scripts tab) into the overlay and writes to disk.
func (a *App) saveProfileScripts(s overlay.Scripts) {
	a.overlay.SetProfileScripts(s)
	if err := a.persistOverlay(); err != nil {
		a.err = err
	} else {
		a.statusMsg = "Saved profile scripts"
	}
}

// persistOverlay writes the current overlay to the active profile's file.
func (a *App) persistOverlay() error {
	path, err := config.OverlayPath(a.config.Name)
	if err != nil {
		return err
	}
	return a.overlay.Save(path)
}

// rebuild re-applies the overlay to the pristine base spec, persists the
// overlay, refreshes path variables, and reloads navigation. focusTag, when a
// non-empty category, reopens that category's endpoint list.
func (a *App) rebuild(focusTag string) {
	a.spec = overlay.Apply(a.baseSpec, a.overlay)
	if err := a.persistOverlay(); err != nil {
		a.err = err
	}
	a.mainViewport.RefreshSpec(a.spec, focusTag)
}

// handleAuthoringIntent acts on an authoring intent surfaced by the main
// viewport (create/edit/delete/duplicate request, manage categories). Returns
// true when it handled one.
func (a *App) handleAuthoringIntent(result panels.MainViewportResult) bool {
	switch {
	case result.OpenNewOp:
		a.opEditor.OpenCreate(result.NewOpTag)
	case result.EditOp != nil:
		ep := result.EditOp
		created := a.overlay.HasAdded(ep.Method, ep.Path)
		ovr := a.editorOverride(ep, created)
		a.opEditor.OpenEdit(ep, ovr, created)
	case result.DuplicateOp != nil:
		a.duplicateRequest(result.DuplicateOp)
	case result.DeleteOp != nil:
		a.requestDeleteRequest(result.DeleteOp)
	case result.NewCategory:
		a.categoryForm.OpenCreate()
	case result.RenameCategory != "":
		a.categoryForm.OpenRename(result.RenameCategory)
	case result.DeleteCategory != "":
		a.requestDeleteCategory(result.DeleteCategory)
	default:
		return false
	}
	return true
}

// editorOverride builds the OpOverride passed to the editor for prefill. For a
// created op it surfaces the added op's headers; for an imported op it returns
// the saved Operations override.
func (a *App) editorOverride(ep *openapi.Endpoint, created bool) *overlay.OpOverride {
	if created {
		if added, ok := a.overlay.AddedFor(ep.Method, ep.Path); ok && len(added.Headers) > 0 {
			return &overlay.OpOverride{Headers: added.Headers}
		}
		return nil
	}
	return a.overlay.Get(ep.Method, ep.Path)
}

// duplicateRequest clones an endpoint into the overlay as a new created request
// and opens the editor so the user can disambiguate its path.
func (a *App) duplicateRequest(ep *openapi.Endpoint) {
	op := overlay.AddedOp{
		Method:      ep.Method,
		Path:        ep.Path,
		Tag:         tagOf(ep),
		Summary:     ep.Summary,
		Description: ep.Description,
	}
	for _, prm := range ep.Parameters {
		op.Parameters = append(op.Parameters, overlay.AddedParam{Name: prm.Name, In: prm.In, Required: prm.Required})
	}
	if ovr := a.editorOverride(ep, a.overlay.HasAdded(ep.Method, ep.Path)); ovr != nil {
		op.Headers = ovr.Headers
	}
	a.overlay.AppendAdded(op)
	a.statusMsg = "Duplicated request; edit its path"
	a.rebuild(op.Tag)
	// Open the editor on the freshly added clone.
	clone := endpointForEditor(op)
	a.opEditor.OpenEdit(&clone, &overlay.OpOverride{Headers: op.Headers}, true)
}

// endpointForEditor builds a display endpoint from an added op (for the editor).
func endpointForEditor(op overlay.AddedOp) openapi.Endpoint {
	ep := openapi.Endpoint{Method: op.Method, Path: op.Path, Summary: op.Summary, Description: op.Description}
	if op.Tag != "" {
		ep.Tags = []string{op.Tag}
	}
	for _, p := range op.Parameters {
		ep.Parameters = append(ep.Parameters, openapi.Parameter{Name: p.Name, In: p.In, Required: p.Required})
	}
	return ep
}

// requestDeleteRequest asks to confirm deleting a request (or reverting an
// imported override). Skips when an imported op has nothing to revert.
func (a *App) requestDeleteRequest(ep *openapi.Endpoint) {
	created := a.overlay.HasAdded(ep.Method, ep.Path)
	if !created && a.overlay.Get(ep.Method, ep.Path) == nil {
		a.statusMsg = "No overlay changes to remove"
		return
	}
	a.pendingDeleteOp = ep
	verb := "Delete"
	if !created {
		verb = "Revert"
	}
	a.confirm.Open(fmt.Sprintf("%s %s %s?", verb, ep.Method, ep.Path), "delete-request", "")
}

// requestDeleteCategory deletes an empty category, or blocks a non-empty one.
func (a *App) requestDeleteCategory(name string) {
	for _, tg := range a.spec.Tags {
		if tg.Name == name && len(tg.Endpoints) > 0 {
			a.statusMsg = "Category not empty; move or delete its requests first"
			return
		}
	}
	a.pendingDeleteCat = name
	a.confirm.Open(fmt.Sprintf("Delete category %q?", name), "delete-category", "")
}

// updateFocusedPanel forwards updates to the focused panel
func (a *App) updateFocusedPanel(msg tea.Msg) (*App, tea.Cmd) {
	var cmd tea.Cmd

	switch a.focusedPanel {
	case "main":
		result := a.mainViewport.Update(msg)
		cmd = result.Cmd
		if result.ExecuteRequest {
			return a, tea.Batch(cmd, a.executeRequest(result.Request, result.Endpoint))
		}
		if result.SaveOverride {
			a.saveOverlayOverride(result.Endpoint, result.Override)
		}
		if result.SaveTagScripts {
			a.saveTagScripts(result.TagName, result.Scripts)
		}
		if result.SaveProfileScripts {
			a.saveProfileScripts(result.Scripts)
		}
		if a.handleAuthoringIntent(result) {
			return a, nil
		}

	case "request":
		result := a.requestPanel.Update(msg)
		cmd = result.Cmd
		if result.Close {
			a.requestPanel.Close()
			a.setFocus("main")
			a.calculateSizes()
		}

	case "history":
		result := a.historyPanel.Update(msg)
		cmd = result.Cmd
		if result.Close {
			a.historyPanel.Close()
			a.setFocus("main")
			a.calculateSizes()
		}
	}

	return a, cmd
}

// updateFocusedPanelWithMouse translates mouse coordinates to panel-relative and forwards
func (a *App) updateFocusedPanelWithMouse(msg tea.MouseMsg) (*App, tea.Cmd) {
	var cmd tea.Cmd

	// Calculate panel offsets and translate mouse coordinates
	// All Y coordinates need to account for header
	switch a.focusedPanel {
	case "main":
		// Main viewport is below header
		translated := tea.MouseMsg{
			X:      msg.X,
			Y:      msg.Y - a.headerHeight,
			Button: msg.Button,
			Action: msg.Action,
		}
		result := a.mainViewport.Update(translated)
		cmd = result.Cmd
		if result.ExecuteRequest {
			return a, tea.Batch(cmd, a.executeRequest(result.Request, result.Endpoint))
		}
		if result.SaveOverride {
			a.saveOverlayOverride(result.Endpoint, result.Override)
		}
		if result.SaveTagScripts {
			a.saveTagScripts(result.TagName, result.Scripts)
		}
		if result.SaveProfileScripts {
			a.saveProfileScripts(result.Scripts)
		}

	case "request":
		// Request panel sits to the right of history when both are visible,
		// otherwise alone at column 0.
		xOffset := 0
		if a.historyPanel.IsVisible() {
			xOffset = a.width / 2
		}
		translated := tea.MouseMsg{
			X:      msg.X - xOffset,
			Y:      msg.Y - a.headerHeight - a.mainHeight,
			Button: msg.Button,
			Action: msg.Action,
		}
		result := a.requestPanel.Update(translated)
		cmd = result.Cmd
		if result.Close {
			a.requestPanel.Close()
			a.setFocus("main")
			a.calculateSizes()
		}

	case "history":
		// History panel is at bottom left, Y offset is headerHeight + mainHeight
		translated := tea.MouseMsg{
			X:      msg.X,
			Y:      msg.Y - a.headerHeight - a.mainHeight,
			Button: msg.Button,
			Action: msg.Action,
		}
		result := a.historyPanel.Update(translated)
		cmd = result.Cmd
		if result.Close {
			a.historyPanel.Close()
			a.setFocus("main")
			a.calculateSizes()
		}
	}

	return a, cmd
}

// cycleFocus cycles focus between panels
func (a *App) cycleFocus(direction int) {
	// Define focusable panels in order (context is now modal-only)
	order := []string{"main"}
	if a.historyPanel.IsVisible() {
		order = append(order, "history")
	}
	if a.requestPanel.IsVisible() {
		order = append(order, "request")
	}

	// Find current position
	currentIdx := 0
	for i, id := range order {
		if id == a.focusedPanel {
			currentIdx = i
			break
		}
	}

	// Calculate new position
	newIdx := (currentIdx + direction + len(order)) % len(order)
	a.setFocus(order[newIdx])
}

// setFocus sets the focused panel
func (a *App) setFocus(id string) {
	// Unfocus all panels
	a.mainViewport.SetFocused(false)
	a.requestPanel.SetFocused(false)
	a.historyPanel.SetFocused(false)

	a.focusedPanel = id
	switch id {
	case "main":
		a.mainViewport.SetFocused(true)
	case "request":
		a.requestPanel.SetFocused(true)
	case "history":
		a.historyPanel.SetFocused(true)
	}
}

// calculateSizes computes panel dimensions
func (a *App) calculateSizes() {
	// Header is just the profile bar (1 line) when one is shown, otherwise 0.
	a.headerHeight = 0
	if a.config != nil && a.config.Name != "" {
		a.headerHeight = 1
	}

	remaining := a.height - a.headerHeight - 2 // -2 for status/help

	// Request panel at bottom (1/2 of remaining height when visible)
	a.requestHeight = 0
	if a.requestPanel.IsVisible() || a.historyPanel.IsVisible() {
		a.requestHeight = remaining / 2
	}

	// Main viewport fills the rest (no context panel in layout anymore)
	a.mainHeight = a.height - a.headerHeight - a.requestHeight - 2
	if a.mainHeight < 5 {
		a.mainHeight = 5
	}
}

// View implements tea.Model
func (a *App) View() string {
	if a.width == 0 {
		return "Loading..."
	}

	// Ensure sizes are calculated
	if a.mainHeight == 0 {
		a.calculateSizes()
	}

	// Profile info bar (rainbow "Feather" + active profile + URL + spec) sits
	// at the very top in place of the old ASCII art header.
	profileBar := a.buildProfileBar()

	// Main viewport (now full height minus header/footer)
	mainView := a.mainViewport.View(a.width, a.mainHeight)

	// Request panel (if visible)
	requestView := ""
	if a.requestPanel.IsVisible() {
		requestView = a.requestPanel.View(a.width, a.requestHeight)
	}

	historyView := ""
	if a.historyPanel.IsVisible() {
		historyView = a.historyPanel.View(a.width, a.requestHeight)
	}

	// Status bar
	statusBar := a.buildStatusBar()

	// Help
	helpView := a.help.View(a.keys)

	// Combine all into base view
	var parts []string
	if profileBar != "" {
		parts = append(parts, profileBar)
	}
	parts = append(parts, mainView)

	bottomParts := []string{}
	if historyView != "" {
		bottomParts = append(bottomParts, historyView)
	}
	if requestView != "" {
		bottomParts = append(bottomParts, requestView)
	}
	bottom := lipgloss.JoinHorizontal(lipgloss.Bottom, bottomParts...)
	if bottom != "" {
		parts = append(parts, bottom)
	}

	parts = append(parts, statusBar)
	parts = append(parts, helpView)

	baseView := lipgloss.JoinVertical(lipgloss.Left, parts...)

	// Build layers for modals
	var layers []*lipgloss.Layer
	layers = append(layers, lipgloss.NewLayer(baseView))

	// Profile switcher modal at Z=1
	if a.profilePanel.IsExpanded() {
		modalContent := a.profilePanel.ViewModal(a.width, a.height)
		modalWidth := min(92, a.width-8)
		modalHeight := min(24, a.height-8)
		modalX := (a.width - modalWidth) / 2
		modalY := (a.height - modalHeight) / 2
		layers = append(layers, lipgloss.NewLayer(modalContent).X(modalX).Y(modalY).Z(1))
	}

	// Request editor modal at Z=1
	if a.opEditor.IsExpanded() {
		modalContent := a.opEditor.ViewModal(a.width, a.height)
		modalWidth := min(76, a.width-8)
		modalHeight := min(24, a.height-8)
		modalX := (a.width - modalWidth) / 2
		modalY := (a.height - modalHeight) / 2
		layers = append(layers, lipgloss.NewLayer(modalContent).X(modalX).Y(modalY).Z(1))
	}

	// Scripts (JS hooks) modal at Z=1
	if a.scriptsPanel.IsExpanded() {
		modalContent := a.scriptsPanel.ViewModal(a.width, a.height)
		modalWidth := min(100, a.width-8)
		modalHeight := min(30, a.height-6)
		modalX := (a.width - modalWidth) / 2
		modalY := (a.height - modalHeight) / 2
		layers = append(layers, lipgloss.NewLayer(modalContent).X(modalX).Y(modalY).Z(1))
	}

	// Environments modal at Z=1
	if a.envPanel.IsExpanded() {
		modalContent := a.envPanel.ViewModal(a.width, a.height)
		modalWidth := min(92, a.width-8)
		modalHeight := min(26, a.height-6)
		modalX := (a.width - modalWidth) / 2
		modalY := (a.height - modalHeight) / 2
		layers = append(layers, lipgloss.NewLayer(modalContent).X(modalX).Y(modalY).Z(1))
	}

	// Help modal at Z=3 — sits above everything else so F1 can pop it
	// open while other modals are visible.
	if a.helpPanel.IsExpanded() {
		modalContent := a.helpPanel.ViewModal(a.width, a.height)
		modalWidth := min(96, a.width-8)
		modalHeight := min(30, a.height-6)
		modalX := (a.width - modalWidth) / 2
		modalY := (a.height - modalHeight) / 2
		layers = append(layers, lipgloss.NewLayer(modalContent).X(modalX).Y(modalY).Z(3))
	}

	// Category form modal at Z=2 (can open above the editor during duplicate).
	if a.categoryForm.IsExpanded() {
		modalContent := a.categoryForm.ViewModal(a.width, a.height)
		modalWidth := min(50, a.width-8)
		modalHeight := min(10, a.height-8)
		modalX := (a.width - modalWidth) / 2
		modalY := (a.height - modalHeight) / 2
		layers = append(layers, lipgloss.NewLayer(modalContent).X(modalX).Y(modalY).Z(2))
	}

	// Confirmation modal at Z=2.
	if a.confirm.IsExpanded() {
		modalContent := a.confirm.ViewModal(a.width, a.height)
		modalWidth := min(60, a.width-8)
		modalHeight := min(10, a.height-8)
		modalX := (a.width - modalWidth) / 2
		modalY := (a.height - modalHeight) / 2
		layers = append(layers, lipgloss.NewLayer(modalContent).X(modalX).Y(modalY).Z(2))
	}

	// Error modal at Z=2 (on top of other modals)
	if a.showErrorModal && a.errFull != "" {
		errorModal := a.buildErrorModal()
		modalWidth := min(90, a.width-8)
		modalHeight := min(30, a.height-8)
		modalX := (a.width - modalWidth) / 2
		modalY := (a.height - modalHeight) / 2
		layers = append(layers, lipgloss.NewLayer(errorModal).X(modalX).Y(modalY).Z(2))
	}

	// If we have modals, use compositor
	if len(layers) > 1 {
		comp := lipgloss.NewCompositor(layers...)
		return comp.Render()
	}

	return baseView
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// buildErrorModal creates the error detail modal
func (a *App) buildErrorModal() string {
	modalWidth := min(90, a.width-8)
	modalHeight := min(30, a.height-8)
	// Inner content area accounts for border (2) + padding (4).
	contentWidth := modalWidth - 6

	// Title style
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(shared.ColorError).
		Width(contentWidth).
		Align(lipgloss.Center)

	// Close hint style
	closeHintStyle := lipgloss.NewStyle().
		Foreground(shared.ColorMuted).
		Width(contentWidth).
		Align(lipgloss.Center)

	// Divider
	dividerStyle := lipgloss.NewStyle().
		Foreground(shared.ColorBorder).
		Width(contentWidth)

	// Content style with word wrap
	contentStyle := lipgloss.NewStyle().
		Width(contentWidth).
		Height(modalHeight - 6) // Account for title, hint, dividers

	// Modal container — Width/Height are the total rendered size (lipgloss v2
	// counts padding and border inside Width). contentWidth = modalWidth - 6
	// is the area inner elements must fit into.
	modalStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(shared.ColorError).
		Padding(1, 2).
		Width(modalWidth).
		Height(modalHeight)

	// Build content
	title := titleStyle.Render("Error Details")
	closeHint := closeHintStyle.Render("[press any key to close]")
	divider := dividerStyle.Render(strings.Repeat("─", contentWidth))

	// Word wrap the error content
	wrappedContent := lipgloss.NewStyle().
		Width(contentWidth).
		Render(a.errFull)

	content := lipgloss.JoinVertical(lipgloss.Left,
		title,
		closeHint,
		divider,
		contentStyle.Render(wrappedContent),
	)

	return modalStyle.Render(content)
}

// buildProfileBar renders a single-line band at the top of the TUI:
// rainbow "Feather" wordmark, the active profile name, then base URL and
// spec path. Replaces the old ASCII art header — the logo now lives only
// on the splash screen.
func (a *App) buildProfileBar() string {
	if a.config == nil || a.config.Name == "" {
		return ""
	}

	nameStyle := lipgloss.NewStyle().
		Foreground(shared.ColorPrimary).
		Bold(true)
	urlStyle := lipgloss.NewStyle().
		Foreground(shared.ColorSecondary)
	specStyle := shared.DimStyle
	sepStyle := lipgloss.NewStyle().Foreground(shared.ColorBorder)
	barStyle := lipgloss.NewStyle().
		Background(shared.ColorBackground).
		Width(a.width).
		MaxWidth(a.width).
		Padding(0, 1)

	sep := " " + sepStyle.Render("│") + " "

	// Rainbow "Feather" sits immediately to the left of the active profile,
	// which carries a "profile:" label so it reads the same way the
	// environment chip does.
	wordmark := shared.Rainbow("Feather")
	segments := []string{
		wordmark + " " + nameStyle.Render("profile: "+a.config.Name),
	}

	// Active environment chip — only shown when one is set so profiles
	// without environments don't pay for the extra noise.
	if a.config.ActiveEnvironment != "" {
		envStyle := lipgloss.NewStyle().
			Foreground(shared.ColorSuccess).
			Bold(true)
		segments = append(segments, envStyle.Render("env: "+a.config.ActiveEnvironment))
	}

	url := a.appCtx.BaseURL
	if url == "" {
		url = a.config.BaseURL
	}
	if url == "" && a.spec != nil {
		url = a.spec.BaseURL
	}
	if url != "" {
		segments = append(segments, urlStyle.Render(url))
	}

	if a.config.SpecPath != "" {
		segments = append(segments, specStyle.Render(a.config.SpecPath))
	}

	content := strings.Join(segments, sep)
	if lipgloss.Width(content) > a.width-2 {
		content = shared.TruncateWithEllipsis(content, a.width-2)
	}
	return barStyle.Render(content)
}

// buildStatusBar creates the status bar (always exactly 1 line)
func (a *App) buildStatusBar() string {
	var parts []string

	// Breadcrumb
	breadcrumb := a.mainViewport.GetBreadcrumb()
	if breadcrumb != "" {
		parts = append(parts, breadcrumb)
	}

	// Error or status message
	if a.err != nil {
		errMsg := strings.ReplaceAll(a.err.Error(), "\n", " ")
		// Take only first line for display
		if idx := strings.Index(errMsg, "==="); idx > 0 {
			errMsg = strings.TrimSpace(errMsg[:idx])
		}
		maxLen := a.width / 3
		if maxLen < 20 {
			maxLen = 20
		}
		if len(errMsg) > maxLen {
			errMsg = errMsg[:maxLen-3] + "..."
		}
		parts = append(parts, shared.ErrorStyle.Render("⚠ "+errMsg+" [D]"))
	} else if a.statusMsg != "" {
		msg := strings.ReplaceAll(a.statusMsg, "\n", " ")
		parts = append(parts, shared.DimStyle.Render(msg))
	}

	left := "  "
	for i, p := range parts {
		if i > 0 {
			left += " │ "
		}
		left += p
	}

	// Force single line output
	return shared.StatusBarStyle.
		Width(a.width).
		MaxWidth(a.width).
		Height(1).
		MaxHeight(1).
		Render(left)
}

// executeRequest executes an API request
func (a *App) executeRequest(req *http.Request, endpoint *openapi.Endpoint) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()

		// ---- Pre-request chain (profile → tag → operation). Each script
		// may mutate `req`; an abort skips the HTTP call.
		preSnippets := scripting.Resolve(a.overlay, endpoint, a.config.Name, scripting.PhasePre)
		preResults := scripting.RunChain(
			ctx, preSnippets, scripting.PhasePre,
			endpoint, req, nil, a.appCtx, a.overlay.Scripts.TimeoutMs,
		)
		preLogs := scripting.AllLogs(preResults)
		if aborted, reason := scripting.AnyAborted(preResults); aborted {
			return errMsg{
				err:  fmt.Errorf("pre-request script aborted: %s", reason),
				logs: preLogs,
			}
		}
		if err := scripting.AnyError(preResults); err != nil {
			return errMsg{err: fmt.Errorf("pre-request script error: %w", err), logs: preLogs}
		}

		// Set the (possibly mutated) request in the panel for viewing.
		a.requestPanel.SetRequest(req, endpoint)

		// ---- HTTP.
		resp, err := a.client.Do(ctx, req)
		if err != nil {
			return errMsg{err: err, logs: preLogs}
		}

		// ---- Post-request chain (operation → tag → profile). Mutations
		// to `resp` propagate; errors are recorded but don't fail the
		// response display.
		postSnippets := scripting.Resolve(a.overlay, endpoint, a.config.Name, scripting.PhasePost)
		postResults := scripting.RunChain(
			ctx, postSnippets, scripting.PhasePost,
			endpoint, req, resp, a.appCtx, a.overlay.Scripts.TimeoutMs,
		)
		logs := append(preLogs, scripting.AllLogs(postResults)...)
		return responseMsg{
			response:  resp,
			endpoint:  endpoint,
			logs:      logs,
			scriptErr: scripting.AnyError(postResults),
		}
	}
}

// Message types
type errMsg struct {
	err  error
	logs []scripting.LogEntry // captured pre-script logs even on abort
}

type statusMsg string

type responseMsg struct {
	response  *http.Response
	endpoint  *openapi.Endpoint
	logs      []scripting.LogEntry // pre + post-script logs, in run order
	scriptErr error                // first non-abort script error, if any
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
