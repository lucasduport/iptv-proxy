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

package server

import (
	"bytes"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/gin-contrib/cors"
	"github.com/jamesnetherton/m3u"
	"github.com/lucasduport/iptv-proxy/pkg/config"
	"github.com/lucasduport/iptv-proxy/pkg/utils"
	uuid "github.com/satori/go.uuid"

	"github.com/gin-gonic/gin"
)

var defaultProxyfiedM3UPath = filepath.Join(os.TempDir(), uuid.NewV4().String()+".iptv-proxy.m3u")
var endpointAntiColision = strings.Split(uuid.NewV4().String(), "-")[0]

// Config represent the server configuration
type Config struct {
	*config.ProxyConfig

	// M3U service part
	playlist *m3u.Playlist
	// this variable is set only for m3u proxy endpoints
	track *m3u.Track
	// path to the proxyfied m3u file
	proxyfiedM3UPath string

	endpointAntiColision string
}

// NewServer initializes a new server configuration with all necessary components
func NewServer(config *config.ProxyConfig) (*Config, error) {
	var p m3u.Playlist
	
	// Parse the M3U playlist from the remote URL if provided
	if config.RemoteURL.String() != "" {
		var err error
		p, err = m3u.Parse(config.RemoteURL.String())
		if err != nil {
			return nil, utils.PrintErrorAndReturn(err)
		}
		utils.InfoLog("Successfully parsed M3U playlist from %s", config.RemoteURL.String())
	}

	// Use custom ID for endpoint if provided, otherwise use a generated one
	customID := endpointAntiColision
	if trimmedCustomId := strings.Trim(config.CustomId, "/"); trimmedCustomId != "" {
		customID = trimmedCustomId
		utils.InfoLog("Using custom endpoint ID: %s", customID)
	}

	// Initialize debug logging from environment variable
	utils.Config.DebugLoggingEnabled = os.Getenv("DEBUG_LOGGING") == "true"

	return &Config{
		config,
		&p,
		nil,
		defaultProxyfiedM3UPath,
		customID,
	}, nil
}

// Serve the iptv-proxy api
func (c *Config) Serve() error {
	if err := c.playlistInitialization(); err != nil {
		return err
	}

	router := gin.Default()
	router.Use(cors.Default())
	group := router.Group("/")
	c.routes(group)
	
	// Add direct streaming routes with proxy credentials
	c.addProxyCredentialRoutes(router)

	// Add a message to indicate the server is ready
	log.Printf("[iptv-proxy] Server is ready and listening on :%d", c.HostConfig.Port)

	return router.Run(fmt.Sprintf(":%d", c.HostConfig.Port))
}

// Add direct streaming routes with proxy credentials
func (c *Config) addProxyCredentialRoutes(router *gin.Engine) {
	log.Printf("[iptv-proxy] Setting up direct stream routes with proxy credentials")
	
	// Handle root level streaming endpoints with proxy credentials
	router.GET("/:username/:password/:id", c.authWithPathCredentials(), c.xtreamProxyCredentialsStreamHandler)
	
	// Handle live, movie, series endpoints with proxy credentials
	router.GET("/live/:username/:password/:id", c.authWithPathCredentials(), c.xtreamProxyCredentialsLiveStreamHandler)
	router.GET("/movie/:username/:password/:id", c.authWithPathCredentials(), c.xtreamProxyCredentialsMovieStreamHandler)
	router.GET("/series/:username/:password/:id", c.authWithPathCredentials(), c.xtreamProxyCredentialsSeriesStreamHandler)
	
	// Handle timeshift with proxy credentials
	router.GET("/timeshift/:username/:password/:duration/:start/:id", c.authWithPathCredentials(), func(ctx *gin.Context) {
		duration := ctx.Param("duration")
		start := ctx.Param("start")
		id := ctx.Param("id")
		log.Printf("[DEBUG] Timeshift request with proxy credentials: %s/%s/%s", duration, start, id)
		
		// Use Xtream credentials for upstream request
		rpURL, err := url.Parse(fmt.Sprintf("%s/timeshift/%s/%s/%s/%s/%s", 
			c.XtreamBaseURL, c.XtreamUser, c.XtreamPassword, duration, start, id))
		if err != nil {
			ctx.AbortWithError(http.StatusInternalServerError, err)
			return
		}
		
		c.stream(ctx, rpURL)
	})

	log.Printf("[iptv-proxy] Routes initialized with direct stream URL support")
}

// Authentication middleware that checks credentials from URL path parameters
func (c *Config) authWithPathCredentials() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		username := ctx.Param("username")
		password := ctx.Param("password")
		
		log.Printf("[DEBUG] Path credentials auth check: username=%s", username)
		
		// If LDAP is enabled, authenticate against LDAP
		if c.ProxyConfig.LDAPEnabled {
			ok := ldapAuthenticate(
				c.ProxyConfig.LDAPServer,
				c.ProxyConfig.LDAPBaseDN,
				c.ProxyConfig.LDAPBindDN,
				c.ProxyConfig.LDAPBindPassword,
				c.ProxyConfig.LDAPUserAttribute,
				c.ProxyConfig.LDAPGroupAttribute,
				c.ProxyConfig.LDAPRequiredGroup,
				username,
				password,
			)
			if !ok {
				log.Printf("[DEBUG] LDAP authentication failed for user in path: %s", username)
				ctx.AbortWithStatus(http.StatusUnauthorized)
				return
			}
			log.Printf("[DEBUG] LDAP authentication succeeded for user in path: %s", username)
		} else if c.ProxyConfig.User.String() != username || c.ProxyConfig.Password.String() != password {
			log.Printf("[DEBUG] Local authentication failed for user in path: %s", username)
			ctx.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		
		ctx.Next()
	}
}

func (c *Config) playlistInitialization() error {
	if len(c.playlist.Tracks) == 0 {
		return nil
	}

	f, err := os.Create(c.proxyfiedM3UPath)
	if err != nil {
		return err
	}
	defer f.Close()

	return c.marshallInto(f, false)
}

// MarshallInto a *bufio.Writer a Playlist.
func (c *Config) marshallInto(into *os.File, xtream bool) error {
	filteredTrack := make([]m3u.Track, 0, len(c.playlist.Tracks))

	ret := 0
	into.WriteString("#EXTM3U\n") // nolint: errcheck
	for i, track := range c.playlist.Tracks {
		var buffer bytes.Buffer

		buffer.WriteString("#EXTINF:")                       // nolint: errcheck
		buffer.WriteString(fmt.Sprintf("%d ", track.Length)) // nolint: errcheck
		for i := range track.Tags {
			if i == len(track.Tags)-1 {
				buffer.WriteString(fmt.Sprintf("%s=%q", track.Tags[i].Name, track.Tags[i].Value)) // nolint: errcheck
				continue
			}
			buffer.WriteString(fmt.Sprintf("%s=%q ", track.Tags[i].Name, track.Tags[i].Value)) // nolint: errcheck
		}

		uri, err := c.replaceURL(track.URI, i-ret, xtream)
		if err != nil {
			ret++
			log.Printf("ERROR: track: %s: %s", track.Name, err)
			continue
		}

		into.WriteString(fmt.Sprintf("%s, %s\n%s\n", buffer.String(), track.Name, uri)) // nolint: errcheck

		filteredTrack = append(filteredTrack, track)
	}
	c.playlist.Tracks = filteredTrack

	return into.Sync()
}

// ReplaceURL replace original playlist url by proxy url
func (c *Config) replaceURL(uri string, trackIndex int, xtream bool) (string, error) {
	oriURL, err := url.Parse(uri)
	if err != nil {
		return "", err
	}

	protocol := "http"
	if c.HTTPS {
		protocol = "https"
	}

	customEnd := strings.Trim(c.CustomEndpoint, "/")
	if customEnd != "" {
		customEnd = fmt.Sprintf("/%s", customEnd)
	}

	uriPath := oriURL.EscapedPath()
	if xtream {
		uriPath = strings.ReplaceAll(uriPath, c.XtreamUser.PathEscape(), c.User.PathEscape())
		uriPath = strings.ReplaceAll(uriPath, c.XtreamPassword.PathEscape(), c.Password.PathEscape())
	} else {
		uriPath = path.Join("/", c.endpointAntiColision, c.User.PathEscape(), c.Password.PathEscape(), fmt.Sprintf("%d", trackIndex), path.Base(uriPath))
	}

	basicAuth := oriURL.User.String()
	if basicAuth != "" {
		basicAuth += "@"
	}

	newURI := fmt.Sprintf(
		"%s://%s%s:%d%s%s",
		protocol,
		basicAuth,
		c.HostConfig.Hostname,
		c.AdvertisedPort,
		customEnd,
		uriPath,
	)

	newURL, err := url.Parse(newURI)
	if err != nil {
		return "", err
	}

	return newURL.String(), nil
}
