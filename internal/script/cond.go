package script

import (
	"strings"
	"unicode"
)

// cond evaluates an if or while condition: comparisons like ($1 == hi),
// combined with && and || and grouped with brackets. Like mIRC, && and
// || go left to right, with no precedence between them.
func (r *run) cond(fr *frame, text string) (bool, error) {
	cp := &condParser{r: r, fr: fr, toks: condTokens(text)}
	ok, err := cp.expr()
	if err != nil {
		return false, err
	}
	if cp.i < len(cp.toks) {
		return false, errorf("invalid condition: ", text)
	}
	return ok, nil
}

type condParser struct {
	r    *run
	fr   *frame
	toks []string
	i    int
}

func (cp *condParser) peek() string {
	if cp.i < len(cp.toks) {
		return cp.toks[cp.i]
	}
	return ""
}

func (cp *condParser) expr() (bool, error) {
	ok, err := cp.term()
	if err != nil {
		return false, err
	}
	for {
		op := cp.peek()
		if op != "&&" && op != "||" {
			return ok, nil
		}
		cp.i++
		// mIRC stops evaluating once the answer is known; the rest is
		// still parsed, so its brackets match up.
		skip := op == "&&" && !ok || op == "||" && ok
		right, err := cp.termMaybe(skip)
		if err != nil {
			return false, err
		}
		if !skip {
			ok = right
		}
	}
}

func (cp *condParser) termMaybe(skip bool) (bool, error) {
	if !skip {
		return cp.term()
	}
	depth := 0
	for cp.i < len(cp.toks) {
		switch t := cp.toks[cp.i]; {
		case t == "(":
			depth++
		case t == ")":
			if depth == 0 {
				return false, nil
			}
			depth--
		case depth == 0 && (t == "&&" || t == "||"):
			return false, nil
		}
		cp.i++
	}
	return false, nil
}

func (cp *condParser) term() (bool, error) {
	if cp.peek() == "(" {
		cp.i++
		ok, err := cp.expr()
		if err != nil {
			return false, err
		}
		if cp.peek() != ")" {
			return false, errorf("invalid condition: missing )")
		}
		cp.i++
		return ok, nil
	}
	var words []string
	for cp.i < len(cp.toks) {
		t := cp.toks[cp.i]
		if t == "(" || t == ")" || t == "&&" || t == "||" {
			break
		}
		words = append(words, t)
		cp.i++
	}
	if len(words) == 0 {
		return false, errorf("invalid condition")
	}
	return cp.r.compare(cp.fr, words)
}

// condTokens splits a condition into words, brackets, && and ||. An
// identifier's brackets stay in its word, so "($len($1) > 3)" is (,
// $len($1), >, 3 and ).
func condTokens(s string) []string {
	var out []string
	for i := 0; i < len(s); {
		switch c := s[i]; {
		case c == ' ':
			i++
		case c == '(' || c == ')':
			out = append(out, s[i:i+1])
			i++
		default:
			j := i
			for j < len(s) && s[j] != ' ' && s[j] != ')' {
				if s[j] == '(' {
					if !identBefore(s[i:j]) {
						break
					}
					end := matchParen(s, j)
					if end < 0 {
						j = len(s)
						break
					}
					j = end + 1
					continue
				}
				j++
			}
			if j == i {
				// A ( right after a word that isn't an identifier: take it
				// as a bracket.
				out = append(out, s[i:i+1])
				i++
				continue
			}
			out = append(out, s[i:j])
			i = j
		}
	}
	return out
}

// identBefore reports whether a ( after w opens an identifier's arguments:
// w ends in $name, or is $ itself, as in $(...).
func identBefore(w string) bool {
	k := strings.LastIndexByte(w, '$')
	if k < 0 {
		return false
	}
	for i := k + 1; i < len(w); i++ {
		if !isWordChar(w[i]) && w[i] != '+' {
			return false
		}
	}
	return true
}

// Comparison operators, like mIRC's if-then-else section.
var operators = map[string]bool{
	"==": true, "===": true, "!=": true, "<": true, "<=": true, ">": true, ">=": true,
	"//": true, `\\`: true, "&": true,
	"isin": true, "isincs": true, "iswm": true, "iswmcs": true, "isnum": true,
	"isletter": true, "isalnum": true, "isalpha": true, "islower": true, "isupper": true,
	"ison": true, "isop": true, "ishop": true, "isvoice": true, "isreg": true, "ischan": true,
	"isban": true, "isinvite": true, "isexcept": true, "isaop": true, "isavoice": true,
	"isignore": true, "isprotect": true, "isnotify": true,
}

// optionalV2 are the operators whose v2 can be left out.
var optionalV2 = map[string]bool{
	"isnum": true, "isletter": true, "isalnum": true, "isalpha": true, "islower": true,
	"isupper": true, "ischan": true, "isaop": true, "isavoice": true, "isignore": true,
	"isprotect": true, "isnotify": true,
}

// isOperator reports whether w is a comparison operator, maybe negated
// with !, like !isin.
func isOperator(w string) bool {
	w = strings.ToLower(w)
	return operators[w] || strings.HasPrefix(w, "!") && operators[w[1:]] && w != "!="
}

// compare evaluates one comparison: "v1 op v2", or a single value that's
// true unless it's $null, 0 or $false. It sets $v1 and $v2.
func (r *run) compare(fr *frame, words []string) (bool, error) {
	k := -1
	for i := 1; i < len(words); i++ {
		if isOperator(words[i]) {
			k = i
			break
		}
	}
	if k < 0 {
		neg := false
		text := strings.Join(words, " ")
		if len(words) == 1 && strings.HasPrefix(text, "!") && len(text) > 1 {
			neg, text = true, text[1:]
		}
		v, err := r.eval(fr, text)
		if err != nil {
			return false, err
		}
		fr.v1, fr.v2 = v, ""
		return truthy(v) != neg, nil
	}
	op := strings.ToLower(words[k])
	neg := false
	if op != "!=" && strings.HasPrefix(op, "!") {
		neg, op = true, op[1:]
	}
	if k == len(words)-1 && !optionalV2[op] {
		// "a ==" with nothing after it: mIRC says invalid format.
		return false, errorf("/if: invalid format")
	}
	v1, err := r.eval(fr, strings.Join(words[:k], " "))
	if err != nil {
		return false, err
	}
	v2, err := r.eval(fr, strings.Join(words[k+1:], " "))
	if err != nil {
		return false, err
	}
	fr.v1, fr.v2 = v1, v2
	ok, err := r.compareOp(fr, op, v1, v2)
	return ok != neg, err
}

// truthy is whether a value alone is true: not $null, $false or a zero
// (0, 00, 0.0 and -0 all count, like mIRC).
func truthy(v string) bool {
	if n, ok := parseNum(v); ok {
		return n != 0
	}
	return v != "" && !strings.EqualFold(v, "$false")
}

func (r *run) compareOp(fr *frame, op, v1, v2 string) (bool, error) {
	n1, num1 := parseNum(v1)
	n2, num2 := parseNum(v2)
	nums := num1 && num2
	order := func() int {
		if nums {
			switch {
			case n1 < n2:
				return -1
			case n1 > n2:
				return 1
			}
			return 0
		}
		return strings.Compare(strings.ToLower(v1), strings.ToLower(v2))
	}
	switch op {
	case "==":
		return order() == 0, nil
	case "===":
		// Case-sensitive, but numbers still compare as numbers: 05 === 5.
		if nums {
			return n1 == n2, nil
		}
		return v1 == v2, nil
	case "!=":
		return order() != 0, nil
	case "<":
		return order() < 0, nil
	case "<=":
		return order() <= 0, nil
	case ">":
		return order() > 0, nil
	case ">=":
		return order() >= 0, nil
	case "//", `\\`:
		multiple := num1 && num2 && int64(n1) != 0 && int64(n2)%int64(n1) == 0
		return multiple == (op == "//"), nil
	case "&":
		return num1 && num2 && int64(n1)&int64(n2) != 0, nil
	case "isin":
		return strings.Contains(strings.ToLower(v2), strings.ToLower(v1)), nil
	case "isincs":
		return strings.Contains(v2, v1), nil
	case "iswm":
		return wildMatch(v1, v2, false), nil
	case "iswmcs":
		return wildMatch(v1, v2, true), nil
	case "isnum":
		if !num1 {
			return false, nil
		}
		if v2 == "" {
			return true, nil
		}
		// n1-n2, n1- or n1 (exactly n1). A leading - is a negative n1.
		k := strings.IndexByte(v2[min(1, len(v2)):], '-')
		if k < 0 {
			n, ok := parseNum(v2)
			return ok && n1 == n, nil
		}
		lo, hi := v2[:k+1], v2[k+2:]
		l, okl := parseNum(lo)
		h, okh := parseNum(hi)
		return (!okl || n1 >= l) && (!okh || n1 <= h), nil
	case "isletter":
		// Every character of v1 is a letter, or one of v2's.
		return v1 != "" && strings.IndexFunc(v1, func(c rune) bool {
			if v2 == "" {
				return !unicode.IsLetter(c)
			}
			return !strings.ContainsRune(v2, c)
		}) < 0, nil
	case "isalnum", "isalpha", "islower", "isupper":
		return v1 != "" && strings.IndexFunc(v1, func(c rune) bool {
			switch op {
			case "isalnum":
				return !unicode.IsLetter(c) && !unicode.IsDigit(c)
			case "isalpha":
				return !unicode.IsLetter(c)
			case "islower":
				return !unicode.IsLower(c)
			}
			return !unicode.IsUpper(c)
		}) < 0, nil
	case "ison":
		_, ok := r.nickOn(fr.network, v2, v1)
		return ok, nil
	case "isop":
		return r.prefixed(fr.network, v2, v1, "~&@"), nil
	case "ishop":
		return r.prefixed(fr.network, v2, v1, "%"), nil
	case "isvoice":
		return r.prefixed(fr.network, v2, v1, "+"), nil
	case "isreg":
		n, ok := r.nickOn(fr.network, v2, v1)
		return ok && n.Prefix == "", nil
	case "ischan":
		_, ok := r.nickOn(fr.network, v1, r.h.Me(fr.network))
		return ok, nil
	}
	return false, errorf(op, " isn't supported yet")
}

// nickOn finds nick in channel's nicklist.
func (r *run) nickOn(network, channel, nick string) (Nick, bool) {
	for _, n := range r.h.Nicks(network, channel) {
		if strings.EqualFold(n.Name, nick) {
			return n, true
		}
	}
	return Nick{}, false
}

// prefixed reports whether nick is on channel with one of the mode
// prefixes. Like mIRC, owners (~) and admins (&) count as ops too.
func (r *run) prefixed(network, channel, nick, prefixes string) bool {
	n, ok := r.nickOn(network, channel, nick)
	return ok && strings.ContainsAny(n.Prefix, prefixes)
}
