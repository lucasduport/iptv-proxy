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
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jamesnetherton/m3u"
	"github.com/lucasduport/stream-share/pkg/config"
	"github.com/lucasduport/stream-share/pkg/utils"
	xtreamapi "github.com/lucasduport/stream-share/pkg/xtream-proxy"
	uuid "github.com/satori/go.uuid"
)

type cacheMeta struct {
	string
	time.Time
}

var hlsChannelsRedirectURL map[string]url.URL = map[string]url.URL{}
var hlsChannelsRedirectURLLock = sync.RWMutex{}

// XXX Use key/value storage e.g: etcd, redis...
// and remove that dirty globals
var xtreamM3uCache map[string]cacheMeta = map[string]cacheMeta{}
var xtreamM3uCacheLock = sync.RWMutex{}

func (c *Config) cacheXtreamM3u(playlist *m3u.Playlist, cacheName string) error {
	xtreamM3uCacheLock.Lock()
	defer xtreamM3uCacheLock.Unlock()

	tmp := *c
	tmp.playlist = playlist

	path := filepath.Join(os.TempDir(), uuid.NewV4().String()+".stream-share.m3u")
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	if err := tmp.marshallInto(f, true); err != nil {
		return err
	}
	xtreamM3uCache[cacheName] = cacheMeta{path, time.Now()}

	return nil
}

func (c *Config) xtreamGenerateM3u(ctx *gin.Context, extension string) (*m3u.Playlist, error) {
	client, err := xtreamapi.New(c.XtreamUser.String(), c.XtreamPassword.String(), c.XtreamBaseURL, ctx.Request.UserAgent())
	if err != nil {
		return nil, utils.PrintErrorAndReturn(err)
	}

	utils.DebugLog("========== GENERATING M3U PLAYLIST ==========")
	utils.DebugLog("Requesting live categories...")
	
	// Use the robust Action method to get categories
	catResp, httpCode, contentType, err := client.Action(c.ProxyConfig, "get_live_categories", url.Values{})
	if err != nil {
		utils.DebugLog("Failed to get live categories: %v", err)
		return nil, utils.PrintErrorAndReturn(err)
	}
	
	utils.DebugLog("Live categories response - HTTP Status: %d, Content-Type: %s", httpCode, contentType)
	utils.DumpStructToLog("live_categories", catResp)

	// Type assert the response to the expected format
	catData, ok := catResp.([]interface{})
	if !ok {
		utils.DebugLog("Unexpected format for live categories: %T - %+v", catResp, catResp)
		return nil, utils.PrintErrorAndReturn(fmt.Errorf("unexpected format for live categories: %T", catResp))
	}
	
	utils.DebugLog("Found %d live categories", len(catData))

	// this is specific to xtream API,
	// prefix with "live" if there is an extension.
	var prefix string
	if extension != "" {
		extension = "." + extension
		prefix = "live/"
	}

	var playlist = new(m3u.Playlist)
	playlist.Tracks = make([]m3u.Track, 0)

	for i, categoryItem := range catData {
		categoryMap, ok := categoryItem.(map[string]interface{})
		if !ok {
			utils.DebugLog("WARNING: Category item #%d is not a map: %T - %+v", i, categoryItem, categoryItem)
			continue
		}

		categoryID := fmt.Sprintf("%v", categoryMap["category_id"])
		categoryName := fmt.Sprintf("%v", categoryMap["category_name"])
		utils.DebugLog("Processing category: %s (ID: %s)", categoryName, categoryID)

		// Use the robust Action method to get live streams for each category
		utils.DebugLog("Requesting streams for category %s...", categoryID)
		liveResp, httpCode, contentType, err := client.Action(c.ProxyConfig, "get_live_streams", url.Values{"category_id": {categoryID}})
		if err != nil {
			utils.DebugLog("Failed to get live streams for category %s: %v", categoryID, err)
			return nil, utils.PrintErrorAndReturn(err)
		}
		
		utils.DebugLog("Streams response - HTTP Status: %d, Content-Type: %s", httpCode, contentType)
		utils.DumpStructToLog(fmt.Sprintf("streams_cat_%s", categoryID), liveResp)

		liveData, ok := liveResp.([]interface{})
		if !ok {
			utils.DebugLog("WARNING: Unexpected format for streams in category '%s': %T", categoryName, liveResp)
			continue
		}

		utils.DebugLog("Found %d streams in category: %s", len(liveData), categoryName)

		for j, streamItem := range liveData {
			streamMap, ok := streamItem.(map[string]interface{})
			if !ok {
				utils.DebugLog("WARNING: Stream #%d in category '%s' is not a map: %T", j, categoryName, streamItem)
				continue
			}

			// Validate required fields
			streamName, hasName := streamMap["name"].(string)
			streamID, hasID := streamMap["stream_id"].(string)
			
			if !hasName || !hasID {
				utils.DebugLog("WARNING: Stream missing required fields - Name: %v, ID: %v", streamMap["name"], streamMap["stream_id"])
				continue
			}

			track := m3u.Track{
				Name:   streamName,
				Length: -1,
				URI:    "",
				Tags:   nil,
			}

			//TODO: Add more tag if needed.
			if epgID, ok := streamMap["epg_channel_id"].(string); ok && epgID != "" {
				track.Tags = append(track.Tags, m3u.Tag{Name: "tvg-id", Value: epgID})
			}
			if name, ok := streamMap["name"].(string); ok && name != "" {
				track.Tags = append(track.Tags, m3u.Tag{Name: "tvg-name", Value: name})
			}
			if logo, ok := streamMap["stream_icon"].(string); ok && logo != "" {
				track.Tags = append(track.Tags, m3u.Tag{Name: "tvg-logo", Value: logo})
			}
			if categoryName != "" {
				track.Tags = append(track.Tags, m3u.Tag{Name: "group-title", Value: categoryName})
			}

			streamID = fmt.Sprintf("%v", streamMap["stream_id"])
			track.URI = fmt.Sprintf("%s/%s%s/%s/%s%s", c.XtreamBaseURL, prefix, c.XtreamUser, c.XtreamPassword, streamID, extension)
			
			utils.DebugLog("Added stream: %s (ID: %s)", track.Name, streamID)
			playlist.Tracks = append(playlist.Tracks, track)
		}
	}

	utils.DebugLog("Playlist generation complete: %d total tracks", len(playlist.Tracks))
	return playlist, nil
}

func (c *Config) xtreamGetAuto(ctx *gin.Context) {
	newQuery := ctx.Request.URL.Query()
	q := c.RemoteURL.Query()
	for k, v := range q {
		if k == "username" || k == "password" {
			continue
		}

		newQuery.Add(k, strings.Join(v, ","))
	}
	ctx.Request.URL.RawQuery = newQuery.Encode()

	c.xtreamGet(ctx)
}

func (c *Config) xtreamGet(ctx *gin.Context) {
	// Always use Xtream credentials from config for backend requests
	utils.DebugLog("Xtream backend request using Xtream credentials: user=%s, password=%s, baseURL=%s", 
		c.XtreamUser.String(), c.XtreamPassword.String(), c.XtreamBaseURL)
	rawURL := fmt.Sprintf("%s/get.php?username=%s&password=%s", c.XtreamBaseURL, c.XtreamUser, c.XtreamPassword)

	q := ctx.Request.URL.Query()

	for k, v := range q {
		if k == "username" || k == "password" {
			continue
		}

		rawURL = fmt.Sprintf("%s&%s=%s", rawURL, k, strings.Join(v, ","))
	}

	m3uURL, err := url.Parse(rawURL)
	if err != nil {
		ctx.AbortWithError(http.StatusInternalServerError, utils.PrintErrorAndReturn(err)) // nolint: errcheck
		return
	}

	xtreamM3uCacheLock.RLock()
	meta, ok := xtreamM3uCache[m3uURL.String()]
	d := time.Since(meta.Time)
	if !ok || d.Hours() >= float64(c.M3UCacheExpiration) {
		log.Printf("[stream-share] %v | %s | xtream cache m3u file\n", time.Now().Format("2006/01/02 - 15:04:05"), ctx.ClientIP())
		xtreamM3uCacheLock.RUnlock()
		playlist, err := m3u.Parse(m3uURL.String())
		// --- FIX: Check for empty playlist ---
		if err != nil {
			ctx.AbortWithError(http.StatusInternalServerError, utils.PrintErrorAndReturn(err)) // nolint: errcheck
			return
		}
		if len(playlist.Tracks) == 0 {
			ctx.AbortWithError(http.StatusBadGateway, utils.PrintErrorAndReturn(fmt.Errorf("Xtream backend returned empty playlist")))
			return
		}
		if err := c.cacheXtreamM3u(&playlist, m3uURL.String()); err != nil {
			ctx.AbortWithError(http.StatusInternalServerError, utils.PrintErrorAndReturn(err)) // nolint: errcheck
			return
		}
	} else {
		xtreamM3uCacheLock.RUnlock()
	}

	ctx.Header("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, c.M3UFileName))
	xtreamM3uCacheLock.RLock()
	path := xtreamM3uCache[m3uURL.String()].string
	xtreamM3uCacheLock.RUnlock()
	ctx.Header("Content-Type", "application/octet-stream")

	ctx.File(path)
}

func (c *Config) xtreamPlayerAPI(ctx *gin.Context, q url.Values) {
	var action string
	if len(q["action"]) > 0 {
		action = q["action"][0]
	}

	// Handle login (no action) locally to avoid upstream FlexInt unmarshaling issues
	if strings.TrimSpace(action) == "" {
		protocol := "http"
		if c.ProxyConfig.HTTPS {
			protocol = "https"
		}

		// Use numeric strings for fields usually tagged with ,string in Xtream
		now := time.Now()
		nowUnix := strconv.FormatInt(now.Unix(), 10)
		// Choose an exp_date far in the future or "0". Using a year ahead for compatibility.
		expDate := strconv.FormatInt(now.Add(365*24*time.Hour).Unix(), 10)

		loginResp := map[string]interface{}{
			"user_info": map[string]interface{}{
				"username":               c.User.String(),
				"password":               c.Password.String(),
				"message":                "",
				"auth":                   "1",
				"status":                 "Active",
				"exp_date":               expDate,   // numeric string to avoid client FormatException
				"is_trial":               "0",
				"active_cons":            "0",       // numeric string
				"created_at":             nowUnix,   // numeric string
				"max_connections":        "1",       // numeric string
				"allowed_output_formats": []string{"m3u8", "ts"},
			},
			"server_info": map[string]interface{}{
				"url":             fmt.Sprintf("%s://%s", protocol, c.HostConfig.Hostname),
				"port":            strconv.Itoa(c.AdvertisedPort), // numeric string
				"https_port":      strconv.Itoa(c.AdvertisedPort), // numeric string
				"server_protocol": protocol,
				"rtmp_port":       strconv.Itoa(c.AdvertisedPort), // numeric string
				"timezone":        "UTC",
				"timestamp_now":   nowUnix,                         // numeric string
				"time_now":        now.UTC().Format("2006-01-02 15:04:05"),
			},
		}

		log.Printf("[stream-share] %v | %s |Action\tlogin (local)\n", time.Now().Format("2006/01/02 - 15:04:05"), ctx.ClientIP())

		if config.CacheFolder != "" {
			readableJSON, _ := json.Marshal(loginResp)
			filename := fmt.Sprintf("login_%s.json", time.Now().Format("20060102_150405"))
			utils.WriteResponseToFile(filename, readableJSON, "application/json")
		}

		ctx.JSON(http.StatusOK, loginResp)
		return
	}

	client, err := xtreamapi.New(c.XtreamUser.String(), c.XtreamPassword.String(), c.XtreamBaseURL, ctx.Request.UserAgent())
	if err != nil {
		ctx.AbortWithError(http.StatusInternalServerError, utils.PrintErrorAndReturn(err)) // nolint: errcheck
		return
	}

	resp, httpcode, contentType, err := client.Action(c.ProxyConfig, action, q)
	if err != nil {
		ctx.AbortWithError(httpcode, utils.PrintErrorAndReturn(err))
		return
	}

	if contentType == "application/json" {
		// If resp is a string, check if it's empty or only whitespace
		if s, ok := resp.(string); ok && strings.TrimSpace(s) == "" {
			ctx.AbortWithError(http.StatusBadGateway, utils.PrintErrorAndReturn(fmt.Errorf("Xtream backend returned empty JSON response for action: %s", action)))
			return
		}
		// If resp is a []byte, check if it's empty or only whitespace
		if b, ok := resp.([]byte); ok && len(bytes.TrimSpace(b)) == 0 {
			ctx.AbortWithError(http.StatusBadGateway, utils.PrintErrorAndReturn(fmt.Errorf("Xtream backend returned empty JSON response for action: %s", action)))
			return
		}
	}

	log.Printf("[stream-share] %v | %s |Action\t%s\n", time.Now().Format("2006/01/02 - 15:04:05"), ctx.ClientIP(), action)

	processedResp := ProcessResponse(resp)

	if config.CacheFolder != "" {
		readableJSON, _ := json.Marshal(processedResp)
		filename := fmt.Sprintf("%s_%s.json", action, time.Now().Format("20060102_150405"))
		utils.WriteResponseToFile(filename, readableJSON, contentType)
	}

	ctx.JSON(http.StatusOK, processedResp)
}

// Prefer multiplexed streaming if enabled via env, otherwise fall back to legacy stream
func (c *Config) xtreamStream(ctx *gin.Context, oriURL *url.URL) {
	utils.DebugLog("-> Xtream streaming request: %s", ctx.Request.URL.Path)
	utils.DebugLog("-> Proxying to Xtream upstream: %s", oriURL.String())

	if c.sessionManager != nil && os.Getenv("FORCE_MULTIPLEXING") == "true" {
		utils.DebugLog("Using multiplexed streaming (FORCE_MULTIPLEXING=true)")
		c.multiplexedStream(ctx, oriURL)
		return
	}

	// Always use Xtream credentials for upstream requests
	utils.DebugLog("Xtream backend request using Xtream credentials: user=%s, password=%s, baseURL=%s", 
		c.XtreamUser.String(), c.XtreamPassword.String(), c.XtreamBaseURL)
	rawURL := fmt.Sprintf("%s/get.php?username=%s&password=%s", c.XtreamBaseURL, c.XtreamUser, c.XtreamPassword)

	q := ctx.Request.URL.Query()

	for k, v := range q {
		if k == "username" || k == "password" {
			continue
		}

		rawURL = fmt.Sprintf("%s&%s=%s", rawURL, k, strings.Join(v, ","))
	}

	m3uURL, err := url.Parse(rawURL)
	if err != nil {
		ctx.AbortWithError(http.StatusInternalServerError, utils.PrintErrorAndReturn(err)) // nolint: errcheck
		return
	}

	xtreamM3uCacheLock.RLock()
	meta, ok := xtreamM3uCache[m3uURL.String()]
	d := time.Since(meta.Time)
	if !ok || d.Hours() >= float64(c.M3UCacheExpiration) {
		log.Printf("[stream-share] %v | %s | xtream cache m3u file\n", time.Now().Format("2006/01/02 - 15:04:05"), ctx.ClientIP())
		xtreamM3uCacheLock.RUnlock()
		playlist, err := m3u.Parse(m3uURL.String())
		// --- FIX: Check for empty playlist ---
		if err != nil {
			ctx.AbortWithError(http.StatusInternalServerError, utils.PrintErrorAndReturn(err)) // nolint: errcheck
			return
		}
		if len(playlist.Tracks) == 0 {
			ctx.AbortWithError(http.StatusBadGateway, utils.PrintErrorAndReturn(fmt.Errorf("Xtream backend returned empty playlist")))
			return
		}
		if err := c.cacheXtreamM3u(&playlist, m3uURL.String()); err != nil {
			ctx.AbortWithError(http.StatusInternalServerError, utils.PrintErrorAndReturn(err)) // nolint: errcheck
			return
		}
	} else {
		xtreamM3uCacheLock.RUnlock()
	}

	ctx.Header("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, c.M3UFileName))
	xtreamM3uCacheLock.RLock()
	path := xtreamM3uCache[m3uURL.String()].string
	xtreamM3uCacheLock.RUnlock()
	ctx.Header("Content-Type", "application/octet-stream")

	ctx.File(path)
}

func (c *Config) xtreamApiGet(ctx *gin.Context) {
	const (
		apiGet = "apiget"
	)

	var (
		extension = ctx.Query("output")
		cacheName = apiGet + extension
	)

	xtreamM3uCacheLock.RLock()
	meta, ok := xtreamM3uCache[cacheName]
	d := time.Since(meta.Time)
	if !ok || d.Hours() >= float64(c.M3UCacheExpiration) {
		log.Printf("[stream-share] %v | %s | xtream cache API m3u file\n", time.Now().Format("2006/01/02 - 15:04:05"), ctx.ClientIP())
		xtreamM3uCacheLock.RUnlock()
		playlist, err := c.xtreamGenerateM3u(ctx, extension)
		if err != nil {
			ctx.AbortWithError(http.StatusInternalServerError, utils.PrintErrorAndReturn(err)) // nolint: errcheck
			return
		}
		if err := c.cacheXtreamM3u(playlist, cacheName); err != nil {
			ctx.AbortWithError(http.StatusInternalServerError, utils.PrintErrorAndReturn(err)) // nolint: errcheck
			return
		}
	} else {
		xtreamM3uCacheLock.RUnlock()
	}

	ctx.Header("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, c.M3UFileName))
	xtreamM3uCacheLock.RLock()
	path := xtreamM3uCache[cacheName].string
	xtreamM3uCacheLock.RUnlock()
	ctx.Header("Content-Type", "application/octet-stream")

	ctx.File(path)

}

func (c *Config) xtreamPlayerAPIGET(ctx *gin.Context) {
	c.xtreamPlayerAPI(ctx, ctx.Request.URL.Query())
}

func (c *Config) xtreamPlayerAPIPOST(ctx *gin.Context) {
	contents, err := ioutil.ReadAll(ctx.Request.Body)
	if err != nil {
		ctx.AbortWithError(http.StatusInternalServerError, utils.PrintErrorAndReturn(err)) // nolint: errcheck
		return
	}

	q, err := url.ParseQuery(string(contents))
	if err != nil {
		ctx.AbortWithError(http.StatusInternalServerError, utils.PrintErrorAndReturn(err)) // nolint: errcheck
		return
	}

	c.xtreamPlayerAPI(ctx, q)
}

// ProcessResponse processes various types of xtream-codes responses
func ProcessResponse(resp interface{}) interface{} {
	if resp == nil {
		return nil
	}

	respType := reflect.TypeOf(resp)
	utils.DebugLog("Processing response of type: %v", respType)

	switch {
	case respType == nil:
		return resp
	case strings.Contains(respType.String(), "[]xtream"):
		return processXtreamArray(resp)
	case strings.Contains(respType.String(), "xtream"):
		return processXtreamStruct(resp)
	case strings.Contains(respType.String(), "VideoOnDemandInfo"):
		// Special handling for VideoOnDemandInfo which contains FFMPEGStreamInfo
		utils.DebugLog("Processing VideoOnDemandInfo specifically")
		return processXtreamStruct(resp)
	default:
		utils.DebugLog("No special processing for type: %v", respType)
	}
	return resp
}

func processXtreamArray(arr interface{}) interface{} {
	v := reflect.ValueOf(arr)
	if v.Kind() != reflect.Slice {
		return arr
	}

	if v.Len() == 0 {
		return arr
	}

	// Check if the first item is an xtreamcodes struct having a Fields field
	if !isXtreamCodesStruct(v.Index(0).Interface()) {
		return arr
	}

	result := make([]interface{}, v.Len())
	for i := 0; i < v.Len(); i++ {
		result[i] = processXtreamStruct(v.Index(i).Interface())
	}

	return result
}

// Define a helper function to check if fields exist
func hasFieldsField(item interface{}) bool {
	respValue := reflect.ValueOf(item)
	if respValue.Kind() == reflect.Ptr {
		respValue = respValue.Elem()
	}
	
	// Make sure we're dealing with a struct
	if respValue.Kind() != reflect.Struct {
		return false
	}

	// Check for specific fields, e.g., "Fields"
	fieldValue := respValue.FieldByName("Fields")
	return fieldValue.IsValid() && fieldValue.CanInterface() && !fieldValue.IsZero()
}

func isXtreamCodesStruct(item interface{}) bool {
	if item == nil {
		return false
	}
	
	respType := reflect.TypeOf(item)
	if respType == nil {
		return false
	}
	
	typeStr := respType.String()
	
	// Check if it's an xtreamcodes type or contains FFMPEGStreamInfo
	isXtreamType := strings.Contains(typeStr, "xtreamcodes.") || 
	                strings.Contains(typeStr, "xtreamapi.") ||
	                strings.Contains(typeStr, "*xtream.") ||
	                strings.Contains(typeStr, "FFMPEGStreamInfo") ||
	                strings.Contains(typeStr, "VideoOnDemandInfo")
	
	// If it's a known Xtream type, we first check for Fields
	if isXtreamType && hasFieldsField(item) {
		return true
	}
	
	// Special check for VideoOnDemandInfo which has Info that contains FFMPEGStreamInfo
	if strings.Contains(typeStr, "VideoOnDemandInfo") {
		utils.DebugLog("Found VideoOnDemandInfo, special handling needed")
		return true
	}
	
	return false
}

func processXtreamStruct(item interface{}) interface{} {
	if item == nil {
		return nil
	}
	
	respType := reflect.TypeOf(item)
	if respType == nil {
		return item
	}
	
	// Log the type we're processing for debugging
	utils.DebugLog("Processing struct of type: %v", respType)
	
	// Special handling for VideoOnDemandInfo which contains FFMPEGStreamInfo
	if strings.Contains(respType.String(), "VideoOnDemandInfo") {
		utils.DebugLog("Special handling for VideoOnDemandInfo")
		
		// Extract the raw JSON if available
		respValue := reflect.ValueOf(item)
		if respValue.Kind() == reflect.Ptr {
			respValue = respValue.Elem()
		}
		
		fieldValue := respValue.FieldByName("Fields")
		if fieldValue.IsValid() && fieldValue.CanInterface() && !fieldValue.IsZero() {
			// If we have raw Fields data, unmarshal directly to a map to avoid struct constraints
			if fieldValue.Kind() == reflect.Slice && fieldValue.Type().Elem().Kind() == reflect.Uint8 {
				var rawMap map[string]interface{}
				err := json.Unmarshal(fieldValue.Interface().([]byte), &rawMap)
				if err != nil {
					utils.DebugLog("Error unmarshaling VideoOnDemandInfo: %v", err)
					return item
				}
				
				// Special handling for info.video when it's an array
				if info, ok := rawMap["info"].(map[string]interface{}); ok {
					if video, exists := info["video"]; exists {
						utils.DebugLog("Found info.video of type: %T", video)
					}
				}
				
				return rawMap
			}
		}
	}
	
	// Standard processing for Xtream structs with Fields
	if isXtreamCodesStruct(item) {
		respValue := reflect.ValueOf(item)
		if respValue.Kind() == reflect.Ptr {
			respValue = respValue.Elem()
		}

		fieldValue := respValue.FieldByName("Fields")
		if fieldValue.IsValid() && fieldValue.CanInterface() && !fieldValue.IsZero() {
			// Check if Fields is a byte array
			if fieldValue.Kind() == reflect.Slice && fieldValue.Type().Elem().Kind() == reflect.Uint8 {
				var unmarshaledValue interface{}
				err := json.Unmarshal(fieldValue.Interface().([]byte), &unmarshaledValue)
				if err != nil {
					utils.DebugLog("-- processXtreamStruct: JSON unmarshal error: %v", err)
					return item
				}
				return unmarshaledValue
			}

			return fieldValue.Interface()
		}
	}
	return item
}

func (c *Config) xtreamXMLTV(ctx *gin.Context) {
	client, err := xtreamapi.New(c.XtreamUser.String(), c.XtreamPassword.String(), c.XtreamBaseURL, ctx.Request.UserAgent())
	if err != nil {
		ctx.AbortWithError(http.StatusInternalServerError, utils.PrintErrorAndReturn(err)) // nolint: errcheck
		return
	}

	resp, err := client.GetXMLTV()
	if err != nil {
		ctx.AbortWithError(http.StatusInternalServerError, utils.PrintErrorAndReturn(err)) // nolint: errcheck
		return
	}

	// Write response to file
	// utils.WriteResponseToFile(ctx, resp)

	ctx.Data(http.StatusOK, "application/xml", resp)
}

func (c *Config) xtreamStreamHandler(ctx *gin.Context) {
	id := ctx.Param("id")
	// Always use Xtream credentials for upstream requests
	rpURL, err := url.Parse(fmt.Sprintf("%s/%s/%s/%s", c.XtreamBaseURL, c.XtreamUser, c.XtreamPassword, id))
	if err != nil {
		ctx.AbortWithError(http.StatusInternalServerError, utils.PrintErrorAndReturn(err)) // nolint: errcheck
		return
	}

	c.xtreamStream(ctx, rpURL)
}

func (c *Config) xtreamStreamLive(ctx *gin.Context) {
	id := ctx.Param("id")
	// Always use Xtream credentials for upstream requests
	rpURL, err := url.Parse(fmt.Sprintf("%s/live/%s/%s/%s", c.XtreamBaseURL, c.XtreamUser, c.XtreamPassword, id))
	if err != nil {
		ctx.AbortWithError(http.StatusInternalServerError, utils.PrintErrorAndReturn(err)) // nolint: errcheck
		return
	}

	c.xtreamStream(ctx, rpURL)
}

func (c *Config) xtreamStreamPlay(ctx *gin.Context) {
	token := ctx.Param("token")
	t := ctx.Param("type")
	rpURL, err := url.Parse(fmt.Sprintf("%s/play/%s/%s", c.XtreamBaseURL, token, t))
	if err != nil {
		ctx.AbortWithError(http.StatusInternalServerError, utils.PrintErrorAndReturn(err)) // nolint: errcheck
		return
	}

	c.xtreamStream(ctx, rpURL)
}

func (c *Config) xtreamStreamTimeshift(ctx *gin.Context) {
	duration := ctx.Param("duration")
	start := ctx.Param("start")
	id := ctx.Param("id")
	// Always use Xtream credentials for upstream requests
	rpURL, err := url.Parse(fmt.Sprintf("%s/timeshift/%s/%s/%s/%s/%s", c.XtreamBaseURL, c.XtreamUser, c.XtreamPassword, duration, start, id))
	if err != nil {
		ctx.AbortWithError(http.StatusInternalServerError, utils.PrintErrorAndReturn(err)) // nolint: errcheck
		return
	}

	c.stream(ctx, rpURL)
}

func (c *Config) xtreamStreamMovie(ctx *gin.Context) {
	id := ctx.Param("id")
	// Serve from cache if present
	if c.db != nil {
		if entry, err := c.db.GetVODCache(id); err == nil && entry != nil && entry.Status == "ready" {
			if fi, statErr := os.Stat(entry.FilePath); statErr == nil && !fi.IsDir() {
				utils.InfoLog("Serving cached movie for %s from %s", id, entry.FilePath)
				// Set a matching content type based on file extension
				if ext := strings.ToLower(path.Ext(entry.FilePath)); ext == ".ts" {
					ctx.Header("Content-Type", "video/mp2t")
				} else if ext == ".mkv" {
					ctx.Header("Content-Type", "video/x-matroska")
				} else {
					ctx.Header("Content-Type", "video/mp4")
				}
				c.db.TouchVODCache(id)
				ctx.File(entry.FilePath)
				return
			}
			utils.WarnLog("Cached movie missing on disk for %s at %s; falling back to upstream", id, entry.FilePath)
		}
	}
	// Always use Xtream credentials for upstream requests
	rpURL, err := url.Parse(fmt.Sprintf("%s/movie/%s/%s/%s", c.XtreamBaseURL, c.XtreamUser, c.XtreamPassword, id))
	if err != nil {
		ctx.AbortWithError(http.StatusInternalServerError, utils.PrintErrorAndReturn(err)) // nolint: errcheck
		return
	}

	utils.DebugLog("Movie streaming request - using Xtream credentials for upstream: %s", rpURL.String())
	c.xtreamStream(ctx, rpURL)
}

func (c *Config) xtreamStreamSeries(ctx *gin.Context) {
	id := ctx.Param("id")
	// Serve from cache if present
	if c.db != nil {
		if entry, err := c.db.GetVODCache(id); err == nil && entry != nil && entry.Status == "ready" {
			if fi, statErr := os.Stat(entry.FilePath); statErr == nil && !fi.IsDir() {
				utils.InfoLog("Serving cached episode for %s from %s", id, entry.FilePath)
				if ext := strings.ToLower(path.Ext(entry.FilePath)); ext == ".ts" {
					ctx.Header("Content-Type", "video/mp2t")
				} else if ext == ".mkv" {
					ctx.Header("Content-Type", "video/x-matroska")
				} else {
					ctx.Header("Content-Type", "video/mp4")
				}
				c.db.TouchVODCache(id)
				ctx.File(entry.FilePath)
				return
			}
			utils.WarnLog("Cached episode missing on disk for %s at %s; falling back to upstream", id, entry.FilePath)
		}
	}
	// Always use Xtream credentials for upstream requests
	rpURL, err := url.Parse(fmt.Sprintf("%s/series/%s/%s/%s", c.XtreamBaseURL, c.XtreamUser, c.XtreamPassword, id))
	if err != nil {
		ctx.AbortWithError(http.StatusInternalServerError, utils.PrintErrorAndReturn(err)) // nolint: errcheck
		return
	}

	c.xtreamStream(ctx, rpURL)
}

// Added to handle direct streaming URLs with proxy credentials instead of Xtream credentials
func (c *Config) xtreamProxyCredentialsStreamHandler(ctx *gin.Context) {
	id := ctx.Param("id")
	utils.DebugLog("Direct stream request with proxy credentials: username=%s, id=%s", ctx.Param("username"), id)

	rpURL, err := url.Parse(fmt.Sprintf("%s/%s/%s/%s", c.XtreamBaseURL, c.XtreamUser, c.XtreamPassword, id))
	if err != nil {
		utils.ErrorLog("Failed to parse upstream URL: %v", err)
		ctx.AbortWithStatus(500)
		return
	}
	c.multiplexedStream(ctx, rpURL)
}

func (c *Config) xtreamProxyCredentialsLiveStreamHandler(ctx *gin.Context) {
	id := ctx.Param("id")
	utils.DebugLog("Direct live stream request with proxy credentials: username=%s, id=%s", ctx.Param("username"), id)

	rpURL, err := url.Parse(fmt.Sprintf("%s/live/%s/%s/%s", c.XtreamBaseURL, c.XtreamUser, c.XtreamPassword, id))
	if err != nil {
		utils.ErrorLog("Failed to parse upstream URL: %v", err)
		ctx.AbortWithStatus(500)
		return
	}
	c.multiplexedStream(ctx, rpURL)
}

func (c *Config) xtreamProxyCredentialsMovieStreamHandler(ctx *gin.Context) {
	id := ctx.Param("id")
	utils.DebugLog("Direct movie stream request with proxy credentials: username=%s, id=%s", ctx.Param("username"), id)

	// Serve from cache if available
	if c.db != nil {
		if entry, err := c.db.GetVODCache(id); err == nil && entry != nil && entry.Status == "ready" {
			if fi, statErr := os.Stat(entry.FilePath); statErr == nil && !fi.IsDir() {
				utils.InfoLog("Serving cached movie (proxy creds path) for %s from %s", id, entry.FilePath)
				if ext := strings.ToLower(path.Ext(entry.FilePath)); ext == ".ts" {
					ctx.Header("Content-Type", "video/mp2t")
				} else if ext == ".mkv" {
					ctx.Header("Content-Type", "video/x-matroska")
				} else {
					ctx.Header("Content-Type", "video/mp4")
				}
				c.db.TouchVODCache(id)
				ctx.File(entry.FilePath)
				return
			}
			utils.WarnLog("Cached movie (proxy creds) missing on disk for %s at %s; falling back to upstream", id, entry.FilePath)
		}
	}

	rpURL, err := url.Parse(fmt.Sprintf("%s/movie/%s/%s/%s", c.XtreamBaseURL, c.XtreamUser, c.XtreamPassword, id))
	if err != nil {
		utils.ErrorLog("Failed to parse upstream URL: %v", err)
		ctx.AbortWithStatus(500)
		return
	}
	c.multiplexedStream(ctx, rpURL)
}

func (c *Config) xtreamProxyCredentialsSeriesStreamHandler(ctx *gin.Context) {
	id := ctx.Param("id")
	utils.DebugLog("Direct series stream request with proxy credentials: username=%s, id=%s", ctx.Param("username"), id)

	// Serve from cache if available
	if c.db != nil {
		if entry, err := c.db.GetVODCache(id); err == nil && entry != nil && entry.Status == "ready" {
			if fi, statErr := os.Stat(entry.FilePath); statErr == nil && !fi.IsDir() {
				utils.InfoLog("Serving cached episode (proxy creds path) for %s from %s", id, entry.FilePath)
				if ext := strings.ToLower(path.Ext(entry.FilePath)); ext == ".ts" {
					ctx.Header("Content-Type", "video/mp2t")
				} else if ext == ".mkv" {
					ctx.Header("Content-Type", "video/x-matroska")
				} else {
					ctx.Header("Content-Type", "video/mp4")
				}
				c.db.TouchVODCache(id)
				ctx.File(entry.FilePath)
				return
			}
			utils.WarnLog("Cached episode (proxy creds) missing on disk for %s at %s; falling back to upstream", id, entry.FilePath)
		}
	}

	rpURL, err := url.Parse(fmt.Sprintf("%s/series/%s/%s/%s", c.XtreamBaseURL, c.XtreamUser, c.XtreamPassword, id))
	if err != nil {
		utils.ErrorLog("Failed to parse upstream URL: %v", err)
		ctx.AbortWithStatus(500)
		return
	}
	c.multiplexedStream(ctx, rpURL)
}

func (c *Config) xtreamHlsStream(ctx *gin.Context) {
	chunk := ctx.Param("chunk")
	s := strings.Split(chunk, "_")
	if len(s) != 2 {
		ctx.AbortWithError( // nolint: errcheck
			http.StatusInternalServerError,
			utils.PrintErrorAndReturn(errors.New("HSL malformed chunk")),
		)
		return
	}
	channel := s[0]

	redirURL, err := getHlsRedirectURL(channel)
	if err != nil {
		ctx.AbortWithError(http.StatusInternalServerError, utils.PrintErrorAndReturn(err)) // nolint: errcheck
		return
	}

	req, reqErr := http.NewRequestWithContext(ctx.Request.Context(), "GET",
		fmt.Sprintf("%s://%s/hls/%s/%s", redirURL.Scheme, redirURL.Host, ctx.Param("token"), ctx.Param("chunk")), nil)
	if reqErr != nil {
		ctx.AbortWithError(http.StatusInternalServerError, utils.PrintErrorAndReturn(reqErr)) // nolint: errcheck
		return
	}

	mergeHttpHeader(req.Header, ctx.Request.Header)

	resp, doErr := http.DefaultClient.Do(req)
	if doErr != nil {
		ctx.AbortWithError(http.StatusInternalServerError, utils.PrintErrorAndReturn(doErr)) // nolint: errcheck
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusFound {
		loc, locErr := resp.Location()
		if locErr != nil {
			ctx.AbortWithError(http.StatusInternalServerError, utils.PrintErrorAndReturn(locErr)) // nolint: errcheck
			return
		}
		id := ctx.Param("id")
		if strings.Contains(loc.String(), id) {
			hlsChannelsRedirectURLLock.Lock()
			hlsChannelsRedirectURL[id] = *loc
			hlsChannelsRedirectURLLock.Unlock()

			hlsReq, hlsReqErr := http.NewRequestWithContext(ctx.Request.Context(), "GET", loc.String(), nil)
			if hlsReqErr != nil {
				ctx.AbortWithError(http.StatusInternalServerError, utils.PrintErrorAndReturn(hlsReqErr)) // nolint: errcheck
				return
			}

			mergeHttpHeader(hlsReq.Header, ctx.Request.Header)

			hlsResp, hlsDoErr := http.DefaultClient.Do(hlsReq)
			if hlsDoErr != nil {
				ctx.AbortWithError(http.StatusInternalServerError, utils.PrintErrorAndReturn(hlsDoErr)) // nolint: errcheck
				return
			}
			defer hlsResp.Body.Close()

			b, readErr := ioutil.ReadAll(hlsResp.Body)
			if readErr != nil {
				ctx.AbortWithError(http.StatusInternalServerError, utils.PrintErrorAndReturn(readErr)) // nolint: errcheck
				return
			}
			body := string(b)

			// Replace upstream Xtream credentials with proxy user credentials in response
			body = strings.ReplaceAll(body, "/"+c.XtreamUser.String()+"/"+c.XtreamPassword.String()+"/", "/"+c.User.String()+"/"+c.Password.String()+"/")

			utils.DebugLog("HLS stream response modified to use proxy credentials for client URLs")
			mergeHttpHeader(ctx.Writer.Header(), hlsResp.Header)

			ctx.Data(http.StatusOK, hlsResp.Header.Get("Content-Type"), []byte(body))
			return
		}
		ctx.AbortWithError(http.StatusInternalServerError, utils.PrintErrorAndReturn(errors.New("Unable to HLS stream"))) // nolint: errcheck
		return
	}

	utils.DebugLog("HLS stream response status: %d", resp.StatusCode)
	ctx.Status(resp.StatusCode)
}

func (c *Config) hlsXtreamStream(ctx *gin.Context, oriURL *url.URL) {
	utils.DebugLog("HLS stream request with URL: %s", oriURL.String())
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	req, reqErr := http.NewRequestWithContext(ctx.Request.Context(), "GET", oriURL.String(), nil)
	if reqErr != nil {
		ctx.AbortWithError(http.StatusInternalServerError, utils.PrintErrorAndReturn(reqErr)) // nolint: errcheck
		return
	}

	mergeHttpHeader(req.Header, ctx.Request.Header)

	resp, doErr := client.Do(req)
	if doErr != nil {
		ctx.AbortWithError(http.StatusInternalServerError, utils.PrintErrorAndReturn(doErr)) // nolint: errcheck
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusFound {
		loc, locErr := resp.Location()
		if locErr != nil {
			ctx.AbortWithError(http.StatusInternalServerError, utils.PrintErrorAndReturn(locErr)) // nolint: errcheck
			return
		}
		id := ctx.Param("id")
		if strings.Contains(loc.String(), id) {
			hlsChannelsRedirectURLLock.Lock()
			hlsChannelsRedirectURL[id] = *loc
			hlsChannelsRedirectURLLock.Unlock()

			hlsReq, hlsReqErr := http.NewRequestWithContext(ctx.Request.Context(), "GET", loc.String(), nil)
			if hlsReqErr != nil {
				ctx.AbortWithError(http.StatusInternalServerError, utils.PrintErrorAndReturn(hlsReqErr)) // nolint: errcheck
				return
			}

			mergeHttpHeader(hlsReq.Header, ctx.Request.Header)

			hlsResp, hlsDoErr := client.Do(hlsReq)
			if hlsDoErr != nil {
				ctx.AbortWithError(http.StatusInternalServerError, utils.PrintErrorAndReturn(hlsDoErr)) // nolint: errcheck
				return
			}
			defer hlsResp.Body.Close()

			b, readErr := ioutil.ReadAll(hlsResp.Body)
			if readErr != nil {
				ctx.AbortWithError(http.StatusInternalServerError, utils.PrintErrorAndReturn(readErr)) // nolint: errcheck
				return
			}
			body := string(b)

			// Replace upstream Xtream credentials with proxy user credentials in response
			body = strings.ReplaceAll(body, "/"+c.XtreamUser.String()+"/"+c.XtreamPassword.String()+"/", "/"+c.User.String()+"/"+c.Password.String()+"/")

			utils.DebugLog("HLS stream response modified to use proxy credentials for client URLs")
			mergeHttpHeader(ctx.Writer.Header(), hlsResp.Header)

			ctx.Data(http.StatusOK, hlsResp.Header.Get("Content-Type"), []byte(body))
			return
		}
		ctx.AbortWithError(http.StatusInternalServerError, utils.PrintErrorAndReturn(errors.New("Unable to HLS stream"))) // nolint: errcheck
		return
	}

	utils.DebugLog("HLS stream response status: %d", resp.StatusCode)
	ctx.Status(resp.StatusCode)
}

// Added handler expected by routes.go for HLSR path; delegates to hlsXtreamStream
func (c *Config) xtreamHlsrStream(ctx *gin.Context) {
	channel := ctx.Param("channel")

	redirURL, err := getHlsRedirectURL(channel)
	if err != nil {
		ctx.AbortWithError(http.StatusInternalServerError, utils.PrintErrorAndReturn(err)) // nolint: errcheck
		return
	}

	nextURL, parseErr := url.Parse(
		fmt.Sprintf(
			"%s://%s/hlsr/%s/%s/%s/%s/%s/%s",
			redirURL.Scheme,
			redirURL.Host,
			ctx.Param("token"),
			c.XtreamUser,     // Always use Xtream credentials
			c.XtreamPassword, // Always use Xtream credentials
			ctx.Param("channel"),
			ctx.Param("hash"),
			ctx.Param("chunk"),
		),
	)
	if parseErr != nil {
		ctx.AbortWithError(http.StatusInternalServerError, utils.PrintErrorAndReturn(parseErr)) // nolint: errcheck
		return
	}

	c.hlsXtreamStream(ctx, nextURL)
}

// Restore helper used by HLS handlers
func getHlsRedirectURL(channel string) (*url.URL, error) {
	hlsChannelsRedirectURLLock.RLock()
	defer hlsChannelsRedirectURLLock.RUnlock()

	u, ok := hlsChannelsRedirectURL[channel+".m3u8"]
	if !ok {
		return nil, utils.PrintErrorAndReturn(errors.New("HSL redirect url not found"))
	}
	return &u, nil
}