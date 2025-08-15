FROM golang:1.17-alpine

RUN apk add ca-certificates

WORKDIR /go/src/github.com/pierre-emmanuelJ/iptv-proxy
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o iptv-proxy .

FROM alpine:3
COPY --from=0  /go/src/github.com/pierre-emmanuelJ/iptv-proxy/iptv-proxy /

# Environment variables for LDAP support
ENV LDAP_ENABLED=false
ENV LDAP_SERVER=""
ENV LDAP_BASE_DN=""
ENV LDAP_BIND_DN=""
ENV LDAP_BIND_PASSWORD=""
ENV LDAP_USER_ATTRIBUTE="uid"
ENV LDAP_GROUP_ATTRIBUTE="memberOf"
ENV LDAP_REQUIRED_GROUP="iptv"

# Enable debug logging
ENV DEBUG_LOGGING=false

ENTRYPOINT ["/iptv-proxy"]
