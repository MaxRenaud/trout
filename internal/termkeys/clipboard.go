package termkeys

import (
	"encoding/base64"
	"strings"
)

// ClipboardSequence is OSC 52, which has the terminal put text on its
// computer's clipboard: the way a program over ssh reaches yours.
// Terminals that don't have it ignore it, as they do any OSC they don't
// know. In tmux it goes both as it is, for tmux's set-clipboard on, and
// wrapped to pass through to the terminal outside, for allow-passthrough;
// with both on, the clipboard is set twice, to the same text.
func ClipboardSequence(text string, tmux bool) string {
	s := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(text)) + "\a"
	if tmux {
		s += "\x1bPtmux;" + strings.ReplaceAll(s, "\x1b", "\x1b\x1b") + "\x1b\\"
	}
	return s
}
