package script

import (
	"strings"
	"unicode/utf8"
)

// wildMatch matches text against a mIRC wildcard pattern: * is any text,
// ? any one character and & any one word. The whole text must match, so
// "hello" only matches a line that's just hello, like on TEXT's matchtext.
func wildMatch(pattern, text string, caseSensitive bool) bool {
	if !caseSensitive {
		pattern, text = strings.ToLower(pattern), strings.ToLower(text)
	}
	return wild(pattern, text)
}

// wild matches without recursion: on a mismatch it backtracks only to the
// last *, which takes one more character. At worst that's the pattern's
// length times the text's, so matchtext with many * can't stall the
// client on a long line.
func wild(p, t string) bool {
	pi, ti := 0, 0
	starP, starT := -1, 0
	for ti < len(t) {
		if pi < len(p) {
			switch p[pi] {
			case '*':
				starP, starT = pi, ti
				pi++
				continue
			case '?':
				_, n := utf8.DecodeRuneInString(t[ti:])
				pi, ti = pi+1, ti+n
				continue
			case '&':
				// A whole word: everything up to the next space.
				if n := wordLen(t[ti:]); n > 0 {
					pi, ti = pi+1, ti+n
					continue
				}
			default:
				if p[pi] == t[ti] {
					pi, ti = pi+1, ti+1
					continue
				}
			}
		}
		if starP < 0 {
			return false
		}
		_, n := utf8.DecodeRuneInString(t[starT:])
		starT += n
		pi, ti = starP+1, starT
	}
	for pi < len(p) && p[pi] == '*' {
		pi++
	}
	return pi == len(p)
}

func wordLen(s string) int {
	n := 0
	for n < len(s) && s[n] != ' ' {
		n++
	}
	return n
}
