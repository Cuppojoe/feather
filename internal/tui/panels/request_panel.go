package panels

import (
	"fmt"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/cuppojoe/feather/internal/http"
	"github.com/cuppojoe/feather/internal/openapi"
	"github.com/cuppojoe/feather/internal/scripting"
	"github.com/cuppojoe/feather/internal/tui/shared"
)

// Tab indices
const (
	TabRequest = iota
	TabResponse
	TabHeaders
	TabSummary
	TabConsole
)

// RequestPanel displays request/response with tabs
type RequestPanel struct {
	request     *http.Request
	response    *http.Response
	endpoint    *openapi.Endpoint
	activeTab   int
	pager       shared.TextEditor
	visible     bool
	focused     bool
	ready       bool
	copied      bool
	copyHint    string // method or error message shown next to "copied!"
	keyMap      shared.KeyMap
	width       int
	height      int
	tabRowY     int            // Y position of tab row for mouse clicks (panel-relative)
	tabBounds   []int          // panel-relative end-col (exclusive) of each tab, in order
	footerHints shared.HintBar // clickable shortcut hints in the footer

	// Cached response presentation (computed when the response is set).
	respText string
	respKind http.BodyKind

	// Script execution output from the last request (pre + post chains).
	// scriptErr is set when a post-script returned a non-abort error.
	scriptLogs []scripting.LogEntry
	scriptErr  error
	// consoleUnread is true when new logs have arrived since the user last
	// visited the Console tab — surfaces a "●" marker on the tab label.
	consoleUnread bool

	// History
	history *HistoryPanel
}

// setResponseDisplay computes how the current response body should be shown
// (JSON pretty-print, plain text, or a binary summary) and caches it.
func (r *RequestPanel) setResponseDisplay() {
	if r.response == nil {
		r.respText, r.respKind = "", http.BodyEmpty
		return
	}
	r.respText, r.respKind = http.FormatResponseBody(r.response.ContentType(), r.response.Body)
}

// RequestPanelResult is the result of a request panel update
type RequestPanelResult struct {
	Close bool
	Cmd   tea.Cmd
}

// NewRequestPanel creates a new request panel
func NewRequestPanel(keys shared.KeyMap) *RequestPanel {
	return &RequestPanel{
		keyMap:    keys,
		activeTab: TabResponse,
	}
}

func (r *RequestPanel) LinkHistoryPanel(history *HistoryPanel) {
	r.history = history
}

// SetRequest sets the current request
func (r *RequestPanel) SetRequest(req *http.Request, endpoint *openapi.Endpoint) {
	r.request = req
	r.endpoint = endpoint
	r.ready = false
	r.updateViewportContent()
}

// SetResponse sets the current response and adds to history
func (r *RequestPanel) SetResponse(resp *http.Response, endpoint *openapi.Endpoint) {
	r.response = resp
	r.endpoint = endpoint
	r.visible = true
	r.activeTab = TabResponse
	r.ready = false
	r.pager.ClearSearch()
	r.setResponseDisplay()

	r.history.AddToHistory(r.request, resp, endpoint)

	r.updateViewportContent()
}

// SetScriptOutput stores the pre+post script logs from the last execution.
// Pass nil/empty to clear. scriptErr is the first post-script error, if any.
func (r *RequestPanel) SetScriptOutput(logs []scripting.LogEntry, scriptErr error) {
	r.scriptLogs = logs
	r.scriptErr = scriptErr
	r.consoleUnread = len(logs) > 0 || scriptErr != nil
	// If the user is already on the Console tab when logs arrive, refresh
	// the editor immediately so they see the new output.
	if r.activeTab == TabConsole {
		r.updateViewportContent()
	}
}

// ShowHistoryItem displays a stored request/response pair without re-recording
// it in history. Used when selecting an entry from the history panel.
func (r *RequestPanel) ShowHistoryItem(req *http.Request, resp *http.Response, endpoint *openapi.Endpoint) {
	r.request = req
	r.response = resp
	r.endpoint = endpoint
	r.visible = true
	r.activeTab = TabResponse
	r.ready = false
	r.pager.ClearSearch()
	r.setResponseDisplay()
	r.updateViewportContent()
}

// SetVisible sets the visibility
func (r *RequestPanel) SetVisible(visible bool) {
	r.visible = visible
}

// ShowHistory shows the panel with history view
func (r *RequestPanel) ShowHistory() {
	r.visible = true
	r.ready = false
}

// IsVisible returns visibility state
func (r *RequestPanel) IsVisible() bool {
	return r.visible
}

// SetFocused sets the focus state
func (r *RequestPanel) SetFocused(focused bool) {
	r.focused = focused
}

// IsFocused returns whether the panel is focused
func (r *RequestPanel) IsFocused() bool {
	return r.focused
}

// IsSearching reports whether the pager is capturing input (search pattern
// being typed OR the language picker open). While true the app routes all
// key input here so pattern characters and picker navigation aren't
// intercepted by global bindings.
func (r *RequestPanel) IsSearching() bool {
	return r.pager.IsSearching() || r.pager.PickerOpen()
}

// ID returns the panel identifier
func (r *RequestPanel) ID() string {
	return "request"
}

// Close hides the panel but retains the last request/response so reopening it
// (or toggling with 'r') shows the same data again. History keeps older items.
func (r *RequestPanel) Close() {
	r.visible = false
}

// updateViewportContent feeds the TextEditor when the active tab is one of
// the pager-backed tabs (Response, Console). Other tabs render their content
// directly in View() — they're pre-styled tables that would confuse the
// editor's plain-text pipeline.
func (r *RequestPanel) updateViewportContent() {
	if !r.ready {
		return
	}
	switch r.activeTab {
	case TabResponse:
		language := r.responseLanguage()
		if language == "" {
			language = "plain"
		}
		r.pager.SetLanguage(language)
		r.pager.SetValue(r.renderResponseTab())
	case TabConsole:
		plain, spans := r.renderConsoleContent()
		// Plain language: Chroma shouldn't try to tokenize log output; our
		// own spans handle the LEVEL tag colour and the dim block headers.
		r.pager.SetLanguage("plain")
		r.pager.SetValue(plain)
		r.pager.SetLineStyles(spans)
	}
}

// responseLanguage picks a syntax-highlight grammar for the response body from
// its classification and Content-Type, falling back to content sniffing.
func (r *RequestPanel) responseLanguage() string {
	switch r.respKind {
	case http.BodyJSON:
		return "json"
	case http.BodyText:
		if lang := shared.LexerForMIME(r.response.ContentType()); lang != "plain" {
			return lang
		}
		return shared.LexerForContent(r.respText)
	default:
		return "plain"
	}
}

// requestLanguage detects the grammar of the request body (usually JSON).
func (r *RequestPanel) requestLanguage() string {
	if r.request == nil || len(r.request.Body) == 0 {
		return "plain"
	}
	if http.ClassifyBody("", r.request.Body) == http.BodyJSON {
		return "json"
	}
	return shared.LexerForContent(string(r.request.Body))
}

// renderRequestTab renders the request body
func (r *RequestPanel) renderRequestTab() string {
	if r.request == nil {
		return shared.DimStyle.Render("No request data")
	}

	var b strings.Builder

	// Method and path
	method := shared.MethodStyle(r.request.Method).Render(r.request.Method)
	b.WriteString(fmt.Sprintf("%s %s\n\n", method, r.request.Path))

	// Headers as table
	if len(r.request.Headers) > 0 {
		b.WriteString(shared.DimStyle.Render("Headers:"))
		b.WriteString("\n")

		var headerKeys []string
		for k := range r.request.Headers {
			headerKeys = append(headerKeys, k)
		}
		sort.Strings(headerKeys)

		var rows [][]string
		for _, k := range headerKeys {
			// Redact bearer tokens, cookies, API keys, etc. so they don't
			// flash on screen — feather is often used in shared contexts
			// (pairing, screenshares, demos).
			rows = append(rows, []string{k, shared.RedactHeaderValue(k, r.request.Headers[k])})
		}

		t := table.New().
			Border(lipgloss.NormalBorder()).
			BorderStyle(lipgloss.NewStyle().Foreground(shared.ColorBorder)).
			Rows(rows...).
			Width(r.width - 8).
			StyleFunc(func(row, col int) lipgloss.Style {
				style := lipgloss.NewStyle().Padding(0, 1)
				if col == 0 {
					return style.Foreground(shared.ColorPrimary)
				}
				return style.Foreground(lipgloss.Color("#E5E7EB"))
			})
		b.WriteString(t.String())
	}

	// Body — the editor applies JSON highlighting per visible line, so we
	// hand it the raw formatted text rather than pre-styling here.
	if r.request.Body != nil && len(r.request.Body) > 0 {
		b.WriteString("\n")
		b.WriteString(shared.DimStyle.Render("Body:"))
		b.WriteString("\n")
		if formatted, err := http.FormatJSON(r.request.Body); err == nil {
			b.WriteString(formatted)
		} else {
			b.WriteString(string(r.request.Body))
		}
	}

	return b.String()
}

// newResponseViewer builds a TextEditor configured as the read-only response
// viewer. The grammar is chosen per content in updateViewportContent.
func newResponseViewer(width, height int) shared.TextEditor {
	e := shared.NewTextEditor(width, height)
	e.SetReadOnly(true)
	e.Focus() // focused so it receives scroll + search keys
	return e
}

// activeTabContent returns the raw text of the active tab — used to feed
// `$EDITOR` for the read-only vim viewer. Strips ANSI styling so the file
// is human-readable.
func (r *RequestPanel) activeTabContent() string {
	switch r.activeTab {
	case TabRequest:
		if r.request != nil && len(r.request.Body) > 0 {
			if formatted, err := http.FormatJSON(r.request.Body); err == nil {
				return formatted
			}
			return string(r.request.Body)
		}
	case TabResponse:
		if r.response != nil {
			return r.respText
		}
	case TabConsole:
		plain, _ := r.renderConsoleContent()
		return plain
	}
	return ""
}

// renderResponseTab returns the response body for display. The body is
// classified (JSON / text / binary) when the response is set; JSON is
// pretty-printed and highlighted, text shown as-is, and binary rendered as a
// summary instead of garbled bytes.
func (r *RequestPanel) renderResponseTab() string {
	if r.response == nil {
		return shared.DimStyle.Render("No response yet")
	}
	if r.respKind == http.BodyEmpty {
		return shared.DimStyle.Render("(empty response body)")
	}
	return r.respText
}

// renderHeadersTab renders response headers
func (r *RequestPanel) renderHeadersTab() string {
	if r.response == nil {
		return shared.DimStyle.Render("No response yet")
	}

	// Sort headers for consistent display
	var headerKeys []string
	for k := range r.response.Headers {
		headerKeys = append(headerKeys, k)
	}
	sort.Strings(headerKeys)

	if len(headerKeys) == 0 {
		return shared.DimStyle.Render("No headers")
	}

	// Build table rows. Redact each value individually so multi-valued
	// sensitive headers (e.g. multiple Set-Cookie lines) each get their
	// own bullets + length hint.
	var rows [][]string
	for _, k := range headerKeys {
		v := r.response.Headers[k]
		redacted := make([]string, len(v))
		for i, val := range v {
			redacted[i] = shared.RedactHeaderValue(k, val)
		}
		rows = append(rows, []string{k, strings.Join(redacted, ", ")})
	}

	// Create styled table
	t := table.New().
		Border(lipgloss.NormalBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(shared.ColorBorder)).
		Headers("HEADER", "VALUE").
		Rows(rows...).
		Width(r.width - 8).
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == table.HeaderRow {
				return lipgloss.NewStyle().
					Foreground(shared.ColorMuted).
					Bold(true).
					Padding(0, 1)
			}

			style := lipgloss.NewStyle().Padding(0, 1)
			if col == 0 {
				return style.Foreground(shared.ColorPrimary)
			}
			return style.Foreground(lipgloss.Color("#E5E7EB"))
		})

	return t.String()
}

// renderSummaryTab renders a summary of the request/response (status, timing,
// size, etc.).
func (r *RequestPanel) renderSummaryTab() string {
	if r.response == nil {
		return shared.DimStyle.Render("No response yet")
	}

	rows := [][]string{
		{"Total Duration", r.response.Duration.String()},
		{"Status Code", fmt.Sprintf("%d", r.response.StatusCode)},
		{"Response Size", fmt.Sprintf("%d bytes", len(r.response.Body))},
	}

	t := table.New().
		Border(lipgloss.NormalBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(shared.ColorBorder)).
		Headers("METRIC", "VALUE").
		Rows(rows...).
		Width(r.width - 8).
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == table.HeaderRow {
				return lipgloss.NewStyle().
					Foreground(shared.ColorMuted).
					Bold(true).
					Padding(0, 1)
			}

			style := lipgloss.NewStyle().Padding(0, 1)
			if col == 0 {
				return style.Foreground(shared.ColorMuted)
			}
			// Highlight duration in green
			if row == 0 {
				return style.Foreground(shared.ColorSuccess).Bold(true)
			}
			return style.Foreground(lipgloss.Color("#E5E7EB"))
		})

	return t.String()
}

// setActiveTab switches the active tab and clears the Console unread marker
// when the user lands on it. updateViewportContent gets re-run so the
// TextEditor's content matches the new tab (only meaningful for Response).
func (r *RequestPanel) setActiveTab(t int) {
	r.activeTab = t
	if t == TabConsole {
		r.consoleUnread = false
	}
	r.updateViewportContent()
}

// renderConsoleContent builds the captured pre+post script output as plain
// text plus a parallel [][]Span that styles block headers, [LEVEL] tags, and
// error lines at render time. Returning the pair lets us feed the result to
// the read-only TextEditor (SetValue + SetLineStyles) so the user gets the
// same search / scroll / scrollbar-drag behaviour as the response viewer.
func (r *RequestPanel) renderConsoleContent() (string, [][]shared.Span) {
	if len(r.scriptLogs) == 0 && r.scriptErr == nil {
		plain := "No script output.\n\nDefine pre/post-request hooks in the Scripts modal."
		parts := strings.Split(plain, "\n")
		spans := make([][]shared.Span, len(parts))
		for i, ln := range parts {
			if ln == "" {
				continue
			}
			spans[i] = []shared.Span{{Start: 0, End: len(ln), Style: shared.DimStyle}}
		}
		return plain, spans
	}

	var lines []string
	var spans [][]shared.Span
	var lastHeader string

	for _, e := range r.scriptLogs {
		header := scriptBlockHeader(e.Phase, e.Scope, e.Tag)
		if header != lastHeader {
			if lastHeader != "" {
				lines = append(lines, "")
				spans = append(spans, nil)
			}
			text := "─── " + header + " ───"
			lines = append(lines, text)
			spans = append(spans, []shared.Span{{Start: 0, End: len(text), Style: shared.DimStyle}})
			lastHeader = header
		}

		level := strings.ToUpper(string(e.Level))
		tag := "[" + level + "]"
		var tagStyle lipgloss.Style
		switch e.Level {
		case scripting.LogWarn:
			tagStyle = shared.WarningStyle
		case scripting.LogError:
			tagStyle = shared.ErrorStyle
		default:
			tagStyle = shared.DimStyle
		}

		// Messages may contain newlines — emit the first line with the
		// [LEVEL] prefix styled, then any continuation lines as plain text
		// (matching the previous pre-styled renderer's behaviour).
		msgLines := strings.Split(e.Message, "\n")
		first := tag + "  " + msgLines[0]
		lines = append(lines, first)
		spans = append(spans, []shared.Span{{Start: 0, End: len(tag), Style: tagStyle}})
		for _, ml := range msgLines[1:] {
			lines = append(lines, ml)
			spans = append(spans, nil)
		}
	}

	if r.scriptErr != nil {
		lines = append(lines, "")
		spans = append(spans, nil)
		errLine := "script error: " + r.scriptErr.Error()
		lines = append(lines, errLine)
		spans = append(spans, []shared.Span{{Start: 0, End: len(errLine), Style: shared.ErrorStyle}})
	}

	return strings.Join(lines, "\n"), spans
}

// tabUsesPager reports whether the active tab routes its body through the
// shared TextEditor (and therefore supports search, scroll, scrollbar drag).
func (r *RequestPanel) tabUsesPager() bool {
	return r.activeTab == TabResponse || r.activeTab == TabConsole
}

func scriptBlockHeader(phase scripting.Phase, scope scripting.Scope, tag string) string {
	scopeLabel := string(scope)
	if scope == scripting.ScopeTag && tag != "" {
		scopeLabel = "tag: " + tag
	}
	return string(phase) + " / " + scopeLabel
}

// Syntax highlighting is performed inside TextEditor via Chroma grammars; the
// panel just selects the language per tab (see responseLanguage /
// requestLanguage).

// Update handles input for the request panel
func (r *RequestPanel) Update(msg tea.Msg) RequestPanelResult {
	var cmd tea.Cmd

	if !r.visible {
		return RequestPanelResult{}
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		if !r.ready {
			r.pager = newResponseViewer(msg.Width-4, r.height-6)
			r.ready = true
			r.updateViewportContent()
		} else {
			r.pager.SetSize(msg.Width-4, r.height-6)
		}

	case tea.MouseMsg:
		if r.focused {
			switch msg.Button {
			case tea.MouseButtonLeft:
				// Forward into the pager so it can handle scrollbar
				// press / drag / release — only on tabs that actually
				// render through the editor (Response, Console). On
				// other tabs clicks in the body shouldn't drive its
				// state.
				if r.tabUsesPager() {
					cmd = r.pager.Update(msg)
					if msg.Action != tea.MouseActionRelease {
						return RequestPanelResult{Cmd: cmd}
					}
				}
				if msg.Action == tea.MouseActionRelease {
					// Hit-test the tab bar using measured per-tab widths.
					if msg.Y == r.tabRowY {
						startCol := shared.BorderStyle.GetHorizontalFrameSize() / 2
						if msg.X >= startCol && len(r.tabBounds) > 0 {
							for i, end := range r.tabBounds {
								if msg.X < end {
									r.activeTab = i
									r.updateViewportContent()
									break
								}
							}
						}
					}
					// Clicking a footer shortcut runs it (replayed as a keypress).
					if key, ok := r.footerHints.HitKey(msg.X, msg.Y); ok {
						return r.Update(shared.KeyMsgFromName(key))
					}
				}
			default:
				// Forward scroll events to the pager — only on tabs that
				// route through it (Response, Console).
				if r.tabUsesPager() {
					cmd = r.pager.Update(msg)
					return RequestPanelResult{Cmd: cmd}
				}
			}
		}

	case tea.KeyMsg:
		if r.focused {
			// While a search pattern is being entered OR the language picker
			// is open, the pager owns every keystroke — don't let panel
			// bindings (tabs, copy, close) fire.
			if r.pager.IsSearching() || r.pager.PickerOpen() {
				cmd = r.pager.Update(msg)
				return RequestPanelResult{Cmd: cmd}
			}

			// Detail view key handling
			switch {
			case (msg.String() == "/" || msg.String() == "?" ||
				msg.String() == "n" || msg.String() == "N") &&
				r.tabUsesPager():
				// Search lives in the editor — meaningful on any tab
				// that routes through it (Response, Console).
				cmd = r.pager.Update(msg)
				return RequestPanelResult{Cmd: cmd}
			case msg.String() == "x":
				return RequestPanelResult{Close: true}
			case key.Matches(msg, r.keyMap.Back):
				// First escape clears an active search; a second one (nothing
				// left to clear) closes the panel.
				if r.pager.ClearSearch() {
					return RequestPanelResult{}
				}
				return RequestPanelResult{Close: true}
			case msg.String() == "1":
				r.setActiveTab(TabRequest)
			case msg.String() == "2":
				r.setActiveTab(TabResponse)
			case msg.String() == "3":
				r.setActiveTab(TabHeaders)
			case msg.String() == "4":
				r.setActiveTab(TabSummary)
			case msg.String() == "5":
				r.setActiveTab(TabConsole)
			case key.Matches(msg, r.keyMap.Left):
				if r.activeTab > 0 {
					r.setActiveTab(r.activeTab - 1)
				}
			case key.Matches(msg, r.keyMap.Right):
				if r.activeTab < TabConsole {
					r.setActiveTab(r.activeTab + 1)
				}
			case key.Matches(msg, r.keyMap.Copy):
				if r.response != nil {
					// Copy the same text shown in the viewer: pretty JSON, the
					// raw text, or the binary summary.
					res := shared.CopyToClipboard(r.respText)
					r.copied = res.Err == nil
					if res.Err != nil {
						r.copyHint = "copy failed: " + res.Err.Error()
					} else {
						r.copyHint = res.Method
					}
				}
			case msg.String() == "ctrl+v":
				// Open whatever the active tab shows in an external editor
				// (read-only).
				if content := r.activeTabContent(); content != "" {
					ext := ".json"
					if r.activeTab == TabResponse && r.respKind != http.BodyJSON {
						ext = ".txt"
					}
					return RequestPanelResult{
						Cmd: shared.OpenInEditor("response_panel", content, ext, true),
					}
				}
			default:
				// Forward to the pager for scrolling
				cmd = r.pager.Update(msg)
				return RequestPanelResult{Cmd: cmd}
			}
		}
	}

	return RequestPanelResult{Cmd: cmd}
}

// View renders the request panel
func (r *RequestPanel) View(width, height int) string {
	if !r.visible {
		return ""
	}

	r.width = width / 2
	r.height = height
	return r.renderDetailPanel()
}

// renderDetailPanel renders the detail view with tabs
func (r *RequestPanel) renderDetailPanel() string {
	// Content width accounts for border and padding
	contentWidth := r.width - shared.BorderStyle.GetHorizontalFrameSize()
	viewportHeight := r.height - 6 // 1 status, 1 tabs, 1 divider, 1 footer, 2 border

	// Initialize the viewer if needed.
	if !r.ready {
		r.pager = newResponseViewer(contentWidth, viewportHeight)
		r.ready = true
		r.updateViewportContent()
	} else {
		r.pager.SetSize(contentWidth, viewportHeight)
	}

	// Status line: METHOD PATH STATUS [x]
	var statusLine string
	if r.response != nil && r.endpoint != nil {
		statusStyle := shared.StatusCodeStyle(r.response.StatusCode)
		method := shared.MethodStyle(r.endpoint.Method).Render(r.endpoint.Method)
		maxPathLen := contentWidth - 25 // Room for method, status, close button
		if maxPathLen < 20 {
			maxPathLen = 20
		}
		path := shared.TruncateWithEllipsis(r.endpoint.Path, maxPathLen)

		leftPart := fmt.Sprintf("%s %s  %s", method, path, statusStyle.Render(r.response.Status))
		gap := contentWidth - lipgloss.Width(leftPart)
		if gap < 1 {
			gap = 1
		}
		statusLine = leftPart + strings.Repeat(" ", gap)
	} else {
		statusLine = shared.DimStyle.Render("No response")
	}

	// Tab bar. Build each tab, measure its rendered width, and remember the
	// panel-relative end column so the mouse click handler can hit-test
	// against the real positions instead of an assumed uniform width.
	tabs := []string{"Request", "Response", "Headers", "Summary", "Console"}
	var tabViews []string
	r.tabBounds = r.tabBounds[:0]
	startCol := shared.BorderStyle.GetHorizontalFrameSize() / 2 // border + left padding
	cursor := startCol
	for i, tab := range tabs {
		label := fmt.Sprintf("%d:%s", i+1, tab)
		// Tag the Console tab with a marker when there are unread logs or
		// a script error from the last execution.
		if i == TabConsole {
			if r.scriptErr != nil {
				label += " " + shared.ErrorStyle.Render("●")
			} else if r.consoleUnread {
				label += " " + shared.WarningStyle.Render("●")
			}
		}
		var view string
		if i == r.activeTab {
			view = shared.ActiveTabStyle.Render(label)
		} else {
			view = shared.InactiveTabStyle.Render(label)
		}
		tabViews = append(tabViews, view)
		cursor += lipgloss.Width(view)
		r.tabBounds = append(r.tabBounds, cursor)
	}
	tabBar := lipgloss.JoinHorizontal(lipgloss.Bottom, tabViews...)
	// Tab row is the first content row inside the top border.
	r.tabRowY = 1

	// Divider
	divider := shared.DimStyle.Render(strings.Repeat("─", contentWidth))

	// Footer with scroll percentage and copy hint
	scrollPercent := fmt.Sprintf("%3.f%%", r.pager.ScrollPercent()*100)
	// Footer row in panel-relative coordinates so clicked hints can be
	// hit-tested: top border (1) + tab + divider + status (3) + viewport.
	// startCol (declared with the tab bar above) is the same left inset.
	footerRow := viewportHeight + 4

	// Search / scroll-% come from the editor — only show them on tabs
	// where the editor is actually in use (Response, Console).
	usesPager := r.tabUsesPager()
	var footerLeft, footerRight string
	if usesPager && r.pager.IsSearching() {
		r.footerHints.Render(nil, footerRow, startCol, false, "  ", shared.DimStyle) // clear stale regions
		footerLeft = r.pager.PromptLine()
		footerRight = shared.DimStyle.Render(scrollPercent)
	} else {
		hints := []shared.Hint{{Key: "esc", Label: "back"}}
		if usesPager {
			hints = append(hints, shared.Hint{Key: "/", Label: "search"})
		}
		// The copy slot shows "y:copy" normally, or a transient status after a
		// copy attempt (in which case it isn't a clickable hint).
		showCopyStatus := r.copied || r.copyHint != ""
		if !showCopyStatus {
			hints = append(hints, shared.Hint{Key: "y", Label: "copy"})
		}
		footerLeft = r.footerHints.Render(hints, footerRow, startCol, false, "  ", shared.DimStyle)
		if r.copied {
			footerLeft += "  " + shared.SuccessStyle.Render("copied!")
			r.copied = false
			r.copyHint = ""
		} else if r.copyHint != "" {
			footerLeft += "  " + shared.ErrorStyle.Render(r.copyHint)
			r.copyHint = ""
		}
		if usesPager {
			footerRight = shared.DimStyle.Render(scrollPercent)
			if status := r.pager.StatusLine(); status != "" {
				footerRight = status + "  " + footerRight
			}
		}
	}
	footerGap := contentWidth - lipgloss.Width(footerLeft) - lipgloss.Width(footerRight)
	if footerGap < 0 {
		footerGap = 0
	}
	footer := footerLeft + strings.Repeat(" ", footerGap) + footerRight

	// Body of the active tab. Tabs that go through the TextEditor
	// (Response, Console) render via the pager; the rest are pre-styled
	// strings sized to viewportHeight.
	var bodyView string
	if r.tabUsesPager() {
		// Editor sits below border-top + tab + divider + status (panel-row 4)
		// and right of border-left + left-padding (col 2). Tell it so
		// scrollbar press/drag/release coords resolve correctly.
		r.pager.SetMouseOrigin(2, 4)
		bodyView = r.pager.View()
	} else {
		var raw string
		switch r.activeTab {
		case TabRequest:
			raw = r.renderRequestTab()
		case TabHeaders:
			raw = r.renderHeadersTab()
		case TabSummary:
			raw = r.renderSummaryTab()
		}
		bodyView = lipgloss.NewStyle().
			Width(contentWidth).
			Height(viewportHeight).
			MaxHeight(viewportHeight).
			Render(raw)
	}

	// Compose the panel content (status line is below divider)
	content := lipgloss.JoinVertical(lipgloss.Left,
		tabBar,
		divider,
		statusLine,
		bodyView,
		footer,
	)

	return shared.Panel(content, r.width, r.height, r.focused)
}
