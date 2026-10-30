package script

import (
	"cmp"
	"slices"
	"strings"
)

// Token identifiers, from mIRC's Token Identifiers section: text split at
// a separator given by its character code, like $gettok(a.b.c,2,46).
var tokenIdents = map[string]builtin{
	"gettok": func(r *run, fr *frame, c *call) (string, error) {
		toks, sep, err := c.tokens(0, 2)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(c.arg(1)) == "0" {
			return itoa(len(toks)), nil
		}
		from, to, ok := tokRange(c.arg(1), len(toks))
		if !ok {
			return "", nil
		}
		return strings.Join(toks[from:to], sep), nil
	},
	"numtok": func(r *run, fr *frame, c *call) (string, error) {
		toks, _, err := c.tokens(0, 1)
		return itoa(len(toks)), err
	},
	"addtok":   addtok(false),
	"addtokcs": addtok(true),
	"deltok": func(r *run, fr *frame, c *call) (string, error) {
		toks, sep, err := c.tokens(0, 2)
		if err != nil {
			return "", err
		}
		from, to, ok := tokRange(c.arg(1), len(toks))
		if !ok {
			return strings.Join(toks, sep), nil
		}
		return strings.Join(slices.Delete(toks, from, to), sep), nil
	},
	"findtok":   findtok(false),
	"findtokcs": findtok(true),
	"instok": func(r *run, fr *frame, c *call) (string, error) {
		toks, sep, err := c.tokens(0, 3)
		if err != nil {
			return "", err
		}
		n := int(c.num(2))
		switch {
		case n == 0:
			n = len(toks) + 1 // at the end, like mIRC
		case n < 0:
			n = len(toks) + n + 1
		}
		i := clamp(n-1, 0, len(toks))
		return strings.Join(slices.Insert(toks, i, c.arg(1)), sep), nil
	},
	"istok": func(r *run, fr *frame, c *call) (string, error) {
		toks, _, err := c.tokens(0, 2)
		return boolStr(slices.ContainsFunc(toks, eqTok(c.arg(1), false))), err
	},
	"istokcs": func(r *run, fr *frame, c *call) (string, error) {
		toks, _, err := c.tokens(0, 2)
		return boolStr(slices.Contains(toks, c.arg(1))), err
	},
	"matchtok":   matchtok(false, false),
	"matchtokcs": matchtok(true, false),
	"wildtok":    matchtok(false, true),
	"wildtokcs":  matchtok(true, true),
	"puttok": func(r *run, fr *frame, c *call) (string, error) {
		toks, sep, err := c.tokens(0, 3)
		if err != nil {
			return "", err
		}
		if i, ok := tokIndex(int(c.num(2)), len(toks)); ok {
			toks[i] = c.arg(1)
		}
		return strings.Join(toks, sep), nil
	},
	"remtok":    remtok(false),
	"remtokcs":  remtok(true),
	"reptok":    reptok(false),
	"reptokcs":  reptok(true),
	"sorttok":   sorttok(false),
	"sorttokcs": sorttok(true),
}

// tokens splits argument i at the separator in argument sep. A bad
// separator makes the identifier $null, like mIRC.
func (c *call) tokens(i, sep int) ([]string, string, error) {
	s, ok := charCode(c.arg(sep))
	if !ok {
		return nil, "", errNull
	}
	return tokensOf(c.arg(i), s), s, nil
}

// tokIndex turns a 1-based token number, or a negative one counting from
// the end, into an index.
func tokIndex(n, count int) (int, bool) {
	if n < 0 {
		n = count + n + 1
	}
	return n - 1, n >= 1 && n <= count
}

// tokRange reads N, N-, or N-M (N may be negative) as a slice range.
func tokRange(s string, count int) (from, to int, ok bool) {
	s = strings.TrimSpace(s)
	// A leading - is a negative N, not a range.
	k := strings.Index(s[min(1, len(s)):], "-")
	if k < 0 {
		i, ok := tokIndex(atoi(s), count)
		return i, i + 1, ok
	}
	k++
	from = atoi(s[:k])
	if from < 0 {
		from = count + from + 1
	}
	to = count
	if s[k+1:] != "" {
		to = atoi(s[k+1:])
	}
	// Like mIRC, N-M works either way round (2-1 is 1-2), but N- past
	// the end is nothing.
	if s[k+1:] != "" {
		from, to = min(from, to), max(from, to)
	}
	from, to = max(from, 1), min(to, count)
	if from > to {
		return 0, 0, false
	}
	return from - 1, to, true
}

func eqTok(tok string, cs bool) func(string) bool {
	return func(t string) bool {
		if cs {
			return t == tok
		}
		return strings.EqualFold(t, tok)
	}
}

// $addtok(text,token,C) adds token at the end unless it's there already.
func addtok(cs bool) builtin {
	return func(r *run, fr *frame, c *call) (string, error) {
		toks, sep, err := c.tokens(0, 2)
		if err != nil {
			return "", err
		}
		if !slices.ContainsFunc(toks, eqTok(c.arg(1), cs)) {
			toks = append(toks, c.arg(1))
		}
		return strings.Join(toks, sep), nil
	}
}

// $findtok(text,token,N,C) is the position of the Nth matching token, or
// with N = 0 how many match.
func findtok(cs bool) builtin {
	return func(r *run, fr *frame, c *call) (string, error) {
		toks, _, err := c.tokens(0, 3)
		if err != nil {
			return "", err
		}
		want, n := int(c.num(2)), 0
		for i, t := range toks {
			if eqTok(c.arg(1), cs)(t) {
				n++
				if n == want {
					return itoa(i + 1), nil
				}
			}
		}
		if want == 0 {
			return itoa(n), nil
		}
		return "", nil
	}
}

// $matchtok(tokens,string,N,C) is the Nth token containing string, and
// $wildtok the Nth matching a wildcard; with N = 0, how many there are.
func matchtok(cs, wild bool) builtin {
	return func(r *run, fr *frame, c *call) (string, error) {
		toks, _, err := c.tokens(0, 3)
		if err != nil {
			return "", err
		}
		want, n := int(c.num(2)), 0
		for _, t := range toks {
			var ok bool
			switch {
			case wild:
				ok = wildMatch(c.arg(1), t, cs)
			case cs:
				ok = strings.Contains(t, c.arg(1))
			default:
				ok = strings.Contains(strings.ToLower(t), strings.ToLower(c.arg(1)))
			}
			if ok {
				n++
				if n == want {
					return t, nil
				}
			}
		}
		if want == 0 {
			return itoa(n), nil
		}
		return "", nil
	}
}

// $remtok(text,token,N,C) removes the Nth matching token, or all of them
// with N = 0. The older $remtok(text,token,C) removes the first.
func remtok(cs bool) builtin {
	return func(r *run, fr *frame, c *call) (string, error) {
		sepArg, want := 3, int(c.num(2))
		if len(c.vals) == 3 {
			sepArg, want = 2, 1
		}
		toks, sep, err := c.tokens(0, sepArg)
		if err != nil {
			return "", err
		}
		var out []string
		n := 0
		for _, t := range toks {
			if eqTok(c.arg(1), cs)(t) {
				n++
				if want == 0 || n == want {
					continue
				}
			}
			out = append(out, t)
		}
		return strings.Join(out, sep), nil
	}
}

// $reptok(text,token,new,N,C) replaces the Nth matching token, or all of
// them with N = 0. The older $reptok(text,token,new,C) replaces the first.
func reptok(cs bool) builtin {
	return func(r *run, fr *frame, c *call) (string, error) {
		sepArg, want := 4, int(c.num(3))
		if len(c.vals) == 4 {
			sepArg, want = 3, 1
		}
		toks, sep, err := c.tokens(0, sepArg)
		if err != nil {
			return "", err
		}
		n := 0
		for i, t := range toks {
			if eqTok(c.arg(1), cs)(t) {
				n++
				if want == 0 || n == want {
					toks[i] = c.arg(2)
				}
			}
		}
		return strings.Join(toks, sep), nil
	}
}

// $sorttok(text,C,ncra) sorts tokens: alphabetically, or n numerically, c
// by nick prefix (ops, then voices, then the rest), r reversed. a
// (alphanumeric) sorts alphabetically.
func sorttok(cs bool) builtin {
	return func(r *run, fr *frame, c *call) (string, error) {
		toks, sep, err := c.tokens(0, 1)
		if err != nil {
			return "", err
		}
		opts := c.arg(2)
		slices.SortStableFunc(toks, func(a, b string) int {
			if strings.Contains(opts, "n") {
				x, _ := parseNum(a)
				y, _ := parseNum(b)
				return cmp.Compare(x, y)
			}
			if strings.Contains(opts, "c") {
				if d := cmp.Compare(prefixRank(a), prefixRank(b)); d != 0 {
					return d
				}
				a, b = strings.TrimLeft(a, nickPrefixes), strings.TrimLeft(b, nickPrefixes)
			}
			if !cs {
				a, b = strings.ToLower(a), strings.ToLower(b)
			}
			return strings.Compare(a, b)
		})
		if strings.Contains(opts, "r") {
			slices.Reverse(toks)
		}
		return strings.Join(toks, sep), nil
	}
}

// nickPrefixes are channel mode prefixes, highest first.
const nickPrefixes = "~&@%+"

// prefixRank orders a nick by its prefix: owners first, then down to
// nicks with none.
func prefixRank(nick string) int {
	if nick != "" {
		if i := strings.IndexByte(nickPrefixes, nick[0]); i >= 0 {
			return i
		}
	}
	return len(nickPrefixes)
}
