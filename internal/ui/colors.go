package ui

import (
	"errors"
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
	base := a.baseTheme()
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

type colorsBox struct {
	sel    int
	sx, sy int // the Scheme box, from the last frame
}

func (a *App) openColors() { a.openDialog(&colorsBox{sel: 1}) }

func (d *colorsBox) title() string { return "Colors" }

func (d *colorsBox) size(a *App) (int, int) { return 68, len(colorItems) + 9 }

func (d *colorsBox) draw(a *App, c *Canvas, x, y, w, h int) {
	t := a.theme()
	face := Style{FG: t.FaceText, BG: t.Face}
	lx, ly, lw := x+2, y+2, 22
	// The scheme is a drop-down list, like mIRC's.
	label := Style{FG: t.FaceText, BG: t.Face, Bold: true}
	c.Text(lx, ly, lw, "Scheme:", label)
	a.underlineMnemonic(c, lx, ly, "Scheme:", 0, label)
	d.sx, d.sy = lx+9, ly
	field := Style{FG: t.TextFG, BG: t.TextBG}
	c.Fill(d.sx, ly, 24, 1, field)
	c.Text(d.sx+1, ly, 20, a.themeList()[a.themeIdx].label, field)
	c.Text(d.sx+22, ly, 1, "▼", field)
	a.addRegion(Region{X: d.sx, Y: ly, W: 24, H: 1, Kind: HitDlg, S: "scheme"})
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
	c.Text(px, ly+9, 28, "E or right-click: change one", face)
	a.dlgButtons(c, x, y+h-3, w, d.buttonLabels(a), 0)
}

// current is the palette color the selected item uses: its own choice, or
// the scheme's color if that's one of the 16 (-1 if not).
func (d *colorsBox) current(a *App) int {
	it := colorItems[d.sel]
	if idx, ok := a.colors[it.role]; ok {
		return idx
	}
	t := a.baseTheme()
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
	case "e", "alt+e":
		d.editColor(a, max(0, d.current(a)))
	case "d", "alt+d":
		if a.themeList()[a.themeIdx].fork != "" {
			d.confirmDelete(a)
		}
	case "s", "alt+s":
		d.openScheme(a)
	case "enter", "esc", "o":
		a.dlg = nil
	}
	return nil
}

func (d *colorsBox) mouse(a *App, m tea.MouseMsg, r Region, _ bool) tea.Cmd {
	if m.Action == tea.MouseActionPress && m.Button == tea.MouseButtonRight && r.S == "swatch" {
		d.editColor(a, r.I)
		return nil
	}
	if m.Action != tea.MouseActionPress || m.Button != tea.MouseButtonLeft {
		return nil
	}
	switch r.S {
	case "item":
		d.sel = r.I
	case "swatch":
		d.pick(a, r.I)
	case "scheme":
		d.openScheme(a)
	case "button":
		return d.pressButton(a, r.I)
	case "close":
		a.dlg = nil
	}
	return nil
}

// openScheme drops the Scheme list down, on the theme in use.
func (d *colorsBox) openScheme(a *App) {
	items := a.themeItems()
	a.closeMenus()
	a.pushMenu(items, d.sx, d.sy+1)
	a.menus[0].Sel = slices.IndexFunc(items, func(it MenuItem) bool { return it.Check })
}

func (d *colorsBox) takenMnemonics() string { return "s" } // Scheme

// The buttons: Delete Theme only on a custom theme ("Classic*").
func (d *colorsBox) buttonLabels(a *App) []string {
	if a.themeList()[a.themeIdx].fork != "" {
		return []string{"OK", "Reset", "Edit Color...", "Delete Theme"}
	}
	return []string{"OK", "Reset", "Edit Color..."}
}

func (d *colorsBox) typing() bool { return false }
func (d *colorsBox) pressButton(a *App, i int) tea.Cmd {
	return d.key(a, [...]string{"enter", "r", "e", "d"}[min(i, 3)])
}

// editColor asks for what color n (0-15) should look like, as #RRGGBB,
// then changes it in the theme's custom copy (customtheme.go). mIRC
// opens Windows' color picker; a terminal types the color.
func (d *colorsBox) editColor(a *App, n int) {
	sel := d.sel
	t := a.baseTheme()
	f := &formBox{Title: "Edit Color", Lines: []string{
		"What a color number looks like in " + a.themeList()[a.themeIdx].label + ".",
		"Changing one makes your own copy of the theme to change."},
		Fields:  []*formField{newField("Color number (0-15):", fmt.Sprintf("%02d", n), false), newField("Color (#RRGGBB):", fmt.Sprintf("#%06X", uint32(t.Pal[n])), false)},
		Buttons: []string{"OK", "Cancel"}}
	f.OnOK = func(a *App, vals []string) (tea.Cmd, error) {
		n, err := strconv.Atoi(strings.TrimSpace(vals[0]))
		if err != nil || n < 0 || n > 15 {
			return nil, errors.New("the color number is 0-15")
		}
		c, ok := parseHex(vals[1])
		if !ok {
			return nil, errors.New("type the color as #RRGGBB, like #FF8000")
		}
		a.setColor(n, c, true)
		if a.dlg == f { // not asking about the custom copy
			a.dlg = &colorsBox{sel: sel}
		}
		return nil, nil
	}
	f.OnCancel = func(a *App) tea.Cmd { a.dlg = &colorsBox{sel: sel}; return nil }
	a.openDialog(f)
}

// confirmDelete asks before deleting the custom theme in use.
func (d *colorsBox) confirmDelete(a *App) {
	cur := a.themeList()[a.themeIdx]
	orig := cur.fork
	if i := slices.IndexFunc(a.themeList(), func(t namedTheme) bool { return t.name == cur.fork }); i >= 0 {
		orig = a.themeList()[i].label
	}
	m := message("Delete "+cur.label+", your copy of "+orig+"?", "", "You'll be back on "+orig+", and its colors you changed are gone.")
	m.Title = "Delete Theme"
	m.Buttons, m.Sel, m.Cancel = []string{"Delete", "Cancel"}, 1, 1
	sel := d.sel
	m.OnClose = func(a *App, button int) tea.Cmd {
		if button == 0 {
			a.deleteFork()
		}
		a.openDialog(&colorsBox{sel: sel})
		return nil
	}
	a.openDialog(m)
}
