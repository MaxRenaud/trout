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
	// Queries is the open query windows: $query(N).
	Queries(network string) []string
	// IAL is the nick!user@host of everyone the client knows the address
	// of (mIRC's Internal Address List): $ial, $address(nick,type).
	IAL(network string) []string
	// Networks is the networks, in order: a network's connection id
	// ($cid, /scid) is its place in it, from 1.
	Networks() []string
	// Info is a network's connection: $server, $status, ISUPPORT...
	Info(network string) NetInfo
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
// /hsave, /write, /writeini, /fwrite or /copy, or to delete (/remove).
type File struct {
	Name   string // as the script gave it
	Data   string
	Append bool // add Data to the end, instead of replacing the file
	Delete bool // delete the file instead
}

// Echo is a line a script prints with /echo, or an error message.
type Echo struct {
	Network string
	Window  string // "" for the status window
	Color   int    // mIRC color 0-99, or -1 for the default
	Text    string
}

// Save is what to keep after a script changed it: the global variables,
// or the Users list and default level. A nil field didn't change.
type Save struct {
	Vars   map[string]string // without the %
	Users  []string          // levels:address info lines; non-nil when the list changed
	DLevel int
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
	opts       Options
	scripts    []*file
	vars       map[string]*variable // global variables, by lower-case name
	dirty      bool                 // vars changed since the last Save
	groups     map[string]bool      // #group name -> enabled, across scripts
	tables     map[string]*table    // hash tables, by lower-case name
	timers     []*timer
	regml      regexMatches // the last matches, for $regml
	regexes    map[string]*compiledRegex
	handles    map[string]*handle // /fopen's files, by lower-case name
	ferr       bool               // $ferr: the last file access failed
	feof       bool               // $feof: the last read reached the end
	users      []*user            // the Users list
	dlevel     int                // the default level, for users who aren't listed
	usersDirty bool               // the Users list changed since the last Save
	ctimer     string             // the timer running now
	ltimer     string             // the last timer started
}

type variable struct {
	name    string // as first set, without %
	value   string
	temp    bool      // set -e or -uN: not saved
	expires time.Time // set -uN: when it's unset
}

func New(opts Options) *Engine {
	if opts.MaxSteps <= 0 {
		opts.MaxSteps = 100000
	}
	if opts.Rand == nil {
		opts.Rand = rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64()))
	}
	return &Engine{opts: opts, dlevel: 1, vars: map[string]*variable{}, groups: map[string]bool{},
		tables: map[string]*table{}, regexes: map[string]*compiledRegex{}, handles: map[string]*handle{}}
}

// Load parses a script and loads it, replacing a script of the same name.
// The Problems say what's wrong or unsupported; if one is Fatal, nothing
// was loaded. Loading doesn't run on LOAD or on START: that's Init.
func (e *Engine) Load(name, src string) []Problem { return e.LoadAs(name, src, "") }

// LoadAs loads a file of aliases ("aliases", like mIRC's /load -a, each
// alias without alias in front) or of popup items (as is the popup:
// status, channel, query, nicklist or menubar, like /load -ps... -pm), or
// with "" a script, like Load.
func (e *Engine) LoadAs(name, src, as string) []Problem {
	f, problems := parseFileAs(name, src, as)
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

// Unload runs a script's on UNLOAD, then removes it.
func (e *Engine) Unload(name string, h Host) []Action {
	i := e.find(name)
	if i < 0 {
		return nil
	}
	r := e.newRun(h)
	net, _ := h.Active()
	r.dispatchFile(e.scripts[i], &Event{Kind: "UNLOAD", Network: net})
	e.scripts = slices.Delete(e.scripts, i, i+1)
	return r.finish()
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
	events := r.expand(ev)
	for i := range events {
		for _, f := range e.scripts {
			if r.dispatchFile(f, &events[i]) {
				halted = true
			}
		}
	}
	return r.finish(), halted || r.haltdef
}

// DispatchBefore runs every script's ^ handler for an event, before the
// client shows it, and reports whether one halted that (with /halt or
// /haltdef), like mIRC's ^ events. Dispatch runs the others after, with
// ev.Halted set to what this returned.
func (e *Engine) DispatchBefore(ev Event, h Host) (actions []Action, halted bool) {
	r := e.newRun(h)
	r.caret = true
	events := r.expand(ev)
	for i := range events {
		for _, f := range e.scripts {
			if r.dispatchFile(f, &events[i]) {
				halted = true
			}
		}
	}
	return r.finish(), halted || r.haltdef
}

// expand is the events an event is: a channel MODE line is on RAWMODE,
// then an event for each change (OP, BAN...) and MODE for the rest.
func (r *run) expand(ev Event) []Event {
	events := []Event{ev}
	if ev.Kind == "RAWMODE" {
		events[0].modes = r.splitModes(ev.Network, ev.Params[min(1, len(ev.Params)):])
		events = append(events, r.modeEvents(&events[0])...)
	}
	return events
}

// HasBefore reports whether any script has a ^ handler for this kind of
// event, so the client knows whether to call DispatchBefore.
func (e *Engine) HasBefore(kind string) bool {
	for _, f := range e.scripts {
		for _, h := range f.events {
			if h.caret && (h.kind == kind || kind == "RAWMODE" && modeKind[h.kind]) {
				return true
			}
		}
	}
	return false
}

// modeKind are the events a RAWMODE line turns into.
var modeKind = map[string]bool{"OP": true, "DEOP": true, "SERVEROP": true, "VOICE": true, "DEVOICE": true,
	"HELP": true, "DEHELP": true, "OWNER": true, "DEOWNER": true, "BAN": true, "UNBAN": true,
	"MODE": true, "SERVERMODE": true}

// Alias runs a command typed in the editbox, name and args, with a single
// / (so args aren't evaluated). See Command.
func (e *Engine) Alias(network, window, name, args string, h Host) (actions []Action, ok bool) {
	return e.Command(network, window, strings.TrimSpace(name+" "+args), false, h)
}

// Command runs a line typed in the editbox, without its slashes, if the
// engine does it: an alias (aliases come before built-in commands, like
// mIRC's; a ! in front skips them), or one of the engine's own commands
// (/set, /hadd, /timer, /auser...). With eval (typed with //), the line's
// identifiers and variables are evaluated first, like mIRC's, and any
// command works: one that's the client's comes back as an Action. Without
// it, nothing is evaluated. ok is false if the client should run the line
// itself.
func (e *Engine) Command(network, window, line string, eval bool, h Host) (actions []Action, ok bool) {
	word, args, _ := strings.Cut(strings.TrimSpace(line), " ")
	name := strings.TrimLeft(word, ".!")
	r := e.newRun(h)
	fr := &frame{network: network, window: window, params: fields(args), typed: true}
	if isChannel(window) {
		fr.channel = window
	}
	if a := e.alias(nil, name); a != nil && !strings.Contains(word, "!") {
		if eval {
			v, err := r.eval(fr, args)
			if err != nil {
				r.report(fr, err)
				return r.finish(), true
			}
			fr.params = fields(v)
		}
		r.runAlias(fr, a)
		return r.finish(), true
	}
	if !eval && !engineCommand(name) {
		return nil, false
	}
	if !eval {
		r.literal++
	}
	r.report(fr, r.command(fr, line))
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
		if w.deleted {
			return "", false
		}
		if !w.appended {
			return w.data.String(), true
		}
		old, _ := r.h.ReadFile(name)
		return old + w.data.String(), true
	}
	return r.h.ReadFile(name)
}

// deleteFile deletes a script's file, through the client.
func (r *run) deleteFile(name string) {
	if r.files == nil {
		r.files = map[string]*pendingFile{}
	}
	r.files[strings.ToLower(name)] = &pendingFile{deleted: true}
	r.actions = append(r.actions, Action{File: &File{Name: name, Delete: true}})
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
	if w == nil || !appendTo || w.deleted {
		// After a /remove, even an append starts the file afresh.
		w = &pendingFile{appended: appendTo && w == nil}
		r.files[key] = w
	}
	w.data.WriteString(data)
	if !appendTo {
		// The whole file again (/fwrite, /writeini...): this run's earlier
		// writes of it are out of date, so the client gets only this one.
		r.actions = slices.DeleteFunc(r.actions, func(a Action) bool {
			return a.File != nil && !a.File.Delete && strings.EqualFold(a.File.Name, name)
		})
	}
	r.actions = append(r.actions, Action{File: &File{Name: name, Data: data, Append: appendTo}})
}

// pendingFile is what a run wrote to a file: all of it, or with appended
// what it added to the client's.
type pendingFile struct {
	data     strings.Builder
	appended bool
	deleted  bool
}
