#!/bin/sh
# Starts soju and, the first time, creates the trout user with both dev
# networks and a channel on each. Setup steps may fail harmlessly when
# they already happened; soju keeps running either way.
mkdir -p /run/soju /data
rm -f /run/soju/admin # left over if the container restarted
soju -config /etc/soju/config &
pid=$!
i=0
until [ -S /run/soju/admin ]; do
    i=$((i + 1))
    [ "$i" -gt 100 ] && { echo "soju didn't start" >&2; exit 1; }
    sleep 0.1
done
if [ ! -f /data/.seeded ]; then
    ctl() { sojuctl -config /etc/soju/config "$@" || echo "setup step failed (fine if it already exists): $*" >&2; }
    ctl user create -username trout -password trout -admin
    ctl user run trout network create -addr irc+insecure://troutnet:6667 -name TroutNet
    ctl user run trout network create -addr irc+insecure://devnet:6667 -name DevNet
    # soju names a channel's network with a "/network" suffix.
    ctl user run trout channel create '#trout/TroutNet'
    ctl user run trout channel create '#dev/DevNet'
    touch /data/.seeded
fi
wait "$pid"
