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
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lucasduport/stream-share/pkg/utils"
	"github.com/go-ldap/ldap/v3"
)

func (c *Config) getM3U(ctx *gin.Context) {
	ctx.Header("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, c.M3UFileName))
	ctx.Header("Content-Type", "application/octet-stream")

	ctx.File(c.proxyfiedM3UPath)
}

func (c *Config) reverseProxy(ctx *gin.Context) {
	// Parse the original track URI
	rpURL, err := url.Parse(c.track.URI)
	if err != nil {
		ctx.AbortWithError(http.StatusInternalServerError, err) // nolint: errcheck
		return
	}

	// Always use Xtream creds for upstream query
	q := rpURL.Query()
	q.Set("username", c.XtreamUser.String())
	q.Set("password", c.XtreamPassword.String())
	rpURL.RawQuery = q.Encode()

	utils.DebugLog("-> Upstream username: %s, password: %s", c.XtreamUser.String(), c.XtreamPassword.String())
	utils.DebugLog("-> Final upstream URL: %s", rpURL.String())

	c.stream(ctx, rpURL)
}

func (c *Config) m3u8ReverseProxy(ctx *gin.Context) {
	id := ctx.Param("id")
	rpURL, err := url.Parse(strings.ReplaceAll(c.track.URI, path.Base(c.track.URI), id))
	if err != nil {
		ctx.AbortWithError(http.StatusInternalServerError, err) // nolint: errcheck
		return
	}

	// Always use Xtream creds for upstream query
	q := rpURL.Query()
	q.Set("username", c.XtreamUser.String())
	q.Set("password", c.XtreamPassword.String())
	rpURL.RawQuery = q.Encode()

	utils.DebugLog("-> Upstream username: %s, password: %s", c.XtreamUser.String(), c.XtreamPassword.String())
	utils.DebugLog("-> Final upstream URL: %s", rpURL.String())

	c.stream(ctx, rpURL)
}

// stream handles proxying the actual stream content from the upstream source
// to the requesting client, preserving headers and status codes
func (c *Config) stream(ctx *gin.Context, oriURL *url.URL) {
	utils.DebugLog("-> Streaming request URL: %s", ctx.Request.URL)
	utils.DebugLog("-> Proxying to upstream URL: %s", oriURL.String())

	// Configure HTTP transport suitable for long-lived streaming
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     false,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	// No global Timeout; let the stream run as long as the client stays connected
	client := &http.Client{
		Transport: transport,
	}

	// Prepare the upstream request (bound to client context so it cancels if client disconnects)
	req, err := http.NewRequestWithContext(ctx.Request.Context(), "GET", oriURL.String(), nil)
	if err != nil {
		utils.ErrorLog("Failed to create request: %v", err)
		ctx.AbortWithError(http.StatusInternalServerError, utils.PrintErrorAndReturn(err))
		return
	}

	// For VOD endpoints, some providers are extremely strict: use a whitelist header set
	p := oriURL.Path
	isVOD := strings.Contains(p, "/movie/") || strings.Contains(p, "/series/")
	if ext := strings.ToLower(path.Ext(p)); ext == ".mp4" || ext == ".mkv" || ext == ".ts" { isVOD = true }

	if isVOD {
		// Start with clean headers and add only known-good ones
		clean := http.Header{}
		// Accept
		if v := ctx.Request.Header.Get("Accept"); v != "" { clean.Set("Accept", v) } else { clean.Set("Accept", "*/*") }
		// Accept-Language
	if v := ctx.Request.Header.Get("Accept-Language"); v != "" { clean.Set("Accept-Language", v) } else { clean.Set("Accept-Language", utils.GetLanguageHeader()) }
		// Range
		if v := ctx.Request.Header.Get("Range"); v != "" { clean.Set("Range", v) } else { clean.Set("Range", "bytes=0-") }
		// Connection
		clean.Set("Connection", "keep-alive")
		// UA and encoding
		clean.Set("User-Agent", utils.GetIPTVUserAgent())
		clean.Set("Accept-Encoding", "identity")
		req.Header = clean
	} else {
		// Non-VOD: copy and normalize minimally
		mergeHttpHeader(req.Header, ctx.Request.Header)
		req.Header.Set("User-Agent", utils.GetIPTVUserAgent())
		req.Header.Del("Accept-Encoding")
		req.Header.Set("Accept-Encoding", "identity")
		if req.Header.Get("Accept") == "" { req.Header.Set("Accept", "*/*") }
		if req.Header.Get("Connection") == "" { req.Header.Set("Connection", "keep-alive") }
	}

	// Execute the upstream request
	resp, err := client.Do(req)
	if err != nil {
		utils.DebugLog("-> Upstream request error: %v", err)
		ctx.AbortWithError(http.StatusInternalServerError, utils.PrintErrorAndReturn(err))
		return
	}
	defer resp.Body.Close()

	utils.DebugLog("-> Upstream response status: %d", resp.StatusCode)
	if resp.StatusCode == 461 {
		utils.DebugLog("Upstream returned 461 (often blocks HEAD/Range or unexpected headers). UA=%q, AE=%q", req.Header.Get("User-Agent"), req.Header.Get("Accept-Encoding"))
	}

	// Copy response headers and status code
	mergeHttpHeader(ctx.Writer.Header(), resp.Header)
	ctx.Status(resp.StatusCode)

	// Stream the response body to the client with flushes
	w := ctx.Writer
	buf := make([]byte, 64*1024)

	for {
		// Respect client cancellation
		select {
		case <-ctx.Request.Context().Done():
			utils.DebugLog("Client cancelled stream for URL: %s", ctx.Request.URL)
			return
		default:
		}

		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				utils.DebugLog("Client write error: %v", werr)
				return
			}
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
		if rerr != nil {
			if rerr != io.EOF {
				utils.DebugLog("Upstream read error: %v", rerr)
			}
			return
		}
	}
}

type values []string

func (vs values) contains(s string) bool {
	for _, v := range vs {
		if v == s {
			return true
		}
	}

	return false
}

func mergeHttpHeader(dst, src http.Header) {
	for k, vv := range src {
		for _, v := range vv {
			if values(dst.Values(k)).contains(v) {
				continue
			}
			dst.Add(k, v)
		}
	}
}

// authRequest handle auth credentials
type authRequest struct {
	Username string `form:"username" binding:"required"`
	Password string `form:"password" binding:"required"`
}

func (c *Config) authenticate(ctx *gin.Context) {
	utils.DebugLog("-> Incoming URL: %s", ctx.Request.URL)
	var authReq authRequest
	if err := ctx.Bind(&authReq); err != nil {
		utils.DebugLog("Bind error: %v", err)
		ctx.AbortWithError(http.StatusBadRequest, err)
		return
	}

	// Only use LDAP authentication to validate client access
	if c.ProxyConfig.LDAPEnabled {
		utils.DebugLog("LDAP authentication enabled for user: %s", authReq.Username)
		ok := ldapAuthenticate(
			c.ProxyConfig.LDAPServer,
			c.ProxyConfig.LDAPBaseDN,
			c.ProxyConfig.LDAPBindDN,
			c.ProxyConfig.LDAPBindPassword,
			c.ProxyConfig.LDAPUserAttribute,
			c.ProxyConfig.LDAPGroupAttribute,
			c.ProxyConfig.LDAPRequiredGroup,
			authReq.Username,
			authReq.Password,
		)
		if !ok {
			utils.DebugLog("LDAP authentication failed for user: %s", authReq.Username)
			ctx.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		utils.DebugLog("LDAP authentication succeeded for user: %s", authReq.Username)
		return
	}

	// If LDAP is not enabled, fallback to local credentials
	utils.DebugLog("Local authentication for user: %s", authReq.Username)
	if c.ProxyConfig.User.String() != authReq.Username || c.ProxyConfig.Password.String() != authReq.Password {
		utils.DebugLog("Local authentication failed for user: %s", authReq.Username)
		ctx.AbortWithStatus(http.StatusUnauthorized)
	}
}

func (c *Config) appAuthenticate(ctx *gin.Context) {
	utils.DebugLog("-> Incoming URL: %s", ctx.Request.URL)

	contents, err := ioutil.ReadAll(ctx.Request.Body)
	if err != nil {
		ctx.AbortWithError(http.StatusInternalServerError, err) // nolint: errcheck
		return
	}

	q, err := url.ParseQuery(string(contents))
	if err != nil {
		ctx.AbortWithError(http.StatusInternalServerError, err) // nolint: errcheck
		return
	}
	if len(q["username"]) == 0 || len(q["password"]) == 0 {
		ctx.AbortWithError(http.StatusBadRequest, fmt.Errorf("bad body url query parameters")) // nolint: errcheck
		return
	}
	log.Printf("[stream-share] %v | %s |App Auth\n", time.Now().Format("2006/01/02 - 15:04:05"), ctx.ClientIP())

	// Use LDAP authentication if enabled
	if c.ProxyConfig.LDAPEnabled {
		utils.DebugLog("LDAP app authentication for user: %s", q["username"][0])
		ok := ldapAuthenticate(
			c.ProxyConfig.LDAPServer,
			c.ProxyConfig.LDAPBaseDN,
			c.ProxyConfig.LDAPBindDN,
			c.ProxyConfig.LDAPBindPassword,
			c.ProxyConfig.LDAPUserAttribute,
			c.ProxyConfig.LDAPGroupAttribute,
			c.ProxyConfig.LDAPRequiredGroup,
			q["username"][0],
			q["password"][0],
		)
		if !ok {
			utils.DebugLog("LDAP app authentication failed for user: %s", q["username"][0])
			ctx.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		utils.DebugLog("LDAP app authentication succeeded for user: %s", q["username"][0])
	} else if c.ProxyConfig.User.String() != q["username"][0] || c.ProxyConfig.Password.String() != q["password"][0] {
		utils.DebugLog("Local app authentication failed for user: %s", q["username"][0])
		ctx.AbortWithStatus(http.StatusUnauthorized)
		return
	}

	ctx.Request.Body = ioutil.NopCloser(bytes.NewReader(contents))
}

func ldapAuthenticate(server, baseDN, bindDN, bindPassword, userAttr, groupAttr, requiredGroup, username, password string) bool {
	utils.DebugLog("LDAP DialURL: %s", server)
	l, err := ldap.DialURL(server)
	if err != nil {
		utils.DebugLog("LDAP DialURL error: %v", err)
		return false
	}
	defer l.Close()

	// Bind with service account
	if bindDN != "" && bindPassword != "" {
		utils.DebugLog("LDAP service bind attempt: DN=%s", bindDN)
		if err := l.Bind(bindDN, bindPassword); err != nil {
			utils.DebugLog("LDAP service bind error: %v", err)
			return false
		}
		utils.DebugLog("LDAP service bind succeeded")
	}

	// Search for user DN
	filter := fmt.Sprintf("(%s=%s)", userAttr, ldap.EscapeFilter(username))
	utils.DebugLog("LDAP search: baseDN=%s, filter=%s", baseDN, filter)
	searchRequest := ldap.NewSearchRequest(
		baseDN,
		ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 1, 0, false,
		filter,
		[]string{"dn", groupAttr}, // Include group attribute
		nil,
	)
	sr, err := l.Search(searchRequest)
	if err != nil {
		utils.DebugLog("LDAP search error: %v", err)
		return false
	}
	if len(sr.Entries) == 0 {
		utils.DebugLog("LDAP search: no entries found for user: %s", username)
		return false
	}
	userDN := sr.Entries[0].DN
	utils.DebugLog("LDAP user DN found: %s", userDN)

	// Check group membership if requiredGroup is specified
	if requiredGroup != "" && groupAttr != "" {
		hasGroup := false
		// Get group attribute values
		for _, entry := range sr.Entries {
			for _, groupValue := range entry.GetAttributeValues(groupAttr) {
				utils.DebugLog("LDAP user group: %s", groupValue)
				// Check if group attribute value contains requiredGroup
				// This handles both direct membership values like 'iptv'
				// and DN-style values like 'cn=iptv,ou=groups,dc=example,dc=com'
				if strings.Contains(strings.ToLower(groupValue), strings.ToLower(requiredGroup)) {
					hasGroup = true
					break
				}
			}
		}

		if !hasGroup {
			utils.DebugLog("LDAP user %s is not a member of required group: %s", username, requiredGroup)
			return false
		}
		utils.DebugLog("LDAP user %s is a member of required group: %s", username, requiredGroup)
	}

	// Try to bind as user
	utils.DebugLog("LDAP user bind attempt: DN=%s", userDN)
	if err := l.Bind(userDN, password); err != nil {
		utils.DebugLog("LDAP user bind error: %v", err)
		return false
	}
	utils.DebugLog("LDAP user bind succeeded for user: %s", username)
	return true
}
