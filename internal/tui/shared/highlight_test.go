package shared

import (
	"strings"
	"testing"
)

func anyStyledSpan(lines [][]Span) bool {
	for _, l := range lines {
		if len(l) > 0 {
			return true
		}
	}
	return false
}

func TestHighlightSpansJSON(t *testing.T) {
	text := "{\n  \"name\": \"widget\",\n  \"count\": 3\n}"
	spans := HighlightSpans("json", text)
	if spans == nil {
		t.Fatal("expected spans for json")
	}
	if got, want := len(spans), strings.Count(text, "\n")+1; got != want {
		t.Fatalf("span lines = %d, want %d", got, want)
	}
	if !anyStyledSpan(spans) {
		t.Fatal("expected at least one styled span")
	}
	// Every span must be within its line's byte bounds.
	lines := strings.Split(text, "\n")
	for i, ls := range spans {
		for _, s := range ls {
			if s.Start < 0 || s.End > len(lines[i]) || s.Start > s.End {
				t.Fatalf("line %d span %v out of bounds (len %d)", i, s, len(lines[i]))
			}
		}
	}
}

func TestHighlightSpansMultiLineComment(t *testing.T) {
	// An XML comment spanning multiple lines must color the continuation line.
	text := "<root>\n  <!-- line one\n       line two -->\n</root>"
	spans := HighlightSpans("xml", text)
	if spans == nil {
		t.Fatal("expected spans for xml")
	}
	if len(spans) < 3 || len(spans[2]) == 0 {
		t.Fatalf("expected the comment continuation (line 2) to be styled: %#v", spans)
	}
}

func TestHighlightSpansPlainAndUnknown(t *testing.T) {
	if HighlightSpans("plain", "hello") != nil {
		t.Error("plain should yield nil")
	}
	if HighlightSpans("", "hello") != nil {
		t.Error("empty language should yield nil")
	}
	if HighlightSpans("this-is-not-a-language", "hello") != nil {
		t.Error("unknown lexer should yield nil")
	}
}

func TestHighlightSpansSizeCap(t *testing.T) {
	big := strings.Repeat("x", maxHighlightBytes+1)
	if HighlightSpans("json", big) != nil {
		t.Error("oversized input should yield nil")
	}
}

func TestLexerForMIME(t *testing.T) {
	cases := map[string]string{
		"application/json":            "JSON",
		"application/json; charset=8": "JSON",
		"text/xml":                    "XML",
		"text/html":                   "HTML",
		"":                            "plain",
		"application/octet-stream":    "plain",
	}
	for ct, want := range cases {
		if got := LexerForMIME(ct); got != want {
			t.Errorf("LexerForMIME(%q) = %q, want %q", ct, got, want)
		}
	}
}
