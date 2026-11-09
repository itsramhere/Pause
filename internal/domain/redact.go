package domain

import "regexp"

// privateAddresses is the single shared PII pattern for untrusted free text.
// Every outbound disclosure surface (private search excerpts, LLM prompts)
// must redact URLs, emails and phone numbers through RedactPrivateAddresses
// instead of keeping its own regex.
var privateAddresses = regexp.MustCompile(`(?i)https?://\S+|[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}|\+?\d[\d ()-]{6,}\d`)

// RedactPrivateAddresses replaces direct contact channels with "[redacted]".
func RedactPrivateAddresses(text string) string {
	return privateAddresses.ReplaceAllString(text, "[redacted]")
}
