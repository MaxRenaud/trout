package ui

import "fmt"

// palette is the color picker Ctrl+K opens above the editbox, like mIRC's.
// The first pick inserts the text color after the ^K, the second an
// optional background.
type palette struct {
	bg   bool
	sel  int
	x, y int // caret position when it opened
}

func (a *App) openPalette() { a.pal = &palette{x: a.caretX, y: a.caretY} }

// paletteKey handles keys while the picker is open. Any other key closes it
// and is typed as usual, so color numbers can still be typed by hand.
func (a *App) paletteKey(k string) bool {
	p := a.pal
	switch k {
	case "left":
		p.sel = (p.sel + 15) % 16
	case "right":
		p.sel = (p.sel + 1) % 16
	case "up", "down":
		p.sel = (p.sel + 8) % 16
	case "enter":
		a.pickColor(p.sel)
	case "esc":
		a.pal = nil
	default:
		a.pal = nil
		return false
	}
	return true
}

func (a *App) pickColor(i int) {
	e := a.edit(a.active)
	if a.pal.bg {
		e.Insert([]rune(fmt.Sprintf(",%02d", i))...)
		a.pal = nil
		return
	}
	e.Insert([]rune(fmt.Sprintf("%02d", i))...)
	a.pal.bg = true
}

func (a *App) drawPalette(c *Canvas) {
	p := a.pal
	if p == nil {
		return
	}
	t := a.theme()
	const sw = 4 // swatch width
	w, h := 8*sw+2, 5
	x, y := clamp(p.x-1, 0, a.w-w), max(0, p.y-h)
	a.raisedBox(c, x, y, w, h)
	title := "Text color"
	if p.bg {
		title = "Background (Esc to skip)"
	}
	c.Text(x+2, y+1, w-4, title, Style{FG: t.FaceText, BG: t.Face})
	for i, col := range t.Pal {
		sx, sy := x+1+(i%8)*sw, y+2+i/8
		fg := RGB(0x000000)
		if (299*(col>>16&0xff)+587*(col>>8&0xff)+114*(col&0xff))/1000 < 128 {
			fg = 0xFFFFFF
		}
		label := fmt.Sprintf(" %02d ", i)
		if i == p.sel {
			label = fmt.Sprintf("[%02d]", i)
		}
		c.Text(sx, sy, sw, label, Style{FG: fg, BG: col, Bold: i == p.sel})
		a.addRegion(Region{X: sx, Y: sy, W: sw, H: 1, Kind: HitSwatch, I: i})
	}
}
