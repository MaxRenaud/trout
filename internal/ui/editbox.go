package ui

import "github.com/mattn/go-runewidth"

// ctrlGlyph is how mIRC formatting codes show up while typing.
var ctrlGlyph = map[rune]rune{0x02: 'B', 0x03: 'K', 0x0f: 'O', 0x16: 'R', 0x1d: 'I', 0x1f: 'U'}

func editWidth(r rune) int {
	if _, ok := ctrlGlyph[r]; ok {
		return 1
	}
	return runewidth.RuneWidth(r)
}
