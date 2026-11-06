package ui

import (
	"strings"
	"testing"

	"github.com/MaxRenaud/trout/internal/state"

	"github.com/mattn/go-runewidth"
)

// plainLines is a frame's lines without their colors.
func plainLines(s string) []string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = sgrRe.ReplaceAllString(l, "")
	}
	return lines
}

// TestCanvasClusters draws what terminals show as one glyph of several
// runes: a skin-toned emoji takes two cells, not four, a combining accent
// stays on its letter, and a tab is a space.
func TestCanvasClusters(t *testing.T) {
	c := NewCanvas(12, 1, Style{})
	n := c.Text(0, 0, 12, "a👍🏽b\téx👨‍👩‍👧", Style{})
	if n != 9 {
		t.Errorf("drew %d cells, want 9", n)
	}
	got := plainLines(c.String())[0]
	if want := "a👍🏽b éx👨‍👩‍👧   "; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if w := runewidth.StringWidth(got); w != 12 {
		t.Errorf("the line is %d cells wide, want 12", w)
	}
	if c.cells[3].R != 'b' {
		t.Errorf("cell 3 is %q, want b", c.cells[3].R)
	}

	// Drawing over half of a wide cluster blanks the other half.
	c.Set(2, 0, 'z', Style{})
	if got := plainLines(c.String())[0]; !strings.HasPrefix(got, "a z") {
		t.Errorf("overdrawn: %q", got)
	}

	// Controls and lone marks at the start of a row still draw nothing.
	c = NewCanvas(4, 1, Style{})
	if n := c.Text(0, 0, 4, "́‮\x01ab", Style{}); n != 2 {
		t.Errorf("drew %d cells, want 2", n)
	}
}

// TestWrapKeepsClusters doesn't split an emoji from its skin tone.
func TestWrapKeepsClusters(t *testing.T) {
	line := parseMIRC("ab👍🏽cd", Style{}, &classicTheme.Pal)
	rows := wrap(line, 4, 0)
	var got []string
	for _, r := range rows {
		var b strings.Builder
		for _, sr := range r {
			b.WriteRune(sr.R)
		}
		got = append(got, b.String())
	}
	if strings.Join(got, "|") != "ab👍🏽|cd" {
		t.Errorf("rows %q", got)
	}
}

// TestClusterFrame: a line with a skin-toned emoji still fills the screen
// exactly, so the scrollbar and nicklist stay put.
func TestClusterFrame(t *testing.T) {
	a, n, feed := realApp(t)
	ch := n.Find(state.KChannel, "#go")
	a.activate(ch)
	feed(":bob!b@h PRIVMSG #go :nice 👍🏽 café\tok")
	for i, l := range plainLines(a.View()) {
		if w := runewidth.StringWidth(l); w != a.w {
			t.Errorf("line %d is %d cells wide, want %d: %q", i, w, a.w, l)
		}
	}
}

// TestCopyKeepsMarks copies a combining accent with the letter it's on.
func TestCopyKeepsMarks(t *testing.T) {
	a, n, feed := realApp(t)
	ch := n.Find(state.KChannel, "#go")
	a.activate(ch)
	a.stamps = false
	feed(":bob!b@h PRIVMSG #go :café ok")
	a.View()
	rows, _ := a.windowRows(ch, a.msgW)
	last := len(rows) - 1
	a.sel = &selection{win: ch, ar: last, ac: 0, br: last, bc: 9, moved: true}
	if got := a.selectedText(); got != "<bob> café" {
		t.Errorf("copied %q", got)
	}
	a.sel.ac, a.sel.bc = 10, 30
	if got := a.selectedText(); got != " ok" {
		t.Errorf("copied %q", got)
	}
}
