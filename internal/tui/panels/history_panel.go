package panels

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/cuppojoe/feather/internal/http"
	"github.com/cuppojoe/feather/internal/openapi"
	"github.com/cuppojoe/feather/internal/tui/shared"
)

// HistoryItem stores a request/response pair with metadata
type HistoryItem struct {
	Request   *http.Request
	Response  *http.Response
	Endpoint  *openapi.Endpoint
	Timestamp time.Time
}

// RequestPanelResult is the result of a request panel update
type HistoryPanelResult struct {
	Close bool
	Cmd   tea.Cmd
}

const maxHistoryItems = 50

// HistoryPanel displays request/response with tabs
type HistoryPanel struct {
	activeTab int
	viewport  viewport.Model
	visible   bool
	focused   bool
	ready     bool
	copied    bool
	keyMap    shared.KeyMap
	width     int
	height    int
	tabRowY   int // Y position of tab row for mouse clicks

	footerHints shared.HintBar // clickable shortcut hints in the footer

	history       []HistoryItem
	historyCursor int

	// listStartIdx is the scroll offset (first visible history index) and
	// listStartRow is the panel-relative Y of the first visible row. Both
	// are populated during render so click hit-testing can be exact.
	listStartIdx int
	listStartRow int

	requestPanel *RequestPanel
}

// NewHistoryPanel creates a new request panel
func NewHistoryPanel(keys shared.KeyMap) *HistoryPanel {
	return &HistoryPanel{
		keyMap:    keys,
		activeTab: TabResponse,
		history:   make([]HistoryItem, 0),
	}
}

func (r *HistoryPanel) LinkRequestPanel(panel *RequestPanel) {
	r.requestPanel = panel
}

// SetRequest sets the current request
func (r *HistoryPanel) SetRequest(req *http.Request, endpoint *openapi.Endpoint) {
	r.ready = false
	r.updateViewportContent()
}

// SetResponse sets the current response and adds to history
func (r *HistoryPanel) AddToHistory(request *http.Request, resp *http.Response, endpoint *openapi.Endpoint) {
	r.visible = true
	r.activeTab = TabResponse
	r.ready = false

	// Add to history
	item := HistoryItem{
		Request:   request,
		Response:  resp,
		Endpoint:  endpoint,
		Timestamp: time.Now(),
	}
	// Prepend to history (newest first)
	r.history = append([]HistoryItem{item}, r.history...)
	// Limit history size
	if len(r.history) > maxHistoryItems {
		r.history = r.history[:maxHistoryItems]
	}
	r.historyCursor = 0

	r.updateViewportContent()
}

// SetVisible sets the visibility
func (r *HistoryPanel) SetVisible(visible bool) {
	r.visible = visible
}

// ShowHistory shows the panel with history view
func (r *HistoryPanel) ShowHistory() {
	r.visible = true
	r.ready = false
}

// IsVisible returns visibility state
func (r *HistoryPanel) IsVisible() bool {
	return r.visible
}

// SetFocused sets the focus state
func (r *HistoryPanel) SetFocused(focused bool) {
	r.focused = focused
}

// IsFocused returns whether the panel is focused
func (r *HistoryPanel) IsFocused() bool {
	return r.focused
}

// ID returns the panel identifier
func (r *HistoryPanel) ID() string {
	return "history"
}

// Close closes the panel
func (r *HistoryPanel) Close() {
	r.visible = false
}

// Clear empties the stored request/response history.
func (r *HistoryPanel) Clear() {
	r.history = r.history[:0]
	r.historyCursor = 0
	r.updateViewportContent()
}

// updateViewportContent updates the viewport with current tab content
func (r *HistoryPanel) updateViewportContent() {
	if !r.ready {
		return
	}

	var content string
	content = r.renderHistoryView()

	r.viewport.SetContent(content)
}

// renderHistoryView renders the history list
func (r *HistoryPanel) renderHistoryView() string {
	if len(r.history) == 0 {
		return shared.DimStyle.Render("No request history yet\n\nExecute a request to see it here")
	}

	var b strings.Builder

	// Calculate visible items
	listHeight := r.viewport.Height - 2
	if listHeight < 1 {
		listHeight = 10
	}

	startIdx := 0
	if r.historyCursor >= listHeight {
		startIdx = r.historyCursor - listHeight + 1
	}
	r.listStartIdx = startIdx
	// Panel layout: border-top (1) + title (1) + divider (1) + hidden table
	// border-top (1) = first visible row at panel-Y 4.
	r.listStartRow = 4

	// Build table rows
	var rows [][]string
	for i := startIdx; i < len(r.history) && i < startIdx+listHeight; i++ {
		item := r.history[i]
		cursor := " "
		if i == r.historyCursor {
			cursor = ">"
		}

		// Format timestamp
		timeStr := item.Timestamp.Format("15:04:05")

		// Status indicator
		statusCode := fmt.Sprintf("%d", item.Response.StatusCode)

		// Path (truncated)
		path := ""
		if item.Endpoint != nil {
			path = shared.TruncateWithEllipsis(item.Endpoint.Path, r.width-40)
		}

		rows = append(rows, []string{
			cursor,
			timeStr,
			item.Endpoint.Method,
			statusCode,
			path,
		})
	}

	// Create table
	t := table.New().
		Border(lipgloss.HiddenBorder()).
		Rows(rows...).
		Width(r.width - 8).
		StyleFunc(func(row, col int) lipgloss.Style {
			actualIdx := startIdx + row
			isSelected := actualIdx == r.historyCursor

			switch col {
			case 0: // Cursor
				style := lipgloss.NewStyle().Width(2)
				if isSelected {
					return style.Foreground(shared.ColorPrimary).Bold(true)
				}
				return style
			case 1: // Time
				return lipgloss.NewStyle().Width(10).Foreground(shared.ColorMuted)
			case 2: // Method
				if actualIdx < len(r.history) {
					methodColor, ok := shared.MethodColors[r.history[actualIdx].Endpoint.Method]
					if !ok {
						methodColor = shared.ColorMuted
					}
					return lipgloss.NewStyle().Width(8).Foreground(methodColor).Bold(true)
				}
				return lipgloss.NewStyle().Width(8)
			case 3: // Status
				if actualIdx < len(r.history) {
					statusCode := r.history[actualIdx].Response.StatusCode
					return lipgloss.NewStyle().Width(6).Foreground(shared.StatusCodeColor(statusCode))
				}
				return lipgloss.NewStyle().Width(6)
			case 4: // Path
				style := lipgloss.NewStyle()
				if isSelected {
					return style.Foreground(shared.ColorPrimary).Bold(true)
				}
				return style.Foreground(lipgloss.Color("#E5E7EB"))
			}
			return lipgloss.NewStyle()
		})

	b.WriteString(t.String())
	return b.String()
}

// highlightJSON applies basic syntax highlighting to JSON
func (r *HistoryPanel) highlightJSON(json string) string {
	var result strings.Builder
	inString := false
	isKey := true
	var prev rune

	for _, ch := range json {
		switch {
		case ch == '"' && prev != '\\':
			if inString {
				result.WriteRune(ch)
				inString = false
			} else {
				inString = true
				if isKey {
					result.WriteString(shared.JSONKeyStyle.Render(string(ch)))
				} else {
					result.WriteString(shared.JSONStringStyle.Render(string(ch)))
				}
			}

		case inString:
			if isKey {
				result.WriteString(shared.JSONKeyStyle.Render(string(ch)))
			} else {
				result.WriteString(shared.JSONStringStyle.Render(string(ch)))
			}

		case ch == ':':
			isKey = false
			result.WriteRune(ch)

		case ch == ',' || ch == '{' || ch == '[':
			isKey = (ch == '{' || ch == ',')
			result.WriteRune(ch)

		case ch == '}' || ch == ']':
			result.WriteRune(ch)

		case ch >= '0' && ch <= '9' || ch == '.' || ch == '-':
			result.WriteString(shared.JSONNumberStyle.Render(string(ch)))

		default:
			result.WriteRune(ch)
		}
		prev = ch
	}

	return result.String()
}

// Update handles input for the request panel
func (r *HistoryPanel) Update(msg tea.Msg) HistoryPanelResult {
	var cmd tea.Cmd

	if !r.visible {
		return HistoryPanelResult{}
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		if !r.ready {
			r.viewport = viewport.New(msg.Width-4, r.height-6)
			r.viewport.MouseWheelEnabled = true
			r.ready = true
			r.updateViewportContent()
		} else {
			r.viewport.Width = msg.Width - 4
			r.viewport.Height = r.height - 6
		}

	case tea.MouseMsg:
		if r.focused {
			// History view mouse handling
			switch msg.Button {
			case tea.MouseButtonLeft:
				if msg.Action == tea.MouseActionRelease {
					// A footer shortcut click runs it (replayed as a keypress).
					if key, ok := r.footerHints.HitKey(msg.X, msg.Y); ok {
						return r.Update(shared.KeyMsgFromName(key))
					}
					// Click on history item. listStartRow / listStartIdx are
					// populated during render so the math survives scroll.
					visualIdx := msg.Y - r.listStartRow
					actualIdx := r.listStartIdx + visualIdx
					if visualIdx >= 0 && actualIdx >= 0 && actualIdx < len(r.history) {
						r.historyCursor = actualIdx
						// Re-render the table so the `>` cursor lands on the
						// clicked row (the viewport caches the previously
						// rendered table; without this it stays put).
						r.updateViewportContent()
						item := r.history[r.historyCursor]
						r.requestPanel.ShowHistoryItem(item.Request, item.Response, item.Endpoint)
					}
				}
			case tea.MouseButtonWheelUp:
				if r.historyCursor > 0 {
					r.historyCursor--
					r.updateViewportContent()
				}
			case tea.MouseButtonWheelDown:
				if r.historyCursor < len(r.history)-1 {
					r.historyCursor++
					r.updateViewportContent()
				}
			default:
				r.viewport, cmd = r.viewport.Update(msg)
				return HistoryPanelResult{Cmd: cmd}
			}
		}

	case tea.KeyMsg:
		if r.focused {
			// History view key handling
			switch {
			case msg.String() == "x":
				return HistoryPanelResult{Close: true}
			case key.Matches(msg, r.keyMap.Up):
				if r.historyCursor > 0 {
					r.historyCursor--
					r.updateViewportContent()
				}
			case key.Matches(msg, r.keyMap.Down):
				if r.historyCursor < len(r.history)-1 {
					r.historyCursor++
					r.updateViewportContent()
				}
			case msg.String() == "D":
				// Clear all stored history.
				r.Clear()
			case key.Matches(msg, r.keyMap.Enter):
				// Select history item to view details (request + response).
				if len(r.history) > 0 && r.historyCursor < len(r.history) {
					item := r.history[r.historyCursor]
					r.requestPanel.ShowHistoryItem(item.Request, item.Response, item.Endpoint)
					r.activeTab = TabResponse
					r.ready = false
					r.updateViewportContent()
				}
			case key.Matches(msg, r.keyMap.Home):
				r.historyCursor = 0
				r.updateViewportContent()
			case key.Matches(msg, r.keyMap.End):
				r.historyCursor = len(r.history) - 1
				if r.historyCursor < 0 {
					r.historyCursor = 0
				}
				r.updateViewportContent()
			default:
				// Forward to viewport for scrolling
				r.viewport, cmd = r.viewport.Update(msg)
				return HistoryPanelResult{Cmd: cmd}
			}
		}
	}

	return HistoryPanelResult{Cmd: cmd}
}

// View renders the request panel
func (r *HistoryPanel) View(width, height int) string {
	if !r.visible {
		return ""
	}

	r.width = width / 2
	r.height = height
	return r.renderHistoryPanel()
}

// renderHistoryPanel renders the history list view
func (r *HistoryPanel) renderHistoryPanel() string {
	// Content width accounts for border and padding
	contentWidth := r.width - shared.BorderStyle.GetHorizontalFrameSize()
	viewportHeight := r.height - 5 // 1 title, 1 divider, 1 footer, 2 border

	// Initialize viewport if needed
	if !r.ready {
		r.viewport = viewport.New(contentWidth, viewportHeight)
		r.viewport.MouseWheelEnabled = true
		r.ready = true
		r.updateViewportContent()
	} else {
		r.viewport.Width = contentWidth
		r.viewport.Height = viewportHeight
	}

	// Title line with count
	title := shared.TitleStyle.Render("History ")
	topLine := lipgloss.JoinHorizontal(lipgloss.Top, title, shared.DimStyle.Render(fmt.Sprintf("(%d)", len(r.history))))

	// Divider
	divider := shared.DimStyle.Render(strings.Repeat("─", contentWidth))

	// Footer with clickable hints. Footer row (panel-relative): top border (1)
	// + title + divider (2) + viewport. startCol is the border+padding inset.
	startCol := shared.BorderStyle.GetHorizontalFrameSize() / 2
	footerRow := viewportHeight + 3
	footerLeft := r.footerHints.Render([]shared.Hint{
		{Key: "enter", Label: "view"},
		{Key: "D", Label: "clear"},
		{Key: "x", Label: "close"},
	}, footerRow, startCol, false, "  ", shared.DimStyle)
	scrollPercent := fmt.Sprintf("%3.f%%", r.viewport.ScrollPercent()*100)
	footerRight := shared.DimStyle.Render(scrollPercent)
	footerGap := contentWidth - lipgloss.Width(footerLeft) - lipgloss.Width(footerRight)
	if footerGap < 0 {
		footerGap = 0
	}
	footer := footerLeft + strings.Repeat(" ", footerGap) + footerRight

	// Compose the panel content
	content := lipgloss.JoinVertical(lipgloss.Left,
		topLine,
		divider,
		r.viewport.View(),
		footer,
	)

	return shared.Panel(content, r.width, r.height, r.focused)
}
