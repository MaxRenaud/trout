// Package demo is trout's fake world for `trout --demo`: two networks with
// history, and random traffic. The dev IRC bot reuses its chatter.
package demo

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/MaxRenaud/trout/internal/state"
)

// Me is the user's nick in demo mode.
const Me = "max"

func network(name, server, umodes string) *state.Network {
	n := state.NewNetwork(name)
	n.Server, n.Port, n.Nick, n.Umodes = server, 6697, Me, umodes
	return n
}

func channel(n *state.Network, name, topic string, nicks []state.Nick) *state.Window {
	w := n.Add(&state.Window{Kind: state.KChannel, Name: name, Modes: "nt", Topic: topic, Nicks: nicks, Joined: true})
	w.SortNicks()
	return w
}

func nicklist(ops, voiced []string, rest ...string) []state.Nick {
	var out []state.Nick
	for _, s := range ops {
		out = append(out, state.Nick{Prefix: "@", Name: s})
	}
	for _, s := range voiced {
		out = append(out, state.Nick{Prefix: "+", Name: s})
	}
	for _, s := range rest {
		out = append(out, state.Nick{Name: s})
	}
	return out
}

func L(role state.Role, text string) state.Line { return state.Line{Role: role, Text: text} }

// backfill spreads lines evenly over the last ago, oldest first.
func backfill(w *state.Window, ago time.Duration, lines ...state.Line) {
	step := ago / time.Duration(len(lines)+1)
	t := time.Now().Add(-ago)
	for _, l := range lines {
		t = t.Add(step)
		l.Time = t
		w.Lines = append(w.Lines, l)
	}
}

// Seed returns the demo networks with some history in every window.
func Seed() []*state.Network {
	const (
		normal = state.RoleNormal
		own    = state.RoleOwn
		event  = state.RoleEvent
		info   = state.RoleInfo
		notice = state.RoleNotice
	)
	lib := network("Libera.Chat", "irc.libera.chat", "Ziw")
	backfill(lib.Status(), 55*time.Minute,
		L(info, "* Connecting to irc.libera.chat (6697)"),
		L(notice, "-irc.libera.chat- *** Checking Ident"),
		L(notice, "-irc.libera.chat- *** Looking up your hostname..."),
		L(notice, "-irc.libera.chat- *** Found your hostname: host-203-0-113-42.example.net"),
		L(normal, "Welcome to the Libera.Chat Internet Relay Chat Network max"),
		L(normal, "Your host is irc.libera.chat, running version solanum-1.0-dev"),
		L(normal, "There are 52 users and 31337 invisible on 27 servers"),
		L(normal, "- irc.libera.chat Message of the Day -"),
		L(normal, "- \x0304█\x0307█\x0308█\x0309█\x0311█\x0312█\x0306█\x0f \x02Welcome to Libera Chat\x02"),
		L(normal, "- The IRC network for free and open-source software"),
		L(normal, "- and peer directed projects."),
		L(normal, "End of /MOTD command."),
		L(event, "* max sets mode: +Ziw"),
		L(notice, "-NickServ- You are now identified for \x02max\x02."),
		L(info, "* \x02This is demo mode.\x02 Nothing connects anywhere; all traffic is fake."),
		L(info, "* Keys: F1 all shortcuts, F2 theme, Ctrl+N/Ctrl+Q next channel/query, Ctrl+L line marker, Ctrl+C quit"),
		L(info, "* Mouse: right-click, Ctrl+click or press and hold a nick; double-click nicks and URLs; drag the gray splitters"),
		L(info, "* Try the Options, Favorites and Channels toolbar buttons, /join #something, /me, Tab, Ctrl+K colors"),
	)

	goNuts := channel(lib, "#go-nuts", "Go programming language | https://go.dev | Ask your question directly",
		nicklist([]string{"ChanServ", "gopher", "deferclose"}, []string{"alice", "segfault"},
			"max", "bob", "nullptr", "goroutine", "tux", "rwmutex", "chanrecv", "zerovalue", "iota_", "gcpause",
			"racecond", "byteorder", "hexdump", "bitflip", "coredump", "sigsegv", "lurker42", "afk_bob", "coffee_",
			"grepme", "judy", "ivan", "heidi", "grace", "frank", "erin", "dave", "olivia", "rupert", "sybil",
			"trent", "victor", "walter", "zoe", "niaj", "peggy", "mallory"))
	backfill(goNuts, 42*time.Minute,
		L(event, "* Now talking in #go-nuts"),
		L(event, "* Topic is 'Go programming language | https://go.dev | Ask your question directly'"),
		L(event, "* Set by gopher on Mon Oct 26 14:02:11"),
		L(normal, "<alice> anyone using range-over-func iterators in prod yet?"),
		L(normal, "<segfault> yep. stack traces get a little weird but it's fine"),
		L(normal, "<nullptr> the iter package is lovely once it clicks"),
		L(event, "* Joins: bob (~bob@user/bob)"),
		L(normal, "<bob> morning all"),
		L(normal, "<gopher> morning bob"),
		L(state.RoleAction, "* tux waves"),
		L(normal, "<goroutine> is it just me or did the GC pacer changes make p99 way better?"),
		L(normal, "<deferclose> not just you, we saw about 15% on our API boxes"),
		L(event, "* Parts: lambda (~lambda@203.0.113.88) (bye!)"),
		L(state.RoleHighlight, "<alice> max: you were asking about bubbletea yesterday, did you pick it?"),
		L(own, "<max> yeah, going with it for a little IRC client experiment"),
		L(normal, "<alice> nice, post a screenshot when it's done"),
		L(state.RoleQuit, "* Quits: nilpointer (~np@198.51.100.4) (Ping timeout: 240 seconds)"),
		L(event, "* gopher sets mode: +v segfault"),
		L(event, "* mutex is now known as rwmutex"),
		L(normal, "<rwmutex> much better"),
	)

	linux := channel(lib, "#linux", "Welcome to #linux | No distro wars | Use a pastebin for anything over 3 lines",
		nicklist([]string{"ChanServ", "tux", "kernelpanic"}, []string{"vimfan"},
			"max", "dotfiles", "zshrc", "tmuxer", "grepme", "coffee_", "sedawk", "emacsuser", "rustacean",
			"packetloss", "netsplit", "pingtimeout", "bitflip", "frank", "grace", "heidi"))
	backfill(linux, 35*time.Minute,
		L(event, "* Now talking in #linux"),
		L(event, "* Topic is 'Welcome to #linux | No distro wars | Use a pastebin for anything over 3 lines'"),
		L(event, "* Set by tux on Sat Oct 24 09:13:55"),
		L(normal, "<kernelpanic> who broke my initramfs again"),
		L(normal, "<vimfan> did you update the kernel and forget to regenerate it?"),
		L(normal, "<kernelpanic> ...maybe"),
		L(normal, "<dotfiles> \x0300,04 PSA \x0f new dotfiles dropped: \x1fhttps://example.org/dotfiles\x1f"),
		L(state.RoleAction, "* zshrc slaps tmuxer around a bit with a large trout"),
		L(normal, "<tmuxer> rude"),
		L(normal, "<grepme> anyone running sway on nvidia these days?"),
		L(normal, "<coffee_> works with the open kernel modules, mostly"),
	)

	alice := lib.Add(&state.Window{Kind: state.KQuery, Name: "alice", Host: "~alice@user/alice"})
	backfill(alice, 20*time.Minute,
		L(normal, "<alice> hey, got a sec?"),
		L(normal, "<alice> you said something about an mIRC-looking TUI?"),
		L(own, "<max> yeah, just mocking up the UI for now"),
		L(normal, "<alice> \x02nostalgia intensifies\x02"),
	)

	oftc := network("OFTC", "irc.oftc.net", "i")
	backfill(oftc.Status(), 50*time.Minute,
		L(info, "* Connecting to irc.oftc.net (6697)"),
		L(notice, "-irc.oftc.net- *** Looking up your hostname..."),
		L(notice, "-irc.oftc.net- *** Found your hostname"),
		L(normal, "Welcome to the OFTC Internet Relay Chat Network max"),
		L(normal, "- irc.oftc.net Message of the Day -"),
		L(normal, "- \x02OFTC\x02: the Open and Free Technology Community"),
		L(normal, "End of /MOTD command."),
		L(event, "* max sets mode: +i"),
	)

	debian := channel(oftc, "#debian", "Debian GNU/Linux | Stable: trixie | https://www.debian.org | Don't ask to ask",
		nicklist([]string{"dpkg", "aptbot"}, []string{"carol"},
			"max", "peggy", "walter", "trent", "sybil", "olivia", "zoe", "judy", "sedawk"))
	backfill(debian, 30*time.Minute,
		L(event, "* Now talking in #debian"),
		L(event, "* Topic is 'Debian GNU/Linux | Stable: trixie | https://www.debian.org | Don't ask to ask'"),
		L(event, "* Set by dpkg on Thu Oct 22 18:40:02"),
		L(normal, "<carol> full-upgrade went smoothly here"),
		L(normal, "<aptbot> Tip: \"apt policy <pkg>\" shows where a version comes from"),
		L(normal, "<peggy> anyone tried the installer on arm64 boards?"),
		L(normal, "<walter> works on mine, needed a device tree tweak"),
	)

	carol := oftc.Add(&state.Window{Kind: state.KQuery, Name: "carol", Host: "~carol@203.0.113.21"})
	backfill(carol, 15*time.Minute,
		L(normal, "<carol> are you coming to the bug squashing party on saturday?"),
		L(own, "<max> probably! what time?"),
		L(normal, "<carol> 10am, bring coffee"),
	)

	return []*state.Network{lib, oftc}
}

// Chatter, Actions and QueryChatter are what fake people say; $me is the
// user's nick and $nick another person in the channel.
var (
	Chatter = []string{
		"has anyone benchmarked this on arm64?",
		"lgtm, ship it",
		"that's a race condition waiting to happen",
		"git bisect saved my weekend",
		"works on my machine",
		"brb coffee",
		"the docs are wrong, I'll send a patch",
		"$me: what do you think?",
		"$me: ping",
		"anyone else seeing packet loss?",
		"heh",
		"lol",
		"tabs vs spaces, round 47",
		"I miss the sound of a 56k modem",
		"who remembers /dcc send?",
		"\x0303green\x03 was always my mIRC color",
		"\x02\x0304,08 HELLO \x0f from 1999",
		"rebasing 40 commits, pray for me",
		"it's always DNS",
	}
	Actions      = []string{"stretches", "goes to get coffee", "is away: lunch", "hugs $nick", "slaps $nick around a bit with a large trout"}
	QueryChatter = []string{"you there?", "lol", "check this out: https://example.org", "ok cool", "brb", "did you see what happened in the channel?"}
	QuitReasons  = []string{"Ping timeout: 240 seconds", "Quit: Leaving", "Remote host closed the connection", "Quit: brb", "*.net *.split"}
	guestNames   = []string{"Guest", "lurker", "newbie", "nomad", "pixel"}
)

// Favorites are the demo Favorites folder, per network.
var Favorites = map[string][]string{
	"Libera.Chat": {"#go-nuts", "#linux", "#irc-tui", "#mirc-nostalgia"},
	"OFTC":        {"#debian", "#oftc"},
}

type ChanInfo struct {
	Name  string
	Users int
	Topic string
}

// Channels is what the demo Channels List "downloads"; the user counts and
// topics are made up.
var Channels = map[string][]ChanInfo{
	"Libera.Chat": {
		{"#libera", 2100, "Libera Chat network support"},
		{"#python", 1700, "Python help | Don't ask to ask, just ask"},
		{"#archlinux", 1500, "Arch Linux support"},
		{"#linux", 1350, "Welcome to #linux | No distro wars"},
		{"#go-nuts", 900, "Go programming language"},
		{"#rust", 800, "The Rust programming language"},
		{"#vim", 700, "Vim help and discussion"},
		{"#emacs", 650, "GNU Emacs"},
		{"#git", 600, "Git help"},
		{"#postgresql", 550, "PostgreSQL"},
		{"#nixos", 500, "NixOS"},
		{"#networking", 450, "Computer networking"},
		{"#security", 400, "Information security"},
		{"#music", 120, "What are you listening to?"},
		{"#irc-tui", 42, "Terminal IRC clients"},
		{"#mirc-nostalgia", 31, "Remember /dcc send?"},
	},
	"OFTC": {
		{"#debian", 1200, "Debian GNU/Linux"},
		{"#tor", 600, "The Tor Project"},
		{"#kernelnewbies", 450, "Linux kernel newbies"},
		{"#oftc", 300, "OFTC network support"},
		{"#qemu", 250, "QEMU"},
	},
}

func Pick[T any](xs []T) T { return xs[rand.IntN(len(xs))] }

func GuestNick() string { return fmt.Sprintf("%s%d", Pick(guestNames), rand.IntN(1000)) }

func fakeHost() string { return fmt.Sprintf("198.51.100.%d", 1+rand.IntN(254)) }

// Event is one fake thing happening: a line to add to a window.
type Event struct {
	Win  *state.Window
	Role state.Role
	Text string
}

// Traffic makes up one random event somewhere in nets, updating nicklists
// for joins and parts. ok is false when nothing happened.
func Traffic(nets []*state.Network) (ev Event, ok bool) {
	var chans, queries []*state.Window
	for _, n := range nets {
		for _, w := range n.Wins {
			switch w.Kind {
			case state.KChannel:
				chans = append(chans, w)
			case state.KQuery:
				queries = append(queries, w)
			}
		}
	}
	if len(chans) == 0 {
		return Event{}, false
	}
	ch := Pick(chans)
	me := ch.Net.Nick
	var others []string
	for _, n := range ch.Nicks {
		if n.Name != me && n.Name != "ChanServ" {
			others = append(others, n.Name)
		}
	}
	if len(others) == 0 {
		return Event{}, false
	}

	switch r := rand.IntN(100); {
	case r < 10 && len(queries) > 0:
		q := Pick(queries)
		return Event{q, state.RoleNormal, "<" + q.Name + "> " + Pick(QueryChatter)}, true
	case r < 20:
		nick := GuestNick()
		ch.AddNick(state.Nick{Name: nick})
		return Event{ch, state.RoleEvent, fmt.Sprintf("* Joins: %s (~%s@%s)", nick, strings.ToLower(nick), fakeHost())}, true
	case r < 28:
		var plain []string
		for _, n := range ch.Nicks {
			if n.Prefix == "" && n.Name != me {
				plain = append(plain, n.Name)
			}
		}
		if len(plain) == 0 {
			return Event{}, false
		}
		nick := Pick(plain)
		ch.RemoveNick(nick)
		mask := fmt.Sprintf("%s (~%s@%s)", nick, strings.ToLower(nick), fakeHost())
		if rand.IntN(2) == 0 {
			return Event{ch, state.RoleEvent, "* Parts: " + mask}, true
		}
		return Event{ch, state.RoleQuit, "* Quits: " + mask + " (" + Pick(QuitReasons) + ")"}, true
	case r < 35:
		act := strings.ReplaceAll(Pick(Actions), "$nick", Pick(others))
		return Event{ch, state.RoleAction, "* " + Pick(others) + " " + act}, true
	}
	text := strings.ReplaceAll(Pick(Chatter), "$me", me)
	role := state.RoleNormal
	if strings.Contains(strings.ToLower(text), strings.ToLower(me)) {
		role = state.RoleHighlight
	}
	return Event{ch, role, "<" + Pick(others) + "> " + text}, true
}
