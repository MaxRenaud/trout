package script

import (
	"slices"
	"strings"
)

// piece is a word of a line on its way to being evaluated.
type piece struct {
	text string
	done bool // already evaluated by [ ] brackets: used as it is
}

// eval evaluates a line: its %variables, $identifiers and [ ] brackets,
// joining words around $+. Like mIRC, it works word by word, and runs of
// spaces count as one.
func (r *run) eval(fr *frame, text string) (string, error) {
	if r.literal > 0 {
		return strings.Join(strings.Fields(text), " "), nil
	}
	words := splitWords(text)
	if len(words) == 0 {
		return "", nil
	}
	pieces, err := r.brackets(fr, words, true)
	if err != nil {
		return "", err
	}
	return r.evalPieces(fr, pieces)
}

// brackets evaluates a line's [ ] brackets first, innermost first, which
// is what they're for: "% [ $+ [ $1 ] ]" makes the variable name before
// it's looked up. At the top level a bracket's result is used as it is;
// inside another bracket it's evaluated again, so "[ [ %x ] ]" evaluates
// %x twice, like mIRC. A bracket starting with $+ joins its result to the
// word before it while that's still unevaluated.
func (r *run) brackets(fr *frame, words []string, top bool) ([]piece, error) {
	var out []piece
	for i := 0; i < len(words); i++ {
		if words[i] != "[" {
			out = append(out, piece{text: words[i]})
			continue
		}
		j := closeBracket(words, i)
		v, joinLeft, joinRight, err := r.bracket(fr, words[i+1:j])
		if err != nil {
			return nil, err
		}
		i = j
		p := piece{text: v, done: top}
		switch {
		case joinLeft && len(out) > 0 && out[len(out)-1].text != "$+":
			out[len(out)-1].text += v
		case v != "" || joinRight:
			out = append(out, p)
		}
		if joinRight && i+1 < len(words) && words[i+1] != "[" {
			last := &out[len(out)-1]
			last.text += words[i+1]
			last.done = false
			i++
		}
	}
	return out, nil
}

// bracket evaluates what's inside a [ ] bracket, and says whether it
// started or ended with $+.
func (r *run) bracket(fr *frame, words []string) (v string, joinLeft, joinRight bool, err error) {
	pieces, err := r.brackets(fr, words, false)
	if err != nil {
		return "", false, false, err
	}
	joinLeft = len(pieces) > 0 && pieces[0].text == "$+"
	joinRight = len(pieces) > 0 && pieces[len(pieces)-1].text == "$+"
	v, err = r.evalPieces(fr, pieces)
	return v, joinLeft, joinRight, err
}

// splitWords splits a line at spaces, except inside an identifier's
// brackets: "$calc(1 + 2) apples" is two words.
func splitWords(s string) []string {
	var out []string
	for i := 0; i < len(s); {
		if isSpace(s[i]) {
			i++
			continue
		}
		j := i
		for j < len(s) && !isSpace(s[j]) {
			if s[j] == '(' && identBefore(s[i:j]) {
				if end := matchParen(s, j); end >= 0 {
					j = end + 1
					continue
				}
			}
			j++
		}
		out = append(out, s[i:j])
		i = j
	}
	return out
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' }

// closeBracket is the index of the ] closing the [ at words[open], or the
// end of the line if there's none.
func closeBracket(words []string, open int) int {
	depth := 0
	for i := open; i < len(words); i++ {
		switch words[i] {
		case "[":
			depth++
		case "]":
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return len(words)
}

// evalPieces evaluates each piece and joins them: with spaces, or with
// nothing around $+. Empty values ($null) drop out with their space.
func (r *run) evalPieces(fr *frame, pieces []piece) (string, error) {
	var out []string
	join := false
	for _, p := range pieces {
		if p.text == "$+" && !p.done {
			join = true
			continue
		}
		v := p.text
		if !p.done {
			var err error
			if v, err = r.evalWord(fr, v); err != nil {
				return "", err
			}
		}
		switch {
		case join && len(out) > 0:
			out[len(out)-1] += v
		case v != "":
			out = append(out, v)
		}
		join = false
	}
	return strings.Join(out, " "), nil
}

// evalWord evaluates one word: a %variable, an $identifier, # for the
// current channel, or plain text.
func (r *run) evalWord(fr *frame, w string) (string, error) {
	switch {
	case w == "#":
		return fr.channel, nil
	case strings.HasPrefix(w, "#$"):
		// #$1 makes sure it's a channel name, like mIRC.
		v, err := r.evalWord(fr, w[1:])
		if err != nil || v == "" || isChannel(v) {
			return v, err
		}
		return "#" + v, nil
	case len(w) > 1 && w[0] == '%':
		return r.getVar(fr, w[1:]), nil
	case len(w) > 1 && w[0] == '$':
		return r.evalIdent(fr, w)
	}
	return w, nil
}

// evalIdent evaluates a word starting with $: $1, $2-, $1-3, $0, $$1,
// $name, $name(args) or $name(args).prop. Text after a parameter number
// is kept, so "$1's" works, like mIRC's Aliases help says.
func (r *run) evalIdent(fr *frame, w string) (string, error) {
	s := w[1:]
	required := false
	if strings.HasPrefix(s, "$") {
		required, s = true, s[1:]
	}
	v, err := r.evalIdentBody(fr, s)
	if err != nil {
		return "", err
	}
	if required && v == "" {
		// $$1: the command needs this, so don't run it, or the rest.
		return "", errStop
	}
	return v, nil
}

func (r *run) evalIdentBody(fr *frame, s string) (string, error) {
	if s == "" {
		return "$", nil
	}
	if s[0] >= '0' && s[0] <= '9' {
		return r.param(fr, s), nil
	}
	switch {
	case s[0] == '!' && len(s) > 1:
		// $!name is $name, left for a later evaluation: $eval($!len(ab),2).
		return "$" + s[1:], nil
	case s[0] == '?' || s[0] == '!':
		return "", errorf("$", s[:1], ": input prompts aren't supported")
	}
	// Like mIRC, the name runs to a ( or the end of the word: "$nick!" is
	// an identifier called nick!, which doesn't exist, so it's $null (mIRC's
	// own on TEXT example is wrong about that; checked in mIRC 7.86). After
	// the ), a .property is read and anything else is dropped: $len(ab)x
	// is 2.
	name, rest, hasArgs := s, "", false
	var args []string
	if i := strings.IndexByte(s, '('); i >= 0 {
		name, rest = s[:i], s[i:]
		end := matchParen(rest, 0)
		if end < 0 {
			return "", errorf("$", name, ": missing )")
		}
		args, hasArgs, rest = splitArgs(rest[1:end]), true, rest[end+1:]
	}
	prop := ""
	if p, ok := strings.CutPrefix(rest, "."); ok && hasArgs {
		n := 0
		for n < len(p) && isWordChar(p[n]) {
			n++
		}
		prop = p[:n]
	}
	return r.ident(fr, name, &call{args: args, hasArgs: hasArgs, prop: strings.ToLower(prop)})
}

// param is $N, $N-, $N-M or $0, with any text after it kept: $1's.
func (r *run) param(fr *frame, s string) string {
	n := 0
	for n < len(s) && s[n] >= '0' && s[n] <= '9' {
		n++
	}
	from := atoi(s[:n])
	rest := s[n:]
	if from == 0 {
		return itoa(len(fr.params)) + rest
	}
	to := from
	if r, ok := strings.CutPrefix(rest, "-"); ok {
		m := 0
		for m < len(r) && r[m] >= '0' && r[m] <= '9' {
			m++
		}
		to = len(fr.params)
		if m > 0 {
			to = atoi(r[:m])
		}
		rest = r[m:]
	}
	// $2-1 is just $2, like mIRC.
	to = max(to, from)
	if from > len(fr.params) {
		return rest
	}
	return strings.Join(fr.params[from-1:min(to, len(fr.params))], " ") + rest
}

// splitArgs splits an identifier's arguments at commas outside nested
// brackets.
func splitArgs(s string) []string {
	var out []string
	depth, start := 0, 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth = max(depth-1, 0)
		case ',':
			if depth == 0 {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	return append(out, s[start:])
}

func isWordChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_'
}

// call is an identifier's arguments, not yet evaluated: most identifiers
// evaluate them all, but $iif and $eval choose.
type call struct {
	args    []string
	hasArgs bool
	prop    string
	vals    []string // evaluated args, once evalArgs has run
}

// evalArgs evaluates all of c's arguments.
func (r *run) evalArgs(fr *frame, c *call) error {
	if c.vals != nil || len(c.args) == 0 {
		return nil
	}
	c.vals = make([]string, len(c.args))
	for i, a := range c.args {
		v, err := r.eval(fr, a)
		if err != nil {
			return err
		}
		c.vals[i] = v
	}
	return nil
}

// arg is the evaluated argument i, or "".
func (c *call) arg(i int) string {
	if i >= 0 && i < len(c.vals) {
		return c.vals[i]
	}
	return ""
}

// num is argument i as a number, or 0.
func (c *call) num(i int) float64 {
	n, _ := parseNum(c.arg(i))
	return n
}

// ident evaluates an identifier: a built-in one, or else an alias called
// as one ("Built-in identifiers of the same name have priority", says
// mIRC's help).
func (r *run) ident(fr *frame, name string, c *call) (string, error) {
	key := strings.ToLower(name)
	if f := builtins[key]; f != nil {
		if !lazyArgs[key] {
			if err := r.evalArgs(fr, c); err != nil {
				return "", err
			}
		}
		v, err := f(r, fr, c)
		if err == errNull {
			return "", nil
		}
		return v, err
	}
	if a := r.e.alias(fr.file, name); a != nil && !slices.Contains(r.stack, a) {
		if err := r.evalArgs(fr, c); err != nil {
			return "", err
		}
		params := c.vals
		if !c.hasArgs {
			params = nil
		}
		v, err := r.callAlias(fr, a, params, true, c.prop)
		return v, err
	}
	if mircIdents[key] {
		return "", errorf("$", name, " isn't supported yet")
	}
	// Like mIRC, an identifier that doesn't exist is $null.
	return "", nil
}
