// Package script runs a subset of mIRC's scripting language: aliases,
// remote events, variables and the identifiers that go with them. It's for
// automation (auto-greet, !seen, kick counters), not mIRC's dialogs or DLLs.
// See tmp/scripting.md.
//
// The engine does no I/O and knows nothing about the client. Events go in,
// Actions come out, and anything it needs to know about the client (our
// nick, nicklists, the time) it asks a Host. Scripts never send IRC
// themselves: their commands come back as text for the client to run like
// typed input.
package script

import (
	"maps"
	"math/rand/v2"
	"slices"
	"strings"
	"time"
)

// Host answers the engine's questions about the client. Read-only.
type Host interface {
	// Me is our nick on network.
	Me(network string) string
	// Active is the window the user is looking at; window is "" for a
	// network's status window.
	Active() (network, window string)
	// Nicks is channel's nicklist, in display order.
	Nicks(network, channel string) []Nick
	// HasWindow reports whether a channel, query or other window by that
	// name is open, for /echo's window parameter.
	HasWindow(network, name string) bool
	// Chans is the channels we're on, in order: $chan(N), $comchan.
	Chans(network string) []string
	// Address is nick's user@host, if the client knows it (mIRC's
	// Internal Address List): $address(nick,type).
	Address(network, nick string) string
	// ReadFile reads a file from the scripts folder: /hload, $read. The
	// name is as the script gave it; the client keeps it in that folder.
	ReadFile(name string) (string, bool)
	Now() time.Time
}

// Nick is someone in a nicklist.
type Nick struct {
	Name   string
	Prefix string // their mode prefixes, highest first: "@", "+", "@+"...
}

// Action is something a script wants done. The engine never does it itself.
type Action struct {
	// Command is a command to run like typed input: "/msg #c hi". A "."
	// after the slash means quietly, like mIRC's /.ignore.
	Command string
	Network string // the network it runs on: $network when it ran
	Window  string // the window it runs in, "" for status
	Echo    *Echo
	Save    *Save
	File    *File
}

// File is a file for the client to write in the scripts folder, from
// /hsave or /write.
type File struct {
	Name   string // as the script gave it
	Data   string
	Append bool // add Data to the end, instead of replacing the file
}

// Echo is a line a script prints with /echo, or an error message.
type Echo struct {
	Network string
	Window  string // "" for the status window
	Color   int    // mIRC color 0-99, or -1 for the default
	Text    string
}

// Save is the global variables to keep, sent after a script changed them.
type Save struct {
	Vars map[string]string // without the %, lower-case names
}

// Problem is something wrong or unsupported in a script, found when it's
// loaded.
type Problem struct {
	Line  int
	Text  string
	Fatal bool // the script wasn't loaded
}

func (p Problem) String() string {
	if p.Line == 0 {
		return p.Text
	}
	return "line " + itoa(p.Line) + ": " + p.Text
}

// Options are the engine's settings. The zero value is fine.
type Options struct {
	// MaxSteps is how many statements one event or alias call may run
	// before it's stopped (default 100000). mIRC freezes on a script that
	// never ends and offers Ctrl+Break; a terminal has no Ctrl+Break, so
	// trout stops it instead.
	MaxSteps int
	// Rand is the source for $rand; nil uses a random seed.
	Rand *rand.Rand
}

// Engine is a set of loaded scripts and their variables. It isn't safe
// for concurrent use: the client calls it from its Update loop.
type Engine struct {
	opts    Options
	scripts []*file
	vars    map[string]*variable // global variables, by lower-case name
	dirty   bool                 // vars changed since the last Save
	groups  map[string]bool      // #group name -> enabled, across scripts
	tables  map[string]*table    // hash tables, by lower-case name
	timers  []*timer
	regml   regexMatches // the last matches, for $regml
	regexes map[string]*compiledRegex
	ctimer  string // the timer running now
	ltimer  string // the last timer started
}

type variable struct {
	name  string // as first set, without %
	value string
	temp  bool // set -e: not saved
}

func New(opts Options) *Engine {
	if opts.MaxSteps <= 0 {
		opts.MaxSteps = 100000
	}
	if opts.Rand == nil {
		opts.Rand = rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64()))
	}
	return &Engine{opts: opts, vars: map[string]*variable{}, groups: map[string]bool{},
		tables: map[string]*table{}, regexes: map[string]*compiledRegex{}}
}

// Load parses a script and loads it, replacing a script of the same name.
// The Problems say what's wrong or unsupported; if one is Fatal, nothing
// was loaded. Loading doesn't run on LOAD or on START: that's Init.
func (e *Engine) Load(name, src string) []Problem {
	f, problems := parseFile(name, src)
	problems = append(problems, e.check(f)...)
	slices.SortStableFunc(problems, func(a, b Problem) int { return a.Line - b.Line })
	if slices.ContainsFunc(problems, func(p Problem) bool { return p.Fatal }) {
		return problems
	}
	if i := e.find(name); i >= 0 {
		e.scripts[i] = f
	} else {
		e.scripts = append(e.scripts, f)
	}
	for _, g := range f.groups {
		e.groups[strings.ToLower(g.name)] = g.on
	}
	return problems
}

// Unload removes a script.
func (e *Engine) Unload(name string) {
	if i := e.find(name); i >= 0 {
		e.scripts = slices.Delete(e.scripts, i, i+1)
	}
}

// Scripts is the loaded scripts' names, in load order.
func (e *Engine) Scripts() []string {
	var names []string
	for _, f := range e.scripts {
		names = append(names, f.name)
	}
	return names
}

func (e *Engine) find(name string) int {
	return slices.IndexFunc(e.scripts, func(f *file) bool { return strings.EqualFold(f.name, name) })
}

// HasInit reports whether a script has on LOAD or on START events: mIRC
// asks before running a new script's initialization commands.
func (e *Engine) HasInit(name string) bool {
	i := e.find(name)
	if i < 0 {
		return false
	}
	return slices.ContainsFunc(e.scripts[i].events, func(ev *event) bool {
		return ev.kind == "LOAD" || ev.kind == "START"
	})
}

// Init runs a script's on START event, and on LOAD before it when first is
// true (the script was just loaded, not reloaded at startup), like mIRC.
func (e *Engine) Init(name string, first bool, h Host) []Action {
	i := e.find(name)
	if i < 0 {
		return nil
	}
	net, _ := h.Active()
	r := e.newRun(h)
	if first {
		r.dispatchFile(e.scripts[i], &Event{Kind: "LOAD", Network: net})
	}
	r.dispatchFile(e.scripts[i], &Event{Kind: "START", Network: net})
	return r.finish()
}

// Dispatch runs every script's matching handler for an event. halted is
// true if one used /halt, which for a CTCP means the client shouldn't send
// its own reply.
func (e *Engine) Dispatch(ev Event, h Host) (actions []Action, halted bool) {
	r := e.newRun(h)
	for _, f := range e.scripts {
		if r.dispatchFile(f, &ev) {
			halted = true
		}
	}
	return r.finish(), halted
}

// Alias runs a command typed in the editbox (without its slash) if it's an
// alias, like mIRC: aliases come before built-in commands. args aren't
// evaluated, the way a single / leaves them. ok is false if name isn't an
// alias, and the client should run the command itself.
func (e *Engine) Alias(network, window, name, args string, h Host) (actions []Action, ok bool) {
	a := e.alias(nil, name)
	if a == nil {
		return nil, false
	}
	r := e.newRun(h)
	fr := &frame{network: network, window: window, params: fields(args), typed: true}
	if isChannel(window) {
		fr.channel = window
	}
	r.runAlias(fr, a)
	return r.finish(), true
}

// Vars is a copy of the global variables, without the % and the ones set
// with -e, as Save sends them.
func (e *Engine) Vars() map[string]string {
	vars := map[string]string{}
	for _, v := range e.vars {
		if !v.temp {
			vars[v.name] = v.value
		}
	}
	return vars
}

// Restore sets the global variables saved from an earlier session.
func (e *Engine) Restore(vars map[string]string) {
	for _, name := range slices.Sorted(maps.Keys(vars)) {
		e.vars[strings.ToLower(name)] = &variable{name: name, value: vars[name]}
	}
}

// alias finds an alias by name: one of from's local (-l) aliases, or a
// global one from any script.
func (e *Engine) alias(from *file, name string) *alias {
	if from != nil {
		if a := from.aliases[strings.ToLower(name)]; a != nil {
			return a
		}
	}
	for _, f := range e.scripts {
		if a := f.aliases[strings.ToLower(name)]; a != nil && !a.local {
			return a
		}
	}
	return nil
}

// readFile reads a script's file: as written earlier in this run (the
// client hasn't written it yet), or the client's.
func (r *run) readFile(name string) (string, bool) {
	key := strings.ToLower(name)
	if w, ok := r.files[key]; ok {
		if !w.appended {
			return w.data.String(), true
		}
		old, _ := r.h.ReadFile(name)
		return old + w.data.String(), true
	}
	return r.h.ReadFile(name)
}

// writeFile writes a script's file, through the client. It's kept for the
// rest of this run, so a script can read back what it wrote; the client
// writes it right after.
func (r *run) writeFile(name, data string, appendTo bool) {
	key := strings.ToLower(name)
	if r.files == nil {
		r.files = map[string]*pendingFile{}
	}
	w := r.files[key]
	if w == nil || !appendTo {
		w = &pendingFile{appended: appendTo}
		r.files[key] = w
	}
	w.data.WriteString(data)
	r.actions = append(r.actions, Action{File: &File{Name: name, Data: data, Append: appendTo}})
}

// pendingFile is what a run wrote to a file: all of it, or with appended
// what it added to the client's.
type pendingFile struct {
	data     strings.Builder
	appended bool
}
