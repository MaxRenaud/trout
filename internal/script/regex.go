package script

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// Regular expressions, from mIRC's Regular Expressions section. mIRC uses
// PCRE; trout uses Go's RE2, which has no backreferences or lookarounds,
// so a pattern needing those is an error, not a wrong match.

// regexMatches are the captures $regml and $regmlex read, by lower-case
// name; "" is the default name, which every unnamed call overwrites.
type regexMatches map[string][]regexCapture

// regexCapture is one capture: its text, where it starts (in characters,
// from 1: .pos), which match it's in (from 1) and its group's number.
type regexCapture struct {
	text              string
	pos, match, group int
}

// regexFlags are the modifiers after a pattern's closing / that change
// more than the pattern: g (global), S (strip control codes from the
// text) and F (keep groups that didn't take part, as $null).
type regexFlags struct{ global, strip, fill bool }

// compileRegex reads a pattern: /re/flags, m/re/flags, or a bare pattern.
func compileRegex(pattern string) (re *regexp.Regexp, fl regexFlags, err error) {
	p, flags := pattern, ""
	if strings.HasPrefix(p, "m/") {
		p = p[1:]
	}
	if strings.HasPrefix(p, "/") {
		if end := strings.LastIndexByte(p, '/'); end > 0 {
			p, flags = p[1:end], p[end+1:]
		}
	}
	var mods []rune
	for _, f := range flags {
		switch f {
		case 'g':
			fl.global = true
		case 'S':
			fl.strip = true
		case 'F':
			fl.fill = true
		case 'i', 'm', 's', 'U':
			mods = append(mods, f)
		case 'x':
			// RE2 has no extended mode: take out the whitespace and comments
			// it would ignore.
			p = unextend(p)
		case 'u', 'X', 'A', 'E', 'D':
			// UTF-8 is RE2's anyway. A anchors at the start.
			if f == 'A' {
				p = `\A(?:` + p + `)`
			}
		}
	}
	if len(mods) > 0 {
		p = "(?" + string(mods) + ")" + p
	}
	re, err = regexp.Compile(p)
	if err != nil {
		return nil, fl, errorf("regular expression ", pattern, " isn't supported: ", err.Error())
	}
	return re, fl, nil
}

// unextend drops what the x flag ignores: unescaped whitespace, and #
// comments to the end of a line, outside character classes ([ #] keeps
// both, like PCRE).
func unextend(p string) string {
	var b strings.Builder
	inClass := false
	for i := 0; i < len(p); i++ {
		switch c := p[i]; {
		case c == '\\' && i+1 < len(p):
			b.WriteByte(c)
			b.WriteByte(p[i+1])
			i++
		case inClass:
			b.WriteByte(c)
			inClass = c != ']'
		case c == '[':
			// A ] first in the class ([]a] or [^]a]) is part of it.
			b.WriteByte(c)
			inClass = true
			if i+1 < len(p) && p[i+1] == '^' {
				b.WriteByte('^')
				i++
			}
			if i+1 < len(p) && p[i+1] == ']' {
				b.WriteByte(']')
				i++
			}
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
		case c == '#':
			for i < len(p) && p[i] != '\n' {
				i++
			}
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// regexMatch reports whether text matches pattern, for $hfind and event
// matchtext.
func (r *run) regexMatch(pattern, text string) (bool, error) {
	c, err := r.e.compile(pattern)
	if err != nil {
		return false, err
	}
	if c.strip {
		text = stripCodes(text, "")
	}
	return c.re.MatchString(text), nil
}

// compiledRegex is a pattern compileRegex has read, kept so the same
// pattern isn't compiled again for every message or line.
type compiledRegex struct {
	re *regexp.Regexp
	regexFlags
	err error
}

// compile is compileRegex, through the Engine's cache.
func (e *Engine) compile(pattern string) (*compiledRegex, error) {
	if c, ok := e.regexes[pattern]; ok {
		return c, c.err
	}
	re, fl, err := compileRegex(pattern)
	if len(e.regexes) >= 256 {
		clear(e.regexes)
	}
	c := &compiledRegex{re: re, regexFlags: fl, err: err}
	e.regexes[pattern] = c
	return c, err
}

// regex runs a match and keeps its captures for $regml and $regmlex: in
// order, without the groups that took no part (a group that matched
// nothing is kept), unless F asks for those too, like mIRC's (checked).
// It returns how many matches there were (at most one without g).
func (r *run) regex(name, text, pattern string) (int, error) {
	c, err := r.e.compile(pattern)
	if err != nil {
		return 0, err
	}
	if c.strip {
		text = stripCodes(text, "")
	}
	n := 1
	if c.global {
		n = -1
	}
	matches := c.re.FindAllStringSubmatchIndex(text, n)
	var caps []regexCapture
	var at, atChars int // a byte offset and how many characters come before it
	pos := func(b int) int {
		if b < at {
			at, atChars = 0, 0
		}
		atChars += utf8.RuneCountInString(text[at:b])
		at = b
		return atChars + 1
	}
	for i, loc := range matches {
		for g := 1; g < len(loc)/2; g++ {
			start, end := loc[2*g], loc[2*g+1]
			switch {
			case start >= 0:
				caps = append(caps, regexCapture{text[start:end], pos(start), i + 1, g})
			case c.fill:
				caps = append(caps, regexCapture{"", 0, i + 1, g})
			}
		}
	}
	if r.e.regml == nil {
		r.e.regml = regexMatches{}
	}
	r.e.regml[strings.ToLower(name)] = caps
	return len(matches), nil
}

// value is $regml's and $regmlex's value for a capture: its text, or
// with .pos where it starts.
func (c *regexCapture) value(prop string) string {
	if prop == "pos" {
		if c.pos == 0 {
			return ""
		}
		return itoa(c.pos)
	}
	return c.text
}

// regexArgs splits the optional name off a regex identifier's arguments:
// $regex([name], text, pattern) has 2 or 3.
func regexArgs(c *call, without int) (name string, args []string) {
	if len(c.vals) > without {
		return c.vals[0], c.vals[1:]
	}
	return "", c.vals
}

var regexIdents = map[string]builtin{
	// $regex([name], text, pattern) is how many times pattern matches.
	"regex": func(r *run, fr *frame, c *call) (string, error) {
		name, a := regexArgs(c, 2)
		if len(a) < 2 {
			return "", errorf("$regex: insufficient parameters")
		}
		n, err := r.regex(name, a[0], a[1])
		return itoa(n), err
	},
	// $regml([name], N) is the Nth capture, or with N = 0 how many there
	// are; .pos is where it starts.
	"regml": func(r *run, fr *frame, c *call) (string, error) {
		name, a := regexArgs(c, 1)
		if len(a) < 1 {
			return "", errorf("$regml: insufficient parameters")
		}
		caps := r.e.regml[strings.ToLower(name)]
		n := int(c.num(len(c.vals) - 1))
		switch {
		case n == 0:
			return itoa(len(caps)), nil
		case n > 0 && n <= len(caps):
			return caps[n-1].value(c.prop), nil
		}
		return "", nil
	},
	// $regmlex([name], N, M) is group M of the Nth match; with M = 0, how
	// many captures that match has, and with N = 0, how many matches
	// there are, up to the last with a capture. Given two parameters, the
	// first is a name unless it's a number, and M is then 1; .pos is where
	// it starts. All checked in mIRC.
	"regmlex": func(r *run, fr *frame, c *call) (string, error) {
		var name string
		var n, m int
		_, numFirst := parseNum(c.arg(0))
		switch {
		case len(c.vals) == 3:
			name, n, m = c.vals[0], int(c.num(1)), int(c.num(2))
		case len(c.vals) != 2:
			return "", errorf("$regmlex: invalid parameters")
		case numFirst:
			n, m = int(c.num(0)), int(c.num(1))
		default:
			name, n, m = c.vals[0], int(c.num(1)), 1
		}
		caps := r.e.regml[strings.ToLower(name)]
		switch {
		case n <= 0:
			if len(caps) == 0 {
				return "0", nil
			}
			return itoa(caps[len(caps)-1].match), nil
		case m == 0:
			count := 0
			for _, cp := range caps {
				if cp.match == n {
					count++
				}
			}
			return itoa(count), nil
		}
		for _, cp := range caps {
			if cp.match == n && cp.group == m {
				return cp.value(c.prop), nil
			}
		}
		return "", nil
	},
	// $regsub([name], text, pattern, subtext, %var) puts text with matches
	// replaced into %var, and is how many were. In subtext, \1 is a
	// capture and \0 the whole match.
	"regsub": func(r *run, fr *frame, c *call) (string, error) {
		name, a := regexArgs(c, 4)
		if len(a) < 4 {
			return "", errorf("$regsub: invalid parameters")
		}
		varName, isVar := strings.CutPrefix(strings.TrimSpace(c.args[len(c.args)-1]), "%")
		if !isVar {
			return "", errorf("$regsub: invalid parameters")
		}
		n, err := r.regex(name, a[0], a[1])
		if err != nil {
			return "", err
		}
		compiled, _ := r.e.compile(a[1])
		out := replaceMatches(compiled.re, a[0], compiled.global, func(m []string, _ int) string { return expandRefs(a[2], m, true) })
		if len(out) > maxLine {
			return "", errLong("$regsub")
		}
		r.setVar(fr, varName, out, false)
		return itoa(n), nil
	},
}

// regsubexIdent is $regsubex([name], text, pattern, subtext): text with
// each match replaced by subtext, evaluated for each match, where \t is
// the match, \n its number, \1 a capture and \0 how many captures. Its
// subtext is evaluated late, so it's in lazyArgs.
func regsubexIdent(r *run, fr *frame, c *call) (string, error) {
	raw := c.args
	if len(raw) < 3 {
		return "", errorf("$regsubex: insufficient parameters")
	}
	name := ""
	if len(raw) > 3 {
		v, err := r.eval(fr, raw[0])
		if err != nil {
			return "", err
		}
		name, raw = v, raw[1:]
	}
	text, err := r.eval(fr, raw[0])
	if err != nil {
		return "", err
	}
	pattern, err := r.eval(fr, raw[1])
	if err != nil {
		return "", err
	}
	if _, err := r.regex(name, text, pattern); err != nil {
		return "", err
	}
	compiled, _ := r.e.compile(pattern)
	var evalErr error
	out := replaceMatches(compiled.re, text, compiled.global, func(m []string, i int) string {
		if err := r.step(); err != nil {
			if evalErr == nil {
				evalErr = err
			}
			return ""
		}
		sub := strings.NewReplacer(`\t`, m[0], `\n`, itoa(i)).Replace(raw[2])
		v, err := r.eval(fr, expandRefs(sub, m, false))
		if err != nil && evalErr == nil {
			evalErr = err
		}
		return v
	})
	return out, evalErr
}

// replaceMatches replaces the first match, or with global every one, by
// what with returns for it (and its number, from 1).
func replaceMatches(re *regexp.Regexp, text string, global bool, with func(m []string, i int) string) string {
	n := 1
	if global {
		n = -1
	}
	var b strings.Builder
	last := 0
	for i, loc := range re.FindAllStringSubmatchIndex(text, n) {
		if b.Len() > maxLine {
			// Too long already, which the identifier reports: stop making it.
			return b.String()
		}
		m := make([]string, len(loc)/2)
		for k := range m {
			if loc[2*k] >= 0 {
				m[k] = text[loc[2*k]:loc[2*k+1]]
			}
		}
		b.WriteString(text[last:loc[0]])
		b.WriteString(with(m, i+1))
		last = loc[1]
	}
	b.WriteString(text[last:])
	return b.String()
}

// expandRefs puts captures into subtext: \1 to \9, and \0, which is the
// whole match for $regsub (whole) and how many captures there are for
// $regsubex.
func expandRefs(sub string, m []string, whole bool) string {
	var b strings.Builder
	for i := 0; i < len(sub); i++ {
		if sub[i] == '\\' && i+1 < len(sub) && sub[i+1] >= '0' && sub[i+1] <= '9' {
			k := int(sub[i+1] - '0')
			switch {
			case k == 0 && whole:
				b.WriteString(m[0])
			case k == 0:
				b.WriteString(itoa(len(m) - 1))
			case k < len(m):
				b.WriteString(m[k])
			}
			i++
			continue
		}
		b.WriteByte(sub[i])
	}
	return b.String()
}
