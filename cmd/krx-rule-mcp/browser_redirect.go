package main

import (
	"mime"
	"net/http"
	"strconv"
	"strings"
)

const projectURL = "https://github.com/chromato99/krx-rule-mcp"

// This handler stays inside the existing authentication and Origin checks.
// Only explicit browser HTML requests are redirected; MCP traffic reaches the SDK.
func withBrowserProjectRedirect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			// A cached GET response must not change how an MCP client reaches us.
			w.Header().Add("Vary", "Accept")
			w.Header().Set("Cache-Control", "no-store")
			if isBrowserHTMLRequest(r) {
				// Do not forward endpoint query parameters through Location or Referer.
				w.Header().Set("Referrer-Policy", "no-referrer")
				http.Redirect(w, r, projectURL, http.StatusFound)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func isBrowserHTMLRequest(r *http.Request) bool {
	for _, name := range []string{"MCP-Protocol-Version", "MCP-Session-Id", "Last-Event-ID"} {
		if len(r.Header.Values(name)) > 0 {
			return false
		}
	}
	if mode := r.Header.Get("Sec-Fetch-Mode"); mode != "" && mode != "navigate" {
		return false
	}
	acceptsHTML := false
	for _, value := range r.Header.Values("Accept") {
		for _, item := range strings.Split(value, ",") {
			mediaType, params, err := mime.ParseMediaType(strings.TrimSpace(item))
			if err != nil || !strings.Contains(mediaType, "/") {
				return false
			}
			// Prefer MCP handling even when HTML is also listed or has higher quality.
			if mediaType == "text/event-stream" || mediaType == "application/json" {
				return false
			}
			quality := 1.0
			if raw, ok := params["q"]; ok {
				quality, err = strconv.ParseFloat(raw, 64)
				if err != nil || !(quality >= 0 && quality <= 1) {
					return false
				}
			}
			if mediaType == "text/html" && quality > 0 {
				acceptsHTML = true
			}
		}
	}
	return acceptsHTML
}
