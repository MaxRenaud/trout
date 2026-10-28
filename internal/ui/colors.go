package ui

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MaxRenaud/trout/internal/state"
)

// The Colors dialog (the toolbar's Colors, or Options > Display), like
// mIRC's: pick a scheme, then give any kind of text one of the 16 mIRC
// colors. Choices are palette numbers, so they follow the scheme's palette
// when it changes, and they're saved as settings' colors, like
// "action=6 notice=5".

// colorItem is a row of the Colors dialog: a role's text, or the
// background (role -1).
type colorItem struct {
	label, key string
	role       state.Role
}

var colorItems = []colorItem{
	{"Background", "background", -1},
	{"Normal text", "normal", state.RoleNormal},
	{"Own text", "own", state.RoleOwn},
	{"Action text", "action", state.RoleAction},
	{"Join, part, mode", "event", state.RoleEvent},
	{"Quit text", "quit", state.RoleQuit},
	{"Notice text", "notice", state.RoleNotice},
	{"Info text", "info", state.RoleInfo},
	{"Highlight text", "highlight", state.RoleHighlight},
	{"Timestamps", "timestamp", state.RoleTimestamp},
	{"Switchbar: events", "act-event", actEvent},
	{"Switchbar: messages", "act-message", actMsg},
	{"Switchbar: highlights", "act-highlight", actHi},
}

// The switchbar's activity colors, like mIRC's Display options, as
// colorItem roles below zero (-1 is the background).
const (
	actEvent state.Role = -2 - iota
	actMsg
	actHi
)

// themeColor is what the scheme gives a colorItem role.
func themeColor(t *Theme, role state.Role) RGB {
	switch role {
	case -1:
		return t.TextBG
	case actEvent:
		return t.ActEvent
	case actMsg:
		return t.ActMsg
	case actHi:
		return t.ActHi
	}
	return t.Roles[role]
}

// theme is the scheme with the Colors dialog's choices applied.
func (a *App) theme() *Theme {
	base := themes[a.themeIdx]
	if len(a.colors) == 0 {
		return base
	}
	t := *base
	for role, idx := range a.colors {
		switch role {
		case -1:
			t.TextBG = t.Pal[idx]
		case actEvent:
			t.ActEvent = t.Pal[idx]
		case actMsg:
			t.ActMsg = t.Pal[idx]
		case actHi:
			t.ActHi = t.Pal[idx]
		default:
			t.Roles[role] = t.Pal[idx]
		}
	}
	return &t
}

// colorsSetting and parseColors move the choices to and from config.toml.
func (a *App) colorsSetting() string {
	var parts []string
	for _, it := range colorItems {
		if idx, ok := a.colors[it.role]; ok {
			parts = append(parts, fmt.Sprintf("%s=%d", it.key, idx))
		}
	}
	return strings.Join(parts, " ")
}

func parseColors(s string) map[state.Role]int {
	m := map[state.Role]int{}
	for kv := range strings.FieldsSeq(s) {
		k, v, _ := strings.Cut(kv, "=")
		idx, err := strconv.Atoi(v)
		i := slices.IndexFunc(colorItems, func(it colorItem) bool { return it.key == k })
		if err == nil && i >= 0 && idx >= 0 && idx < 16 {
			m[colorItems[i].role] = idx
		}
	}
	return m
}

type colorsBox struct{ sel int }

func (a *App) openColors() { a.openDialog(&colorsBox{sel: 1}) }

func (d *colorsBox) title() string { return "Colors" }

func (d *colorsBox) size(a *App) (int, int) { return 58, len(colorItems) + 9 }

func (d *colorsBox) draw(a *App, c *Canvas, x, y, w, h int) {
	t := a.theme()
	face := Style{FG: t.FaceText, BG: t.Face}
	lx, ly, lw := x+2, y+2, 22
	c.Text(lx, ly, lw, "Scheme:", Style{FG: t.FaceText, BG: t.Face, Bold: true})
	for i, name := range []string{"Classic", "Dark"} {
		box := "( ) "
		if a.themeIdx == i {
			box = "(•) "
		}
		sx := lx + 9 + i*12
		c.Text(sx, ly, 12, box+name, face)
		a.addRegion(Region{X: sx, Y: ly, W: 11, H: 1, Kind: HitDlg, S: "scheme", I: i})
	}
	ly += 2
	c.Fill(lx, ly, lw, len(colorItems), Style{BG: t.TextBG})
	for i, it := range colorItems {
		st := Style{FG: t.TextFG, BG: t.TextBG}
		if i == d.sel {
			st = Style{FG: t.SelFG, BG: t.SelBG}
		}
		c.Fill(lx, ly+i, lw, 1, st)
		// A sample of the color, then the name.
		c.Text(lx+1, ly+i, 2, "██", Style{FG: themeColor(t, it.role), BG: st.BG})
		c.Text(lx+4, ly+i, lw-4, it.label, st)
		a.addRegion(Region{X: lx, Y: ly + i, W: lw, H: 1, Kind: HitDlg, S: "item", I: i})
	}
	// The 16 mIRC colors, the selected item's marked.
	px, cur := lx+lw+3, d.current(a)
	c.Text(px, ly-2+1, 30, "Click a color:", face)
	for i, col := range t.Pal {
		sx, sy := px+(i%4)*7, ly+(i/4)*2
		fg := RGB(0x000000)
		if (299*(col>>16&0xff)+587*(col>>8&0xff)+114*(col&0xff))/1000 < 128 {
			fg = 0xFFFFFF
		}
		label := fmt.Sprintf("  %02d  ", i)
		if i == cur {
			label = fmt.Sprintf(" [%02d] ", i)
		}
		c.Text(sx, sy, 6, label, Style{FG: fg, BG: col, Bold: i == cur})
		a.addRegion(Region{X: sx, Y: sy, W: 6, H: 1, Kind: HitDlg, S: "swatch", I: i})
	}
	note := "Left/Right picks a color"
	if _, ok := a.colors[colorItems[d.sel].role]; !ok {
		note = "Using the scheme's color"
	}
	c.Text(px, ly+8, 28, note, face)
	a.dlgButtons(c, x, y+h-3, w, []string{"OK", "Reset"}, 0)
}

// current is the palette color the selected item uses: its own choice, or
// the scheme's color if that's one of the 16 (-1 if not).
func (d *colorsBox) current(a *App) int {
	it := colorItems[d.sel]
	if idx, ok := a.colors[it.role]; ok {
		return idx
	}
	t := themes[a.themeIdx]
	return slices.Index(t.Pal[:], themeColor(t, it.role))
}

func (d *colorsBox) pick(a *App, idx int) {
	if a.colors == nil {
		a.colors = map[state.Role]int{}
	}
	a.colors[colorItems[d.sel].role] = (idx + 16) % 16
}

func (d *colorsBox) key(a *App, k string) tea.Cmd {
	switch k {
	case "up":
		d.sel = (d.sel + len(colorItems) - 1) % len(colorItems)
	case "down":
		d.sel = (d.sel + 1) % len(colorItems)
	case "left", "right":
		step := 1
		if k == "left" {
			step = -1
		}
		d.pick(a, max(0, d.current(a))+step)
	case "r":
		clear(a.colors)
	case "enter", "esc", "o":
		a.dlg = nil
	}
	return nil
}

func (d *colorsBox) mouse(a *App, m tea.MouseMsg, r Region, _ bool) tea.Cmd {
	if m.Action != tea.MouseActionPress || m.Button != tea.MouseButtonLeft {
		return nil
	}
	switch r.S {
	case "item":
		d.sel = r.I
	case "swatch":
		d.pick(a, r.I)
	case "scheme":
		a.themeIdx = r.I
	case "button":
		if r.I == 1 {
			clear(a.colors)
			return nil
		}
		a.dlg = nil
	case "close":
		a.dlg = nil
	}
	return nil
}
