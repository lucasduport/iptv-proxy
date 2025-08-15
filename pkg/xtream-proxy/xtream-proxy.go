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
	"net/http"
	"net/url"
	"strconv"

	"github.com/pierre-emmanuelJ/iptv-proxy/pkg/config"
	"github.com/pierre-emmanuelJ/iptv-proxy/pkg/utils"
	xtream "github.com/tellytv/go.xtream-codes"
)

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

// Client represent an xtream client
type Client struct {
	*xtream.XtreamClient
}

// New new xtream client
func New(user, password, baseURL, userAgent string) (*Client, error) {
	cli, err := xtream.NewClientWithUserAgent(context.Background(), user, password, baseURL, userAgent)
	if err != nil {
		return nil, utils.PrintErrorAndReturn(err)
	}

	return &Client{cli}, nil
}

type login struct {
	UserInfo   xtream.UserInfo   `json:"user_info"`
	ServerInfo xtream.ServerInfo `json:"server_info"`
}

// Login xtream login
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

// Action execute an xtream action.
func (c *Client) Action(config *config.ProxyConfig, action string, q url.Values) (respBody interface{}, httpcode int, contentType string, err error) {
	protocol := "http"
	if config.HTTPS {
		protocol = "https"
	}

	// Default content type for most responses
	contentType = "application/json"

	// Debug log: always use Xtream credentials from config
	utils.DebugLog("Xtream API backend call: user=%s, password=%s, baseURL=%s", config.XtreamUser.String(), config.XtreamPassword.String(), config.XtreamBaseURL)

	switch action {
	case getLiveCategories:
		respBody, err = c.GetLiveCategories()
		if err != nil {
			err = utils.PrintErrorAndReturn(err)
		}
	case getLiveStreams:
		categoryID := ""
		if len(q["category_id"]) > 0 {
			categoryID = q["category_id"][0]
		}
		respBody, err = c.GetLiveStreams(categoryID)
		if err != nil {
			err = utils.PrintErrorAndReturn(err)
		}
	case getVodCategories:
		respBody, err = c.GetVideoOnDemandCategories()
		if err != nil {
			err = utils.PrintErrorAndReturn(err)
		}
	case getVodStreams:
		categoryID := ""
		if len(q["category_id"]) > 0 {
			categoryID = q["category_id"][0]
		}
		respBody, err = c.GetVideoOnDemandStreams(categoryID)
		if err != nil {
			err = utils.PrintErrorAndReturn(err)
		}
	case getVodInfo:
		httpcode, err = validateParams(q, "vod_id")
		if err != nil {
			err = utils.PrintErrorAndReturn(err)
			return
		}
		respBody, err = c.GetVideoOnDemandInfo(q["vod_id"][0])
		if err != nil {
			err = utils.PrintErrorAndReturn(err)
		}
	case getSeriesCategories:
		respBody, err = c.GetSeriesCategories()
		if err != nil {
			err = utils.PrintErrorAndReturn(err)
		}
	case getSeries:
		categoryID := ""
		if len(q["category_id"]) > 0 {
			categoryID = q["category_id"][0]
		}
		respBody, err = c.GetSeries(categoryID)
		if err != nil {
			err = utils.PrintErrorAndReturn(err)
		}
	case getSerieInfo:
		httpcode, err = validateParams(q, "series_id")
		if err != nil {
			err = utils.PrintErrorAndReturn(err)
			return
		}
		respBody, err = c.GetSeriesInfo(q["series_id"][0])
		if err != nil {
			err = utils.PrintErrorAndReturn(err)
		}
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
		if err != nil {
			err = utils.PrintErrorAndReturn(err)
		}
	case getSimpleDataTable:
		httpcode, err = validateParams(q, "stream_id")
		if err != nil {
			err = utils.PrintErrorAndReturn(err)
			return
		}
		respBody, err = c.GetEPG(q["stream_id"][0])
		if err != nil {
			err = utils.PrintErrorAndReturn(err)
		}
	default:
		// Always use Xtream credentials from config for login response
		// but return the proxy credentials to the client
		respBody, err = c.login(
			config.User.String(),
			config.Password.String(),
			protocol+"://"+config.HostConfig.Hostname,
			config.AdvertisedPort,
			protocol,
		)
		if err != nil {
			err = utils.PrintErrorAndReturn(err)
		}
	}

	return
}

func validateParams(u url.Values, params ...string) (int, error) {
	for _, p := range params {
		if len(u[p]) < 1 {
			return http.StatusBadRequest, fmt.Errorf("missing %q", p)
		}

	}

	return 0, nil
}

// FFMPEGStreamInfo represents the stream info
type FFMPEGStreamInfo struct {
	Bitrate    int         `json:"bitrate"`
	Width      int         `json:"width"`
	Height     int         `json:"height"`
	Video      interface{} `json:"video"` // Changed to interface{} to handle both array and struct
	Audio      interface{} `json:"audio"` // Also changed for consistency
	Duration   string      `json:"duration"`
	MediaFile  string      `json:"mediafile"`
	StreamFile string      `json:"stream_file"`
	Fields     []byte      `json:"-"` // Raw JSON data
}

// UnmarshalJSON provides custom JSON unmarshaling to handle different response formats
func (f *FFMPEGStreamInfo) UnmarshalJSON(data []byte) error {
	// Store raw JSON data
	f.Fields = make([]byte, len(data))
	copy(f.Fields, data)

	// Create a temporary struct with interface{} types for flexible parsing
	type TempInfo struct {
		Bitrate    int         `json:"bitrate"`
		Width      int         `json:"width"`
		Height     int         `json:"height"`
		Video      interface{} `json:"video"` // Can be array or struct
		Audio      interface{} `json:"audio"` // Can be array or struct
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
