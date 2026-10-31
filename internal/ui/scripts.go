package ui

import (
	"cmp"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ergochat/irc-go/ircmsg"

	"github.com/MaxRenaud/trout/internal/script"
	"github.com/MaxRenaud/trout/internal/state"
)

// Scripts: trout's side of internal/script, mIRC's remote scripts. The
// engine sees IRC through three hooks (handleIRC, typed lines in submit,
// and the popups) and the client events below; it asks trout what it needs
// through scriptHost, and what it wants done comes back as actions, which
// runScript does like typed commands. See tmp/scripting.md.
//
// Loaded scripts are config.toml's scripts, in order. They live in the
// scripts folder next to config.toml (or anywhere, by full path); their
// variables and Users list are vars.ini and users.ini there, like mIRC's,
// and their own files ($read, /write, /hsave) can only be in that folder.

// scriptTickMsg wakes the engine for a timer, or a -uN variable to unset.
type scriptTickMsg struct{}

// scriptsOn reports whether scripts run: there's an engine and /remote
// isn't off.
func (a *App) scriptsOn() bool {
	return a.scripts != nil && !a.book().ScriptsOff
}

// scriptsDir is the scripts folder, or "" without a config (tests and the
// demo), where scriptMem stands in for it.
func (a *App) scriptsDir() string {
	if a.cfg == nil {
		return ""
	}
	return filepath.Join(filepath.Dir(a.cfg.Path), "scripts")
}

// scriptFile is where a script's file name is in the scripts folder: no
// absolute paths and no .., so a script can't reach outside it. mIRC's
// backslashes work too.
func (a *App) scriptFile(name string) (string, bool) {
	name = filepath.Clean(filepath.FromSlash(strings.ReplaceAll(name, `\`, "/")))
	if name == "" || !filepath.IsLocal(name) {
		return "", false
	}
	return filepath.Join(a.scriptsDir(), name), true
}

func (a *App) scriptReadFile(name string) (string, bool) {
	if a.scriptsDir() == "" {
		data, ok := a.scriptMem[strings.ToLower(name)]
		return data, ok
	}
	path, ok := a.scriptFile(name)
	if !ok {
		return "", false
	}
	b, err := os.ReadFile(path)
	return string(b), err == nil
}

// scriptWriteFile does a script's File action.
func (a *App) scriptWriteFile(f *script.File) error {
	if a.scriptsDir() == "" {
		if a.scriptMem == nil {
			a.scriptMem = map[string]string{}
		}
		key := strings.ToLower(f.Name)
		switch {
		case f.Delete:
			delete(a.scriptMem, key)
		case f.Append:
			a.scriptMem[key] += f.Data
		default:
			a.scriptMem[key] = f.Data
		}
		return nil
	}
	path, ok := a.scriptFile(f.Name)
	if !ok {
		return errors.New(f.Name + " isn't in the scripts folder")
	}
	if f.Delete {
		return os.Remove(path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	flags := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	if f.Append {
		flags = os.O_CREATE | os.O_WRONLY | os.O_APPEND
	}
	file, err := os.OpenFile(path, flags, 0o600)
	if err != nil {
		return err
	}
	_, err = file.WriteString(f.Data)
	return errors.Join(err, file.Close())
}

// readScript reads a script to load: a file name in the scripts folder,
// or a full path the user gave.
func (a *App) readScript(name string) (string, error) {
	if filepath.IsAbs(name) {
		b, err := os.ReadFile(name)
		return string(b), err
	}
	if data, ok := a.scriptReadFile(name); ok {
		return data, nil
	}
	return "", errors.New("no such file")
}

// startScripts loads config.toml's scripts with their saved variables and
// Users list, and runs their on START, at startup.
func (a *App) startScripts() tea.Cmd {
	if a.scripts == nil {
		return nil
	}
	b := a.book()
	a.applyScriptOpts()
	vars, _ := a.scriptReadFile("vars.ini")
	saved := map[string]string{}
	for _, l := range readIniList(vars, "variables") {
		if name, value, _ := strings.Cut(strings.TrimPrefix(l, "%"), " "); name != "" {
			saved[name] = value
		}
	}
	a.scripts.Restore(saved)
	users, _ := a.scriptReadFile("users.ini")
	a.scripts.RestoreUsers(readIniList(users, "users"), b.ScriptLevel)
	var cmds []tea.Cmd
	for _, name := range b.Scripts {
		cmds = append(cmds, a.loadScript(name, false))
	}
	return tea.Batch(cmds...)
}

// applyScriptOpts tells the engine the Scripts Editor's Listen menu and
// Identifier Warning.
func (a *App) applyScriptOpts() {
	o := a.book().ScriptOpts
	a.scripts.Listen(!o.CTCPsOff, !o.EventsOff, !o.RawOff)
	a.scripts.IdentifierWarning(o.IdentWarning)
}

// readIniList reads an ini section's n0=, n1=... lines (vars.ini,
// users.ini), in order: the Users list's order matters, the first entry
// that matches someone is theirs.
func readIniList(data, section string) []string {
	var out []string
	in := false
	for l := range strings.Lines(data) {
		l = strings.TrimRight(l, "\r\n")
		if strings.HasPrefix(l, "[") {
			in = strings.EqualFold(l, "["+section+"]")
			continue
		}
		if _, v, ok := strings.Cut(l, "="); in && ok {
			out = append(out, v)
		}
	}
	return out
}

// writeIniList writes lines as an ini section's n0=, n1=...
func writeIniList(section string, lines []string) string {
	var b strings.Builder
	b.WriteString("[" + section + "]\r\n")
	for i, l := range lines {
		b.WriteString("n" + strconv.Itoa(i) + "=" + l + "\r\n")
	}
	return b.String()
}

// loadScript loads a script by name: at startup (first false) it runs on
// START; loaded now (first true), it asks before running its on LOAD and
// on START, like mIRC.
func (a *App) loadScript(entry string, first bool) tea.Cmd {
	where := a.firstStatus()
	if first {
		where = a.active
	}
	as, name := splitLoad(entry)
	src, err := a.readScript(name)
	if err != nil {
		a.addLine(where, state.RoleQuit, "* /load: couldn't read "+name+": "+err.Error())
		return nil
	}
	problems := a.scripts.LoadAs(name, src, as)
	a.scriptProblems[strings.ToLower(name)] = problems
	fatal := slices.ContainsFunc(problems, func(p script.Problem) bool { return p.Fatal })
	switch {
	case fatal:
		a.addLine(where, state.RoleQuit, "* "+name+" wasn't loaded:")
	case first:
		a.addLine(where, state.RoleInfo, "* Loaded "+name)
	}
	if first || fatal {
		for _, p := range problems {
			a.addLine(where, state.RoleInfo, "*   "+p.String())
		}
	}
	if fatal {
		return nil
	}
	if !first {
		return a.runScript(a.scripts.Init(name, false, a.scriptHost()))
	}
	b := a.book()
	if !slices.ContainsFunc(b.Scripts, func(s string) bool { _, f := splitLoad(s); return strings.EqualFold(f, name) }) {
		b.Scripts = append(b.Scripts, entry)
		a.saveConfig()
	}
	if as != "" || !a.scripts.HasInit(name) {
		return nil
	}
	if b.ScriptOpts.NoInitWarning { // Options > Initialization Warning, off
		return a.runScript(a.scripts.Init(name, true, a.scriptHost()))
	}
	// Like mIRC's Script Warning: a new script's initialization commands
	// run only if you say so.
	d := message(name+" has initialization commands.", "", "Run them?")
	d.Title = "Script Warning"
	d.Buttons, d.Cancel = []string{"Yes", "No"}, 1
	d.OnClose = func(a *App, button int) tea.Cmd {
		if button == 0 {
			return a.runScript(a.scripts.Init(name, true, a.scriptHost()))
		}
		return nil
	}
	// Loaded in the Scripts Editor, it asks once the editor has closed.
	a.whenFree(func(a *App) tea.Cmd { a.openDialog(d); return nil })
	return nil
}

// unloadScript runs a script's on UNLOAD and takes it out.
func (a *App) unloadScript(name string) tea.Cmd {
	b := a.book()
	_, name = splitLoad(name)
	i := slices.IndexFunc(b.Scripts, func(s string) bool { _, f := splitLoad(s); return strings.EqualFold(f, name) })
	if i < 0 {
		a.addLine(a.active, state.RoleInfo, "* /unload: "+name+" isn't loaded")
		return nil
	}
	_, name = splitLoad(b.Scripts[i])
	b.Scripts = slices.Delete(b.Scripts, i, i+1)
	a.saveConfig()
	delete(a.scriptProblems, strings.ToLower(name))
	a.addLine(a.active, state.RoleInfo, "* Unloaded "+name)
	return a.runScript(a.scripts.Unload(name, a.scriptHost()))
}

// scriptAdmin handles the commands that manage scripts, typed or from a
// script: /load -rs, /unload -rs, /reload -rs and /remote [on|off]. It
// reports whether line was one. A script (fromScript) can only load files
// from the scripts folder, like its other files: a full path is yours to
// type.
func (a *App) scriptAdmin(line string, fromScript bool) (tea.Cmd, bool) {
	if a.scripts == nil {
		return nil, false
	}
	name, rest, _ := strings.Cut(strings.TrimSpace(line), " ")
	rest = strings.TrimSpace(rest)
	sw := ""
	if strings.HasPrefix(rest, "-") {
		sw, rest, _ = strings.Cut(rest, " ")
	}
	file := strings.Trim(strings.TrimSpace(rest), `"`)
	if _, inFolder := a.scriptFile(file); fromScript && !inFolder {
		switch strings.ToLower(name) {
		case "load", "reload":
			a.addLine(a.firstStatus(), state.RoleInfo, "* /"+name+": a script can only load files from the scripts folder")
			return nil, true
		}
	}
	switch strings.ToLower(name) {
	case "load":
		if file == "" {
			a.addLine(a.active, state.RoleInfo, "* /load: /load -rs <file>, a file in the scripts folder or a full path")
			return nil, true
		}
		switch strings.ToLower(sw) {
		case "-rv", "-ru":
			return a.loadVarsOrUsers(file, sw == "-rv"), true
		case "", "-rs", "-r":
			return a.loadScript(file, true), true
		}
		if _, ok := loadKinds[strings.ToLower(sw)]; !ok {
			a.addLine(a.active, state.RoleInfo, "* /load: "+sw+" isn't one of -rs, -a, -ps, -pc, -pq, -pn, -pm, -rv, -ru")
			return nil, true
		}
		return a.loadScript(strings.ToLower(sw)+" "+file, true), true
	case "unload":
		return a.unloadScript(file), true
	case "reload":
		src, err := a.readScript(file)
		if err != nil {
			a.addLine(a.active, state.RoleQuit, "* /reload: couldn't read "+file+": "+err.Error())
			return nil, true
		}
		// Without on LOAD or on START, like mIRC's /reload.
		as := ""
		for _, s := range a.book().Scripts {
			if k, f := splitLoad(s); strings.EqualFold(f, file) {
				as = k
			}
		}
		a.scriptProblems[strings.ToLower(file)] = a.scripts.LoadAs(file, src, as)
		a.addLine(a.active, state.RoleInfo, "* Reloaded "+file)
		return nil, true
	case "remote":
		b := a.book()
		switch strings.ToLower(rest) {
		case "on":
			b.ScriptsOff = false
			a.saveConfig()
		case "off":
			b.ScriptsOff = true
			a.saveConfig()
		}
		a.addLine(a.active, state.RoleInfo, "* Remote is "+map[bool]string{true: "off", false: "on"}[b.ScriptsOff])
		return nil, true
	case "ctcps", "events", "raw":
		// mIRC's listening switches. /raw is also trout's raw IRC line:
		// only /raw on and /raw off are the switch, like mIRC.
		o := &a.book().ScriptOpts
		off := map[string]*bool{"ctcps": &o.CTCPsOff, "events": &o.EventsOff, "raw": &o.RawOff}[strings.ToLower(name)]
		switch strings.ToLower(rest) {
		case "on", "off":
			*off = strings.EqualFold(rest, "off")
			a.saveConfig()
			a.applyScriptOpts()
		case "":
			if strings.EqualFold(name, "raw") {
				return nil, false
			}
		default:
			return nil, false
		}
		label := map[string]string{"ctcps": "Ctcps are ", "events": "Events are ", "raw": "Raw is "}[strings.ToLower(name)]
		a.addLine(a.active, state.RoleInfo, "* "+label+map[bool]string{true: "off", false: "on"}[*off])
		return nil, true
	}
	return nil, false
}

// runScript does what scripts asked: commands like typed ones, echoes,
// files, and saving their variables and Users list. Then it wakes the
// engine for its next timer.
func (a *App) runScript(actions []script.Action) tea.Cmd {
	var cmds []tea.Cmd
	for _, act := range actions {
		switch {
		case act.Save != nil:
			a.saveScriptState(act.Save)
		case act.File != nil:
			if err := a.scriptWriteFile(act.File); err != nil {
				a.addLine(a.firstStatus(), state.RoleQuit, "* Script file "+act.File.Name+": "+err.Error())
			}
		case act.Echo != nil:
			w := a.scriptWindow(act.Echo.Network, act.Echo.Window)
			l := state.Line{Time: time.Now(), Role: state.RoleNormal, Text: act.Echo.Text}
			if act.Echo.Color >= 0 && act.Echo.Color < 16 {
				l.Color = act.Echo.Color + 1
			}
			a.addLineL(w, l)
		case act.Command != "":
			cmds = append(cmds, a.scriptCommand(a.scriptWindow(act.Network, act.Window), act.Command))
		}
	}
	return tea.Batch(append(cmds, a.scheduleScriptTick())...)
}

// scriptCommand runs a script's command like a typed one: trout's own
// commands, and the script management ones.
func (a *App) scriptCommand(w *state.Window, cmd string) tea.Cmd {
	line := strings.TrimLeft(strings.TrimPrefix(cmd, "/"), ".!")
	if c, ok := a.scriptAdmin(line, true); ok {
		return c
	}
	name, _, _ := strings.Cut(line, " ")
	switch strings.ToLower(name) {
	case "splay", "sound", "vol":
		// A terminal plays no sounds; scripts beeping on events go quietly.
		return nil
	}
	if what := script.Unsupported(name); what != "" {
		// mIRC's own, which a server would only call unknown: say why.
		a.addLine(w.Net.Status(), state.RoleInfo, "* /"+name+": "+what)
		return nil
	}
	return a.command(w, line)
}

// saveScriptState writes the scripts' variables (vars.ini) or Users list
// (users.ini), and the default level.
func (a *App) saveScriptState(s *script.Save) {
	write := func(name, data string) {
		if err := a.scriptWriteFile(&script.File{Name: name, Data: data}); err != nil {
			a.addLine(a.firstStatus(), state.RoleQuit, "* Couldn't save "+name+": "+err.Error())
		}
	}
	if s.Vars != nil {
		var lines []string
		for _, k := range slices.Sorted(maps.Keys(s.Vars)) {
			lines = append(lines, "%"+k+" "+s.Vars[k])
		}
		write("vars.ini", writeIniList("variables", lines))
	}
	if s.Users != nil {
		write("users.ini", writeIniList("users", s.Users))
	}
	if b := a.book(); s.DLevel != 0 && b.ScriptLevel != s.DLevel && !(b.ScriptLevel == 0 && s.DLevel == 1) {
		b.ScriptLevel = s.DLevel
		a.saveConfig()
	}
}

// scriptWindow finds a script's window: by network name and window name,
// "" for status. A window that isn't open is the network's status window.
func (a *App) scriptWindow(network, window string) *state.Window {
	n := a.scriptNet(network)
	if n == nil {
		return a.active
	}
	if window == "" {
		return n.Status()
	}
	for _, w := range n.Wins {
		if n.Same(w.Name, window) {
			return w
		}
	}
	return n.Status()
}

func (a *App) scriptNet(name string) *state.Network {
	for _, n := range a.nets {
		if n.Name == name {
			return n
		}
	}
	return nil
}

// scheduleScriptTick wakes the engine when its next timer is due, unless
// it already will by then.
func (a *App) scheduleScriptTick() tea.Cmd {
	if a.scripts == nil {
		return nil
	}
	next, ok := a.scripts.NextTimer()
	if !ok || !a.scriptTickAt.IsZero() && !a.scriptTickAt.After(next) {
		return nil
	}
	a.scriptTickAt = next
	return tea.Tick(max(time.Until(next), 0), func(time.Time) tea.Msg { return scriptTickMsg{} })
}

func (a *App) scriptTick() tea.Cmd {
	a.scriptTickAt = time.Time{}
	return a.runScript(a.scripts.Tick(a.scriptHost()))
}

// scriptEvent dispatches an event trout makes itself (on OPEN, on CLOSE,
// on NOTIFY, on CONNECT...): ^ handlers, then the others.
func (a *App) scriptEvent(ev script.Event) tea.Cmd {
	cmd, halted := a.scriptBefore(ev)
	ev.Halted = halted
	return tea.Batch(cmd, a.scriptAfter(ev))
}

// scriptBefore runs an event's ^ handlers, and reports whether they
// halted trout's default for it.
func (a *App) scriptBefore(ev script.Event) (tea.Cmd, bool) {
	if !a.scriptsOn() || !a.scripts.HasBefore(ev.Kind) {
		return nil, false
	}
	actions, halted := a.scripts.DispatchBefore(ev, a.scriptHost())
	return a.runScript(actions), halted
}

func (a *App) scriptAfter(ev script.Event) tea.Cmd {
	if !a.scriptsOn() {
		return nil
	}
	actions, _ := a.scripts.Dispatch(ev, a.scriptHost())
	return a.runScript(actions)
}

// handleIRC applies one message from network n to the UI state, with
// scripts' events around it: ^ handlers before trout shows it (which can
// hide what trout would say), the others after, so they see what it did.
// A CTCP's handlers run first, since their /halt stops trout's reply.
func (a *App) handleIRC(n *state.Network, m ircmsg.Message) tea.Cmd {
	a.noteIAL(n, m)
	if m.Command == "PART" || m.Command == "KICK" {
		defer a.pruneIAL(n)
	}
	if !a.scriptsOn() || !a.scriptLine(n, m) {
		return a.handleLine(n, m)
	}
	own := n.Same(m.Nick(), n.Nick)
	ev := script.EventFromMessage(n.Name, m, own)
	h := a.scriptHost()
	var cmds []tea.Cmd
	nick := m.Nick()
	newQuery := m.Command == "PRIVMSG" && ev.Kind == "TEXT" && !own && !n.IsChannel(ev.Target) &&
		n.Find(state.KQuery, nick) == nil
	open := script.Event{Kind: "OPEN", Network: n.Name, Nick: nick, Address: ev.Address, Target: nick, Text: ev.Text}
	if newQuery {
		// on ^OPEN can stop the query window opening, and then there's no
		// on TEXT either, like mIRC's.
		cmd, halted := a.scriptBefore(open)
		cmds = append(cmds, cmd)
		if halted {
			return tea.Batch(cmds...)
		}
	}
	if a.scripts.HasBefore(ev.Kind) {
		actions, halted := a.scripts.DispatchBefore(ev, h)
		cmds = append(cmds, a.runScript(actions))
		ev.Halted = halted
	}
	if ev.Kind == "CTCP" {
		actions, halted := a.scripts.Dispatch(ev, h)
		cmds = append(cmds, a.runScript(actions))
		a.ctcpHalted = halted
		cmds = append(cmds, a.handleLine(n, m))
		a.ctcpHalted = false
		return tea.Batch(cmds...)
	}
	a.hideLines = ev.Halted
	cmds = append(cmds, a.handleLine(n, m))
	a.hideLines = false
	if newQuery && n.Find(state.KQuery, nick) != nil {
		// A query window opened for this message: on OPEN, before on TEXT.
		cmds = append(cmds, a.scriptAfter(open))
	}
	actions, _ := a.scripts.Dispatch(ev, h)
	cmds = append(cmds, a.runScript(actions))
	if m.Command == "376" || m.Command == "422" {
		// The end of the MOTD: on CONNECT, like mIRC's.
		cmds = append(cmds, a.scriptEvent(script.Event{Kind: "CONNECT", Network: n.Name}))
	}
	return tea.Batch(cmds...)
}

// scriptLine reports whether scripts see a message: not the bouncer's own
// connection, history the bouncer replays, BouncerServ's replies, or
// someone you're ignoring (mIRC's ignore stops events too).
func (a *App) scriptLine(n *state.Network, m ircmsg.Message) bool {
	if n == a.home || m.Command == "BATCH" || strings.EqualFold(m.Nick(), "BouncerServ") {
		return false
	}
	if ok, id := m.GetTag("batch"); ok && a.batches[n.ID+" "+id] != nil {
		return false
	}
	if (m.Command == "PRIVMSG" || m.Command == "NOTICE") && a.ignored(n, m) {
		return false
	}
	return true
}

// noteIAL keeps the address list scripts see ($ial, $address, the Users
// list's addresses): everyone's nick!user@host from what they send and
// WHO replies, while they share a channel with us, like mIRC's.
func (a *App) noteIAL(n *state.Network, m ircmsg.Message) {
	if a.ial == nil {
		a.ial = map[*state.Network]map[string]string{}
	}
	ial := a.ial[n]
	if ial == nil {
		ial = map[string]string{}
		a.ial[n] = ial
	}
	nick := m.Nick()
	if strings.Contains(m.Source, "!") && strings.Contains(m.Source, "@") {
		ial[n.Fold(nick)] = m.Source
	}
	switch m.Command {
	case "NICK":
		if len(m.Params) > 0 {
			if old, ok := ial[n.Fold(nick)]; ok {
				delete(ial, n.Fold(nick))
				_, rest, _ := strings.Cut(old, "!")
				ial[n.Fold(m.Params[0])] = m.Params[0] + "!" + rest
			}
		}
	case "QUIT":
		delete(ial, n.Fold(nick))
	case "352": // RPL_WHOREPLY: me channel user host server nick ...
		if p := m.Params; len(p) >= 6 {
			ial[n.Fold(p[5])] = p[5] + "!" + p[2] + "@" + p[3]
		}
	}
}

// pruneIAL drops addresses of nicks no longer on any of our channels.
func (a *App) pruneIAL(n *state.Network) {
	for key := range a.ial[n] {
		on := false
		for _, w := range n.Wins {
			if w.Kind == state.KChannel && w.Joined && w.NickIndex(key) >= 0 {
				on = true
				break
			}
		}
		if !on && n.Find(state.KQuery, key) == nil {
			delete(a.ial[n], key)
		}
	}
}

// scriptHost answers the engine's questions about trout.
type scriptHost struct{ a *App }

func (a *App) scriptHost() script.Host { return scriptHost{a} }

func (h scriptHost) net(name string) *state.Network { return h.a.scriptNet(name) }

func (h scriptHost) Me(network string) string {
	if n := h.net(network); n != nil {
		return n.Nick
	}
	return ""
}

func (h scriptHost) Active() (string, string) {
	w := h.a.active
	if w.Kind == state.KStatus {
		return w.Net.Name, ""
	}
	return w.Net.Name, w.Name
}

func (h scriptHost) Nicks(network, channel string) []script.Nick {
	n := h.net(network)
	if n == nil {
		return nil
	}
	w := n.Find(state.KChannel, channel)
	if w == nil || !w.Joined {
		return nil
	}
	out := make([]script.Nick, len(w.Nicks))
	for i, nk := range w.Nicks {
		out[i] = script.Nick{Name: nk.Name, Prefix: nk.Prefix}
	}
	return out
}

func (h scriptHost) HasWindow(network, name string) bool {
	n := h.net(network)
	return n != nil && slices.ContainsFunc(n.Wins, func(w *state.Window) bool { return n.Same(w.Name, name) })
}

func (h scriptHost) windows(network string, kind state.Kind) []string {
	n := h.net(network)
	if n == nil {
		return nil
	}
	var out []string
	for _, w := range n.Wins {
		if w.Kind == kind && (kind != state.KChannel || w.Joined) {
			out = append(out, w.Name)
		}
	}
	return out
}

func (h scriptHost) Chans(network string) []string   { return h.windows(network, state.KChannel) }
func (h scriptHost) Queries(network string) []string { return h.windows(network, state.KQuery) }

func (h scriptHost) IAL(network string) []string {
	n := h.net(network)
	if n == nil {
		return nil
	}
	return slices.Sorted(maps.Values(h.a.ial[n]))
}

// Networks is the IRC networks, in trout's order: not the bouncer's own
// connection.
func (h scriptHost) Networks() []string {
	var out []string
	for _, n := range h.a.nets {
		if n != h.a.home {
			out = append(out, n.Name)
		}
	}
	return out
}

func (h scriptHost) Info(network string) script.NetInfo {
	n := h.net(network)
	if n == nil {
		return script.NetInfo{}
	}
	a := h.a
	info := script.NetInfo{
		Server:     cmp.Or(n.Attrs["host"], n.Server),
		Port:       n.Port,
		SSL:        n.Attrs["tls"] == "1",
		Connected:  a.conns[n] != nil && n.State() != "disconnected",
		Connecting: n.State() == "connecting",
		ISupport:   a.isupport[n],
		UserModes:  n.Umodes,
		Away:       a.away != "",
		AwayMsg:    a.away,
		Idle:       time.Since(a.lastTyped),
	}
	if a.opts.Demo {
		info.Connected = true
	}
	if p, _ := strconv.Atoi(n.Attrs["port"]); p > 0 {
		info.Port = p
	}
	if !info.Connected {
		info.Server = ""
	}
	return info
}

func (h scriptHost) ReadFile(name string) (string, bool) { return h.a.scriptReadFile(name) }
func (h scriptHost) Now() time.Time                      { return time.Now() }

// scriptMenu is the scripts' items for a popup (status, channel, query,
// nicklist or menubar), as trout's menu items.
func (a *App) scriptMenu(kind string, w *state.Window, params []string) []MenuItem {
	if !a.scriptsOn() {
		return nil
	}
	window := w.Name
	if w.Kind == state.KStatus {
		window = ""
	}
	items, actions := a.scripts.Popups(kind, w.Net.Name, window, params, a.scriptHost())
	a.queueScript(a.runScript(actions))
	var conv func([]script.MenuItem) []MenuItem
	conv = func(items []script.MenuItem) []MenuItem {
		var out []MenuItem
		for _, it := range items {
			m := MenuItem{Label: it.Label, Sep: it.Separator, Disabled: it.Disabled, Check: it.Checked}
			if len(it.Items) > 0 {
				m.Sub = conv(it.Items)
			} else if it.ID != "" {
				id := it.ID
				m.Action = func(a *App) tea.Cmd {
					return a.runScript(a.scripts.RunMenu(id, kind, w.Net.Name, window, params, a.scriptHost()))
				}
			}
			out = append(out, m)
		}
		return out
	}
	return conv(items)
}

// textMenu is the popup for right-clicking a window's text: the scripts'
// status, channel or query popup, like mIRC's.
func (a *App) textMenu(w *state.Window, x, y int) {
	kind := map[state.Kind]string{state.KStatus: "status", state.KChannel: "channel", state.KQuery: "query"}[w.Kind]
	if kind == "" {
		return
	}
	if items := a.scriptMenu(kind, w, nil); len(items) > 0 {
		a.closeMenus()
		a.pushMenu(items, x, y)
	}
}

// firstStatus is where trout reports on scripts: the bouncer's status
// window, or the demo's first network's.
func (a *App) firstStatus() *state.Window {
	if a.home != nil {
		return a.home.Status()
	}
	return a.nets[0].Status()
}

// scriptInput is on INPUT: scripts see what was typed in a window first,
// and can stop it (/halt or /haltdef), like mIRC's.
func (a *App) scriptInput(w *state.Window, line string) (tea.Cmd, bool) {
	if !a.scriptsOn() {
		return nil, false
	}
	ev := script.Event{Kind: "INPUT", Network: w.Net.Name, Nick: w.Net.Nick, Target: w.Name, Text: line}
	if w.Kind == state.KStatus {
		ev.Target = ""
	}
	if w.Kind == state.KChannel {
		ev.Chan = w.Name
	}
	actions, halted := a.scripts.Dispatch(ev, a.scriptHost())
	return a.runScript(actions), halted
}

// queueScript runs a script event's commands from somewhere that can't
// return them: Update sends them on.
func (a *App) queueScript(cmd tea.Cmd) {
	if cmd != nil {
		a.scriptCmds = append(a.scriptCmds, cmd)
	}
}

func (a *App) takeScriptCmds() tea.Cmd {
	cmds := a.scriptCmds
	a.scriptCmds = nil
	return tea.Batch(cmds...)
}

// scriptsExit is on EXIT, as trout quits: scripts can save what they
// need (their files and variables are written straight away).
func (a *App) scriptsExit() {
	a.scriptEvent(script.Event{Kind: "EXIT"})
}

// loadKinds are /load's switches for files that aren't scripts: aliases
// (-a) and popup items, like mIRC's.
var loadKinds = map[string]string{"-a": "aliases", "-ps": "status", "-pc": "channel",
	"-pq": "query", "-pn": "nicklist", "-pm": "menubar"}

// splitLoad reads a loaded script's entry in the config: a file name, or a
// /load switch and one, like "-a aliases.mrc". "-p popups.ini" is the
// Scripts Editor's popups.ini, all five popups (there's no /load -p).
func splitLoad(entry string) (as, file string) {
	if sw, f, ok := strings.Cut(entry, " "); ok && strings.HasPrefix(sw, "-") {
		if strings.EqualFold(sw, "-p") {
			return "popups", f
		}
		return loadKinds[strings.ToLower(sw)], f
	}
	return "", entry
}

// loadVarsOrUsers is /load -rv or -ru: mIRC's variables or Users file,
// added to trout's (its own are vars.ini and users.ini).
func (a *App) loadVarsOrUsers(file string, vars bool) tea.Cmd {
	data, err := a.readScript(file)
	if err != nil {
		a.addLine(a.active, state.RoleQuit, "* /load: couldn't read "+file+": "+err.Error())
		return nil
	}
	var lines []string
	for _, l := range mircList(data, vars) {
		if vars {
			lines = append(lines, "set -n "+l)
		} else {
			levels, rest, _ := strings.Cut(l, ":")
			lines = append(lines, "auser "+levels+" "+rest)
		}
	}
	var actions []script.Action
	for _, l := range lines {
		acts, _ := a.scripts.Command(a.active.Net.Name, "", l, false, a.scriptHost())
		actions = append(actions, acts...)
	}
	a.addLine(a.active, state.RoleInfo, "* Loaded "+strconv.Itoa(len(lines))+map[bool]string{true: " variables", false: " users"}[vars]+" from "+file)
	return a.runScript(actions)
}
