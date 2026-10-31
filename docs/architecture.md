# Architecture

## Always through a bouncer

trout only ever talks to soju. The client has one code path: connecting directly is soju
running alongside it (the "None" bouncer, not built yet), and a remote bouncer is the same
protocol at a different address. soju is AGPL, so trout runs it as a separate process
rather than linking it.

Other bouncers (ZNC and so on) work as plain IRC servers. trout uses IRCv3 capabilities
when the server offers them and falls back without them; there is no per-bouncer adapter
layer. Only soju gets network management from inside the client.

## Connections

```
trout ──control──▶ soju        (soju.im/bouncer-networks: BOUNCER LISTNETWORKS + notify)
trout ──per net──▶ soju ──▶ IRC network   (BOUNCER BIND <netid> before CAP END)
```

- TCP, with soju's address syntax: `ircs://host[:port]` (TLS, the default, port 6697) or
  `irc+insecure://host:port` (plain text, for localhost, like trout's own soju). TCP is the one transport that
  works the same on macOS, Linux and Windows; Unix sockets don't reach out of Docker
  Desktop's VM on macOS, and soju doesn't target Windows.
- TLS checks the system's authorities first. A certificate none of them signed (a
  self-signed soju) gets an SSH-style prompt showing its key fingerprint
  (`SHA256:` + base64 of the public key's SHA-256, so renewing with the same key keeps
  the pin). Trusting it saves `pin` on the bouncer; a different key later is refused.
- A failed SASL login or an untrusted certificate stops the retry loop and asks instead.
- A bouncer can vanish without closing the connection (its machine gone): writes still go
  into the OS's buffer for minutes and nothing comes back. So after anything trout sends, a
  `PING` with a token follows, and the connection is dropped if its `PONG` doesn't come
  within 15 seconds (or 20 after a `PING` for 60 quiet seconds); only that `PONG` counts,
  not lines the bouncer sent before it froze (`irc.Conn`). Then the usual reconnect loop.
  With echo-message your messages only show when echoed, so the ones never echoed are
  listed as not sent instead of vanishing.
- Capabilities requested per network: `server-time`, `message-tags`, `batch`,
  `echo-message`, `multi-prefix`, `draft/chathistory`, `draft/read-marker`,
  `soju.im/bouncer-networks`.
- With `echo-message`, own messages are shown when the server echoes them, not locally.
- Each connection runs in its own goroutine and sends every message to Bubble Tea, so
  all state changes happen in `Update`. A dropped connection retries every 5 seconds.

## History and read markers

`internal/ui/read.go`. soju keeps the history and, with `draft/read-marker`, how far you've
read each conversation in any client.

- When a channel or query window opens, trout asks for `CHATHISTORY LATEST <target> * 50`
  once. History merges into the window in time order, skipping lines it already has
  (`Window.Merge`), so it can arrive before or after live lines.
- On (re)connecting to a network, every open window asks for `CHATHISTORY AFTER` its
  newest message (`Window.Latest`), so a dropped connection leaves no gap.
- Also on connecting, `CHATHISTORY TARGETS` lists the last week's conversations; for each
  private one without a window trout asks for its read marker (`MARKREAD <nick>`), then
  its history: an unread private message opens the window, while unread notices go to
  the status window (soju's history counts services and XDCC bots that only send
  notices as conversations; mIRC never gave notices a window). That's how messages
  from while trout was closed show up.
- Reading: a window is read when it's active and the terminal has focus (Bubble Tea focus
  reporting; assumed focused if the terminal never says). trout sends `MARKREAD` when you
  leave a window and, while you stay, at most every 2 seconds.
- soju's `MARKREAD` (on JOIN, when asked, and when another client reads) sets
  `Window.ReadAt`. For windows you're not looking at, what's been read moves behind the
  line marker and stops counting as activity, so reading on your phone clears the treebar
  here. The line marker itself stays mIRC's: where you were when you left a window.

## Config

`config.toml` in `os.UserConfigDir()/trout` lists `[[bouncer]]` entries (name, address,
user, optional `password-command` and `pin`), names the `active` one, and holds
`[settings]` from the View menu and Options, saved as they change. trout rewrites the
file, so unknown keys are an error rather than silently dropped. Passwords go in
`credentials` next to it, by bouncer name; trout writes it 0600 and, like SSH, won't read
it if others can. Flags and `TROUT_*` variables override the config for one run.

With no bouncer configured, trout opens the Connect dialog; Tools > Bouncers adds, edits
and switches between saved ones. OK saves the bouncer (a new one is named after its host)
and its password, makes it active, and reconnects from scratch. A bouncer with a
`password-command` and no saved password runs the command with the UI suspended
(`tea.ExecProcess`), so it can prompt.

## soju is plumbing

The IRC side of the UI acts on networks, and trout asks soju to do it:

| In trout | Sent to soju |
|---|---|
| File > Connect / Disconnect, status popup | `BouncerServ network update <name> -enabled true/false` |
| File > Select Server: Add / Edit / Delete | `network create` / `network update` / `network delete`; SASL with `sasl set-plain -network` |
| `/server host` | focuses the network soju has on that host, or creates one (never repoints the current one, unlike mIRC) |
| Channel popup > Detach | `channel update <#chan> -detached true`; soju parts it on our side, `/join` reattaches |

On soju's own status window, Connect and Disconnect are the bouncer connection itself.
Bouncers live under Tools > Bouncers (config.toml's list: Use / Add / Edit / Remove).

trout never guesses at the outcome: soju reports network changes through
`soju.im/bouncer-networks` notifications (`state` connected / connecting / disconnected,
`error`), which drive the treebar, titles and status lines. soju doesn't part channels on
the bound connection when a network goes down, so trout marks them parted itself; they
come back with JOINs. There's no "enabled" attribute, so a network you disabled and one
soju is retrying both just show as disconnected.

BouncerServ commands trout sends itself (`serv` in `internal/ui/soju.go`) are sent as
shell-quoted PRIVMSGs; their echo-message copy is dropped (it can hold a password) and
the one-line reply goes to the right status window instead of a BouncerServ query, or a
message box for errors. Any `network update` makes soju reconnect, and `sasl set-plain`
doesn't, so a new network with a SASL login is created disabled, gets its login, then is
enabled.

## Everyday details

- **Away** (`/away`, Commands > Set Away) goes to every connection, so soju and other
  clients know. soju never passes a client's away message on: once all clients on a
  network are away it sends its own `AWAY *` (or "Auto away"), and servers with
  `draft/pre-away` such as ergo don't show `*` at all. So after soju's RPL_(UN)AWAY, trout
  also sends the real message to each network with BouncerServ `network quote`, and again
  whenever soju reconnects to a network. This means trout's away wins even while your
  phone is online. `away-notify` and a `WHO` on joining (kept out of the status
  window) grey out away nicks in the nicklist.
- **WHOIS** replies are formatted like mIRC's and shown in the active window if it's on
  that network, else its status window.
- **The bell** rings for highlights and private messages when the window isn't active or
  the terminal isn't focused, at most every 2 seconds. It's a BEL written to stdout:
  Bubble Tea writes each frame in one write, so it can't land inside an escape sequence.
- **Favorites** are per network in `config.toml` (`[favorites]`).
- **Notices** from people show in the active window (or that network's status window),
  never a window of their own, like mIRC; server notices go to the status window.
- **The switchbar**, like mIRC's, shortens labels when crowded (down to 8 characters) and
  then wraps onto more rows, so every window keeps a button. Each row keeps 2 columns free
  for a hovered button's ×, so hovering never pushes a button off.
- **Long press** (holding the left button) opens the popup on nicks and on treebar and
  switchbar entries, for terminals that keep right-clicks.
- **The Channels List** (Alt+L, `/list`) shows the network's last downloaded list with its
  age; Get List asks again. A rate-limited LIST (RPL_TRYAGAIN, which soju passes on) or
  no answer in 60 seconds ends the wait and keeps the cached list. `/list` with filters
  goes to the server and its replies to the status window.
- **Highlight rules** (Address Book > Highlight, or Options > IRC > Highlight words) match
  words (wildcards allowed) as whole words, in messages or nicks, in some channels or
  everywhere, with a color and the bell; mentions of your nick come first. Old comma
  "highlight words" in config.toml become rules on load.
- **The Address Book** (Alt+B; Alt+N opens Notify) has mIRC's tabs: Address, Notify,
  Ignore, Control, Nick Colors and Highlight (`abook.go`; matching in `rules.go`). Address,
  Control, Nick Colors and Highlight entries are global with an optional network; notify
  and ignore lists stay per network. Notify entries take a note, and a "+" sends WHOIS when
  they come on. Control auto-ops and voices 2 seconds after a join (if you're still an op
  and they still need it) and protects: deopping a protected nick re-ops them and deops the
  deopper, banning one unbans.
- **Ignores** are typed and timed, like mIRC: `/ignore -pcntikd -u60 mask` (private,
  channel, notice, ctcp, invite, codes, dcc); with no type, everything. An open query still
  shows the person's private messages.
- **Perform** (Options > Connect) is a network's commands on connect. soju runs them as the
  network's connect-command, so they run even when trout isn't open; trout keeps a copy in
  config.toml for the dialog.
- **The Channels List** is also kept on disk (`cache/` next to `config.toml`), like mIRC's
  list file.
- **Ctrl+F** searches the active window's lines (not soju's whole history).
- **CTCP**: VERSION, PING, TIME and CLIENTINFO are answered (at most one reply every 2
  seconds); replies to yours show in the active window.
- **Ignore** and **notify** lists live per network in `config.toml`. Ignoring hides
  messages, notices, CTCPs, DCC offers and invites (live and from history), not joins.
  Notify uses MONITOR through soju (730/731).
- **DCC Get**: soju passes DCC SEND offers through untouched, and trout connects straight
  to the sender, which needs no open port but shows the sender this computer's address.
  A one-time privacy notice explains that ("don't show again" + Decline means ignore all).
  Then Options > DCC decides: ask (default; offers from nicks you messaged in the last 5
  minutes are fetched without asking, for XDCC), get without asking, or ignore. Offers
  pointing at loopback or private addresses always ask, with a warning. File names lose
  their folders; existing files are resumed (DCC RESUME/ACCEPT) or not overwritten;
  offers older than 2 minutes (replays) are skipped; a re-offer of a file that failed this
  session resumes without asking. Like mIRC, each download gets a Get window (a
  `KTransfer` window: progress bar, speed, time left; closing it cancels), and the
  toolbar's Received opens the download folder. Passive DCC and DCC CHAT aren't done.
- **Send File** uploads to soju's file host (`soju.im/FILEHOST`, HTTP Basic auth with the
  bouncer login, the pinned key trusted as for IRC) and sends the link.

## Protocol coverage

Checked against RFC 1459/2812 and IRCv3 (October 2026). Through soju, so some things are
soju's job (flood control: `soju.im/SAFERATE`; upstream SASL, reconnects, nick clashes).

- **Messages:** PRIVMSG, NOTICE (private ones in the active window, like mIRC), CTCP
  (ACTION, VERSION, PING, TIME, CLIENTINFO, DCC SEND/RESUME/ACCEPT). Text is split to fit
  the 512-byte line, set aside for the relayed `:nick!user@host` prefix, at UTF-8 and word
  boundaries. A pasted block of lines is sent line by line after asking, like mIRC.
- **Channels:** JOIN, PART, KICK, TOPIC (332/333), MODE (324, prefix and list modes,
  CHANMODES), NAMES (353/366), LIST (321-323, RPL_TRYAGAIN), INVITE, WHO (352/315, for away).
- **Users:** NICK, QUIT, AWAY (305/306/301, `away-notify`), WHOIS/WHOWAS (311-319, 330, 338,
  378, 671, 276, 307, 335, 369), WALLOPS, `chghost`, notify via MONITOR (730/731) or ISON
  polling (303) where there's no MONITOR (Rizon).
- **Server:** registration, CAP 302, SASL PLAIN, PING, ERROR, ISUPPORT (PREFIX, CHANMODES,
  CASEMAPPING for nick and channel matching, MONITOR, soju.im/FILEHOST), MOTD (375-376,
  422), other numerics as text (errors in the active window, like mIRC).
- **IRCv3:** `server-time`, `message-tags`, `batch`, `echo-message`, `multi-prefix`,
  `away-notify`, `chghost`, `draft/chathistory`, `draft/read-marker`,
  `soju.im/bouncer-networks(-notify)`, standard replies (FAIL/WARN/NOTE shown).
- **Not done, on purpose (mIRC didn't show them):** typing indicators, reactions and
  replies (`+draft/typing`, `+draft/react`, `+draft/reply`), `account-notify` and
  `extended-join` accounts, `setname`, message redaction.
- **soju extensions:** `soju.im/search` (Ctrl+F's "Search the bouncer's history": a results
  list, and Go To fetches `CHATHISTORY AROUND` the result and highlights it), and
  `soju.im/client-cert`: over TLS trout presents its own certificate (`client.pem` next to
  `config.toml`, made once), registers it with `CLIENTCERT CREATE` after a password login,
  and from then on logs in with SASL EXTERNAL (bouncer `login = "certificate"`). A refused
  certificate falls back to the saved password. Needs soju's `client-cert-auth`.
- **DCC CHAT:** accepted like DCC Get (trout connects out), in `=nick` windows; Options >
  DCC has mIRC's ask / accept / ignore. Starting a chat and passive DCC need a listening
  port here, so trout doesn't.
- **Not done yet:** `labeled-response`.

## From mIRC's help file

trout was checked against mIRC's help (https://www.mirc.com/help/html/, October 2026);
`tmp/plan.md` has the tiers. Besides the basics above:

- **Channel Central** (the channel popup's Channel Modes): topic, +t +n +i +m +p +s, key,
  limit; ban, exception and invite lists (and quiets where the network has them) fetched on
  demand with Add / Remove / Next List and kept current from MODE changes; the topic
  history of the session. Read-only for non-operators, like mIRC's greyed-out dialog.
- **Commands:** /privmsg, /say, /describe, /omsg, /onotice, /qmsg, /qme, /amsg, /ame,
  /hop (keeps the window), /partall, /ctcpreply, /disconnect, /dcc send (Send File),
  /channel, /findtext, /savebuf, /url, /abook, /markasread, /join -i; /topic and /kick
  take a channel; mIRC's -switches on /query, /msg and the like aren't sent along.
- **Keys:** Ctrl+PgUp/PgDn a line, Ctrl+Home/End the scrollback's ends, Ctrl+E
  strikethrough, Alt+B/N Address Book, Alt+K Colors, Alt+U URLs List. A #channel in text is
  a hotlink (double-click joins). Colors 16-98 are mIRC's fixed extended palette.
- **Messages options:** strip codes, prefix own messages, keep windows open on part, CTCPs
  in the active window, back from away when you talk, invisible mode, Favorites pop up on
  connect (with an Add button). The Channels List filters by text and minimum users. An
  existing file asks Resume / Overwrite / Rename. Status titles show an online timer.

- **A window's own options** (its switchbar or treebar popup; mIRC has them in each
  window's system menu): Timestamp, Beeping on every message, Events (joins, parts, quits,
  nick and mode changes shown, sent to the status window once, or hidden), Nicklist on the
  left, Track URLs, and the Second Editbox. They're saved in config.toml by network and
  window name (`winopts.go`).
- **Editboxes** (`editwin.go`, `state/editbox.go`): Alt+Enter makes the editbox multi-line
  (up to 5 rows, then it scrolls; Up/Down move between lines, Ctrl+Up/Down is the history;
  Enter sends each line as if typed alone). Alt+Q opens a second editbox under it. Shift
  with the arrows, Home and End, or a drag, selects; Ctrl+B/K/U/R/E put codes around the
  selection, Ctrl+C copies it (and never quits, like mIRC: File > Exit or /exit does), Ctrl+X
  cuts, Ctrl+V pastes the system clipboard. Tab completes `*bob*`-style wildcards.
- **Message pane:** nicks in a channel's text are hotlinks (double-click for a query,
  right-click or hold for the nick popup). Ctrl+drag copies whole lines with their codes,
  Shift+drag keeps line breaks as shown (many terminals keep Shift+drag for their own
  selection).
- **Window > Order** moves a network's windows up and down the switchbar.
- **Proxy** (Options > Connect, `proxy.go` and `internal/proxy`): SOCKS4/4a, SOCKS5 (with
  a login) or HTTP CONNECT, for the bouncer and its file host, for DCC Gets and Chats, or
  both, with masks of hosts to reach directly. The password is in the credentials file.

- **The Option key on a Mac** (`optionkey.go`, `internal/termkeys`): trout's Alt shortcuts
  need the terminal's Option key to send Alt, as WeeChat's and Emacs's do. At startup trout
  finds the terminal and reads its setting; if Option types characters, it asks once (with
  "Don't remind me again"; Options > Other brings it back). iTerm2: a dynamic profile,
  "trout (<your profile>)", the same but with Left Option as Esc+, which trout switches the
  tab to (OSC 1337 SetProfile) while it runs and back when it quits; your profiles aren't
  touched. kitty and Ghostty: a line added to their config. Terminal.app, Alacritty, VS
  Code, and iTerm2 in tmux or over SSH: the steps. Ctrl+J is a new line without Alt.

## Options

Like mIRC's Options dialog, with what fits a terminal and a bouncer:

- **Connect:** nickname and real name for every network (BouncerServ `user update`; soju
  sends NICK or SETNAME, or reconnects, but doesn't notify clients, so trout updates what
  it shows itself). A network's own nick, username and real name are in Select Server >
  Edit (`network update`). soju handles nick clashes itself, so there's no Alternative.
- **IRC:** mode prefix, timestamps and their format in mIRC's notation (`[HH:nn]`),
  highlight words, the bell, notices in the active window, rejoin when kicked, join when
  invited, WHOIS when someone opens a query.
- **Messages:** show addresses on joins and parts, nicks on join, notify in the active
  window, private messages in their own windows, one (Messages) window, or the active one.
- **Display:** bars, line marker and shading, nick colors, scheme, Colors (with the
  switchbar's activity colors), switchbar sort and fill, highlight the active button.
- **Bouncer:** Tools > Bouncers.
- **DCC:** the download folder, get / chat ask-accept-ignore, auto-get what you asked for,
  close Get windows when done, the privacy notice, trusted users (taken without asking),
  folders by file type, file types to ignore (mIRC's default list) or the only ones to
  take, and the time-out for connecting and for data (`dccopts.go`).
- **Other:** confirm pasting several lines, confirm exit, command prefix, Tab completion
  suffix, window buffer size.

The toolbar's Connect is a toggle, like mIRC's: Disconnect while the active network (or,
from the bouncer's window, the bouncer) is connected.

## Commands

Known slash commands map to IRC (`/join`, `/part`, `/msg`, `/me`, `/topic`, `/mode`,
`/kick`, `/op` …). Like mIRC, anything else goes to the server as it is, so `/whois`,
`/list` and `/msg BouncerServ help` work. `/quote` sends a raw line.

## Setting soju up on your server

Tools > Set Up Bouncer (and Set One Up on the first-run Connect dialog) installs soju on a
Linux server you can SSH into (`internal/install`, the wizard in `internal/ui/install.go`):

- **SSH is the system `ssh`** (Windows 10+ has OpenSSH too), run with BatchMode, so
  `~/.ssh/config`, agents, ProxyJump, known_hosts and hardware keys work as in a terminal.
  When ssh needs you (a new host key, a locked key), trout hands it the terminal once
  (`tea.ExecProcess`). `ssh -G` gives the real host name for the bouncer's address.
- **Probe:** Linux on x86_64 or ARM64 with systemd, or trout says it can't.
- **Root, in tiers:** logged in as root; `sudo -n`; sudo with your password (asked in a
  masked field, kept in memory, sent to `sudo -k -S`, through a pty if sudoers wants a
  tty; sudo-rs's messages are recognized too); or, when sudo says no, the command to run
  as root yourself, with View Script. trout never trusts that it ran: Check looks.
- **What's uploaded** to `~/.trout-install` (0700): trout's pinned soju and sojuctl
  (`install.SojuVersion`, pure Go, found next to trout or where `dev/build-soju` builds
  them), a TLS key and self-signed certificate made locally (so the pin is known before
  soju starts, and no domain is needed), and `setup.sh` with the settings on top.
- **`setup.sh`** (safe to run again): a `soju` system user, `/usr/local/bin/soju`,
  `/etc/soju` (root:soju), `/var/lib/soju`, a hardened `soju.service` after soju's
  contrib unit, ircs on 6697 and https (file uploads) on 8443, those ports opened in ufw or
  firewalld when active, and your account (`-admin=true`). `update` swaps in trout's soju
  and keeps the key and accounts; `remove` keeps the history, `purge` doesn't.
- **Check** logs in from here with the pin and the password. If that fails it looks on
  the server for what's missing; if the server's fine, it's the provider's firewall.
- **Then** the bouncer is saved (its `ssh` host marks it as trout's, for Update and
  Uninstall in Tools > Bouncers), and trout moves the old bouncer's networks over.

Moving networks happens here and when you switch bouncers (Bouncers > Use, This Computer),
which asks first. Once the new bouncer's network list is complete (the end of its
`soju.im/bouncer-networks` batch), trout has it `network create` each network it doesn't
already have, and `channel create` its joined channels. soju never hands out network
passwords, so trout says to set those again.

## A new server for it

The setup wizard's first question is where: your own server, or a new one at Hetzner Cloud,
DigitalOcean or Google Cloud (`internal/cloud`, the wizard steps in `ui/vps.go`). Google
goes through the `gcloud` command (its login and project, no API token): the free tier's
e2-micro in us-west1, us-central1 or us-east1, a reserved IPv4 (about $3.65/month, released
with the server), a `trout-bouncer` firewall rule for 6697 and 8443, and your key for user
`trout` (Google's images don't let root in; its agent gives that user sudo). For a provider, trout
asks for an API token once (kept in the credentials file as `(hetzner)` or
`(digitalocean)`; DigitalOcean's prompt shows trout's referral link, labeled), lists its
regions with the cheapest plan that can be ordered there and its monthly price with the
public IPv4, and Create Server makes a Debian 13 server with your SSH key (the agent's
first, or `~/.ssh/id_*.pub`; uploaded to the provider once). Then it's the install above,
as root over ssh (`accept-new` for the new host key, retrying while it boots). If anything
fails, or you stop, with a server made, the wizard offers to delete it so it isn't left
billing. The bouncer is saved with `cloud` and `server-id`, and Bouncers > Remove offers
Delete Server.

## Phones

There's no trout for phones: Goguma (App Store, F-Droid, Google Play) speaks soju's
extensions and its push notifications (`soju.im/webpush`, on in soju by default). Tools >
Connect a Phone (`ui/phone.go`) shows a QR code of `ircs://user:password@host:port` for the
bouncer in use, which the phone's camera hands to Goguma. Goguma doesn't know trout's pin:
the first time it shows the certificate's SHA-1 and asks, so the dialog shows that SHA-1
(`irc.CertSHA1`, fetched through the pin) to compare before Accept Always.

## This Computer: trout's own soju

With no bouncer anywhere, This Computer (on the first-run Connect dialog, or Bouncers >
Add) has trout run soju itself (`internal/local`): a separate process (soju is AGPL),
started before connecting and stopped when trout quits or switches bouncers, so it's
connected to your networks only while trout is open. It listens on `127.0.0.1` in plain
text on a port picked each start, with an admin account whose random password lives in
the credentials file (`sojuctl` sets it each start, so the two can't drift); its database,
config and log are in `soju/` next to config.toml. A soju already serving that folder (a
trout that crashed, or a second one open) is used as it is, and left running. The config
entry has `local = true` and no address. Its config names the admin socket and database relative to
that folder (soju runs there, and so does `sojuctl`): a Windows path in a listen URL loses
its backslashes. Tested on Windows 11 (`dev/windows/run-tests`). The About box links soju's source.

## Scripts

mIRC scripting is its own package, `internal/script`, kept apart from the client: no I/O
and no imports of `ui`, `irc` or `state`. Events go in (built from IRC lines by
`EventFromMessage`, or from the client: INPUT, OPEN, EXIT...), actions come out (commands
to run as if typed, echoes, files to write, variables to keep), and it asks the client
read-only questions through a `Host` (nicklists, the address list, the time). A script
never sends IRC itself: its `/msg` goes through the same path as a typed one.

`ui/scripts.go` is the only place they meet: one wrapper around the IRC handler (`^`
handlers before trout shows a line, the rest after), typed lines (on INPUT, aliases,
`//`), timers (the engine says when it next needs waking), and the commands that load and
unload scripts. The JOIN, PRIVMSG and other handlers don't know scripts exist.
`ui/scripteditor.go` is the Scripts Editor and `ui/popups.go` the right-click menus,
which all come from popups.ini. Each handler has a step budget, so a script that never
ends can't freeze trout. What users can do is in [scripting.md](scripting.md).

## Later

- **Clipboard over SSH** via OSC 52 (copying uses pbcopy/xclip/clip locally).
