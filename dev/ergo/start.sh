#!/bin/sh
# Builds this network's config from the image's stock one (which already
# listens on plain-text :6667) with its own network and server name, then
# starts ergo the way the image's own entrypoint does.
set -e
sed -e "s/^    name: ErgoTest$/    name: $NETWORK/" \
    -e "s/^    name: ergo.test$/    name: $SERVER/" \
    -e 's|path: languages|path: /ircd-bin/languages|' \
    /ircd-bin/default.yaml > /ircd/ircd.yaml
sed "s/NETWORK/$NETWORK/" /dev-ergo/motd.txt > /ircd/ergo.motd
# /ircd is a volume, so the certs survive restarts; mkcerts refuses to
# overwrite them and set -e would turn that into a crash loop.
[ -f fullchain.pem ] && [ -f privkey.pem ] || /ircd-bin/ergo mkcerts
exec /ircd-bin/ergo run
