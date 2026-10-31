package script

import (
	"cmp"
	"maps"
	"strings"
)

// builtin is a built-in identifier. Its arguments are evaluated before
// it's called, unless it's in lazyArgs.
type builtin func(r *run, fr *frame, c *call) (string, error)

var builtins map[string]builtin

// lazyArgs are identifiers that evaluate their own arguments: $iif only
// evaluates the branch it returns, and $eval decides how many times.
var lazyArgs = map[string]bool{"iif": true, "eval": true, "": true, "regsubex": true, "var": true}

func init() {
	builtins = map[string]builtin{"regsubex": regsubexIdent}
	for _, m := range []map[string]builtin{eventIdents, coreIdents, textIdents, tokenIdents, numIdents,
		timeIdents, hashIdents, regexIdents, timerIdents, fileIdents, menuIdents, miscIdents,
		fileHandleIdents, connIdents, userIdents} {
		maps.Copy(builtins, m)
	}
}

func str(s string) builtin {
	return func(*run, *frame, *call) (string, error) { return s, nil }
}

func boolStr(b bool) string {
	if b {
		return "$true"
	}
	return "$false"
}

// Identifiers about the event being handled, from mIRC's Remote
// Identifiers section.
var eventIdents = map[string]builtin{
	"nick": func(r *run, fr *frame, c *call) (string, error) {
		if c.hasArgs {
			return r.nicklist(fr, c)
		}
		return evField(fr, func(ev *Event) string { return ev.Nick }), nil
	},
	// $chan is the event's channel; $chan(N) the Nth channel we're on
	// (N = 0 is how many), and $chan(#name) that name if we're on it.
	"chan": func(r *run, fr *frame, c *call) (string, error) {
		if !c.hasArgs {
			return fr.channel, nil
		}
		if c.prop != "" {
			return "", errorf("$chan().", c.prop, " isn't supported yet")
		}
		chans := r.h.Chans(fr.network)
		if n, ok := parseNum(c.arg(0)); ok {
			switch i := int(n); {
			case i == 0:
				return itoa(len(chans)), nil
			case i > 0 && i <= len(chans):
				return chans[i-1], nil
			}
			return "", nil
		}
		for _, ch := range chans {
			if strings.EqualFold(ch, c.arg(0)) {
				return ch, nil
			}
		}
		return "", nil
	},
	// $comchan(nick,N) is the Nth channel both we and nick are on.
	"comchan": func(r *run, fr *frame, c *call) (string, error) {
		var common []string
		for _, ch := range r.h.Chans(fr.network) {
			if _, ok := r.nickOn(fr.network, ch, c.arg(0)); ok {
				common = append(common, ch)
			}
		}
		switch i := int(c.num(1)); {
		case i == 0:
			return itoa(len(common)), nil
		case i > 0 && i <= len(common):
			return common[i-1], nil
		}
		return "", nil
	},
	// $address is the event's user@host; $address(nick,type) a mask of
	// nick's address, if the client knows it, like $mask.
	"address": func(r *run, fr *frame, c *call) (string, error) {
		if !c.hasArgs {
			return evField(fr, func(ev *Event) string { return ev.Address }), nil
		}
		if a, ok := r.ialEntry(fr.network, c.arg(0)); ok {
			return mask(a, int(c.num(1)))
		}
		return "", nil
	},
	"mask": func(r *run, fr *frame, c *call) (string, error) {
		return mask(c.arg(0), int(c.num(1)))
	},
	"numeric": func(r *run, fr *frame, c *call) (string, error) {
		return evField(fr, func(ev *Event) string { return iff(ev.Numeric > 0, itoa(ev.Numeric), "") }), nil
	},
	"site": func(r *run, fr *frame, c *call) (string, error) {
		_, site, _ := strings.Cut(evField(fr, func(ev *Event) string { return ev.Address }), "@")
		return site, nil
	},
	"fulladdress": func(r *run, fr *frame, c *call) (string, error) {
		if fr.ev == nil || fr.ev.Address == "" {
			return "", nil
		}
		return fr.ev.Nick + "!" + fr.ev.Address, nil
	},
	"wildsite": func(r *run, fr *frame, c *call) (string, error) {
		_, site, ok := strings.Cut(evField(fr, func(ev *Event) string { return ev.Address }), "@")
		if !ok {
			return "", nil
		}
		return "*!*@" + site, nil
	},
	"target": func(r *run, fr *frame, c *call) (string, error) {
		return evField(fr, func(ev *Event) string { return cmp.Or(ev.Chan, ev.Target) }), nil
	},
	"knick": func(r *run, fr *frame, c *call) (string, error) {
		return evField(fr, func(ev *Event) string { return iff(ev.Kind == "KICK", ev.Target, "") }), nil
	},
	"newnick": func(r *run, fr *frame, c *call) (string, error) {
		return evField(fr, func(ev *Event) string { return iff(ev.Kind == "NICK", ev.Target, "") }), nil
	},
	// $event is the event's name, or for a raw event its numeric or
	// command.
	"event": func(r *run, fr *frame, c *call) (string, error) {
		return evField(fr, func(ev *Event) string {
			return iff(ev.Kind == "RAW", ev.Command, strings.ToLower(ev.Kind))
		}), nil
	},
	"me": func(r *run, fr *frame, c *call) (string, error) {
		return r.h.Me(fr.network), nil
	},
	"network": func(r *run, fr *frame, c *call) (string, error) {
		return fr.network, nil
	},
	"active": func(r *run, fr *frame, c *call) (string, error) {
		_, w := r.h.Active()
		if w == "" {
			return "Status Window", nil
		}
		return w, nil
	},
	// $group(#name) is on or off; $group(N) the Nth group's name, or with
	// .status its state; $group(0) how many there are.
	"group": func(r *run, fr *frame, c *call) (string, error) {
		var groups []group
		for _, f := range r.e.scripts {
			groups = append(groups, f.groups...)
		}
		status := func(g group) string {
			return iff(r.e.groups[strings.ToLower(g.name)], "on", "off")
		}
		arg := c.arg(0)
		if n, ok := parseNum(arg); ok {
			i := int(n)
			switch {
			case i == 0:
				return itoa(len(groups)), nil
			case i < 0 || i > len(groups):
				return "", nil
			case c.prop == "status":
				return status(groups[i-1]), nil
			}
			return "#" + groups[i-1].name, nil
		}
		for _, g := range groups {
			if strings.EqualFold(g.name, strings.TrimPrefix(arg, "#")) {
				if c.prop == "name" {
					return "#" + g.name, nil
				}
				return status(g), nil
			}
		}
		return "", nil
	},
	"script": func(r *run, fr *frame, c *call) (string, error) {
		if c.hasArgs {
			return "", errorf("$script(N) isn't supported yet")
		}
		if fr.file == nil {
			return "", nil
		}
		return fr.file.name, nil
	},
}

func evField(fr *frame, f func(*Event) string) string {
	if fr.ev == nil {
		return ""
	}
	return f(fr.ev)
}

func iff(b bool, t, f string) string {
	if b {
		return t
	}
	return f
}

// nicklist is $nick(#,N,modes): the Nth nick on a channel, or with N = 0
// how many there are. modes limits it to nicks with those prefixes (o, h,
// v, or r for regular), like mIRC.
func (r *run) nicklist(fr *frame, c *call) (string, error) {
	nicks := r.h.Nicks(fr.network, c.arg(0))
	if modes := c.arg(2); modes != "" {
		var keep []Nick
		for _, n := range nicks {
			if nickHasMode(n, modes) {
				keep = append(keep, n)
			}
		}
		nicks = keep
	}
	arg := c.arg(1)
	if n, ok := parseNum(arg); ok {
		switch i := int(n); {
		case i == 0:
			return itoa(len(nicks)), nil
		case i > 0 && i <= len(nicks):
			return nicks[i-1].Name, nil
		}
		return "", nil
	}
	// $nick(#,nick) is that nick's position.
	for i, n := range nicks {
		if strings.EqualFold(n.Name, arg) {
			return itoa(i + 1), nil
		}
	}
	return "", nil
}

func nickHasMode(n Nick, modes string) bool {
	for _, m := range modes {
		switch m {
		case 'o':
			if strings.ContainsAny(n.Prefix, "~&@") {
				return true
			}
		case 'h':
			if strings.Contains(n.Prefix, "%") {
				return true
			}
		case 'v':
			if strings.Contains(n.Prefix, "+") {
				return true
			}
		case 'r':
			if n.Prefix == "" {
				return true
			}
		}
	}
	return false
}

// The language's own identifiers.
var coreIdents = map[string]builtin{
	// $version is the mIRC version trout's scripting follows, so scripts'
	// version checks pass.
	"version": str("7.86"),
	"null":    str(""),
	"true":    str("$true"),
	"false":   str("$false"),
	"cr":      str("\r"),
	"lf":      str("\n"),
	"crlf":    str("\r\n"),
	"v1": func(r *run, fr *frame, c *call) (string, error) {
		return fr.v1, nil
	},
	"ifmatch": func(r *run, fr *frame, c *call) (string, error) {
		return fr.v1, nil
	},
	"v2": func(r *run, fr *frame, c *call) (string, error) {
		return fr.v2, nil
	},
	"signal": func(r *run, fr *frame, c *call) (string, error) {
		return evField(fr, func(ev *Event) string { return iff(ev.Kind == "SIGNAL", ev.Target, "") }), nil
	},
	// $halted is whether a ^ event halted the client's text: in a ^
	// handler, one before this one; after, any of them.
	"halted": func(r *run, fr *frame, c *call) (string, error) {
		return boolStr(r.haltdef || fr.ev != nil && fr.ev.Halted), nil
	},
	"error": func(r *run, fr *frame, c *call) (string, error) {
		return r.errText, nil
	},
	"result": func(r *run, fr *frame, c *call) (string, error) {
		return fr.result, nil
	},
	"isid": func(r *run, fr *frame, c *call) (string, error) {
		return boolStr(fr.isid), nil
	},
	"prop": func(r *run, fr *frame, c *call) (string, error) {
		return fr.prop, nil
	},
	// $iif(C,T,F) evaluates only the branch it returns.
	"iif": func(r *run, fr *frame, c *call) (string, error) {
		if len(c.args) < 2 {
			return "", errorf("$iif: insufficient parameters")
		}
		ok, err := r.cond(fr, c.args[0])
		if err != nil {
			return "", err
		}
		if ok {
			return r.eval(fr, c.args[1])
		}
		if len(c.args) > 2 {
			return r.eval(fr, c.args[2])
		}
		return "", nil
	},
	// $eval(text,N) evaluates text N times; $(text,N) is the same.
	"eval": evalIdent,
	"":     evalIdent,
	// $+(a,b,c) joins its arguments.
	"+": func(r *run, fr *frame, c *call) (string, error) {
		return strings.Join(c.vals, ""), nil
	},
}

func evalIdent(r *run, fr *frame, c *call) (string, error) {
	if len(c.args) == 0 {
		return "", nil
	}
	n := 1
	if len(c.args) > 1 {
		v, err := r.eval(fr, c.args[1])
		if err != nil {
			return "", err
		}
		n = atoi(v)
	}
	v := strings.TrimSpace(c.args[0])
	for range n {
		// Each evaluation is a step, so $eval(x,999999999) can't get
		// around the budget.
		if err := r.step(); err != nil {
			return "", err
		}
		var err error
		if v, err = r.eval(fr, v); err != nil {
			return "", err
		}
	}
	return v, nil
}

// mask writes nick!user@host as one of mIRC's mask types, from its Nick and
// Address Identifiers section:
//
//	0: *!user@host     5: nick!user@host
//	1: *!*user@host    6: nick!*user@host
//	2: *!*@host        7: nick!*@host
//	3: *!*user@*.host  8: nick!*user@*.host
//	4: *!*@*.host      9: nick!*@*.host
//
// *user is the user without a leading ~, and *.host the host with its
// first part (or for an IP address, its last) made a wildcard.
func mask(address string, kind int) (string, error) {
	if kind < 0 || kind > 9 {
		return "", errorf("$mask: type ", itoa(kind), " isn't supported yet")
	}
	nick, rest, _ := strings.Cut(address, "!")
	user, host, ok := strings.Cut(rest, "@")
	if !ok {
		return "", nil
	}
	n := "*"
	if kind >= 5 {
		n, kind = nick, kind-5
	}
	u := "*" + strings.TrimLeft(user, "~")
	h := host
	if kind == 3 || kind == 4 {
		h = wildHost(host)
	}
	switch kind {
	case 0:
		u = user
	case 2, 4:
		u = "*"
	}
	return n + "!" + u + "@" + h, nil
}

// wildHost makes the most specific part of a host a wildcard: *.b.c for
// a.b.c, and 1.2.3.* for an IP address. A host of one or two parts stays
// as it is (checked in mIRC: a.b is a.b).
func wildHost(host string) string {
	parts := strings.Split(host, ".")
	if len(parts) < 3 {
		return host
	}
	if strings.Trim(host, "0123456789.") == "" {
		return strings.Join(parts[:len(parts)-1], ".") + ".*"
	}
	return "*." + strings.Join(parts[1:], ".")
}
