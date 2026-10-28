---
name: dev-env
description: Start, check, reset or debug trout's local IRC dev environment (two ergo networks, soju, chatter bot in Docker) and run trout or the integration test against it. Use when asked to run trout for real, test against IRC, or when soju/ergo/bot need inspecting.
---

# trout dev environment

Everything lives in `dev/compose.yaml`; details in `docs/dev-environment.md`.

1. Make sure Docker is running: `docker version --format '{{.Server.Version}}'`. If the
   daemon isn't up, ask the user to start Docker Desktop; don't start it yourself.
2. Start or update the stack:
   `docker compose -f dev/compose.yaml up -d --build`
3. Check it's healthy: `docker compose -f dev/compose.yaml ps`, then
   `docker compose -f dev/compose.yaml logs --tail 30 soju bot`. soju should show both
   upstreams connected; the bot logs only on errors.
4. Verify trout end to end without a terminal:
   `go test -tags integration ./internal/ui -run TestAgainstSoju -v`
5. trout itself is a full-screen TUI: the user runs `go run ./cmd/trout` in their own
   terminal. For protocol debugging, have them run it with `TROUT_DEBUG=1` and read
   `trout-debug.log`.

Reset to a fresh soju (user, networks and history recreated by `dev/soju/start.sh`):
`docker compose -f dev/compose.yaml down -v && docker compose -f dev/compose.yaml up -d --build`

Admin commands go through sojuctl inside the container:
`docker compose -f dev/compose.yaml exec soju sojuctl -config /etc/soju/config user run trout <command>`
(see `soju(1)` for BouncerServ commands such as `network status` and `channel create`).
