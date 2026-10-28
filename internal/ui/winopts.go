package ui

import (
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MaxRenaud/trout/internal/config"
	"github.com/MaxRenaud/trout/internal/state"
)

// A window's own options, like the ones in mIRC's window system menu
// (Timestamp, Events, Beeping, Nicklist, Track URLs, Editbox). trout has
// no system menu, so they're in the switchbar and treebar popup. They're
// saved by network and window name, so a channel keeps them.

func winKey(w *state.Window) string {
	return strings.ToLower(w.Net.Name) + "/" + w.Net.Fold(w.Name)
}

func (a *App) winOpts(w *state.Window) config.WindowOpts {
	if w == nil || w.Net == nil {
		return config.WindowOpts{}
	}
	return a.book().Windows[winKey(w)]
}

func (a *App) setWinOpts(w *state.Window, edit func(*config.WindowOpts)) {
	b := a.book()
	o := b.Windows[winKey(w)]
	edit(&o)
	if o == (config.WindowOpts{}) {
		delete(b.Windows, winKey(w))
	} else {
		if b.Windows == nil {
			b.Windows = map[string]config.WindowOpts{}
		}
		b.Windows[winKey(w)] = o
	}
	a.saveConfig()
}

// stampsFor is whether w shows timestamps: its own choice, or Options.
func (a *App) stampsFor(w *state.Window) bool {
	switch a.winOpts(w).Timestamps {
	case "on":
		return true
	case "off":
		return false
	}
	return a.stamps
}

// eventLine shows a join, part, quit, nick change or mode change in a
// channel, where its Events option says: there, in the status window, or
// nowhere. It reports whether the status window got it.
func (a *App) eventLine(w *state.Window, l state.Line) (toStatus bool) {
	switch a.winOpts(w).Events {
	case "hide":
		return false
	case "status":
		l.Text += " (" + w.Name + ")"
		a.addLineL(w.Net.Status(), l)
		return true
	}
	a.addLineL(w, l)
	return false
}

// winOptItems are the window popup's own-options items.
func (a *App) winOptItems(w *state.Window) []MenuItem {
	o := a.winOpts(w)
	set := func(edit func(*config.WindowOpts)) func(*App) tea.Cmd {
		return func(a *App) tea.Cmd { a.setWinOpts(w, edit); return nil }
	}
	stamps := !a.stampsFor(w)
	items := []MenuItem{
		{Label: "Timestamp", Check: !stamps, Action: set(func(o *config.WindowOpts) {
			o.Timestamps = map[bool]string{true: "on", false: "off"}[stamps]
			if stamps == a.stamps {
				o.Timestamps = ""
			}
		})},
		{Label: "Beeping", Check: o.Beep == "on", Action: set(func(o *config.WindowOpts) {
			o.Beep = map[bool]string{true: "", false: "on"}[o.Beep == "on"]
		})},
	}
	if w.Kind == state.KChannel {
		radio := func(label, val string, cur string, field func(*config.WindowOpts) *string) MenuItem {
			return MenuItem{Label: label, Check: cur == val, Action: set(func(o *config.WindowOpts) { *field(o) = val })}
		}
		events := func(o *config.WindowOpts) *string { return &o.Events }
		nicks := func(o *config.WindowOpts) *string { return &o.Nicklist }
		items = append(items,
			MenuItem{Label: "Events", Sub: []MenuItem{
				radio("Show in channel", "", o.Events, events),
				radio("Show in status window", "status", o.Events, events),
				radio("Hide", "hide", o.Events, events),
			}},
			MenuItem{Label: "Nicklist", Sub: []MenuItem{
				radio("Right", "", o.Nicklist, nicks),
				radio("Left", "left", o.Nicklist, nicks),
			}})
	}
	if w.Kind != state.KTransfer {
		items = append(items,
			MenuItem{Label: "Track URLs", Check: o.TrackURLs != "off", Action: set(func(o *config.WindowOpts) {
				o.TrackURLs = map[bool]string{true: "", false: "off"}[o.TrackURLs == "off"]
			})},
			MenuItem{Label: "Second Editbox", Key: "Alt+Q", Check: w.Edit2On,
				Action: func(a *App) tea.Cmd { a.toggleEdit2(w); return nil }})
	}
	return items
}

// openOrder is mIRC's Window > Order: move the active network's windows
// up and down the switchbar and treebar. Its status window stays first.
func (a *App) openOrder() {
	n := a.active.Net
	l := &listBox{Title: "Order - " + n.Name, Header: "Windows, in switchbar order",
		Buttons: []string{"Move Up", "Move Down", "Close"}}
	if a.sortSwitch {
		l.Header = "Sorted by Options > Display; turn that off to use this"
	}
	fill := func() {
		l.Rows, l.Keys = nil, nil
		for i, w := range n.Wins[1:] {
			l.Rows = append(l.Rows, w.Name)
			l.Keys = append(l.Keys, strconv.Itoa(i+1))
		}
		if len(l.Rows) == 0 {
			l.Rows = []string{"Only the status window so far."}
		}
	}
	fill()
	l.OnPress = func(a *App, l *listBox, button, key string) tea.Cmd {
		i, err := strconv.Atoi(key)
		if err != nil {
			return nil
		}
		j := i - 1
		if button == "Move Down" {
			j = i + 1
		}
		if j < 1 || j >= len(n.Wins) {
			return nil
		}
		n.Wins[i], n.Wins[j] = n.Wins[j], n.Wins[i]
		fill()
		l.Sel = j - 1
		return nil
	}
	a.openDialog(l)
}
