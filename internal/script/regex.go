package script

import (
	"regexp"
	"strings"
)

// Regular expressions, from mIRC's Regular Expressions section. mIRC uses
// PCRE; trout uses Go's RE2, which has no backreferences or lookarounds,
// so a pattern needing those is an error, not a wrong match.

// regexMatches are the results $regml reads, by lower-case name; "" is the
// default name, which every unnamed call overwrites.
type regexMatches map[string][]string

// compileRegex reads a pattern: /re/flags, m/re/flags, or a bare pattern.
// It reports whether g (global) was given and whether S (strip control
// codes from the text) was.
func compileRegex(pattern string) (re *regexp.Regexp, global, strip bool, err error) {
	p, flags := pattern, ""
	if strings.HasPrefix(p, "m/") {
		p = p[1:]
	}
	if strings.HasPrefix(p, "/") {
		if end := strings.LastIndexByte(p, '/'); end > 0 {
			p, flags = p[1:end], p[end+1:]
		}
	}
	var mods string
	for _, f := range flags {
		switch f {
		case 'g':
			global = true
		case 'S':
			strip = true
		case 'i', 'm', 's', 'U':
			mods += string(f)
		case 'x':
			// RE2 has no extended mode: take out the whitespace and comments
			// it would ignore.
			p = unextend(p)
		case 'u', 'F', 'X', 'A', 'E', 'D':
			// UTF-8 is RE2's anyway, and F's numbering of groups is how RE2
			// numbers them. A anchors at the start.
			if f == 'A' {
				p = `\A(?:` + p + `)`
			}
		}
	}
	if mods != "" {
		p = "(?" + mods + ")" + p
	}
	re, err = regexp.Compile(p)
	if err != nil {
		return nil, false, false, errorf("regular expression ", pattern, " isn't supported: ", err.Error())
	}
	return re, global, strip, nil
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
	re            *regexp.Regexp
	global, strip bool
	err           error
}

// compile is compileRegex, through the Engine's cache.
func (e *Engine) compile(pattern string) (*compiledRegex, error) {
	if c, ok := e.regexes[pattern]; ok {
		return c, c.err
	}
	re, global, strip, err := compileRegex(pattern)
	if len(e.regexes) >= 256 {
		clear(e.regexes)
	}
	c := &compiledRegex{re: re, global: global, strip: strip, err: err}
	e.regexes[pattern] = c
	return c, err
}

// regex runs a match and keeps its captures for $regml: in order, without
// empty ones, like mIRC without its F modifier. It returns how many
// matches there were (at most one without g).
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
	matches := c.re.FindAllStringSubmatch(text, n)
	var caps []string
	for _, m := range matches {
		for _, c := range m[1:] {
			if c != "" {
				caps = append(caps, c)
			}
		}
	}
	if r.e.regml == nil {
		r.e.regml = regexMatches{}
	}
	r.e.regml[strings.ToLower(name)] = caps
	return len(matches), nil
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
	// are.
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
			return caps[n-1], nil
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
