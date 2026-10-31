package script

import (
	"cmp"
	"maps"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ergochat/irc-go/ircmsg"
)

// TestJourneys runs each testdata/journeys/*.txtar: scripts, then a
// transcript of what happens and what the scripts should do. Every action
// a script takes must be in the transcript.
//
// Transcript lines:
//
//	me trout                  our nick
//	chan #c @trout bob        we're on #c, with this nicklist
//	active #c                 the active window
//	clock 2026-10-29T15:04:05Z
//	info server=irc.test connected PREFIX=(ov)@+   the connection, for $server, $prefix...
//	query bob                 an open query window
//	event OPEN bob bob :hi    an event only the client makes: kind, nick, target, text
//	< :bob!b@h PRIVMSG #c :hi a raw IRC line (\x01 for CTCP); our nicklists follow it
//	/j #help                  typed in the editbox (after on INPUT)
//	type hello there          text typed in the editbox: on INPUT, then > /say
//	* hidden                  a ^ handler halted the client's text for the line
//	* input stopped           on INPUT halted what was typed
//	@ +3h                     time passes
//	reload                    restart: load the scripts again with the saved variables
//	> /msg #c hi              a command the scripts run
//	= #c: text                an /echo or error, in a window ("status" for status)
//	* halted                  the event was /halted
//	? %var value              a variable's value now
//	menu nicklist bob         the scripts' items for a popup, with $1-; they're | lines:
//	| Modes                     a submenu, its items indented under it
//	|   Op [x] (disabled)       an item, checked and disabled
//	pick nicklist Modes/Op bob  run a popup item
//	! name.mrc: line 2: ...   a Problem loading a script (all of them must be listed)
//	# comment
func TestJourneys(t *testing.T) {
	paths, err := filepath.Glob("testdata/journeys/*.txtar")
	if err != nil || len(paths) == 0 {
		t.Fatal("no journeys", err)
	}
	for _, path := range paths {
		t.Run(strings.TrimSuffix(filepath.Base(path), ".txtar"), func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			runJourney(t, parseTxtar(string(data)))
		})
	}
}

type txtarFile struct{ name, data string }

// parseTxtar reads the txtar format: "-- name --" lines start files.
func parseTxtar(s string) []txtarFile {
	var files []txtarFile
	for line := range strings.Lines(s) {
		if name, ok := strings.CutPrefix(strings.TrimSpace(line), "-- "); ok && strings.HasSuffix(name, " --") {
			files = append(files, txtarFile{name: strings.TrimSuffix(name, " --")})
			continue
		}
		if len(files) > 0 {
			files[len(files)-1].data += line
		}
	}
	return files
}

type journey struct {
	t       *testing.T
	host    *fakeHost
	e       *Engine
	scripts []txtarFile
	saved   map[string]string
	// The saved Users list and default level, for reload.
	savedUsers  []string
	savedDLevel int
	out         []string // actions not yet matched by the transcript
	waiting     bool     // in wait, running timers
}

func runJourney(t *testing.T, files []txtarFile) {
	j := &journey{t: t, host: &fakeHost{me: "trout", net: "TestNet", chans: map[string][]Nick{},
		now: time.Date(2026, 10, 29, 15, 4, 5, 0, time.UTC)}}
	var transcript string
	for _, f := range files {
		switch {
		case f.name == "transcript":
			transcript = f.data
		case strings.HasSuffix(f.name, ".mrc"):
			j.scripts = append(j.scripts, f)
		default:
			// A data file, in the scripts folder.
			j.host.writeFile(&File{Name: f.name, Data: f.data})
		}
	}
	var wantProblems []string
	var lines []string
	for l := range strings.Lines(transcript) {
		l = strings.TrimSpace(l)
		switch {
		case l == "" || strings.HasPrefix(l, "# "):
		case strings.HasPrefix(l, "! "):
			wantProblems = append(wantProblems, l[2:])
		default:
			lines = append(lines, l)
		}
	}
	// The setup lines come before the scripts load, so on LOAD sees them.
	for len(lines) > 0 && j.setup(lines[0]) {
		lines = lines[1:]
	}
	if got := j.load(true); !slices.Equal(got, wantProblems) {
		t.Fatalf("problems:\n got %q\nwant %q", got, wantProblems)
	}
	for _, l := range lines {
		j.step(l)
	}
	j.flush()
}

// load makes a new engine and loads the scripts, running on LOAD (when
// first) and on START.
func (j *journey) load(first bool) []string {
	j.e = New(Options{Rand: rand.New(rand.NewPCG(1, 2))})
	j.e.Restore(j.saved)
	if j.savedDLevel > 0 || j.savedUsers != nil {
		j.e.RestoreUsers(j.savedUsers, j.savedDLevel)
	}
	var problems []string
	for _, f := range j.scripts {
		for _, p := range j.e.Load(f.name, f.data) {
			s := f.name + ": " + p.String()
			if p.Fatal {
				s += " [fatal]"
			}
			problems = append(problems, s)
		}
		if j.e.HasInit(f.name) {
			j.record(j.e.Init(f.name, first, j.host), false)
		}
	}
	return problems
}

func (j *journey) setup(l string) bool {
	h := j.host
	cmd, rest, _ := strings.Cut(l, " ")
	switch cmd {
	case "me":
		h.me = rest
	case "chan":
		w := strings.Fields(rest)
		var nicks []Nick
		for _, n := range w[1:] {
			name := strings.TrimLeft(n, "~&@%+")
			nicks = append(nicks, Nick{Name: name, Prefix: n[:len(n)-len(name)]})
		}
		h.chans[strings.ToLower(w[0])] = nicks
	case "active":
		h.active = rest
	case "query":
		h.queries = append(h.queries, rest)
	case "info":
		// info server=irc.test port=6697 ssl connected PREFIX=(qohv)~@%+ usermodes=+iw away=msg
		h.info = NetInfo{ISupport: map[string]string{}}
		for kv := range strings.FieldsSeq(rest) {
			k, v, _ := strings.Cut(kv, "=")
			switch k {
			case "server":
				h.info.Server = v
			case "port":
				h.info.Port = atoi(v)
			case "ssl":
				h.info.SSL = true
			case "connected":
				h.info.Connected = true
			case "usermodes":
				h.info.UserModes = v
			case "away":
				h.info.Away, h.info.AwayMsg = true, v
			default:
				h.info.ISupport[k] = v
			}
		}
	case "clock":
		now, err := time.Parse(time.RFC3339, rest)
		if err != nil {
			j.t.Fatal(err)
		}
		h.now = now
	default:
		return false
	}
	return true
}

func (j *journey) step(l string) {
	t := j.t
	if j.setup(l) {
		return
	}
	switch {
	case strings.HasPrefix(l, "> "), strings.HasPrefix(l, "= "), l == "* halted", l == "* hidden", l == "* input stopped":
		if len(j.out) == 0 {
			t.Fatalf("want %q, but the scripts did nothing more", l)
		}
		if j.out[0] != l {
			t.Fatalf("got  %q\nwant %q", j.out[0], l)
		}
		j.out = j.out[1:]
		return
	case strings.HasPrefix(l, "| "):
		if len(j.out) == 0 || j.out[0] != l {
			t.Fatalf("got  %q\nwant %q", strings.Join(j.out, " "), l)
		}
		j.out = j.out[1:]
		return
	case strings.HasPrefix(l, "? "):
		name, want, _ := strings.Cut(l[2:], " ")
		if got := j.e.Vars()[strings.TrimPrefix(name, "%")]; got != want {
			t.Fatalf("%s = %q, want %q", name, got, want)
		}
		return
	}
	j.flush()
	switch {
	case strings.HasPrefix(l, "< "):
		raw := strings.ReplaceAll(l[2:], `\x01`, "\x01")
		m, err := ircmsg.ParseLine(raw)
		if err != nil {
			t.Fatalf("%q: %v", l, err)
		}
		nick, _, _ := strings.Cut(m.Source, "!")
		ev := EventFromMessage(j.host.net, m, strings.EqualFold(nick, j.host.me))
		// Like the client: ^ handlers before it shows the line, the rest
		// after.
		if j.e.HasBefore(ev.Kind) {
			actions, halted := j.e.DispatchBefore(ev, j.host)
			j.record(actions, false)
			if halted {
				j.out = append(j.out, "* hidden")
			}
			ev.Halted = halted
		}
		j.host.apply(m)
		actions, halted := j.e.Dispatch(ev, j.host)
		j.record(actions, halted)
	case strings.HasPrefix(l, "type "), strings.HasPrefix(l, "/"):
		// Like the client: on INPUT first, which can stop the line.
		text := strings.TrimPrefix(l, "type ")
		actions, halted := j.e.Dispatch(Event{Kind: "INPUT", Network: j.host.net, Target: j.host.active,
			Chan: iff(isChannel(j.host.active), j.host.active, ""), Text: text}, j.host)
		j.record(actions, false)
		if halted {
			j.out = append(j.out, "* input stopped")
			return
		}
		if !strings.HasPrefix(text, "/") {
			j.out = append(j.out, "> /say "+text)
			return
		}
		l = text
		// /!cmd skips aliases, and a command that isn't one runs as a
		// built-in.
		name, args, _ := strings.Cut(l[1:], " ")
		if actions, ok := j.e.Alias(j.host.net, j.host.active, name, args, j.host); ok && !strings.HasPrefix(name, "!") {
			j.record(actions, false)
		} else {
			j.out = append(j.out, "> /"+strings.TrimPrefix(l[1:], "!"))
		}
	case strings.HasPrefix(l, "@ +"):
		d, err := time.ParseDuration(l[3:])
		if err != nil {
			t.Fatal(err)
		}
		j.wait(j.host.now.Add(d))
	case strings.HasPrefix(l, "event "):
		// event KIND nick target :text, for events only the client makes.
		head, text, _ := strings.Cut(l[6:], " :")
		w := append(strings.Fields(head), "", "", "")
		ev := Event{Kind: w[0], Network: j.host.net, Nick: w[1], Target: w[2], Text: text}
		actions, halted := j.e.Dispatch(ev, j.host)
		j.record(actions, halted)
	case l == "reload":
		j.load(false)
	case strings.HasPrefix(l, "menu "):
		w := strings.Fields(l)
		items, actions := j.e.Popups(w[1], j.host.net, j.host.active, w[2:], j.host)
		j.showMenu(items, "")
		j.record(actions, false)
	case strings.HasPrefix(l, "pick "):
		w := strings.Fields(l)
		items, actions := j.e.Popups(w[1], j.host.net, j.host.active, w[3:], j.host)
		j.record(actions, false)
		id := findMenu(items, strings.Split(w[2], "/"))
		if id == "" {
			t.Fatalf("no popup item %s", w[2])
		}
		j.record(j.e.RunMenu(id, w[1], j.host.net, j.host.active, w[3:], j.host), false)
	default:
		t.Fatalf("bad transcript line %q", l)
	}
}

// wait moves the clock to until, running each timer as it comes due, the
// way the client would.
func (j *journey) wait(until time.Time) {
	if j.waiting {
		return
	}
	j.waiting = true
	defer func() { j.waiting = false }()
	for range 100000 {
		next, ok := j.e.NextTimer()
		if !ok || next.After(until) {
			break
		}
		j.host.now = later(j.host.now, next)
		j.record(j.e.Tick(j.host), false)
	}
	j.host.now = until
}

func later(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// flush fails if the scripts did something the transcript didn't list.
func (j *journey) flush() {
	if len(j.out) > 0 {
		j.t.Fatalf("the scripts also did %q", j.out)
	}
}

func (j *journey) record(actions []Action, halted bool) {
	for _, a := range actions {
		switch {
		case a.Save != nil:
			if a.Save.Vars != nil {
				j.saved = maps.Clone(a.Save.Vars)
			}
			if a.Save.Users != nil {
				j.savedUsers = a.Save.Users
			}
			j.savedDLevel = a.Save.DLevel
		case a.File != nil:
			j.host.writeFile(a.File)
		case a.Echo != nil:
			win := cmp.Or(a.Echo.Window, "status")
			if a.Echo.Color >= 0 {
				win += " " + itoa(a.Echo.Color)
			}
			j.out = append(j.out, "= "+win+": "+a.Echo.Text)
		default:
			j.out = append(j.out, "> "+a.Command)
		}
	}
	if halted {
		j.out = append(j.out, "* halted")
	}
	// A timer with no delay runs as soon as the script that started it ends.
	if next, ok := j.e.NextTimer(); ok && !next.After(j.host.now) {
		j.wait(j.host.now)
	}
}

// fakeHost is one network's channels, updated by the raw lines the way
// the client updates its state before scripts see an event.
type fakeHost struct {
	me, net, active string
	chans           map[string][]Nick
	addrs           map[string]string // nick -> user@host, from the lines seen
	files           map[string]string // the scripts folder
	queries         []string
	info            NetInfo
	now             time.Time
}

func (h *fakeHost) Me(string) string           { return h.me }
func (h *fakeHost) Active() (string, string)   { return h.net, h.active }
func (h *fakeHost) Nicks(_, ch string) []Nick  { return h.chans[strings.ToLower(ch)] }
func (h *fakeHost) HasWindow(_, w string) bool { _, ok := h.chans[strings.ToLower(w)]; return ok }
func (h *fakeHost) Now() time.Time             { return h.now }

func (h *fakeHost) Chans(string) []string   { return slices.Sorted(maps.Keys(h.chans)) }
func (h *fakeHost) Queries(string) []string { return h.queries }
func (h *fakeHost) Networks() []string      { return []string{h.net} }
func (h *fakeHost) Info(string) NetInfo     { return h.info }

func (h *fakeHost) IAL(string) []string {
	var ial []string
	for _, nick := range slices.Sorted(maps.Keys(h.addrs)) {
		ial = append(ial, h.addrs[nick])
	}
	return ial
}

func (h *fakeHost) ReadFile(name string) (string, bool) {
	data, ok := h.files[strings.ToLower(name)]
	return data, ok
}

// writeFile applies a File action, as the client would.
func (h *fakeHost) writeFile(f *File) {
	if h.files == nil {
		h.files = map[string]string{}
	}
	key := strings.ToLower(f.Name)
	if f.Append {
		h.files[key] += f.Data
	} else {
		h.files[key] = f.Data
	}
}

func (h *fakeHost) apply(m ircmsg.Message) {
	nick, addr, _ := strings.Cut(m.Source, "!")
	if addr != "" {
		if h.addrs == nil {
			h.addrs = map[string]string{}
		}
		h.addrs[strings.ToLower(nick)] = nick + "!" + addr
	}
	own := strings.EqualFold(nick, h.me)
	key := func(i int) string { return strings.ToLower(m.Params[i]) }
	remove := func(ch, nick string) {
		h.chans[ch] = slices.DeleteFunc(h.chans[ch], func(n Nick) bool { return strings.EqualFold(n.Name, nick) })
	}
	switch m.Command {
	case "JOIN":
		if own {
			h.chans[key(0)] = nil
		}
		h.chans[key(0)] = append(h.chans[key(0)], Nick{Name: nick})
	case "PART":
		remove(key(0), nick)
		if own {
			delete(h.chans, key(0))
		}
	case "KICK":
		remove(key(0), m.Params[1])
		if strings.EqualFold(m.Params[1], h.me) {
			delete(h.chans, key(0))
		}
	case "QUIT":
		for ch := range h.chans {
			remove(ch, nick)
		}
	case "NICK":
		for _, nicks := range h.chans {
			for i := range nicks {
				if strings.EqualFold(nicks[i].Name, nick) {
					nicks[i].Name = m.Params[0]
				}
			}
		}
		if own {
			h.me = m.Params[0]
		}
	case "MODE":
		// Only +o/-o/+v/-v on one nick, which is all the journeys need.
		if len(m.Params) == 3 && len(m.Params[1]) == 2 {
			prefix := map[byte]string{'o': "@", 'v': "+"}[m.Params[1][1]]
			for i, n := range h.chans[key(0)] {
				if strings.EqualFold(n.Name, m.Params[2]) {
					p := strings.ReplaceAll(n.Prefix, prefix, "")
					if m.Params[1][0] == '+' {
						p = prefix + p
					}
					h.chans[key(0)][i].Prefix = p
				}
			}
		}
	}
}

// showMenu adds a popup's items to the output, as | lines.
func (j *journey) showMenu(items []MenuItem, indent string) {
	for _, m := range items {
		line := "| " + indent + m.Label
		switch {
		case m.Separator:
			line = "| " + indent + "-"
		case m.Checked && m.Disabled:
			line += " [x] (disabled)"
		case m.Checked:
			line += " [x]"
		case m.Disabled:
			line += " (disabled)"
		}
		j.out = append(j.out, line)
		j.showMenu(m.Items, indent+"  ")
	}
}

// findMenu finds the item at a path of labels.
func findMenu(items []MenuItem, path []string) string {
	for _, m := range items {
		if m.Label != path[0] {
			continue
		}
		if len(path) == 1 {
			return m.ID
		}
		return findMenu(m.Items, path[1:])
	}
	return ""
}
