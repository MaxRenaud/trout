package ui

import (
	"hash/fnv"
	"strings"

	"github.com/mattn/go-runewidth"
)

// SRune is a rune with its resolved style, and the URL it's part of.
type SRune struct {
	R   rune
	St  Style
	URL *string
}

// parseMIRC applies mIRC formatting codes on top of base: ^B bold,
// ^Cfg[,bg] color, ^] italic, ^_ underline, ^^ strikethrough (typed as
// Ctrl+E, like mIRC), ^V reverse and ^O reset.
func parseMIRC(s string, base Style, pal *[16]RGB) []SRune {
	rs := []rune(s)
	out := make([]SRune, 0, len(rs))
	st, rev := base, false
	for i := 0; i < len(rs); i++ {
		switch r := rs[i]; r {
		case 0x02:
			st.Bold = !st.Bold
		case 0x1d:
			st.Italic = !st.Italic
		case 0x1f:
			st.Underline = !st.Underline
		case 0x1e:
			st.Strike = !st.Strike
		case 0x16:
			rev = !rev
		case 0x0f:
			st, rev = base, false
		case 0x03:
			fg, n := readColor(rs, i+1)
			if n == 0 {
				st.FG, st.BG = base.FG, base.BG
				continue
			}
			i += n
			st.FG = palColor(pal, fg, base.FG)
			if i+1 < len(rs) && rs[i+1] == ',' {
				if bg, m := readColor(rs, i+2); m > 0 {
					st.BG = palColor(pal, bg, base.BG)
					i += 1 + m
				}
			}
		default:
			if r < 0x20 {
				continue
			}
			cs := st
			if rev {
				cs.FG, cs.BG = cs.BG, cs.FG
			}
			out = append(out, SRune{R: r, St: cs})
		}
	}
	return out
}

func readColor(rs []rune, i int) (v, n int) {
	for n < 2 && i+n < len(rs) && rs[i+n] >= '0' && rs[i+n] <= '9' {
		v = v*10 + int(rs[i+n]-'0')
		n++
	}
	return v, n
}

func palColor(pal *[16]RGB, i int, def RGB) RGB {
	switch {
	case i < len(pal):
		return pal[i]
	case i < 99:
		return extendedColors[i-16]
	}
	return def // 99 is "default"
}

// extendedColors are mIRC's colors 16-98, which are fixed: they look the
// same in every scheme, unlike 0-15.
var extendedColors = [83]RGB{
	0x470000, 0x472100, 0x474700, 0x324700, 0x004700, 0x00472c, 0x004747, 0x002747, 0x000047, 0x2e0047, 0x470047, 0x47002a,
	0x740000, 0x743a00, 0x747400, 0x517400, 0x007400, 0x007449, 0x007474, 0x004074, 0x000074, 0x4b0074, 0x740074, 0x740045,
	0xb50000, 0xb56300, 0xb5b500, 0x7db500, 0x00b500, 0x00b571, 0x00b5b5, 0x0063b5, 0x0000b5, 0x7500b5, 0xb500b5, 0xb5006b,
	0xff0000, 0xff8c00, 0xffff00, 0xb2ff00, 0x00ff00, 0x00ffa0, 0x00ffff, 0x008cff, 0x0000ff, 0xa500ff, 0xff00ff, 0xff0098,
	0xff5959, 0xffb459, 0xffff71, 0xcfff60, 0x6fff6f, 0x65ffc9, 0x6dffff, 0x59b4ff, 0x5959ff, 0xc459ff, 0xff66ff, 0xff59bc,
	0xff9c9c, 0xffd39c, 0xffff9c, 0xe2ff9c, 0x9cff9c, 0x9cffdb, 0x9cffff, 0x9cd3ff, 0x9c9cff, 0xdc9cff, 0xff9cff, 0xff94d3,
	0x000000, 0x131313, 0x282828, 0x363636, 0x4d4d4d, 0x656565, 0x818181, 0x9f9f9f, 0xbcbcbc, 0xe2e2e2, 0xffffff,
}

// markURLs underlines http(s) links and tags their runes with the URL, so
// they can be double-clicked.
func markURLs(rs []SRune) {
	hasPrefix := func(i int, p string) bool {
		for _, r := range p {
			if i >= len(rs) || rs[i].R != r {
				return false
			}
			i++
		}
		return true
	}
	for i := 0; i < len(rs); i++ {
		if rs[i].R == '#' && (i == 0 || rs[i-1].R == ' ' || rs[i-1].R == '(') {
			i = markChannel(rs, i) // a channel hotlink, like mIRC's: double-click joins
			continue
		}
		if !hasPrefix(i, "https://") && !hasPrefix(i, "http://") {
			continue
		}
		j := i
		for j < len(rs) && rs[j].R > ' ' && !strings.ContainsRune(`<>"'`, rs[j].R) {
			j++
		}
		for j > i && strings.ContainsRune(".,;:!?)", rs[j-1].R) {
			j--
		}
		u := string(func() []rune {
			out := make([]rune, j-i)
			for k := range out {
				out[k] = rs[i+k].R
			}
			return out
		}())
		for k := i; k < j; k++ {
			rs[k].St.Underline = true
			rs[k].URL = &u
		}
		i = j
	}
}

// markChannel tags a #channel at i as a link (its URL is the channel
// name) and returns where it ends.
func markChannel(rs []SRune, i int) int {
	j := i + 1
	for j < len(rs) && rs[j].R > ' ' && !strings.ContainsRune(",:;!?)\"'", rs[j].R) {
		j++
	}
	for j > i+1 && rs[j-1].R == '.' {
		j--
	}
	if j-i < 2 {
		return i
	}
	name := make([]rune, j-i)
	for k := range name {
		name[k] = rs[i+k].R
	}
	ch := string(name)
	for k := i; k < j; k++ {
		rs[k].St.Underline = true
		rs[k].URL = &ch
	}
	return j
}

var nickPalette = []int{2, 3, 4, 5, 6, 7, 10, 12, 13}

// nickColor picks a stable mIRC color index for a nick.
func nickColor(nick string) int {
	h := fnv.New32a()
	h.Write([]byte(strings.ToLower(nick)))
	return nickPalette[h.Sum32()%uint32(len(nickPalette))]
}

func stripCodes(s string) string {
	var b strings.Builder
	for _, r := range parseMIRC(s, Style{}, &classicTheme.Pal) {
		b.WriteRune(r.R)
	}
	return b.String()
}

// wrap breaks a line into rows of at most w cells, preferring to break
// after a space. Rows after the first are w-indent cells wide.
func wrap(line []SRune, w, indent int) [][]SRune {
	var rows [][]SRune
	avail := w
	for start := 0; start < len(line); {
		width, end, brk := 0, start, -1
		for end < len(line) {
			rw := runewidth.RuneWidth(line[end].R)
			if width+rw > avail {
				break
			}
			width += rw
			if line[end].R == ' ' {
				brk = end + 1
			}
			end++
		}
		if end < len(line) && brk > start {
			end = brk
		}
		if end == start {
			end++ // a rune wider than the row; emit it anyway to make progress
		}
		rows = append(rows, line[start:end])
		start = end
		avail = w - indent
	}
	if len(rows) == 0 {
		rows = [][]SRune{nil}
	}
	return rows
}
