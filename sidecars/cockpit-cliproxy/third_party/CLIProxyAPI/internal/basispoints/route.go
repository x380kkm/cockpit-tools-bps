package basispoints

import "strings"

// IsDataURL reports whether an image reference embeds its bytes inline.
func IsDataURL(raw string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(raw)), "data:")
}
