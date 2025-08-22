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
    "errors"
    "fmt"
    "io/ioutil"
    "net/http"
    "net/url"
    "os"
    "path"
    "strings"
    "sync"
    "time"
	"log"

    "github.com/gin-gonic/gin"
    "github.com/jamesnetherton/m3u"
    "github.com/lucasduport/stream-share/pkg/utils"
    xtreamapi "github.com/lucasduport/stream-share/pkg/xtream"
)

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

// Prefer multiplexed streaming if enabled via env, otherwise fall back to legacy stream
// xtreamStream proxies streams; can switch to multiplexed mode via env flag.
func (c *Config) xtreamStream(ctx *gin.Context, oriURL *url.URL) {
    utils.DebugLog("-> Xtream streaming request: %s", ctx.Request.URL.Path)
    utils.DebugLog("-> Proxying to Xtream upstream: %s", oriURL.String())

    if c.sessionManager != nil && os.Getenv("FORCE_MULTIPLEXING") == "true" {
        utils.DebugLog("Using multiplexed streaming (FORCE_MULTIPLEXING=true)")
        c.multiplexedStream(ctx, oriURL)
        return
    }

    utils.DebugLog("Xtream backend request using Xtream credentials: user=%s, password=%s, baseURL=%s", c.XtreamUser.String(), c.XtreamPassword.String(), c.XtreamBaseURL)
    rawURL := fmt.Sprintf("%s/get.php?username=%s&password=%s", c.XtreamBaseURL, c.XtreamUser, c.XtreamPassword)

    q := ctx.Request.URL.Query()
    for k, v := range q {
        if k == "username" || k == "password" { continue }
        rawURL = fmt.Sprintf("%s&%s=%s", rawURL, k, strings.Join(v, ","))
    }

    m3uURL, err := url.Parse(rawURL)
    if err != nil { ctx.AbortWithError(http.StatusInternalServerError, utils.PrintErrorAndReturn(err)); return }

    xtreamM3uCacheLock.RLock()
    meta, ok := xtreamM3uCache[m3uURL.String()]
    d := time.Since(meta.Time)
    if !ok || d.Hours() >= float64(c.M3UCacheExpiration) {
        utils.InfoLog("xtream cache m3u file refresh requested by %s", ctx.ClientIP())
        xtreamM3uCacheLock.RUnlock()
        playlist, err := m3u.Parse(m3uURL.String())
        if err != nil { ctx.AbortWithError(http.StatusInternalServerError, utils.PrintErrorAndReturn(err)); return }
        if len(playlist.Tracks) == 0 { ctx.AbortWithError(http.StatusBadGateway, utils.PrintErrorAndReturn(fmt.Errorf("Xtream backend returned empty playlist"))); return }
        if err := c.cacheXtreamM3u(&playlist, m3uURL.String()); err != nil { ctx.AbortWithError(http.StatusInternalServerError, utils.PrintErrorAndReturn(err)); return }
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

func (c *Config) xtreamXMLTV(ctx *gin.Context) {
    client, err := xtreamapi.New(c.XtreamUser.String(), c.XtreamPassword.String(), c.XtreamBaseURL, ctx.Request.UserAgent())
    if err != nil { ctx.AbortWithError(http.StatusInternalServerError, utils.PrintErrorAndReturn(err)); return }
    resp, err := client.GetXMLTV()
    if err != nil { ctx.AbortWithError(http.StatusInternalServerError, utils.PrintErrorAndReturn(err)); return }
    ctx.Data(http.StatusOK, "application/xml", resp)
}

func (c *Config) xtreamStreamHandler(ctx *gin.Context) {
    id := ctx.Param("id")
    rpURL, err := url.Parse(fmt.Sprintf("%s/%s/%s/%s", c.XtreamBaseURL, c.XtreamUser, c.XtreamPassword, id))
    if err != nil { ctx.AbortWithError(http.StatusInternalServerError, utils.PrintErrorAndReturn(err)); return }
    c.xtreamStream(ctx, rpURL)
}

func (c *Config) xtreamStreamLive(ctx *gin.Context) {
    id := ctx.Param("id")
    rpURL, err := url.Parse(fmt.Sprintf("%s/live/%s/%s/%s", c.XtreamBaseURL, c.XtreamUser, c.XtreamPassword, id))
    if err != nil { ctx.AbortWithError(http.StatusInternalServerError, utils.PrintErrorAndReturn(err)); return }
    c.xtreamStream(ctx, rpURL)
}

func (c *Config) xtreamStreamPlay(ctx *gin.Context) {
    token := ctx.Param("token")
    t := ctx.Param("type")
    rpURL, err := url.Parse(fmt.Sprintf("%s/play/%s/%s", c.XtreamBaseURL, token, t))
    if err != nil { ctx.AbortWithError(http.StatusInternalServerError, utils.PrintErrorAndReturn(err)); return }
    c.xtreamStream(ctx, rpURL)
}

func (c *Config) xtreamStreamTimeshift(ctx *gin.Context) {
    duration := ctx.Param("duration")
    start := ctx.Param("start")
    id := ctx.Param("id")
    rpURL, err := url.Parse(fmt.Sprintf("%s/timeshift/%s/%s/%s/%s/%s", c.XtreamBaseURL, c.XtreamUser, c.XtreamPassword, duration, start, id))
    if err != nil { ctx.AbortWithError(http.StatusInternalServerError, utils.PrintErrorAndReturn(err)); return }
    c.stream(ctx, rpURL)
}

func (c *Config) xtreamStreamMovie(ctx *gin.Context) {
    id := ctx.Param("id")
    // Normalize DB key: cached entries are stored by bare stream_id without extension
    idRaw := strings.TrimSuffix(id, path.Ext(id))
    if c.db != nil {
        if entry, err := c.db.GetVODCache(idRaw); err == nil && entry != nil && entry.Status == "ready" {
            if fi, statErr := os.Stat(entry.FilePath); statErr == nil && !fi.IsDir() {
                utils.InfoLog("Serving cached movie for %s from %s", idRaw, entry.FilePath)
                var ct string
                if ext := strings.ToLower(path.Ext(entry.FilePath)); ext == ".ts" { ct = "video/mp2t" } else if ext == ".mkv" { ct = "video/x-matroska" } else { ct = "video/mp4" }
                c.db.TouchVODCache(idRaw)
                serveLocalFileRange(ctx, entry.FilePath, ct, "", false)
                return
            }
            utils.WarnLog("Cached movie missing on disk for %s at %s; falling back to upstream", idRaw, entry.FilePath)
        }
    }
    rpURL, err := url.Parse(fmt.Sprintf("%s/movie/%s/%s/%s", c.XtreamBaseURL, c.XtreamUser, c.XtreamPassword, id))
    if err != nil { ctx.AbortWithError(http.StatusInternalServerError, utils.PrintErrorAndReturn(err)); return }
    utils.DebugLog("Movie streaming request - using Xtream credentials for upstream: %s", rpURL.String())
    c.xtreamStream(ctx, rpURL)
}

func (c *Config) xtreamStreamSeries(ctx *gin.Context) {
    id := ctx.Param("id")
    idRaw := strings.TrimSuffix(id, path.Ext(id))
    if c.db != nil {
        if entry, err := c.db.GetVODCache(idRaw); err == nil && entry != nil && entry.Status == "ready" {
            if fi, statErr := os.Stat(entry.FilePath); statErr == nil && !fi.IsDir() {
                utils.InfoLog("Serving cached episode for %s from %s", idRaw, entry.FilePath)
                var ct string
                if ext := strings.ToLower(path.Ext(entry.FilePath)); ext == ".ts" { ct = "video/mp2t" } else if ext == ".mkv" { ct = "video/x-matroska" } else { ct = "video/mp4" }
                c.db.TouchVODCache(idRaw)
                serveLocalFileRange(ctx, entry.FilePath, ct, "", false)
                return
            }
            utils.WarnLog("Cached episode missing on disk for %s at %s; falling back to upstream", idRaw, entry.FilePath)
        }
    }
    rpURL, err := url.Parse(fmt.Sprintf("%s/series/%s/%s/%s", c.XtreamBaseURL, c.XtreamUser, c.XtreamPassword, id))
    if err != nil { ctx.AbortWithError(http.StatusInternalServerError, utils.PrintErrorAndReturn(err)); return }
    c.xtreamStream(ctx, rpURL)
}

// Direct handlers using proxy credentials
func (c *Config) xtreamProxyCredentialsStreamHandler(ctx *gin.Context) {
    id := ctx.Param("id")
    utils.DebugLog("Direct stream request with proxy credentials: username=%s, id=%s", ctx.Param("username"), id)
    rpURL, err := url.Parse(fmt.Sprintf("%s/%s/%s/%s", c.XtreamBaseURL, c.XtreamUser, c.XtreamPassword, id))
    if err != nil { utils.ErrorLog("Failed to parse upstream URL: %v", err); ctx.AbortWithStatus(500); return }
    c.multiplexedStream(ctx, rpURL)
}

func (c *Config) xtreamProxyCredentialsLiveStreamHandler(ctx *gin.Context) {
    id := ctx.Param("id")
    utils.DebugLog("Direct live stream request with proxy credentials: username=%s, id=%s", ctx.Param("username"), id)
    rpURL, err := url.Parse(fmt.Sprintf("%s/live/%s/%s/%s", c.XtreamBaseURL, c.XtreamUser, c.XtreamPassword, id))
    if err != nil { utils.ErrorLog("Failed to parse upstream URL: %v", err); ctx.AbortWithStatus(500); return }
    c.multiplexedStream(ctx, rpURL)
}

func (c *Config) xtreamProxyCredentialsMovieStreamHandler(ctx *gin.Context) {
    id := ctx.Param("id")
    idRaw := strings.TrimSuffix(id, path.Ext(id))
    utils.DebugLog("Direct movie stream request with proxy credentials: username=%s, id=%s", ctx.Param("username"), id)
    if c.db != nil {
        if entry, err := c.db.GetVODCache(idRaw); err == nil && entry != nil && entry.Status == "ready" {
            if fi, statErr := os.Stat(entry.FilePath); statErr == nil && !fi.IsDir() {
                utils.InfoLog("Serving cached movie (proxy creds path) for %s from %s", idRaw, entry.FilePath)
                var ct string
                if ext := strings.ToLower(path.Ext(entry.FilePath)); ext == ".ts" { ct = "video/mp2t" } else if ext == ".mkv" { ct = "video/x-matroska" } else { ct = "video/mp4" }
                c.db.TouchVODCache(idRaw)
                serveLocalFileRange(ctx, entry.FilePath, ct, "", false)
                return
            }
            utils.WarnLog("Cached movie (proxy creds) missing on disk for %s at %s; falling back to upstream", idRaw, entry.FilePath)
        }
    }
    rpURL, err := url.Parse(fmt.Sprintf("%s/movie/%s/%s/%s", c.XtreamBaseURL, c.XtreamUser, c.XtreamPassword, id))
    if err != nil { utils.ErrorLog("Failed to parse upstream URL: %v", err); ctx.AbortWithStatus(500); return }
    c.multiplexedStream(ctx, rpURL)
}

func (c *Config) xtreamProxyCredentialsSeriesStreamHandler(ctx *gin.Context) {
    id := ctx.Param("id")
    idRaw := strings.TrimSuffix(id, path.Ext(id))
    utils.DebugLog("Direct series stream request with proxy credentials: username=%s, id=%s", ctx.Param("username"), id)
    if c.db != nil {
        if entry, err := c.db.GetVODCache(idRaw); err == nil && entry != nil && entry.Status == "ready" {
            if fi, statErr := os.Stat(entry.FilePath); statErr == nil && !fi.IsDir() {
                utils.InfoLog("Serving cached episode (proxy creds path) for %s from %s", idRaw, entry.FilePath)
                var ct string
                if ext := strings.ToLower(path.Ext(entry.FilePath)); ext == ".ts" { ct = "video/mp2t" } else if ext == ".mkv" { ct = "video/x-matroska" } else { ct = "video/mp4" }
                c.db.TouchVODCache(idRaw)
                serveLocalFileRange(ctx, entry.FilePath, ct, "", false)
                return
            }
            utils.WarnLog("Cached episode (proxy creds) missing on disk for %s at %s; falling back to upstream", idRaw, entry.FilePath)
        }
    }
    rpURL, err := url.Parse(fmt.Sprintf("%s/series/%s/%s/%s", c.XtreamBaseURL, c.XtreamUser, c.XtreamPassword, id))
    if err != nil { utils.ErrorLog("Failed to parse upstream URL: %v", err); ctx.AbortWithStatus(500); return }
    c.multiplexedStream(ctx, rpURL)
}

// HLS helpers and handlers
var hlsChannelsRedirectURL map[string]url.URL = map[string]url.URL{}
var hlsChannelsRedirectURLLock = sync.RWMutex{}

func (c *Config) xtreamHlsStream(ctx *gin.Context) {
    chunk := ctx.Param("chunk")
    s := strings.Split(chunk, "_")
    if len(s) != 2 {
        ctx.AbortWithError(http.StatusInternalServerError, utils.PrintErrorAndReturn(errors.New("HSL malformed chunk")))
        return
    }
    channel := s[0]

    redirURL, err := getHlsRedirectURL(channel)
    if err != nil { ctx.AbortWithError(http.StatusInternalServerError, utils.PrintErrorAndReturn(err)); return }

    req, reqErr := http.NewRequestWithContext(ctx.Request.Context(), "GET", fmt.Sprintf("%s://%s/hls/%s/%s", redirURL.Scheme, redirURL.Host, ctx.Param("token"), ctx.Param("chunk")), nil)
    if reqErr != nil { ctx.AbortWithError(http.StatusInternalServerError, utils.PrintErrorAndReturn(reqErr)); return }

    mergeHttpHeader(req.Header, ctx.Request.Header)

    resp, doErr := http.DefaultClient.Do(req)
    if doErr != nil { ctx.AbortWithError(http.StatusInternalServerError, utils.PrintErrorAndReturn(doErr)); return }
    defer resp.Body.Close()

    if resp.StatusCode == http.StatusFound {
        loc, locErr := resp.Location()
        if locErr != nil { ctx.AbortWithError(http.StatusInternalServerError, utils.PrintErrorAndReturn(locErr)); return }
        id := ctx.Param("id")
        if strings.Contains(loc.String(), id) {
            hlsChannelsRedirectURLLock.Lock(); hlsChannelsRedirectURL[id] = *loc; hlsChannelsRedirectURLLock.Unlock()
            hlsReq, hlsReqErr := http.NewRequestWithContext(ctx.Request.Context(), "GET", loc.String(), nil)
            if hlsReqErr != nil { ctx.AbortWithError(http.StatusInternalServerError, utils.PrintErrorAndReturn(hlsReqErr)); return }
            mergeHttpHeader(hlsReq.Header, ctx.Request.Header)
            hlsResp, hlsDoErr := http.DefaultClient.Do(hlsReq)
            if hlsDoErr != nil { ctx.AbortWithError(http.StatusInternalServerError, utils.PrintErrorAndReturn(hlsDoErr)); return }
            defer hlsResp.Body.Close()

            b, readErr := ioutil.ReadAll(hlsResp.Body)
            if readErr != nil { ctx.AbortWithError(http.StatusInternalServerError, utils.PrintErrorAndReturn(readErr)); return }
            body := string(b)
            body = strings.ReplaceAll(body, "/"+c.XtreamUser.String()+"/"+c.XtreamPassword.String()+"/", "/"+c.User.String()+"/"+c.Password.String()+"/")
            utils.DebugLog("HLS stream response modified to use proxy credentials for client URLs")
            mergeHttpHeader(ctx.Writer.Header(), hlsResp.Header)
            ctx.Data(http.StatusOK, hlsResp.Header.Get("Content-Type"), []byte(body))
            return
        }
        ctx.AbortWithError(http.StatusInternalServerError, utils.PrintErrorAndReturn(errors.New("Unable to HLS stream")))
        return
    }

    utils.DebugLog("HLS stream response status: %d", resp.StatusCode)
    ctx.Status(resp.StatusCode)
}

func (c *Config) hlsXtreamStream(ctx *gin.Context, oriURL *url.URL) {
    utils.DebugLog("HLS stream request with URL: %s", oriURL.String())
    client := &http.Client{ CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse } }
    req, reqErr := http.NewRequestWithContext(ctx.Request.Context(), "GET", oriURL.String(), nil)
    if reqErr != nil { ctx.AbortWithError(http.StatusInternalServerError, utils.PrintErrorAndReturn(reqErr)); return }
    mergeHttpHeader(req.Header, ctx.Request.Header)
    resp, doErr := client.Do(req)
    if doErr != nil { ctx.AbortWithError(http.StatusInternalServerError, utils.PrintErrorAndReturn(doErr)); return }
    defer resp.Body.Close()

    if resp.StatusCode == http.StatusFound {
        loc, locErr := resp.Location()
        if locErr != nil { ctx.AbortWithError(http.StatusInternalServerError, utils.PrintErrorAndReturn(locErr)); return }
        id := ctx.Param("id")
        if strings.Contains(loc.String(), id) {
            hlsChannelsRedirectURLLock.Lock(); hlsChannelsRedirectURL[id] = *loc; hlsChannelsRedirectURLLock.Unlock()
            hlsReq, hlsReqErr := http.NewRequestWithContext(ctx.Request.Context(), "GET", loc.String(), nil)
            if hlsReqErr != nil { ctx.AbortWithError(http.StatusInternalServerError, utils.PrintErrorAndReturn(hlsReqErr)); return }
            mergeHttpHeader(hlsReq.Header, ctx.Request.Header)
            hlsResp, hlsDoErr := client.Do(hlsReq)
            if hlsDoErr != nil { ctx.AbortWithError(http.StatusInternalServerError, utils.PrintErrorAndReturn(hlsDoErr)); return }
            defer hlsResp.Body.Close()

            b, readErr := ioutil.ReadAll(hlsResp.Body)
            if readErr != nil { ctx.AbortWithError(http.StatusInternalServerError, utils.PrintErrorAndReturn(readErr)); return }
            body := string(b)
            body = strings.ReplaceAll(body, "/"+c.XtreamUser.String()+"/"+c.XtreamPassword.String()+"/", "/"+c.User.String()+"/"+c.Password.String()+"/")
            utils.DebugLog("HLS stream response modified to use proxy credentials for client URLs")
            mergeHttpHeader(ctx.Writer.Header(), hlsResp.Header)
            ctx.Data(http.StatusOK, hlsResp.Header.Get("Content-Type"), []byte(body))
            return
        }
        ctx.AbortWithError(http.StatusInternalServerError, utils.PrintErrorAndReturn(errors.New("Unable to HLS stream")))
        return
    }

    utils.DebugLog("HLS stream response status: %d", resp.StatusCode)
    ctx.Status(resp.StatusCode)
}

func (c *Config) xtreamHlsrStream(ctx *gin.Context) {
    channel := ctx.Param("channel")
    redirURL, err := getHlsRedirectURL(channel)
    if err != nil { ctx.AbortWithError(http.StatusInternalServerError, utils.PrintErrorAndReturn(err)); return }
    nextURL, parseErr := url.Parse(fmt.Sprintf("%s://%s/hlsr/%s/%s/%s/%s/%s/%s", redirURL.Scheme, redirURL.Host, ctx.Param("token"), c.XtreamUser, c.XtreamPassword, ctx.Param("channel"), ctx.Param("hash"), ctx.Param("chunk")))
    if parseErr != nil { ctx.AbortWithError(http.StatusInternalServerError, utils.PrintErrorAndReturn(parseErr)); return }
    c.hlsXtreamStream(ctx, nextURL)
}

// Restore helper used by HLS handlers
func getHlsRedirectURL(channel string) (*url.URL, error) {
    hlsChannelsRedirectURLLock.RLock(); defer hlsChannelsRedirectURLLock.RUnlock()
    u, ok := hlsChannelsRedirectURL[channel+".m3u8"]
    if !ok { return nil, utils.PrintErrorAndReturn(errors.New("HSL redirect url not found")) }
    return &u, nil
}
