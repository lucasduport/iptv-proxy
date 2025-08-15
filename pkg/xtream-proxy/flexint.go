package xtreamproxy

import (
	"encoding/json"
	"strconv"

	"github.com/lucasduport/iptv-proxy/pkg/utils"
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
