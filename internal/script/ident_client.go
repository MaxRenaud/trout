package script

import (
	"path"
	"slices"
	"strings"
)

// Identifiers about the client: its colors, its windows' focus, and the
// scripts folder's files. Checked in mIRC 7.86 (tmp/misc-oracle*.mrc).

// Colors is the client's palette and the color numbers it uses for kinds
// of text, for $color.
type Colors struct {
	// Pal is colors 0-98 as 0xRRGGBB: the scheme's 16, then mIRC's fixed
	// ones.
	Pal [99]uint32
	// Index is the palette number of each kind of text the client colors:
	// background, normal, own, action, event (joins, parts, modes...),
	// quit, notice, info, highlight.
	Index map[string]int
}

// DirEntry is a file or folder in the scripts folder, for $findfile.
type DirEntry struct {
	Name  string
	IsDir bool
}

// colorNames are mIRC's Colors dialog items, in its order, with the kind
// of trout's text each is, or "" with mIRC's default color for the ones
// trout doesn't color separately.
var colorNames = []struct {
	name, kind string
	def        int
}{
	{"background", "background", 0}, {"action text", "action", 6}, {"ctcp text", "", 4},
	{"highlight text", "highlight", 5}, {"info text", "info", 2}, {"info2 text", "info", 3},
	{"invite text", "event", 3}, {"join text", "event", 3}, {"kick text", "event", 3},
	{"mode text", "event", 3}, {"nick text", "event", 3}, {"normal text", "normal", 1},
	{"notice text", "notice", 5}, {"notify text", "", 7}, {"other text", "", 6},
	{"own text", "own", 1}, {"part text", "event", 3}, {"quit text", "quit", 2},
	{"topic text", "event", 3}, {"wallops text", "", 5}, {"whois text", "normal", 1},
	{"editbox", "background", 0}, {"editbox text", "normal", 1}, {"listbox", "background", 0},
	{"listbox text", "normal", 1}, {"gray text", "", 14}, {"title text", "", 6},
	{"inactive", "background", 0}, {"treebar", "background", 0}, {"treebar text", "normal", 1},
}

// colorIdent is $color(name): the color number of a kind of text (a name
// can be cut short, $color(action); one that matches nothing is normal
// text's, checked), .dd in two digits; $color(N) is color N as Windows
// writes it, blue * 65536 + green * 256 + red.
func colorIdent(r *run, fr *frame, c *call) (string, error) {
	cs := r.h.Colors()
	arg := strings.ToLower(strings.TrimSpace(c.arg(0)))
	if n, ok := parseNum(arg); ok && isDigits(arg) {
		i := int(n) % 100
		if i == 99 {
			return "0", nil // checked: 99 is 0, 100 is 0's again
		}
		return itoa(int(colorref(cs.Pal[i]))), nil
	}
	idx := -1
	for _, exact := range []bool{true, false} {
		for _, it := range colorNames {
			if exact && it.name == arg || !exact && arg != "" && strings.HasPrefix(it.name, arg) {
				idx = it.def
				if v, ok := cs.Index[it.kind]; ok && it.kind != "" {
					idx = v
				}
				break
			}
		}
		if idx >= 0 {
			break
		}
	}
	if idx < 0 {
		idx = 1
		if v, ok := cs.Index["normal"]; ok {
			idx = v
		}
	}
	if c.prop == "dd" {
		return twoDigits(idx), nil
	}
	return itoa(idx), nil
}

// colorref is 0xRRGGBB as Windows' COLORREF, which mIRC's numbers are.
func colorref(rgb uint32) uint32 {
	r, g, b := rgb>>16&0xff, rgb>>8&0xff, rgb&0xff
	return b<<16 | g<<8 | r
}

// rgbIdent is $rgb(R,G,B): one number, each part wrapping at 256; $rgb(N)
// is it back as R,G,B; $rgb(face) and $rgb(shadow) are Windows' button
// colors (Windows 11's, checked).
func rgbIdent(r *run, fr *frame, c *call) (string, error) {
	switch len(c.vals) {
	case 3:
		v := int64(c.num(0))&0xff | (int64(c.num(1))&0xff)<<8 | (int64(c.num(2))&0xff)<<16
		return itoa(int(v)), nil
	case 1:
		switch strings.ToLower(c.arg(0)) {
		case "face":
			return "15790320", nil
		case "shadow":
			return "10526880", nil
		}
		v := int64(c.num(0))
		return itoa(int(v&0xff)) + "," + itoa(int(v>>8&0xff)) + "," + itoa(int(v>>16&0xff)), nil
	}
	return "", errorf("Invalid parameters: $rgb")
}

// findFiles is what $findfile(dir,wildcard,...) finds, in mIRC's order: a
// folder's own files by name (ignoring case), then its folders', depth
// levels down (1 or 0: the folder alone; -1: all of them). Names are the
// scripts folder's, with / between folders.
func (r *run) findFiles(dir, wildcards string, depth int) []string {
	var pats []string
	for w := range strings.SplitSeq(wildcards, ";") {
		if w = strings.TrimSpace(w); w != "" {
			pats = append(pats, w)
		}
	}
	var out []string
	var walk func(dir string, level int)
	walk = func(dir string, level int) {
		entries, ok := r.h.ListDir(dir)
		if !ok {
			return
		}
		slices.SortFunc(entries, func(a, b DirEntry) int {
			return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
		})
		var dirs []string
		for _, e := range entries {
			full := path.Join(dir, e.Name)
			if e.IsDir {
				dirs = append(dirs, full)
				continue
			}
			if slices.ContainsFunc(pats, func(p string) bool { return wildMatch(p, e.Name, false) }) {
				out = append(out, full)
			}
		}
		if depth >= 0 && level >= max(depth, 1) {
			return
		}
		for _, d := range dirs {
			walk(d, level+1)
		}
	}
	walk(cleanDir(dir), 1)
	return out
}

// cleanDir is a script's folder name as the scripts folder's: "" or "." is
// the folder itself, $mircdir's "" too, and mIRC's backslashes work.
func cleanDir(dir string) string {
	dir = path.Clean(strings.ReplaceAll(strings.TrimSpace(dir), `\`, "/"))
	if dir == "." || dir == "/" {
		return ""
	}
	return strings.TrimPrefix(dir, "./")
}

// findfileIdent is $findfile(dir,wildcard,N[,depth][,@window|command]):
// the Nth file found, or with N = 0 how many. With a command it runs for
// each one ($1- is its name), stopping at /halt; with a @window listbox
// it fills it. Both return how many (checked).
func findfileIdent(r *run, fr *frame, c *call) (string, error) {
	if len(c.vals) < 3 {
		return "", errorf("Invalid parameters: $findfile")
	}
	// The last argument is a @window, or a command, which runs as it
	// was written for each file (unevaluated till then: $1- is the file).
	depth, from := -1, 3
	if len(c.vals) > 3 {
		if d, ok := parseNum(c.arg(3)); ok && c.arg(3) != "" {
			depth, from = int(d), 4
		}
	}
	rest := ""
	if from < len(c.args) {
		rest = strings.TrimSpace(strings.Join(c.args[from:], ","))
	}
	found := r.findFiles(c.arg(0), c.arg(1), depth)
	n := int(c.num(2))
	switch {
	case strings.HasPrefix(rest, "@"):
		name, err := r.eval(fr, rest)
		if err != nil {
			return "", err
		}
		if w := r.customWindow(name); w != nil {
			var lines []WinLine
			for _, f := range found {
				lines = append(lines, WinLine{Text: f, Color: -1})
			}
			w.SetLines(false, append(slices.Clone(w.Lines(false)), lines...))
		}
		return itoa(len(found)), nil
	case rest != "":
		// The command is evaluated for each file, with $1- its name.
		for i, f := range found {
			r.findfileN = i + 1
			sub := *fr
			sub.params = []string{f}
			if err := r.command(&sub, rest); err != nil {
				if err == errHalt {
					return "", nil
				}
				return "", err
			}
		}
		return itoa(len(found)), nil
	case n == 0:
		return itoa(len(found)), nil
	case n >= 1 && n <= len(found):
		r.findfileN = n
		return found[n-1], nil
	}
	return "", nil
}

var clientIdents = map[string]builtin{
	"color":    colorIdent,
	"rgb":      rgbIdent,
	"findfile": findfileIdent,
	// $findfilen is where the last file $findfile gave was in its list.
	"findfilen": func(r *run, fr *frame, c *call) (string, error) { return itoa(r.findfileN), nil },
	// $mircini is mIRC's settings file: here a mirc.ini in the scripts
	// folder, which holds none of trout's settings.
	"mircini": str("mirc.ini"),
	"isdir": func(r *run, fr *frame, c *call) (string, error) {
		_, ok := r.h.ListDir(cleanDir(c.arg(0)))
		return boolStr(ok && cleanDir(c.arg(0)) != ""), nil
	},
	// $appactive is whether trout's terminal is in front.
	"appactive": func(r *run, fr *frame, c *call) (string, error) { return boolStr(r.h.AppActive()), nil },
	// $lactive is the window active before this one ($null at first).
	"lactive": func(r *run, fr *frame, c *call) (string, error) {
		_, w, ok := r.h.LastActive()
		switch {
		case !ok:
			return "", nil
		case w == "":
			return "Status Window", nil
		}
		return w, nil
	},
}
