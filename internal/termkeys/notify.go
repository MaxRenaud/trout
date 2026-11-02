package termkeys

import (
	"strconv"
	"strings"
	"unicode"
)

// Notify is how a terminal shows a desktop notification, if it can: the
// terminal's take on mIRC's tips.
type Notify int

const (
	NotifyNone   Notify = iota
	NotifyOSC9          // iTerm2's: ESC ] 9 ; text BEL
	NotifyOSC777        // ESC ] 777 ; notify ; title ; body BEL: Ghostty, WezTerm, foot
	NotifyOSC99         // kitty's own protocol
)

// NotifyFor picks how the terminal in getenv's environment shows a
// notification. A terminal it doesn't know gets none: one that doesn't
// understand the sequence may show it as garbage, and Windows Terminal
// and ConEmu read OSC 9 as other things (9;4 is a progress bar). GNU
// screen gets none too: its passthrough can't carry kitty's, and cuts
// long ones.
func NotifyFor(getenv func(string) string) Notify {
	term, prog := getenv("TERM"), getenv("TERM_PROGRAM")
	switch {
	case getenv("STY") != "":
		return NotifyNone
	case getenv("LC_TERMINAL") == "iTerm2" || prog == "iTerm.app":
		return NotifyOSC9
	case getenv("KITTY_WINDOW_ID") != "" || term == "xterm-kitty":
		return NotifyOSC99
	case prog == "ghostty" || term == "xterm-ghostty" || getenv("GHOSTTY_RESOURCES_DIR") != "",
		prog == "WezTerm" || getenv("WEZTERM_PANE") != "",
		strings.HasPrefix(term, "foot"):
		return NotifyOSC777
	}
	return NotifyNone
}

// Sequence is what to write to the terminal to show a notification with
// title and body; id tells kitty's apart. In tmux it's wrapped to pass
// through to the terminal outside, which needs tmux's allow-passthrough.
func (n Notify) Sequence(id int, title, body string, tmux bool) string {
	title, body = clean(title, 80), clean(body, 240)
	var s string
	switch n {
	case NotifyOSC9:
		s = title + ": " + body
		if s[0] >= '0' && s[0] <= '9' { // not iTerm2's 9;4 progress bar
			s = "trout: " + s
		}
		s = "\x1b]9;" + s + "\a"
	case NotifyOSC777:
		s = "\x1b]777;notify;" + strings.ReplaceAll(title, ";", ",") + ";" + body + "\a"
	case NotifyOSC99:
		// The title, then the body, as one notification (d=0: more to come).
		i := "i=trout" + strconv.Itoa(id)
		s = "\x1b]99;" + i + ":d=0;" + title + "\x1b\\" + "\x1b]99;" + i + ":p=body;" + body + "\x1b\\"
	default:
		return ""
	}
	if tmux {
		s = "\x1bPtmux;" + strings.ReplaceAll(s, "\x1b", "\x1b\x1b") + "\x1b\\"
	}
	return s
}

// clean makes text safe inside an escape sequence: no control characters
// (they could end it early), spaces collapsed, and at most limit runes.
func clean(s string, limit int) string {
	var b strings.Builder
	n, space := 0, false
	for _, r := range s {
		if unicode.IsSpace(r) {
			space = b.Len() > 0
			continue
		}
		if unicode.IsControl(r) {
			continue
		}
		if n == limit {
			b.WriteRune('…')
			break
		}
		if space {
			b.WriteByte(' ')
			n++
			space = false
		}
		b.WriteRune(r)
		n++
	}
	return b.String()
}
