package ui

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"
)

// TestWrapText breaks at spaces, keeps indents and cuts a long word into
// even pieces.
func TestWrapText(t *testing.T) {
	fp := strings.Repeat("0123456789abcdef", 8)
	for _, c := range []struct {
		in   []string
		w    int
		want []string
	}{
		{[]string{"fits", ""}, 10, []string{"fits", ""}},
		{[]string{"one two three four"}, 9, []string{"one two", "three", "four"}},
		{[]string{"  one two three"}, 9, []string{"  one two", "  three"}},
		{[]string{"    " + fp}, 104, []string{"    " + fp[:64], "    " + fp[64:]}},
		{[]string{"    " + fp}, 54, []string{"    " + fp[:43], "    " + fp[43:86], "    " + fp[86:]}},
		{[]string{"a 👍🏽👍🏽👍🏽"}, 4, []string{"a", "👍🏽", "👍🏽", "👍🏽"}},
	} {
		got := wrapText(c.in, c.w)
		if !slices.Equal(got, c.want) {
			t.Errorf("wrapText(%q, %d) = %q, want %q", c.in, c.w, got, c.want)
		}
		widest := 0
		for _, r := range got {
			widest = max(widest, runewidth.StringWidth(r))
		}
		for w := widest; w <= c.w; w++ {
			if again := wrapText(c.in, w); !slices.Equal(again, got) {
				t.Errorf("wrapText(%q, %d) = %q, not the same as at %d", c.in, w, again, c.w)
			}
		}
	}
}

// TestSmallDialogsWrap: at the smallest screen, 60x16, the certificate
// dialogs show the whole fingerprint and all their text, wrapped rather
// than cut off, and a form's text and note wrap too.
func TestSmallDialogsWrap(t *testing.T) {
	a, n, _ := realApp(t)
	a.Update(tea.WindowSizeMsg{Width: 60, Height: 16})
	fp := strings.Repeat("4f1c9e07b2d5a3d6", 8)
	hex := regexp.MustCompile("[0-9a-f]{10,}")
	check := func(name string, words ...string) {
		t.Helper()
		lines := plainLines(a.View())
		var pieces []string
		for i, l := range lines {
			if w := runewidth.StringWidth(l); w != a.w {
				t.Errorf("%s: line %d is %d cells wide", name, i, w)
			}
			pieces = append(pieces, hex.FindAllString(l, -1)...)
		}
		got, text := strings.Join(pieces, ""), strings.Join(lines, "\n")
		if fp != "" && got != fp {
			t.Errorf("%s: the fingerprint shows as %q\n%s", name, got, text)
		}
		for _, w := range words {
			if !strings.Contains(text, w) {
				t.Errorf("%s: %q is cut off\n%s", name, w, text)
			}
		}
	}
	a.openDialog(a.netCertBox(n, "x509: certificate signed by unknown authority", fp))
	check("unknown", "Libera sent:", "unknown authority", "sign", "their own.", "certificate ever changes.", "Trust")
	a.openDialog(a.netCertChangedBox(n, fp))
	check("changed", "has changed!", "It sent", "was changed.", "Trust New Certificate")

	fp = ""
	a.openDialog(&formBox{Title: "Form", Lines: []string{strings.Repeat("words of text ", 8) + "the end."},
		Fields: []*formField{newField("Name:", "", false)}, Buttons: []string{"OK", "Cancel"},
		Note: strings.Repeat("a note ", 10) + "its end."})
	check("form", "the end.", "its end.", "Cancel")
}

// TestFormNoteCapitalized: a note starting with a letter of more than one
// byte is capitalized whole, not cut in half.
func TestFormNoteCapitalized(t *testing.T) {
	a := New(Options{})
	a.w, a.h = 110, 30
	f := &formBox{Title: "Test", Fields: []*formField{newField("Name:", "", false)},
		Buttons: []string{"OK", "Cancel"}, Note: "échec: «nope»"}
	if got := dialogText(a, f); !strings.Contains(got, "Échec: «nope»") {
		t.Errorf("the note shows as:\n%s", got)
	}
}

// TestListBoxSizeCached: a list of 50,000 channels is measured once, not
// on every frame, and measured again when its rows change.
func TestListBoxSizeCached(t *testing.T) {
	a := New(Options{})
	a.w, a.h = 200, 40
	rows := make([]string, 50000)
	for i := range rows {
		rows[i] = fmt.Sprintf("#channel%-6d %5d  a topic about something or other", i, i)
	}
	l := &listBox{Title: "Channels List", Header: "Channel", Rows: rows}
	start := time.Now()
	w0, _ := l.size(a)
	first := time.Since(start)
	start = time.Now()
	for range 100 {
		l.size(a)
	}
	if again := time.Since(start); again > first {
		t.Errorf("100 more frames took %v, measuring once took %v", again, first)
	}
	l.Rows = append(rows[:10:10], "#a-channel-with-a-much-longer-row-than-the-others-have, and a topic too")
	if w, _ := l.size(a); w <= w0 {
		t.Errorf("new rows weren't measured: %d wide, was %d", w, w0)
	}
}
