package ui

import (
	"fmt"
	"strings"

	"github.com/MaxRenaud/trout/internal/state"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"
)

// bouncer is a soju instance trout set up, on your own server or a VPS.
type bouncer struct {
	Name  string
	Where string // "Hetzner Cloud, Falkenstein, DE" or "your server, over SSH"
	Addr  string
	Pin   string // the TLS key trout generated and trusts
	VPS   bool   // removing it deletes the server
}

func (a *App) currentBouncer() *bouncer {
	if a.bouncer > 0 && a.bouncer <= len(a.bouncers) {
		return &a.bouncers[a.bouncer-1]
	}
	return nil
}

func (a *App) useBouncer(i int) {
	a.bouncer = i
	via := "direct connections from this computer"
	if b := a.currentBouncer(); b != nil {
		via = "soju on " + b.Name
	}
	for _, n := range a.nets {
		a.addLine(n.Wins[0], state.RoleInfo, "* Bouncer: now using "+via+" (mock)")
	}
}

// addBouncer records a bouncer the wizard set up and switches to it.
func (a *App) addBouncer(b bouncer) {
	name := b.Name
	for i := 2; a.hasBouncer(name); i++ {
		name = fmt.Sprintf("%s-%d", b.Name, i)
	}
	b.Name = name
	a.bouncers = append(a.bouncers, b)
	a.useBouncer(len(a.bouncers))
}

func (a *App) hasBouncer(name string) bool {
	for _, b := range a.bouncers {
		if b.Name == name {
			return true
		}
	}
	return false
}

func (a *App) openBouncers() { a.openDialog(&bouncerBox{}) }

// bouncerBox manages the configured bouncers: list on the left, details of
// the selected one on the right.
type bouncerBox struct{ sel int }

func (d *bouncerBox) title() string { return "Bouncers" }

func (d *bouncerBox) size(a *App) (int, int) { return 72, 19 }

func (d *bouncerBox) buttons(a *App) []string {
	if len(a.bouncers) == 0 {
		return []string{"Add...", "Close"}
	}
	return []string{"Use", "Add...", "Update", "Remove", "Close"}
}

func (d *bouncerBox) draw(a *App, c *Canvas, x, y, w, h int) {
	t := a.theme()
	face := Style{FG: t.FaceText, BG: t.Face}
	if len(a.bouncers) == 0 {
		c.Text(x+3, y+2, w-6, "No bouncers yet. trout connects to your networks directly.", face)
		c.Text(x+3, y+4, w-6, "Add one to stay connected while trout is closed.", face)
		a.dlgButtons(c, x, y+h-3, w, d.buttons(a), 0)
		return
	}
	d.sel = clamp(d.sel, 0, len(a.bouncers)-1)
	lx, ly, lw, lh := x+2, y+2, 20, h-6
	c.Fill(lx, ly, lw, lh, Style{BG: t.TextBG})
	for i, b := range a.bouncers[:min(len(a.bouncers), lh)] {
		st := Style{FG: t.TextFG, BG: t.TextBG}
		if i == d.sel {
			st = Style{FG: t.SelFG, BG: t.SelBG}
		}
		c.Fill(lx, ly+i, lw, 1, st)
		c.Text(lx+1, ly+i, lw-1, b.Name, st)
		a.addRegion(Region{X: lx, Y: ly + i, W: lw, H: 1, Kind: HitDlg, S: "item", I: i})
	}

	b := a.bouncers[d.sel]
	status := "● Online"
	if d.sel == a.bouncer-1 {
		status += ", in use"
	}
	var nets []string
	for _, n := range a.nets {
		nets = append(nets, n.Name)
	}
	updates := "auto-updates (Debian packages)"
	rows := [][2]string{
		{"Name", b.Name},
		{"Runs on", b.Where},
		{"Address", b.Addr},
		{"TLS key", b.Pin + " (pinned)"},
		{"soju", updates},
		{"Status", status},
		{"Networks", strings.Join(nets, ", ")},
	}
	px, pw := lx+lw+2, x+w-3-(lx+lw+2)
	for i, r := range rows {
		label := Style{FG: t.FaceText, BG: t.Face, Bold: true}
		c.Text(px, ly+i, 9, r[0], label)
		c.Text(px+10, ly+i, pw-10, r[1], face)
	}
	a.dlgButtons(c, x, y+h-3, w, d.buttons(a), 0)
}

func (d *bouncerBox) press(a *App, button string) tea.Cmd {
	switch button {
	case "Use":
		a.useBouncer(d.sel + 1)
	case "Add...":
		a.openSetup()
	case "Update":
		name := a.bouncers[d.sel].Name
		a.openDialog(&msgBox{Title: "Bouncers", Buttons: []string{"OK"},
			Lines:   []string{"soju on " + name + " is up to date.", "", "It updates with the server's Debian packages,", "so there's nothing to do. (Mock.)"},
			OnClose: func(a *App, _ int) tea.Cmd { a.openDialog(d); return nil }})
	case "Remove":
		a.openDialog(d.confirmRemove(a))
	case "Close":
		a.dlg = nil
	}
	return nil
}

// confirmRemove asks before removing a bouncer, spelling out what happens
// to the server and to your networks.
func (d *bouncerBox) confirmRemove(a *App) *msgBox {
	i := d.sel
	b := a.bouncers[i]
	lines := []string{"Remove " + b.Name + "?", ""}
	if b.VPS {
		lines = append(lines, "This deletes the server on "+strings.SplitN(b.Where, ",", 2)[0]+",", "including its chat history. Billing stops.")
	} else {
		lines = append(lines, "This uninstalls soju from your server", "and deletes its chat history there.")
	}
	if i == a.bouncer-1 {
		lines = append(lines, "", "Your networks move back to this computer.")
	}
	return &msgBox{Title: "Remove Bouncer", Lines: lines, Buttons: []string{"Yes", "No"}, Sel: 1, Cancel: 1,
		OnClose: func(a *App, button int) tea.Cmd {
			if button == 0 {
				inUse := i == a.bouncer-1
				a.bouncers = append(a.bouncers[:i], a.bouncers[i+1:]...)
				switch {
				case inUse:
					a.useBouncer(0)
				case a.bouncer > i:
					a.bouncer--
				}
			}
			a.openDialog(d)
			return nil
		}}
}

func (d *bouncerBox) key(a *App, k string) tea.Cmd {
	switch k {
	case "up":
		d.sel--
	case "down":
		d.sel++
	case "enter":
		return d.press(a, d.buttons(a)[0])
	case "delete", "backspace":
		if len(a.bouncers) > 0 {
			return d.press(a, "Remove")
		}
	case "esc":
		a.dlg = nil
	}
	d.sel = clamp(d.sel, 0, len(a.bouncers)-1)
	return nil
}

func (d *bouncerBox) mouse(a *App, m tea.MouseMsg, r Region, double bool) tea.Cmd {
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

// bouncerItems lists None and each configured bouncer as radio buttons for
// Options, followed by the add and manage buttons.
func (a *App) bouncerItems() []optItem {
	items := []optItem{{Label: "None (connect from this computer)", Int: &a.bouncer, Val: 0}}
	for i, b := range a.bouncers {
		items = append(items, optItem{Label: bouncerLabel(b, 38), Int: &a.bouncer, Val: i + 1})
	}
	return append(items,
		optItem{Label: "Add a bouncer...", Action: (*App).openSetup},
		optItem{Label: "Manage bouncers...", Action: (*App).openBouncers})
}

// bouncerLabel is how a bouncer shows up in Options.
func bouncerLabel(b bouncer, width int) string {
	s := b.Name + " (" + b.Where + ")"
	if runewidth.StringWidth(s) > width {
		s = runewidth.Truncate(s, width, "…")
	}
	return s
}
