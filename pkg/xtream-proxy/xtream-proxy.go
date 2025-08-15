/*
 * Iptv-Proxy is a project to proxyfie an m3u file and to proxyfie an Xtream iptv service (client API).
 * Copyright (C) 2020  Pierre-Emmanuel Jacquier
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
    "bytes"
    "context"
    "crypto/tls"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "net/url"
    "strings"
    "time"
    "unicode/utf8"

    "github.com/lucasduport/iptv-proxy/pkg/config"
    "github.com/lucasduport/iptv-proxy/pkg/utils"
)

// API endpoint constants
const (
	getLiveCategories   = "get_live_categories"
	getLiveStreams      = "get_live_streams"
	getVodCategories    = "get_vod_categories"
	getVodStreams       = "get_vod_streams"
	getVodInfo          = "get_vod_info"
	getSeriesCategories = "get_series_categories"
	getSeries           = "get_series"
	getSerieInfo        = "get_series_info"
	getShortEPG         = "get_short_epg"
	getSimpleDataTable  = "get_simple_data_table"
)

// Client represents an Xtream API client
type Client struct {
	Username  string
	Password  string
	BaseURL   string
	UserAgent string
	Client    *http.Client
}

// New creates a new Xtream client instance
func New(user, password, baseURL, userAgent string) (*Client, error) {
	// Validate the base URL
	_, err := url.Parse(baseURL)
	if err != nil {
		return nil, utils.PrintErrorAndReturn(fmt.Errorf("invalid base URL: %w", err))
	}

	// Create HTTP client with standard timeout
	httpClient := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}

	return &Client{
		Username:  user,
		Password:  password,
		BaseURL:   baseURL,
		UserAgent: userAgent,
		Client:    httpClient,
	}, nil
}

// Action executes Xtream API player_api actions using a raw HTTP call and returns parsed JSON or a fallback.
func (c *Client) Action(cfg *config.ProxyConfig, action string, q url.Values) (respBody interface{}, httpcode int, contentType string, err error) {
    contentType = "application/json"
    utils.DebugLog("Processing Xtream action=%s", action)

    // Build upstream URL with credentials
    u, err := url.Parse(strings.TrimRight(c.BaseURL, "/") + "/player_api.php")
    if err != nil {
        return nil, http.StatusInternalServerError, contentType, utils.PrintErrorAndReturn(err)
    }

    params := url.Values{}
    params.Set("username", c.Username)
    params.Set("password", c.Password)
    if strings.TrimSpace(action) != "" {
        params.Set("action", action)
    }
    for k, vs := range q {
        if k == "username" || k == "password" || k == "action" {
            continue
        }
        for _, v := range vs {
            if v == "" {
                continue
            }
            params.Add(k, v)
        }
    }
    u.RawQuery = params.Encode()
    utils.DebugLog("Xtream raw request: %s", u.String())

    client := &http.Client{
        Timeout: 10 * time.Second,
        Transport: &http.Transport{
            TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
        },
    }

    var lastErr error
    var resp *http.Response
    var b []byte

    for i := 0; i < 5; i++ {
        req, err := http.NewRequest("GET", u.String(), nil)
        if err != nil {
            lastErr = err
            continue
        }
        req.Header.Set("User-Agent", "IPTV-Proxy")
        req.Header.Set("Accept", "application/json, text/plain, */*")

        resp, err = client.Do(req)
        if err != nil {
            lastErr = err
            continue
        }
        defer resp.Body.Close()

        if resp.StatusCode == http.StatusOK {
            b, err = io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
            if err != nil {
                lastErr = err
                continue
            }
            break
        } else {
            lastErr = fmt.Errorf("HTTP status %d", resp.StatusCode)
        }
    }

    if resp == nil || resp.StatusCode != http.StatusOK || len(b) == 0 {
        utils.DebugLog("Request failed, last error: %v", lastErr)
        return fallbackForAction(action), http.StatusBadGateway, contentType, lastErr
    }

    trim := bytes.TrimSpace(b)
    if len(trim) == 0 || bytes.Equal(trim, []byte("null")) || (len(trim) > 0 && trim[0] == '<') {
        return fallbackForAction(action), http.StatusOK, contentType, nil
    }
    if bytes.Equal(trim, []byte("{}")) {
        return map[string]interface{}{}, http.StatusOK, contentType, nil
    }
    if bytes.Equal(trim, []byte("[]")) {
        return []interface{}{}, http.StatusOK, contentType, nil
    }

    var result interface{}
    decoder := json.NewDecoder(bytes.NewReader(trim))
    decoder.UseNumber()
    if err := decoder.Decode(&result); err != nil {
        utils.DebugLog("JSON decoding failed: %v", err)
        return fallbackForAction(action), http.StatusOK, contentType, err
    }

    return result, http.StatusOK, contentType, nil
}

// GetXMLTV retrieves the EPG data in XMLTV format
func (c *Client) GetXMLTV() ([]byte, error) {
    // Build URL for EPG data
    u, err := url.Parse(strings.TrimRight(c.BaseURL, "/") + "/xmltv.php")
    if err != nil {
        return nil, utils.PrintErrorAndReturn(err)
    }

    // Add credentials to query
    params := url.Values{}
    params.Set("username", c.Username)
    params.Set("password", c.Password)
    u.RawQuery = params.Encode()

    utils.DebugLog("XMLTV request: %s", u.String())

    // Create context with timeout
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()
    
    // Create request
    req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
    if err != nil {
        return nil, utils.PrintErrorAndReturn(err)
    }

    // Set appropriate headers
    req.Header.Set("User-Agent", c.UserAgent)
    req.Header.Set("Accept", "application/xml, text/xml")
    
    // Execute request
    resp, err := c.Client.Do(req)
    if err != nil {
        return nil, utils.PrintErrorAndReturn(err)
    }
    defer resp.Body.Close()
    
    // Check status code
    if resp.StatusCode != http.StatusOK {
        return nil, utils.PrintErrorAndReturn(fmt.Errorf("unexpected status code: %d", resp.StatusCode))
    }
    
    // Read response with a limit to prevent memory issues (50MB should be enough for most EPG data)
    limitedReader := &io.LimitedReader{R: resp.Body, N: 50 * 1024 * 1024}
    xmlData, err := io.ReadAll(limitedReader)
    if err != nil {
        return nil, utils.PrintErrorAndReturn(fmt.Errorf("failed to read XMLTV data: %w", err))
    }
    
    return xmlData, nil
}

// max returns the maximum of two integers
func max(a, b int) int {
    if a > b {
        return a
    }
    return b
}

// replaceAllNonBasicChars replaces all non-basic ASCII characters with safe equivalents
// This is a last resort for dealing with extremely problematic JSON
func replaceAllNonBasicChars(input []byte) []byte {
    // Try to detect if it's supposed to be an array
    isArray := false
    if len(input) > 0 && input[0] == '[' {
        isArray = true
    }
    
    // First, ensure we're dealing with valid UTF-8
    validUTF8 := make([]byte, 0, len(input))
    for len(input) > 0 {
        r, size := utf8.DecodeRune(input)
        if r == utf8.RuneError {
            input = input[1:] // Skip invalid byte
        } else {
            char := []byte(string(r))
            validUTF8 = append(validUTF8, char...)
            input = input[size:]
        }
    }
    
    // Now clean the string more aggressively
    s := string(validUTF8)
    var result strings.Builder
    
    // If it's an array, start with [
    if isArray {
        result.WriteString("[")
    }
    
    inString := false
    inObject := false
    objectCount := 0
    
    for i, r := range s {
        switch {
        case r == '"':
            // Handle quotes
            if i > 0 && s[i-1] != '\\' {
                inString = !inString
            }
            result.WriteRune(r)
        case r == '{':
            // Start of object
            if !inString {
                inObject = true
                objectCount++
                result.WriteRune(r)
            } else if inString {
                // Replace with space in strings
                result.WriteRune(' ')
            }
        case r == '}':
            // End of object
            if !inString && inObject {
                objectCount--
                if objectCount == 0 {
                    inObject = false
                }
                result.WriteRune(r)
            } else if inString {
                // Replace with space in strings
                result.WriteRune(' ')
            }
        case inString:
            // In strings, replace non-ASCII with spaces
            if r < 32 || r > 126 {
                result.WriteRune(' ')
            } else {
                result.WriteRune(r)
            }
        default:
            // Outside strings, only keep JSON syntax chars
            if r == '[' || r == ']' || r == ',' || r == ':' || 
               r == 't' || r == 'r' || r == 'u' || r == 'e' || r == 'f' || r == 'a' || r == 'l' || r == 's' || 
               r == 'n' || r == 'u' || r == 'l' || (r >= '0' && r <= '9') || 
               r == '-' || r == '.' || r == ' ' {
                result.WriteRune(r)
            }
        }
    }
    
    // If it's an array and we didn't end it, add closing bracket
    if isArray && !strings.HasSuffix(result.String(), "]") {
        result.WriteString("]")
    }
    
    s = result.String()
    
    // Fix common structural issues
    s = strings.ReplaceAll(s, ",]", "]")
    s = strings.ReplaceAll(s, ",}", "}")
    s = strings.ReplaceAll(s, ",,", ",")
    s = strings.ReplaceAll(s, "::", ":")
    
    return []byte(s)
}

// createEmergencyCategoryData creates a minimal set of valid categories as an emergency fallback
func createEmergencyCategoryData() []map[string]interface{} {
    utils.DebugLog("Creating emergency fallback category data")
    
    // Create one dummy category to avoid client errors
    return []map[string]interface{}{
        {
            "category_id": "1",
            "category_name": "Default Category",
            "parent_id": "0",
        },
    }
}

// sanitizeJSON performs basic sanitization of JSON strings to help with parsing
// Specifically targets issues with escaped Unicode characters
func sanitizeJSON(input string) string {
    // Replace problematic escapes with their proper JSON escapes
    // These are common issues seen in Xtream provider responses
    result := input
    
    // Handle invalid slash escapes
    result = strings.ReplaceAll(result, "\\/", "/")
    
    // Remove any null bytes that might have been introduced
    result = strings.ReplaceAll(result, "\u0000", "")
    
    // If response starts with [ and ends with ], it's likely an array
    // Make sure we don't have trailing commas (which are invalid in JSON)
    if strings.HasPrefix(result, "[") && strings.HasSuffix(result, "]") {
        // Replace any pattern of comma followed by closing bracket
        result = strings.ReplaceAll(result, ",]", "]")
    }
    
    return result
}

// sanitizeUnicodeJSON sanitizes JSON containing problematic Unicode characters
// that often cause parsing issues with Xtream providers.
// Returns a cleaned version of the JSON byte array.
func sanitizeUnicodeJSON(input []byte) []byte {
    // Early return for empty input
    if len(input) == 0 {
        return input
    }
    
    result := string(input)
    originalLen := len(result)
    utils.DebugLog("Sanitizing JSON: original length %d bytes", originalLen)
    
    // Step 1: Remove BOM and null bytes
    result = removeProblematicCharacters(result)
    
    // Step 2: Fix common JSON syntax errors
    result = fixJsonSyntaxErrors(result)
    
    // Step 3: Fix Unicode quote issues
    result = normalizeQuotes(result)
    
    // Step 4: Ensure UTF-8 validity
    result = fixBrokenUTF8(result)
    
    // Step 5: Balance brackets and braces
    result = balanceBracketsAndBraces(result)
    
    utils.DebugLog("Sanitizing complete: new length %d bytes (%d%% of original)",
        len(result), (len(result) * 100 / max(1, originalLen)))
    
    return []byte(result)
}

// removeProblematicCharacters removes common problematic characters from JSON
func removeProblematicCharacters(s string) string {
    // Remove BOM if present
    s = strings.TrimPrefix(s, "\uFEFF")
    
    // Remove null bytes and normalize slashes
    s = strings.ReplaceAll(s, "\u0000", "")
    s = strings.ReplaceAll(s, "\\/", "/")
    
    // Remove control characters except whitespace
    for i := 0; i < 32; i++ {
        if i != 9 && i != 10 && i != 13 { // Keep tabs, newlines, and carriage returns
            s = strings.ReplaceAll(s, string(rune(i)), "")
        }
    }
    
    return s
}

// fixJsonSyntaxErrors fixes common JSON syntax errors
func fixJsonSyntaxErrors(s string) string {
    // Fix trailing commas
    s = strings.ReplaceAll(s, ",]", "]")
    s = strings.ReplaceAll(s, ",}", "}")
    
    // Fix double commas and colons
    s = strings.ReplaceAll(s, ",,", ",")
    s = strings.ReplaceAll(s, "::", ":")
    
    return s
}

// normalizeQuotes replaces various Unicode quote types with standard JSON double quotes
func normalizeQuotes(s string) string {
    replacements := map[string]string{
        "“": "\"",
        "”": "\"",
        "‘": "'",
        "’": "'",
        "«": "\"",
        "»": "\"",
    }

    for from, to := range replacements {
        s = strings.ReplaceAll(s, from, to)
    }

    return s
}

// balanceBracketsAndBraces ensures JSON has balanced brackets and braces
func balanceBracketsAndBraces(s string) string {
    // Count opening and closing brackets
    openBrackets := strings.Count(s, "[")
    closeBrackets := strings.Count(s, "]")
    
    // Add missing closing brackets if needed
    for i := 0; i < openBrackets-closeBrackets; i++ {
        s += "]"
        utils.DebugLog("Added missing closing bracket ]")
    }
    
    // Count opening and closing braces
    openBraces := strings.Count(s, "{")
    closeBraces := strings.Count(s, "}")
    
    // Add missing closing braces if needed
    for i := 0; i < openBraces-closeBraces; i++ {
        s += "}"
        utils.DebugLog("Added missing closing brace }")
    }
    
    return s
}

// fixBrokenUTF8 attempts to fix broken UTF-8 sequences
func fixBrokenUTF8(s string) string {
    // First convert to valid UTF-8 (replacing invalid sequences with the Unicode replacement character)
    valid := []rune(s)
    
    // Convert back to string
    return string(valid)
}

// sanitizeAggressively performs more aggressive sanitization for really problematic JSON
func sanitizeAggressively(input []byte) []byte {
    // Convert to string for easier manipulation
    s := string(input)
    
    // Create a new buffer for the sanitized content
    var result strings.Builder
    
    // Process the input character by character
    inString := false
    escaped := false
    
    for _, r := range s {
        switch {
        case escaped:
            // If we're in escaped mode, add the current character regardless
            result.WriteRune(r)
            escaped = false
        case r == '\\':
            // Start of an escape sequence
            result.WriteRune(r)
            escaped = true
        case r == '"':
            // Toggle string mode
            inString = !inString
            result.WriteRune(r)
        case inString:
            // In a string, allow all characters except control characters
            if r >= 32 || r == '\t' || r == '\n' || r == '\r' {
                result.WriteRune(r)
            }
        case r >= 32 || r == '\t' || r == '\n' || r == '\r':
            // Outside strings, only allow whitespace and basic JSON syntax
            if r == '{' || r == '}' || r == '[' || r == ']' || 
               r == ',' || r == ':' || r == 't' || r == 'f' || 
               r == 'n' || r == 'u' || r == 'l' || (r >= '0' && r <= '9') || 
               r == '-' || r == '.' || r == '+' || r == 'e' || r == 'E' || 
               r == ' ' || r == '\t' || r == '\n' || r == '\r' {
                result.WriteRune(r)
            }
        }
    }
    
    // Final sanitization to fix any remaining JSON syntax issues
    sanitized := result.String()
    sanitized = strings.ReplaceAll(sanitized, ",]", "]")
    sanitized = strings.ReplaceAll(sanitized, ",}", "}")
    
    return []byte(sanitized)
}

// extractValidCategoryData attempts to manually extract category data from malformed JSON
// Returns the extracted categories and a boolean indicating success
func extractValidCategoryData(input []byte) ([]map[string]interface{}, bool) {
    // Convert to string for pattern matching
    data := string(input)

    // Check if it starts with [ and ends with ]
    if !strings.HasPrefix(data, "[") || !strings.HasSuffix(data, "]") {
        return nil, false
    }

    // Remove the outer brackets
    data = data[1 : len(data)-1]

    // Split by object separators
    parts := strings.Split(data, "},{")
    result := make([]map[string]interface{}, 0, len(parts))

    for i, part := range parts {
        // Restore the brackets
        if i == 0 && !strings.HasPrefix(part, "{") {
            part = "{" + part
        } else if i > 0 {
            part = "{" + part
        }
        if i == len(parts)-1 && !strings.HasSuffix(part, "}") {
            part = part + "}"
        } else if i < len(parts)-1 {
            part = part + "}"
        }

        var item map[string]interface{}
        // Try to parse each object
        if err := json.Unmarshal([]byte(part), &item); err == nil {
            // Check for minimum required fields
            if _, hasID := item["category_id"]; hasID {
                if name, hasName := item["category_name"].(string); hasName && name != "" {
                    result = append(result, item)
                }
            }
        }
    }

    return result, len(result) > 0
}

// fallbackForAction returns a reasonable empty structure for each action
// to avoid 500 when providers return empty/invalid JSON.
func fallbackForAction(action string) interface{} {
	switch action {
	case getLiveCategories, getVodCategories, getSeriesCategories,
		getLiveStreams, getVodStreams, getSeries:
		return []interface{}{}
	case getVodInfo, getSerieInfo:
		return map[string]interface{}{}
	case getShortEPG:
		// Compatible with expected EPG container
		return map[string]interface{}{"epg_listings": []interface{}{}}
	case getSimpleDataTable:
		// Some providers return arrays, some objects; safe empty object
		return map[string]interface{}{}
	default:
		return map[string]interface{}{}
	}
}

// min helper for getting the minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}