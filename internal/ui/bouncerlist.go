package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MaxRenaud/trout/internal/config"
	"github.com/MaxRenaud/trout/internal/irc"
	"github.com/MaxRenaud/trout/internal/state"
)

// savedBouncersBox is Tools > Bouncers for real: the bouncers in
// config.toml, to switch between, add, edit and remove, and update the
// soju on ones trout set up. (The demo has bouncerBox, its mock-up.)
type savedBouncersBox struct{ sel int }

// buttons has Update when the selected bouncer is one trout set up.
func (d *savedBouncersBox) buttons(a *App) []string {
	if bs := a.savedBouncers(); len(bs) > 0 && bs[clamp(d.sel, 0, len(bs)-1)].SSH != "" {
		return []string{"Use", "Add", "Edit", "Update", "Remove", "Close"}
	}
	return []string{"Use", "Add", "Edit", "Remove", "Close"}
}

func (a *App) openSavedBouncers() {
	d := &savedBouncersBox{}
	for i, b := range a.savedBouncers() {
		if b.Name == a.bnc.Name {
			d.sel = i
		}
	}
	a.openDialog(d)
}

// savedBouncers is config.toml's bouncers, or just the one in use when
// there's no config (tests).
func (a *App) savedBouncers() []config.Bouncer {
	switch {
	case a.cfg != nil:
		return a.cfg.Bouncers
	case a.bnc.Address != "":
		return []config.Bouncer{a.bnc}
	}
	return nil
}

func (d *savedBouncersBox) title() string { return "Bouncers" }

func (d *savedBouncersBox) size(a *App) (int, int) { return 78, 16 }

func (d *savedBouncersBox) draw(a *App, c *Canvas, x, y, w, h int) {
	t := a.theme()
	face := Style{FG: t.FaceText, BG: t.Face}
	bs := a.savedBouncers()
	lx, ly, lw, lh := x+2, y+2, 20, h-6
	c.Fill(lx, ly, lw, lh, Style{BG: t.TextBG})
	d.sel = clamp(d.sel, 0, len(bs)-1)
	for i, b := range bs[:min(len(bs), lh)] {
		st := Style{FG: t.TextFG, BG: t.TextBG}
		if i == d.sel {
			st = Style{FG: t.SelFG, BG: t.SelBG}
		}
		mark := "  "
		if b.Name == a.bnc.Name {
			mark = "• "
		}
		c.Fill(lx, ly+i, lw, 1, st)
		c.Text(lx+1, ly+i, lw-1, mark+b.Name, st)
		a.addRegion(Region{X: lx, Y: ly + i, W: lw, H: 1, Kind: HitDlg, S: "item", I: i})
	}
	px, pw := lx+lw+2, x+w-3-(lx+lw+2)
	if len(bs) == 0 {
		c.Text(px, ly, pw, "No bouncers yet. Add yours.", face)
	} else {
		b := bs[d.sel]
		for i, r := range bouncerDetails(a, b) {
			c.Text(px, ly+i, 9, r[0], Style{FG: t.FaceText, BG: t.Face, Bold: true})
			c.Text(px+10, ly+i, pw-10, r[1], face)
		}
	}
	a.dlgButtons(c, x, y+h-3, w, d.buttons(a), 0)
}

func bouncerDetails(a *App, b config.Bouncer) [][2]string {
	key := "checked by this computer's authorities"
	switch _, useTLS, _ := irc.ParseAddress(b.Address); {
	case !useTLS:
		key = "none: plain text"
	case b.Pin != "":
		key = b.Pin
	}
	pw := "in the credentials file"
	if b.PasswordCommand != "" {
		pw = "from " + b.PasswordCommand
	}
	if b.Login == "certificate" {
		pw = "this computer's certificate (" + pw + " as a fallback)"
	}
	status := "not in use"
	if b.Name == a.bnc.Name {
		status = "in use, not connected"
		if a.control != nil {
			status = "in use, connected"
		}
	}
	addr := b.Address
	if b.Local {
		addr, key = "this computer, while trout is open", "none: it never leaves this computer"
	}
	rows := [][2]string{
		{"Name", b.Name},
		{"Address", addr},
		{"User", b.User},
		{"TLS key", key},
		{"Login", pw},
		{"Status", status},
	}
	if b.SSH != "" {
		rows = append(rows, [2]string{"Server", "set up by trout, on " + b.SSH})
	}
	return rows
}

func (d *savedBouncersBox) press(a *App, button string) tea.Cmd {
	bs := a.savedBouncers()
	if button == "Close" {
		a.dlg = nil
		return nil
	}
	if button == "Add" {
		a.openConnectFor(config.Bouncer{}, "", "", d)
		return nil
	}
	if len(bs) == 0 {
		return nil
	}
	b := bs[clamp(d.sel, 0, len(bs)-1)]
	switch button {
	case "Use":
		a.dlg = nil
		if a.cfg == nil {
			return a.switchBouncer(b, a.password)
		}
		pw, err := a.cfg.StoredPassword(b.Name)
		if err != nil {
			a.addLine(a.home.Status(), state.RoleQuit, "* "+err.Error())
			return nil
		}
		a.cfg.Active = b.Name
		a.saveConfig()
		return a.connectWith(b, pw)
	case "Edit":
		if b.Local {
			a.openDialog(&msgBox{Title: "Bouncers", Buttons: []string{"OK"},
				Lines:   []string{"There's nothing to edit: trout runs this one itself."},
				OnClose: func(a *App, _ int) tea.Cmd { a.openDialog(d); return nil }})
			return nil
		}
		pw := a.password
		if a.cfg != nil && b.Name != a.bnc.Name {
			pw, _ = a.cfg.StoredPassword(b.Name)
		}
		a.openConnectFor(b, pw, "", d)
	case "Update":
		a.dlg = nil
		return a.openReinstall(b, "update")
	case "Remove":
		if b.SSH != "" {
			a.openDialog(d.uninstallBox(b))
			return nil
		}
		lines := []string{"Remove " + b.Name + " from trout?", "",
			"The bouncer itself, and everything on it, stays as it is."}
		if b.Name == a.bnc.Name {
			lines = append(lines, "trout stays connected to it until you quit.")
		}
		a.openDialog(&msgBox{Title: "Remove Bouncer", Lines: lines, Buttons: []string{"Yes", "No"}, Sel: 1, Cancel: 1,
			OnClose: func(a *App, button int) tea.Cmd {
				if button == 0 && a.cfg != nil {
					if err := a.cfg.Remove(b.Name); err != nil {
						a.addLine(a.home.Status(), state.RoleQuit, "* Couldn't remove "+b.Name+": "+err.Error())
					}
				}
				a.openDialog(d)
				return nil
			}})
	}
	return nil
}

func (d *savedBouncersBox) key(a *App, k string) tea.Cmd {
	switch k {
	case "up":
		d.sel--
	case "down":
		d.sel++
	case "enter":
		return d.press(a, "Use")
	case "delete", "backspace":
		return d.press(a, "Remove")
	case "esc":
		a.dlg = nil
	default:
		for _, b := range d.buttons(a) {
			if strings.EqualFold(k, b[:1]) {
				return d.press(a, b)
			}
		}
	}
	return nil
}

func (d *savedBouncersBox) mouse(a *App, m tea.MouseMsg, r Region, double bool) tea.Cmd {
	if m.Action != tea.MouseActionPress || m.Button != tea.MouseButtonLeft {
		return nil
	}
	switch r.S {
	case "item":
		d.sel = r.I
		if double {
			return d.press(a, "Use")
		}
	case "button":
		return d.press(a, d.buttons(a)[r.I])
	case "close":
		a.dlg = nil
	}
	return nil
}

// uninstallBox asks what removing a bouncer trout set up should do: take
// soju off the server (and maybe its history), or only forget it here.
func (d *savedBouncersBox) uninstallBox(b config.Bouncer) *msgBox {
	box := &msgBox{Title: "Remove Bouncer", Buttons: []string{"Uninstall", "Forget", "Cancel"}, Sel: 2, Cancel: 2,
		Lines: []string{"Remove " + b.Name + "?", "",
			"Uninstall takes the bouncer off " + b.SSH + ". Forget only",
			"removes it from trout and leaves the server as it is."},
		Check: "Delete its chat history too"}
	box.OnClose = func(a *App, button int) tea.Cmd {
		switch button {
		case 0:
			mode := "remove"
			if box.Checked {
				mode = "purge"
			}
			return a.openReinstall(b, mode)
		case 1:
			if a.cfg != nil {
				if err := a.cfg.Remove(b.Name); err != nil {
					a.addLine(a.home.Status(), state.RoleQuit, "* Couldn't remove "+b.Name+": "+err.Error())
				}
			}
		}
		a.openDialog(d)
		return nil
	}
	return box
}
