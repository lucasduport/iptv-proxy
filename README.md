# IPTV Proxy Server with LDAP Authentication

## Overview

This project is a feature-rich IPTV proxy server with LDAP authentication, based on [jtdevops/iptv-proxy](https://github.com/jtdevops/iptv-proxy) (itself a fork of [pierre-emmanuelJ/iptv-proxy](https://github.com/pierre-emmanuelJ/iptv-proxy)). It fixes parsing bugs (Xtream Codes EPG/VOD) and adds LDAP authentication.

### Key Features

- Xtream Codes EPG/VOD parsing fixes
  - Robust handling of Unicode characters in provider responses
  - Fallback mechanisms for malformed JSON data
  - Advanced sanitization for category data
- M3U/M3U8 playlist proxying
- Xtream Codes client API proxying (live, VOD, series, EPG)
- **LDAP authentication support**
- Docker-ready deployment

---

## How It Works

### M3U/M3U8 Proxy

The proxy transforms original IPTV playlist URLs into endpoints on your proxy server, securing access via authentication.

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
http://proxyserver.com:8080/12/test/1?username=test&password=passwordtest
```

**Start Example:**
```bash
iptv-proxy --m3u-url http://example.com/get.php?username=user&password=pass&type=m3u_plus&output=m3u8 \
       --port 8080 \
       --hostname proxyexample.com \
       --user test \
       --password passwordtest
```
Access your playlist at:  
`http://proxyserver.com:8080/iptv.m3u?username=test&password=passwordtest`

### Xtream Codes API Proxy

Proxy all Xtream Codes API endpoints (live, VOD, series, EPG) using your proxy credentials.

**Start Example:**
```bash
iptv-proxy --m3u-url http://example.com:1234/get.php?username=user&password=pass&type=m3u_plus&output=m3u8 \
       --port 8080 \
       --hostname proxyexample.com \
       --xtream-user xtream_user \
       --xtream-password xtream_password \
       --xtream-base-url http://example.com:1234 \
       --user test \
       --password passwordtest
```

**Proxied Xtream Credentials:**
```
user: test
password: passwordtest
base-url: http://proxyexample.com:8080
```
Access the playlist:  
`http://proxyexample.com:8080/get.php?username=test&password=passwordtest&type=m3u_plus&output=ts`

---

## Full Docker Compose Example

Below is a complete `docker-compose.yml` configuration for running the IPTV Proxy with LDAP authentication.  
Comments explain which settings are required, optional, and how each part works.

```yaml
services:
  iptv-proxy:
    image: lucasduport/iptv-proxy:latest  # Use the latest image from Docker Hub
    container_name: iptv-proxy
    restart: unless-stopped
    ports:
      - "8080:8080"  # Expose port 8080 (change if needed)
    environment:
      # --- REQUIRED SETTINGS ---
      M3U_URL: "http://example.com/playlist.m3u"  # Source playlist URL (required)
      PORT: 8080                                  # Internal proxy port (should match 'ports')
      HOSTNAME: "my-iptv-proxy.example.com"        # Public hostname for proxied URLs

      # --- OPTIONAL / RECOMMENDED SETTINGS ---
      ADVERTISED_PORT: 443                        # Set to 443 if behind HTTPS reverse proxy
      GIN_MODE: release                           # Use 'release' for production, 'debug' for development
      HTTPS: 1                                    # Set to 1 if using HTTPS

      # --- XTREAM CODES BACKEND (REQUIRED for API proxying) ---
      XTREAM_USER: "provider_username"             # Xtream Codes backend username
      XTREAM_PASSWORD: "provider_password"         # Xtream Codes backend password
      XTREAM_BASE_URL: "http://provider.example.com:1234"  # Xtream Codes backend URL

      # --- LOCAL/BASIC AUTH (if LDAP not enabled) ---
      USER: "local_user"                          # Local username (used if LDAP is disabled)
      PASSWORD: "local_password"                  # Local password (used if LDAP is disabled)

      # --- LDAP AUTHENTICATION (enable for LDAP login) ---
      LDAP_ENABLED: "true"                        # Set to "true" to enable LDAP authentication
      LDAP_SERVER: "ldap://ldap.example.com:389"  # LDAP server URL
      LDAP_BASE_DN: "ou=people,dc=example,dc=com" # Base DN for user search
      LDAP_BIND_DN: "uid=admin,ou=people,dc=example,dc=com" # Bind DN for LDAP admin
      LDAP_BIND_PASSWORD: "admin_password"        # Bind password for LDAP admin
      LDAP_USER_ATTRIBUTE: "uid"                  # LDAP attribute for username lookup
      LDAP_GROUP_ATTRIBUTE: "memberOf"            # LDAP attribute for group membership
      LDAP_REQUIRED_GROUP: "iptv"                 # Require users to be in this group

      # --- DEBUGGING ---
      DEBUG_LOGGING: "true"                       # Set to "false" for production

    volumes:
      - iptv_config:/root/.iptv-proxy             # Persist config/cache data

volumes:
  iptv_config:
```

**How it works:**
- The proxy container starts and loads environment variables for configuration.
- If `LDAP_ENABLED` is `"true"`, users must authenticate via LDAP (all LDAP settings required).
- If `LDAP_ENABLED` is not set or `"false"`, local `USER` and `PASSWORD` are used for basic authentication.
- Xtream Codes settings are required for API proxying (live, VOD, series, EPG).
- Playlist and API endpoints are exposed on the configured port.
- Persistent data (config, cache) is stored in the named Docker volume.

**Tip:**  
Remove or comment out LDAP settings if you do not use LDAP authentication.  
Set `DEBUG_LOGGING` to `"false"` for production deployments.

### Notes

- **Xtream API Credentials:** All backend queries use `XTREAM_USER`, `XTREAM_PASSWORD`, and `XTREAM_BASE_URL`.
- **Authentication:** Set `LDAP_ENABLED` to `"true"` for LDAP authentication, otherwise basic auth is used.
- **Performance:** Set `DEBUG_LOGGING: "false"` for production.
- **HTTPS:** If behind a reverse proxy, set `ADVERTISED_PORT` to 443 and `HTTPS` to 1.

---

## Powered By

- [go-ldap/ldap](https://github.com/go-ldap/ldap)
- [spf13/cobra](https://github.com/spf13/cobra)
- [tellytv/go.xtream-codes](https://github.com/tellytv/go.xtream-codes)
- [gin-gonic/gin](https://github.com/gin-gonic/gin)

---

## Support

If you find this project useful, consider supporting its development:

[![paypal](https://www.paypalobjects.com/en_US/i/btn/btn_donateCC_LG.gif)](https://www.paypal.me/lucasdup135)

