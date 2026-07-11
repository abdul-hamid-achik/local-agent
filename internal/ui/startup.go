package ui

import "strings"

// sanitizeStartupDetail keeps transport and server errors from injecting
// multiline/unbounded content into the startup screen. Full failures remain
// available in the durable system entry and logs after initialization.
func sanitizeStartupDetail(detail string) string {
	detail = strings.TrimSpace(detail)
	if detail == "" {
		return ""
	}
	if detail[0] == '{' || detail[0] == '[' {
		return "details available in logs"
	}
	detail = strings.NewReplacer("\n", " ", "\r", " ", "\t", " ").Replace(detail)
	return strings.Join(strings.Fields(detail), " ")
}
