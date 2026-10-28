package state

import (
	"slices"
	"strings"
)

// EditBox is a window's single-line input with history and Tab completion.
type EditBox struct {
	Buf []rune
	Pos int
	Off int // first visible rune when the text is wider than the box

	hist []string
	hi   int

	tabList  []string // completion candidates; nil when not completing
	tabStart int
	tabI     int
}

func (e *EditBox) Insert(rs ...rune) {
	e.Buf = slices.Insert(e.Buf, e.Pos, rs...)
	e.Pos += len(rs)
}

func (e *EditBox) Backspace() {
	if e.Pos > 0 {
		e.Buf = slices.Delete(e.Buf, e.Pos-1, e.Pos)
		e.Pos--
	}
}

func (e *EditBox) Delete() {
	if e.Pos < len(e.Buf) {
		e.Buf = slices.Delete(e.Buf, e.Pos, e.Pos+1)
	}
}

func (e *EditBox) Move(d int) { e.Pos = clamp(e.Pos+d, 0, len(e.Buf)) }

// Take returns the text, clears the box and records it in the history.
func (e *EditBox) Take() string {
	s := string(e.Buf)
	e.Buf, e.Pos, e.Off = nil, 0, 0
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
}

// ResetTab ends a Tab completion cycle.
func (e *EditBox) ResetTab() { e.tabList = nil }

// Complete replaces the word before the cursor with the next candidate that
// starts with it; pressing Tab again cycles through the matches.
func (e *EditBox) Complete(cands []string) {
	if e.tabList == nil {
		start := e.Pos
		for start > 0 && e.Buf[start-1] != ' ' {
			start--
		}
		prefix := strings.ToLower(string(e.Buf[start:e.Pos]))
		if prefix == "" {
			return
		}
		for _, c := range cands {
			if strings.HasPrefix(strings.ToLower(c), prefix) {
				e.tabList = append(e.tabList, c)
			}
		}
		if e.tabList == nil {
			return
		}
		e.tabStart, e.tabI = start, -1
	}
	e.tabI = (e.tabI + 1) % len(e.tabList)
	word := []rune(e.tabList[e.tabI])
	e.Buf = slices.Concat(e.Buf[:e.tabStart], word, e.Buf[e.Pos:])
	e.Pos = e.tabStart + len(word)
}
