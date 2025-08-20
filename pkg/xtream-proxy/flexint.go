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

package xtreamproxy

import (
	"encoding/json"
	"strconv"

	"github.com/lucasduport/stream-share/pkg/utils"
)

// CustomFlexInt is a flexible integer type that can unmarshal from
// JSON string, number, or null/empty values.
type CustomFlexInt int

// UnmarshalJSON implements the json.Unmarshaler interface.
func (fi *CustomFlexInt) UnmarshalJSON(b []byte) error {
	if len(b) == 0 || string(b) == "null" || string(b) == `""` {
		*fi = 0
		return nil
	}

	// Try to unmarshal as integer
	var i int
	if err := json.Unmarshal(b, &i); err == nil {
		*fi = CustomFlexInt(i)
		return nil
	}

	// Try to unmarshal as string containing an integer
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return utils.PrintErrorAndReturn(err)
	}

	// Handle empty string case
	if s == "" {
		*fi = 0
		return nil
	}

	// Parse string as integer
	i, err := strconv.Atoi(s)
	if err != nil {
		utils.DebugLog("Warning: cannot convert %q to integer, defaulting to 0", s)
		*fi = 0
		return nil
	}

	*fi = CustomFlexInt(i)
	return nil
}

// MarshalJSON implements the json.Marshaler interface.
func (fi CustomFlexInt) MarshalJSON() ([]byte, error) {
	return json.Marshal(int(fi))
}

// Int returns the int value of the CustomFlexInt
func (fi CustomFlexInt) Int() int {
	return int(fi)
}
