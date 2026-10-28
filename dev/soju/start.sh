#!/bin/sh
# Starts soju and, the first time, creates the trout user with both dev
# networks and a channel on each. Setup steps may fail harmlessly when
# they already happened; soju keeps running either way.
mkdir -p /run/soju /data /data/uploads
# A self-signed certificate, made once so its key (and pin) stay put.
[ -f /data/tls.key ] || openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 \
    -nodes -keyout /data/tls.key -out /data/tls.crt -days 3650 -subj /CN=soju.test 2>/dev/null
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
# A second user for the integration tests, so they don't land in your
# session: the bouncer sends every client of a user the same messages.
if [ ! -f /data/.seeded-tester ]; then
    ctl() { sojuctl -config /etc/soju/config "$@" || echo "setup step failed (fine if it already exists): $*" >&2; }
    ctl user create -username tester -password tester
    ctl user run tester network create -addr irc+insecure://troutnet:6667 -name TroutNet
    ctl user run tester network create -addr irc+insecure://devnet:6667 -name DevNet
    ctl user run tester channel create '#trout/TroutNet'
    ctl user run tester channel create '#dev/DevNet'
    touch /data/.seeded-tester
fi
# Two more people on TroutNet for the live Control test (auto-op, protect),
# logging in as tester2/TroutNet and tester3/TroutNet.
if [ ! -f /data/.seeded-others ]; then
    ctl() { sojuctl -config /etc/soju/config "$@" || echo "setup step failed (fine if it already exists): $*" >&2; }
    for u in tester2 tester3; do
        ctl user create -username "$u" -password "$u"
        ctl user run "$u" network create -addr irc+insecure://troutnet:6667 -name TroutNet
    done
    touch /data/.seeded-others
fi
wait "$pid"
