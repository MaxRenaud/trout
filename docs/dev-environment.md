# Dev environment

`dev/compose.yaml` runs a small IRC world on your machine:

| Service | What it is |
|---|---|
| `troutnet`, `devnet` | Two [ergo](https://ergo.chat) IRC servers: two networks. |
| `soju` | The bouncer: plain text on `irc+insecure://127.0.0.1:6667`, and TLS with a self-signed certificate on `ircs://127.0.0.1:6697` (for trying key pinning), file uploads (Send File) on `http://127.0.0.1:8080`, and certificate logins (`client-cert-auth`). User `trout`, password `trout`, with both networks and a channel on each (`#trout`, `#dev`); the integration tests use a second user, `tester`/`tester`, set up the same way, so they stay out of your session (the bouncer sends every client of a user the same messages, DCC offers included). `tester2` and `tester3` (passwords the same) are two more people on TroutNet only, for the live Control test. |
| `bot` | Five personas per network (alice, bob, carol, dave, segfault) who chat, come and go, message you privately, answer when you mention them, and answer CTCP VERSION/PING/TIME. Plus `xdcc` on TroutNet: `/msg xdcc xdcc send #1` sends a 3 MB test file over DCC from `127.0.0.1:6800`-`6809`, a port per offer (resumable); `/msg xdcc chat` offers a DCC chat that echoes what you say. |

```sh
docker compose -f dev/compose.yaml up -d --build
dev/trout                     # plain text on 127.0.0.1:6667
dev/trout --bouncer dev-tls   # TLS with the self-signed cert: try the trust prompt
```

`dev/trout` runs `go run ./cmd/trout` with its own config in `tmp/dev-config`, made on
first run with both dev bouncers, so your real config is never touched and dev settings
stick. Other arguments go to trout. `rm -r tmp/dev-config` starts over, including the
pinned key. To see the first-run Connect dialog, point trout at a config that doesn't
exist yet: `TROUT_CONFIG=/tmp/scratch/config.toml go run ./cmd/trout`.

## Handy commands

```sh
docker compose -f dev/compose.yaml logs -f soju bot   # what's happening
docker compose -f dev/compose.yaml down -v            # stop and wipe soju's database
docker compose -f dev/compose.yaml exec soju \
    sojuctl -config /etc/soju/config user run trout network status
```

Inside trout, `/msg BouncerServ help` talks to soju directly; replies open in a query
window under the `soju` entry in the treebar.

## Testing against it

```sh
go test -tags integration ./internal/ui -run TestAgainstSoju -v
```

It connects without a terminal as `tester`, waits for the networks and a joined channel,
sends a message and waits for the echo. `-run TestSojuManagement` (or no `-run`) also disconnects
and reconnects DevNet, detaches and rejoins `#dev`, and adds and deletes a network;
`TestFilesAndCTCP` asks a bot for its VERSION, gets the xdcc test file (then resumes it),
and uploads a file, posting the link in a channel you're in on TroutNet. Over TLS, give it the dev certificate's key pin (the
Connect dialog shows it, or ask openssl):

```sh
pin="SHA256:$(openssl s_client -connect 127.0.0.1:6697 </dev/null 2>/dev/null | openssl x509 -pubkey -noout |
    openssl pkey -pubin -outform der | openssl dgst -sha256 -binary | base64 | tr -d =)"
TROUT_ADDR=ircs://127.0.0.1:6697 TROUT_PIN="$pin" go test -tags integration ./internal/ui -run TestAgainstSoju -v
```
