package ui

import (
	"cmp"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MaxRenaud/trout/internal/state"
)

// Sending text the way IRC needs it, and the commands built on that.

// maxText is how many bytes of text fit in one message to target. An IRC
// line is 512 bytes with its CRLF (RFC 1459), and the server puts
// ":nick!user@host " in front when it passes the message on, so that's
// set aside too (a generous guess, since our own host isn't known).
func maxText(n *state.Network, command, target string) int {
	relayPrefix := 1 + len(n.Nick) + 1 + 10 + 1 + 64 + 1
	return max(64, 510-relayPrefix-len(command)-1-len(target)-2)
}

// splitText cuts text into pieces of at most limit bytes, between UTF-8
// characters, at a space where one is near the end, like mIRC does with
// long lines.
func splitText(text string, limit int) []string {
	var out []string
	for len(text) > limit {
		cut := limit
		for cut > 0 && !utf8.RuneStart(text[cut]) {
			cut--
		}
		if sp := strings.LastIndexByte(text[:cut], ' '); sp > cut*3/4 {
			cut = sp
		}
		out = append(out, text[:cut])
		text = strings.TrimLeft(text[cut:], " ")
	}
	return append(out, text)
}

// sendText sends a PRIVMSG or NOTICE, split as needed; wrap puts each
// piece in a CTCP ACTION and so on. It returns the pieces sent.
func (a *App) sendText(n *state.Network, command, target, text string, wrap func(string) string) []string {
	extra := len(wrap(""))
	var sent []string
	for _, part := range splitText(text, maxText(n, command, target)-extra) {
		if !a.sendNet(n, command, target, wrap(part)) {
			break
		}
		sent = append(sent, part)
	}
	return sent
}

func plain(s string) string { return s }

func asAction(s string) string { return "\x01ACTION " + s + "\x01" }

// mentions reports whether text says nick as a word, the way mIRC's
// highlight does: "max:" and "@max" count, "maximum" doesn't.
func mentions(text, nick string) bool {
	if nick == "" {
		return false
	}
	t, k := strings.ToLower(stripCodes(text)), strings.ToLower(nick)
	for i := 0; i <= len(t)-len(k); {
		j := strings.Index(t[i:], k)
		if j < 0 {
			return false
		}
		j += i
		before, _ := utf8.DecodeLastRuneInString(t[:j])
		after, _ := utf8.DecodeRuneInString(t[j+len(k):])
		if (j == 0 || !nickRune(before)) && (j+len(k) == len(t) || !nickRune(after)) {
			return true
		}
		i = j + 1
	}
	return false
}

// nickRune is a character that can be part of a nick (RFC 2812).
func nickRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("-[]\\`^{}|_", r)
}

// pasteLines handles a paste with line breaks in it. Like mIRC, each line
// becomes a message, after asking; the first line joins what's already
// typed.
func (a *App) pasteLines(w *state.Window, text string) tea.Cmd {
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	var lines []string
	for l := range strings.SplitSeq(text, "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, strings.ReplaceAll(l, "\t", "    "))
		}
	}
	e := a.edit(w)
	if len(lines) <= 1 || (w.Kind != state.KChannel && w.Kind != state.KQuery) {
		e.Insert([]rune(strings.Join(lines, " "))...)
		return nil
	}
	lines[0] = string(e.Buf) + lines[0]
	if !a.confirmPaste { // Options > Other
		e.Take()
		for _, l := range lines {
			a.say(w, l)
		}
		return nil
	}
	preview := []string{fmt.Sprintf("Paste %d lines to %s?", len(lines), w.Name), ""}
	for i, l := range lines[:min(3, len(lines))] {
		if i == 2 && len(lines) > 3 {
			l = fmt.Sprintf("... and %d more", len(lines)-2)
		}
		preview = append(preview, "  "+truncate(l, 56))
	}
	d := message(preview...)
	d.Title = "Paste"
	d.Buttons, d.Cancel = []string{"Yes", "No"}, 1
	d.OnClose = func(a *App, button int) tea.Cmd {
		if button == 0 {
			e.Take()
			for _, l := range lines { // as text, not commands
				a.say(w, l)
			}
		}
		return nil
	}
	a.openDialog(d)
	return nil
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// isonEvery is how often the notify list is checked with ISON on networks
// without MONITOR (Rizon, for one), like mIRC's notify polling.
const isonEvery = time.Minute

type isonTickMsg struct{}

// pollNotify asks every network without MONITOR who on the notify list is
// online. The answer (RPL_ISON) goes to isonReply.
func (a *App) pollNotify() tea.Cmd {
	for _, n := range a.nets[1:] {
		if list := a.notifyNicks(n); len(list) > 0 && !n.Monitor && a.conns[n] != nil {
			a.sendNet(n, "ISON", strings.Join(list, " "))
		}
	}
	return tea.Tick(isonEvery, func(time.Time) tea.Msg { return isonTickMsg{} })
}

// isonReply is RPL_ISON (303): the nicks asked about that are online.
func (a *App) isonReply(n *state.Network, online string) {
	on := map[string]bool{}
	for nick := range strings.FieldsSeq(online) {
		on[n.Fold(nick)] = true
	}
	for _, nick := range a.notifyNicks(n) {
		a.notifySeen(n, nick, on[n.Fold(nick)], "")
	}
}

// notifyCommand is mIRC's /notify: /notify alone lists the notify list,
// /notify nick adds, /notify -r nick removes.
func (a *App) notifyCommand(w *state.Window, rest string) {
	n := w.Net
	f := strings.Fields(rest)
	switch {
	case len(f) == 0:
		var on, off []string
		for _, nick := range a.notifyNicks(n) {
			if n.Online[n.Fold(nick)] {
				on = append(on, nick)
			} else {
				off = append(off, nick)
			}
		}
		a.addLine(w, state.RoleInfo, fmt.Sprintf("* Notify on %s: online %s; offline %s", n.Name,
			cmp.Or(strings.Join(on, ", "), "nobody"), cmp.Or(strings.Join(off, ", "), "nobody")))
	case f[0] == "-r" && len(f) > 1:
		a.setNotify(n, f[1], false)
		a.addLine(w, state.RoleInfo, "* Removed "+f[1]+" from the notify list")
	default:
		a.setNotify(n, f[0], true)
		a.addLine(w, state.RoleInfo, "* Added "+f[0]+" to the notify list")
	}
}

// allChannels is /amsg and /ame: a message or action to every channel
// you're in on this network, like mIRC.
func (a *App) allChannels(w *state.Window, text string, action bool) {
	for _, ch := range w.Net.Wins {
		if ch.Kind != state.KChannel || !ch.Joined {
			continue
		}
		if action {
			a.action(ch, text)
		} else {
			a.privmsg(ch, ch.Name, text)
		}
	}
}

// mircTokens maps mIRC's $asctime/timestamp notation to Go's, longest
// first: HH:nn is 24-hour hours and minutes, hh:nn tt the 12-hour clock.
var mircTokens = []struct{ mirc, goLayout string }{
	{"yyyy", "2006"}, {"mmmm", "January"}, {"dddd", "Monday"},
	{"mmm", "Jan"}, {"ddd", "Mon"},
	{"yy", "06"}, {"mm", "01"}, {"dd", "02"}, {"HH", "15"}, {"hh", "03"},
	{"nn", "04"}, {"ss", "05"}, {"tt", "pm"}, {"TT", "PM"},
	{"m", "1"}, {"d", "2"}, {"H", "15"}, {"h", "3"}, {"n", "4"}, {"s", "5"},
}

// stampLayout turns a timestamp format like mIRC's "[HH:nn]" into a Go
// time layout. Other characters stay as they are.
func stampLayout(format string) string {
	var b strings.Builder
	for i := 0; i < len(format); {
		matched := false
		for _, tk := range mircTokens {
			if strings.HasPrefix(format[i:], tk.mirc) {
				b.WriteString(tk.goLayout)
				i += len(tk.mirc)
				matched = true
				break
			}
		}
		if !matched {
			b.WriteByte(format[i])
			i++
		}
	}
	return b.String()
}
