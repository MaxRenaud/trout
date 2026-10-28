# Dev environment

`dev/compose.yaml` runs a small IRC world on your machine:

| Service | What it is |
|---|---|
| `troutnet`, `devnet` | Two [ergo](https://ergo.chat) IRC servers: two networks. |
| `soju` | The bouncer, on `127.0.0.1:6667`. User `trout`, password `trout`, with both networks and a channel on each (`#trout`, `#dev`). |
| `bot` | Five personas per network (alice, bob, carol, dave, segfault) who chat, come and go, message you privately, and answer when you mention them. |

```sh
docker compose -f dev/compose.yaml up -d --build
go run ./cmd/trout
```

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

It connects without a terminal, waits for the networks and a joined channel, sends a
message and waits for the echo.
