package ui

import (
	"fmt"
	"strings"

	"github.com/mattn/go-runewidth"

	"github.com/MaxRenaud/trout/internal/termkeys"
)

// RGB is a 24-bit color, 0xRRGGBB.
type RGB uint32

func lerp(a, b RGB, t float64) RGB {
	ch := func(shift uint) RGB {
		v := float64(a>>shift&0xff)*(1-t) + float64(b>>shift&0xff)*t
		return RGB(v+0.5) << shift
	}
	return ch(16) | ch(8) | ch(0)
}

type Style struct {
	FG, BG                  RGB
	Bold, Italic, Underline bool
	Strike                  bool
}

// A cell with R == 0 is the right half of the double-width rune before it.
type cell struct {
	R  rune
	St Style
}

// Canvas is a screen-sized cell buffer. Each frame is drawn into it and
// serialized once, which keeps overlapping popups and mouse hit-testing
// simple compared to joining styled strings.
type Canvas struct {
	W, H int
	// Colors is how many the terminal shows (termkeys.Colors): fewer than
	// 24-bit's are matched to the nearest it has. 0 is 24-bit.
	Colors int
	cells  []cell
}

func NewCanvas(w, h int, st Style) *Canvas {
	c := &Canvas{W: w, H: h, cells: make([]cell, w*h)}
	for i := range c.cells {
		c.cells[i] = cell{' ', st}
	}
	return c
}

func (c *Canvas) Set(x, y int, r rune, st Style) {
	if x >= 0 && y >= 0 && x < c.W && y < c.H {
		c.cells[y*c.W+x] = cell{r, st}
	}
}

func (c *Canvas) Fill(x, y, w, h int, st Style) {
	for j := y; j < y+h; j++ {
		for i := x; i < x+w; i++ {
			c.Set(i, j, ' ', st)
		}
	}
}

// Rune draws r at (x,y) if it fits in maxW cells and returns the cells used.
func (c *Canvas) Rune(x, y, maxW int, r rune, st Style) int {
	w := runewidth.RuneWidth(r)
	if w == 0 || w > maxW || x+w > c.W {
		return 0
	}
	c.Set(x, y, r, st)
	if w == 2 {
		c.Set(x+1, y, 0, st)
	}
	return w
}

// Text draws s clipped to maxW cells and returns the cells used.
func (c *Canvas) Text(x, y, maxW int, s string, st Style) int {
	n := 0
	for _, r := range s {
		if n >= maxW {
			break
		}
		n += c.Rune(x+n, y, maxW-n, r, st)
	}
	return n
}

// TextOver draws s in fg on top of whatever background is already there.
func (c *Canvas) TextOver(x, y, maxW int, s string, fg RGB, bold bool) {
	if y < 0 || y >= c.H {
		return
	}
	n := 0
	for _, r := range s {
		if n >= maxW || x+n >= c.W {
			break
		}
		bg := c.cells[y*c.W+x+n].St.BG
		n += c.Rune(x+n, y, maxW-n, r, Style{FG: fg, BG: bg, Bold: bold})
	}
}

// Restyle recolors a cell, keeping its rune; used for text selection.
func (c *Canvas) Restyle(x, y int, fg, bg RGB) {
	if x >= 0 && y >= 0 && x < c.W && y < c.H {
		st := &c.cells[y*c.W+x].St
		st.FG, st.BG = fg, bg
	}
}

// Dim darkens a cell; used for popup drop shadows.
func (c *Canvas) Dim(x, y int) {
	if x >= 0 && y >= 0 && x < c.W && y < c.H {
		st := &c.cells[y*c.W+x].St
		st.FG, st.BG = lerp(st.FG, 0, 0.45), lerp(st.BG, 0, 0.45)
	}
}

func (c *Canvas) String() string {
	var b strings.Builder
	b.Grow(c.W * c.H * 4)
	for y := range c.H {
		row := c.cells[y*c.W : (y+1)*c.W]
		var cur Style
		for x, cl := range row {
			r := cl.R
			switch {
			case r == 0:
				if x > 0 && row[x-1].R != 0 && runewidth.RuneWidth(row[x-1].R) == 2 {
					continue // already covered by the wide rune
				}
				r = ' '
			case runewidth.RuneWidth(r) == 2 && (x+1 >= len(row) || row[x+1].R != 0):
				r = ' ' // its right half was drawn over
			}
			if x == 0 || cl.St != cur {
				sgr(&b, cl.St, c.Colors)
				cur = cl.St
			}
			b.WriteRune(r)
		}
		b.WriteString("\x1b[0m")
		if y < c.H-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func sgr(b *strings.Builder, s Style, colors int) {
	b.WriteString("\x1b[0")
	if s.Bold {
		b.WriteString(";1")
	}
	if s.Italic {
		b.WriteString(";3")
	}
	if s.Underline {
		b.WriteString(";4")
	}
	if s.Strike {
		b.WriteString(";9")
	}
	switch {
	case colors == termkeys.Colors16:
		fg, bg := nearest16(s.FG), nearest16(s.BG)
		fmt.Fprintf(b, ";%d;%dm", ansi16(fg, 30), ansi16(bg, 40))
	case colors == termkeys.Colors256:
		fmt.Fprintf(b, ";38;5;%d;48;5;%dm", nearest256(s.FG), nearest256(s.BG))
	default:
		fmt.Fprintf(b, ";38;2;%d;%d;%d;48;2;%d;%d;%dm",
			s.FG>>16&0xff, s.FG>>8&0xff, s.FG&0xff, s.BG>>16&0xff, s.BG>>8&0xff, s.BG&0xff)
	}
}

// dist2 is how far apart two colors look, roughly: squared distance with
// green weighted most, as eyes do.
func dist2(a, b RGB) int {
	d := func(shift uint) int { return int(a>>shift&0xff) - int(b>>shift&0xff) }
	r, g, bl := d(16), d(8), d(0)
	return 2*r*r + 4*g*g + 3*bl*bl
}

// cubeLevels are the xterm 256-color cube's steps (colors 16-231).
var cubeLevels = [6]int{0, 95, 135, 175, 215, 255}

// nearest256 is the xterm color nearest c, from the cube and the gray
// ramp (232-255); 0-15 are left out, as terminals' themes change them.
func nearest256(c RGB) int {
	level := func(v int) int {
		best := 0
		for i, l := range cubeLevels {
			if abs(v-l) < abs(v-cubeLevels[best]) {
				best = i
			}
		}
		return best
	}
	r, g, b := level(int(c>>16&0xff)), level(int(c>>8&0xff)), level(int(c&0xff))
	cube := RGB(cubeLevels[r]<<16 | cubeLevels[g]<<8 | cubeLevels[b])
	avg := (int(c>>16&0xff) + int(c>>8&0xff) + int(c&0xff)) / 3
	gi := clamp((avg-8+5)/10, 0, 23)
	gv := 8 + 10*gi
	gray := RGB(gv<<16 | gv<<8 | gv)
	if dist2(c, gray) < dist2(c, cube) {
		return 232 + gi
	}
	return 16 + 36*r + 6*g + b
}

// vga16 are the 16 ANSI colors as the VGA palette has them, which is
// mIRC's own palette's, so the Classic theme maps onto them exactly.
var vga16 = [16]RGB{
	0x000000, 0x800000, 0x008000, 0x808000, 0x000080, 0x800080, 0x008080, 0xc0c0c0,
	0x808080, 0xff0000, 0x00ff00, 0xffff00, 0x0000ff, 0xff00ff, 0x00ffff, 0xffffff,
}

func nearest16(c RGB) int {
	best := 0
	for i, v := range vga16 {
		if dist2(c, v) < dist2(c, vga16[best]) {
			best = i
		}
	}
	return best
}

// ansi16 is the SGR code for color i (0-15) from base, 30 for the
// foreground and 40 for the background; the bright ones are 90 and 100.
func ansi16(i, base int) int {
	if i < 8 {
		return base + i
	}
	return base + 60 + i - 8
}

func abs(v int) int { return max(v, -v) }
