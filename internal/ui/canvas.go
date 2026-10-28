package ui

import (
	"fmt"
	"strings"

	"github.com/mattn/go-runewidth"
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
	W, H  int
	cells []cell
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
				sgr(&b, cl.St)
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

func sgr(b *strings.Builder, s Style) {
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
	fmt.Fprintf(b, ";38;2;%d;%d;%d;48;2;%d;%d;%dm",
		s.FG>>16&0xff, s.FG>>8&0xff, s.FG&0xff, s.BG>>16&0xff, s.BG>>8&0xff, s.BG&0xff)
}
