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
	"fmt"
	"log"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/lucasduport/stream-share/pkg/utils"
)

func (c *Config) routes(r *gin.RouterGroup) {
	r = r.Group(c.CustomEndpoint)

	//Xtream service endopoints
	if c.ProxyConfig.XtreamBaseURL != "" {
		c.xtreamRoutes(r)
		if strings.Contains(c.XtreamBaseURL, c.RemoteURL.Host) &&
			c.XtreamUser.String() == c.RemoteURL.Query().Get("username") &&
			c.XtreamPassword.String() == c.RemoteURL.Query().Get("password") {

			r.GET("/"+c.M3UFileName, c.authenticate, c.xtreamGetAuto)
			// XXX Private need: for external Android app
			r.POST("/"+c.M3UFileName, c.authenticate, c.xtreamGetAuto)

			return
		}
	}

	c.m3uRoutes(r)
}

func (c *Config) xtreamRoutes(r *gin.RouterGroup) {
	getphp := gin.HandlerFunc(c.xtreamGet)
	if c.XtreamGenerateApiGet {
		getphp = c.xtreamApiGet
	}
	r.GET("/get.php", c.authenticate, getphp)
	r.POST("/get.php", c.authenticate, getphp)
	r.GET("/apiget", c.authenticate, c.xtreamApiGet)
	r.GET("/player_api.php", c.authenticate, c.xtreamPlayerAPIGET)
	r.POST("/player_api.php", c.appAuthenticate, c.xtreamPlayerAPIPOST)
	r.GET("/xmltv.php", c.authenticate, c.xtreamXMLTV)
	r.GET(fmt.Sprintf("/%s/%s/:id", c.XtreamUser.String(), c.XtreamPassword.String()), c.xtreamStreamHandler)
	r.GET(fmt.Sprintf("/live/%s/%s/:id", c.XtreamUser.String(), c.XtreamPassword.String()), c.xtreamStreamLive)
	r.GET(fmt.Sprintf("/timeshift/%s/%s/:duration/:start/:id", c.XtreamUser.String(), c.XtreamPassword.String()), c.xtreamStreamTimeshift)
	r.GET(fmt.Sprintf("/movie/%s/%s/:id", c.XtreamUser.String(), c.XtreamPassword.String()), c.xtreamStreamMovie)
	r.GET(fmt.Sprintf("/series/%s/%s/:id", c.XtreamUser.String(), c.XtreamPassword.String()), c.xtreamStreamSeries)
	r.GET(fmt.Sprintf("/hlsr/:token/%s/%s/:channel/:hash/:chunk", c.XtreamUser.String(), c.XtreamPassword.String()), c.xtreamHlsrStream)
	r.GET("/hls/:token/:chunk", c.xtreamHlsStream)
	r.GET("/play/:token/:type", c.xtreamStreamPlay)
}

func (c *Config) m3uRoutes(r *gin.RouterGroup) {
	r.GET("/"+c.M3UFileName, c.authenticate, c.getM3U)
	// XXX Private need: for external Android app
	r.POST("/"+c.M3UFileName, c.authenticate, c.getM3U)

	for i, track := range c.playlist.Tracks {
		trackConfig := &Config{
			ProxyConfig: c.ProxyConfig,
			track:       &c.playlist.Tracks[i],
		}

		if strings.HasSuffix(track.URI, ".m3u8") {
			r.GET(fmt.Sprintf("/%s/%s/%s/%d/:id", c.endpointAntiColision, c.XtreamUser.String(), c.XtreamPassword.String(), i), trackConfig.m3u8ReverseProxy)
		} else {
			r.GET(fmt.Sprintf("/%s/%s/%s/%d/%s", c.endpointAntiColision, c.XtreamUser.String(), c.XtreamPassword.String(), i, path.Base(track.URI)), trackConfig.reverseProxy)
		}
	}
}

// InitializeRoutes sets up all the routes for the server
func (c *Config) InitializeRoutes(r *gin.Engine) {
	// Standard routes using authentication middleware
	// ... existing authenticated routes ...

	// Add routes to handle direct streaming with proxy credentials
	// These routes will map requests with proxy credentials to the proper handlers
	// that will use the Xtream credentials for upstream requests
	utils.DebugLog("Setting up direct stream routes with proxy credentials")

	// Handle root level streaming endpoints with proxy credentials
	r.GET("/:username/:password/:id", c.authWithPathCredentials(), c.xtreamProxyCredentialsStreamHandler)

	// Handle live, movie, series endpoints with proxy credentials
	r.GET("/live/:username/:password/:id", c.authWithPathCredentials(), c.xtreamProxyCredentialsLiveStreamHandler)
	r.GET("/movie/:username/:password/:id", c.authWithPathCredentials(), c.xtreamProxyCredentialsMovieStreamHandler)
	r.GET("/series/:username/:password/:id", c.authWithPathCredentials(), c.xtreamProxyCredentialsSeriesStreamHandler)

	// Handle timeshift with proxy credentials
	r.GET("/timeshift/:username/:password/:duration/:start/:id", c.authWithPathCredentials(), func(ctx *gin.Context) {
		duration := ctx.Param("duration")
		start := ctx.Param("start")
		id := ctx.Param("id")
		utils.DebugLog("Timeshift request with proxy credentials: %s/%s/%s", duration, start, id)

		// Use Xtream credentials for upstream request
		rpURL, err := url.Parse(fmt.Sprintf("%s/timeshift/%s/%s/%s/%s/%s",
			c.XtreamBaseURL, c.XtreamUser, c.XtreamPassword, duration, start, id))
		if err != nil {
			ctx.AbortWithError(http.StatusInternalServerError, utils.PrintErrorAndReturn(err))
			return
		}

		c.stream(ctx, rpURL)
	})

	log.Printf("[stream-share] Routes initialized with direct stream URL support")
}