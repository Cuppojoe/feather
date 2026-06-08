package shared

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
)

// Colors
var (
	ColorPrimary    = lipgloss.Color("#7C3AED") // Purple
	ColorSecondary  = lipgloss.Color("#06B6D4") // Cyan
	ColorSuccess    = lipgloss.Color("#10B981") // Green
	ColorWarning    = lipgloss.Color("#F59E0B") // Amber
	ColorError      = lipgloss.Color("#EF4444") // Red
	ColorMuted      = lipgloss.Color("#6B7280") // Gray
	ColorBackground = lipgloss.Color("#1F2937") // Dark gray
	ColorBorder     = lipgloss.Color("#374151") // Medium gray
)

// Method colors
var MethodColors = map[string]color.Color{
	"GET":     lipgloss.Color("#10B981"), // Green
	"POST":    lipgloss.Color("#3B82F6"), // Blue
	"PUT":     lipgloss.Color("#F59E0B"), // Amber
	"PATCH":   lipgloss.Color("#8B5CF6"), // Purple
	"DELETE":  lipgloss.Color("#EF4444"), // Red
	"OPTIONS": lipgloss.Color("#6B7280"), // Gray
	"HEAD":    lipgloss.Color("#6B7280"), // Gray
}

// Styles
var (
	// App frame
	AppStyle = lipgloss.NewStyle().
			Padding(1, 2)

	// Title
	TitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorPrimary)

	// Subtitle
	SubtitleStyle = lipgloss.NewStyle().
			Foreground(ColorMuted).
			MarginBottom(1)

	// Status bar
	StatusBarStyle = lipgloss.NewStyle().
			Foreground(ColorMuted).
			Background(ColorBackground).
			Padding(0, 1)

	// Help text
	HelpStyle = lipgloss.NewStyle().
			Foreground(ColorMuted)

	// Selected item
	SelectedStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorPrimary)

	// Normal item
	NormalStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#E5E7EB"))

	// Dimmed text
	DimStyle = lipgloss.NewStyle().
			Foreground(ColorMuted)

	// Error text
	ErrorStyle = lipgloss.NewStyle().
			Foreground(ColorError)

	// Success text
	SuccessStyle = lipgloss.NewStyle().
			Foreground(ColorSuccess)

	// Warning text
	WarningStyle = lipgloss.NewStyle().
			Foreground(ColorWarning)

	// Border
	BorderStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(ColorBorder).
			Padding(0, 1)

	// Focused border
	FocusedBorderStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(ColorPrimary).
				Padding(0, 1)

	// Input field
	InputStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#E5E7EB"))

	// Input label
	InputLabelStyle = lipgloss.NewStyle().
			Foreground(ColorMuted).
			Width(12)

	// Badge styles
	BadgeStyle = lipgloss.NewStyle().
			Padding(0, 1).
			Background(ColorPrimary).
			Foreground(lipgloss.Color("#FFFFFF"))

	// Tag badge
	TagBadgeStyle = lipgloss.NewStyle().
			Padding(0, 1).
			Background(ColorSecondary).
			Foreground(lipgloss.Color("#FFFFFF"))

	// JSON key
	JSONKeyStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#7C3AED"))

	// JSON string
	JSONStringStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#10B981"))

	// JSON number
	JSONNumberStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#F59E0B"))

	// JSON boolean
	JSONBoolStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#3B82F6"))

	// JSON null
	JSONNullStyle = lipgloss.NewStyle().
			Foreground(ColorMuted)

	// Cursor
	CursorStyle = lipgloss.NewStyle().
			Foreground(ColorPrimary)

	// Tab styles
	ActiveTabStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(ColorPrimary).
			Padding(0, 1)

	InactiveTabStyle = lipgloss.NewStyle().
				Foreground(ColorMuted).
				Padding(0, 1)

	TabGapStyle = lipgloss.NewStyle().
			Foreground(ColorBorder)

	// Search match highlight (all occurrences of the pattern)
	SearchMatchStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#1F2937")).
				Background(ColorWarning)

	// Focused search match (the occurrence the view is parked on)
	SearchCurrentStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#1F2937")).
				Background(ColorSecondary)

	// Search entry prompt (the "/pattern" line)
	SearchPromptStyle = lipgloss.NewStyle().
				Foreground(ColorSecondary)

	// Visual text selection (mouse drag) — inverse over the selection.
	SelectionStyle = lipgloss.NewStyle().Reverse(true)

	// Syntax-highlight styles for non-JSON token categories (Chroma). JSON
	// keys/strings/numbers/booleans reuse the JSON* styles above.
	HighlightCommentStyle = lipgloss.NewStyle().Foreground(ColorMuted).Italic(true)
	HighlightAttrStyle    = lipgloss.NewStyle().Foreground(ColorSecondary)
	HighlightKeywordStyle = lipgloss.NewStyle().Foreground(ColorPrimary)
)

// MethodStyle returns the style for an HTTP method
func MethodStyle(method string) lipgloss.Style {
	color, ok := MethodColors[method]
	if !ok {
		color = ColorMuted
	}
	return lipgloss.NewStyle().
		Foreground(color).
		Bold(true).
		Width(7)
}

// StatusCodeStyle returns the style for a status code
func StatusCodeStyle(code int) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(StatusCodeColor(code)).Bold(true)
}

// StatusCodeColor returns the color for a status code
func StatusCodeColor(code int) color.Color {
	switch {
	case code >= 200 && code < 300:
		return ColorSuccess
	case code >= 300 && code < 400:
		return ColorWarning
	case code >= 400:
		return ColorError
	default:
		return ColorMuted
	}
}

// LogoLines is the ASCII art shown on the splash screen.
var LogoLines = []string{
	` _____          _   _               `,
	`|  ___|__  __ _| |_| |__   ___ _ __ `,
	`| |_ / _ \/ _` + "`" + ` | __| '_ \ / _ \ '__|`,
	`|  _|  __/ (_| | |_| | | |  __/ |   `,
	`|_|  \___|\__,_|\__|_| |_|\___|_|   `,
}

// RainbowColors is the gradient used across the app's brand surfaces (the
// splash logo and the "Feather" wordmark in the profile bar). Exposed so
// callers can paint arbitrary text in the same colour cycle.
var RainbowColors = []color.Color{
	lipgloss.Color("#FF6B6B"), // Red
	lipgloss.Color("#FFA06B"), // Orange
	lipgloss.Color("#FFD93D"), // Yellow
	lipgloss.Color("#6BCB77"), // Green
	lipgloss.Color("#4D96FF"), // Blue
	lipgloss.Color("#9B6BFF"), // Purple
}

// Rainbow renders s with each rune coloured according to its position in
// the rainbow gradient. Useful for the "Feather" wordmark.
func Rainbow(s string) string {
	runes := []rune(s)
	n := len(runes)
	if n == 0 {
		return ""
	}
	var b strings.Builder
	for i, r := range runes {
		idx := (i * len(RainbowColors)) / n
		if idx >= len(RainbowColors) {
			idx = len(RainbowColors) - 1
		}
		b.WriteString(lipgloss.NewStyle().Foreground(RainbowColors[idx]).Render(string(r)))
	}
	return b.String()
}

// RenderLogo builds the centred rainbow ASCII logo at the given width.
func RenderLogo(width int) string {
	var lines []string
	for _, line := range LogoLines {
		lines = append(lines, Rainbow(line))
	}
	return lipgloss.NewStyle().
		Width(width).
		Align(lipgloss.Center).
		Render(strings.Join(lines, "\n"))
}

// TruncateWithEllipsis truncates a string to maxLen and adds ellipsis
func TruncateWithEllipsis(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}

// Panel renders content in a bordered box with colored borders depending on focus status
func Panel(content string, width, height int, focused bool) string {
	borderStyle := BorderStyle
	if focused {
		borderStyle = FocusedBorderStyle
	}

	return borderStyle.
		Width(width).
		Height(height).
		Render(content)
}
