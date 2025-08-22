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
    "path"
    "strings"
    "net/http"

    "github.com/gin-gonic/gin"
)

// isVODPath reports whether the given URL path likely targets VOD content
// (movie or series) based on known path segments or file extensions.
func isVODPath(p string) bool {
    lp := strings.ToLower(p)
    if strings.Contains(lp, "/movie/") || strings.Contains(lp, "/series/") {
        return true
    }
    switch strings.ToLower(path.Ext(lp)) {
    case ".mp4", ".mkv", ".ts":
        return true
    default:
        return false
    }
}

// contentTypeForPath maps a file extension or known path to an appropriate
// Content-Type value for streaming responses.
func contentTypeForPath(p string) string {
    lp := strings.ToLower(p)
    ext := strings.ToLower(path.Ext(lp))
    if strings.Contains(lp, "/live/") || ext == ".ts" {
        return "video/mp2t"
    }
    switch ext {
    case ".m3u8":
        return "application/vnd.apple.mpegurl"
    case ".mp4":
        return "video/mp4"
    case ".mkv":
        return "video/x-matroska"
    default:
        return "application/octet-stream"
    }
}

// setNoBufferingHeaders configures common headers to minimize intermediary
// buffering and keep the connection alive during long-running streams.
func setNoBufferingHeaders(ctx *gin.Context, contentType string) {
    if contentType != "" {
        ctx.Header("Content-Type", contentType)
    }
    ctx.Header("Cache-Control", "no-store")
    ctx.Header("Pragma", "no-cache")
    ctx.Header("Connection", "keep-alive")
    ctx.Header("X-Accel-Buffering", "no")
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

// mergeHttpHeader copies headers from src to dst without duplicating identical values.
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
