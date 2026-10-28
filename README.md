# trout

An mIRC-inspired IRC client for the terminal. It looks and works like the mIRC you
remember, menus, switchbar, treebar, nicklist popups, Address Book and all, and connects
through a [soju](https://soju.im) bouncer, which keeps you online and keeps your history
while trout is closed.

```
 trout - [#go-nuts [42] [+nt]: Go programming language | https://go.dev | Ask your question directly]     ×
  File  View  Favorites  Tools  Commands  Window  Help
  Connect  Options │  Favorites  Channels │  Address Book  Notify │  Send File  Received │  Colors
 ▏Libera.Chat▕ ▏#go-nuts ×▕ ▏#linux▕ ▏alice▕ ▏OFTC▕ ▏#debian▕ ▏carol▕
 ▾ Libera.Chat        [06:33] * Now talking in #go-nuts                                   ▲  @ChanServ       ▲
  ├─ #go-nuts         [06:35] * Topic is 'Go programming language | https://go.dev | Ask  ▒  @deferclose
```

trout is a hobby project. There's no hosted service and nothing to sign up for.

## What you need

- A soju bouncer; trout is tested with soju 0.11 and uses its networks, history, read
  markers, search and file host. trout talks to IRC networks only through it.
- A terminal with mouse support and 24-bit color: iTerm2, Ghostty, kitty, WezTerm,
  Alacritty, Windows Terminal, GNOME Terminal, Konsole.
- Go 1.25 or later to build it.

## Install and run

```sh
go install github.com/MaxRenaud/trout/cmd/trout@latest
trout                 # the Connect dialog the first time, then your saved bouncer
trout --demo          # a fake world, no network: look around first
```

The first run asks for the bouncer's address (`ircs://host[:port]`), user and password.
The password goes in a `credentials` file (mode 0600) next to `config.toml`, in your
config folder (`~/Library/Application Support/trout` on macOS, `~/.config/trout` on
Linux, `%AppData%\trout` on Windows). Over TLS, after a password login, trout asks the
bouncer to remember this computer's certificate and logs in with that from then on.

Flags and environment variables override the config: `--addr` / `TROUT_ADDR`,
`--user` / `TROUT_USER`, `--password` / `TROUT_PASSWORD`, `--config` / `TROUT_CONFIG`,
`--bouncer` to pick a saved one. `password-command` in `config.toml` runs a command such as
`pass show soju` for the password.

## What's in it

- mIRC's layout: menubar, toolbar, switchbar, treebar, nicklist, the editbox, Windows 98
  dialogs, and mIRC's colors and formatting codes (Ctrl+B, Ctrl+K, Ctrl+U...).
- The bouncer's networks as mIRC's servers: Connect, Disconnect, Select Server and
  `/server` go through soju.
- History: windows fill in from the bouncer, read markers sync with your other clients,
  Ctrl+F can search the bouncer's whole history.
- Channel Central (modes, bans, exceptions, invites, topics), the Channels List,
  Favorites, the Address Book (notify, ignore, auto-op and protect, nick colors,
  highlights), Perform, CTCP, DCC Get and Chat, Send File through the bouncer's file
  host, URL catching, timestamps, the line marker, Options pages and the Colors dialog.
- The multi-line editbox (Alt+Enter), the second editbox (Alt+Q), per-window options in
  each button's popup, a proxy (SOCKS4, SOCKS5, HTTP) for the bouncer and DCC.

F1 lists the keys. Right-click, Ctrl+click or a long press opens popups.

## Terminal notes

**Mac: Alt is Option.** trout's Alt shortcuts need the Option key to send Alt, as in
WeeChat or Emacs. trout checks at startup and offers to fix it: in iTerm2 it uses a copy
of your profile with the left Option key as Alt while trout runs; in kitty and Ghostty it
adds one line to their config; elsewhere it shows the setting to change. Ctrl+J also
starts a new line in the editbox.

**tmux** works. For the mouse, focus (for the bell) and 24-bit color, put this in
`~/.tmux.conf` (the last line names your outer terminal's `TERM`):

```
set -g mouse on
set -s focus-events on
set -s escape-time 10
set -as terminal-features ',xterm-256color:RGB'
```

**Windows:** Windows Terminal should work, but trout hasn't been tried there yet.

## Developing

`docker compose -f dev/compose.yaml up -d --build` starts two IRC networks, soju and a
chatty bot; `dev/trout` connects to it. `go test ./...` runs the unit and screen tests;
`go test -tags integration ./internal/ui` runs against the dev servers. See
`docs/dev-environment.md` and `docs/architecture.md`.

## License

Not decided yet.
