package script

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// Text identifiers, from mIRC's Text and Number Identifiers section. They
// count characters, not bytes, and compare without case unless they end
// in cs.
var textIdents = map[string]builtin{
	"len": func(r *run, fr *frame, c *call) (string, error) {
		return itoa(utf8.RuneCountInString(c.arg(0))), nil
	},
	"upper": func(r *run, fr *frame, c *call) (string, error) {
		return strings.ToUpper(c.arg(0)), nil
	},
	"lower": func(r *run, fr *frame, c *call) (string, error) {
		return strings.ToLower(c.arg(0)), nil
	},
	// $left(text,N): the first N characters, or all but the last -N.
	"left": func(r *run, fr *frame, c *call) (string, error) {
		t := []rune(c.arg(0))
		n := int(c.num(1))
		if n < 0 {
			n = len(t) + n
		}
		return string(t[:clamp(n, 0, len(t))]), nil
	},
	// $right(text,N): the last N characters, or all but the first -N.
	"right": func(r *run, fr *frame, c *call) (string, error) {
		t := []rune(c.arg(0))
		n := int(c.num(1))
		if n < 0 {
			n = len(t) + n
		}
		return string(t[len(t)-clamp(n, 0, len(t)):]), nil
	},
	// $mid(text,S,N): N characters from position S. S < 0 counts from the
	// end; with no N it goes to the end, and N < 0 stops that many before
	// it. N = 0 is how many characters there are from S to the end.
	"mid": func(r *run, fr *frame, c *call) (string, error) {
		t := []rune(c.arg(0))
		s := int(c.num(1))
		if s < 0 {
			s = len(t) + s + 1
		}
		start := clamp(s-1, 0, len(t))
		end := len(t)
		if len(c.vals) > 2 && strings.TrimSpace(c.arg(2)) == "0" {
			return itoa(end - start), nil
		}
		if n := int(c.num(2)); n > 0 {
			end = clamp(start+n, start, len(t))
		} else if n < 0 {
			end = clamp(len(t)+n, start, len(t))
		}
		return string(t[start:end]), nil
	},
	"replace":   replaceIdent(false),
	"replacecs": replaceIdent(true),
	"remove":    removeIdent(false),
	"removecs":  removeIdent(true),
	"str": func(r *run, fr *frame, c *call) (string, error) {
		return strings.Repeat(c.arg(0), clamp(int(c.num(1)), 0, 10000)), nil
	},
	// $strip(text,buriec) strips only those: bold, underline, reverse,
	// italic, strikethrough (e) and color.
	"strip": func(r *run, fr *frame, c *call) (string, error) {
		return stripCodes(c.arg(0), c.arg(1)), nil
	},
	"pos":   posIdent(false),
	"poscs": posIdent(true),
	// $count(text,sub1,sub2...): how many times the subs appear.
	"count":   countIdent(false),
	"countcs": countIdent(true),
	"chr": func(r *run, fr *frame, c *call) (string, error) {
		n := int(c.num(0))
		if n <= 0 {
			return "", nil
		}
		return string(rune(n)), nil
	},
	"asc": func(r *run, fr *frame, c *call) (string, error) {
		ch, _ := utf8.DecodeRuneInString(c.arg(0))
		if ch == utf8.RuneError {
			return "", nil
		}
		return itoa(int(ch)), nil
	},
}

func clamp(n, lo, hi int) int { return max(lo, min(n, hi)) }

// indexFold is strings.Index, ignoring case unless cs.
func indexFold(s, sub string, cs bool) int {
	if cs {
		return strings.Index(s, sub)
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if strings.EqualFold(s[i:i+len(sub)], sub) {
			return i
		}
	}
	return -1
}

// replaceAll replaces every sub in s, ignoring case unless cs.
func replaceAll(s, sub, with string, cs bool) string {
	if sub == "" {
		return s
	}
	var b strings.Builder
	for {
		i := indexFold(s, sub, cs)
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:i])
		b.WriteString(with)
		s = s[i+len(sub):]
	}
}

// $replace(text,a,A,b,B...) makes each replacement in turn.
func replaceIdent(cs bool) builtin {
	return func(r *run, fr *frame, c *call) (string, error) {
		s := c.arg(0)
		for i := 1; i+1 < len(c.vals); i += 2 {
			s = replaceAll(s, c.vals[i], c.vals[i+1], cs)
		}
		return s, nil
	}
}

func removeIdent(cs bool) builtin {
	return func(r *run, fr *frame, c *call) (string, error) {
		s := c.arg(0)
		for _, sub := range c.vals[min(1, len(c.vals)):] {
			s = replaceAll(s, sub, "", cs)
		}
		return s, nil
	}
}

// $pos(text,sub,N): the character position of the Nth sub, or with N = 0
// how many there are.
func posIdent(cs bool) builtin {
	return func(r *run, fr *frame, c *call) (string, error) {
		s, sub := c.arg(0), c.arg(1)
		if sub == "" {
			return "", nil
		}
		want := 1
		if len(c.vals) > 2 {
			want = int(c.num(2))
		}
		count, off := 0, 0
		for {
			i := indexFold(s[off:], sub, cs)
			if i < 0 {
				break
			}
			count++
			if count == want {
				return itoa(utf8.RuneCountInString(s[:off+i]) + 1), nil
			}
			off += i + len(sub)
		}
		if want == 0 {
			return itoa(count), nil
		}
		return "", nil
	}
}

func countIdent(cs bool) builtin {
	return func(r *run, fr *frame, c *call) (string, error) {
		s, n := c.arg(0), 0
		for _, sub := range c.vals[min(1, len(c.vals)):] {
			if sub == "" {
				continue
			}
			for off := 0; ; {
				i := indexFold(s[off:], sub, cs)
				if i < 0 {
					break
				}
				n++
				off += i + len(sub)
			}
		}
		return itoa(n), nil
	}
}

// stripCodes removes IRC formatting: bold, underline, italic, reverse,
// strikethrough, monospace, reset and colors, or with only (like
// $strip's "buriec") just some of them.
func stripCodes(s, only string) string {
	strip := func(kind byte) bool { return only == "" || strings.IndexByte(only, kind) >= 0 }
	codes := map[byte]byte{0x02: 'b', 0x1f: 'u', 0x16: 'r', 0x1d: 'i', 0x1e: 'e', 0x03: 'c', 0x04: 'c'}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if kind, ok := codes[s[i]]; ok && !strip(kind) || (s[i] == 0x0f || s[i] == 0x11) && only != "" {
			b.WriteByte(s[i])
			continue
		}
		switch s[i] {
		case 0x02, 0x1f, 0x1d, 0x16, 0x1e, 0x11, 0x0f:
		case 0x03:
			// \x03 then up to two digits, then maybe a comma and two more.
			j := i + 1
			j += digitsAt(s, j, 2)
			if j < len(s) && s[j] == ',' && digitsAt(s, j+1, 2) > 0 {
				j += 1 + digitsAt(s, j+1, 2)
			}
			i = j - 1
		case 0x04:
			// \x04 then a six-digit hex color, maybe ,background.
			j := i + 1 + hexAt(s, i+1, 6)
			if j < len(s) && s[j] == ',' && hexAt(s, j+1, 6) > 0 {
				j += 1 + hexAt(s, j+1, 6)
			}
			i = j - 1
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

func digitsAt(s string, i, most int) int {
	n := 0
	for n < most && i+n < len(s) && s[i+n] >= '0' && s[i+n] <= '9' {
		n++
	}
	return n
}

func hexAt(s string, i, most int) int {
	n := 0
	for n < most && i+n < len(s) && strings.ContainsRune("0123456789abcdefABCDEF", rune(s[i+n])) {
		n++
	}
	return n
}

// charCode reads a token identifier's C: the character code of the
// separator, like 32 for a space.
func charCode(s string) (string, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n <= 0 {
		return "", false
	}
	return string(rune(n)), true
}

// tokensOf splits text into tokens at sep. Like mIRC, empty tokens don't
// count: "a..b" with . is two tokens.
func tokensOf(text, sep string) []string {
	var out []string
	for t := range strings.SplitSeq(text, sep) {
		if t != "" {
			out = append(out, t)
		}
	}
	return out
}
