package model

import (
	"net/url"
	"strings"
	"unicode/utf8"
)

// SafeAbsoluteHTTPURL returns a normalized public URL only when it is an
// absolute HTTP(S) URL without credentials or control characters.
func SafeAbsoluteHTTPURL(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" || containsUnsafePublicText(value) {
		return "", false
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return "", false
	}
	return value, true
}

// SafeAttachmentSourceURL permits safe absolute URLs and the two stable KRX
// portal-relative endpoints emitted by the producer.
func SafeAttachmentSourceURL(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", true
	}
	if absolute, ok := SafeAbsoluteHTTPURL(value); ok {
		return absolute, true
	}
	if containsUnsafePublicText(value) {
		return "", false
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || parsed.Fragment != "" {
		return "", false
	}
	switch parsed.Path {
	case "/Download.do", "/out/pds/pdsViewPop.do":
		return value, true
	default:
		return "", false
	}
}

// PortableFileName accepts a single portable basename, not a local path.
func PortableFileName(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", true
	}
	if !utf8.ValidString(value) || containsUnsafePublicText(value) || value == "." || value == ".." {
		return "", false
	}
	if strings.ContainsAny(value, `/\<>:"|?*`) {
		return "", false
	}
	return value, true
}

func containsUnsafePublicText(value string) bool {
	lower := strings.ToLower(value)
	if strings.Contains(lower, "%00") || strings.Contains(lower, "%0a") || strings.Contains(lower, "%0d") {
		return true
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}
