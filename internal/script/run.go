package script

import (
	"errors"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"
)

// run is one Dispatch, Alias or Init: the actions it collects and the
// steps it has left.
type run struct {
	e         *Engine
	h         Host
	actions   []Action
	steps     int
	stack     []*alias // aliases being run: an alias can't call itself, like mIRC
	unset     []string // global variables set with -u0, unset when it's done
	literal   int      // > 0: eval leaves text as it is (it's evaluated already)
	files     map[string]*pendingFile
	errText   string // $error: the error an :error label caught
	signals   []signal
	depth     int  // nested /signal -n calls
	quietCall bool // the alias about to be called was called with a .
}

// frame is an alias or event handler being run: its parameters, local
// variables and the event it's handling.
type frame struct {
	file    *file  // the script it's in: for -l aliases and $script
	ev      *Event // the event, also in aliases called from its handler
	network string
	window  string // where its commands run
	channel string // # and $chan
	params  []string
	locals  map[string]string
	typed   bool   // run from the editbox or a popup, not an event: /echo's default window
	ulevel  string // $ulevel: the user's level that let them trigger the event
	clevel  string // $clevel: the event's level
	menu    string // the popup being built or run: $menu
	home    string // the network before /scid, for /scid -r
	isid    bool   // called as $identifier
	quiet   bool   // called with a . in front: $show is $false
	prop    string // the identifier's .property
	v1, v2  string // the last if comparison's values
	result  string // the last alias call's /return value
}

// Control flow, passed up as errors until something handles it.
var (
	errHalt     = errors.New("halt")
	errBreak    = errors.New("break")
	errContinue = errors.New("continue")
	errStop     = errors.New("stop") // $$1 was empty: end quietly
	errNull     = errors.New("null") // an identifier's arguments make it $null
)

type returnErr struct{ value string }
type gotoErr struct{ label string }

// scriptErr is a script error, shown like mIRC's "* /cmd: problem (line
// 3, script.mrc)", which ends the handler.
type scriptErr struct {
	msg  string
	line int
	file string
}

// text is the error as mIRC shows it, and as $error has it.
func (e *scriptErr) text() string {
	where := ""
	if e.line > 0 {
		where = " (line " + itoa(e.line) + ", " + e.file + ")"
	}
	return "* " + e.msg + where
}

func (e *returnErr) Error() string { return "return" }
func (e *gotoErr) Error() string   { return "goto " + e.label }
func (e *scriptErr) Error() string { return e.msg }

func errorf(parts ...string) error { return &scriptErr{msg: strings.Join(parts, "")} }

func (e *Engine) newRun(h Host) *run { return &run{e: e, h: h} }

// finish returns the actions, with a Save if global variables changed.
// Signals sent without -n run first, now that the scripts that sent them
// are done.
func (r *run) finish() []Action {
	for n := 0; len(r.signals) > 0 && n < 1000; n++ {
		s := r.signals[0]
		r.signals = r.signals[1:]
		r.dispatchSignal(s)
	}
	for _, name := range r.unset {
		delete(r.e.vars, name)
	}
	if r.e.dirty || r.e.usersDirty {
		s := &Save{DLevel: r.e.dlevel}
		if r.e.dirty {
			s.Vars = r.e.Vars()
		}
		if r.e.usersDirty {
			s.Users = append([]string{}, r.e.Users()...)
		}
		r.e.dirty, r.e.usersDirty = false, false
		r.actions = append(r.actions, Action{Save: s})
	}
	return r.actions
}

// runBody runs an event handler's body and reports whether it /halted.
func (r *run) runBody(fr *frame, body []stmt) bool {
	return r.report(fr, r.catching(fr, body))
}

// catching runs an alias's or handler's body. A script error jumps to its
// :error label, if it has one, with the error in $error, like mIRC. The
// script goes on from there; unless it does /reseterror, the error then
// goes on to the calling alias, until one has an :error or it's shown.
func (r *run) catching(fr *frame, body []stmt) error {
	err := r.block(fr, body)
	var se *scriptErr
	if !errors.As(err, &se) {
		return err
	}
	i := slices.IndexFunc(body, func(s stmt) bool {
		l, ok := s.(*labelStmt)
		return ok && l.name == "error"
	})
	if i < 0 {
		return err
	}
	r.errText = se.text()
	if err := r.blockFrom(fr, body, i+1); err != nil {
		return err
	}
	if r.errText != "" {
		return err
	}
	return nil
}

// report shows a handler's error, if any, and reports whether it halted.
func (r *run) report(fr *frame, err error) bool {
	var se *scriptErr
	var ge *gotoErr
	switch {
	case err == errHalt:
		return true
	case errors.As(err, &se):
		r.actions = append(r.actions, Action{Echo: &Echo{Network: fr.network, Color: -1, Text: se.text()}})
	case errors.As(err, &ge):
		r.actions = append(r.actions, Action{Echo: &Echo{Network: fr.network, Color: -1, Text: "* /goto: '" + ge.label + "' not found"}})
	}
	return false
}

func (r *run) step() error {
	r.steps++
	if r.steps > r.e.opts.MaxSteps {
		return errorf("script stopped: it ran more than ", itoa(r.e.opts.MaxSteps), " steps")
	}
	return nil
}

// block runs statements, handling /goto to labels in it.
func (r *run) block(fr *frame, stmts []stmt) error {
	return r.blockFrom(fr, stmts, 0)
}

// blockFrom runs statements from stmts[start].
func (r *run) blockFrom(fr *frame, stmts []stmt, start int) error {
	for i := start; i < len(stmts); i++ {
		err := r.exec(fr, stmts[i])
		var ge *gotoErr
		if errors.As(err, &ge) {
			if j := slices.IndexFunc(stmts, func(s stmt) bool {
				l, ok := s.(*labelStmt)
				return ok && l.name == ge.label
			}); j >= 0 {
				i = j
				continue
			}
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *run) exec(fr *frame, s stmt) error {
	if err := r.step(); err != nil {
		return r.at(fr, s, err)
	}
	switch s := s.(type) {
	case *cmdStmt:
		return r.at(fr, s, r.command(fr, s.text))
	case *ifStmt:
		for _, b := range s.branches {
			if b.cond != "" {
				ok, err := r.cond(fr, b.cond)
				if err != nil {
					return r.at(fr, s, err)
				}
				if !ok {
					continue
				}
			}
			return r.block(fr, b.body)
		}
	case *whileStmt:
		for {
			ok, err := r.cond(fr, s.cond)
			if err != nil {
				return r.at(fr, s, err)
			}
			if !ok {
				return nil
			}
			switch err := r.block(fr, s.body); err {
			case nil, errContinue:
			case errBreak:
				return nil
			default:
				return err
			}
			if err := r.step(); err != nil {
				return r.at(fr, s, err)
			}
		}
	}
	return nil
}

// at adds where a script error happened, if it doesn't say yet.
func (r *run) at(fr *frame, s stmt, err error) error {
	var se *scriptErr
	if errors.As(err, &se) && se.line == 0 {
		se.line = s.line()
		if fr.file != nil {
			se.file = fr.file.name
		}
	}
	return err
}

// command runs one command line.
func (r *run) command(fr *frame, text string) error {
	if t := strings.TrimSpace(text); t == "=" || t == "!" {
		// An event whose commands are = or ! is there to stop users from
		// getting lower levels' events, like mIRC's: it does nothing.
		return nil
	}
	text = strings.TrimSpace(strings.TrimLeft(text, "/"))
	words := splitWords(text)
	if len(words) == 0 {
		return nil
	}
	word := words[0]
	rest := strings.TrimSpace(text[len(word):])
	quiet, noAlias := false, false
	name := word
	for name != "" && (name[0] == '.' || name[0] == '!') {
		if name[0] == '.' {
			quiet = true
		} else {
			noAlias = true
		}
		name = name[1:]
	}
	if strings.HasPrefix(name, "%") && (rest == "=" || strings.HasPrefix(rest, "= ")) {
		return r.assign(fr, name[1:], strings.TrimSpace(strings.TrimPrefix(rest, "=")))
	}
	switch strings.ToLower(name) {
	case "set":
		return r.cmdSet(fr, rest)
	case "var":
		return r.cmdVar(fr, rest)
	case "unset":
		return r.cmdUnset(fr, rest)
	case "unsetall":
		if len(r.e.vars) > 0 {
			clear(r.e.vars)
			r.e.dirty = true
		}
		return nil
	case "inc", "dec":
		return r.cmdInc(fr, rest, strings.EqualFold(name, "dec"))
	case "halt":
		return errHalt
	case "haltdef":
		// trout always shows its default text, so there's nothing to halt.
		return nil
	case "return":
		v, err := r.eval(fr, rest)
		if err != nil {
			return err
		}
		return &returnErr{v}
	case "break":
		return errBreak
	case "continue":
		return errContinue
	case "goto":
		v, err := r.eval(fr, rest)
		if err != nil {
			return err
		}
		return &gotoErr{strings.ToLower(v)}
	case "signal":
		return r.cmdSignal(fr, rest)
	case "auser", "guser", "ruser", "iuser", "rlevel", "flush", "ulist", "dlevel":
		return r.cmdUsers(fr, strings.ToLower(name), rest)
	case "scid", "scon":
		return r.cmdScid(fr, strings.ToLower(name), rest)
	case "ial", "ialclear", "ialfill", "ialmark":
		// The client keeps its address list itself.
		_, err := r.eval(fr, rest)
		return err
	case "reseterror":
		r.errText = ""
		return nil
	case "noop":
		_, err := r.eval(fr, rest)
		return err
	case "echo":
		return r.cmdEcho(fr, rest)
	case "enable", "disable":
		return r.cmdGroups(fr, rest, strings.EqualFold(name, "enable"))
	case "tokenize":
		return r.cmdTokenize(fr, rest)
	case "hmake", "hfree", "hadd", "hdel", "hinc", "hdec", "hsave", "hload":
		return r.cmdHash(fr, strings.ToLower(name), rest)
	case "write":
		return r.cmdWrite(fr, rest)
	case "writeini":
		return r.cmdWriteini(fr, rest)
	case "remini":
		return r.cmdRemini(fr, rest)
	case "flushini":
		// INI files are written straight away: there's nothing to flush.
		_, err := r.eval(fr, rest)
		return err
	case "remove":
		return r.cmdRemove(fr, rest)
	case "copy", "rename":
		return r.cmdCopy(fr, rest, strings.EqualFold(name, "rename"))
	case "fopen", "fclose", "fwrite", "fseek":
		return r.cmdFile(fr, strings.ToLower(name), rest)
	}
	if id, ok := cutPrefixFold(name, "timer"); ok {
		return r.cmdTimer(fr, id, rest)
	}

	var args string
	if strings.HasPrefix(name, "$") || strings.HasPrefix(name, "%") {
		// The command itself is computed: $iif(%loud,msg,notice) $nick hi.
		v, err := r.evalWord(fr, name)
		if err != nil {
			return err
		}
		if args, err = r.eval(fr, rest); err != nil {
			return err
		}
		var more string
		name, more, _ = strings.Cut(v, " ")
		args = strings.TrimSpace(more + " " + args)
		if name == "" {
			return nil
		}
		if engineCommand(name) {
			// One of the engine's own, like set or echo: run it as one, with
			// its arguments as they are. mIRC evaluates a line once; doing it
			// again would run $identifiers in what someone else said.
			r.literal++
			defer func() { r.literal-- }()
			return r.command(fr, name+" "+args)
		}
	} else {
		var err error
		if args, err = r.eval(fr, rest); err != nil {
			return err
		}
	}
	if !noAlias {
		if a := r.e.alias(fr.file, name); a != nil && !slices.Contains(r.stack, a) {
			r.quietCall = quiet
			v, err := r.callAlias(fr, a, fields(args), false, "")
			fr.result = v
			return err
		}
	}
	cmd := "/"
	if quiet {
		cmd += "."
	}
	cmd += name
	if args != "" {
		cmd += " " + args
	}
	r.actions = append(r.actions, Action{Command: cmd, Network: fr.network, Window: fr.window})
	return nil
}

// callAlias runs an alias from fr and returns its /return value.
func (r *run) callAlias(fr *frame, a *alias, params []string, isid bool, prop string) (string, error) {
	child := &frame{file: a.file, ev: fr.ev, network: fr.network, window: fr.window, channel: fr.channel,
		params: params, typed: fr.typed, isid: isid, prop: prop, quiet: r.quietCall}
	r.quietCall = false
	r.stack = append(r.stack, a)
	defer func() { r.stack = r.stack[:len(r.stack)-1] }()
	err := r.catching(child, a.body)
	var re *returnErr
	var ge *gotoErr
	switch {
	case errors.As(err, &re):
		return re.value, nil
	case errors.As(err, &ge):
		return "", errorf("/goto: '", ge.label, "' not found")
	case err == errBreak, err == errContinue:
		return "", nil
	}
	return "", err
}

// runAlias runs an alias called from the editbox.
func (r *run) runAlias(fr *frame, a *alias) {
	_, err := r.callAlias(fr, a, fr.params, false, "")
	r.report(fr, err)
}

// Variables.

// getVar is a variable's value: a local one if there is one, like mIRC.
func (r *run) getVar(fr *frame, name string) string {
	key := strings.ToLower(name)
	if v, ok := fr.locals[key]; ok {
		return v
	}
	if v := r.e.vars[key]; v != nil {
		return v.value
	}
	return ""
}

func (r *run) setGlobal(name, value string, temp bool) {
	key := strings.ToLower(name)
	v := r.e.vars[key]
	if v == nil {
		v = &variable{name: name}
		r.e.vars[key] = v
	}
	if v.value != value || v.temp != temp {
		v.value, v.temp = value, temp
		r.e.dirty = true
	}
}

func (r *run) setLocal(fr *frame, name, value string) {
	if fr.locals == nil {
		fr.locals = map[string]string{}
	}
	fr.locals[strings.ToLower(name)] = value
}

// setVar sets a local variable if one exists, or a global one.
func (r *run) setVar(fr *frame, name, value string, temp bool) {
	if _, ok := fr.locals[strings.ToLower(name)]; ok {
		r.setLocal(fr, name, value)
		return
	}
	r.setGlobal(name, value, temp)
}

// assign handles %x = value, which can do one math operation: %x = %y + 1.
func (r *run) assign(fr *frame, name, value string) error {
	v, err := r.eval(fr, value)
	if err != nil {
		return err
	}
	r.setVar(fr, name, mathOp(v), r.isTemp(name))
	r.expiry(fr, name, "")
	return nil
}

// isTemp reports whether a global variable was set with -e (forgotten on
// exit, so not saved): changing its value doesn't change that.
func (r *run) isTemp(name string) bool {
	v := r.e.vars[strings.ToLower(name)]
	return v != nil && v.temp
}

// mathOp does the single operation mIRC allows in an assignment, "5 + 1",
// on the evaluated value; anything else is kept as text.
func mathOp(v string) string {
	w := strings.Fields(v)
	if len(w) != 3 || len(w[1]) != 1 || !strings.Contains("+-*/%^", w[1]) {
		return v
	}
	a, ok1 := parseNum(w[0])
	b, ok2 := parseNum(w[2])
	if !ok1 || !ok2 {
		return v
	}
	return formatNum(calcOp(w[1][0], a, b))
}

// switches splits leading -switches off a command's arguments.
func switches(rest string) (sw, after string) {
	for strings.HasPrefix(rest, "-") {
		w, more, _ := strings.Cut(rest, " ")
		sw += w[1:]
		rest = strings.TrimSpace(more)
	}
	return sw, rest
}

// varName reads a %variable name at the start of rest. The name can be
// built with evaluation brackets, like mIRC's "set %a [ $+ $nick ] 1".
func (r *run) varName(fr *frame, rest string) (name, after string, err error) {
	words := strings.Fields(rest)
	if len(words) == 0 || !strings.HasPrefix(words[0], "%") {
		return "", "", errorf("invalid parameters")
	}
	name = words[0][1:]
	i := 1
	for i < len(words) && words[i] == "[" && r.literal == 0 {
		j := closeBracket(words, i)
		v, _, _, err := r.bracket(fr, words[i+1:j])
		if err != nil {
			return "", "", err
		}
		name += v
		i = min(j+1, len(words))
	}
	return name, strings.Join(words[i:], " "), nil
}

// cmdSet is /set [-switches] %var [value].
func (r *run) cmdSet(fr *frame, rest string) error {
	sw, rest := switches(rest)
	name, value, err := r.varName(fr, rest)
	if err != nil {
		return errorf("/set: ", err.Error())
	}
	return r.setWith(fr, sw, name, value, strings.Contains(sw, "l"))
}

// setWith sets a variable with /set's switches: -l local, -g global, -n
// no math, -i only if unset, -e forgotten on exit (not saved), -u0 unset
// when the script ends.
func (r *run) setWith(fr *frame, sw, name, value string, local bool) error {
	v, err := r.eval(fr, value)
	if err != nil {
		return err
	}
	if !strings.Contains(sw, "n") {
		v = mathOp(v)
	}
	key := strings.ToLower(name)
	if strings.Contains(sw, "i") {
		if _, ok := fr.locals[key]; ok {
			return nil
		}
		if _, ok := r.e.vars[key]; ok && !local {
			return nil
		}
	}
	temp := strings.Contains(sw, "e") || strings.Contains(sw, "u")
	switch {
	case local:
		r.setLocal(fr, name, v)
	case strings.Contains(sw, "g"):
		r.setGlobal(name, v, temp)
	default:
		r.setVar(fr, name, v, temp)
	}
	if !local {
		r.expiry(fr, name, sw)
	}
	return nil
}

// expiry applies -uN to a global variable just set: -u0 unsets it when
// the script ends, -uN after N seconds. Setting it again without -u
// cancels that, like mIRC, unless -k keeps it.
func (r *run) expiry(fr *frame, name, sw string) {
	key := strings.ToLower(name)
	if _, local := fr.locals[key]; local {
		return
	}
	v := r.e.vars[key]
	if v == nil {
		return
	}
	n, u := switchNum(sw, 'u')
	switch {
	case u && n == 0:
		r.unset = append(r.unset, key)
	case u:
		v.expires = r.h.Now().Add(time.Duration(n) * time.Second)
	case !strings.Contains(sw, "k"):
		v.expires = time.Time{}
	}
}

// cmdVar is /var %x = 1, %y = 2: local variables, separated by commas.
func (r *run) cmdVar(fr *frame, rest string) error {
	sw, rest := switches(rest)
	local := !strings.Contains(sw, "g")
	for _, decl := range splitVarDecls(rest) {
		name, value, err := r.varName(fr, decl)
		if err != nil {
			return errorf("/var: ", err.Error())
		}
		value = strings.TrimSpace(strings.TrimPrefix(value, "="))
		if err := r.setWith(fr, sw, name, value, local); err != nil {
			return err
		}
	}
	return nil
}

// splitVarDecls splits /var's declarations at ", %" outside parentheses.
func splitVarDecls(s string) []string {
	var out []string
	depth, start := 0, 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth = max(depth-1, 0)
		case ',':
			if depth == 0 && strings.HasPrefix(strings.TrimLeft(s[i+1:], " "), "%") {
				out = append(out, strings.TrimSpace(s[start:i]))
				start = i + 1
			}
		}
	}
	return append(out, strings.TrimSpace(s[start:]))
}

// cmdUnset is /unset [-l|-g] %a %b*: wildcards remove every match.
func (r *run) cmdUnset(fr *frame, rest string) error {
	sw, rest := switches(rest)
	for rest != "" {
		// Names can be built with brackets too: unset %a [ $+ b ].
		name, after, err := r.varName(fr, rest)
		if err != nil {
			return errorf("/unset: ", err.Error())
		}
		rest = after
		pattern := strings.ToLower(name)
		if !strings.Contains(sw, "g") {
			maps.DeleteFunc(fr.locals, func(k, _ string) bool { return wildMatch(pattern, k, false) })
		}
		if !strings.Contains(sw, "l") {
			n := len(r.e.vars)
			maps.DeleteFunc(r.e.vars, func(k string, _ *variable) bool { return wildMatch(pattern, k, false) })
			if len(r.e.vars) != n {
				r.e.dirty = true
			}
		}
	}
	return nil
}

// cmdInc is /inc and /dec [-switches] %var [value], 1 by default.
func (r *run) cmdInc(fr *frame, rest string, dec bool) error {
	sw, rest := switches(rest)
	name, value, err := r.varName(fr, rest)
	if err != nil {
		return errorf("/inc: ", err.Error())
	}
	by := 1.0
	if value != "" {
		v, err := r.eval(fr, value)
		if err != nil {
			return err
		}
		by, _ = parseNum(v)
	}
	if dec {
		by = -by
	}
	v := r.getVar(fr, name)
	cur, ok := parseNum(v)
	if !ok && v != "" {
		// Like mIRC, text isn't a number to add to: it's left as it is.
		return nil
	}
	r.setVar(fr, name, formatNum(cur+by), strings.ContainsAny(sw, "eu") || r.isTemp(name))
	r.expiry(fr, name, sw)
	return nil
}

// cmdEcho is /echo [color] [-switches] [window] text. With -a it
// prints in the active window, with -s in status. Without either or a
// window, mIRC (though its help doesn't say) prints in the window it was
// typed in, or from an event, in the network's status window.
func (r *run) cmdEcho(fr *frame, rest string) error {
	line, err := r.eval(fr, rest)
	if err != nil {
		return err
	}
	words := strings.Fields(line)
	color, sw := -1, ""
	// The color comes first, then the switches: "echo 4 -a text".
	if len(words) > 1 && isDigits(words[0]) {
		color, words = atoi(words[0]), words[1:]
	}
	for len(words) > 0 && strings.HasPrefix(words[0], "-") && len(words[0]) > 1 {
		sw += words[0][1:]
		words = words[1:]
	}
	net, win := fr.network, ""
	switch {
	case strings.Contains(sw, "s"):
	case strings.Contains(sw, "a"):
		net, win = r.h.Active()
	case len(words) > 1 && (isChannel(words[0]) || strings.HasPrefix(words[0], "=") || r.h.HasWindow(fr.network, words[0])):
		net, win = fr.network, words[0]
		words = words[1:]
	case fr.typed:
		win = fr.window
	}
	if len(words) == 0 {
		return errorf("/echo: insufficient parameters")
	}
	r.actions = append(r.actions, Action{Echo: &Echo{Network: net, Window: win, Color: color, Text: strings.Join(words, " ")}})
	return nil
}

// cmdGroups is /enable and /disable #group, with wildcards.
func (r *run) cmdGroups(fr *frame, rest string, on bool) error {
	line, err := r.eval(fr, rest)
	if err != nil {
		return err
	}
	for w := range strings.FieldsSeq(line) {
		pattern := strings.ToLower(strings.TrimPrefix(w, "#"))
		for name := range r.e.groups {
			if wildMatch(pattern, name, false) {
				r.e.groups[name] = on
			}
		}
	}
	return nil
}

// cmdTokenize is /tokenize C text: it sets $1- to text split at the
// character with code C.
func (r *run) cmdTokenize(fr *frame, rest string) error {
	line, err := r.eval(fr, rest)
	if err != nil {
		return err
	}
	c, text, _ := strings.Cut(line, " ")
	sep, ok := charCode(c)
	if !ok {
		return errorf("/tokenize: invalid parameters")
	}
	fr.params = tokensOf(text, sep)
	return nil
}

// fields splits text into $1-style parameters.
func fields(s string) []string { return strings.Fields(s) }

func itoa(n int) string { return strconv.Itoa(n) }

// atoi is n, or 0 if s isn't a number.
func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// cutPrefixFold is strings.CutPrefix, ignoring case.
func cutPrefixFold(s, prefix string) (string, bool) {
	if len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix) {
		return s[len(prefix):], true
	}
	return s, false
}

// engineCommand reports whether the engine runs a command itself, rather
// than handing it to the client.
func engineCommand(name string) bool {
	switch strings.ToLower(strings.TrimLeft(name, "/.!")) {
	case "set", "var", "unset", "unsetall", "inc", "dec", "halt", "haltdef", "return", "break",
		"continue", "goto", "noop", "echo", "enable", "disable", "tokenize", "hmake", "hfree",
		"hadd", "hdel", "hinc", "hdec", "hsave", "hload", "write", "reseterror", "signal",
		"writeini", "remini", "flushini", "remove", "copy", "rename", "fopen", "fclose", "fwrite", "fseek",
		"scid", "scon", "ial", "ialclear", "ialfill", "ialmark",
		"auser", "guser", "ruser", "iuser", "rlevel", "flush", "ulist", "dlevel":
		return true
	}
	_, ok := cutPrefixFold(strings.TrimLeft(name, "/.!"), "timer")
	return ok
}

// signal is a /signal waiting for the scripts running now to finish.
type signal struct {
	from *file // the script that sent it, which hears it first
	ev   Event
}

// cmdSignal is /signal [-n] <name> [parameters]: on SIGNAL events in all
// scripts, after the scripts running now finish, or with -n right now.
func (r *run) cmdSignal(fr *frame, rest string) error {
	line, err := r.eval(fr, rest)
	if err != nil {
		return err
	}
	sw, line := switches(line)
	name, params, _ := strings.Cut(line, " ")
	if name == "" {
		return errorf("/signal: insufficient parameters")
	}
	s := signal{from: fr.file, ev: Event{Kind: "SIGNAL", Network: fr.network, Target: name, Text: params}}
	if !strings.Contains(sw, "n") {
		r.signals = append(r.signals, s)
		return nil
	}
	if r.depth >= 50 {
		return errorf("/signal: too many signals inside each other")
	}
	r.depth++
	defer func() { r.depth-- }()
	r.dispatchSignal(s)
	return nil
}

// dispatchSignal runs on SIGNAL in the sending script, then the others.
func (r *run) dispatchSignal(s signal) {
	order := slices.Clone(r.e.scripts)
	if i := slices.Index(order, s.from); i > 0 {
		order = append([]*file{s.from}, slices.Delete(order, i, i+1)...)
	}
	for _, f := range order {
		r.dispatchFile(f, &s.ev)
	}
}
