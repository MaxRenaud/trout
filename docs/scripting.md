# Scripting

trout runs mIRC scripts: aliases, popups, remote events, variables, timers, hash tables,
files and the Users list. It's meant for the scripts that automate things (auto-op,
greetings, `!seen`, trivia, kick counters) and for the popups and aliases people keep
around. Big all-in-one scripts such as NNScript or Peace & Protection don't run: most of
what they are is dialogs and Windows DLLs.

What trout supports was checked against mIRC 7.86 and its help file. When a script uses
something trout doesn't do, it says so (see [What doesn't run](#what-doesnt-run)) rather
than half-running it.

## Where scripts live

In a `scripts` folder next to `config.toml`:

| | |
|---|---|
| macOS | `~/Library/Application Support/trout/scripts` |
| Linux | `~/.config/trout/scripts` |
| Windows | `%AppData%\trout\scripts` |

It holds mIRC's own files, in mIRC's format, so you can copy yours over:

| File | What it is |
|---|---|
| `aliases.ini` | the Aliases tab |
| `popups.ini` | the right-click menus and Commands (see [Popups](#popups)) |
| `remote.ini` | the Remote tab's own script |
| `users.ini` | the Users list |
| `vars.ini` | the variables scripts keep between sessions |

Other scripts (`.mrc` files) go there too, and anything a script reads or writes
(`/write`, `$read`, `/hsave`, `/writeini`): names are relative to it, and `$mircdir` is
its full path. Like mIRC, a full path reaches any file (`$read(/home/me/notes.txt)`), and
so does a leading `..`; unlike mIRC, which has no `~`, so does `~/` for your home folder
(`$read(~/notes.txt)`): a script can read and write what you can, so load only scripts you
trust. A `..` after a folder's name (`logs\#x\..\..`) isn't allowed (see
[Where trout differs](#where-trout-differs-from-mirc)).

`/timer`, `/scon` and `/scid` evaluate their command again when it runs, so text from
someone else in it, like `$1-` of their message, would run as script code. Pass it through
`$unsafe($1-)`, as in mIRC: `.timer 1 5 msg $chan $unsafe($1-)`.

`config.toml` remembers what's loaded, in order (`scripts`), and the switches below
(`scripts-off`, `script-level`, `script-options`).

## The Scripts Editor

Tools > Scripts Editor, the toolbar's Scripts button, Alt+R (Remote) or Alt+P (Popups).
Like mIRC's, it has five tabs:

- **Aliases**: one alias a line, `/name commands` (`/j /join #$$1`), in `aliases.ini`
  and in files loaded with `/load -a`.
- **Popups**: the five menus; View picks Status, Channel, Query, Nickname List or Menu
  Bar.
- **Remote**: scripts with `on` events, `ctcp` and `raw` sections, `alias` and `menu`
  sections; `remote.ini` and the scripts you load.
- **Users**: the Users list, one `levels:address` a line (`5,friend:*!*@host.example`).
- **Variables**: one `%name value` a line.

OK saves what changed and loads it again. A script loaded in the editor that has `on LOAD`
or `on START` asks whether to run them once the editor has closed. Cancel forgets the
changes (asking first, with Options > Ask to Save Files). The ✓ button checks the
brackets and lists what trout can't run in the file, by line. In the text, Enter is a new
line and Tab indents, so Shift+Tab goes to the buttons (mIRC's Ctrl+Tab, which terminals
don't send): there Tab and the arrows move along them, and Enter presses one.

The menus are mIRC's:

- **File**: New, Load (Ctrl+L), Unload, Save (Ctrl+S), Save As, Save All, Rename, Delete,
  Order (the order scripts load in, which is the order their events run in), Save & Exit.
- **Edit**: Undo (Ctrl+Z), Redo (Ctrl+Y), Cut, Copy, Paste, Select All (Ctrl+A), Check
  Brackets (Ctrl+H), Go To Line (Ctrl+G), More Files, Find (Ctrl+F), Find Next (F3).
- **View**: the tab's files. **Index**: jump to an alias, event, menu or group.
- **Listen**: CTCPs, Events, Raw (below).
- **Options**: Initialization Warning, Ask to Save Files, Monitor File Changes (a file
  changed by another editor is read again when the terminal gets the focus back), Backup
  File on Save (`remote.ini.bak` beside `remote.ini`), Find Selection, Line Numbers, Identifier Warning, Default
  Level.
- **Help**: short pages on aliases, popups, remote, variables and `if`; the commands and
  identifiers trout has; and Search, which says what trout does with any command or
  `$identifier`.

Ctrl+PgUp and Ctrl+PgDn move between tabs. mIRC's Font, Show on Desktop and Always on Top
aren't there: the terminal has those.

## Commands

| Command | |
|---|---|
| `/load -rs file` | loads a script (`-rs` or nothing) |
| `/load -a file` | loads a file of aliases |
| `/alias [file] name [commands]` | adds or replaces a one-line alias in an aliases file (`aliases.ini`, or `file` if it's loaded), or removes it with no commands |
| `/load -ps` `-pc` `-pq` `-pn` `-pm file` | loads popup items for status, channel, query, nicklist or the menubar |
| `/load -rv file`, `/load -ru file` | adds mIRC's variables or Users file to trout's |
| `/unload -rs file`, `/reload -rs file` | unloads, or loads again without `on LOAD`/`on START` |
| `/remote on\|off` | all scripts on or off |
| `/ctcps on\|off`, `/events on\|off`, `/raw on\|off` | ctcp events, named events, raw events (Listen) |

A file name is in the scripts folder unless it's a full path. Typed in the editbox, `//`
evaluates a line (`//echo -a $me`), and aliases come before trout's own commands, like
mIRC.

## Popups

Every right-click menu and the menubar's Commands menu come from `popups.ini`, like
mIRC's:

| Popups tab, View | popups.ini | Where |
|---|---|---|
| Status | `[mpopup]` | right-clicking a status window's text |
| Channel | `[cpopup]` | right-clicking a channel's text |
| Query | `[qpopup]` | right-clicking a query's text (`$1` is who it's with) |
| Nickname List | `[lpopup]` | right-clicking a nick, in the nicklist or the text (`$1` is the nick) |
| Menu Bar | `[bpopup]` | the Commands menu; its first line is the menu's name |

Until you save one, trout uses its own: mIRC's defaults, without DCC Chat (trout doesn't
start chats), mIRC's own channels, Edit Notes and Quit IRC, and with Send File. Change a
menu or empty it and the real one changes, as in mIRC. Scripts' `menu` sections add their
items after these, and popups work with `/remote off`.

Each line is an item, `Label:commands`. A `.` in front puts it in the submenu above it
(`..` one level further), and `-` on its own is a separator:

```
Whois:/whois $$1
Control
.Op:/mode # +o $$1
.Kick (why):/kick # $$1 $$?="Reason:"
-
Slap!:/me slaps $$1 around a bit with a large trout
```

`$?="question"` asks for something in an input box before the item runs; with `$$?`,
leaving it empty (or Cancel) stops the command. `$?*` hides what you type (a password)
and `$?!` asks Yes or No (`$true` or `$false`). `$!` is the last answer. In events,
timers and sockets they ask when the script gets there and wait for you, like mIRC's;
other scripts' events wait their turn meanwhile. Only `^` events, `on INPUT` and a
CTCP's handlers can't wait (trout needs their answer at once), so `$?` there is an
error.

## What runs

- **The language**: `alias`, `on`, `ctcp`, `raw` and `menu` sections, `{ }` blocks, `|`,
  comments, `#groups` (`/enable`, `/disable`); `if`/`elseif`/`else` with mIRC's
  operators, `while`, `break`, `continue`, `goto`, `return`, `halt`, `:error` and
  `$error`; `[ ]` evaluation brackets and `$+`; `%variables`, global or `/var` local, with
  `-uN`; `$1-`, `$$1`, `#$1`, `$v1`/`$v2`, `$result`.
- **Events**: `TEXT`, `ACTION`, `NOTICE`, `CTCP` and `CTCPREPLY`, `JOIN`, `PART`,
  `QUIT`, `NICK`, `KICK`, `TOPIC`, `INVITE`, `RAWMODE` and the mode events (`OP`,
  `DEOP`, `VOICE`, `BAN`, `UNBAN`, `MODE`...), `USERMODE`, `SNOTICE`, `WALLOPS`,
  `ERROR`, `PING`, `PONG`, `raw` numerics, `CONNECT`, `DISCONNECT`, `CONNECTFAIL`,
  `INPUT`, `OPEN`, `CLOSE`, `ACTIVE` (with `$lactive`), `APPACTIVE` (the terminal
  gaining or losing focus, `$appactive`), `FILERCVD` and `GETFAIL` (DCC Get),
  `FILESENT` and `SENDFAIL` (Send File, trout's DCC send), `CHAT` (a DCC chat message),
  `NOTIFY`, `UNOTIFY`, `LOAD`, `START`,
  `UNLOAD`, `EXIT`, `SIGNAL`, and the socket events `SOCKOPEN`, `SOCKREAD`, `SOCKWRITE` and `SOCKCLOSE`. `^` handlers run before trout shows a line and can hide it (`/haltdef`); a
  CTCP handler's `/halt` stops trout's reply.
- **Levels**: the Users list, with general, `=` exact and named levels, `+`, `*`, the
  `me`, `!`, `@` and `&` prefixes, and `/auser`, `/guser`, `/ruser`, `/iuser`,
  `/rlevel`, `/flush`, `/ulist`, `/dlevel`.
- **Colors and files**: `$color` (the Colors dialog's kinds of text, from your scheme),
  `$rgb`, `$findfile` (in the scripts folder, with a command or a `@window`),
  `$findfilen`, `$isdir`, and `$mircini` (a `mirc.ini` there, which trout doesn't read).
- **DCC and more**: `$get`, `$send` and `$chat` (with `-1` for the event's), `$filename`,
  `$notify` (the notify list), `$sfile` (an input box for a file name: a terminal has no
  file dialog) and `$sfstate`, `$readn`, `$longip`, `$isalias`, `$alias`, `$finddir`,
  `$mklogfn`, `$fullname`, `$mircexe`; `$insong`/`$inwave` are `$false` and `$anick`
  and `$emailaddr` empty, since trout has no sounds, alternate nick or email.
- **The connection**: `$network` is the network's own name (the server's 005
  `NETWORK=`, as in mIRC), `$host` and `$ip` your host and address as the network sees
  them (asked with `USERHOST` when you connect, like mIRC's Local Info: with a bouncer
  they're the bouncer's; with a cloak, `$ip` is empty), and `$serverip` the server's
  (with a bouncer, the bouncer's). `on CONNECT` waits for `$host` and `$ip`. `$version` is the mIRC version trout's scripting
  follows (7.86), for scripts' checks; `$troutversion` is trout's.
- **Uptime**: `$uptime(mirc)` is trout's, `$uptime(server)` the network connection's, and
  `$uptime(system)` (and `$ticks`) the computer's, in milliseconds or, with N, as
  `$duration` does it.
- **Identifiers**: about 200, among them the text, token, number, time and `$calc`
  families, `$nick`, `$chan`, `$address`, `$ial`, `$comchan`, `$chan(N)`, `$nick(#,N)`,
  `$hget`, `$hfind`, `$read`, `$readini`, `$ini`, `$regex`, `$regsubex`, `$timer`,
  hashes and encodings. Help > Identifiers in the editor lists them all.
- **Timers** (`/timer`, `-m`, `-e`, `-p`, `-r`, time of day), **hash tables** (kept in
  memory until `/hsave`, like mIRC's), **files** (`/write`, INI files, file handles,
  `/copy`, `/rename`, `/remove`, `/mkdir`; `/write` makes no folders, like mIRC's).
- **Several networks**: each network is a connection (`$cid`, `/scid`, `/scon`).
- **Sockets**: connections out, plain or SSL (`/sockopen`, `-e`, and `-a` to accept a
  certificate that doesn't check out), `/sockwrite`, `/sockread` into a `%variable` or a
  `&binvar`, `/sockclose`, `/sockmark`, `/sockrename`, `$sock()`, `$sockname`, `$sockerr`,
  `$sockbr`, and `$iptype`. A web API or a whois server is a few lines, like mIRC's.
- **Binary variables** (`&name`, for one run of a script, like mIRC's): `/bset`,
  `/bunset`, `/bcopy`, `/breplace`, `/bread`, `/bwrite`, `/btrunc`, `$bvar` (with `.text`, `.word`,
  `.nword`, `.long`, `.nlong`), `$bfind`, and `&binvars` in `$md5`, `$sha1`, `$crc` and
  the rest (N = 1), `$encode`/`$decode` (`b`), and the socket commands.
- **Web requests**: `$urlget(url,options,target,alias,headers,body)`, like mIRC's: GET,
  HEAD, POST, PUT, PATCH or DELETE into a `&binvar` or a file, with `&binvar` headers
  and body, `i` (any certificate), `k` (no redirects), `e` (keep a 404's reply), `r`
  (resume a file) and `c` (cancel); the alias gets the id, and `$urlget(id)`'s
  properties (`state`, `reply`, `size`, `redirect`...) last until it returns. trout makes
  the request, so `.reply`'s headers are sorted, not in the server's order.
- **Custom windows**: `@windows` of text or a listbox (`/window -l`), with a side
  listbox (`-lN`), an editbox (`-e`) and its default command, sorted (`-s`), opened
  minimized (`-n`), hidden (`-h`) or without a button (`-w0`, `-k0` for one without its
  `@`); `/aline`, `/iline`, `/rline`, `/dline`, `/cline`, `/sline` (with `-s`, `-a`,
  `-n`, `-h`, `-l`), `/echo @name`, `/clear`, `/renwin`, `/loadbuf`, `/savebuf` and
  `/filter`; `$window`, `$line`, `$fline`, `$sline` and `$filtered`. A `menu @name`
  section is its popup, and its `sclick`, `dclick`, `lbclick` and `rclick` items run on
  clicks (`$1` is the line), Up, Down and Enter in a listbox; `on INPUT:@name:` and
  `on CLOSE:@:` work too. Checked against mIRC 7.86 where its help is silent.

A script's commands run as if you typed them: `/msg`, `/mode` and the rest go through
trout's own commands, and anything trout doesn't know goes to the server.

## What doesn't run

- **Not loaded at all**: scripts with dialogs, DLLs, COM objects, listening or UDP
  sockets (`/socklisten`, `/sockudp`), or `/run`. `/load` says why, and nothing of the
  script runs.
- **Sockets'** `/sockpause`, `/socklist` and STARTTLS (`-t`) give an error when
  reached, as does `/bread -t`.
- **Loaded, with an error when reached**: picture windows (`/window -p`, `/drawdot` and
  the other drawing commands), popup files given to `/window`, `/filter -a`, and the mIRC
  identifiers trout doesn't have yet. `/load` and the ✓ button list them.
- **Ignored**: sounds (`/splay`); `/beep` is the terminal's bell.
- **`$?` in `^` events, `on INPUT` and CTCP handlers** is an error: they can't wait.
- **Regular expressions** are Go's (RE2): no lookarounds or backreferences. Using one is
  an error, not a wrong match.
- **On Windows, Microsoft Defender deletes** a script that has both `on *:START` and
  `/bwrite` as `Worm:IRC/Generic` (found 2026-11-04; it does the same to mIRC's). `/load`
  then can't find the file. Keep `/bwrite` in another file than your `on START`, or tell
  Defender to leave trout's scripts folder alone. trout says so once, the first time you
  `/load` on Windows, and warns when you load or save a script that has both. Defender
  shows a notification when it deletes one; Protection history can restore it.

## Where trout differs from mIRC

- A handler that runs too long (100,000 steps) is stopped with an error, where mIRC
  freezes.
- `/alias` writes its aliases file straight away; mIRC keeps the change until `/saveini`
  or it closes.
- A line, a variable's value or what an identifier returns can be up to 64 KB, where
  mIRC's limit is about 10,000 characters; past it, the script stops with mIRC's "Line
  too long" error, so a loop doubling a variable can't take all the memory.
- A variable's value or a Users list entry is saved up to its first line break. mIRC
  saves the rest as lines of their own, so text from someone else with a line break in
  it could add a variable or a user at the next start.
- `$calc` with brackets or signs nested more than 1,000 deep is an error, where mIRC
  freezes.
- A binary variable holds up to 64 MB (`/bset` and `/bcopy` past that are errors), so a
  position from someone's text can't make trout allocate gigabytes.
- Hash table items come out in the order they were added; mIRC's order is its hash's.
- Typed in the editbox, an identifier that doesn't exist stays as typed (as in mIRC); in a
  script it's `$null`, or with Options > Identifier Warning an error that stops the
  script, with mIRC's "No such identifier" message.
- A file name with a `..` after a folder's name, like `logs\#x\..\..\foo.log`, isn't a
  file (`/write` and the rest give an error). A channel's name can have `\` and `..` in
  it, so `write logs\ $+ $chan $+ .log` could otherwise write anywhere. A leading `..`
  and full paths work as in mIRC.
- A file name starting with `~/` is in your home folder, as in a terminal; mIRC has no
  `~`, and would look for a folder named `~` in the scripts folder.
- Timers run outside any script, so they can't call another script's `alias -l`.
- `isop` counts owners and admins (`~`, `&`) as ops.
- Custom windows are always full size, like trout's other windows: their positions,
  sizes, fonts and icons are left out, and `$window().x`, `.y`, `.w` and `.h` are 0.
  `/savebuf` and `/filter` only read custom windows (the bouncer keeps the others' logs).
- A socket that fails says so in mIRC's terms (`$sockerr` 3, or 4 for a name that doesn't
  resolve, and Windows' error numbers in `$sock().wserr`), whatever the system.

## For developers

The engine is `internal/script`: events in, actions out, no I/O. `internal/ui/scripts.go`
hooks it into the client, `scripteditor.go` is the Scripts Editor and `popups.go` the
menus. Its tests are user journeys (`internal/script/testdata/journeys/*.txtar`: a script
and what should happen), cases from mIRC's help file (`spec_test.go`), and checks against
real mIRC on a Windows VM (`go test -tags mirc ./internal/script -run TestMIRC`, through
`dev/windows/mirc-run`).
