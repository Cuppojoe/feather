package shared

import "github.com/charmbracelet/bubbles/key"

// KeyMap defines the application keybindings.
//
// Single-letter shortcuts are split into two pools so the dispatcher can
// cascade outer→inner without one scope shadowing another:
//
//   - App-scope globals are uppercase (H, R, X, D, E, J, P) plus a few
//     symbol/modifier keys (?, f1, q, ctrl+c, ctrl+s, tab, shift+tab).
//     They fire whenever the focused panel isn't typing text.
//   - Screen- and widget-scope letters are lowercase (n, e, d, a, r, c, s,
//     v, y, ...). They never collide with globals because the global pool
//     is strictly uppercase.
type KeyMap struct {
	Up              key.Binding
	Down            key.Binding
	Left            key.Binding
	Right           key.Binding
	Enter           key.Binding
	Back            key.Binding
	Quit            key.Binding
	Help            key.Binding
	History         key.Binding
	Profile         key.Binding
	Scripts         key.Binding
	EnvList         key.Binding
	RequestPanel    key.Binding
	CloseSidePanels key.Binding
	ErrorDetails    key.Binding
	Search          key.Binding
	Copy            key.Binding
	Save            key.Binding
	Refresh         key.Binding
	Tab             key.Binding
	ShiftTab        key.Binding
	PageUp          key.Binding
	PageDown        key.Binding
	Home            key.Binding
	End             key.Binding
}

// DefaultKeyMap returns the default keybindings
func DefaultKeyMap() KeyMap {
	return KeyMap{
		Up: key.NewBinding(
			key.WithKeys("up", "k"),
			key.WithHelp("↑/k", "up"),
		),
		Down: key.NewBinding(
			key.WithKeys("down", "j"),
			key.WithHelp("↓/j", "down"),
		),
		Left: key.NewBinding(
			key.WithKeys("left", "h"),
			key.WithHelp("←/h", "left"),
		),
		Right: key.NewBinding(
			key.WithKeys("right", "l"),
			key.WithHelp("→/l", "right"),
		),
		Enter: key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("enter", "select"),
		),
		Back: key.NewBinding(
			key.WithKeys("esc"),
			key.WithHelp("esc", "back"),
		),
		Quit: key.NewBinding(
			key.WithKeys("q", "ctrl+c"),
			key.WithHelp("q", "quit"),
		),
		Help: key.NewBinding(
			key.WithKeys("f1", "?"),
			key.WithHelp("f1", "help"),
		),
		History: key.NewBinding(
			key.WithKeys("H"),
			key.WithHelp("H", "history"),
		),
		Profile: key.NewBinding(
			key.WithKeys("P"),
			key.WithHelp("P", "profile"),
		),
		Scripts: key.NewBinding(
			key.WithKeys("J"),
			key.WithHelp("J", "scripts"),
		),
		EnvList: key.NewBinding(
			key.WithKeys("E"),
			key.WithHelp("E", "envs"),
		),
		RequestPanel: key.NewBinding(
			key.WithKeys("R"),
			key.WithHelp("R", "request"),
		),
		CloseSidePanels: key.NewBinding(
			key.WithKeys("X"),
			key.WithHelp("X", "close panels"),
		),
		ErrorDetails: key.NewBinding(
			key.WithKeys("D"),
			key.WithHelp("D", "error details"),
		),
		Search: key.NewBinding(
			key.WithKeys("/"),
			key.WithHelp("/", "search"),
		),
		Copy: key.NewBinding(
			key.WithKeys("y"),
			key.WithHelp("y", "copy"),
		),
		Save: key.NewBinding(
			key.WithKeys("ctrl+s"),
			key.WithHelp("ctrl+s", "save"),
		),
		Refresh: key.NewBinding(
			key.WithKeys("r"),
			key.WithHelp("r", "refresh"),
		),
		Tab: key.NewBinding(
			key.WithKeys("tab"),
			key.WithHelp("tab", "next field"),
		),
		ShiftTab: key.NewBinding(
			key.WithKeys("shift+tab"),
			key.WithHelp("shift+tab", "prev field"),
		),
		PageUp: key.NewBinding(
			key.WithKeys("pgup", "ctrl+u"),
			key.WithHelp("pgup", "page up"),
		),
		PageDown: key.NewBinding(
			key.WithKeys("pgdown", "ctrl+d"),
			key.WithHelp("pgdown", "page down"),
		),
		Home: key.NewBinding(
			key.WithKeys("home", "g"),
			key.WithHelp("home/g", "top"),
		),
		End: key.NewBinding(
			key.WithKeys("end", "G"),
			key.WithHelp("end/G", "bottom"),
		),
	}
}

// ShortHelp returns a short help text
func (k KeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Profile, k.EnvList, k.History, k.RequestPanel, k.Scripts, k.Help, k.Quit}
}

// FullHelp returns the full help text
func (k KeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.Left, k.Right},
		{k.Enter, k.Back, k.Search},
		{k.Profile, k.EnvList, k.History, k.RequestPanel},
		{k.CloseSidePanels, k.ErrorDetails},
		{k.Scripts, k.Help, k.Copy, k.Save, k.Quit},
	}
}
