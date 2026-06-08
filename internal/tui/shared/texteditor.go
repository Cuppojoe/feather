package shared

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// TextEditor is a lightweight multi-line text editor / viewer whose View
// only renders the visible window — unlike bubbles/textarea, which iterates
// every line of content on every render. It powers both the editable
// request body and the read-only response viewer; the two configurations
// differ only by the ReadOnly flag.
type TextEditor struct {
	lines    []string
	row, col int
	topRow   int
	leftCol  int
	width    int
	height   int
	focused  bool
	dirty    bool
	readOnly bool
	language string // "json", "plain", …

	// maxLineWidth is the widest line in `lines`, measured in display cells
	// (lipgloss.Width). It clamps horizontal scrolling so the read-only
	// viewer can pan within long lines without overshooting blank space.
	// Recomputed whenever `lines` changes (SetValue / edits).
	maxLineWidth int

	// lineStyles holds per-line syntax-highlight spans, parallel to lines.
	// A nil slice (or nil entry) means no highlighting.
	lineStyles [][]Span

	placeholder string
	showLineNos bool
	gutterStyle lipgloss.Style
	cursorStyle lipgloss.Style

	// Blink. blinkTickID invalidates stale ticks (so a Blur+Focus restarts a
	// clean cycle); cursorOn flips each tick when the editor is focused.
	cursorOn    bool
	blinkTickID int

	// Search state.
	searching   bool
	searchDir   int
	searchInput string
	pattern     string
	lastDir     int
	matches     []editorMatch
	current     int
	notFound    bool

	// Scrollbar drag state.
	draggingBar bool

	// Mouse origin. Hosts call SetMouseOrigin with the editor View's
	// top-left position (in the same coordinate space the host's mouse
	// events arrive in) so the editor can translate clicks internally.
	mouseOriginX int
	mouseOriginY int

	// Inline language picker. When open the editor's View replaces its
	// normal output with a searchable list; keyboard input drives the
	// picker until it's closed.
	pickerOpen   bool
	pickerQuery  string
	pickerCursor int
	pickerHits   []string

	// Mouse text selection. selStart is the press position, selEnd is the
	// current/release position. selecting is true while the left button is
	// held during a drag. The visible selection is the range between them,
	// normalised; equal start/end means no selection.
	selStart  editorPos
	selEnd    editorPos
	selecting bool

	// External-editor integration. When externalID is non-empty, ctrl+v
	// in edit mode opens the buffer in $EDITOR (via OpenInEditor) tagged
	// with that ID. When the matching EditExternalMsg comes back, the
	// editor SetValues itself with the returned content. Hosts no longer
	// need to plumb ctrl+v or the return message — they only need to
	// forward the message into the editor's Update.
	externalID  string
	externalExt string
}

// editorPos is a buffer position (row + byte column within that row).
type editorPos struct{ row, col int }

func posLess(a, b editorPos) bool {
	return a.row < b.row || (a.row == b.row && a.col < b.col)
}

type editorMatch struct {
	row        int
	start, end int
}

// EditorBlinkMsg arrives on the blink-cycle timer; we ignore stale ones.
type EditorBlinkMsg struct{ id int }

// blinkInterval is the on/off period for the cursor.
const blinkInterval = 500 * time.Millisecond

// NewTextEditor returns an empty editor sized to width x height.
func NewTextEditor(width, height int) TextEditor {
	return TextEditor{
		lines:       []string{""},
		width:       width,
		height:      height,
		showLineNos: true,
		current:     -1,
		cursorOn:    true,
		language:    "plain",
		gutterStyle: lipgloss.NewStyle().Foreground(ColorMuted),
		cursorStyle: lipgloss.NewStyle().Reverse(true),
	}
}

// --- mode / config --------------------------------------------------------

// SetMouseOrigin records the editor View's top-left position in the host's
// mouse-coordinate space. The editor uses it to translate incoming mouse
// events so press/drag on the scrollbar resolves to the right cell. Hosts
// should call this from their View() right before placing the editor.
func (e *TextEditor) SetMouseOrigin(x, y int) {
	e.mouseOriginX = x
	e.mouseOriginY = y
}

func (e *TextEditor) SetReadOnly(v bool)        { e.readOnly = v }
func (e *TextEditor) ReadOnly() bool            { return e.readOnly }
func (e *TextEditor) SetShowLineNumbers(v bool) { e.showLineNos = v }
func (e *TextEditor) SetPlaceholder(s string)   { e.placeholder = s }

// SetExternalEditorID configures the per-instance identifier used to
// tag $EDITOR open requests so the matching EditExternalMsg comes back
// to this exact editor. Two TextEditor instances active at the same
// time MUST use different IDs.
//
// Pair with SetExternalEditorExt to control the file extension used by
// $EDITOR (so syntax highlighting kicks in). When the ID is empty
// (default), ctrl+v in edit mode is a no-op.
func (e *TextEditor) SetExternalEditorID(id string) { e.externalID = id }

// SetExternalEditorExt configures the extension used when opening the
// buffer in $EDITOR. Include the dot — ".json", ".js", ".txt", etc.
// Defaults to ".txt" when unset.
func (e *TextEditor) SetExternalEditorExt(ext string) { e.externalExt = ext }

func (e *TextEditor) externalEditorExt() string {
	if e.externalExt == "" {
		return ".txt"
	}
	return e.externalExt
}

// SetLanguage sets the syntax-highlight grammar by lexer name (any Chroma
// lexer, e.g. "json", "xml", "yaml"). Empty/unknown names fall back to
// "plain" (no highlighting). The current content is re-highlighted.
func (e *TextEditor) SetLanguage(name string) {
	lang := canonicalLanguage(name)
	if lang == e.language {
		return
	}
	e.language = lang
	e.recomputeHighlight()
}

// recomputeHighlight re-tokenizes the whole document for the current language.
func (e *TextEditor) recomputeHighlight() {
	e.lineStyles = HighlightSpans(e.language, e.Value())
}

// SetLineStyles overrides per-line styling with externally-computed spans.
// Use this when the host wants its own styling (e.g. a custom doc renderer)
// instead of Chroma's. Spans index into the plain text held by e.lines and
// are honoured at render time, while the underlying bytes stay plain so
// search, selection, and copy keep working. The override is preserved until
// the next content mutation (SetValue / SetLanguage / an edit), at which
// point Chroma takes over again.
func (e *TextEditor) SetLineStyles(spans [][]Span) {
	e.lineStyles = spans
}

// afterEdit refreshes search matches and syntax highlighting after a content
// mutation. (HighlightSpans is bounded by a size cap, so this stays cheap.)
func (e *TextEditor) afterEdit() {
	e.recomputeMatches()
	e.recomputeHighlight()
	e.recomputeMaxLineWidth()
}

// stylesForLine returns the syntax spans for a line, or nil.
func (e *TextEditor) stylesForLine(row int) []Span {
	if row >= 0 && row < len(e.lineStyles) {
		return e.lineStyles[row]
	}
	return nil
}

// Language returns the canonical language identifier.
func (e *TextEditor) Language() string {
	if e.language == "" {
		return "plain"
	}
	return e.language
}

// SupportedLanguages lists the languages a host can cycle through. Any Chroma
// lexer name also works via SetLanguage / content detection.
func SupportedLanguages() []string {
	return []string{"plain", "json", "xml", "html", "yaml", "toml", "markdown"}
}

// --- content / sizing ----------------------------------------------------

func (e *TextEditor) SetValue(s string) {
	e.lines = strings.Split(s, "\n")
	if len(e.lines) == 0 {
		e.lines = []string{""}
	}
	e.clampCursor()
	e.current = -1
	e.recomputeMatches()
	e.recomputeHighlight()
	e.recomputeMaxLineWidth()
	e.adjustScroll()
}

// recomputeMaxLineWidth caches the widest line in the buffer (in display
// cells, not bytes — so wide chars and ANSI styling are counted correctly).
// Called whenever lines change.
func (e *TextEditor) recomputeMaxLineWidth() {
	mx := 0
	for _, l := range e.lines {
		if w := lipgloss.Width(l); w > mx {
			mx = w
		}
	}
	e.maxLineWidth = mx
}

func (e *TextEditor) Value() string { return strings.Join(e.lines, "\n") }

func (e *TextEditor) SetSize(width, height int) {
	if width == e.width && height == e.height {
		return
	}
	e.width = width
	e.height = height
	e.adjustScroll()
}

// --- focus / cursor ------------------------------------------------------

func (e *TextEditor) Focus() tea.Cmd {
	if e.focused {
		return nil
	}
	e.focused = true
	e.cursorOn = true
	return e.scheduleBlink()
}

func (e *TextEditor) Blur() {
	e.focused = false
	e.cursorOn = false
	e.blinkTickID++ // invalidate any in-flight tick
}

func (e *TextEditor) Focused() bool      { return e.focused }
func (e *TextEditor) Dirty() bool        { return e.dirty }
func (e *TextEditor) Clean()             { e.dirty = false }
func (e *TextEditor) Cursor() (int, int) { return e.row, e.col }

func (e *TextEditor) scheduleBlink() tea.Cmd {
	if e.readOnly || !e.focused {
		return nil
	}
	id := e.blinkTickID
	return tea.Tick(blinkInterval, func(time.Time) tea.Msg {
		return EditorBlinkMsg{id: id}
	})
}

// --- search ---------------------------------------------------------------

func (e *TextEditor) IsSearching() bool     { return e.searching }
func (e *TextEditor) HasActiveSearch() bool { return e.pattern != "" || e.notFound }

func (e *TextEditor) ClearSearch() bool {
	if !e.searching && !e.HasActiveSearch() {
		return false
	}
	e.searching = false
	e.searchInput = ""
	e.pattern = ""
	e.matches = e.matches[:0]
	e.current = -1
	e.notFound = false
	return true
}

func (e *TextEditor) ScrollPercent() float64 {
	if len(e.lines) <= e.height {
		return 0
	}
	maxTop := len(e.lines) - e.height
	if maxTop <= 0 {
		return 0
	}
	return float64(e.topRow) / float64(maxTop)
}

func (e *TextEditor) PromptLine() string {
	if !e.searching {
		return ""
	}
	prefix := "/"
	if e.searchDir < 0 {
		prefix = "?"
	}
	return SearchPromptStyle.Render(prefix + e.searchInput + "█")
}

func (e *TextEditor) StatusLine() string {
	if e.searching {
		return ""
	}
	if e.notFound {
		return ErrorStyle.Render(fmt.Sprintf("pattern not found: %s", e.pattern))
	}
	if e.pattern != "" && len(e.matches) > 0 && e.current >= 0 {
		return DimStyle.Render(fmt.Sprintf("match %d/%d", e.current+1, len(e.matches)))
	}
	return ""
}

// --- Update ---------------------------------------------------------------

func (e *TextEditor) Update(msg tea.Msg) tea.Cmd {
	// Blink ticks are always processed so the cursor keeps animating even
	// when the editor lost message focus briefly.
	if bm, ok := msg.(EditorBlinkMsg); ok {
		if bm.id != e.blinkTickID || e.readOnly || !e.focused {
			return nil
		}
		e.cursorOn = !e.cursorOn
		return e.scheduleBlink()
	}

	// $EDITOR returned. Match against this instance's external-editor ID
	// (set via SetExternalEditorID) so the right buffer absorbs the
	// content even when multiple editors live in the same tree. Hosts no
	// longer need to write per-panel handlers — they just forward the
	// message through their Update.
	if edit, ok := msg.(EditExternalMsg); ok {
		if e.externalID != "" && edit.Caller == e.externalID {
			if edit.Err == nil && !edit.ReadOnly && !e.readOnly {
				e.SetValue(edit.Content)
			}
		}
		return nil
	}

	// When the language picker is open, it owns all input.
	if e.pickerOpen {
		if km, ok := msg.(tea.KeyMsg); ok {
			e.updateLanguagePicker(km)
		}
		return nil
	}

	// Enter when blurred (and not read-only) focuses the editor. Hosts that
	// forward Enter to a blurred editor get this UX for free; the keypress
	// itself is consumed so it doesn't double as "insert newline".
	if km, ok := msg.(tea.KeyMsg); ok && !e.focused && !e.readOnly && km.String() == "enter" {
		return e.Focus()
	}

	// Left-button press on a blurred, editable editor focuses it AND
	// keeps the message flowing so the press-handler below can lay down
	// selStart. Without this the first click on an unfocused editor was
	// silently swallowed (the early-return on !e.focused fired before
	// the mouse handler), and the user had to click once to focus and
	// click again to start a selection — same-line drags never registered
	// because selStart never got written on the first press.
	if mouse, ok := msg.(tea.MouseMsg); ok &&
		mouse.Button == tea.MouseButtonLeft &&
		mouse.Action == tea.MouseActionPress &&
		!e.focused && !e.readOnly {
		e.Focus()
	}

	if !e.focused {
		return nil
	}

	if mouse, ok := msg.(tea.MouseMsg); ok {
		// Translate to editor-local coordinates via the origin the host
		// recorded with SetMouseOrigin.
		localX := mouse.X - e.mouseOriginX
		localY := mouse.Y - e.mouseOriginY
		switch mouse.Button {
		case tea.MouseButtonWheelUp:
			e.scrollUp(1)
			return nil
		case tea.MouseButtonWheelDown:
			e.scrollDown(1)
			return nil
		case tea.MouseButtonWheelLeft:
			// Terminals that report shift+wheel as a horizontal wheel
			// event get free horizontal panning.
			e.leftCol -= e.horizScrollStep()
			e.adjustScroll()
			return nil
		case tea.MouseButtonWheelRight:
			e.leftCol += e.horizScrollStep()
			e.adjustScroll()
			return nil
		case tea.MouseButtonLeft:
			scrollbarX := e.width - 1
			inBar := len(e.lines) > e.height && localX == scrollbarX &&
				localY >= 0 && localY < e.height
			inText := localY >= 0 && localY < e.height &&
				localX >= e.gutterColEnd() && localX < scrollbarX
			switch mouse.Action {
			case tea.MouseActionPress:
				switch {
				case inBar:
					e.draggingBar = true
					e.scrollToBarRow(localY)
				case inText:
					p := e.posFromClick(localX, localY)
					e.selStart = p
					e.selEnd = p
					e.selecting = true
					e.row, e.col = p.row, p.col
					e.cursorOn = true
				}
			case tea.MouseActionMotion:
				switch {
				case e.draggingBar:
					y := localY
					if y < 0 {
						y = 0
					}
					if y >= e.height {
						y = e.height - 1
					}
					e.scrollToBarRow(y)
				case e.selecting:
					p := e.posFromClick(localX, localY)
					e.selEnd = p
					e.row, e.col = p.row, p.col
					e.adjustScroll()
				}
			case tea.MouseActionRelease:
				e.draggingBar = false
				e.selecting = false
				// No auto-copy: keep the selection visual only. Native
				// terminal selection (Shift+drag) + terminal copy
				// (e.g. Ctrl+Shift+C) still works because those events
				// bypass app mouse capture entirely. Terminal paste
				// (e.g. Ctrl+Shift+V) injects clipboard text as
				// keystrokes that the editor inserts normally.
			}
			return nil
		}
		return nil
	}

	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}

	if e.searching {
		e.updateSearchInput(km)
		return nil
	}

	switch km.String() {
	case "/":
		// Only treat '/' as the search trigger when the editor is a
		// viewer. In edit mode the user is typing prose / code that may
		// contain slashes — fall through so the keystroke inserts.
		if e.readOnly {
			e.beginSearch(1)
			return nil
		}
	case "?":
		if e.readOnly {
			e.beginSearch(-1)
			return nil
		}
	case "ctrl+y":
		// Copy the current selection to the OS clipboard. Works in both
		// view and edit modes; no-op when nothing is selected.
		if e.hasSelection() {
			if text := e.SelectedText(); text != "" {
				_ = CopyToClipboard(text)
			}
		}
		return nil
	case "ctrl+l":
		// Open the inline language picker.
		e.openLanguagePicker()
		return nil
	case "n":
		if e.readOnly {
			e.repeatSearch(e.lastDir)
			return nil
		}
	case "N":
		if e.readOnly {
			e.repeatSearch(-e.lastDir)
			return nil
		}
	}

	if e.readOnly {
		switch km.String() {
		case "up", "k":
			e.scrollUp(1)
		case "down", "j":
			e.scrollDown(1)
		case "pgup":
			e.scrollUp(e.height)
		case "pgdown":
			e.scrollDown(e.height)
		case "home", "g":
			e.topRow = 0
		case "end", "G":
			e.scrollDown(len(e.lines))
		case "shift+left":
			// Horizontal pan. Read-only viewers don't have a cursor to
			// drive leftCol, so they need explicit shift+arrow keys to
			// see lines wider than the viewport.
			e.leftCol -= e.horizScrollStep()
			e.adjustScroll()
		case "shift+right":
			e.leftCol += e.horizScrollStep()
			e.adjustScroll()
		case "shift+home":
			// Jump to the leftmost column.
			e.leftCol = 0
			e.adjustScroll()
		case "shift+end":
			// Jump as far right as the widest line allows; adjustScroll
			// clamps to maxLineWidth - textWidth.
			e.leftCol = e.maxLineWidth
			e.adjustScroll()
		}
		return nil
	}

	// Translate shifted-variant cursor moves onto their base names so the
	// switch below only needs one case per motion. shifted=true marks the
	// keystroke as a selection-extending move, which gates anchoring and
	// extending around the existing cursor logic.
	baseKey, shifted := normalizeShiftedMove(km.String())

	switch {
	case shifted:
		// First shifted move from a clean cursor anchors the selection at
		// the current position. Subsequent shifted moves keep extending
		// from the same anchor — selStart stays put while selEnd tracks
		// the cursor.
		if !e.hasSelection() {
			e.selStart = editorPos{row: e.row, col: e.col}
		}
	case isCursorMoveKey(baseKey):
		// Unshifted cursor motion drops the selection before moving so
		// the highlight doesn't linger out behind a wandering caret.
		e.clearSelection()
	}

	// Read-write editing.
	switch baseKey {
	case "esc":
		// Exit edit mode. The buffer keeps its current content — esc is
		// "stop editing", not "discard changes". Hosts that want a
		// discard-changes flow can call MarkSaved on a checkpoint and
		// implement revert separately.
		e.Blur()
		return nil
	case "ctrl+v":
		// Hand the buffer to $EDITOR when the host has configured an
		// external-editor ID. The matching EditExternalMsg comes back
		// through Update at the top of this function and SetValues the
		// buffer automatically — no per-host plumbing required.
		if e.externalID == "" {
			return nil
		}
		return OpenInEditor(e.externalID, e.Value(), e.externalEditorExt(), false)
	case "left":
		e.moveLeft()
	case "right":
		e.moveRight()
	case "ctrl+left":
		e.moveWordLeft()
	case "ctrl+right":
		e.moveWordRight()
	case "up":
		e.moveUp()
	case "down":
		e.moveDown()
	case "home", "ctrl+a":
		e.col = 0
	case "end", "ctrl+e":
		e.col = len(e.lines[e.row])
	case "pgup":
		for i := 0; i < e.height; i++ {
			e.moveUp()
		}
	case "pgdown":
		for i := 0; i < e.height; i++ {
			e.moveDown()
		}
	case "backspace":
		// Editing keys also drop any active selection before mutating.
		// We don't yet implement selection-aware editing (delete the
		// selection on type/backspace) — clear so the highlight doesn't
		// outlive the operation.
		e.clearSelection()
		e.deleteBackward()
		e.afterEdit()
	case "delete":
		e.clearSelection()
		e.deleteForward()
		e.afterEdit()
	case "enter", "ctrl+j":
		// Treat ctrl+j (the bare LF byte) the same as enter so that
		// non-bracketed pastes from terminals that send LF for newlines
		// keep their line breaks instead of silently swallowing them.
		e.clearSelection()
		e.insertNewline()
		e.afterEdit()
	case "tab":
		e.clearSelection()
		e.insertString("  ")
		e.afterEdit()
	default:
		if len(km.Runes) > 0 {
			e.clearSelection()
			s := string(km.Runes)
			// Pastes — even bracketed ones — arrive as a single KeyMsg
			// whose Runes contains embedded \n / \r\n. The e.lines slice
			// stores one entry per logical line and assumes no embedded
			// newlines, so split the input and lay each segment down on
			// its own row.
			if strings.ContainsAny(s, "\r\n") {
				e.insertMultiline(s)
			} else {
				e.insertString(s)
			}
			e.afterEdit()
		}
	}

	// Sync the floating end of the selection to wherever the cursor
	// landed. Doing this after the switch covers every motion uniformly
	// — plain arrows, ctrl+arrow word-jumps, home/end, page keys, and any
	// future cursor key added to isCursorMoveKey.
	if shifted {
		e.selEnd = editorPos{row: e.row, col: e.col}
	}
	e.cursorOn = true // keystrokes reset the blink cycle to "on"
	e.adjustScroll()
	return nil
}

// --- View -----------------------------------------------------------------

// View renders the visible window plus a 1-cell vertical scrollbar on the
// right edge. JSON highlighting and search-match overlay are applied per
// visible line.
func (e *TextEditor) View() string {
	rows := e.height
	if rows < 1 {
		return ""
	}
	if e.pickerOpen {
		return e.renderPicker()
	}
	gutterWidth := 0
	if e.showLineNos {
		gutterWidth = len(itoa(len(e.lines))) + 2
	}
	// Only reserve a scrollbar column when content actually overflows the
	// viewport — otherwise we draw a phantom thumb and waste a column.
	scrollbarCol := 0
	if len(e.lines) > e.height {
		scrollbarCol = 1
	}
	textWidth := e.width - gutterWidth - scrollbarCol
	if textWidth < 1 {
		textWidth = 1
	}

	if e.placeholderActive() {
		var b strings.Builder
		if e.showLineNos {
			b.WriteString(e.gutterStyle.Render(padLeft(itoa(1), gutterWidth-1) + " "))
		}
		b.WriteString(e.gutterStyle.Render(e.placeholder))
		for i := 1; i < rows; i++ {
			b.WriteRune('\n')
		}
		return b.String()
	}

	currentLine, currentStart := -1, -1
	if e.current >= 0 && e.current < len(e.matches) {
		currentLine = e.matches[e.current].row
		currentStart = e.matches[e.current].start
	}

	thumbStart, thumbEnd := e.scrollbarRange()

	var b strings.Builder
	for i := 0; i < rows; i++ {
		lineIdx := e.topRow + i
		showLine := lineIdx < len(e.lines)

		if e.showLineNos {
			if showLine {
				b.WriteString(e.gutterStyle.Render(padLeft(itoa(lineIdx+1), gutterWidth-1) + " "))
			} else {
				b.WriteString(strings.Repeat(" ", gutterWidth))
			}
		}

		if showLine {
			line := e.lines[lineIdx]
			// Pre-styled content (e.g. a rendered lipgloss table) has more
			// bytes than display cells. Byte-slicing would cut mid-escape
			// and lose the right portion entirely; route those lines
			// through ansi.Cut and skip the search/selection/highlight
			// pipeline (which assumes plain-text byte offsets).
			if lipgloss.Width(line) != len(line) {
				styled := ansi.Cut(line, e.leftCol, e.leftCol+textWidth)
				b.WriteString(styled)
				if pad := textWidth - lipgloss.Width(styled); pad > 0 {
					b.WriteString(strings.Repeat(" ", pad))
				}
			} else {
				visibleStart := e.leftCol
				if visibleStart > len(line) {
					visibleStart = len(line)
				}
				visible := line[visibleStart:]
				if len(visible) > textWidth {
					visible = visible[:textWidth]
				}
				var styled string
				if !e.readOnly && e.focused && e.cursorOn && lineIdx == e.row {
					// Combined path: selection + cursor in one render so
					// the line that carries the caret keeps its selection
					// highlight (the failure mode users saw on the first
					// or last line of any selection — and on every cell
					// of a same-line drag).
					styled = e.renderLineWithCursor(visible, lineIdx, visibleStart, currentLine, currentStart)
				} else {
					styled = e.renderLineWithSelection(visible, lineIdx, visibleStart, currentLine, currentStart)
				}
				b.WriteString(styled)
				if pad := textWidth - lipgloss.Width(styled); pad > 0 {
					b.WriteString(strings.Repeat(" ", pad))
				}
			}
		} else {
			b.WriteString(strings.Repeat(" ", textWidth))
		}

		// Scrollbar column (rendered only when content overflows).
		if scrollbarCol > 0 {
			b.WriteString(e.scrollbarCell(i, thumbStart, thumbEnd))
		}

		b.WriteRune('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

// Footer renders a 1-line summary suitable for placement under View(). It
// contains the editor's own shortcuts plus any extra hints the host wants
// to surface. The right side carries language + scroll % (or search status).
func (e *TextEditor) Footer(extras []Hint, width int) string {
	if e.searching {
		return e.PromptLine()
	}

	hints := e.builtinHints()
	hints = append(hints, extras...)

	var bar HintBar
	left := bar.Render(hints, 0, 0, true, "  ", DimStyle)

	rightParts := []string{}
	if s := e.StatusLine(); s != "" {
		rightParts = append(rightParts, s)
	}
	rightParts = append(rightParts,
		DimStyle.Render(strings.ToUpper(e.Language())),
		DimStyle.Render(fmt.Sprintf("%3.f%%", e.ScrollPercent()*100)),
	)
	right := strings.Join(rightParts, "  ")

	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
}

func (e *TextEditor) builtinHints() []Hint {
	if e.readOnly {
		return []Hint{
			{Key: "/", Label: "search"},
			{Key: "ctrl+y", Label: "copy"},
		}
	}
	return []Hint{
		{Key: "esc", Label: "exit"},
		{Key: "ctrl+v", Label: "external editor"},
		{Key: "ctrl+y", Label: "copy"},
		{Key: "ctrl+l", Label: "language"},
	}
}

// --- inline language picker ----------------------------------------------

// PickerOpen reports whether the inline language picker is active.
func (e *TextEditor) PickerOpen() bool { return e.pickerOpen }

func (e *TextEditor) openLanguagePicker() {
	e.pickerOpen = true
	e.pickerQuery = ""
	e.pickerCursor = 0
	e.filterLanguages()
}

func (e *TextEditor) closeLanguagePicker() {
	e.pickerOpen = false
	e.pickerQuery = ""
	e.pickerCursor = 0
	e.pickerHits = nil
}

func (e *TextEditor) updateLanguagePicker(km tea.KeyMsg) {
	switch km.String() {
	case "esc":
		e.closeLanguagePicker()
	case "enter":
		if len(e.pickerHits) > 0 && e.pickerCursor >= 0 && e.pickerCursor < len(e.pickerHits) {
			e.SetLanguage(e.pickerHits[e.pickerCursor])
		}
		e.closeLanguagePicker()
	case "up":
		if e.pickerCursor > 0 {
			e.pickerCursor--
		}
	case "down":
		if e.pickerCursor < len(e.pickerHits)-1 {
			e.pickerCursor++
		}
	case "pgup":
		e.pickerCursor -= 10
		if e.pickerCursor < 0 {
			e.pickerCursor = 0
		}
	case "pgdown":
		e.pickerCursor += 10
		if e.pickerCursor >= len(e.pickerHits) {
			e.pickerCursor = len(e.pickerHits) - 1
		}
		if e.pickerCursor < 0 {
			e.pickerCursor = 0
		}
	case "backspace":
		if r := []rune(e.pickerQuery); len(r) > 0 {
			e.pickerQuery = string(r[:len(r)-1])
			e.filterLanguages()
		}
	default:
		if r := km.Runes; len(r) > 0 {
			e.pickerQuery += string(r)
			e.filterLanguages()
		}
	}
}

// filterLanguages caches the substring-filtered list and re-clamps the cursor.
func (e *TextEditor) filterLanguages() {
	q := strings.ToLower(strings.TrimSpace(e.pickerQuery))
	all := AllLanguageNames()
	if q == "" {
		e.pickerHits = all
	} else {
		hits := e.pickerHits[:0]
		for _, name := range all {
			if strings.Contains(strings.ToLower(name), q) {
				hits = append(hits, name)
			}
		}
		e.pickerHits = hits
	}
	if e.pickerCursor >= len(e.pickerHits) {
		e.pickerCursor = len(e.pickerHits) - 1
	}
	if e.pickerCursor < 0 {
		e.pickerCursor = 0
	}
}

// renderPicker returns the picker UI rendered into the editor's content area.
func (e *TextEditor) renderPicker() string {
	rows := e.height
	if rows < 3 {
		rows = 3
	}
	var b strings.Builder

	title := SelectedStyle.Render("Choose syntax-highlight language")
	b.WriteString(title)
	b.WriteRune('\n')

	prompt := SearchPromptStyle.Render("/" + e.pickerQuery + "█")
	b.WriteString(prompt)
	b.WriteRune('\n')

	listRows := rows - 3 // title + prompt + footer hint
	if listRows < 1 {
		listRows = 1
	}

	start := 0
	if e.pickerCursor >= listRows {
		start = e.pickerCursor - listRows + 1
	}

	for i := 0; i < listRows; i++ {
		idx := start + i
		if idx >= len(e.pickerHits) {
			b.WriteRune('\n')
			continue
		}
		name := e.pickerHits[idx]
		cursor := "  "
		style := NormalStyle
		if idx == e.pickerCursor {
			cursor = CursorStyle.Render("> ")
			style = SelectedStyle
		}
		current := ""
		if name == e.language {
			current = "  " + DimStyle.Render("(current)")
		}
		b.WriteString(cursor + style.Render(name) + current)
		b.WriteRune('\n')
	}

	hint := DimStyle.Render("[↑/↓] move  [enter] select  [esc] cancel  " +
		fmt.Sprintf("%d/%d", e.pickerCursor+1, max(len(e.pickerHits), 1)))
	b.WriteString(hint)

	return b.String()
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// scrollbarRange returns [start, end) row indices for the thumb within the
// viewport. Both are zero if no scrolling is possible.
func (e *TextEditor) scrollbarRange() (int, int) {
	total := len(e.lines)
	if total <= e.height {
		return 0, e.height
	}
	thumbH := e.height * e.height / total
	if thumbH < 1 {
		thumbH = 1
	}
	maxTop := total - e.height
	start := 0
	if maxTop > 0 {
		// Round to the nearest row rather than flooring, otherwise a short
		// (e.g. 1-cell) thumb renders a row too high relative to the drag.
		start = (e.topRow*(e.height-thumbH) + maxTop/2) / maxTop
	}
	end := start + thumbH
	if end > e.height {
		end = e.height
	}
	return start, end
}

func (e *TextEditor) scrollbarCell(row, thumbStart, thumbEnd int) string {
	if row >= thumbStart && row < thumbEnd {
		return lipgloss.NewStyle().Foreground(ColorPrimary).Render("█")
	}
	return e.gutterStyle.Render("│")
}

// renderLine overlays search matches and syntax highlighting on the visible
// slice of a line. Search matches take precedence over syntax spans, which
// take precedence over plain text.
func (e *TextEditor) renderLine(visible string, lineIdx, visibleStart int, currentLine, currentStart int) string {
	type seg struct {
		text    string
		start   int // byte offset within visible
		isMatch bool
		isCur   bool
	}
	var segs []seg
	if e.pattern != "" {
		cursor := 0
		for _, m := range e.matches {
			if m.row != lineIdx {
				continue
			}
			ms := m.start - visibleStart
			me := m.end - visibleStart
			if me <= 0 || ms >= len(visible) {
				continue
			}
			if ms < 0 {
				ms = 0
			}
			if me > len(visible) {
				me = len(visible)
			}
			if ms > cursor {
				segs = append(segs, seg{text: visible[cursor:ms], start: cursor})
			}
			segs = append(segs, seg{
				text:    visible[ms:me],
				start:   ms,
				isMatch: true,
				isCur:   lineIdx == currentLine && m.start == currentStart,
			})
			cursor = me
		}
		if cursor < len(visible) {
			segs = append(segs, seg{text: visible[cursor:], start: cursor})
		}
	} else {
		segs = []seg{{text: visible, start: 0}}
	}

	spans := e.stylesForLine(lineIdx)
	var b strings.Builder
	for _, s := range segs {
		switch {
		case s.isMatch && s.isCur:
			b.WriteString(SearchCurrentStyle.Render(s.text))
		case s.isMatch:
			b.WriteString(SearchMatchStyle.Render(s.text))
		default:
			// Apply syntax spans to non-match text; offsets are absolute.
			b.WriteString(applySpans(s.text, visibleStart+s.start, spans))
		}
	}
	return b.String()
}

// applySpans styles a plain-text run (whose first byte is at absolute line
// offset absStart) according to the line's syntax spans. Text not covered by
// any span is emitted unstyled.
func applySpans(text string, absStart int, spans []Span) string {
	n := len(text)
	if n == 0 || len(spans) == 0 {
		return text
	}
	type sr struct {
		s, e int
		st   lipgloss.Style
	}
	bset := map[int]struct{}{0: {}, n: {}}
	var rs []sr
	for _, sp := range spans {
		s := sp.Start - absStart
		en := sp.End - absStart
		if en <= 0 || s >= n {
			continue
		}
		if s < 0 {
			s = 0
		}
		if en > n {
			en = n
		}
		rs = append(rs, sr{s, en, sp.Style})
		bset[s] = struct{}{}
		bset[en] = struct{}{}
	}
	if len(rs) == 0 {
		return text
	}
	bounds := make([]int, 0, len(bset))
	for x := range bset {
		bounds = append(bounds, x)
	}
	sort.Ints(bounds)

	var b strings.Builder
	for i := 0; i+1 < len(bounds); i++ {
		a, c := bounds[i], bounds[i+1]
		piece := text[a:c]
		styled := false
		for _, r := range rs {
			if r.s <= a && a < r.e {
				b.WriteString(r.st.Render(piece))
				styled = true
				break
			}
		}
		if !styled {
			b.WriteString(piece)
		}
	}
	return b.String()
}

// renderLineWithCursor renders the current cursor line so that syntax
// highlighting, search highlights, the selection background AND the
// caret cell all coexist. Splits the visible portion into [before-cursor,
// cursor-cell, after-cursor] and feeds the two outer slices back through
// renderLineWithSelection so any selection that overlaps those ranges is
// styled normally. The cursor cell itself takes the caret style — even
// when it falls inside the selection — so the user can still see where
// they are in the buffer.
func (e *TextEditor) renderLineWithCursor(visible string, lineIdx, visibleStart, currentLine, currentStart int) string {
	cursorCol := e.col - visibleStart
	// Off-screen cursor: fall back to plain selection rendering.
	if cursorCol < 0 || cursorCol > len(visible) {
		return e.renderLineWithSelection(visible, lineIdx, visibleStart, currentLine, currentStart)
	}

	before := visible[:cursorCol]
	var at, after string
	hasAfter := cursorCol < len(visible)
	if hasAfter {
		at = string(visible[cursorCol])
		after = visible[cursorCol+1:]
	} else {
		at = " "
	}

	var b strings.Builder
	b.WriteString(e.renderLineWithSelection(before, lineIdx, visibleStart, currentLine, currentStart))
	b.WriteString(e.cursorStyle.Render(at))
	if hasAfter {
		b.WriteString(e.renderLineWithSelection(after, lineIdx, visibleStart+cursorCol+1, currentLine, currentStart))
	}
	return b.String()
}

func (e *TextEditor) overlayCursor(visible string, visibleStart int) string {
	col := e.col - visibleStart
	if col < 0 || col > len(visible) {
		return visible
	}
	spans := e.stylesForLine(e.row)
	before := visible[:col]
	var at, after string
	hasAfter := col < len(visible)
	if hasAfter {
		at = string(visible[col])
		after = visible[col+1:]
	} else {
		at = " "
	}
	var b strings.Builder
	b.WriteString(applySpans(before, visibleStart, spans))
	b.WriteString(e.cursorStyle.Render(at))
	if hasAfter {
		b.WriteString(applySpans(after, visibleStart+col+1, spans))
	}
	return b.String()
}

// --- search internals -----------------------------------------------------

func (e *TextEditor) beginSearch(dir int) {
	e.searching = true
	e.searchDir = dir
	e.searchInput = ""
	e.notFound = false
}

func (e *TextEditor) updateSearchInput(km tea.KeyMsg) {
	switch km.String() {
	case "esc":
		e.searching = false
		e.searchInput = ""
	case "enter":
		e.commitSearch()
	case "backspace":
		if r := []rune(e.searchInput); len(r) > 0 {
			e.searchInput = string(r[:len(r)-1])
		}
	default:
		if r := km.Runes; len(r) == 1 {
			e.searchInput += string(r)
		}
	}
}

func (e *TextEditor) commitSearch() {
	e.searching = false
	dir := e.searchDir
	if e.searchInput != "" && e.searchInput != e.pattern {
		e.pattern = e.searchInput
		e.current = -1
		e.recomputeMatches()
	}
	e.searchInput = ""
	if e.pattern == "" {
		return
	}
	e.lastDir = dir
	e.advance(dir)
}

func (e *TextEditor) repeatSearch(dir int) {
	if e.pattern == "" {
		return
	}
	if dir == 0 {
		dir = 1
	}
	e.advance(dir)
}

func (e *TextEditor) advance(dir int) {
	if len(e.matches) == 0 {
		e.notFound = true
		e.current = -1
		return
	}
	e.notFound = false
	if e.current >= 0 {
		e.current = (e.current + dir + len(e.matches)) % len(e.matches)
	} else if dir > 0 {
		idx := 0
		for i, m := range e.matches {
			if m.row >= e.topRow {
				idx = i
				break
			}
		}
		e.current = idx
	} else {
		bottom := e.topRow + e.height - 1
		idx := len(e.matches) - 1
		for i := len(e.matches) - 1; i >= 0; i-- {
			if e.matches[i].row <= bottom {
				idx = i
				break
			}
		}
		e.current = idx
	}
	if e.current >= 0 && e.current < len(e.matches) {
		e.topRow = e.matches[e.current].row
		if e.topRow < 0 {
			e.topRow = 0
		}
		maxTop := len(e.lines) - e.height
		if maxTop < 0 {
			maxTop = 0
		}
		if e.topRow > maxTop {
			e.topRow = maxTop
		}
	}
}

func (e *TextEditor) recomputeMatches() {
	e.matches = e.matches[:0]
	if e.pattern == "" {
		return
	}
	pat := e.pattern
	fold := pat == strings.ToLower(pat)
	for li, line := range e.lines {
		hay := line
		if fold {
			hay = strings.ToLower(line)
		}
		from := 0
		for {
			idx := strings.Index(hay[from:], pat)
			if idx < 0 {
				break
			}
			start := from + idx
			end := start + len(pat)
			e.matches = append(e.matches, editorMatch{row: li, start: start, end: end})
			from = end
		}
	}
}

// --- buffer / cursor helpers ----------------------------------------------

func (e *TextEditor) placeholderActive() bool {
	return !e.focused && len(e.lines) == 1 && e.lines[0] == "" && e.placeholder != ""
}

func (e *TextEditor) clampCursor() {
	if e.row >= len(e.lines) {
		e.row = len(e.lines) - 1
	}
	if e.row < 0 {
		e.row = 0
	}
	if e.col > len(e.lines[e.row]) {
		e.col = len(e.lines[e.row])
	}
	if e.col < 0 {
		e.col = 0
	}
}

func (e *TextEditor) adjustScroll() {
	rows := e.height
	if rows < 1 {
		return
	}
	if e.row < e.topRow {
		e.topRow = e.row
	}
	if e.row >= e.topRow+rows {
		e.topRow = e.row - rows + 1
	}
	maxTop := len(e.lines) - rows
	if maxTop < 0 {
		maxTop = 0
	}
	if e.topRow > maxTop {
		e.topRow = maxTop
	}
	if e.topRow < 0 {
		e.topRow = 0
	}

	gutter := 0
	if e.showLineNos {
		gutter = len(itoa(len(e.lines))) + 2
	}
	textWidth := e.width - gutter - 1 /* scrollbar */
	if textWidth < 1 {
		textWidth = 1
	}
	// Cursor-driven horizontal scroll. Skip in read-only mode — there's no
	// cursor moving across the buffer, so this branch would just snap
	// leftCol back to e.col (which stays at 0) the instant shift+right or
	// the wheel tried to advance it.
	if !e.readOnly {
		if e.col < e.leftCol {
			e.leftCol = e.col
		}
		if e.col >= e.leftCol+textWidth {
			e.leftCol = e.col - textWidth + 1
		}
	}
	// Upper clamp: don't scroll past the widest line. Without this we'd
	// pan into blank space on the right.
	if e.maxLineWidth > textWidth {
		if maxLeft := e.maxLineWidth - textWidth; e.leftCol > maxLeft {
			e.leftCol = maxLeft
		}
	} else {
		e.leftCol = 0
	}
	if e.leftCol < 0 {
		e.leftCol = 0
	}
}

// horizScrollStep returns how many display cells one shift-arrow press or
// horizontal-wheel tick should pan the viewport. Half-screen jumps mean very
// long lines (10k+ chars, e.g. raw HTML in the Console) don't take hundreds
// of keystrokes to traverse, while smaller widths still get a sensible step.
func (e *TextEditor) horizScrollStep() int {
	w := e.width - e.gutterColEnd() - 1 // text region minus scrollbar
	if w < 8 {
		return 4
	}
	return w / 2
}

// --- mouse / selection geometry -------------------------------------------

func (e *TextEditor) gutterColEnd() int {
	if !e.showLineNos {
		return 0
	}
	return len(itoa(len(e.lines))) + 2
}

// posFromClick translates editor-local (x, y) to a buffer position, accounting
// for the gutter and horizontal/vertical scroll. Clamped to a valid (row, col).
func (e *TextEditor) posFromClick(localX, localY int) editorPos {
	row := e.topRow + localY
	if row < 0 {
		row = 0
	}
	if row >= len(e.lines) {
		row = len(e.lines) - 1
	}
	if row < 0 {
		row = 0
	}
	col := e.leftCol + localX - e.gutterColEnd()
	if col < 0 {
		col = 0
	}
	if row < len(e.lines) && col > len(e.lines[row]) {
		col = len(e.lines[row])
	}
	return editorPos{row: row, col: col}
}

func (e *TextEditor) hasSelection() bool {
	return e.selStart != e.selEnd
}

// SelectedText returns the currently-selected text (in buffer order), or
// "" when nothing is selected. Multi-line selections include the
// intermediate newlines.
func (e *TextEditor) SelectedText() string {
	if !e.hasSelection() {
		return ""
	}
	a, b := e.normalizedSelection()
	if a.row == b.row {
		line := e.lines[a.row]
		if a.col >= len(line) {
			return ""
		}
		end := b.col
		if end > len(line) {
			end = len(line)
		}
		return line[a.col:end]
	}
	var sb strings.Builder
	first := e.lines[a.row]
	if a.col <= len(first) {
		sb.WriteString(first[a.col:])
	}
	sb.WriteByte('\n')
	for i := a.row + 1; i < b.row; i++ {
		sb.WriteString(e.lines[i])
		sb.WriteByte('\n')
	}
	last := e.lines[b.row]
	end := b.col
	if end > len(last) {
		end = len(last)
	}
	sb.WriteString(last[:end])
	return sb.String()
}

func (e *TextEditor) clearSelection() {
	e.selStart = editorPos{}
	e.selEnd = editorPos{}
	e.selecting = false
}

func (e *TextEditor) normalizedSelection() (editorPos, editorPos) {
	if posLess(e.selEnd, e.selStart) {
		return e.selEnd, e.selStart
	}
	return e.selStart, e.selEnd
}

// selectionForVisibleLine returns the [start, end) byte range of the selection
// on lineIdx, clamped to the visible window [visibleStart, visibleStart+visibleLen).
func (e *TextEditor) selectionForVisibleLine(lineIdx, visibleStart, visibleLen int) (int, int, bool) {
	if !e.hasSelection() {
		return 0, 0, false
	}
	a, b := e.normalizedSelection()
	if lineIdx < a.row || lineIdx > b.row {
		return 0, 0, false
	}
	startCol := 0
	endCol := len(e.lines[lineIdx])
	if lineIdx == a.row {
		startCol = a.col
	}
	if lineIdx == b.row {
		endCol = b.col
	}
	s := startCol - visibleStart
	en := endCol - visibleStart
	if en <= 0 || s >= visibleLen {
		return 0, 0, false
	}
	if s < 0 {
		s = 0
	}
	if en > visibleLen {
		en = visibleLen
	}
	if s >= en {
		return 0, 0, false
	}
	return s, en, true
}

// renderLineWithSelection layers a selection overlay over renderLine. The
// selected slice is rendered with SelectionStyle (no other overlays); the
// remaining text goes through the normal search/highlight pipeline.
func (e *TextEditor) renderLineWithSelection(visible string, lineIdx, visibleStart int, currentLine, currentStart int) string {
	s, en, has := e.selectionForVisibleLine(lineIdx, visibleStart, len(visible))
	if !has {
		return e.renderLine(visible, lineIdx, visibleStart, currentLine, currentStart)
	}
	var b strings.Builder
	if s > 0 {
		b.WriteString(e.renderLine(visible[:s], lineIdx, visibleStart, currentLine, currentStart))
	}
	b.WriteString(SelectionStyle.Render(visible[s:en]))
	if en < len(visible) {
		b.WriteString(e.renderLine(visible[en:], lineIdx, visibleStart+en, currentLine, currentStart))
	}
	return b.String()
}

// scrollToBarRow maps a click on row `barY` of the scrollbar (editor-local,
// in [0, height)) to a topRow that puts that row's position into the
// document. Linear mapping: bar row 0 → top of document, last bar row →
// last possible top.
func (e *TextEditor) scrollToBarRow(barY int) {
	maxTop := len(e.lines) - e.height
	if maxTop <= 0 || e.height <= 1 {
		return
	}
	if barY < 0 {
		barY = 0
	}
	if barY >= e.height {
		barY = e.height - 1
	}
	e.topRow = barY * maxTop / (e.height - 1)
	if e.topRow < 0 {
		e.topRow = 0
	}
	if e.topRow > maxTop {
		e.topRow = maxTop
	}
}

func (e *TextEditor) scrollUp(n int) {
	e.topRow -= n
	if e.topRow < 0 {
		e.topRow = 0
	}
}

func (e *TextEditor) scrollDown(n int) {
	max := len(e.lines) - e.height
	if max < 0 {
		max = 0
	}
	e.topRow += n
	if e.topRow > max {
		e.topRow = max
	}
}

func (e *TextEditor) moveLeft() {
	if e.col > 0 {
		e.col--
		return
	}
	if e.row > 0 {
		e.row--
		e.col = len(e.lines[e.row])
	}
}

func (e *TextEditor) moveRight() {
	if e.col < len(e.lines[e.row]) {
		e.col++
		return
	}
	if e.row < len(e.lines)-1 {
		e.row++
		e.col = 0
	}
}

// normalizeShiftedMove maps a "shift+<motion>" key string onto its base
// motion name plus a `shifted=true` flag, used to anchor and extend the
// selection around a single, uniform cursor-move switch. Unmatched keys
// pass through with shifted=false.
//
// bubbletea normalizes modifier ordering as "shift+ctrl+left"; the
// "ctrl+shift+left" form is accepted defensively in case that ever shifts.
func normalizeShiftedMove(s string) (base string, shifted bool) {
	switch s {
	case "shift+left":
		return "left", true
	case "shift+right":
		return "right", true
	case "shift+ctrl+left", "ctrl+shift+left":
		return "ctrl+left", true
	case "shift+ctrl+right", "ctrl+shift+right":
		return "ctrl+right", true
	case "shift+up":
		return "up", true
	case "shift+down":
		return "down", true
	case "shift+home":
		return "home", true
	case "shift+end":
		return "end", true
	case "shift+pgup":
		return "pgup", true
	case "shift+pgdown":
		return "pgdown", true
	}
	return s, false
}

// isCursorMoveKey reports whether the (already-normalized) key string
// would move the cursor with no other side effects. Used so unshifted
// motions drop any active selection before they fire.
func isCursorMoveKey(s string) bool {
	switch s {
	case "left", "right", "ctrl+left", "ctrl+right",
		"up", "down",
		"home", "ctrl+a", "end", "ctrl+e",
		"pgup", "pgdown":
		return true
	}
	return false
}

// isWordByte reports whether b is part of a lexical word for ctrl+arrow
// navigation: ASCII letter, digit, or underscore — matches what most
// editors treat as a "word" (identifiers). Non-ASCII bytes are treated as
// word continuations so identifiers that include UTF-8 letters stay
// contiguous under byte-based iteration.
func isWordByte(b byte) bool {
	switch {
	case b == '_':
		return true
	case b >= '0' && b <= '9':
		return true
	case b >= 'A' && b <= 'Z':
		return true
	case b >= 'a' && b <= 'z':
		return true
	case b >= 0x80:
		return true
	}
	return false
}

// moveWordRight jumps the cursor to the start of the next word. If the
// cursor is inside a word it skips past the rest of that word; then it
// skips any whitespace/punctuation. Wraps to the next line on overshoot.
func (e *TextEditor) moveWordRight() {
	line := e.lines[e.row]
	n := len(line)
	pos := e.col

	// At end of line — jump to the start of the next non-empty word, which
	// in the simplest case is just column 0 of the next line.
	if pos >= n {
		if e.row < len(e.lines)-1 {
			e.row++
			e.col = 0
		}
		return
	}

	// Skip the rest of the current word, if any.
	if isWordByte(line[pos]) {
		for pos < n && isWordByte(line[pos]) {
			pos++
		}
	}
	// Skip over the gap to land on the start of the next word.
	for pos < n && !isWordByte(line[pos]) {
		pos++
	}

	// Hit end of line without finding a next word — wrap so a second press
	// continues the motion onto the next line.
	if pos >= n && e.row < len(e.lines)-1 {
		e.row++
		e.col = 0
		return
	}
	e.col = pos
}

// moveWordLeft jumps the cursor to the start of the previous word.
// Mirrors moveWordRight: skip backwards through any gap, then back to the
// start of the word the cursor is now inside. Wraps to the previous line
// when called at column 0.
func (e *TextEditor) moveWordLeft() {
	if e.col == 0 {
		if e.row > 0 {
			e.row--
			e.col = len(e.lines[e.row])
		}
		return
	}

	line := e.lines[e.row]
	pos := e.col - 1
	// Step back over any gap (whitespace, punctuation) preceding the word.
	for pos > 0 && !isWordByte(line[pos]) {
		pos--
	}
	// Walk to the start of this word — keep going while the byte to our
	// left is still part of the same word.
	for pos > 0 && isWordByte(line[pos-1]) {
		pos--
	}
	e.col = pos
}

func (e *TextEditor) moveUp() {
	if e.row > 0 {
		e.row--
		if e.col > len(e.lines[e.row]) {
			e.col = len(e.lines[e.row])
		}
	}
}

func (e *TextEditor) moveDown() {
	if e.row < len(e.lines)-1 {
		e.row++
		if e.col > len(e.lines[e.row]) {
			e.col = len(e.lines[e.row])
		}
	}
}

func (e *TextEditor) insertString(s string) {
	line := e.lines[e.row]
	e.lines[e.row] = line[:e.col] + s + line[e.col:]
	e.col += len(s)
	e.dirty = true
}

func (e *TextEditor) insertNewline() {
	line := e.lines[e.row]
	left := line[:e.col]
	right := line[e.col:]
	e.lines[e.row] = left
	e.lines = append(e.lines, "")
	copy(e.lines[e.row+2:], e.lines[e.row+1:])
	e.lines[e.row+1] = right
	e.row++
	e.col = 0
	e.dirty = true
}

// insertMultiline inserts a string that may contain newlines, splitting
// it across as many lines as necessary so the e.lines invariant (one
// logical line per slice entry, no embedded \n) holds. Used for pastes —
// both bracketed pastes from bubbletea and any default-case rune blob
// that happens to contain a newline.
//
// CRLF and bare-CR are normalised to LF before splitting so Windows /
// classic-Mac clipboards line up with the rest of the buffer.
func (e *TextEditor) insertMultiline(s string) {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	parts := strings.Split(s, "\n")
	for i, part := range parts {
		if i > 0 {
			e.insertNewline()
		}
		if part != "" {
			e.insertString(part)
		}
	}
	e.dirty = true
}

func (e *TextEditor) deleteBackward() {
	e.dirty = true
	if e.col > 0 {
		line := e.lines[e.row]
		e.lines[e.row] = line[:e.col-1] + line[e.col:]
		e.col--
		return
	}
	if e.row > 0 {
		prev := e.lines[e.row-1]
		e.col = len(prev)
		e.lines[e.row-1] = prev + e.lines[e.row]
		e.lines = append(e.lines[:e.row], e.lines[e.row+1:]...)
		e.row--
	}
}

func (e *TextEditor) deleteForward() {
	e.dirty = true
	line := e.lines[e.row]
	if e.col < len(line) {
		e.lines[e.row] = line[:e.col] + line[e.col+1:]
		return
	}
	if e.row < len(e.lines)-1 {
		e.lines[e.row] = line + e.lines[e.row+1]
		e.lines = append(e.lines[:e.row+1], e.lines[e.row+2:]...)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	negative := n < 0
	if negative {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if negative {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func padLeft(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return strings.Repeat(" ", width-len(s)) + s
}
