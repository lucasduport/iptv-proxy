# StreamShare - Advanced IPTV Access Management Platform

## Overview

StreamShare is a comprehensive IPTV management solution that allows secure sharing of a single IPTV provider account with multiple users. Built upon the foundations of [jtdevops/iptv-proxy](https://github.com/jtdevops/iptv-proxy) and [pierre-emmanuelJ/iptv-proxy](https://github.com/pierre-emmanuelJ/iptv-proxy), this project has evolved far beyond a simple proxy to become a full-featured platform with authentication, stream multiplexing, and media management capabilities.

### Key Features

- **Stream Multiplexing** - Share a single IPTV subscription with multiple users simultaneously
- **Authentication Options**
  - LDAP integration for enterprise user management
  - Basic authentication for simpler setups
- **Content Management**
  - M3U/M3U8 playlist proxying with credential protection
  - Xtream Codes API compatibility (live, VOD, series, EPG)
  - Robust handling of Unicode characters and malformed responses
- **User Experience**
  - Discord bot with embed-based responses for content discovery
  - Temporary streaming links for content sharing
  - Session management with configurable timeouts
- **Administration**
  - Status API for monitoring active streams and users
  - Stream timeout enforcement and user management
  - PostgreSQL persistence for settings and state
- **Deployment**
  - Docker-ready with comprehensive environment variables
  - Reverse proxy compatibility with HTTPS support


### Upcoming Features
- **Enhanced Vod Search** - Improved search capabilities for Video on Demand content
- **Episode Sorting** - Make sure episodes are well referenced in m3u playlists
- **VOD Caching** - Cache VOD content for a smooth stream access while someone is already watching

---

## How It Works

### Stream Multiplexing Technology

StreamShare's revolutionary feature is its ability to efficiently multiplex streams. When multiple users request the same content:

1. The first user request establishes a single connection to the IPTV provider
2. StreamShare buffers the incoming stream data
3. All subsequent user requests for the same content receive data from this buffer
4. The provider sees only one connection, while multiple users can watch simultaneously
5. When all users disconnect, the upstream connection is gracefully closed

This technology significantly reduces load on the IPTV provider, prevents account limiting/banning for multiple connections, and improves stream start times for subsequent viewers.

### M3U/M3U8 Proxy

StreamShare transforms original IPTV playlist URLs into secure endpoints on your server:

**Original M3U Example:**
```m3u
#EXTM3U
#EXTINF:-1 tvg-ID="examplechanel1.com" tvg-name="chanel1" tvg-logo="http://ch.xyz/logo1.png" group-title="USA HD",CHANEL1-HD
http://iptvexample.net:1234/12/test/1
```

**Proxied Output:**
```m3u
#EXTM3U
#EXTINF:-1 tvg-ID="examplechanel1.com" tvg-name="chanel1" tvg-logo="http://ch.xyz/logo1.png" group-title="USA HD",CHANEL1-HD
http://yourstreamshare.com:8080/12/test/1?username=test&password=passwordtest
```

**Quick Start Example:**
```bash
streamshare --m3u-url http://provider.com/get.php?username=user&password=pass&type=m3u_plus&output=m3u8 \
       --port 8080 \
       --hostname streamshare.example.com \
       --user test \
       --password passwordtest
```
Access your playlist at:  
`http://streamshare.example.com:8080/iptv.m3u?username=test&password=passwordtest`

### Xtream Codes API Compatibility

StreamShare fully supports the Xtream Codes API with enhanced error handling and response sanitization:

```bash
streamshare --m3u-url http://provider.com:1234/get.php?username=user&password=pass&type=m3u_plus&output=m3u8 \
       --port 8080 \
       --hostname streamshare.example.com \
       --xtream-user provider_username \
       --xtream-password provider_password \
       --xtream-base-url http://provider.com:1234 \
       --user your_username \
       --password your_password
```

**Access with Your Credentials:**
```
user: your_username
password: your_password
base-url: http://streamshare.example.com:8080
```

---

## Discord Bot Integration

StreamShare includes a powerful Discord bot for content discovery and streaming. When enabled with the `DISCORD_BOT_TOKEN` environment variable, users can:

### Commands

| Command | Description |
|---------|-------------|
| `!link <ldap_username>` | Link your Discord account with your LDAP username |
| `!vod <query>` | Search specifically for VOD content |
| `!status` | Show server status (admin only) |
| `!help` | Display available commands |
| `!disconnect <ldap_username>` | Disconnect user from the stream |
| `!timeout <ldap_username> <duration>` | Set a timeout for user activity |

---

## API Documentation

StreamShare provides a REST API for integration and management:

### Endpoints

| Endpoint | Method | Description | Authentication |
|----------|--------|-------------|----------------|
| `/api/status` | GET | Get server status summary | API key |
| `/api/streams` | GET | List all active streams | API key |
| `/api/users` | GET | List all connected users | API key |
| `/api/templink` | POST | Create temporary download link | API key |
| `/api/search` | GET | Search content | API key |

### Authentication

API requests require an API key provided in the `X-API-Key` header:

```bash
curl -H "X-API-Key: your_api_key" https://streamshare.example.com/api/status
```

The API key is automatically generated on first run and stored in the database.

---

## Session Management

StreamShare includes sophisticated session management with configurable timeouts:

- **User Sessions** - Track user logins and activity
- **Stream Sessions** - Monitor and manage active streams
- **Temporary Links** - Create expiring download URLs

Configure with environment variables:
```
SESSION_TIMEOUT_MINUTES=120  # User session timeout (default: 60)
STREAM_TIMEOUT_MINUTES=240   # Stream session timeout (default: 120)
TEMP_LINK_HOURS=24           # Temporary link validity (default: 24)
```

---

## Full Docker Compose Example

```yaml
services:
  streamshare:
    image: lucasduport/streamshare:latest
    container_name: streamshare
    restart: unless-stopped
    ports:
      - "8080:8080"
    environment:
      # --- REQUIRED SETTINGS ---
      M3U_URL: "http://provider.example.com/playlist.m3u"
      PORT: 8080
      HOSTNAME: "streamshare.example.com"

      # --- IPTV PROVIDER SETTINGS ---
      XTREAM_USER: "provider_username"
      XTREAM_PASSWORD: "provider_password"
      XTREAM_BASE_URL: "http://provider.example.com:1234"

      # --- AUTHENTICATION ---
      # For basic authentication:
      USER: "local_user"
      PASSWORD: "local_password"
      
      # For LDAP authentication:
      LDAP_ENABLED: "true"
      LDAP_SERVER: "ldap://ldap.example.com:389"
      LDAP_BASE_DN: "ou=people,dc=example,dc=com"
      LDAP_BIND_DN: "uid=admin,ou=people,dc=example,dc=com"
      LDAP_BIND_PASSWORD: "admin_password"
      LDAP_USER_ATTRIBUTE: "uid"
      LDAP_GROUP_ATTRIBUTE: "memberOf"
      LDAP_REQUIRED_GROUP: "iptv"

      # --- MULTIPLEXING & SESSIONS ---
      SESSION_TIMEOUT_MINUTES: "120"
      STREAM_TIMEOUT_MINUTES: "240"
      TEMP_LINK_HOURS: "24"
      FORCE_MULTIPLEXING: "true"  # Force multiplexing for all streams

      # --- DISCORD BOT ---
      DISCORD_BOT_TOKEN: "your_discord_bot_token"
      DISCORD_BOT_PREFIX: "!"
      DISCORD_ADMIN_ROLE_ID: "1234567890"
      
      # --- DATABASE ---
      DB_HOST: "postgres"
      DB_PORT: "5432"
      DB_NAME: "streamshare"
      DB_USER: "streamshare"
      DB_PASSWORD: "dbpassword"
      
      # --- PERFORMANCE & LOGGING ---
      GIN_MODE: "release"
      DEBUG_LOGGING: "false"
      CACHE_FOLDER: "/cache"

    volumes:
      - streamshare_data:/data
      - streamshare_cache:/cache
    depends_on:
      - postgres

  postgres:
    image: postgres:14-alpine
    container_name: streamshare-db
    restart: unless-stopped
    environment:
      POSTGRES_USER: streamshare
      POSTGRES_PASSWORD: dbpassword
      POSTGRES_DB: streamshare
    volumes:
      - postgres_data:/var/lib/postgresql/data

volumes:
  streamshare_data:
  streamshare_cache:
  postgres_data:
```

---

## Advanced Configuration

### Direct Stream URLs

StreamShare supports direct stream URLs with proxy authentication in the path:

```
https://streamshare.example.com/username/password/12345
https://streamshare.example.com/live/username/password/12345
https://streamshare.example.com/movie/username/password/12345
https://streamshare.example.com/series/username/password/12345
```

These URLs are useful for direct integration with media players and other systems.

### Temporary Links

Generate temporary download links that expire after a configurable period:

```
https://streamshare.example.com/download/a1b2c3d4e5f6
```

Temporary links are perfect for sharing VOD content with users who don't have StreamShare accounts.

---

## Database Support

PostgreSQL is required for state persistence. Configure with:
- `DB_HOST`, `DB_PORT`, `DB_NAME`, `DB_USER`, `DB_PASSWORD`

---

## Powered By

- [go-ldap/ldap](https://github.com/go-ldap/ldap) - LDAP authentication
- [spf13/cobra](https://github.com/spf13/cobra) - Command-line interface
- [bwmarrin/discordgo](https://github.com/bwmarrin/discordgo) - Discord bot integration
- [tellytv/go.xtream-codes](https://github.com/tellytv/go.xtream-codes) - Xtream Codes client
- [gin-gonic/gin](https://github.com/gin-gonic/gin) - Web framework

---

## Support

If you find StreamShare useful, consider supporting its development:

[![paypal](https://www.paypalobjects.com/en_US/i/btn/btn_donateCC_LG.gif)](https://www.paypal.me/lucasdup135)

