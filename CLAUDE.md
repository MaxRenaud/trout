# trout

An mIRC-inspired IRC client for the terminal, in Go with Bubble Tea. It connects
through a [soju](https://soju.im) bouncer. A pet project: there's no hosted service
or business side.

## Commands

```sh
docker compose -f dev/compose.yaml up -d --build   # local IRC world (see docs/dev-environment.md)
go run ./cmd/trout                                 # the bouncer from config.toml, or the Connect dialog
dev/trout                                          # the dev soju, with its own config in tmp/dev-config
go run ./cmd/trout --demo                          # fake world, no network: for UI work
TROUT_DEBUG=1 dev/trout                            # log raw IRC lines to trout-debug.log

go test ./...                                      # unit and frame tests, no network
go test -tags integration ./internal/ui -v         # needs the dev env
dev/build-soju                                     # the soju trout installs on servers
go run github.com/goreleaser/goreleaser/v2@v2.19.0 release --snapshot --clean   # release archives in dist/, nothing published
go test -tags proxmox ./internal/install -v -parallel 3   # bouncer setup on real VMs
go test -tags docker ./internal/install -v         # the same on this computer's arch (arm64)
go test -tags cloud ./internal/ui -run TestCloudBouncer -v   # real Hetzner and DigitalOcean servers (cents)
go vet ./... && go fix -diff ./...                 # must both be clean
```

## Layout

- `cmd/trout`: flags and env (`TROUT_CONFIG`, `TROUT_ADDR`, `TROUT_USER`, `TROUT_PASSWORD`,
  `TROUT_DEBUG`), which override the config; picks the bouncer and runs `password-command`.
- `internal/config`: `config.toml` (bouncers, the active one, settings) and the 0600
  `credentials` file next to it, in `os.UserConfigDir()/trout`.
- `internal/state`: the data model (networks, windows, lines, nicklists, editbox). No I/O.
- `internal/irc`: one IRC connection: TLS with key pinning (`tls.go`), CAP, SASL PLAIN,
  `BOUNCER BIND`, read loop, sending.
- `internal/ui`: everything on screen, plus `irc.go`, which turns IRC events into state and
  slash commands into IRC. `app.go` has the App, keys and mouse. `connect.go` has the
  Connect dialog, certificate trust, bouncer switching and settings saving;
  `bouncerlist.go` is Tools > Bouncers; `soju.go` manages soju's networks through
  BouncerServ (Connect/Disconnect, the Servers dialog, `/server`, Detach); `read.go` catches
  up on history and syncs read markers with soju; `find.go` is Ctrl+F; `away.go` has away,
  WHO, WHOIS formatting and the bell; `prompts.go` has the Commands menu prompts, URLs List
  and Save to File; `chancentral.go` is Channel Central (modes, ban/exception/invite
  lists, topics); `cmds.go` more of mIRC's commands (/describe, /hop, /dcc...); `lists.go`
  the per-network favorites, ignore and notify lists; `abook.go` the Address Book and
  `rules.go` what its entries do (highlight, control, nick colors, typed ignores, notify
  notes, perform); `ctcp.go`, `dcc.go` (DCC Get and Chat), `dccopts.go` (its options) and
  `upload.go` (Send File through the bouncer's file host); `winopts.go` a window's own
  options and Window > Order; `editwin.go` the multi-line and second editboxes;
  `proxy.go` Options > Connect > Proxy; `form.go` is the text-field dialog they and Kick
  (why) use.
- `internal/proxy`: dialing through SOCKS4, SOCKS5 or an HTTP proxy.
- `internal/install`: setting soju up on your server over the system `ssh`: probe, the
  root tiers, upload, the embedded `setup.sh`, and Check. The wizard is `ui/install.go`.
- `internal/cloud`: a server for the bouncer at Hetzner or DigitalOcean (API tokens) or
  Google Cloud (the gcloud command: free e2-micro, reserved IPv4, `trout-bouncer` firewall rule):
  regions with the cheapest plan and price, create with your SSH key, delete. `ui/vps.go`
  puts it in the setup wizard, then hands the server to the same install, as root.
- `internal/local`: trout's own soju for the This Computer bouncer, run while trout is
  open on 127.0.0.1 (a random port each time), in `soju/` next to config.toml;
  `ui/localbnc.go` starts it before connecting and `ui.Run` stops it.
- `internal/termkeys`: which terminal this is, whether its Option key sends Alt, and the
  fix (an iTerm2 dynamic profile, a kitty/Ghostty config line, or steps); `optionkey.go`
  asks at startup.
- `internal/demo`: the fake world for `--demo`; the dev bot reuses its chatter.
- `dev/`: Docker setup (two ergo networks, soju, a chatter bot); `build-soju`; `proxmox/`,
  throwaway test VMs for bouncer setup (credentials and their SSH key in `tmp/`);
  `systemd/`, a Debian image with systemd and sshd for the arm64 install test.
- `tmp/`: git-ignored scratch space until v1.0. The roadmap is `tmp/plan.md`; keep it updated.

## Rules

- Write modern Go: use the modern-go-guidelines skill when changing Go code, and keep
  `go fix -diff ./...` empty.
- All state changes happen in `App.Update` on the Bubble Tea goroutine. Connection
  goroutines only send messages (`ircLineMsg` etc.) through `a.send`.
- The UI only renders `state`; keep IRC parsing out of drawing code.
- Draw into the `Canvas`, and register a `Region` for anything clickable. Later regions win
  hit tests, so overlays register last.
- trout is an mIRC replica that works in a terminal, not a modern TUI. Stay faithful to
  mIRC, flaws and clutter included, unless it can't work in a terminal (like minimize and
  maximize buttons) or is really bad; then say why in a comment and ask before diverging.
- Every mIRC-faithful behavior should match mIRC's help file; say so in comments when it's
  deliberate (e.g. "like mIRC").
- Don't use "mIRC" in names or branding; "mIRC-inspired" is fine.
- On screen, say "bouncer", not "soju": soju is how it's done, the bouncer is the idea.
  Name soju only where the product matters (the first-run Connect dialog, the setup
  wizard). Code, docs and comments say soju.
- The demo keeps its mock-ups (`setupWizard`, `bouncerBox`) behind `a.opts.Demo`; the real
  ones are `installWizard` and `savedBouncersBox`. New demo-only features stay behind it
  until they're real.

## Tests

- `frame_test.go` renders demo screens and checks every line fills the terminal exactly;
  `go test -v` prints them as plain text. Add a frame there when adding a screen.
- `irc_test.go` feeds raw IRC lines through `handleIRC`; add cases there for new events.
  `daily_test.go` does the same for WHOIS, away, the bell, Find, favorites and key changes;
  `menus_test.go` for CTCP, ignore, notify, Channel Modes, DCC offers and downloads;
  `tier2_test.go` for the Address Book rules, perform and the Tier 2 options;
  `tier3_test.go` for window options, editboxes, hotlinks, Order, DCC options and proxy.
- `connect_test.go` walks first run, failed logins, certificate trust, settings saving and
  Tools > Bouncers against a temp config, and soju network management against a scripted
  soju (`fakeSoju`); `TestCatchUp` covers history gaps, missed private messages and read
  markers. `live_test.go` does the same against the dev soju as the `tester` user (not
  `trout`, so it stays out of your session), including two clients sharing a read marker;
  `control_live_test.go` has `tester2` and `tester3` join as raw IRC to test auto-op and
  protect, and logs in through a small SOCKS5 proxy.
- Anything trout tells soju goes through BouncerServ with `serv`; check soju's syntax with
  `sojuctl ... user run trout help <command>` (see the dev-env skill) rather than guessing.
- `internal/irc/conn_test.go` scripts a fake server for registration; `tls_test.go` pins
  a self-signed one.
- `ui/install_test.go` walks the setup wizard against a fake server (`installHooks`):
  sudo with and without a password, by hand, ssh needing you, uninstalling, and moving
  networks. `install/install_test.go` checks the installer's commands against a fake
  runner. With `-tags proxmox`, `install/proxmox_test.go` installs on all six Proxmox
  templates through every root tier, and `ui/proxmox_test.go` drives the wizard for real.
  Only touch VMs in the trout-test pool (IDs 9900-9999). With `-tags docker`,
  `install/docker_test.go` installs in a privileged systemd container (`dev/systemd`), which
  is arm64 on this Mac. `TestMoveNetworksLive` moves tester's networks to a throwaway soju
  user it deletes afterwards. With `-tags cloud` (tokens in `tmp/cloud.env`),
  `cloud/live_test.go` lists regions and makes and deletes a server per provider, and
  `ui/cloud_test.go` runs the whole wizard on one, then deletes it from Bouncers. Check
  nothing's left at any provider afterwards (Google: instances, addresses, disks, the
  `trout-bouncer` firewall rule), and only touch what trout made. `TROUT_CLOUD=gcloud`
  runs one provider.
- `local/local_test.go` and `ui/localbnc_test.go` run the real soju for this computer
  (skipped until `dev/build-soju` has built it), in a short folder under /tmp: Unix
  socket paths can't be long.
- Tests never touch the real config: demo mode and `Options{}` without `Config` save nothing.

## Not built yet

Publishing releases (GitHub
releases and the Homebrew tap wait until trout is public; `.goreleaser.yaml` has release
disabled). Lock was skipped. See `docs/architecture.md` and `tmp/plan.md`.
