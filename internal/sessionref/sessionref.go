// Package sessionref formats and parses short, user-facing session handles.
// Durable storage and JSON continue to use the underlying positive int64 ID.
package sessionref

import (
	"fmt"
	"strconv"
)

const prefix = "S"

// Format returns the short display handle for a positive session ID.
func Format(id int64) string {
	if id <= 0 {
		return ""
	}
	return prefix + strconv.FormatInt(id, 10)
}

// Parse accepts either a positive decimal ID or its case-insensitive S-prefixed
// handle. The returned value is always the authoritative numeric session ID.
func Parse(value string) (int64, error) {
	original := value
	if len(value) > 0 && (value[0] == 's' || value[0] == 'S') {
		value = value[1:]
	}
	if value == "" || value[0] == '0' {
		return 0, fmt.Errorf("invalid session reference %q", original)
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return 0, fmt.Errorf("invalid session reference %q", original)
		}
	}
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != value {
		return 0, fmt.Errorf("invalid session reference %q", original)
	}
	return id, nil
}
