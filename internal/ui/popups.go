package ui

import (
	"cmp"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MaxRenaud/trout/internal/script"
	"github.com/MaxRenaud/trout/internal/state"
)

// Popups, like mIRC's: every right-click menu (status, channel, query and
// nicklist) and the Commands menu come from popups.ini in the scripts
// folder, which the Scripts Editor's Popups tab edits, plus the menu
// sections of loaded scripts. Until it's saved there, it's trout's own
// default, defaultPopups.

// defaultPopups are trout's menus, by popups.ini section, in mIRC's order:
// mIRC's defaults without what trout doesn't do (DCC Chat, mIRC's own
// channels, Edit Notes) or does differently (/quit closes trout), and
// with trout's Send File.
var defaultPopups = []struct{ section, items string }{
	{"cpopup", `Channel Modes:/channel`},
	{"bpopup", `Commands
Join channel:/join #$$?="Enter channel name:"
Part channel:/part #$$?="Enter channel name:"
Query user:/query $$?="Enter nickname and message:"
Send notice:/notice $$?="Enter nickname and message:"
Whois user:/whois $$?="Enter nickname:"
Send CTCP
.Ping:/ctcp $$?="Enter nickname:" ping
.Time:/ctcp $$?="Enter nickname:" time
.Version:/ctcp $$?="Enter nickname:" version
Send File:/dcc send $$?="Enter nickname:"
Set Away
.On:/away $$?="Enter away message:"
.Off:/away
Invite user:/invite $$?="Enter nickname and channel:"
Ban user:/ban $$?="Enter channel and nickname:"
Kick user:/kick $$?="Enter channel and nickname:"
Ignore user:/ignore $$?="Enter nickname:"
Unignore user:/ignore -r $$?="Enter nickname:"
Change nick:/nick $$?="Enter new nickname:"`},
	{"mpopup", `Server
.Lusers:/lusers
.Motd:/motd
.Time:/time
Names
.names ?:/names #$$?="Enter a channel name:"
Join
.join ?:/join #$$?="Enter a channel to join:"
Query
.query ?:/query $$?="Enter nickname to talk to:"
Other
.Whois ?:/whois $$?="Enter a nickname:"
.Query:/query $$?="Enter a nickname:"
.Nickname:/nick $$?="Enter your new nickname:"
.Away
..Set Away...:/away $$?="Enter your away message:"
..Set Back:/away
.List Channels:/list`},
	{"qpopup", `Info:/uwho $$1
Whois:/whois $$1
Query:/query $$1
-
Ignore:/ignore $$1 1 | /closemsg $$1
-
CTCP
.Ping:/ctcp $$1 ping
.Time:/ctcp $$1 time
.Version:/ctcp $$1 version
Send File:/dcc send $$1`},
	{"lpopup", `Info:/uwho $1
Whois:/whois $$1
Query:/query $$1
-
Control
.Ignore:/ignore $$1 1
.Unignore:/ignore -r $$1 1
.Op:/mode # +ooo $$1 $2 $3
.Deop:/mode # -ooo $$1 $2 $3
.Voice:/mode # +vvv $$1 $2 $3
.Devoice:/mode # -vvv $$1 $2 $3
.Kick:/kick # $$1
.Kick (why):/kick # $$1 $$?="Reason:"
.Ban:/ban $$1 2
.Ban, Kick:/ban $$1 2 | /kick # $$1
.Ban, Kick (why):/ban $$1 2 | /kick # $$1 $$?="Reason:"
CTCP
.Ping:/ctcp $$1 ping
.Time:/ctcp $$1 time
.Version:/ctcp $$1 version
Send File:/dcc send $$1
-
Slap!:/me slaps $$1 around a bit with a large trout`},
}

func defaultPopupsIni() string {
	var b strings.Builder
	for _, s := range defaultPopups {
		b.WriteString(writeIniList(s.section, strings.Split(s.items, "\n")))
	}
	return b.String()
}

// popupsData is popups.ini: the scripts folder's, or trout's default.
func (a *App) popupsData() string {
	if data, ok := a.scriptReadFile("popups.ini"); ok {
		return data
	}
	return defaultPopupsIni()
}

// loadPopups loads popups.ini, before the scripts, so its items come
// first in each menu.
func (a *App) loadPopups() {
	a.scriptProblems["popups.ini"] = a.scripts.LoadAs("popups.ini", a.popupsData(), "popups")
}

// scriptMenu is a popup's items (status, channel, query, nicklist or
// menubar), from popups.ini and the scripts, as trout's menu items. They
// work with /remote off too: popups aren't the remote.
func (a *App) scriptMenu(kind string, w *state.Window, params []string) []MenuItem {
	if a.scripts == nil {
		return nil
	}
	window := w.Name
	if w.Kind == state.KStatus {
		window = ""
	}
	items, actions := a.scripts.Popups(kind, w.Net.Name, window, params, a.scriptHost())
	a.queueScript(a.runScript(actions))
	var conv func([]script.MenuItem) []MenuItem
	conv = func(items []script.MenuItem) []MenuItem {
		var out []MenuItem
		for _, it := range items {
			m := MenuItem{Label: it.Label, Sep: it.Separator, Disabled: it.Disabled, Check: it.Checked}
			if len(it.Items) > 0 {
				m.Sub = conv(it.Items)
			} else if it.ID != "" {
				id := it.ID
				m.Action = func(a *App) tea.Cmd {
					return a.askInputs(a.scripts.MenuPrompts(id), func(a *App, answers []string) tea.Cmd {
						a.scripts.Answer(answers)
						return a.runScript(a.scripts.RunMenu(id, kind, w.Net.Name, window, params, a.scriptHost()))
					})
				}
			}
			out = append(out, m)
		}
		return out
	}
	return conv(items)
}

// nickMenu is the nicklist popup, for a nick in the nicklist or the text.
func (a *App) nickMenu(win *state.Window, nick string, x, y int) {
	a.closeMenus()
	if items := a.scriptMenu("nicklist", win, []string{nick}); len(items) > 0 {
		a.pushMenu(items, x, y)
	}
}

// textMenu is the popup for right-clicking a window's text: the status,
// channel or query popup. In a query, $1 is who it's with, like mIRC.
func (a *App) textMenu(w *state.Window, x, y int) {
	kind := map[state.Kind]string{state.KStatus: "status", state.KChannel: "channel", state.KQuery: "query"}[w.Kind]
	if kind == "" {
		return
	}
	var params []string
	if w.Kind == state.KQuery {
		params = []string{w.Name}
	}
	a.closeMenus()
	if items := a.scriptMenu(kind, w, params); len(items) > 0 {
		a.pushMenu(items, x, y)
	}
}

// commandsMenu is the menubar's Commands menu: the menubar popup.
func (a *App) commandsMenu() []MenuItem {
	if items := a.scriptMenu("menubar", a.active, nil); len(items) > 0 {
		return items
	}
	return []MenuItem{{Label: "(empty)", Disabled: true}}
}

// menuBarName is a menubar entry's name: Commands is the menubar popup's
// first line, like mIRC's.
func (a *App) menuBarName(i int) string {
	if i == 4 && a.scripts != nil {
		return cmp.Or(a.scripts.MenubarTitle(), menuBarNames[i])
	}
	return menuBarNames[i]
}

// askInputs asks a command's input boxes ($?="question") one after the
// other, then runs it with the answers. Cancel on a $$? one stops it; on a
// $? one, the answer is empty.
func (a *App) askInputs(ps []script.Prompt, run func(a *App, answers []string) tea.Cmd) tea.Cmd {
	if len(ps) == 0 {
		return run(a, nil)
	}
	var answers []string
	var ask func(i int)
	ask = func(i int) {
		p := ps[i]
		f := &formBox{Title: "Input Request", Buttons: []string{"OK", "Cancel"},
			Fields: []*formField{newField("", "", false)}}
		if p.Text != "" {
			f.Lines = []string{p.Text}
		}
		next := func(a *App, v string) tea.Cmd {
			answers = append(answers, v)
			if i+1 < len(ps) {
				ask(i + 1)
				return nil
			}
			return run(a, answers)
		}
		f.OnOK = func(a *App, v []string) (tea.Cmd, error) {
			a.dlg = nil
			return next(a, v[0]), nil
		}
		f.OnCancel = func(a *App) tea.Cmd {
			if p.Required {
				return nil
			}
			return next(a, "")
		}
		a.openDialog(f)
	}
	ask(0)
	return nil
}
