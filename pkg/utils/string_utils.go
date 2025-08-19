package utils

import "strings"

// MaskString masks sensitive parts of strings for logging.
func MaskString(s string) string {
	if len(s) <= 8 {
		if len(s) <= 0 {
			return "[empty]"
		}
		return s[:1] + "******"
	}
	return s[:4] + "..." + s[len(s)-4:]
}

// MaskURL masks sensitive parts of URLs for logging.
// It follows the same logic as the original server package helper.
func MaskURL(urlStr string) string {
	parts := strings.Split(urlStr, "/")
	if len(parts) >= 7 {
		// For URLs like http://host/path/user/pass/id
		parts[5] = MaskString(parts[5]) // Password
		parts[4] = MaskString(parts[4]) // Username
	}
	return strings.Join(parts, "/")
}
