package panels

// Panel is the interface all focusable panels implement
type Panel interface {
	ID() string
	SetFocused(bool)
	IsFocused() bool
	IsVisible() bool
}
