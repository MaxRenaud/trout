package ui

import (
	"slices"
	"unicode/utf16"

	"github.com/MaxRenaud/trout/internal/state"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"
)

// ctrlGlyph is how mIRC formatting codes show up while typing.
var ctrlGlyph = map[rune]rune{0x02: 'B', 0x03: 'K', 0x0f: 'O', 0x16: 'R', 0x1d: 'I', 0x1e: 'E', 0x1f: 'U'}

func editWidth(r rune) int {
	if _, ok := ctrlGlyph[r]; ok {
		return 1
	}
	return runewidth.RuneWidth(r)
}

// boxRuneWidth is r's width in a text box; masked boxes show one dot per
// rune.
func boxRuneWidth(r rune, mask bool) int {
	if mask {
		return 1
	}
	return editWidth(r)
}

// clickEditBox moves the cursor to where a text box was clicked, rel
// columns from its left edge.
func clickEditBox(e *state.EditBox, rel int, mask bool) {
	i, cx := e.Off, 1
	for i < len(e.Buf) {
		cw := boxRuneWidth(e.Buf[i], mask)
		if cx+cw > rel {
			break
		}
		cx += cw
		i++
	}
	e.Pos = i
}

// typedRunes is the text of a key press that types something, with line
// breaks and tabs from a paste turned into spaces.
func typedRunes(k tea.KeyMsg) ([]rune, bool) {
	if (k.Type != tea.KeyRunes && k.Type != tea.KeySpace) || k.Alt {
		return nil, false
	}
	rs := slices.Clone(k.Runes)
	for i, r := range rs {
		if r == '\n' || r == '\r' || r == '\t' {
			rs[i] = ' '
		}
	}
	return rs, true
}

// joinSurrogates puts characters outside the BMP (emoji) back together on
// Windows, where Bubble Tea hands over the console's UTF-16 one unit at a
// time: a high surrogate waits for the low one that follows. A half on its
// own is dropped; it isn't a character. ok is false when nothing's left.
func (a *App) joinSurrogates(k tea.KeyMsg) (tea.KeyMsg, bool) {
	if k.Type != tea.KeyRunes || !slices.ContainsFunc(k.Runes, utf16.IsSurrogate) && a.highHalf == 0 {
		return k, true
	}
	var rs []rune
	for _, r := range k.Runes {
		switch {
		case r >= 0xd800 && r < 0xdc00: // high: wait for the low half
			a.highHalf = r
		case r >= 0xdc00 && r < 0xe000:
			if a.highHalf != 0 {
				rs = append(rs, utf16.DecodeRune(a.highHalf, r))
			}
			a.highHalf = 0
		default:
			a.highHalf = 0
			rs = append(rs, r)
		}
	}
	k.Runes = rs
	return k, len(rs) > 0
}
