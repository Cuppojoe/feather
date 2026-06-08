package shared

import (
	"strings"

	"charm.land/lipgloss/v2"
	tea "github.com/charmbracelet/bubbletea"
)

// Hint is a single displayed keyboard shortcut. Key is the canonical key that
// gets dispatched when the hint is clicked (e.g. "esc", "enter", "/", "d");
// Label is the human description ("back", "search"). Display optionally
// overrides how the key is shown (e.g. "↑/↓") without changing what is
// dispatched. A hint with an empty Key renders but is not clickable.
type Hint struct {
	Key     string
	Label   string
	Display string
}

// HintBar renders a row of shortcut hints and records where each was drawn so a
// mouse handler can map a click back to the key. Build it inside View() with
// Render, then resolve clicks with HitKey from the panel's mouse handler. The
// recorded coordinates are whatever space Render is given — pass panel-relative
// columns/rows that match the coordinates the mouse handler receives.
type HintBar struct {
	hints   []Hint
	regions ClickMap
}

// hintSegment formats one hint as "[key] label" (bracket) or "key:label".
func hintSegment(hint Hint, bracket bool) string {
	disp := hint.Display
	if disp == "" {
		disp = hint.Key
	}
	if bracket {
		return "[" + disp + "] " + hint.Label
	}
	return disp + ":" + hint.Label
}

// HintsWidth reports the cell width of a hint row laid out by Render with the
// same arguments. Useful for centering the row before rendering.
func HintsWidth(hints []Hint, bracket bool, sep string) int {
	w := 0
	for i, hint := range hints {
		if i > 0 {
			w += lipgloss.Width(sep)
		}
		w += lipgloss.Width(hintSegment(hint, bracket))
	}
	return w
}

// Render lays out hints on row y starting at column startCol and returns the
// styled string. When bracket is true each shortcut shows as "[key] label",
// otherwise as "key:label"; hints are joined by sep. A click region spanning
// each whole hint is recorded (in the coordinate space of y/startCol) so
// clicking anywhere on a hint fires it. When style carries width/alignment the
// caller must pass a startCol matching where the styled text actually lands.
func (h *HintBar) Render(hints []Hint, y, startCol int, bracket bool, sep string, style lipgloss.Style) string {
	h.hints = hints
	h.regions.Reset()

	sepW := lipgloss.Width(sep)
	var b strings.Builder
	col := startCol
	for i, hint := range hints {
		if i > 0 {
			b.WriteString(sep)
			col += sepW
		}
		seg := hintSegment(hint, bracket)
		w := lipgloss.Width(seg)
		if hint.Key != "" {
			h.regions.AddRange(y, col, col+w, i)
		}
		b.WriteString(seg)
		col += w
	}
	return style.Render(b.String())
}

// HitKey returns the canonical key of the hint at (x, y), if any was clicked.
func (h *HintBar) HitKey(x, y int) (string, bool) {
	if id, ok := h.regions.Hit(x, y); ok && id >= 0 && id < len(h.hints) {
		return h.hints[id].Key, true
	}
	return "", false
}

// KeyMsgFromName builds a synthetic tea.KeyMsg for a canonical key name so a
// clicked hint can be replayed through a panel's existing key handling. Named
// keys ("esc", "enter", "tab", "up"…) map to their key types; anything else is
// treated as literal runes ("/", "y", "D").
func KeyMsgFromName(name string) tea.KeyMsg {
	switch name {
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "shift+tab":
		return tea.KeyMsg{Type: tea.KeyShiftTab}
	case "space":
		return tea.KeyMsg{Type: tea.KeySpace}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(name)}
	}
}
