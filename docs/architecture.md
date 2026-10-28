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

- Plain-text TCP on localhost. It's the one transport that works the same on macOS, Linux
  and Windows; Unix sockets don't reach out of Docker Desktop's VM on macOS, and soju
  doesn't target Windows.
- Capabilities requested per network: `server-time`, `message-tags`, `batch`,
  `echo-message`, `multi-prefix`, `draft/chathistory`, `soju.im/bouncer-networks`.
- With `echo-message`, own messages are shown when the server echoes them, not locally.
- When a channel or query window opens, trout asks for `CHATHISTORY LATEST <target> * 50`
  once and puts it in front of what the window shows, skipping lines it already has.
- Each connection runs in its own goroutine and sends every message to Bubble Tea, so
  all state changes happen in `Update`. A dropped connection retries every 5 seconds.

## Commands

Known slash commands map to IRC (`/join`, `/part`, `/msg`, `/me`, `/topic`, `/mode`,
`/kick`, `/op` …). Like mIRC, anything else goes to the server as it is, so `/whois`,
`/list` and `/msg BouncerServ help` work. `/quote` sends a raw line.

## Later

- **"None" mode:** trout starts its own soju on a random localhost port.
- **Bouncer setup:** install soju over SSH (hosts from `~/.ssh/config`) or on a new VPS
  (Hetzner, DigitalOcean), pinning a TLS key trout generates so no domain is needed, and
  move networks over with soju's network commands. The demo shows the planned UI.
- **TLS** for remote bouncers.
- **Clipboard over SSH** via OSC 52 (copying uses pbcopy/xclip/clip locally).
