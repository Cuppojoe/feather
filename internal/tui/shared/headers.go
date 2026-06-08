package shared

import (
	"fmt"
	"strings"
)

// sensitiveHeaders is the canonical (lowercase) set of header names whose
// values get redacted in the request/response detail views. Keep this list
// conservative — we'd rather hide one that doesn't need it than leak a
// bearer token onto a shared screen.
var sensitiveHeaders = map[string]struct{}{
	"authorization":       {},
	"proxy-authorization": {},
	"cookie":              {},
	"set-cookie":          {},
	"x-api-key":           {},
	"x-api-token":         {},
	"api-key":             {},
	"x-auth-token":        {},
	"x-csrf-token":        {},
	"x-csrftoken":         {},
	"x-xsrf-token":        {},
}

// IsSensitiveHeader reports whether a header name should have its value
// redacted in display. Comparison is case-insensitive (per RFC 7230 §3.2).
func IsSensitiveHeader(name string) bool {
	_, ok := sensitiveHeaders[strings.ToLower(strings.TrimSpace(name))]
	return ok
}

// RedactHeaderValue returns a display-safe rendering of a sensitive header.
// For schemes that travel as "<scheme> <token>" (Authorization,
// Proxy-Authorization) it keeps the scheme visible so the user can still
// confirm Bearer vs Basic vs Digest, and replaces the token with bullets +
// a length hint. Other sensitive headers are fully bulleted. Non-sensitive
// header names pass through unchanged.
func RedactHeaderValue(name, value string) string {
	if !IsSensitiveHeader(name) {
		return value
	}
	if value == "" {
		return value
	}

	canon := strings.ToLower(strings.TrimSpace(name))
	if canon == "authorization" || canon == "proxy-authorization" {
		// "<scheme> <token>" — keep the scheme, redact the rest.
		if sp := strings.IndexByte(value, ' '); sp > 0 {
			scheme := value[:sp]
			token := strings.TrimSpace(value[sp+1:])
			return fmt.Sprintf("%s %s", scheme, redactedBlob(token))
		}
		// No space — treat the whole thing as opaque.
		return redactedBlob(value)
	}
	return redactedBlob(value)
}

// redactedBlob renders the placeholder shown in place of a secret value:
// "•••••••• (N chars)". The length hint makes mismatched secrets easier to
// spot without revealing the value.
func redactedBlob(s string) string {
	n := len(s)
	if n == 0 {
		return ""
	}
	const minBullets = 8
	bullets := n
	if bullets > 12 {
		bullets = 12
	}
	if bullets < minBullets {
		bullets = minBullets
	}
	return fmt.Sprintf("%s (%d chars)", strings.Repeat("•", bullets), n)
}
