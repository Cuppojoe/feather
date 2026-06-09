package shared

import (
	"sort"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// KVPair is one row of a KVEditor — a single key with its current value.
type KVPair struct {
	Key   string
	Value string
}

// kvKeyColCells is the visible width reserved for the key column. Chosen
// to fit comfortably alongside most env var names without truncating
// (longer keys still render in full and push the value rightward).
const kvKeyColCells = 26

// KVEditor is a focused two-column editor for ordered key/value pairs.
// Designed for environment variables, override headers, query params —
// anywhere the user manages a small map by hand.
//
// Keybindings (when focused, not mid-edit):
//
//	↑ / ↓ / k / j   navigate
//	enter / e       edit value at cursor
//	r               edit key at cursor (rename)
//	a               add a new row + edit its key
//	c               clear value at cursor (keep the key, blank the value)
//	d               delete row at cursor
//
// Edit mode (inline textinput in the targeted cell):
//
//	enter           commit
//	esc             cancel (drops a freshly-added row whose key is still empty)
//	tab             commit current cell, jump to the other column on the same row
type KVEditor struct {
	pairs    []KVPair
	cursor   int
	topRow   int
	focused  bool
	dirty    bool
	width    int
	height   int
	clickMap ClickMap

	// Inline edit state. editing == true means input owns the keystrokes;
	// editKey toggles which column the input is overlaying.
	editing       bool
	editKey       bool
	editingNew    bool // true when the edit row was just created via 'a'
	input         textinput.Model
	preEditCursor int // cursor row when the edit started, in case we drop a row

	// Sensitive / reveal state. sensitive tracks which keys have their
	// values masked in the view; revealed is transient "show me anyway"
	// state that the user toggles with `v` and that doesn't persist when
	// the editor reloads. `s` toggles the sensitive flag.
	sensitive map[string]bool
	revealed  map[string]bool
}

// NewKVEditor returns an empty editor. SetValues to seed it.
func NewKVEditor() KVEditor {
	ti := textinput.New()
	ti.CharLimit = 4096
	ti.Prompt = ""
	return KVEditor{
		input:     ti,
		sensitive: map[string]bool{},
		revealed:  map[string]bool{},
	}
}

// SetSize records the editor's render dimensions. The View clamps cursor
// and recomputes scroll on every call so changes are picked up.
func (e *KVEditor) SetSize(w, h int) {
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	e.width = w
	e.height = h
}

// SetValues replaces the editor's contents with the entries from m,
// sorted alphabetically by key for a stable initial layout. Resets cursor,
// scroll, dirty flag, and the transient reveal map. Pair with
// SetSensitive to seed which keys should display as masked.
func (e *KVEditor) SetValues(m map[string]string) {
	e.pairs = e.pairs[:0]
	for k, v := range m {
		e.pairs = append(e.pairs, KVPair{Key: k, Value: v})
	}
	sort.Slice(e.pairs, func(i, j int) bool { return e.pairs[i].Key < e.pairs[j].Key })
	e.cursor = 0
	e.topRow = 0
	e.dirty = false
	e.editing = false
	e.input.Blur()
	e.revealed = map[string]bool{}
}

// SetSensitive marks the listed keys as sensitive (values rendered with
// bullets in place of the actual content). Any keys not in the list are
// cleared back to non-sensitive. Reveal state is reset.
func (e *KVEditor) SetSensitive(keys []string) {
	e.sensitive = make(map[string]bool, len(keys))
	for _, k := range keys {
		e.sensitive[k] = true
	}
	e.revealed = map[string]bool{}
}

// SensitiveKeys returns the names of rows currently marked sensitive,
// alphabetised. Rows whose key has been deleted are excluded so the
// returned slice always reflects live rows.
func (e *KVEditor) SensitiveKeys() []string {
	if len(e.sensitive) == 0 {
		return nil
	}
	live := make(map[string]struct{}, len(e.pairs))
	for _, p := range e.pairs {
		if p.Key != "" {
			live[p.Key] = struct{}{}
		}
	}
	var out []string
	for k, v := range e.sensitive {
		if !v {
			continue
		}
		if _, ok := live[k]; !ok {
			continue
		}
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Values returns a fresh map of the current rows. Rows with an empty key
// are skipped (a leftover from "a" that the user didn't finish naming).
func (e *KVEditor) Values() map[string]string {
	out := make(map[string]string, len(e.pairs))
	for _, p := range e.pairs {
		if p.Key == "" {
			continue
		}
		out[p.Key] = p.Value
	}
	return out
}

// Focus / Blur mirror the TextEditor API so hosts can drive focus state
// the same way.
func (e *KVEditor) Focus() tea.Cmd {
	if e.focused {
		return nil
	}
	e.focused = true
	return nil
}
func (e *KVEditor) Blur() {
	e.focused = false
	if e.editing {
		e.cancelEdit()
	}
}
func (e *KVEditor) Focused() bool   { return e.focused }
func (e *KVEditor) IsEditing() bool { return e.editing }
func (e *KVEditor) IsDirty() bool   { return e.dirty }
func (e *KVEditor) MarkClean()      { e.dirty = false }

// Update handles a single bubbletea message.
func (e *KVEditor) Update(msg tea.Msg) tea.Cmd {
	if !e.focused {
		return nil
	}

	if e.editing {
		return e.updateEditing(msg)
	}

	// Mouse: navigate cursor on click, double-click could edit (skip for now).
	if mouse, ok := msg.(tea.MouseMsg); ok {
		return e.updateMouse(mouse)
	}

	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	return e.updateNav(km)
}

func (e *KVEditor) updateNav(km tea.KeyMsg) tea.Cmd {
	switch km.String() {
	case "up", "k":
		if e.cursor > 0 {
			e.cursor--
		}
	case "down", "j":
		if e.cursor < len(e.pairs)-1 {
			e.cursor++
		}
	case "home", "g":
		e.cursor = 0
	case "end", "G":
		e.cursor = len(e.pairs) - 1
		if e.cursor < 0 {
			e.cursor = 0
		}
	case "a":
		// Append a blank row and immediately jump into key edit.
		e.pairs = append(e.pairs, KVPair{})
		e.cursor = len(e.pairs) - 1
		e.beginEdit(true /* editKey */, true /* newRow */)
	case "enter", "e":
		if e.cursor >= 0 && e.cursor < len(e.pairs) {
			e.beginEdit(false /* editKey */, false /* newRow */)
		}
	case "r":
		if e.cursor >= 0 && e.cursor < len(e.pairs) {
			e.beginEdit(true /* editKey */, false /* newRow */)
		}
	case "c":
		// Clear the value but keep the key — the affordance the user
		// asked for. Only marks dirty when there was actually a value.
		if e.cursor >= 0 && e.cursor < len(e.pairs) && e.pairs[e.cursor].Value != "" {
			e.pairs[e.cursor].Value = ""
			e.dirty = true
		}
	case "s":
		// Toggle the sensitive flag for the row. Doesn't mean much for
		// an unnamed (empty key) row, so skip those.
		if e.cursor >= 0 && e.cursor < len(e.pairs) {
			k := e.pairs[e.cursor].Key
			if k != "" {
				if e.sensitive == nil {
					e.sensitive = map[string]bool{}
				}
				e.sensitive[k] = !e.sensitive[k]
				// When marking sensitive we drop any active reveal so
				// the next press of `v` is required to peek.
				if e.sensitive[k] {
					delete(e.revealed, k)
				}
				e.dirty = true
			}
		}
	case "v":
		// Transient reveal of the current row's value. Only meaningful
		// when the row is marked sensitive — otherwise it's a no-op.
		if e.cursor >= 0 && e.cursor < len(e.pairs) {
			k := e.pairs[e.cursor].Key
			if k != "" && e.sensitive[k] {
				if e.revealed == nil {
					e.revealed = map[string]bool{}
				}
				e.revealed[k] = !e.revealed[k]
			}
		}
	case "d":
		if e.cursor >= 0 && e.cursor < len(e.pairs) {
			deletedKey := e.pairs[e.cursor].Key
			e.pairs = append(e.pairs[:e.cursor], e.pairs[e.cursor+1:]...)
			delete(e.sensitive, deletedKey)
			delete(e.revealed, deletedKey)
			if e.cursor >= len(e.pairs) {
				e.cursor = len(e.pairs) - 1
			}
			if e.cursor < 0 {
				e.cursor = 0
			}
			e.dirty = true
		}
	}
	return nil
}

func (e *KVEditor) updateEditing(msg tea.Msg) tea.Cmd {
	if km, ok := msg.(tea.KeyMsg); ok {
		switch km.String() {
		case "esc":
			e.cancelEdit()
			return nil
		case "enter":
			e.commitEdit()
			return nil
		case "tab":
			// Jump to the other column on the same row.
			e.commitEdit()
			if e.cursor >= 0 && e.cursor < len(e.pairs) {
				e.beginEdit(!e.editKey /* swap column */, false)
			}
			return nil
		}
	}
	var cmd tea.Cmd
	e.input, cmd = e.input.Update(msg)
	return cmd
}

func (e *KVEditor) updateMouse(mm tea.MouseMsg) tea.Cmd {
	switch mm.Button {
	case tea.MouseButtonWheelUp:
		if e.cursor > 0 {
			e.cursor--
		}
	case tea.MouseButtonWheelDown:
		if e.cursor < len(e.pairs)-1 {
			e.cursor++
		}
	case tea.MouseButtonLeft:
		if mm.Action != tea.MouseActionRelease {
			return nil
		}
		if id, ok := e.clickMap.Hit(mm.X, mm.Y); ok && id >= 0 && id < len(e.pairs) {
			e.cursor = id
		}
	}
	return nil
}

func (e *KVEditor) beginEdit(editKey, newRow bool) {
	if e.cursor < 0 || e.cursor >= len(e.pairs) {
		return
	}
	e.editing = true
	e.editKey = editKey
	e.editingNew = newRow
	e.preEditCursor = e.cursor
	current := e.pairs[e.cursor].Value
	if editKey {
		current = e.pairs[e.cursor].Key
	}
	e.input.SetValue(current)
	e.input.CursorEnd()
	e.input.Focus()
}

func (e *KVEditor) commitEdit() {
	if !e.editing {
		return
	}
	newVal := e.input.Value()
	if e.editKey {
		newVal = strings.TrimSpace(newVal)
	}
	if e.cursor >= 0 && e.cursor < len(e.pairs) {
		var before string
		if e.editKey {
			before = e.pairs[e.cursor].Key
			e.pairs[e.cursor].Key = newVal
			// Carry sensitive + reveal state with the key when it's
			// renamed so a sensitive flag isn't dropped silently.
			if before != newVal {
				if e.sensitive[before] {
					delete(e.sensitive, before)
					if newVal != "" {
						if e.sensitive == nil {
							e.sensitive = map[string]bool{}
						}
						e.sensitive[newVal] = true
					}
				}
				if e.revealed[before] {
					delete(e.revealed, before)
					if newVal != "" {
						if e.revealed == nil {
							e.revealed = map[string]bool{}
						}
						e.revealed[newVal] = true
					}
				}
			}
		} else {
			before = e.pairs[e.cursor].Value
			e.pairs[e.cursor].Value = newVal
		}
		if before != newVal {
			e.dirty = true
		}
	}
	e.endEdit()
}

func (e *KVEditor) cancelEdit() {
	if !e.editing {
		return
	}
	// If the user bailed on a row that was created by 'a' AND its key is
	// still empty, drop the row so the buffer doesn't keep a phantom
	// entry around.
	if e.editingNew && e.cursor >= 0 && e.cursor < len(e.pairs) &&
		strings.TrimSpace(e.pairs[e.cursor].Key) == "" &&
		e.pairs[e.cursor].Value == "" {
		e.pairs = append(e.pairs[:e.cursor], e.pairs[e.cursor+1:]...)
		if e.cursor >= len(e.pairs) {
			e.cursor = len(e.pairs) - 1
		}
		if e.cursor < 0 {
			e.cursor = 0
		}
	}
	e.endEdit()
}

func (e *KVEditor) endEdit() {
	e.editing = false
	e.editingNew = false
	e.input.Blur()
	e.input.SetValue("")
}

// View renders the editor as a two-column table with the highlighted row
// inset by a "> " cursor. The cell being edited is replaced with the
// inline textinput in place.
func (e *KVEditor) View() string {
	e.clickMap.Reset()
	if e.width < 1 {
		e.width = 40
	}
	if e.height < 2 {
		e.height = 4
	}

	var b strings.Builder
	// Header
	b.WriteString("  ")
	b.WriteString(DimStyle.Render(padRightCells("KEY", kvKeyColCells)))
	b.WriteString("  ")
	b.WriteString(DimStyle.Render("VALUE"))
	b.WriteString("\n")

	if len(e.pairs) == 0 {
		b.WriteString(DimStyle.Render("  (no values — press [a] to add one)"))
		return b.String()
	}

	visibleRows := e.height - 1 // reserve the header line
	if visibleRows < 1 {
		visibleRows = 1
	}
	// Keep cursor in view.
	if e.cursor < e.topRow {
		e.topRow = e.cursor
	}
	if e.cursor >= e.topRow+visibleRows {
		e.topRow = e.cursor - visibleRows + 1
	}
	if e.topRow < 0 {
		e.topRow = 0
	}

	rowStartY := 1 // 0 = header, first data row is 1
	valueColX := 2 + kvKeyColCells + 2
	available := e.width - valueColX - 1
	if available < 4 {
		available = 4
	}

	// Resize the input to fit whichever column we're editing.
	if e.editing {
		if e.editKey {
			e.input.Width = kvKeyColCells
		} else {
			e.input.Width = available
		}
	}

	for i := e.topRow; i < len(e.pairs) && i-e.topRow < visibleRows; i++ {
		pair := e.pairs[i]
		isCursor := i == e.cursor

		cursor := "  "
		if isCursor {
			cursor = CursorStyle.Render("> ")
		}

		isSensitive := pair.Key != "" && e.sensitive[pair.Key]
		isRevealed := isSensitive && e.revealed[pair.Key]

		var keyCell, valCell string
		if e.editing && isCursor && e.editKey {
			keyCell = e.input.View()
		} else {
			label := pair.Key
			labelStyled := false
			if label == "" {
				label = "(unnamed)"
				labelStyled = true
			}
			if isSensitive && !labelStyled {
				// Tiny lock prefix so users can see at a glance which
				// rows are sensitive. Fits in 2 visible cells.
				label = "🔒 " + label
			}
			padded := padRightCells(label, kvKeyColCells)
			switch {
			case labelStyled:
				keyCell = DimStyle.Render(padded)
			case isSensitive:
				keyCell = WarningStyle.Render(padded)
			case isCursor:
				keyCell = SelectedStyle.Render(padded)
			default:
				keyCell = NormalStyle.Render(padded)
			}
		}
		if e.editing && isCursor && !e.editKey {
			// While editing a sensitive cell the user clearly already
			// knows what they're typing — show the actual content in
			// the input regardless of mask state.
			valCell = e.input.View()
		} else {
			val := pair.Value
			switch {
			case val == "":
				valCell = DimStyle.Render("(empty)")
			case isSensitive && !isRevealed:
				valCell = WarningStyle.Render(maskValue(val))
			case isSensitive && isRevealed:
				valCell = WarningStyle.Render(truncateCells(val, available))
			default:
				valCell = NormalStyle.Render(truncateCells(val, available))
			}
		}

		e.clickMap.AddRow(rowStartY+(i-e.topRow), i)
		b.WriteString(cursor)
		b.WriteString(keyCell)
		b.WriteString("  ")
		b.WriteString(valCell)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// Hint returns the single-line shortcut row hosts can render under the
// editor. Reflects whether an inline edit is in progress.
func (e *KVEditor) Hint() string {
	if e.editing {
		return DimStyle.Render("[enter] commit  [tab] switch col  [esc] cancel")
	}
	return DimStyle.Render(
		"[↑/↓] move  [enter] edit  [r] rename  [a] add  [c] clear  [d] delete  [s] secret  [v] reveal")
}

// maskValue renders a value placeholder for sensitive rows: a fixed bullet
// run plus the byte length so a mistyped/short secret is still spottable
// without exposing the value itself.
func maskValue(v string) string {
	if v == "" {
		return ""
	}
	const bullets = "•••••••"
	return bullets + " (" + strconv.Itoa(len(v)) + " chars)"
}

// padRightCells pads s with spaces on the right to reach width display
// cells. Truncates with an ellipsis when s is already wider.
func padRightCells(s string, w int) string {
	cells := lipgloss.Width(s)
	if cells >= w {
		return truncateCells(s, w)
	}
	return s + strings.Repeat(" ", w-cells)
}

// truncateCells trims s to fit within maxCells display cells, replacing
// the last cell with an ellipsis when the input was too wide.
func truncateCells(s string, maxCells int) string {
	if maxCells <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= maxCells {
		return s
	}
	// Byte-based truncation — KVEditor stores plain strings, so byte
	// offsets and cells generally agree for the common ASCII case.
	if maxCells <= 1 {
		return "…"
	}
	cut := maxCells - 1
	if cut > len(s) {
		cut = len(s)
	}
	return s[:cut] + "…"
}
