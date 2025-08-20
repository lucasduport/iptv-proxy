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
