package state

import (
	"slices"
	"unicode/utf8"

	"github.com/clipperhouse/uax29/v2/graphemes"
)

// Clusters are what the cursor steps over: a terminal draws a grapheme
// cluster (an emoji with its skin tone, a letter with its accents) as one
// glyph, so the cursor never stops inside one, and Backspace and Delete
// take it whole.

// boundaries is where buf's clusters start, as rune indexes, ending with
// len(buf).
func boundaries(buf []rune) []int {
	out := []int{0}
	g := graphemes.FromString(string(buf))
	i := 0
	for g.Next() {
		i += utf8.RuneCountInString(g.Value())
		out = append(out, i)
	}
	if out[len(out)-1] != len(buf) { // can't happen, but never lose the end
		out = append(out, len(buf))
	}
	return out
}

// Step is the cursor position d clusters from pos.
func Step(buf []rune, pos, d int) int {
	b := boundaries(buf)
	i := 0
	for i < len(b)-1 && b[i+1] <= pos {
		i++
	}
	if d < 0 && b[i] < pos { // inside a cluster: its start is one step back
		d++
	}
	return b[max(0, min(len(b)-1, i+d))]
}

// Snap is pos moved back to the start of the cluster it's in.
func Snap(buf []rune, pos int) int {
	b := boundaries(buf)
	for _, v := range slices.Backward(b) {
		if v <= pos {
			return v
		}
	}
	return 0
}
