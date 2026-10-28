# trout

An mIRC-inspired IRC client for the terminal, in Go with Bubble Tea. It connects
through a [soju](https://soju.im) bouncer. A pet project: there's no hosted service
or business side.

## Commands

```sh
docker compose -f dev/compose.yaml up -d --build   # local IRC world (see docs/dev-environment.md)
go run ./cmd/trout                                 # connect to soju at 127.0.0.1:6667 as trout/trout
go run ./cmd/trout --demo                          # fake world, no network: for UI work
TROUT_DEBUG=1 go run ./cmd/trout                   # log raw IRC lines to trout-debug.log

go test ./...                                      # unit and frame tests, no network
go test -tags integration ./internal/ui -run TestAgainstSoju -v   # needs the dev env
go vet ./... && go fix -diff ./...                 # must both be clean
```

## Layout

- `cmd/trout`: flags and env (`TROUT_ADDR`, `TROUT_USER`, `TROUT_PASSWORD`, `TROUT_DEBUG`).
- `internal/state`: the data model (networks, windows, lines, nicklists, editbox). No I/O.
- `internal/irc`: one IRC connection: CAP, SASL PLAIN, `BOUNCER BIND`, read loop, sending.
- `internal/ui`: everything on screen, plus `irc.go`, which turns IRC events into state and
  slash commands into IRC. `app.go` has the App, keys and mouse.
- `internal/demo`: the fake world for `--demo`; the dev bot reuses its chatter.
- `dev/`: Docker setup (two ergo networks, soju, a chatter bot).
- `tmp/`: git-ignored scratch space until v1.0. The roadmap is `tmp/plan.md`; keep it updated.

## Rules

- Write modern Go: use the modern-go-guidelines skill when changing Go code, and keep
  `go fix -diff ./...` empty.
- All state changes happen in `App.Update` on the Bubble Tea goroutine. Connection
  goroutines only send messages (`ircLineMsg` etc.) through `a.send`.
- The UI only renders `state`; keep IRC parsing out of drawing code.
- Draw into the `Canvas`, and register a `Region` for anything clickable. Later regions win
  hit tests, so overlays register last.
- Every mIRC-faithful behavior should match mIRC's help file; say so in comments when it's
  deliberate (e.g. "like mIRC").
- Don't use "mIRC" in names or branding; "mIRC-inspired" is fine.
- Demo-only features (bouncer setup wizard, Bouncers manager) stay behind `a.opts.Demo`
  until they're real.

## Tests

- `frame_test.go` renders demo screens and checks every line fills the terminal exactly;
  `go test -v` prints them as plain text. Add a frame there when adding a screen.
- `irc_test.go` feeds raw IRC lines through `handleIRC`; add cases there for new events.
- `internal/irc/conn_test.go` scripts a fake server for registration.

## Not in the MVP yet

TLS, DCC, CTCP (other than ACTION), the embedded "None" mode (trout starting its own
soju), bouncer provisioning, saved favorites, and a prompt for "Kick (why)" reasons. See
`docs/architecture.md`.
