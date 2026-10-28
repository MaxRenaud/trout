package state

import (
	"slices"
	"strings"
)

// EditBox is a window's input with history and Tab completion. It's one
// line unless the text has line breaks (mIRC's multi-line editbox), and
// can have a selection, from Anchor to Pos.
type EditBox struct {
	Buf []rune
	Pos int
	Off int // first visible rune when the text is wider than the box
	Top int // first visible row of a multi-line box

	Sel    bool // a selection from Anchor to Pos
	Anchor int

	hist []string
	hi   int

	tabList  []string // completion candidates; nil when not completing
	tabStart int
	tabI     int
}

// Insert types rs at the cursor, over the selection if there is one.
func (e *EditBox) Insert(rs ...rune) {
	e.DeleteSel()
	e.Buf = slices.Insert(e.Buf, e.Pos, rs...)
	e.Pos += len(rs)
}

func (e *EditBox) Backspace() {
	if e.DeleteSel() {
		return
	}
	if e.Pos > 0 {
		e.Buf = slices.Delete(e.Buf, e.Pos-1, e.Pos)
		e.Pos--
	}
}

func (e *EditBox) Delete() {
	if e.DeleteSel() {
		return
	}
	if e.Pos < len(e.Buf) {
		e.Buf = slices.Delete(e.Buf, e.Pos, e.Pos+1)
	}
}

func (e *EditBox) Move(d int) {
	e.Sel = false
	e.Pos = clamp(e.Pos+d, 0, len(e.Buf))
}

// Selection is the selected runes' range, if any.
func (e *EditBox) Selection() (from, to int, ok bool) {
	if !e.Sel || e.Anchor == e.Pos {
		return 0, 0, false
	}
	return min(e.Anchor, e.Pos), max(e.Anchor, e.Pos), true
}

// SelText is the selected text, or "".
func (e *EditBox) SelText() string {
	from, to, ok := e.Selection()
	if !ok {
		return ""
	}
	return string(e.Buf[from:to])
}

// Extend moves the cursor with move, selecting from where it was (Shift
// with the arrow keys, or a drag).
func (e *EditBox) Extend(move func()) {
	if !e.Sel {
		e.Sel, e.Anchor = true, e.Pos
	}
	move()
	e.Pos = clamp(e.Pos, 0, len(e.Buf))
}

// DeleteSel removes the selection, reporting whether there was one.
func (e *EditBox) DeleteSel() bool {
	from, to, ok := e.Selection()
	e.Sel = false
	if !ok {
		return false
	}
	e.Buf = slices.Delete(e.Buf, from, to)
	e.Pos = from
	return true
}

// Wrap puts open and close around the selection, like mIRC does when you
// press Ctrl+B with text selected. With no selection it just types open.
// The cursor ends up after close, or for a color (close 0x03) right after
// open, where the color number goes.
func (e *EditBox) Wrap(open, close rune) {
	from, to, ok := e.Selection()
	if !ok {
		e.Insert(open)
		return
	}
	e.Sel = false
	e.Buf = slices.Insert(e.Buf, to, close)
	e.Buf = slices.Insert(e.Buf, from, open)
	e.Pos = to + 2
	if close == 0x03 {
		e.Pos = from + 1
	}
}

// Line is the start and end of the line the cursor is on, between line
// breaks.
func (e *EditBox) Line() (start, end int) {
	start, end = e.Pos, e.Pos
	for start > 0 && e.Buf[start-1] != '\n' {
		start--
	}
	for end < len(e.Buf) && e.Buf[end] != '\n' {
		end++
	}
	return start, end
}

// LineMove moves the cursor d lines up or down in a multi-line box,
// keeping its column where it can. It reports whether there was a line
// to go to.
func (e *EditBox) LineMove(d int) bool {
	start, end := e.Line()
	col := e.Pos - start
	switch {
	case d < 0 && start > 0:
		e.Pos = start - 1
		ps, _ := e.Line()
		e.Pos = min(ps+col, start-1)
	case d > 0 && end < len(e.Buf):
		e.Pos = end + 1
		_, ne := e.Line()
		e.Pos = min(end+1+col, ne)
	default:
		return false
	}
	e.Sel = false
	return true
}

// Multi reports whether the text has line breaks.
func (e *EditBox) Multi() bool { return slices.Contains(e.Buf, '\n') }

// Take returns the text, clears the box and records it in the history.
func (e *EditBox) Take() string {
	s := string(e.Buf)
	e.Buf, e.Pos, e.Off, e.Top, e.Sel = nil, 0, 0, 0, false
	if s != "" {
		e.hist = append(e.hist, s)
	}
	e.hi = len(e.hist)
	return s
}

func (e *EditBox) History(d int) {
	if len(e.hist) == 0 {
		return
	}
	e.hi = clamp(e.hi+d, 0, len(e.hist))
	e.Buf = nil
	if e.hi < len(e.hist) {
		e.Buf = []rune(e.hist[e.hi])
	}
	e.Pos = len(e.Buf)
	e.Sel = false
}

// ResetTab ends a Tab completion cycle.
func (e *EditBox) ResetTab() { e.tabList = nil }

// Complete replaces the word before the cursor with the next candidate that
// starts with it, or, if it has * or ? in it, that matches it as a
// wildcard, like mIRC's; pressing Tab again cycles through the matches.
// At the start of the line, suffix (if given) says what to add after a
// word, like ": " after a nick.
func (e *EditBox) Complete(cands []string, suffix func(word string) string) {
	if e.tabList == nil {
		start := e.Pos
		for start > 0 && e.Buf[start-1] != ' ' && e.Buf[start-1] != '\n' {
			start--
		}
		prefix := strings.ToLower(string(e.Buf[start:e.Pos]))
		if prefix == "" {
			return
		}
		wild := strings.ContainsAny(prefix, "*?")
		for _, c := range cands {
			lc := strings.ToLower(c)
			if wild && Wild(prefix, lc) || !wild && strings.HasPrefix(lc, prefix) {
				e.tabList = append(e.tabList, c)
			}
		}
		if e.tabList == nil {
			return
		}
		e.tabStart, e.tabI = start, -1
	}
	e.tabI = (e.tabI + 1) % len(e.tabList)
	text := e.tabList[e.tabI]
	if e.tabStart == 0 && suffix != nil {
		text += suffix(text)
	}
	word := []rune(text)
	e.Buf = slices.Concat(e.Buf[:e.tabStart], word, e.Buf[e.Pos:])
	e.Pos = e.tabStart + len(word)
}

// Wild matches s against pattern, where * is any run of characters and ?
// any one, like mIRC's wildcards. Unlike path.Match, [ and ] are plain
// characters, since nicks have them.
func Wild(pattern, s string) bool {
	p, t := []rune(pattern), []rune(s)
	pi, ti, star, mark := 0, 0, -1, 0
	for ti < len(t) {
		switch {
		case pi < len(p) && (p[pi] == '?' || p[pi] == t[ti]):
			pi++
			ti++
		case pi < len(p) && p[pi] == '*':
			star, mark = pi, ti
			pi++
		case star >= 0:
			pi, mark = star+1, mark+1
			ti = mark
		default:
			return false
		}
	}
	for pi < len(p) && p[pi] == '*' {
		pi++
	}
	return pi == len(p)
}
