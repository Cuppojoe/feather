package panels

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/cuppojoe/feather/internal/tui/shared"
)

// HelpSection is one entry in the Help modal's table of contents.
type HelpSection struct {
	Title   string
	Summary string // one-line description shown next to the title
	Body    string // markdown-flavoured text
}

// helpSections is the registry. Add an entry per topic as the docs grow.
var helpSections = []HelpSection{
	{
		Title:   "Environment variables",
		Summary: "Referencing ${vars} in headers, params, and the base URL",
		Body:    environmentHelpText,
	},
	{
		Title:   "Scripts",
		Summary: "Writing pre/post-request JavaScript hooks",
		Body:    scriptsHelpText,
	},
}

// HelpPanel is a global, anywhere-accessible help reader. It opens on F1
// and shows a list of sections; selecting one renders that section's body
// in a read-only TextEditor (search + scroll + mouse-wheel + scrollbar).
type HelpPanel struct {
	expanded bool

	sections []HelpSection
	cursor   int

	viewing       bool              // true while inside a section's body
	activeSection int               // index of the section being read
	bodyEditor    shared.TextEditor // read-only viewer for the section body
	clickMap      shared.ClickMap   // section-list row hit-tests
}

// HelpPanelResult mirrors the other panel-result types.
type HelpPanelResult struct {
	Cmd tea.Cmd
}

// NewHelpPanel returns a ready-to-open help reader.
func NewHelpPanel() *HelpPanel {
	ed := shared.NewTextEditor(40, 10)
	ed.SetReadOnly(true)
	// language=plain: the body is already ANSI-styled by renderHelpDoc, so
	// running it through a syntax lexer would strip / fight the styling.
	ed.SetLanguage("plain")
	ed.SetShowLineNumbers(false)
	_ = ed.Focus()
	return &HelpPanel{
		sections:   helpSections,
		bodyEditor: ed,
	}
}

// IsExpanded reports whether the modal is open.
func (h *HelpPanel) IsExpanded() bool { return h.expanded }

// IsEditing is true while the body viewer is searching (so the host
// keeps routing all keys here).
func (h *HelpPanel) IsEditing() bool {
	return h.expanded && h.viewing && h.bodyEditor.IsSearching()
}

// Toggle opens or closes the modal. Opening always starts on the section
// list so the user picks a topic.
func (h *HelpPanel) Toggle() {
	if h.expanded {
		h.expanded = false
		h.viewing = false
		return
	}
	h.expanded = true
	h.viewing = false
	if h.cursor < 0 || h.cursor >= len(h.sections) {
		h.cursor = 0
	}
}

// Update handles keys + mouse while the modal is open.
func (h *HelpPanel) Update(msg tea.Msg) HelpPanelResult {
	if !h.expanded {
		return HelpPanelResult{}
	}

	if h.viewing {
		return h.updateViewing(msg)
	}
	return h.updateList(msg)
}

func (h *HelpPanel) updateList(msg tea.Msg) HelpPanelResult {
	if km, ok := msg.(tea.KeyMsg); ok {
		switch {
		case km.String() == "esc":
			h.expanded = false
		case km.String() == "up", km.String() == "k":
			if h.cursor > 0 {
				h.cursor--
			}
		case km.String() == "down", km.String() == "j":
			if h.cursor < len(h.sections)-1 {
				h.cursor++
			}
		case km.String() == "home", km.String() == "g":
			h.cursor = 0
		case km.String() == "end", km.String() == "G":
			h.cursor = len(h.sections) - 1
		case km.String() == "enter":
			h.openSection(h.cursor)
		}
		return HelpPanelResult{}
	}
	if mm, ok := msg.(tea.MouseMsg); ok && mm.Button == tea.MouseButtonLeft &&
		mm.Action == tea.MouseActionRelease {
		if id, ok := h.clickMap.Hit(mm.X, mm.Y); ok {
			h.cursor = id
			h.openSection(id)
		}
	}
	return HelpPanelResult{}
}

func (h *HelpPanel) updateViewing(msg tea.Msg) HelpPanelResult {
	if km, ok := msg.(tea.KeyMsg); ok {
		switch km.String() {
		case "esc":
			// First esc cancels an in-progress search; second backs out to
			// the section list (so the user can pick another).
			if h.bodyEditor.IsSearching() {
				cmd := h.bodyEditor.Update(msg)
				return HelpPanelResult{Cmd: cmd}
			}
			if h.bodyEditor.HasActiveSearch() {
				h.bodyEditor.ClearSearch()
				return HelpPanelResult{}
			}
			h.viewing = false
			return HelpPanelResult{}
		}
	}
	cmd := h.bodyEditor.Update(msg)
	return HelpPanelResult{Cmd: cmd}
}

func (h *HelpPanel) openSection(idx int) {
	if idx < 0 || idx >= len(h.sections) {
		return
	}
	h.activeSection = idx
	h.viewing = true
	// Render the body to plain text + parallel spans. SetValue stores the
	// plain bytes (so search/selection/copy operate on the text the user
	// actually sees), then SetLineStyles applies our styling at render time.
	plain, spans := renderHelpDoc(h.sections[idx].Body)
	h.bodyEditor.SetValue(plain)
	h.bodyEditor.SetLineStyles(spans)
}

// ViewModal renders the modal box. screenWidth/screenHeight are the full
// terminal dimensions.
func (h *HelpPanel) ViewModal(screenWidth, screenHeight int) string {
	modalWidth := min(96, screenWidth-8)
	modalHeight := min(30, screenHeight-6)
	contentWidth := modalWidth - 6
	if contentWidth < 1 {
		contentWidth = 1
	}

	titleText := "Help"
	if h.viewing && h.activeSection >= 0 && h.activeSection < len(h.sections) {
		titleText = "Help: " + h.sections[h.activeSection].Title
	}

	var b strings.Builder
	title := shared.TitleStyle.Render(titleText)
	closeHint := shared.DimStyle.Render("[esc] close")
	gap := max(0, contentWidth-lipgloss.Width(title)-lipgloss.Width(closeHint))
	b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, title,
		strings.Repeat(" ", gap), closeHint))
	b.WriteString("\n")
	b.WriteString(shared.DimStyle.Render(strings.Repeat("─", contentWidth)))
	b.WriteString("\n")

	if h.viewing {
		// Body viewer fills the rest; reserve two rows for the hint line +
		// some breathing room.
		bodyHeight := modalHeight - 5
		if bodyHeight < 5 {
			bodyHeight = 5
		}
		h.bodyEditor.SetSize(contentWidth, bodyHeight)
		// Top-left of the body editor in modal-content coordinates: column 0,
		// row 2 (title + divider). That's the coord space the host sends
		// after translateModalMouse, so set it as the editor's mouse origin
		// so scrollbar press/drag/release resolve correctly.
		h.bodyEditor.SetMouseOrigin(0, 2)
		b.WriteString(h.bodyEditor.View())
		b.WriteString("\n")
		// While a search is being typed, the footer becomes the "/pattern"
		// prompt so the user can see what they're filtering on; otherwise it
		// shows the shortcut hints plus any match/not-found status.
		if h.bodyEditor.IsSearching() {
			b.WriteString(h.bodyEditor.PromptLine())
		} else {
			var bar shared.HintBar
			left := bar.Render(
				[]shared.Hint{
					{Key: "esc", Label: "back"},
					{Key: "/", Label: "search"},
					{Key: "j", Display: "↑/↓", Label: "scroll"},
				},
				0, 0, true, "  ", shared.DimStyle,
			)
			if status := h.bodyEditor.StatusLine(); status != "" {
				gap := max(0, contentWidth-lipgloss.Width(left)-lipgloss.Width(status))
				b.WriteString(left + strings.Repeat(" ", gap) + status)
			} else {
				b.WriteString(left)
			}
		}
	} else {
		b.WriteString(h.renderSectionList(contentWidth, modalHeight-5))
		b.WriteString("\n")
		var bar shared.HintBar
		b.WriteString(bar.Render(
			[]shared.Hint{
				{Key: "enter", Label: "open"},
				{Key: "j", Display: "↑/↓", Label: "move"},
				{Key: "esc", Label: "close"},
			},
			0, 0, true, "  ", shared.DimStyle,
		))
	}

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(shared.ColorPrimary).
		Padding(1, 2).
		Width(modalWidth).
		Render(b.String())
}

// renderSectionList draws the table-of-contents rows and records each row's
// y-position into clickMap so left-clicks resolve to a section index.
func (h *HelpPanel) renderSectionList(contentWidth, rows int) string {
	h.clickMap.Reset()
	if len(h.sections) == 0 {
		return shared.DimStyle.Render("(no help sections yet)")
	}

	// Lay the list out as two aligned columns: the section title and its
	// summary. The title column is sized to the longest title (so nothing is
	// clipped while there's room), and the summary wraps within whatever's
	// left, with continuation lines hanging-indented to the summary column.
	const cursorW = 2 // "> " / "  "
	const gapW = 2    // gap between the title and summary columns

	titleW := 0
	for _, s := range h.sections {
		if w := lipgloss.Width(s.Title); w > titleW {
			titleW = w
		}
	}
	// Never let the title column crowd out the summary; keep at least 20
	// columns for the wrapped summary text.
	if maxTitleW := contentWidth - cursorW - gapW - 20; maxTitleW > 0 && titleW > maxTitleW {
		titleW = maxTitleW
	}
	summaryW := contentWidth - cursorW - titleW - gapW
	if summaryW < 1 {
		summaryW = 1
	}
	indent := strings.Repeat(" ", cursorW+titleW+gapW)

	var b strings.Builder
	// Section rows begin at modal-content row 2 (title row 0, divider 1).
	const firstRowY = 2
	y := firstRowY
	for i, s := range h.sections {
		title := truncateToWidth(s.Title, titleW)
		summaryLines := wrapWords(s.Summary, summaryW)
		if len(summaryLines) == 0 {
			summaryLines = []string{""}
		}

		var line strings.Builder
		if i == h.cursor {
			line.WriteString(shared.CursorStyle.Render("> "))
			line.WriteString(shared.SelectedStyle.Render(padRightS(title, titleW)))
		} else {
			line.WriteString("  ")
			line.WriteString(shared.NormalStyle.Render(padRightS(title, titleW)))
		}
		line.WriteString(strings.Repeat(" ", gapW))
		line.WriteString(shared.DimStyle.Render(summaryLines[0]))
		b.WriteString(line.String())
		b.WriteString("\n")
		// The whole section (title row + any wrapped summary rows) selects it.
		h.clickMap.AddRow(y, i)
		y++

		for _, extra := range summaryLines[1:] {
			b.WriteString(indent)
			b.WriteString(shared.DimStyle.Render(extra))
			b.WriteString("\n")
			h.clickMap.AddRow(y, i)
			y++
		}
	}
	// Pad remaining rows so the hint line sits at the bottom of the modal.
	for printed := y - firstRowY; printed < rows-1; printed++ {
		b.WriteString("\n")
	}
	return b.String()
}

func padRightS(s string, w int) string {
	if lipgloss.Width(s) >= w {
		return s
	}
	return s + strings.Repeat(" ", w-lipgloss.Width(s))
}

// truncateToWidth clips s to at most w display columns, appending an ellipsis
// when it has to cut. Used so a too-long section title degrades gracefully
// instead of breaking column alignment.
func truncateToWidth(s string, w int) string {
	if w <= 0 || lipgloss.Width(s) <= w {
		return s
	}
	if w == 1 {
		return "…"
	}
	r := []rune(s)
	for len(r) > 0 && lipgloss.Width(string(r))+1 > w {
		r = r[:len(r)-1]
	}
	return string(r) + "…"
}

// wrapWords greedily wraps s onto lines no wider than w columns, breaking on
// spaces. A single word longer than w is left intact on its own line rather
// than being split mid-word.
func wrapWords(s string, w int) []string {
	if w <= 0 {
		return []string{s}
	}
	words := strings.Fields(s)
	if len(words) == 0 {
		return nil
	}
	var lines []string
	cur := words[0]
	for _, word := range words[1:] {
		if lipgloss.Width(cur)+1+lipgloss.Width(word) <= w {
			cur += " " + word
		} else {
			lines = append(lines, cur)
			cur = word
		}
	}
	return append(lines, cur)
}

// Suppress unused-import warning for `key`; reserved for keymap integration
// (currently the panel hard-codes key strings).
var _ = key.Binding{}
