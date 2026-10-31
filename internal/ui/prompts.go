package ui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MaxRenaud/trout/internal/state"
)

// prompt is a one-field input box, like mIRC's $?="...": ok gets the
// trimmed text, and an empty one cancels.
func (a *App) prompt(title, line, label, value string, ok func(a *App, v string)) *formBox {
	f := &formBox{Title: title, Fields: []*formField{newField(label, value, false)}, Buttons: []string{"OK", "Cancel"},
		OnOK: func(a *App, vals []string) (tea.Cmd, error) {
			if v := strings.TrimSpace(vals[0]); v != "" {
				ok(a, v)
			}
			return nil, nil
		}}
	if line != "" {
		f.Lines = []string{line}
	}
	return f
}

// command runs a slash command in the active window, as if typed.
func (a *App) run(line string) tea.Cmd { return a.command(a.active, line) }

// commandPrompt is one of the Commands menu's prompts: it asks for the
// argument and runs the slash command with it.
func (a *App) commandPrompt(title, label, cmd, value string) func(*App) tea.Cmd {
	return func(a *App) tea.Cmd {
		a.openDialog(a.prompt(title, "", label, value, func(a *App, v string) { a.run(cmd + " " + v) }))
		return nil
	}
}

// selectedNick is the nick a prompt starts with: the query's, or the one
// selected in the nicklist.
func (a *App) selectedNick() string {
	w := a.active
	switch {
	case w.Kind == state.KQuery:
		return w.Name
	case w.Kind == state.KChannel:
		return w.NickSel
	}
	return ""
}

// openSendNotice and openSendCTCP ask for two things, so they're forms.
func (a *App) openSendNotice() {
	a.openDialog(&formBox{Title: "Send Notice", Buttons: []string{"Send", "Cancel"},
		Fields: []*formField{newField("To:", a.selectedNick(), false), newField("Message:", "", false)},
		OnOK: func(a *App, v []string) (tea.Cmd, error) {
			to, msg := strings.TrimSpace(v[0]), strings.TrimSpace(v[1])
			if to == "" || msg == "" {
				return nil, errors.New("give a nick or channel and a message")
			}
			return a.run("notice " + to + " " + msg), nil
		}})
}

func (a *App) openSendCTCP() {
	f := &formBox{Title: "Send CTCP", Lines: []string{"Ask a client something: VERSION, PING, TIME or CLIENTINFO."},
		Buttons: []string{"Send", "Cancel"},
		Fields:  []*formField{newField("Nick:", a.selectedNick(), false), newField("Request:", "VERSION", false)},
		OnOK: func(a *App, v []string) (tea.Cmd, error) {
			nick, req := strings.TrimSpace(v[0]), strings.TrimSpace(v[1])
			if nick == "" || req == "" {
				return nil, errors.New("give a nick and a request")
			}
			return a.run("ctcp " + nick + " " + req), nil
		}}
	a.openDialog(f)
}

// commandsBox is Help > Help Topics: the slash commands trout knows.
func commandsBox() *msgBox {
	d := message(
		"/join #chan [key]       Join a channel (also Alt+J favorites)",
		"/part [#chan] [reason]  Leave a channel",
		"/query nick [message]   Open a private chat",
		"/msg nick message       Send a private message",
		"/notice target message  Send a notice",
		"/me action              Describe what you're doing",
		"/slap nick              Slap them around a bit with a large trout",
		"/nick name              Change your nick",
		"/topic [text]           Show or set the channel topic",
		"/whois nick             Who someone is",
		"/away [message]         Away on every network, or back",
		"/ctcp nick request      VERSION, PING, TIME, CLIENTINFO",
		"/ignore [nick|mask]     Ignore someone, or list ignores",
		"/unignore nick|mask     Stop ignoring someone",
		"/kick /ban /op /deop /voice /devoice nick",
		"/server [host]          Switch to or add a network",
		"/quote line             Send a raw line",
		"/clear                  Clear the window",
		"/load -rs file          Load a mIRC script (Tools > Scripts)",
		"//command               Evaluate $identifiers and %variables first",
		"",
		"Anything else goes to the server as it is, like /list.",
	)
	d.Title = "Help Topics"
	return d
}

// saveWindow is the window popup's Save to File: the window's lines as
// plain text in the download folder. (The bouncer keeps the real logs.)
func (a *App) saveWindow(w *state.Window) {
	dir, err := a.downloads()
	if err != nil {
		a.addLine(w, state.RoleInfo, "* Couldn't save: "+err.Error())
		return
	}
	name := fmt.Sprintf("trout-%s-%s-%s.txt", w.Net.Name, w.Name, time.Now().Format("20060102-150405"))
	name = safeName(name)
	var b strings.Builder
	for _, l := range w.Lines {
		b.WriteString(l.Time.Format("[2006-01-02 15:04] ") + stripCodes(l.Text) + "\n")
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		a.addLine(w, state.RoleInfo, "* Couldn't save: "+err.Error())
		return
	}
	a.addLine(w, state.RoleInfo, fmt.Sprintf("* Saved %d lines to %s", len(w.Lines), path))
}

// seenURL is a link from the URLs List: where it was said and by whom.
type seenURL struct {
	url, nick, where string
	at               time.Time
}

var urlRe = regexp.MustCompile(`(?i)\bhttps?://[^\s<>"]+`)

// noteURLs remembers the links in a message for Tools > URLs List,
// newest first, at most 200.
func (a *App) noteURLs(w *state.Window, nick, text string, at time.Time) {
	if a.winOpts(w).TrackURLs == "off" || a.hideLines { // the window's Track URLs, or a script hid it
		return
	}
	for _, u := range urlRe.FindAllString(stripCodes(text), -1) {
		u = strings.TrimRight(u, ".,;:!?)]}'")
		a.urls = slices.DeleteFunc(a.urls, func(s seenURL) bool { return s.url == u })
		a.urls = append([]seenURL{{url: u, nick: nick, where: w.Name, at: at}}, a.urls...)
	}
	if len(a.urls) > 200 {
		a.urls = a.urls[:200]
	}
}

// openURLsList is Tools > URLs List: links seen since trout started.
func (a *App) openURLsList() {
	l := &listBox{Title: "URLs List", Header: "Link (newest first)", Buttons: []string{"Open", "Close"},
		OnPress: func(a *App, _ *listBox, _ string, u string) tea.Cmd {
			if u == "" {
				return nil
			}
			return openURL(u)
		}}
	for _, s := range a.urls {
		l.Rows = append(l.Rows, s.at.Format("15:04 ")+s.url+"  ("+s.nick+" in "+s.where+")")
		l.Keys = append(l.Keys, s.url)
	}
	if len(l.Rows) == 0 {
		l.Rows = []string{"No links yet."}
	}
	a.openDialog(l)
}
