package shared

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
)

// Span is a styled byte range within a single line. Offsets are byte indices
// (matching the editor's byte-based line slicing and search matches).
type Span struct {
	Start, End int
	Style      lipgloss.Style
}

// maxHighlightBytes caps how large a body we tokenize. Beyond this the editor
// renders plain text — still readable, and keeps rendering snappy.
const maxHighlightBytes = 256 * 1024

// HighlightSpans tokenizes text with the named lexer and returns per-line
// styled spans aligned to strings.Split(text, "\n"). It returns nil for the
// "plain"/empty language, an unknown lexer, or oversized input.
func HighlightSpans(language, text string) [][]Span {
	if language == "" || language == "plain" || text == "" {
		return nil
	}
	if len(text) > maxHighlightBytes {
		return nil
	}
	lexer := lexers.Get(language)
	if lexer == nil {
		return nil
	}
	tokens, err := chroma.Tokenise(lexer, nil, text)
	if err != nil {
		return nil
	}

	lineCount := strings.Count(text, "\n") + 1
	result := make([][]Span, lineCount)

	row, col := 0, 0
	for _, tok := range tokens {
		style, styled := styleFor(tok.Type)
		// A token value may contain newlines (e.g. a multi-line comment); emit
		// one span per line it covers.
		parts := strings.Split(tok.Value, "\n")
		for pi, part := range parts {
			if pi > 0 {
				row++
				col = 0
			}
			if part == "" {
				continue
			}
			if styled && row < len(result) {
				result[row] = append(result[row], Span{Start: col, End: col + len(part), Style: style})
			}
			col += len(part)
		}
	}
	return result
}

// styleFor maps a Chroma token type to a feather style, reusing the JSON
// palette so highlighting looks consistent across formats. The bool is false
// for ordinary text that needs no styling.
func styleFor(tt chroma.TokenType) (lipgloss.Style, bool) {
	switch {
	case tt.InCategory(chroma.Comment):
		return HighlightCommentStyle, true
	case tt == chroma.NameTag:
		// XML/HTML element names and JSON object keys.
		return JSONKeyStyle, true
	case tt == chroma.NameAttribute:
		return HighlightAttrStyle, true
	case tt.InSubCategory(chroma.LiteralString):
		return JSONStringStyle, true
	case tt.InSubCategory(chroma.LiteralNumber):
		return JSONNumberStyle, true
	case tt.InCategory(chroma.Keyword):
		// Includes JSON true/false/null (Keyword.Constant).
		return JSONBoolStyle, true
	case tt == chroma.NameFunction || tt == chroma.NameClass || tt == chroma.NameBuiltin:
		return HighlightKeywordStyle, true
	case tt == chroma.Error:
		return ErrorStyle, true
	default:
		return lipgloss.Style{}, false
	}
}

// AllLanguageNames returns every Chroma lexer name plus "plain", sorted
// case-insensitively. Used by the in-editor language picker.
func AllLanguageNames() []string {
	names := lexers.Names(false)
	out := make([]string, 0, len(names)+1)
	out = append(out, "plain")
	out = append(out, names...)
	// Names() is already alphabetical, just deduplicate plain if Chroma
	// happened to register it.
	seen := make(map[string]struct{}, len(out))
	dedup := out[:0]
	for _, n := range out {
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		dedup = append(dedup, n)
	}
	return dedup
}

// canonicalLanguage normalizes a language/lexer name. Empty, "plain", and
// unknown names collapse to "plain"; a known lexer returns its canonical name.
func canonicalLanguage(name string) string {
	if name == "" || name == "plain" {
		return "plain"
	}
	if l := lexers.Get(name); l != nil {
		return l.Config().Name
	}
	return "plain"
}

// LexerForMIME returns a lexer name for a Content-Type, or "plain" when none
// matches. Charset and other parameters are ignored.
func LexerForMIME(contentType string) string {
	mt := contentType
	if i := strings.IndexByte(mt, ';'); i >= 0 {
		mt = mt[:i]
	}
	mt = strings.TrimSpace(strings.ToLower(mt))
	// Generic binary types must never be "highlighted" — some Chroma lexers
	// register octet-stream, which would mis-color arbitrary bytes.
	if mt == "" || mt == "application/octet-stream" {
		return "plain"
	}
	if l := lexers.MatchMimeType(mt); l != nil {
		return l.Config().Name
	}
	return "plain"
}

// LexerForContent guesses a lexer from the body itself, or "plain".
func LexerForContent(text string) string {
	if l := lexers.Analyse(text); l != nil {
		return l.Config().Name
	}
	return "plain"
}
