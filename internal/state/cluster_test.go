package state

import (
	"slices"
	"testing"
)

// TestEditBoxClusters: the cursor steps over an emoji with its skin tone,
// a family joined with ZWJs and a letter with a combining accent as one
// character each, and Backspace and Delete take them whole.
func TestEditBoxClusters(t *testing.T) {
	thumb := "\U0001F44D\U0001F3FD"              // 👍🏽
	family := "\U0001F468‍\U0001F469‍\U0001F467" // 👨‍👩‍👧
	text := "a" + thumb + "b" + family + "é"    // a👍🏽b👨‍👩‍👧é
	e := &EditBox{Buf: []rune(text)}
	var stops []int
	for e.Pos < len(e.Buf) {
		e.Move(1)
		stops = append(stops, e.Pos)
	}
	if want := []int{1, 3, 4, 9, 11}; !slices.Equal(stops, want) {
		t.Fatalf("right stops at %v, want %v", stops, want)
	}
	stops = nil
	for e.Pos > 0 {
		e.Move(-1)
		stops = append(stops, e.Pos)
	}
	if want := []int{9, 4, 3, 1, 0}; !slices.Equal(stops, want) {
		t.Fatalf("left stops at %v, want %v", stops, want)
	}
	e.Pos = 3
	e.Backspace()
	if string(e.Buf) != "ab"+family+"é" || e.Pos != 1 {
		t.Fatalf("Backspace after the thumb: %q at %d", string(e.Buf), e.Pos)
	}
	e.Pos = 2
	e.Delete()
	if string(e.Buf) != "abé" {
		t.Fatalf("Delete before the family: %q", string(e.Buf))
	}
	e.Pos = 3 // inside é: Snap and Step treat it as its start
	if Snap(e.Buf, 3) != 2 || Step(e.Buf, 3, -1) != 2 || Step(e.Buf, 3, 1) != 4 {
		t.Errorf("inside a cluster: snap %d, back %d, on %d", Snap(e.Buf, 3), Step(e.Buf, 3, -1), Step(e.Buf, 3, 1))
	}
}
