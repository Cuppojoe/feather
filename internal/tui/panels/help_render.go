package panels

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/cuppojoe/feather/internal/tui/shared"
)

// renderHelpDoc converts our internal markdown-like source into a pair the
// TextEditor can consume directly: a plain-text body (so search, selection,
// and copy work as expected) plus a parallel [][]Span array that overlays
// terminal-friendly styling at render time.
//
// We don't use Chroma's markdown lexer because it leaves headers visually
// indistinguishable from body text in a TUI. Conventions handled here:
//
//	# Title             → bold primary, followed by a dim ─ underline row
//	## Title            → bold secondary
//	### Title           → bold warning
//	    code            → 4-space indent stripped, re-inset by 2 spaces,
//	                      shown in cyan as a code block
//
// Lists, prose, and blank lines pass through verbatim with no styling.
func renderHelpDoc(raw string) (string, [][]shared.Span) {
	h1 := lipgloss.NewStyle().Bold(true).Foreground(shared.ColorPrimary)
	h2 := lipgloss.NewStyle().Bold(true).Foreground(shared.ColorSecondary)
	h3 := lipgloss.NewStyle().Bold(true).Foreground(shared.ColorWarning)
	codeBlock := lipgloss.NewStyle().Foreground(shared.ColorSecondary)
	dim := shared.DimStyle

	var lines []string
	var spans [][]shared.Span

	for _, line := range strings.Split(raw, "\n") {
		switch {
		case strings.HasPrefix(line, "### "):
			text := strings.TrimPrefix(line, "### ")
			lines = append(lines, text)
			spans = append(spans, []shared.Span{{Start: 0, End: len(text), Style: h3}})

		case strings.HasPrefix(line, "## "):
			text := strings.TrimPrefix(line, "## ")
			lines = append(lines, text)
			spans = append(spans, []shared.Span{{Start: 0, End: len(text), Style: h2}})

		case strings.HasPrefix(line, "# "):
			text := strings.TrimPrefix(line, "# ")
			lines = append(lines, text)
			spans = append(spans, []shared.Span{{Start: 0, End: len(text), Style: h1}})

			// Underline as a second physical line so the title text stays
			// plain (and searchable).
			underline := strings.Repeat("─", lipgloss.Width(text))
			lines = append(lines, underline)
			spans = append(spans, []shared.Span{{Start: 0, End: len(underline), Style: dim}})

		case strings.HasPrefix(line, "    "):
			// Code block — drop the 4-space marker, then visually inset by
			// two so prose and code are distinguishable. Style only the
			// payload (after the inset) so leading whitespace doesn't
			// receive a colour that the cursor can sit on.
			stripped := line[4:]
			indented := "  " + stripped
			lines = append(lines, indented)
			spans = append(spans, []shared.Span{{Start: 2, End: 2 + len(stripped), Style: codeBlock}})

		default:
			lines = append(lines, line)
			spans = append(spans, nil)
		}
	}

	return strings.Join(lines, "\n"), spans
}
