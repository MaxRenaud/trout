package script

import (
	"cmp"
	"slices"
	"strconv"
	"strings"
)

// The Users list, from mIRC's Access Levels and Remote Commands sections:
// addresses with access levels, which decide which events a user can
// trigger. Someone who isn't listed has the default level ($dlevel, 1).

// user is a Users list entry, like 3,5,friend:nick!user@host. The first
// level is a general one (the user has every level up to it) unless it
// starts with =, which makes it specific like the others.
type user struct {
	levels []string
	addr   string
	info   string
}

func (u *user) String() string {
	s := strings.Join(u.levels, ",") + ":" + u.addr
	if u.info != "" {
		s += " " + u.info
	}
	return s
}

// parseUser reads a line as Users saves it: levels:address [info].
func parseUser(line string) *user {
	levels, rest, ok := strings.Cut(line, ":")
	if !ok {
		return nil
	}
	addr, info, _ := strings.Cut(rest, " ")
	return &user{levels: splitLevels(levels), addr: addr, info: info}
}

func splitLevels(s string) []string {
	var out []string
	for l := range strings.SplitSeq(s, ",") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// general is a user's general level, or -1 if their first level is
// specific (=N) or named.
func (u *user) general() int {
	if len(u.levels) == 0 || !isDigits(u.levels[0]) {
		return -1
	}
	return atoi(u.levels[0])
}

// has reports whether the user has level l specifically: one of their
// levels other than a general first one.
func (u *user) has(l string) bool {
	for i, lv := range u.levels {
		if i == 0 && isDigits(lv) {
			continue
		}
		if strings.EqualFold(strings.TrimPrefix(lv, "="), l) {
			return true
		}
	}
	return false
}

// matchesAddr reports whether a Users entry is for nick!user@host: an
// entry without ! or @ is a nick, otherwise it's a wildcard address.
func (u *user) matchesAddr(full string) bool {
	if !strings.ContainsAny(u.addr, "!@") {
		nick, _, _ := strings.Cut(full, "!")
		return strings.EqualFold(u.addr, nick)
	}
	return wildMatch(u.addr, full, false)
}

// userFor finds the first Users entry for nick!user@host, or nil.
func (e *Engine) userFor(full string) *user {
	for _, u := range e.users {
		if u.matchesAddr(full) {
			return u
		}
	}
	return nil
}

// levelOf is someone's level for comparing two people, as on KICK's
// level comparisons: their highest numbered level, or the default.
func (e *Engine) levelOf(full string) int {
	u := e.userFor(full)
	if u == nil {
		return e.dlevel
	}
	best := -1
	for _, l := range u.levels {
		if l = strings.TrimPrefix(l, "="); isDigits(l) {
			best = max(best, atoi(l))
		}
	}
	if best < 0 {
		return e.dlevel
	}
	return best
}

// access reports whether someone can trigger an event at this level, and
// the level of theirs that matched ($ulevel).
func (e *Engine) access(h *event, full string) (ok bool, ulevel string) {
	u := e.userFor(full)
	general := e.dlevel
	if u != nil {
		general = u.general()
	}
	switch {
	case h.any:
		return true, strconv.Itoa(max(general, 0))
	case h.named:
		return u != nil && u.has(h.name), h.name
	case h.exact:
		// +N: only users with exactly level N.
		l := strconv.Itoa(h.level)
		return general == h.level || u != nil && u.has(l), l
	}
	l := strconv.Itoa(h.level)
	if u != nil && u.has(l) {
		return true, l
	}
	return general >= h.level, strconv.Itoa(general)
}

// usersChanged notes the list changed, so finish saves it.
func (r *run) usersChanged() { r.e.usersDirty = true }

// addressFor is the address /guser and /ruser use for a nick: from the
// client's address list, as a mask of the given type (3 by default).
func (r *run) addressFor(network, nick string, kind int) string {
	if a, ok := r.ialEntry(network, nick); ok {
		if m, err := mask(a, kind); err == nil {
			return m
		}
	}
	return ""
}

// cmdUsers runs /auser, /guser, /ruser, /iuser, /rlevel, /flush, /ulist and
// /dlevel.
func (r *run) cmdUsers(fr *frame, name, rest string) error {
	line, err := r.eval(fr, rest)
	if err != nil {
		return err
	}
	sw, line := switches(line)
	a := strings.Fields(line)
	arg := func(i int) string {
		if i < len(a) {
			return a[i]
		}
		return ""
	}
	need := func(n int) error {
		if len(a) < n {
			return errorf("/", name, ": insufficient parameters")
		}
		return nil
	}
	switch name {
	case "auser", "guser":
		if err := need(2); err != nil {
			return err
		}
		addr, info := arg(1), strings.Join(a[min(2, len(a)):], " ")
		if name == "guser" {
			kind := 3
			if len(a) > 2 && isDigits(a[2]) {
				kind, info = atoi(a[2]), strings.Join(a[3:], " ")
			}
			if addr = r.addressFor(fr.network, arg(1), kind); addr == "" {
				return errorf("/guser: ", arg(1), "'s address isn't known")
			}
		}
		r.addUser(splitLevels(arg(0)), addr, info, strings.Contains(sw, "a"))
	case "ruser":
		if err := need(1); err != nil {
			return err
		}
		// /ruser [levels] <nick|address> [type]
		// /ruser [levels] <nick|address> [type]: with two words, the second
		// is a type if it's a number, and the first is levels otherwise.
		var levels []string
		target := a
		if len(a) > 2 || len(a) == 2 && !isDigits(a[1]) {
			levels, target = splitLevels(a[0]), a[1:]
		}
		addr := target[0]
		if len(target) > 1 && isDigits(target[1]) {
			addr = r.addressFor(fr.network, addr, atoi(target[1]))
		}
		r.removeUsers(addr, levels)
	case "iuser":
		if err := need(1); err != nil {
			return err
		}
		for _, u := range r.e.users {
			if strings.EqualFold(u.addr, arg(0)) {
				u.info = strings.Join(a[1:], " ")
				r.usersChanged()
			}
		}
	case "rlevel":
		if err := need(1); err != nil {
			return err
		}
		r.removeLevels(splitLevels(arg(0)), strings.Contains(sw, "r"))
	case "flush":
		r.flushUsers(fr, splitLevels(arg(0)))
	case "ulist":
		r.listUsers(fr, arg(0))
	case "dlevel":
		if err := need(1); err != nil {
			return err
		}
		r.e.dlevel = atoi(arg(0))
		r.usersChanged()
	}
	return nil
}

// normalLevels writes levels the way mIRC keeps them (checked): the first
// as given, every other one specific, with an =: 5,7 is 5,=7.
func normalLevels(levels []string) []string {
	out := slices.Clone(levels)
	for i := 1; i < len(out); i++ {
		if !strings.HasPrefix(out[i], "=") {
			out[i] = "=" + out[i]
		}
	}
	return out
}

// addUser is /auser: it adds someone, or replaces their levels. With -a it
// adds to an existing entry's levels instead, like mIRC (checked): a new
// general level replaces theirs, which stays as a specific one (9 added
// to 4,=1 is 9,=4,=1); a first level of =N leaves it as it is.
func (r *run) addUser(levels []string, addr, info string, add bool) {
	r.usersChanged()
	for _, u := range r.e.users {
		if !strings.EqualFold(u.addr, addr) {
			continue
		}
		if !add {
			u.levels, u.info = normalLevels(levels), info
			return
		}
		merged := slices.Clone(u.levels)
		if len(levels) > 0 && isDigits(levels[0]) {
			if len(merged) > 0 && isDigits(merged[0]) {
				merged[0] = "=" + merged[0]
			}
			merged = append([]string{levels[0]}, merged...)
			levels = levels[1:]
		}
		for _, l := range levels {
			l = "=" + strings.TrimPrefix(l, "=")
			if !slices.ContainsFunc(merged, func(x string) bool { return strings.EqualFold(x, l) }) {
				merged = append(merged, l)
			}
		}
		u.levels = normalLevels(merged)
		if info != "" {
			u.info = info
		}
		return
	}
	r.e.users = append(r.e.users, &user{levels: normalLevels(levels), addr: addr, info: info})
}

// removeUsers is /ruser: it removes the entry for addr (with Nick!, every
// entry starting with it), or just some of its levels, and the entry if
// it has none left.
func (r *run) removeUsers(addr string, levels []string) {
	prefix, all := strings.CutSuffix(addr, "!")
	r.e.users = slices.DeleteFunc(r.e.users, func(u *user) bool {
		hit := strings.EqualFold(u.addr, addr) || all && strings.HasPrefix(strings.ToLower(u.addr), strings.ToLower(prefix)+"!")
		if !hit {
			return false
		}
		r.usersChanged()
		if len(levels) == 0 {
			return true
		}
		u.levels = slices.DeleteFunc(u.levels, func(l string) bool {
			return slices.ContainsFunc(levels, func(x string) bool { return strings.EqualFold(strings.TrimPrefix(l, "="), x) })
		})
		return len(u.levels) == 0
	})
}

// removeLevels is /rlevel: it removes users whose general level is one of
// levels, or with -r, those levels from everyone, and users left with none.
func (r *run) removeLevels(levels []string, everywhere bool) {
	in := func(l string) bool {
		return slices.ContainsFunc(levels, func(x string) bool {
			return strings.EqualFold(strings.TrimPrefix(x, "="), strings.TrimPrefix(l, "="))
		})
	}
	r.e.users = slices.DeleteFunc(r.e.users, func(u *user) bool {
		if !everywhere {
			if len(u.levels) > 0 && in(u.levels[0]) {
				r.usersChanged()
				return true
			}
			return false
		}
		n := len(u.levels)
		u.levels = slices.DeleteFunc(u.levels, in)
		if len(u.levels) != n {
			r.usersChanged()
		}
		return len(u.levels) == 0
	})
}

// flushUsers is /flush: it removes nick entries (not addresses) for nicks
// that aren't on any of our channels, with one of levels if given.
func (r *run) flushUsers(fr *frame, levels []string) {
	on := map[string]bool{}
	for _, ch := range r.h.Chans(fr.network) {
		for _, n := range r.h.Nicks(fr.network, ch) {
			on[strings.ToLower(n.Name)] = true
		}
	}
	r.e.users = slices.DeleteFunc(r.e.users, func(u *user) bool {
		if strings.ContainsAny(u.addr, "!@") || on[strings.ToLower(u.addr)] {
			return false
		}
		if len(levels) > 0 && !slices.ContainsFunc(u.levels, func(l string) bool {
			return slices.Contains(levels, strings.TrimPrefix(l, "="))
		}) {
			return false
		}
		r.usersChanged()
		return true
	})
}

// listUsers is /ulist [<|>]level: the users with that level, or at most
// (<) or at least (>) it, echoed.
func (r *run) listUsers(fr *frame, spec string) {
	cmp := ""
	if spec != "" && (spec[0] == '<' || spec[0] == '>') {
		cmp, spec = spec[:1], spec[1:]
	}
	for _, u := range r.e.users {
		g := u.general()
		ok := spec == ""
		switch {
		case ok:
		case cmp == "<":
			ok = g >= 0 && g <= atoi(spec)
		case cmp == ">":
			ok = g >= atoi(spec)
		default:
			ok = g == atoi(spec) || u.has(spec)
		}
		if ok {
			r.actions = append(r.actions, Action{Echo: &Echo{Network: fr.network, Window: fr.window, Color: -1, Text: u.String()}})
		}
	}
}

// compareLevels is an event's <, >, <=, >=, <> or = prefix: the level of
// whoever did it (kicked, opped, banned) against their target's.
func (e *Engine) compareLevels(op string, actor, target int) bool {
	switch op {
	case "<":
		return actor < target
	case ">":
		return actor > target
	case "<=":
		return actor <= target
	case ">=", "=>":
		return actor >= target
	case "<>":
		return actor != target
	case "=":
		return actor == target
	}
	return true
}

var userIdents = map[string]builtin{
	// $ulevel is the level of the user's that let them trigger this
	// event, $clevel the event's level, and $dlevel the default level.
	"ulevel": func(r *run, fr *frame, c *call) (string, error) { return fr.ulevel, nil },
	"clevel": func(r *run, fr *frame, c *call) (string, error) { return fr.clevel, nil },
	"dlevel": func(r *run, fr *frame, c *call) (string, error) { return itoa(r.e.dlevel), nil },
	// $level(address) is the levels of the Users entry for it.
	"level": func(r *run, fr *frame, c *call) (string, error) {
		addr := c.arg(0)
		for _, u := range r.e.users {
			if strings.EqualFold(u.addr, addr) || strings.ContainsAny(addr, "!@") && u.matchesAddr(addr) {
				return strings.Join(u.levels, ","), nil
			}
		}
		return "", nil
	},
	// $ulist(address, L, N) is the Nth entry matching address (a
	// wildcard, completed with * if it isn't a full address) with level L;
	// N = 0 counts them. "If you specify L, you must specify N", so
	// $ulist(address, N) has no L. .info is the entry's info.
	"ulist": func(r *run, fr *frame, c *call) (string, error) {
		level, nArg := "", 1
		if len(c.vals) > 2 {
			level, nArg = c.arg(1), 2
		}
		pattern := c.arg(0)
		if pattern != "*" && !strings.Contains(pattern, "!") {
			pattern += "!*@*"
		}
		var hits []*user
		for _, u := range r.e.users {
			exact := strings.EqualFold(c.arg(0), u.addr)
			if !exact && !wildMatch(pattern, u.addr, false) && !wildMatch(u.addr, pattern, false) && pattern != "*" {
				continue
			}
			if l := level; l != "" && !(u.general() >= 0 && strconv.Itoa(u.general()) == l || u.has(l)) {
				continue
			}
			hits = append(hits, u)
		}
		n := 1
		if len(c.vals) > nArg {
			n = int(c.num(nArg))
		}
		if n == 0 {
			return itoa(len(hits)), nil
		}
		if n < 1 || n > len(hits) {
			return "", nil
		}
		if c.prop == "info" {
			return hits[n-1].info, nil
		}
		return hits[n-1].addr, nil
	},
}

// Users is the Users list, as Save sends it: levels:address info lines.
func (e *Engine) Users() []string {
	var out []string
	for _, u := range e.users {
		out = append(out, u.String())
	}
	return out
}

// RestoreUsers sets the Users list and default level saved from an
// earlier session.
func (e *Engine) RestoreUsers(lines []string, dlevel int) {
	e.users = nil
	for _, l := range lines {
		if u := parseUser(l); u != nil {
			e.users = append(e.users, u)
		}
	}
	// 0 is a missing setting (an int's zero value): the default, 1.
	e.dlevel = cmp.Or(max(dlevel, 0), 1)
}
