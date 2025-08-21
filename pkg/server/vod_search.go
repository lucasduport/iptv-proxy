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
 
 package server

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"regexp"

	"github.com/lucasduport/stream-share/pkg/types"
	"github.com/lucasduport/stream-share/pkg/utils"
	xtreamapi "github.com/lucasduport/stream-share/pkg/xtream-proxy"
)

var vodM3UMu sync.Mutex

// searchXtreamVOD is a helper function to search for VOD content using a cached M3U file
func (c *Config) searchXtreamVOD(query string) ([]types.VODResult, error) {
	utils.DebugLog("Searching VOD with query: %s", query)

	// Validate Xtream configuration
	if c.XtreamBaseURL == "" || c.XtreamUser.String() == "" || c.XtreamPassword.String() == "" {
		utils.ErrorLog("Xtream configuration is incomplete")
		return nil, fmt.Errorf("xtream configuration is incomplete")
	}

	// Ensure local cached M3U exists and is fresh
	m3uPath, err := c.ensureVODM3UCache()
	if err != nil {
		return nil, fmt.Errorf("failed to prepare VOD M3U cache: %w", err)
	}

	// Search inside the local M3U file for movie entries
	results, err := searchVODInM3UFile(m3uPath, query)
	if err != nil {
		return nil, fmt.Errorf("failed to search VOD in M3U: %w", err)
	}

	// Also search series (flatten episodes) using Xtream API for better episode discovery
	seriesResults, err := c.searchXtreamSeries(query)
	if err == nil && len(seriesResults) > 0 {
		results = append(results, seriesResults...)
	}
	// And scan local M3U for series entries as a fallback (provider APIs can be flaky)
	if sres, err := searchSeriesInM3UFile(m3uPath, query); err == nil && len(sres) > 0 {
		results = append(results, sres...)
	}

	// Best-effort: probe size for first few results to display in Discord
	// Avoid hammering provider: limit to 10 probes with small timeout
	maxProbe := 10
	if len(results) < maxProbe {
		maxProbe = len(results)
	}
	client := &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error { if len(via) >= 10 { return http.ErrUseLastResponse }; return nil }}
	for i := 0; i < maxProbe; i++ {
		streamID := results[i].StreamID
		if streamID == "" {
			continue
		}
		// Build Xtream URL by type
		typ := results[i].StreamType
		if typ == "" {
			typ = "movie"
		}
		basePath := "movie"
		if typ == "series" {
			basePath = "series"
		}
		vodURL := fmt.Sprintf("%s/%s/%s/%s/%s", c.XtreamBaseURL, basePath, c.XtreamUser, c.XtreamPassword, streamID)
		// Prefer GET with Range (providers often block HEAD); allow redirects to capture tokenized location
		req, _ := http.NewRequest("GET", vodURL, nil)
		req.Header.Set("Range", "bytes=0-0")
		req.Header.Set("User-Agent", utils.GetIPTVUserAgent())
		req.Header.Set("Accept-Encoding", "identity")
		if resp, err := client.Do(req); err == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			// If 3xx with Location, ignore size but this confirms URL is valid
			if cr := resp.Header.Get("Content-Range"); cr != "" {
				if total := strings.TrimSpace(cr[strings.LastIndex(cr, "/")+1:]); total != "*" {
					if sz, perr := parseInt64(total); perr == nil && sz > 0 {
						results[i].SizeBytes = sz
						results[i].Size = utils.HumanBytes(sz)
					}
				}
			}
		}
	}

	// Sort results by title for stable ordering
	sort.SliceStable(results, func(i, j int) bool { return strings.ToLower(results[i].Title) < strings.ToLower(results[j].Title) })
	utils.DebugLog("VOD search returned %d results for query: %s", len(results), query)
	return results, nil
}

func (c *Config) ensureVODM3UCache() (string, error) {
	vodM3UMu.Lock()
	defer vodM3UMu.Unlock()

	// Cache directory preference: CACHE_FOLDER env or temp dir
	cacheDir := os.Getenv("CACHE_FOLDER")
	if cacheDir == "" {
		cacheDir = filepath.Join(os.TempDir(), ".stream-share")
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", err
	}

	cacheFile := filepath.Join(cacheDir, "vod_cache.m3u")

	// Check freshness vs. configured M3U cache expiration (hours)
	expHours := c.M3UCacheExpiration
	info, err := os.Stat(cacheFile)
	if err == nil {
		age := time.Since(info.ModTime())
		if age.Hours() < float64(expHours) {
			utils.DebugLog("Using cached VOD M3U: %s (age: %v)", cacheFile, age)
			return cacheFile, nil
		}
	}

	// Fetch latest M3U (m3u_plus includes movie entries)
	getURL := fmt.Sprintf("%s/get.php?username=%s&password=%s&type=m3u_plus&output=m3u8",
		c.XtreamBaseURL, c.XtreamUser.String(), c.XtreamPassword.String())

	utils.InfoLog("Refreshing VOD M3U from Xtream: %s", utils.MaskURL(getURL))

	req, err := http.NewRequest("GET", getURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", utils.GetIPTVUserAgent())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("backend returned %d for M3U request", resp.StatusCode)
	}

	f, err := os.Create(cacheFile)
	if err != nil {
		return "", err
	}
	defer f.Close()

	if _, err := io.Copy(f, resp.Body); err != nil {
		return "", err
	}

	utils.InfoLog("Stored VOD M3U to %s", cacheFile)
	return cacheFile, nil
}

func searchVODInM3UFile(m3uPath string, query string) ([]types.VODResult, error) {
	f, err := os.Open(m3uPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	q := strings.TrimSpace(query)
	sc := bufio.NewScanner(f)
	lastEXTINF := ""
	results := make([]types.VODResult, 0, 50)

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#EXTINF:") {
			lastEXTINF = line
			continue
		}
		// URL lines
		if strings.HasPrefix(line, "http://") || strings.HasPrefix(line, "https://") {
			u, err := url.Parse(line)
			if err != nil {
				continue
			}
			// Only consider movie entries
			if !strings.Contains(u.Path, "/movie/") {
				continue
			}

			// Title: take from EXTINF after the comma
			title := ""
			category := ""
			if lastEXTINF != "" {
				if idx := strings.LastIndex(lastEXTINF, ","); idx != -1 && idx+1 < len(lastEXTINF) {
					title = strings.TrimSpace(lastEXTINF[idx+1:])
				}
				// Extract group-title="..."
				attrs := lastEXTINF
				if i := strings.Index(attrs, " "); i != -1 {
					attrs = attrs[i+1:]
				}
				const key = `group-title="`
				if pos := strings.Index(attrs, key); pos != -1 {
					start := pos + len(key)
					if end := strings.Index(attrs[start:], `"`); end != -1 {
						category = attrs[start : start+end]
					}
				}
			}
			if title == "" {
				title = path.Base(u.Path)
			}

			// Filter by query if provided: simple all-words contains (case-insensitive)
			if q != "" && !simpleAllWordsContains(q, title) {
				continue
			}

			// StreamID is the last path segment
			streamID := path.Base(u.Path)

			results = append(results, types.VODResult{
				ID:       streamID,
				Title:    title,
				Category: category,
				Duration: "",
				Year:     "",
				Rating:   "",
				StreamID: streamID,
				StreamType: "movie",
				SizeBytes: 0,
				Size:      "",
			})

			// Reset lastEXTINF after pairing with URL
			lastEXTINF = ""
			if len(results) >= 50 {
				break
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

// parseInt64 converts string to int64, ignoring commas/spaces
func parseInt64(s string) (int64, error) {
	s = strings.TrimSpace(s)
	// Remove thousands separators if any
	s = strings.ReplaceAll(s, ",", "")
	var n int64
	var err error
	// fast path
	n, err = strconv.ParseInt(s, 10, 64)
	return n, err
}

// simpleAllWordsContains: split query by spaces and ensure each word is contained (case-insensitive) in text.
func simpleAllWordsContains(query, text string) bool {
	q := strings.TrimSpace(query)
	if q == "" { return true }
	t := strings.ToLower(text)
	for _, w := range strings.Fields(strings.ToLower(q)) {
		if !strings.Contains(t, w) { return false }
	}
	return true
}


// searchSeriesInM3UFile scans the cached M3U and returns episodic entries as series results.
func searchSeriesInM3UFile(m3uPath string, query string) ([]types.VODResult, error) {
	f, err := os.Open(m3uPath)
	if err != nil { return nil, err }
	defer f.Close()

	q := strings.TrimSpace(query)
	sc := bufio.NewScanner(f)
	lastEXTINF := ""
	results := make([]types.VODResult, 0, 50)
	// Regexes to extract S/E and split title
	reSE := regexp.MustCompile(`(?i)\bS(\d{1,2})\s*[EExx×](\d{1,2})\b`)

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" { continue }
		if strings.HasPrefix(line, "#EXTINF:") { lastEXTINF = line; continue }
		if strings.HasPrefix(line, "http://") || strings.HasPrefix(line, "https://") {
			u, err := url.Parse(line); if err != nil { continue }
			if !strings.Contains(u.Path, "/series/") { continue }
			title := ""; category := ""
			if lastEXTINF != "" {
				if idx := strings.LastIndex(lastEXTINF, ","); idx != -1 && idx+1 < len(lastEXTINF) {
					title = strings.TrimSpace(lastEXTINF[idx+1:])
				}
				attrs := lastEXTINF
				if i := strings.Index(attrs, " "); i != -1 { attrs = attrs[i+1:] }
				const key = `group-title="`
				if pos := strings.Index(attrs, key); pos != -1 { start := pos + len(key); if end := strings.Index(attrs[start:], `"`); end != -1 { category = attrs[start:start+end] } }
			}
			if title == "" { title = path.Base(u.Path) }
			if q != "" && !simpleAllWordsContains(q, title) { lastEXTINF = ""; continue }
			// Extract S/E
			season, episode := 0, 0
			if m := reSE.FindStringSubmatch(title); m != nil {
				season, _ = strconv.Atoi(m[1])
				episode, _ = strconv.Atoi(m[2])
			}
			seriesTitle := title
			if i := reSE.FindStringIndex(seriesTitle); i != nil { seriesTitle = strings.TrimSpace(strings.Trim(seriesTitle[:i[0]], "-—–:|• ")) }
			// StreamID is the last path segment
			streamID := path.Base(u.Path)
			results = append(results, types.VODResult{
				ID:           streamID,
				Title:        title,
				Category:     category,
				Duration:     "",
				Year:         "",
				Rating:       "",
				StreamID:     streamID,
				StreamType:   "series",
				SeriesTitle:  seriesTitle,
				Season:       season,
				Episode:      episode,
				EpisodeTitle: "",
			})
			lastEXTINF = ""
		}
	}
	if err := sc.Err(); err != nil { return nil, err }
	return results, nil
}

// searchXtreamSeries searches series and flattens episodes matching the query
func (c *Config) searchXtreamSeries(query string) ([]types.VODResult, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, nil
	}
	// Use resilient client to avoid FlexInt unmarshaling issues
	utils.DebugLog("Series search: using resilient Xtream client (baseURL=%s, user=%s)", c.XtreamBaseURL, utils.MaskString(c.XtreamUser.String()))
	cli, err := xtreamapi.New(c.XtreamUser.String(), c.XtreamPassword.String(), c.XtreamBaseURL, utils.GetIPTVUserAgent())
	if err != nil {
		utils.WarnLog("Series search: failed to create resilient client: %v", err)
		return nil, err
	}

	resp, httpcode, contentType, err := cli.Action(c.ProxyConfig, "get_series", url.Values{})
	if err != nil {
		utils.WarnLog("Series search: get_series failed (HTTP %d, CT=%s): %v", httpcode, contentType, err)
		return nil, err
	}

	arr, ok := resp.([]interface{})
	if !ok {
		utils.WarnLog("Series search: unexpected get_series format: %T", resp)
		return nil, fmt.Errorf("unexpected get_series format: %T", resp)
	}

	out := make([]types.VODResult, 0, 50)
	for _, item := range arr {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		seriesName := fmt.Sprintf("%v", m["name"]) 
		if seriesName == "" {
			continue
		}
	if !simpleAllWordsContains(q, seriesName) {
			continue
		}
		seriesID := fmt.Sprintf("%v", m["series_id"])
		if seriesID == "" || seriesID == "<nil>" {
			continue
		}
		genre := fmt.Sprintf("%v", m["genre"]) // may be empty
		year := fmt.Sprintf("%v", firstNonEmpty(m["releaseDate"], m["release_date"]))

		utils.DebugLog("Series search: fetching series info for '%s' (series_id=%s)", seriesName, seriesID)
		infoResp, httpcode, contentType, err := cli.Action(c.ProxyConfig, "get_series_info", url.Values{"series_id": {seriesID}})
		if err != nil {
			utils.WarnLog("Series search: get_series_info failed for id=%s: %v (HTTP %d, CT=%s)", seriesID, err, httpcode, contentType)
			continue
		}
		im, ok := infoResp.(map[string]interface{})
		if !ok {
			utils.WarnLog("Series search: unexpected series_info format for id=%s: %T", seriesID, infoResp)
			continue
		}
		epsBySeason, ok := im["episodes"].(map[string]interface{})
		if !ok {
			// Some providers use episodes as array with season inside
			continue
		}
		for seasonStr, epsV := range epsBySeason {
			seasonNum, _ := strconv.Atoi(seasonStr)
			eps, ok := epsV.([]interface{})
			if !ok {
				continue
			}
			for _, e := range eps {
				em, ok := e.(map[string]interface{})
				if !ok {
					continue
				}
				title := fmt.Sprintf("%v", em["title"])
				if !simpleAllWordsContains(q, title) && !simpleAllWordsContains(q, seriesName) {
					continue
				}
				streamID := fmt.Sprintf("%v", firstNonEmpty(em["id"], em["stream_id"]))
				if streamID == "" || streamID == "<nil>" {
					continue
				}
				epNum := toInt(em["episode_num"]) // best-effort
				// info subobject for duration/rating
				var duration, rating string
				if infoSub, ok := em["info"].(map[string]interface{}); ok {
					duration = fmt.Sprintf("%v", infoSub["duration"]) // may be ""
					rating = fmt.Sprintf("%v", firstNonEmpty(infoSub["rating"], infoSub["vote_average"]))
				}

				out = append(out, types.VODResult{
					ID:           streamID,
					Title:        fmt.Sprintf("%s S%02dE%02d — %s", seriesName, seasonNum, epNum, title),
					Category:     genre,
					Duration:     duration,
					Year:         year,
					Rating:       rating,
					StreamID:     streamID,
					StreamType:   "series",
					SeriesTitle:  seriesName,
					Season:       seasonNum,
					Episode:      epNum,
					EpisodeTitle: title,
				})
				if len(out) >= 50 {
					return out, nil
				}
			}
		}
	}
	utils.DebugLog("Series search: returning %d results", len(out))
	return out, nil
}

// logRawXtreamSeriesDiagnostics performs raw calls to Xtream API to collect JSON payloads
// for series and a matching series_info to help diagnose unmarshaling issues in third-party clients.
func (c *Config) logRawXtreamSeriesDiagnostics(q string) {
	// Create our resilient client that parses into generic interfaces
	cli, err := xtreamapi.New(c.XtreamUser.String(), c.XtreamPassword.String(), c.XtreamBaseURL, utils.GetIPTVUserAgent())
	if err != nil {
		utils.WarnLog("Diagnostics: failed to create raw Xtream client: %v", err)
		return
	}

	// Fetch series list
	resp, httpcode, contentType, err := cli.Action(c.ProxyConfig, "get_series", url.Values{})
	if err != nil {
		utils.WarnLog("Diagnostics: get_series failed: %v (HTTP %d, CT=%s)", err, httpcode, contentType)
		return
	}
	// Write raw to file if debugging enabled
	if b, ok := tryJSONMarshal(resp); ok {
		filename := fmt.Sprintf("series_raw_%s.json", time.Now().Format("20060102_150405"))
		utils.WriteResponseToFile(filename, b, contentType)
	}

	// Try to find one series matching q and fetch its info
	var seriesID string
	switch arr := resp.(type) {
	case []interface{}:
		for _, item := range arr {
			m, ok := item.(map[string]interface{})
			if !ok { continue }
			name := fmt.Sprintf("%v", m["name"])
			if name == "" { continue }
			if strings.Contains(strings.ToLower(name), q) {
				seriesID = fmt.Sprintf("%v", m["series_id"])
				utils.DebugLog("Diagnostics: matched series '%s' with id=%s", name, seriesID)
				break
			}
		}
	}
	if seriesID == "" {
		utils.DebugLog("Diagnostics: no matching series found for query %q to fetch series_info", q)
		return
	}
	infoResp, httpcode, contentType, err := cli.Action(c.ProxyConfig, "get_series_info", url.Values{"series_id": {seriesID}})
	if err != nil {
		utils.WarnLog("Diagnostics: get_series_info failed for id=%s: %v (HTTP %d, CT=%s)", seriesID, err, httpcode, contentType)
		return
	}
	if b, ok := tryJSONMarshal(infoResp); ok {
		filename := fmt.Sprintf("series_info_%s_%s.json", seriesID, time.Now().Format("20060102_150405"))
		utils.WriteResponseToFile(filename, b, contentType)
	}
}

// tryJSONMarshal marshals v to pretty JSON bytes for logging
func tryJSONMarshal(v interface{}) ([]byte, bool) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		utils.DebugLog("Diagnostics: failed to marshal JSON: %v", err)
		return nil, false
	}
	return b, true
}

// firstNonEmpty returns the first non-empty/non-nil value among candidates
func firstNonEmpty(values ...interface{}) interface{} {
	for _, v := range values {
		if v == nil {
			continue
		}
		s := fmt.Sprintf("%v", v)
		if s != "" && s != "<nil>" {
			return v
		}
	}
	return ""
}

// toInt best-effort conversion from interface{} number/string to int
func toInt(v interface{}) int {
	if v == nil {
		return 0
	}
	switch t := v.(type) {
	case int:
		return t
	case int64:
		return int(t)
	case float64:
		return int(t)
	case json.Number:
		i, _ := t.Int64()
		return int(i)
	default:
		s := fmt.Sprintf("%v", v)
		n, err := strconv.Atoi(s)
		if err != nil {
			return 0
		}
		return n
	}
}
