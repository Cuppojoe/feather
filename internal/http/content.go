package http

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

// BodyKind classifies how a response body should be presented.
type BodyKind int

const (
	BodyEmpty  BodyKind = iota // no content
	BodyJSON                   // valid JSON — pretty-printed and highlighted
	BodyText                   // human-readable text (xml, html, csv, plain, …)
	BodyBinary                 // not displayable as text — shown as a summary
)

// ContentType returns the response's Content-Type header (without parameters).
func (r *Response) ContentType() string {
	if r == nil {
		return ""
	}
	return mediaType(r.Headers.Get("Content-Type"))
}

// mediaType lowercases a Content-Type and strips any ";charset=…" parameters.
func mediaType(ct string) string {
	ct = strings.ToLower(strings.TrimSpace(ct))
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	return ct
}

// ClassifyBody decides how to present a body, preferring the declared
// Content-Type and falling back to sniffing the bytes.
func ClassifyBody(contentType string, body []byte) BodyKind {
	if len(bytes.TrimSpace(body)) == 0 {
		return BodyEmpty
	}
	ct := mediaType(contentType)

	switch {
	case strings.Contains(ct, "json"):
		if json.Valid(body) {
			return BodyJSON
		}
		// Declared JSON but malformed — still show the text rather than hide it.
		return BodyText
	case ct == "":
		// No declared type: sniff. Unmarked JSON is common for APIs.
		if t := bytes.TrimSpace(body); len(t) > 0 && (t[0] == '{' || t[0] == '[') && json.Valid(t) {
			return BodyJSON
		}
		if looksTextual(body) {
			return BodyText
		}
		return BodyBinary
	case isTextualType(ct):
		return BodyText
	default:
		// Unknown declared type (e.g. application/octet-stream): trust the bytes.
		if looksTextual(body) {
			return BodyText
		}
		return BodyBinary
	}
}

// FormatResponseBody returns display text for a body plus its classification.
// JSON is pretty-printed; text is returned as-is; binary becomes a readable
// summary (type, size, hex preview) instead of dumping raw bytes.
func FormatResponseBody(contentType string, body []byte) (string, BodyKind) {
	kind := ClassifyBody(contentType, body)
	switch kind {
	case BodyEmpty:
		return "", BodyEmpty
	case BodyJSON:
		if formatted, err := prettyJSON(body); err == nil {
			return formatted, BodyJSON
		}
		return string(body), BodyText
	case BodyText:
		return string(body), BodyText
	default:
		return binarySummary(contentType, body), BodyBinary
	}
}

// prettyJSON indents valid JSON; it errors when the input isn't valid JSON.
func prettyJSON(data []byte) (string, error) {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return "", err
	}
	formatted, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	return string(formatted), nil
}

// isTextualType reports whether a media type is a known text-bearing type.
func isTextualType(ct string) bool {
	if strings.HasPrefix(ct, "text/") {
		return true
	}
	if strings.HasSuffix(ct, "+xml") || strings.HasSuffix(ct, "+json") {
		return true
	}
	switch ct {
	case "application/xml", "application/xhtml+xml", "image/svg+xml",
		"application/javascript", "application/ecmascript",
		"application/x-www-form-urlencoded", "application/csv",
		"application/yaml", "application/x-yaml", "application/graphql":
		return true
	}
	return false
}

// looksTextual reports whether a body is plausibly human-readable text: valid
// UTF-8, no NUL bytes, and few control characters.
func looksTextual(body []byte) bool {
	if !utf8.Valid(body) {
		return false
	}
	control := 0
	for _, b := range body {
		if b == 0x00 {
			return false // NUL strongly implies binary
		}
		if b < 0x20 && b != '\t' && b != '\n' && b != '\r' {
			control++
		}
	}
	// Allow a small fraction of control bytes (e.g. ANSI escapes in logs).
	return control*100 <= len(body)*5 // ≤ 5%
}

// binarySummary renders a friendly, non-garbled view of a binary body.
func binarySummary(contentType string, body []byte) string {
	var b strings.Builder
	b.WriteString("⊘ Binary response — not shown as text\n\n")
	ct := mediaType(contentType)
	if ct == "" {
		ct = "(unknown)"
	}
	b.WriteString(fmt.Sprintf("content-type:  %s\n", ct))
	b.WriteString(fmt.Sprintf("size:          %s\n\n", humanizeBytes(len(body))))

	const previewLen = 256
	preview := body
	truncated := false
	if len(preview) > previewLen {
		preview = preview[:previewLen]
		truncated = true
	}
	b.WriteString("hex preview:\n")
	b.WriteString(hexDump(preview))
	if truncated {
		b.WriteString(fmt.Sprintf("\n… %s more", humanizeBytes(len(body)-previewLen)))
	}
	return b.String()
}

// humanizeBytes formats a byte count as B/KB/MB/GB.
func humanizeBytes(n int) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := int64(n) / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

// hexDump renders bytes in the classic `offset  hex  |ascii|` layout.
func hexDump(data []byte) string {
	var b strings.Builder
	for off := 0; off < len(data); off += 16 {
		end := off + 16
		if end > len(data) {
			end = len(data)
		}
		row := data[off:end]

		fmt.Fprintf(&b, "%08x  ", off)
		for i := 0; i < 16; i++ {
			if i < len(row) {
				fmt.Fprintf(&b, "%02x ", row[i])
			} else {
				b.WriteString("   ")
			}
			if i == 7 {
				b.WriteByte(' ')
			}
		}
		b.WriteString(" |")
		for _, c := range row {
			if c >= 0x20 && c < 0x7f {
				b.WriteByte(c)
			} else {
				b.WriteByte('.')
			}
		}
		b.WriteString("|\n")
	}
	return b.String()
}
