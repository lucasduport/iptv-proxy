FROM golang:1.17-alpine

RUN apk add ca-certificates

WORKDIR /go/src/github.com/lucasduport/iptv-proxy
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o iptv-proxy .

FROM alpine:3
COPY --from=0  /go/src/github.com/lucasduport/iptv-proxy/iptv-proxy /

ENTRYPOINT ["/iptv-proxy"]
