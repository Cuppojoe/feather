package http

import (
	"strings"
	"testing"
)

func TestClassifyBody(t *testing.T) {
	png := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x01}
	cases := []struct {
		name string
		ct   string
		body []byte
		want BodyKind
	}{
		{"empty", "application/json", nil, BodyEmpty},
		{"whitespace only", "", []byte("  \n\t"), BodyEmpty},
		{"json by content-type", "application/json", []byte(`{"a":1}`), BodyJSON},
		{"json with charset", "application/json; charset=utf-8", []byte(`[1,2]`), BodyJSON},
		{"vendor +json", "application/vnd.api+json", []byte(`{"x":true}`), BodyJSON},
		{"declared json but invalid", "application/json", []byte(`not json`), BodyText},
		{"unmarked json object", "", []byte(`{"a":1}`), BodyJSON},
		{"unmarked plain number stays text", "", []byte(`12345`), BodyText},
		{"text/plain", "text/plain", []byte("hello world"), BodyText},
		{"html", "text/html", []byte("<html></html>"), BodyText},
		{"xml by type", "application/xml", []byte("<a/>"), BodyText},
		{"octet-stream but textual", "application/octet-stream", []byte("just text"), BodyText},
		{"octet-stream binary", "application/octet-stream", png, BodyBinary},
		{"png by sniff (no ct)", "", png, BodyBinary},
		{"nul byte is binary", "", []byte("ab\x00cd"), BodyBinary},
	}
	for _, c := range cases {
		if got := ClassifyBody(c.ct, c.body); got != c.want {
			t.Errorf("%s: ClassifyBody = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestFormatResponseBodyJSON(t *testing.T) {
	out, kind := FormatResponseBody("application/json", []byte(`{"b":2,"a":1}`))
	if kind != BodyJSON {
		t.Fatalf("kind = %v", kind)
	}
	if !strings.Contains(out, "\n") || !strings.Contains(out, "  ") {
		t.Fatalf("expected indented JSON, got %q", out)
	}
}

func TestFormatResponseBodyBinary(t *testing.T) {
	body := append([]byte{0x00, 0x01, 0x02}, make([]byte, 400)...)
	out, kind := FormatResponseBody("image/png", body)
	if kind != BodyBinary {
		t.Fatalf("kind = %v", kind)
	}
	for _, want := range []string{"Binary response", "content-type:  image/png", "size:", "hex preview:", "more"} {
		if !strings.Contains(out, want) {
			t.Errorf("binary summary missing %q in:\n%s", want, out)
		}
	}
	// The raw NUL bytes must not appear verbatim (that's the whole point).
	if strings.ContainsRune(out, 0x00) {
		t.Error("binary summary leaked raw NUL bytes")
	}
}

func TestHumanizeBytes(t *testing.T) {
	cases := map[int]string{0: "0 B", 512: "512 B", 1024: "1.0 KB", 1536: "1.5 KB", 1048576: "1.0 MB"}
	for n, want := range cases {
		if got := humanizeBytes(n); got != want {
			t.Errorf("humanizeBytes(%d) = %q, want %q", n, got, want)
		}
	}
}
