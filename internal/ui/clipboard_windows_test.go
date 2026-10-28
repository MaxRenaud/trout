package ui

import "testing"

// TestWindowsClipboard copies text outside the console's code page and
// reads it back through the API. (Skipped where there's no clipboard, as
// in some service sessions.)
func TestWindowsClipboard(t *testing.T) {
	const text = "café 🐟 日本"
	if err := systemCopy(text); err != nil {
		t.Skip("no clipboard here:", err)
	}
	got, err := systemPaste()
	if err != nil || got != text {
		t.Fatalf("got %q, %v", got, err)
	}
}
