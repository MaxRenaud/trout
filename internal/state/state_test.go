package state

import (
	"testing"
	"time"
)

func TestMerge(t *testing.T) {
	at := func(m int) time.Time { return time.Date(2026, 10, 27, 12, m, 0, 0, time.UTC) }
	w := &Window{Lines: []Line{{Time: at(5), Text: "five"}, {Time: at(9), Text: "nine"}}, Marker: 1}
	added := w.Merge([]Line{{Time: at(1), Text: "one"}, {Time: at(5), Text: "five"}, {Time: at(7), Text: "seven"}})
	var got []string
	for _, l := range w.Lines {
		got = append(got, l.Text)
	}
	if added != 2 || len(got) != 4 || got[0] != "one" || got[2] != "seven" {
		t.Fatalf("merged %v (%d new), want one five seven nine", got, added)
	}
	if w.Marker != 2 {
		t.Errorf("marker %d, want 2: still after five", w.Marker)
	}
	if n := w.LinesUpTo(at(7)); n != 3 {
		t.Errorf("LinesUpTo(7) = %d, want 3", n)
	}
}
