package script

import (
	"regexp"
	"strings"
)

// file is a loaded script.
type file struct {
	name    string
	aliases map[string]*alias // by lower-case name
	events  []*event
	groups  []group
	popups  []*popup
}

type alias struct {
	name  string
	local bool // alias -l: only this script can call it
	file  *file
	body  []stmt
}

// event is an on or ctcp definition.
type event struct {
	kind  string // TEXT, JOIN... or CTCP for a ctcp section
	level int
	any   bool   // the * level
	exact bool   // + prefix: only users with exactly this level
	named bool   // a named level, like friend
	me    bool   // me: our own events only
	notMe bool   // ! prefix: not our own events
	op    bool   // @ prefix: only when we're an op on the channel
	regex bool   // $ prefix: match is a regular expression
	name  string // a named level, like friend
	cmp   string // <, >, <=, >=, <> or =: compare the actor's and target's levels
	text  string // the level as written, for $clevel
	match string
	loc   string
	group string // the #group it's in, or ""
	body  []stmt
}

type group struct {
	name string
	on   bool
}

// srcLine is a script line and its line number in the file, for error
// messages that say where, like mIRC's "(line 12, script.mrc)".
type srcLine struct {
	n    int
	text string
}

type parser struct {
	f        *file
	lines    []srcLine
	i        int
	group    string
	problems []Problem
}

func (p *parser) problem(line int, fatal bool, text string) {
	p.problems = append(p.problems, Problem{Line: line, Text: text, Fatal: fatal})
}

// parseFile parses a script file: alias, on and ctcp sections, #groups,
// and the old [script]/[aliases] ini format mIRC used to save them in.
func parseFile(name, src string) (*file, []Problem) {
	p := &parser{f: &file{name: name, aliases: map[string]*alias{}}}
	p.lines, p.problems = sourceLines(src)
	aliasFile := false
	for p.i < len(p.lines) {
		l := p.lines[p.i]
		p.i++
		switch t := l.text; {
		case t == "[aliases]":
			aliasFile = true
		case t == "[script]":
			aliasFile = false
		case strings.HasPrefix(t, "["):
			p.problem(l.n, false, t+" sections aren't supported; only [script] and [aliases]")
			p.skipIni()
		case strings.HasPrefix(t, "#"):
			p.groupLine(l)
		case aliasFile:
			p.aliasDef(l, strings.TrimLeft(t, "/"))
		default:
			p.section(l)
		}
	}
	if p.group != "" {
		p.problem(0, false, "#"+p.group+" has no #"+p.group+" end")
	}
	return p.f, p.problems
}

// iniHeader is a section line of the old ini format, like [script].
var iniHeader = regexp.MustCompile(`^\[[A-Za-z]+\]$`)

// sourceLines splits a script into lines: it drops comments and blank
// lines, joins lines ending in $&, and strips the nN= of ini-format files.
func sourceLines(src string) ([]srcLine, []Problem) {
	var lines []srcLine
	var problems []Problem
	inComment, ini := false, false
	var cont *srcLine
	for n, t := range strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n") {
		n++
		t = strings.TrimSpace(t)
		if iniHeader.MatchString(t) {
			ini = true
			lines = append(lines, srcLine{n, strings.ToLower(t)})
			continue
		}
		if ini {
			if k, v, ok := strings.Cut(t, "="); ok && len(k) > 1 && k[0] == 'n' && isDigits(k[1:]) {
				t = strings.TrimSpace(v)
			}
		}
		switch {
		case inComment:
			inComment = !strings.HasSuffix(t, "*/")
			continue
		case strings.HasPrefix(t, "/*"):
			inComment = !strings.HasSuffix(t, "*/") || t == "/*"
			continue
		case t == "" || strings.HasPrefix(t, ";"):
			continue
		}
		if cont != nil {
			cont.text += " " + t
		} else {
			lines = append(lines, srcLine{n, t})
			cont = &lines[len(lines)-1]
		}
		if more, ok := strings.CutSuffix(cont.text, "$&"); ok && (more == "" || strings.HasSuffix(more, " ")) {
			cont.text = strings.TrimSpace(more)
		} else {
			cont = nil
		}
	}
	if inComment {
		problems = append(problems, Problem{Text: "a /* comment has no */"})
	}
	return lines, problems
}

// skipIni skips the rest of an unsupported ini section.
func (p *parser) skipIni() {
	for p.i < len(p.lines) && !strings.HasPrefix(p.lines[p.i].text, "[") {
		p.i++
	}
}

// groupLine handles "#name on", "#name off" and "#name end".
func (p *parser) groupLine(l srcLine) {
	w := strings.Fields(l.text)
	name := strings.TrimPrefix(w[0], "#")
	state := ""
	if len(w) > 1 {
		state = strings.ToLower(w[1])
	}
	switch {
	case state == "end":
		if !strings.EqualFold(name, p.group) {
			p.problem(l.n, false, "#"+name+" end without #"+name)
		}
		p.group = ""
	case p.group != "":
		p.problem(l.n, false, "#"+name+" is inside #"+p.group+"; groups can't be nested")
	case state == "on" || state == "off":
		p.group = name
		p.f.groups = append(p.f.groups, group{name: name, on: state == "on"})
	default:
		p.problem(l.n, false, "#"+name+" needs on, off or end")
	}
}

func (p *parser) section(l srcLine) {
	kw, rest, _ := strings.Cut(l.text, " ")
	switch strings.ToLower(kw) {
	case "alias":
		p.aliasDef(l, rest)
	case "on":
		p.onDef(l, rest)
	case "ctcp":
		f := splitFields(rest, 4)
		if len(f) < 4 {
			p.problem(l.n, false, "a ctcp event needs level:matchtext:location:commands")
			p.body(l, rest)
			return
		}
		ev := &event{kind: "CTCP", match: f[1], loc: f[2]}
		if p.level(l, ev, f[0]) {
			ev.body = p.stmts(p.body(l, f[3]))
			p.addEvent(ev)
		} else {
			p.body(l, f[3])
		}
	case "raw":
		// raw <numeric>:<matchtext>:<commands>, with no level.
		f := splitFields(rest, 3)
		if len(f) < 3 {
			p.problem(l.n, false, "a raw event needs numeric:matchtext:commands")
			p.body(l, afterColons(rest, 2))
			return
		}
		ev := &event{kind: "RAW", any: true, level: 1, loc: strings.TrimSpace(f[0]), match: f[1]}
		ev.body = p.stmts(p.body(l, f[2]))
		p.addEvent(ev)
	case "menu":
		p.menuDef(l, rest)
	case "dialog":
		p.problem(l.n, true, "dialogs aren't supported: they need Windows")
		p.body(l, strings.TrimSpace(strings.TrimPrefix(rest, strings.Fields(rest + " x")[0])))
	default:
		p.problem(l.n, false, "not an alias, on or ctcp definition: "+l.text)
	}
}

func (p *parser) aliasDef(l srcLine, rest string) {
	local := false
	w := strings.Fields(rest)
	if len(w) > 0 && strings.EqualFold(w[0], "-l") {
		local = true
		w = w[1:]
	}
	if len(w) == 0 {
		p.problem(l.n, false, "an alias needs a name")
		return
	}
	name := strings.TrimLeft(w[0], "/")
	body := p.stmts(p.body(l, strings.Join(w[1:], " ")))
	key := strings.ToLower(name)
	if p.f.aliases[key] != nil {
		// Like mIRC, the first definition wins.
		p.problem(l.n, false, "alias "+name+" is defined twice; the first one is used")
		return
	}
	p.f.aliases[key] = &alias{name: name, local: local, file: p.f, body: body}
}

func (p *parser) onDef(l srcLine, rest string) {
	me := false
	if lvl, after, ok := strings.Cut(rest, ":"); ok && strings.EqualFold(strings.TrimSpace(lvl), "me") {
		me, rest = true, after
	}
	head := splitFields(rest, 3)
	if len(head) < 2 {
		p.problem(l.n, false, "an on event needs level:event:...:commands")
		return
	}
	kind := strings.ToUpper(strings.TrimSpace(head[1]))
	n, ok := eventFields[kind]
	if kind == "OPEN" {
		// on OPEN has matchtext only for queries (? or *): on OPEN:=: doesn't.
		if loc := strings.TrimSpace(splitFields(rest, 4)[min(2, len(splitFields(rest, 4))-1)]); loc != "?" && loc != "*" {
			n = 1
		}
	}
	if !ok {
		p.problem(l.n, false, "on "+kind+" isn't supported")
		p.body(l, afterColons(rest, 0))
		return
	}
	f := splitFields(rest, 3+n)
	if len(f) < 3+n {
		p.problem(l.n, false, "on "+kind+" is missing a field")
		p.body(l, afterColons(rest, 0))
		return
	}
	ev := &event{kind: kind, me: me}
	switch {
	case n == 2 && kind == "OPEN":
		ev.loc, ev.match = f[2], f[3]
	case n == 2:
		ev.match, ev.loc = f[2], f[3]
	case n == 1 && matchOnly[kind]:
		ev.match = f[2]
	case n == 1:
		ev.loc = f[2]
	}
	if !p.level(l, ev, f[0]) {
		p.body(l, f[2+n])
		return
	}
	ev.body = p.stmts(p.body(l, f[2+n]))
	p.addEvent(ev)
}

func (p *parser) addEvent(ev *event) {
	ev.group = p.group
	p.f.events = append(p.f.events, ev)
}

// level parses an event's level and prefixes, like "@1", "!*" or "+5". It
// reports false, with a Problem, for ones the engine can't run.
func (p *parser) level(l srcLine, ev *event, s string) bool {
	s = strings.TrimSpace(s)
	for s != "" {
		switch s[0] {
		case '^':
			p.problem(l.n, false, "^ events (replacing the default text) aren't supported")
			return false
		case '$':
			ev.regex = true
		case '<', '>', '=':
			ev.cmp += s[:1]
		case '&':
			// Only skip the event if default text was halted: trout's never is.
		case '!':
			ev.notMe = true
		case '@':
			ev.op = true
		case '+':
			ev.exact = true
		default:
			ev.text = s
			switch {
			case s == "*":
				ev.any, ev.level = true, 1
			case isDigits(s):
				ev.level = atoi(s)
			default:
				// A named level is specific: it ranks above numbered ones
				// when choosing the event that runs.
				ev.named, ev.name, ev.level = true, s, 1<<20
			}
			return true
		}
		s = s[1:]
	}
	p.problem(l.n, false, "an event needs a level, like * or 1")
	return false
}

// body collects a definition's commands: the rest of its line, or a
// { } block that may go on for several lines.
func (p *parser) body(l srcLine, first string) []srcLine {
	first = strings.TrimSpace(first)
	if first != "{" && !strings.HasPrefix(first, "{ ") {
		if first == "" {
			return nil
		}
		return []srcLine{{l.n, first}}
	}
	var out []srcLine
	depth := 0
	add := func(line srcLine) bool {
		var kept []string
		for _, w := range splitWords(line.text) {
			depth += braceDelta(w)
			if depth == 0 {
				if len(kept) > 0 {
					out = append(out, srcLine{line.n, strings.Join(kept, " ")})
				}
				return true
			}
			kept = append(kept, w)
		}
		if len(kept) > 0 {
			out = append(out, srcLine{line.n, strings.Join(kept, " ")})
		}
		return false
	}
	if add(srcLine{l.n, first}) {
		return trimOpen(out)
	}
	for p.i < len(p.lines) {
		line := p.lines[p.i]
		p.i++
		if add(line) {
			return trimOpen(out)
		}
	}
	p.problem(l.n, true, "this { has no matching }")
	return nil
}

// trimOpen drops the opening { a body's first line starts with.
func trimOpen(lines []srcLine) []srcLine {
	if len(lines) == 0 {
		return nil
	}
	t := strings.TrimSpace(strings.TrimPrefix(lines[0].text, "{"))
	if t == "" {
		return lines[1:]
	}
	lines[0].text = t
	return lines
}

// splitFields splits an event definition at its first n-1 colons outside
// parentheses (so $(...) matchtext can have colons); the last field is
// the rest, the commands.
func splitFields(s string, n int) []string {
	var out []string
	depth, start := 0, 0
	for i := 0; i < len(s) && len(out) < n-1; i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth = max(depth-1, 0)
		case ':':
			if depth == 0 {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	return append(out, s[start:])
}

// afterColons guesses where an unsupported definition's commands start,
// so its { } block can be skipped: after colon number n, or the last
// field when n is 0.
func afterColons(s string, n int) string {
	if n > 0 {
		f := splitFields(s, n+1)
		return f[len(f)-1]
	}
	if i := strings.Index(s, ":{"); i >= 0 {
		return s[i+1:]
	}
	if strings.HasSuffix(s, "{") {
		return "{"
	}
	return ""
}

func isDigits(s string) bool {
	return s != "" && strings.Trim(s, "0123456789") == ""
}

// braceDelta is how a word changes the { } depth: { and a popup item's
// Label:{ open a block, } closes one.
func braceDelta(w string) int {
	switch {
	case w == "{" || strings.HasSuffix(w, ":{"):
		return 1
	case w == "}":
		return -1
	}
	return 0
}
