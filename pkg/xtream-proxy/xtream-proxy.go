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
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"strconv"

	"github.com/pierre-emmanuelJ/iptv-proxy/pkg/config"
	"github.com/pierre-emmanuelJ/iptv-proxy/pkg/utils"
	xtream "github.com/tellytv/go.xtream-codes"
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
	*xtream.XtreamClient
}

// New creates a new Xtream client instance
func New(user, password, baseURL, userAgent string) (*Client, error) {
	cli, err := xtream.NewClientWithUserAgent(context.Background(), user, password, baseURL, userAgent)
	if err != nil {
		return nil, utils.PrintErrorAndReturn(err)
	}

	return &Client{cli}, nil
}

// login response structure for Xtream API
type login struct {
	UserInfo   xtream.UserInfo   `json:"user_info"`
	ServerInfo xtream.ServerInfo `json:"server_info"`
}

// login creates a login response with proxy user credentials but server info from upstream
func (c *Client) login(proxyUser, proxyPassword, proxyURL string, proxyPort int, protocol string) (login, error) {
	req := login{
		UserInfo: xtream.UserInfo{
			Username:             proxyUser,
			Password:             proxyPassword,
			Message:              c.UserInfo.Message,
			Auth:                 c.UserInfo.Auth,
			Status:               c.UserInfo.Status,
			ExpDate:              c.UserInfo.ExpDate,
			IsTrial:              c.UserInfo.IsTrial,
			ActiveConnections:    c.UserInfo.ActiveConnections,
			CreatedAt:            c.UserInfo.CreatedAt,
			MaxConnections:       c.UserInfo.MaxConnections,
			AllowedOutputFormats: c.UserInfo.AllowedOutputFormats,
		},
		ServerInfo: xtream.ServerInfo{
			URL:          proxyURL,
			Port:         xtream.FlexInt(proxyPort),
			HTTPSPort:    xtream.FlexInt(proxyPort),
			Protocol:     protocol,
			RTMPPort:     xtream.FlexInt(proxyPort),
			Timezone:     c.ServerInfo.Timezone,
			TimestampNow: c.ServerInfo.TimestampNow,
			TimeNow:      c.ServerInfo.TimeNow,
		},
	}

	return req, nil
}

// Action executes an Xtream API action and returns the response
func (c *Client) Action(config *config.ProxyConfig, action string, q url.Values) (respBody interface{}, httpcode int, contentType string, err error) {
	protocol := "http"
	if config.HTTPS {
		protocol = "https"
	}

	// Default content type for most responses
	contentType = "application/json"

	// Debug log: always use Xtream credentials from config
	utils.DebugLog("Xtream API backend call: user=%s, password=%s, baseURL=%s",
		config.XtreamUser.String(), config.XtreamPassword.String(), config.XtreamBaseURL)

	// Handle different API actions
	switch action {
	case getLiveCategories:
		respBody, err = c.GetLiveCategories()
	case getLiveStreams:
		categoryID := ""
		if len(q["category_id"]) > 0 {
			categoryID = q["category_id"][0]
		}
		respBody, err = c.GetLiveStreams(categoryID)
	case getVodCategories:
		respBody, err = c.GetVideoOnDemandCategories()
	case getVodStreams:
		categoryID := ""
		if len(q["category_id"]) > 0 {
			categoryID = q["category_id"][0]
		}
		respBody, err = c.GetVideoOnDemandStreams(categoryID)
	case getVodInfo:
		httpcode, err = validateParams(q, "vod_id")
		if err != nil {
			err = utils.PrintErrorAndReturn(err)
			return
		}
		
		// Special handling for get_vod_info to avoid FFMPEGStreamInfo unmarshaling issue
		var rawResp map[string]interface{}
		rawResp, err = c.GetVideoOnDemandInfoRaw(q["vod_id"][0])
		respBody = rawResp
	case getSeriesCategories:
		respBody, err = c.GetSeriesCategories()
	case getSeries:
		categoryID := ""
		if len(q["category_id"]) > 0 {
			categoryID = q["category_id"][0]
		}
		respBody, err = c.GetSeries(categoryID)
	case getSerieInfo:
		httpcode, err = validateParams(q, "series_id")
		if err != nil {
			err = utils.PrintErrorAndReturn(err)
			return
		}
		respBody, err = c.GetSeriesInfo(q["series_id"][0])
	case getShortEPG:
		limit := 0
		httpcode, err = validateParams(q, "stream_id")
		if err != nil {
			err = utils.PrintErrorAndReturn(err)
			return
		}
		if len(q["limit"]) > 0 {
			limit, err = strconv.Atoi(q["limit"][0])
			if err != nil {
				httpcode = http.StatusInternalServerError
				err = utils.PrintErrorAndReturn(err)
				return
			}
		}
		respBody, err = c.GetShortEPG(q["stream_id"][0], limit)
	case getSimpleDataTable:
		httpcode, err = validateParams(q, "stream_id")
		if err != nil {
			err = utils.PrintErrorAndReturn(err)
			return
		}
		respBody, err = c.GetEPG(q["stream_id"][0])
	default:
		// Default action is login
		// Return proxy credentials to client but use upstream server info
		respBody, err = c.login(
			config.User.String(),
			config.Password.String(),
			protocol+"://"+config.HostConfig.Hostname,
			config.AdvertisedPort,
			protocol,
		)
	}

	if err != nil {
		err = utils.PrintErrorAndReturn(err)
	}

	return
}

// GetVideoOnDemandInfoRaw gets VOD information for a specific VOD ID, returning raw JSON map
// This avoids the FFMPEGStreamInfo unmarshaling issues
func (c *Client) GetVideoOnDemandInfoRaw(vodID string) (map[string]interface{}, error) {
	utils.DebugLog("GetVideoOnDemandInfoRaw: Using raw JSON approach for vodID=%s", vodID)
	
	// Use the request URL format from the xtream-codes client
	requestURL := fmt.Sprintf("%s/player_api.php?username=%s&password=%s&action=get_vod_info&vod_id=%s", 
		c.BaseURL, c.Username, c.Password, vodID)
	
	// Use http.Get instead of direct client usage
	resp, err := http.Get(requestURL)
	if err != nil {
		return nil, utils.PrintErrorAndReturn(err)
	}
	defer resp.Body.Close()
	
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, utils.PrintErrorAndReturn(err)
	}
	
	// Instead of unmarshaling to a predefined struct, we use a map for flexibility
	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, utils.PrintErrorAndReturn(err)
	}
	
	utils.DebugLog("GetVideoOnDemandInfoRaw: Successfully processed response for vodID=%s", vodID)
	return result, nil
}

// validateParams checks if the required parameters are present in the URL query
func validateParams(u url.Values, params ...string) (int, error) {
	for _, p := range params {
		if len(u[p]) < 1 {
			return http.StatusBadRequest, fmt.Errorf("missing %q", p)
		}
	}

	return 0, nil
}

// FFMPEGStreamInfo represents the stream info with flexible field handling
type FFMPEGStreamInfo struct {
	Bitrate    int         `json:"bitrate"`
	Width      int         `json:"width"`
	Height     int         `json:"height"`
	Video      interface{} `json:"video"` // Can be array or struct
	Audio      interface{} `json:"audio"` // Can be array or struct
	Duration   string      `json:"duration"`
	MediaFile  string      `json:"mediafile"`
	StreamFile string      `json:"stream_file"`
	Fields     []byte      `json:"-"` // Raw JSON data for advanced parsing
}

// UnmarshalJSON provides custom JSON unmarshaling to handle different response formats
func (f *FFMPEGStreamInfo) UnmarshalJSON(data []byte) error {
	// Store raw JSON data for later access if needed
	f.Fields = make([]byte, len(data))
	copy(f.Fields, data)

	// Log the data we're trying to parse
	utils.DebugLog("Unmarshaling FFMPEGStreamInfo: %s", string(data[:min(len(data), 100)]))

	// First try to unmarshal directly using map to handle any structure
	var rawMap map[string]interface{}
	if err := json.Unmarshal(data, &rawMap); err == nil {
		// Extract known fields from the map
		if val, ok := rawMap["bitrate"]; ok {
			if floatVal, ok := val.(float64); ok {
				f.Bitrate = int(floatVal)
			}
		}
		if val, ok := rawMap["width"]; ok {
			if floatVal, ok := val.(float64); ok {
				f.Width = int(floatVal)
			}
		}
		if val, ok := rawMap["height"]; ok {
			if floatVal, ok := val.(float64); ok {
				f.Height = int(floatVal)
			}
		}
		if val, ok := rawMap["video"]; ok {
			f.Video = val // Preserve as interface{} to handle both array and object
			// Log what type we're getting
			utils.DebugLog("Video field type: %T", val)
		}
		if val, ok := rawMap["audio"]; ok {
			f.Audio = val // Preserve as interface{} to handle both array and object
			// Log what type we're getting
			utils.DebugLog("Audio field type: %T", val)
		}
		if val, ok := rawMap["duration"]; ok {
			if strVal, ok := val.(string); ok {
				f.Duration = strVal
			}
		}
		if val, ok := rawMap["mediafile"]; ok {
			if strVal, ok := val.(string); ok {
				f.MediaFile = strVal
			}
		}
		if val, ok := rawMap["stream_file"]; ok {
			if strVal, ok := val.(string); ok {
				f.StreamFile = strVal
			}
		}

		return nil
	}

	// Fallback to the previous approach if direct map doesn't work
	type TempInfo struct {
		Bitrate    int         `json:"bitrate"`
		Width      int         `json:"width"`
		Height     int         `json:"height"`
		Video      interface{} `json:"video"`
		Audio      interface{} `json:"audio"`
		Duration   string      `json:"duration"`
		MediaFile  string      `json:"mediafile"`
		StreamFile string      `json:"stream_file"`
	}

	var temp TempInfo
	if err := json.Unmarshal(data, &temp); err != nil {
		return err
	}

	// Copy the successfully parsed fields
	f.Bitrate = temp.Bitrate
	f.Width = temp.Width
	f.Height = temp.Height
	f.Video = temp.Video
	f.Audio = temp.Audio
	f.Duration = temp.Duration
	f.MediaFile = temp.MediaFile
	f.StreamFile = temp.StreamFile

	return nil
}

// Helper function to get the minimum of two integers
func min(a, b int) int {
    if a < b {
        return a
    }
    return b
}
