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
(`/write`, `$read`, `/hsave`, `/writeini`). A script can't reach outside the folder; you
can still `/load` a script from anywhere by its full path.

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
brackets and lists what trout can't run in the file, by line.

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
leaving it empty (or Cancel) stops the command. `$!` is the last answer. They work in
popup items and typed lines, and in the alias such a line calls.

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
  `INPUT`, `OPEN`, `CLOSE`, `NOTIFY`, `UNOTIFY`, `LOAD`, `START`, `UNLOAD`, `EXIT` and
  `SIGNAL`. `^` handlers run before trout shows a line and can hide it (`/haltdef`); a
  CTCP handler's `/halt` stops trout's reply.
- **Levels**: the Users list, with general, `=` exact and named levels, `+`, `*`, the
  `me`, `!`, `@` and `&` prefixes, and `/auser`, `/guser`, `/ruser`, `/iuser`,
  `/rlevel`, `/flush`, `/ulist`, `/dlevel`.
- **Identifiers**: about 200, among them the text, token, number, time and `$calc`
  families, `$nick`, `$chan`, `$address`, `$ial`, `$comchan`, `$chan(N)`, `$nick(#,N)`,
  `$hget`, `$hfind`, `$read`, `$readini`, `$ini`, `$regex`, `$regsubex`, `$timer`,
  hashes and encodings. Help > Identifiers in the editor lists them all.
- **Timers** (`/timer`, `-m`, `-e`, `-p`, `-r`, time of day), **hash tables** (kept in
  memory until `/hsave`, like mIRC's), **files** (`/write`, INI files, file handles,
  `/copy`, `/rename`, `/remove`).
- **Several networks**: each network is a connection (`$cid`, `/scid`, `/scon`).

A script's commands run as if you typed them: `/msg`, `/mode` and the rest go through
trout's own commands, and anything trout doesn't know goes to the server.

## What doesn't run

- **Not loaded at all**: scripts with dialogs, DLLs, COM objects, sockets or `/run`.
  `/load` says why, and nothing of the script runs.
- **Loaded, with an error when reached**: custom `@windows` (`/window`, `/aline`...) and
  the mIRC identifiers trout doesn't have yet. `/load` and the ✓ button list them.
- **Ignored**: sounds (`/splay`); `/beep` is the terminal's bell.
- **`$?` in events**: input boxes only work in popups and typed lines.
- **Regular expressions** are Go's (RE2): no lookarounds or backreferences. Using one is
  an error, not a wrong match.

## Where trout differs from mIRC

- A handler that runs too long (100,000 steps) is stopped with an error, where mIRC
  freezes.
- Hash table items come out in the order they were added; mIRC's order is its hash's.
- Typed in the editbox, an identifier that doesn't exist stays as typed (as in mIRC); in a
  script it's `$null`, or with Options > Identifier Warning an error that stops the
  script, with mIRC's "No such identifier" message.
- Timers run outside any script, so they can't call another script's `alias -l`.
- `isop` counts owners and admins (`~`, `&`) as ops.

## For developers

The engine is `internal/script`: events in, actions out, no I/O. `internal/ui/scripts.go`
hooks it into the client, `scripteditor.go` is the Scripts Editor and `popups.go` the
menus. Its tests are user journeys (`internal/script/testdata/journeys/*.txtar`: a script
and what should happen), cases from mIRC's help file (`spec_test.go`), and checks against
real mIRC on a Windows VM (`go test -tags mirc ./internal/script -run TestMIRC`, through
`dev/windows/mirc-run`).
