/*
 * stream-share is a project to efficiently share the use of an IPTV service.
 * Copyright (C) 2025  Lucas Duport
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 *
 * You should have received a copy of the GNU General Public License
 * along with this program.  If not, see <https://www.gnu.org/licenses/>.
 */
 
package utils

import (
	"fmt"
	"strings"
)

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

// HumanBytes formats a byte count into a short, human-friendly string (e.g., 1.2 GB)
func HumanBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	pre := []string{"KB", "MB", "GB", "TB", "PB", "EB"}
	if exp >= len(pre) {
		exp = len(pre) - 1
	}
	return fmt.Sprintf("%.1f %s", float64(b)/float64(div), pre[exp])
}
