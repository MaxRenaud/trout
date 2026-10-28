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
// ^Cfg[,bg] color, ^] italic, ^_ underline, ^V reverse and ^O reset.
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
	if i < len(pal) {
		return pal[i]
	}
	return def // 99 means "default"; the extended 16-98 range isn't mocked
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
