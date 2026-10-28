# soju has no official image, so build it. moderncsqlite is the pure-Go
# SQLite driver, since the Alpine Go image has no C compiler.
FROM golang:1.26-alpine AS build
RUN go install -tags moderncsqlite \
    codeberg.org/emersion/soju/cmd/soju@v0.11.1 \
    codeberg.org/emersion/soju/cmd/sojuctl@v0.11.1

FROM alpine:3.22
RUN apk add --no-cache openssl
COPY --from=build /go/bin/soju /go/bin/sojuctl /usr/local/bin/
COPY config /etc/soju/config
COPY start.sh /start.sh
EXPOSE 6667 6697 8080 8443
ENTRYPOINT ["/bin/sh", "/start.sh"]
