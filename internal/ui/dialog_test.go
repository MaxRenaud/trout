package ui

import (
	"fmt"
	"testing"
	"time"
)

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
