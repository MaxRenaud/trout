package ui

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MaxRenaud/trout/internal/state"
)

// Function keys run aliases, like mIRC's: an alias named F5 is F5, sF5
// Shift+F5 and cF5 Ctrl+F5. Shift+Ctrl runs sF5, and Alt+F5 none (both
// checked). In a query $1 is who it's with; in a channel with the
// nicklist focused, $1- the selected nicks (checked: "bob carol").
// With such an alias the key does only that, not what it does in trout.

// fkeyAlias is the alias msg, a key, runs, or "".
func fkeyAlias(msg tea.Msg) string {
	if k, ok := msg.(tea.KeyMsg); ok {
		if k.Alt || k.Paste {
			return ""
		}
		s := k.String()
		n, err := strconv.Atoi(strings.TrimPrefix(s, "f"))
		switch {
		case !strings.HasPrefix(s, "f") || err != nil:
			return ""
		case n >= 1 && n <= 12:
			return s
		case n >= 13 && n <= 20: // Bubble Tea's names for xterm's Shift+F1-F8
			return "sf" + strconv.Itoa(n-12)
		}
		return ""
	}
	// Bubble Tea has no names for the rest (Ctrl+F1, Shift+F9...): it
	// hands them on as a sequence it doesn't know, which prints as
	// ?CSI[49 53 59 53 126]? for ESC [ 15;5~.
	st, ok := msg.(fmt.Stringer)
	if !ok {
		return ""
	}
	s := st.String()
	if !strings.HasPrefix(s, "?CSI[") || !strings.HasSuffix(s, "]?") {
		return ""
	}
	var seq []byte
	for f := range strings.FieldsSeq(s[len("?CSI[") : len(s)-2]) {
		b, err := strconv.Atoi(f)
		if err != nil {
			return ""
		}
		seq = append(seq, byte(b))
	}
	return fkeyCSI(string(seq))
}

// fkeyCSI is the alias for a CSI function key with modifiers (after
// ESC [), as xterm sends them: 15;5~ is Ctrl+F5, 1;5P Ctrl+F1.
func fkeyCSI(seq string) string {
	code, mod, ok := strings.Cut(seq[:max(0, len(seq)-1)], ";")
	if !ok || len(seq) == 0 {
		return ""
	}
	n := 0
	switch final := seq[len(seq)-1]; {
	case code == "1" && final >= 'P' && final <= 'S':
		n = int(final-'P') + 1
	case final == '~':
		n = map[string]int{"11": 1, "12": 2, "13": 3, "14": 4, "15": 5, "17": 6, "18": 7, "19": 8,
			"20": 9, "21": 10, "23": 11, "24": 12}[code]
	}
	m, err := strconv.Atoi(mod)
	if n == 0 || err != nil {
		return ""
	}
	// xterm's modifier is 1 plus Shift 1, Alt 2, Ctrl 4.
	shift, alt, ctrl := (m-1)&1 != 0, (m-1)&2 != 0, (m-1)&4 != 0
	switch {
	case alt:
		return ""
	case shift: // Shift+Ctrl too, like mIRC's
		return "sf" + strconv.Itoa(n)
	case ctrl:
		return "cf" + strconv.Itoa(n)
	}
	return "f" + strconv.Itoa(n)
}

// fkey runs the alias for a function key, if there is one.
func (a *App) fkey(name string) (tea.Cmd, bool) {
	w := a.active
	if name == "" || w == nil || a.dlg != nil || len(a.menus) > 0 || !a.scriptsOn() {
		return nil, false
	}
	window, args := w.Name, ""
	switch {
	case w.Kind == state.KStatus:
		window = ""
	case w.Kind == state.KQuery:
		args = w.Name
	case w.Kind == state.KChannel && a.nickFocus:
		args = strings.Join(w.SelectedNicks(), " ")
	}
	actions, ok := a.scripts.Alias(w.Net.Name, window, name, args, a.scriptHost())
	if !ok {
		return nil, false
	}
	return a.runScript(actions), true
}
