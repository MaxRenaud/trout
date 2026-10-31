package ui

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"

	"github.com/MaxRenaud/trout/internal/script"
	"github.com/MaxRenaud/trout/internal/state"
)

// The Scripts Editor (Tools > Scripts, Alt+R, Alt+P), like mIRC's: tabs
// for Aliases, Popups, Remote, Users and Variables, each editing its own
// file in the scripts folder (aliases.ini, popups.ini, remote.ini,
// users.ini, vars.ini, in mIRC's format) and the files loaded on it. OK
// saves what changed and loads it again, Cancel forgets it, and a script
// loaded here runs its initialization once the editor has closed, like
// mIRC's.

type edTab int

const (
	edAliases edTab = iota
	edPopups
	edRemote
	edUsers
	edVars
)

var edTabNames = [...]string{"Aliases", "Popups", "Remote", "Users", "Variables"}

// edDefaults are each tab's own file, and edSections the ini section it
// keeps its lines in (popups.ini has one per menu).
var (
	edDefaults = [...]string{"aliases.ini", "popups.ini", "remote.ini", "users.ini", "vars.ini"}
	edSections = [...]string{"aliases", "", "script", "users", "variables"}
)

// popupKinds are the Popups tab's menus, in its View menu, by mIRC's names.
var popupKinds = []struct{ kind, label string }{
	{"status", "Status"}, {"channel", "Channel"}, {"query", "Query"},
	{"nicklist", "Nickname List"}, {"menubar", "Menu Bar"}}

func popupLabel(kind string) string {
	for _, k := range popupKinds {
		if k.kind == kind {
			return k.label
		}
	}
	return kind
}

// The editor's own menubar. mIRC's Listen menu turns the remote's
// listening on and off; trout has one switch for it, /remote.
var edMenuNames = [...]string{"File", "Edit", "View", "Index", "Listen", "Options", "Help"}

const edMnemonics = "feviloh"

var edButtons = []string{"OK", "Cancel", "Help"}

// scriptDoc is a file in the editor, or one of popups.ini's menus.
type scriptDoc struct {
	tab   edTab
	file  string // a name in the scripts folder, or a full path
	kind  string // on the Popups tab, the menu
	def   bool   // the tab's own file
	ini   string // the ini section it's saved in, or "" for plain text
	lines [][]rune

	row, col   int // the caret
	arow, acol int // the selection's other end, when sel
	sel        bool
	top, left  int  // scrolled to
	follow     bool // scroll to the caret when next drawn

	undo   []docSnap
	redo   []docSnap
	lastOp string // typing is undone a run at a time
	disk   string // the file as it was read or saved, for Monitor File Changes
	dirty  bool   // changed since it was read
	load   bool   // to load on OK: from Load..., New or Save As...
	was    string // what it was loaded as before Save As...
}

type docSnap struct {
	lines    [][]rune
	row, col int
}

type scriptEditor struct {
	tab     edTab
	docs    []*scriptDoc
	cur     [len(edTabNames)]*scriptDoc
	unloads []string // config entries File > Unload took out, for OK
	find    string
	drag    bool

	tx, ty, tw, th int // the text area, from the last frame
}

// openScripts opens the Scripts Editor on the tab it was last on.
func (a *App) openScripts() { a.openScriptsTab(a.scriptTab) }

func (a *App) openScriptsTab(tab edTab) {
	if a.scripts == nil {
		return
	}
	ed := &scriptEditor{tab: tab}
	data, _ := a.scriptReadFile("popups.ini")
	secs, _, _ := iniSections(data)
	for _, k := range popupKinds {
		d := &scriptDoc{tab: edPopups, file: "popups.ini", kind: k.kind, def: true, ini: script.PopupSections[k.kind], disk: data}
		d.setText(strings.Join(secs[d.ini], "\n"))
		ed.add(d)
	}
	for _, tab := range []edTab{edAliases, edRemote} {
		d := &scriptDoc{tab: tab, file: edDefaults[tab], def: true}
		a.readDoc(d)
		ed.add(d)
	}
	for _, entry := range a.book().Scripts {
		as, file := splitLoad(entry)
		d := &scriptDoc{tab: tabOf(entry), file: file}
		if d.tab == edPopups {
			d.kind = as
		}
		if ed.findDoc(d.tab, file) != nil || d.tab == edPopups && strings.EqualFold(file, "popups.ini") {
			continue
		}
		a.readDoc(d)
		ed.add(d)
	}
	users := &scriptDoc{tab: edUsers, file: "users.ini", def: true, ini: "users"}
	users.setText(strings.Join(a.scripts.Users(), "\n"))
	vars := a.scripts.Vars()
	var lines []string
	for _, n := range slices.Sorted(maps.Keys(vars)) {
		lines = append(lines, "%"+n+" "+vars[n])
	}
	vs := &scriptDoc{tab: edVars, file: "vars.ini", def: true, ini: "variables"}
	vs.setText(strings.Join(lines, "\n"))
	ed.add(users)
	ed.add(vs)
	a.openDialog(ed)
}

func (ed *scriptEditor) add(d *scriptDoc) {
	ed.docs = append(ed.docs, d)
	if ed.cur[d.tab] == nil {
		ed.cur[d.tab] = d
	}
}

func (ed *scriptEditor) doc() *scriptDoc { return ed.cur[ed.tab] }

func (ed *scriptEditor) findDoc(tab edTab, file string) *scriptDoc {
	for _, d := range ed.docs {
		if d.tab == tab && strings.EqualFold(d.file, file) && (tab != edPopups || !d.def) {
			return d
		}
	}
	return nil
}

func (ed *scriptEditor) setTab(a *App, tab edTab) {
	ed.tab, a.scriptTab = tab, tab
	ed.drag = false
}

// readDoc reads d's file: its ini section's lines, without the nN= in
// front, as mIRC shows them, or the text as it is. A missing file is
// empty.
func (a *App) readDoc(d *scriptDoc) {
	data, _ := a.readScript(d.file)
	d.disk = data
	secs, names, ini := iniSections(data)
	switch {
	case ini && len(names) == 1:
		d.ini = names[0]
		d.setText(strings.Join(secs[names[0]], "\n"))
		return
	case strings.TrimSpace(data) == "" && strings.HasSuffix(strings.ToLower(d.file), ".ini"):
		d.ini = edSections[d.tab]
	}
	d.setText(data)
}

// iniSections splits an ini file into its sections' nN= values, in order.
// ini is false if data isn't one: a script in plain text.
func iniSections(data string) (secs map[string][]string, names []string, ini bool) {
	secs = map[string][]string{}
	sec := ""
	for l := range strings.Lines(data) {
		l = strings.TrimRight(l, "\r\n")
		t := strings.TrimSpace(l)
		if sec == "" && (t == "" || strings.HasPrefix(t, ";")) {
			continue
		}
		if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") {
			sec = strings.ToLower(t[1 : len(t)-1])
			if _, ok := secs[sec]; !ok {
				names = append(names, sec)
				secs[sec] = nil
			}
			continue
		}
		if sec == "" {
			return nil, nil, false
		}
		if k, v, ok := strings.Cut(l, "="); ok && len(k) > 1 && k[0] == 'n' && isDigits(k[1:]) {
			l = v
		}
		secs[sec] = append(secs[sec], l)
	}
	return secs, names, len(names) > 0
}

func isDigits(s string) bool {
	return s != "" && strings.Trim(s, "0123456789") == ""
}

// setText replaces d's lines, with tabs as two spaces.
func (d *scriptDoc) setText(s string) {
	s = strings.NewReplacer("\r\n", "\n", "\r", "\n", "\t", "  ").Replace(s)
	s = strings.TrimSuffix(s, "\n")
	d.lines = nil
	for l := range strings.SplitSeq(s, "\n") {
		d.lines = append(d.lines, []rune(l))
	}
}

func (d *scriptDoc) text() string {
	ls := make([]string, len(d.lines))
	for i, l := range d.lines {
		ls[i] = string(l)
	}
	return strings.Join(ls, "\n")
}

// data is d's file as it's saved: n0=, n1=... under its ini section, or
// the text.
func (d *scriptDoc) data() string {
	if d.ini == "" {
		if t := d.text(); t != "" {
			return t + "\n"
		}
		return ""
	}
	return writeIniList(d.ini, d.saved())
}

// saved is d's lines without the empty ones at the end.
func (d *scriptDoc) saved() []string {
	ls := strings.Split(d.text(), "\n")
	for len(ls) > 0 && strings.TrimSpace(ls[len(ls)-1]) == "" {
		ls = ls[:len(ls)-1]
	}
	return ls
}

// as is how d is loaded: LoadAs's as.
func (d *scriptDoc) as() string {
	switch {
	case d.tab == edAliases:
		return "aliases"
	case d.tab == edPopups && d.def:
		return "popups"
	case d.tab == edPopups:
		return d.kind
	}
	return ""
}

// entry is d's entry in the config's scripts: /load's switch and the file.
func (d *scriptDoc) entry() string {
	switch as := d.as(); as {
	case "":
		return d.file
	case "aliases":
		return "-a " + d.file
	case "popups":
		return "-p " + d.file
	default:
		for sw, k := range loadKinds {
			if k == as {
				return sw + " " + d.file
			}
		}
	}
	return d.file
}

// label is d in the View menu.
func (d *scriptDoc) label() string {
	if d.tab == edPopups {
		if d.def {
			return popupLabel(d.kind)
		}
		return d.file + " (" + popupLabel(d.kind) + ")"
	}
	return d.file
}

func (ed *scriptEditor) title() string {
	d := ed.doc()
	switch {
	case d.tab == edPopups && d.def:
		return "Scripts Editor - " + popupLabel(d.kind)
	case d.tab == edUsers || d.tab == edVars:
		return "Scripts Editor - " + edTabNames[d.tab]
	}
	return "Scripts Editor - " + filepath.Base(d.file)
}

func (ed *scriptEditor) size(a *App) (int, int) { return max(60, a.w-4), max(16, a.h-2) }

func (ed *scriptEditor) draw(a *App, c *Canvas, x, y, w, h int) {
	t := a.theme()
	face := Style{FG: t.FaceText, BG: t.Face}
	d := ed.doc()

	// The menubar.
	c.Fill(x+1, y+1, w-2, 1, face)
	mx := x + 1
	for i, name := range edMenuNames {
		st := face
		if a.dlgMenu && a.menuBar == i && len(a.menus) > 0 {
			st = Style{FG: t.MenuSelText, BG: t.MenuSel}
		}
		bw := len(name) + 2
		c.Fill(mx, y+1, bw, 1, st)
		for j, r := range name {
			s := st
			s.Underline = j == 0
			c.Set(mx+1+j, y+1, r, s)
		}
		a.addRegion(Region{X: mx, Y: y + 1, W: bw, H: 1, Kind: HitDlg, S: "strip", I: i})
		mx += bw
	}

	// The tabs, and the check button at their right.
	tx := x + 2
	for i, name := range edTabNames {
		st, label := face, " "+name+" "
		if edTab(i) == ed.tab {
			st = Style{FG: t.SelFG, BG: t.SelBG, Bold: true}
		}
		c.Text(tx, y+2, len(label), label, st)
		a.addRegion(Region{X: tx, Y: y + 2, W: len(label), H: 1, Kind: HitDlg, S: "tab", I: i})
		tx += len(label) + 1
	}
	bw := a.button(c, x+w-6, y+2, "✓", face, 1)
	a.addRegion(Region{X: x + w - 6, Y: y + 2, W: bw, H: 1, Kind: HitDlg, S: "check"})

	// The text, with line numbers in a gutter, and its scrollbar.
	ax, ay, aw, ah := x+2, y+3, w-4, h-7
	gutter := 0
	if !a.book().ScriptOpts.NoLineNumbers {
		gutter = max(3, len(strconv.Itoa(len(d.lines)))) + 1
	}
	ed.tx, ed.ty, ed.tw, ed.th = ax+gutter, ay, max(1, aw-gutter-1), ah
	if d.follow {
		d.scrollToCaret(ed.tw, ed.th)
		d.follow = false
	}
	d.top = clamp(d.top, 0, max(0, len(d.lines)-ah))
	text := Style{FG: t.TextFG, BG: t.TextBG}
	sel := Style{FG: t.SelFG, BG: t.SelBG}
	c.Fill(ax, ay, aw-1, ah, text)
	r1, c1, r2, c2, hasSel := d.selRange()
	for i := range ah {
		row, sy := d.top+i, ay+i
		if row >= len(d.lines) {
			break
		}
		if gutter > 0 {
			num := strconv.Itoa(row + 1)
			c.Fill(ax, sy, gutter, 1, Style{FG: t.Disabled, BG: t.Face})
			c.Text(ax+gutter-1-len(num), sy, len(num), num, Style{FG: t.Disabled, BG: t.Face})
		}
		line := d.lines[row]
		cx := 0
		for j := d.left; j < len(line) && cx < ed.tw; j++ {
			st := text
			if hasSel && inSel(row, j, r1, c1, r2, c2) {
				st = sel
			}
			n := c.Rune(ed.tx+cx, sy, ed.tw-cx, line[j], st)
			if n == 0 {
				break
			}
			cx += n
		}
		if hasSel && row >= r1 && row < r2 && cx < ed.tw { // the line break
			c.Set(ed.tx+cx, sy, ' ', sel)
		}
		if row == d.row {
			if cc := runesWidth(line[min(d.left, d.col):d.col]); d.col >= d.left && cc < ed.tw {
				c.Restyle(ed.tx+cc, sy, t.TextBG, t.TextFG)
			}
		}
	}
	a.addRegion(Region{X: ax, Y: ay, W: aw - 1, H: ah, Kind: HitDlg, S: "text"})
	a.drawScrollbar(c, ax+aw-1, ay, ah, len(d.lines), ah, d.top, HitDlg)

	// The status line: the file, and line:column/lines (size), like mIRC's.
	path := d.file
	if !filepath.IsAbs(path) {
		path = filepath.Join("scripts", path)
	}
	size := len(d.data())
	pos := fmt.Sprintf("%d:%d/%d (%.1fk)", d.row+1, d.col+1, len(d.lines), float64(size)/1024)
	c.Fill(x+1, y+h-4, w-2, 1, face)
	c.Text(x+2, y+h-4, w-6-len(pos), "File: "+path, face)
	c.Text(x+w-2-len(pos), y+h-4, len(pos), pos, face)
	a.dlgButtons(c, x, y+h-3, w, edButtons, 0)
}

func runesWidth(rs []rune) int {
	n := 0
	for _, r := range rs {
		n += runewidth.RuneWidth(r)
	}
	return n
}

func inSel(row, col, r1, c1, r2, c2 int) bool {
	return (row > r1 || row == r1 && col >= c1) && (row < r2 || row == r2 && col < c2)
}

// scrollToCaret scrolls so the caret is in a text area w by h.
func (d *scriptDoc) scrollToCaret(w, h int) {
	d.top = clamp(d.top, d.row-h+1, d.row)
	line := d.lines[d.row]
	if d.col < d.left {
		d.left = d.col
	}
	for d.left < d.col && runesWidth(line[d.left:d.col]) >= w {
		d.left++
	}
}

// posAt is the line and column at screen cell (x, y), for the mouse.
func (ed *scriptEditor) posAt(d *scriptDoc, x, y int) (int, int) {
	row := clamp(d.top+y-ed.ty, 0, len(d.lines)-1)
	line := d.lines[row]
	col, cx := min(d.left, len(line)), 0
	for x >= ed.tx && col < len(line) {
		w := runewidth.RuneWidth(line[col])
		if cx+w > x-ed.tx {
			break
		}
		cx += w
		col++
	}
	if x < ed.tx {
		col = 0
	}
	return row, col
}

// selRange is the selection, from (r1, c1) up to (r2, c2).
func (d *scriptDoc) selRange() (r1, c1, r2, c2 int, ok bool) {
	if !d.sel || d.row == d.arow && d.col == d.acol {
		return 0, 0, 0, 0, false
	}
	r1, c1, r2, c2 = d.arow, d.acol, d.row, d.col
	if r2 < r1 || r2 == r1 && c2 < c1 {
		r1, c1, r2, c2 = r2, c2, r1, c1
	}
	return r1, c1, r2, c2, true
}

func (d *scriptDoc) selText() string {
	r1, c1, r2, c2, ok := d.selRange()
	if !ok {
		return ""
	}
	if r1 == r2 {
		return string(d.lines[r1][c1:c2])
	}
	parts := []string{string(d.lines[r1][c1:])}
	for r := r1 + 1; r < r2; r++ {
		parts = append(parts, string(d.lines[r]))
	}
	return strings.Join(append(parts, string(d.lines[r2][:c2])), "\n")
}

// moveTo puts the caret at (row, col), extending the selection or not.
func (d *scriptDoc) moveTo(row, col int, extend bool) {
	if extend && !d.sel {
		d.sel, d.arow, d.acol = true, d.row, d.col
	}
	if !extend {
		d.sel = false
	}
	d.row = clamp(row, 0, len(d.lines)-1)
	d.col = clamp(col, 0, len(d.lines[d.row]))
	d.follow, d.lastOp = true, ""
}

func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' }

// move is a caret key: k without its shift.
func (d *scriptDoc) move(k string, page int, extend bool) {
	row, col := d.row, d.col
	line := d.lines[row]
	switch k {
	case "left":
		if col > 0 {
			col--
		} else if row > 0 {
			row--
			col = len(d.lines[row])
		}
	case "right":
		if col < len(line) {
			col++
		} else if row < len(d.lines)-1 {
			row, col = row+1, 0
		}
	case "up":
		row--
	case "down":
		row++
	case "pgup":
		row -= max(1, page-1)
		d.top -= max(1, page-1)
	case "pgdown":
		row += max(1, page-1)
		d.top += max(1, page-1)
	case "home":
		col = 0
	case "end":
		col = len(line)
	case "ctrl+home":
		row, col = 0, 0
	case "ctrl+end":
		row = len(d.lines) - 1
		col = len(d.lines[row])
	case "ctrl+left":
		for col > 0 && !isWordRune(line[col-1]) {
			col--
		}
		for col > 0 && isWordRune(line[col-1]) {
			col--
		}
	case "ctrl+right":
		for col < len(line) && isWordRune(line[col]) {
			col++
		}
		for col < len(line) && !isWordRune(line[col]) {
			col++
		}
	}
	d.moveTo(row, col, extend)
}

func (d *scriptDoc) selectAll() {
	d.sel, d.arow, d.acol = true, 0, 0
	d.row = len(d.lines) - 1
	d.col = len(d.lines[d.row])
	d.follow = true
}

func (d *scriptDoc) selectWord() {
	line := d.lines[d.row]
	from, to := d.col, d.col
	for from > 0 && isWordRune(line[from-1]) {
		from--
	}
	for to < len(line) && isWordRune(line[to]) {
		to++
	}
	d.sel, d.arow, d.acol, d.col = true, d.row, from, to
}

// edit changes d's text with f, which reports whether it did: one undo
// step, except that a run of typing (op "type") is one.
func (d *scriptDoc) edit(op string, f func() bool) {
	n := len(d.undo)
	if op == "" || op != d.lastOp {
		d.undo = append(d.undo, docSnap{lines: slices.Clone(d.lines), row: d.row, col: d.col})
		if len(d.undo) > 200 {
			d.undo = slices.Delete(d.undo, 0, 1)
			n--
		}
	}
	if !f() {
		d.undo = d.undo[:max(0, n)]
		return
	}
	d.redo = nil
	d.lastOp, d.dirty, d.follow = op, true, true
}

// undoOne is Edit > Undo with redo false, Redo with true.
func (d *scriptDoc) undoOne(redo bool) {
	from, to := &d.undo, &d.redo
	if redo {
		from, to = to, from
	}
	if len(*from) == 0 {
		return
	}
	s := (*from)[len(*from)-1]
	*from = (*from)[:len(*from)-1]
	*to = append(*to, docSnap{lines: d.lines, row: d.row, col: d.col})
	d.lines, d.row, d.col, d.sel = s.lines, s.row, s.col, false
	d.lastOp, d.dirty, d.follow = "", true, true
}

// The edits keep lines unshared: undo keeps the old slice of lines, so a
// changed line is always a new slice.

func (d *scriptDoc) deleteSel() bool {
	r1, c1, r2, c2, ok := d.selRange()
	d.sel = false
	if !ok {
		return false
	}
	joined := slices.Concat(d.lines[r1][:c1], d.lines[r2][c2:])
	d.lines = slices.Replace(slices.Clone(d.lines), r1, r2+1, joined)
	d.row, d.col = r1, c1
	return true
}

func (d *scriptDoc) insert(rs []rune) bool {
	d.deleteSel()
	s := strings.NewReplacer("\r\n", "\n", "\r", "\n", "\t", "  ").Replace(string(rs))
	parts := strings.Split(s, "\n")
	line := d.lines[d.row]
	head, tail := line[:d.col], line[d.col:]
	repl := make([][]rune, len(parts))
	for i, p := range parts {
		repl[i] = []rune(p)
	}
	last := len(repl) - 1
	d.col = len(repl[last])
	if last == 0 {
		d.col += len(head)
	}
	repl[0] = slices.Concat(head, repl[0])
	repl[last] = slices.Concat(repl[last], tail)
	d.lines = slices.Replace(slices.Clone(d.lines), d.row, d.row+1, repl...)
	d.row += last
	return true
}

// newline is Enter: a new line, indented like the one it's from.
func (d *scriptDoc) newline() bool {
	line := d.lines[d.row]
	n := 0
	for n < len(line) && n < d.col && line[n] == ' ' {
		n++
	}
	return d.insert(append([]rune{'\n'}, line[:n]...))
}

func (d *scriptDoc) backspace() bool {
	if d.deleteSel() {
		return true
	}
	switch {
	case d.col > 0:
		d.lines = slices.Clone(d.lines)
		d.lines[d.row] = slices.Delete(slices.Clone(d.lines[d.row]), d.col-1, d.col)
		d.col--
	case d.row > 0:
		d.col = len(d.lines[d.row-1])
		d.lines = slices.Replace(slices.Clone(d.lines), d.row-1, d.row+1, slices.Concat(d.lines[d.row-1], d.lines[d.row]))
		d.row--
	default:
		return false
	}
	return true
}

func (d *scriptDoc) del() bool {
	if d.deleteSel() {
		return true
	}
	switch {
	case d.col < len(d.lines[d.row]):
		d.lines = slices.Clone(d.lines)
		d.lines[d.row] = slices.Delete(slices.Clone(d.lines[d.row]), d.col, d.col+1)
	case d.row < len(d.lines)-1:
		d.lines = slices.Replace(slices.Clone(d.lines), d.row, d.row+2, slices.Concat(d.lines[d.row], d.lines[d.row+1]))
	default:
		return false
	}
	return true
}

// typed is typing and pasting, with line breaks.
func (ed *scriptEditor) typed(a *App, rs []rune) tea.Cmd {
	d := ed.doc()
	op := "type"
	if len(rs) > 1 {
		op = "" // a paste is undone on its own
	}
	d.edit(op, func() bool {
		// A } typed where Enter indented the line goes back a level.
		if string(rs) == "}" && !d.sel && d.col >= 2 && strings.TrimLeft(string(d.lines[d.row][:d.col]), " ") == "" {
			d.lines = slices.Clone(d.lines)
			d.lines[d.row] = slices.Delete(slices.Clone(d.lines[d.row]), d.col-2, d.col)
			d.col -= 2
		}
		return d.insert(rs)
	})
	return nil
}

func (ed *scriptEditor) key(a *App, k string) tea.Cmd {
	d := ed.doc()
	if rest, ok := strings.CutPrefix(k, "alt+"); ok && len(rest) == 1 {
		if i := strings.IndexByte(edMnemonics, rest[0]); i >= 0 {
			a.openStripMenu(ed, i)
			a.menus[0].Sel = 0
		}
		return nil
	}
	switch mv := strings.Replace(strings.TrimPrefix(k, "shift+"), "ctrl+shift+", "ctrl+", 1); mv {
	case "left", "right", "up", "down", "home", "end", "pgup", "pgdown",
		"ctrl+home", "ctrl+end", "ctrl+left", "ctrl+right":
		d.move(mv, ed.th, mv != k)
		return nil
	}
	switch k {
	case "esc":
		return ed.cancel(a)
	case "enter":
		d.edit("", d.newline)
	case "tab":
		d.edit("type", func() bool { return d.insert([]rune("  ")) })
	case "backspace":
		d.edit("backspace", d.backspace)
	case "delete":
		d.edit("delete", d.del)
	case "ctrl+z":
		d.undoOne(false)
	case "ctrl+y":
		d.undoOne(true)
	case "ctrl+l":
		ed.loadPrompt(a)
	case "ctrl+h":
		ed.checkBrackets(a)
	case "ctrl+x":
		if s := d.selText(); s != "" {
			d.edit("", d.deleteSel)
			return copyText(s)
		}
	case "ctrl+c":
		if s := d.selText(); s != "" {
			return copyText(s)
		}
	case "ctrl+v":
		return pasteClipboard()
	case "ctrl+a":
		d.selectAll()
	case "ctrl+f":
		ed.findPrompt(a)
	case "f3":
		ed.findNext(a)
	case "ctrl+g":
		ed.gotoPrompt(a)
	case "ctrl+s":
		return ed.apply(a, d)
	case "f1":
		return ed.press(a, "Help")
	case "ctrl+pgdown":
		ed.setTab(a, (ed.tab+1)%edTab(len(edTabNames)))
	case "ctrl+pgup":
		ed.setTab(a, (ed.tab+edTab(len(edTabNames))-1)%edTab(len(edTabNames)))
	}
	return nil
}

func (ed *scriptEditor) mouse(a *App, m tea.MouseMsg, r Region, double bool) tea.Cmd {
	d := ed.doc()
	switch {
	case m.Action == tea.MouseActionRelease:
		ed.drag = false
	case m.Action == tea.MouseActionMotion:
		if ed.drag {
			row, col := ed.posAt(d, m.X, m.Y)
			d.moveTo(row, col, true)
		}
	case m.Button == tea.MouseButtonWheelUp:
		d.top -= 3
	case m.Button == tea.MouseButtonWheelDown:
		d.top += 3
	case m.Action != tea.MouseActionPress || m.Button != tea.MouseButtonLeft:
	case r.S == "text":
		row, col := ed.posAt(d, m.X, m.Y)
		d.moveTo(row, col, m.Shift)
		if double {
			d.selectWord()
		} else {
			ed.drag = true
		}
	case r.S == "strip":
		a.openStripMenu(ed, r.I)
	case r.S == "tab":
		ed.setTab(a, edTab(r.I))
	case r.S == "check":
		ed.check(a)
	case r.S == "button":
		return ed.press(a, edButtons[r.I])
	case r.S == "close":
		return ed.cancel(a)
	case r.Kind == HitDlg: // the scrollbar's arrows and track
		d.top += scrollStep(r.I, ed.th)
	}
	return nil
}

// scrollTop is dragging the scrollbar's thumb.
func (ed *scriptEditor) scrollTop(top int) { ed.doc().top = top }

func (ed *scriptEditor) press(a *App, button string) tea.Cmd {
	switch button {
	case "OK":
		return ed.ok(a)
	case "Cancel":
		return ed.cancel(a)
	case "Help":
		ed.helpTopic(a, [...]string{"Aliases", "Popups", "Remote", "Users", "Variables"}[ed.tab])
	}
	return nil
}

// back reopens the editor after one of its prompts.
func (ed *scriptEditor) back(a *App) tea.Cmd {
	a.dlg = ed
	return nil
}

func (ed *scriptEditor) changed() bool {
	return len(ed.unloads) > 0 || slices.ContainsFunc(ed.docs, func(d *scriptDoc) bool { return d.dirty || d.load })
}

// cancel closes the editor without saving: Cancel, Esc and the close
// box. With Options > Ask to Save Files (on, like mIRC's) it asks first
// when something changed.
func (ed *scriptEditor) cancel(a *App) tea.Cmd {
	if a.book().ScriptOpts.NoAskSave || !ed.changed() {
		a.dlg = nil
		return nil
	}
	m := message("Save the changes to your scripts?")
	m.Title = "Scripts Editor"
	m.Buttons, m.Cancel = []string{"Yes", "No", "Cancel"}, 2
	m.OnClose = func(a *App, button int) tea.Cmd {
		switch button {
		case 0:
			a.dlg = ed
			return ed.ok(a)
		case 2:
			return ed.back(a)
		}
		return nil
	}
	a.openDialog(m)
	return nil
}

// ok saves and loads what changed, and closes the editor.
func (ed *scriptEditor) ok(a *App) tea.Cmd {
	if ed.invalid(a) {
		return nil
	}
	a.dlg = nil
	return ed.applyAll(a)
}

func (ed *scriptEditor) applyAll(a *App) tea.Cmd {
	var cmds []tea.Cmd
	for _, e := range ed.unloads {
		cmds = append(cmds, a.unloadScript(e))
	}
	ed.unloads = nil
	for _, d := range ed.docs {
		if d.dirty || d.load {
			cmds = append(cmds, ed.apply(a, d))
		}
	}
	return tea.Batch(cmds...)
}

// invalid shows the Users or Variables lines that aren't right, which
// would be lost: OK waits until they are.
func (ed *scriptEditor) invalid(a *App) bool {
	for _, d := range ed.docs {
		if (d.tab == edUsers || d.tab == edVars) && d.dirty {
			if ps := docProblems(a, d); len(ps) > 0 {
				ed.setTab(a, d.tab)
				ed.showProblems(a, d, ps)
				return true
			}
		}
	}
	return false
}

// apply saves d and loads it again, like mIRC's File > Save: one that
// isn't loaded yet is loaded, and asks about its initialization commands
// once the editor's closed.
func (ed *scriptEditor) apply(a *App, d *scriptDoc) tea.Cmd {
	switch d.tab {
	case edUsers, edVars:
		if ps := docProblems(a, d); len(ps) > 0 {
			ed.showProblems(a, d, ps)
			return nil
		}
		d.dirty = false
		if d.tab == edUsers {
			a.scripts.RestoreUsers(d.saved(), a.book().ScriptLevel)
			a.saveScriptState(&script.Save{Users: a.scripts.Users()})
			return nil
		}
		vars := map[string]string{}
		for _, l := range d.saved() {
			if name, value, _ := strings.Cut(strings.TrimPrefix(strings.TrimSpace(l), "%"), " "); name != "" {
				vars[name] = value
			}
		}
		a.scripts.SetVars(vars)
		a.saveScriptState(&script.Save{Vars: vars})
		return nil
	}
	data := d.data()
	if d.tab == edPopups && d.def {
		data = ed.popupsIni()
	}
	if old, err := a.readScript(d.file); err == nil && a.book().ScriptOpts.Backup && old != data {
		a.writeScript(d.file+".bak", old) // Options > Backup File on Save
	}
	if err := a.writeScript(d.file, data); err != nil {
		m := message("Couldn't save "+d.file+":", err.Error())
		m.OnClose = func(a *App, _ int) tea.Cmd { return ed.back(a) }
		a.openDialog(m)
		return nil
	}
	for _, o := range ed.docs {
		if o.file == d.file && o.tab == d.tab {
			o.dirty, o.disk = false, data // all of popups.ini
		}
	}
	var cmds []tea.Cmd
	if d.was != "" && !strings.EqualFold(d.was, d.file) && a.scriptLoaded(d.was) {
		cmds = append(cmds, a.unloadScript(d.was))
	}
	d.was = ""
	hasText := strings.TrimSpace(d.text()) != ""
	if d.tab == edPopups && d.def {
		hasText = data != "" // any of its menus
	}
	switch {
	case a.scriptLoaded(d.file):
		ps := a.scripts.LoadAs(d.file, data, d.as())
		a.scriptProblems[strings.ToLower(d.file)] = ps
		a.reportProblems(d.file, ps)
	case d.load || hasText:
		cmds = append(cmds, a.loadScript(d.entry(), true))
	}
	d.load = false
	return tea.Batch(cmds...)
}

// popupsIni is popups.ini, from the Popups tab's five menus.
func (ed *scriptEditor) popupsIni() string {
	var b strings.Builder
	for _, k := range popupKinds {
		for _, d := range ed.docs {
			if d.tab == edPopups && d.def && d.kind == k.kind {
				if ls := d.saved(); len(ls) > 0 {
					b.WriteString(writeIniList(d.ini, ls))
				}
			}
		}
	}
	return b.String()
}

// writeScript writes a script's file: in the scripts folder, or by its
// full path.
func (a *App) writeScript(name, data string) error {
	if filepath.IsAbs(name) {
		return os.WriteFile(name, []byte(data), 0o600)
	}
	if _, ok := a.scriptFile(name); !ok {
		return errors.New(name + " isn't in the scripts folder")
	}
	return a.scriptWriteFile(&script.File{Name: name, Data: data})
}

func (a *App) scriptLoaded(file string) bool {
	return slices.ContainsFunc(a.book().Scripts, func(s string) bool { _, f := splitLoad(s); return strings.EqualFold(f, file) })
}

// reportProblems shows a reloaded script's problems.
func (a *App) reportProblems(name string, ps []script.Problem) {
	if len(ps) == 0 {
		return
	}
	if slices.ContainsFunc(ps, func(p script.Problem) bool { return p.Fatal }) {
		a.addLine(a.active, state.RoleQuit, "* "+name+" wasn't loaded again:")
	} else {
		a.addLine(a.active, state.RoleInfo, "* "+name+":")
	}
	for _, p := range ps {
		a.addLine(a.active, state.RoleInfo, "*   "+p.String())
	}
}

// docProblems is what's wrong with d's text: what trout can't run in a
// script, or Users and Variables lines it can't read.
func docProblems(a *App, d *scriptDoc) []string {
	var out []string
	switch d.tab {
	case edUsers:
		for i, l := range d.lines {
			if s := strings.TrimSpace(string(l)); s != "" {
				e := script.New(script.Options{})
				if e.RestoreUsers([]string{s}, 1); len(e.Users()) == 0 {
					out = append(out, "line "+strconv.Itoa(i+1)+": not levels:address, like 5,friend:*!*@host")
				}
			}
		}
	case edVars:
		for i, l := range d.lines {
			if s := strings.TrimSpace(string(l)); s != "" && (!strings.HasPrefix(s, "%") || len(s) == 1 || s[1] == ' ') {
				out = append(out, "line "+strconv.Itoa(i+1)+": not %name value")
			}
		}
	default:
		as := d.as()
		if as == "popups" {
			as = d.kind // checked as plain lines, so the line numbers are the editor's
		}
		for _, p := range a.scripts.Check(d.file, d.text(), as) {
			out = append(out, p.String())
		}
	}
	return out
}

func (ed *scriptEditor) showProblems(a *App, d *scriptDoc, ps []string) {
	lines := append([]string{d.label() + ":", ""}, ps...)
	if len(ps) == 0 {
		lines = []string{d.label() + ": no problems found."}
	}
	if most := max(3, a.h-8); len(lines) > most {
		lines = append(lines[:most-1], "... and "+strconv.Itoa(len(lines)-most+1)+" more")
	}
	m := message(lines...)
	m.Title = "Check"
	m.OnClose = func(a *App, _ int) tea.Cmd { return ed.back(a) }
	a.openDialog(m)
}

// check is the ✓ button: brackets that don't match (the caret goes to
// them), then what else trout finds wrong.
func (ed *scriptEditor) check(a *App) {
	d := ed.doc()
	ps := docProblems(a, d)
	if n := d.brackets(); n >= 0 && d.tab != edUsers && d.tab != edVars {
		d.selectLine(n)
		ps = append([]string{"line " + strconv.Itoa(n+1) + ": brackets don't match"}, ps...)
	}
	ed.showProblems(a, d, ps)
}

// checkBrackets is Edit > Check Brackets: the caret goes to a { or } that
// doesn't match.
func (ed *scriptEditor) checkBrackets(a *App) {
	d := ed.doc()
	text := "The brackets match."
	if n := d.brackets(); n >= 0 {
		d.selectLine(n)
		text = "The brackets don't match: line " + strconv.Itoa(n+1) + "."
	}
	m := message(text)
	m.Title = "Check Brackets"
	m.OnClose = func(a *App, _ int) tea.Cmd { return ed.back(a) }
	a.openDialog(m)
}

// brackets is the line of a { that's never closed or a } that closes
// nothing, or -1.
func (d *scriptDoc) brackets() int {
	var open []int
	for i, l := range d.lines {
		for _, r := range l {
			switch r {
			case '{':
				open = append(open, i)
			case '}':
				if len(open) == 0 {
					return i
				}
				open = open[:len(open)-1]
			}
		}
	}
	if len(open) > 0 {
		return open[len(open)-1]
	}
	return -1
}

func (d *scriptDoc) selectLine(n int) {
	d.moveTo(n, 0, false)
	d.moveTo(n, len(d.lines[n]), true)
}

// The Help menu's topics, about scripting in trout. Commands and
// Identifiers list what the engine has; Search says what it does with a
// name.
var edTopics = map[string][]string{
	"Aliases": {"Aliases: one a line on the Aliases tab, /name and its commands,",
		"like /j /join #$$1; $1, $2- are what you typed after it.",
		"Use { } for more lines. In a script on the Remote tab,",
		"write alias name { ... } instead. They're kept in",
		"aliases.ini and work once you press OK."},
	"Popups": {"Popups: View picks the menu: Status, Channel, Query,",
		"Nickname List or Menu Bar. Each line is an item,",
		"Label:commands. A dot in front puts it in the submenu",
		"above (two dots, the one above that); a - on its own",
		"is a separator. In a script: menu nicklist { ... }."},
	"Remote": {"Remote: scripts, with events (on *:JOIN:#:{ ... }),",
		"ctcp and raw events, aliases (alias name { ... }) and",
		"menus. File > Load adds one from the scripts folder;",
		"✓ shows what trout can't run. Listen turns ctcp, named",
		"and raw events on and off; /remote off stops them all."},
	"Users": {"Users: each line is levels:address, like",
		"5,friend:*!*@host.example. An event with a level, like",
		"on 5:JOIN, only triggers for the people who have it;",
		"others get the default level (Options > Default Level)."},
	"Variables": {"Variables: each line is %name value. /set %x 1, /inc,",
		"/dec, /unset, or %x = 1. /set -uN unsets it after N",
		"seconds, -e when trout quits. /var %x = 1 is local to",
		"the alias or event. Scripts keep the others in vars.ini."},
	"If-Then-Else": {"if (v1 operator v2) { ... } elseif (...) { ... } else { ... }",
		"and while (...) { ... }, with break and continue.",
		"Operators: == === != < > <= >= // \\\\ & isin isincs iswm",
		"iswmcs isnum isletter isalnum isalpha islower isupper ison",
		"isop ishop isvoice isreg ischan isban isinvite isexcept",
		"isaop isavoice isignore isprotect isnotify.",
		"! in front negates; && and || join conditions."},
}

// helpTopic opens one of the Help menu's topics.
func (ed *scriptEditor) helpTopic(a *App, topic string) {
	switch topic {
	case "Contents":
		names := []string{"Aliases", "Commands", "Identifiers", "If-Then-Else", "Popups", "Remote", "Users", "Variables"}
		l := &listBox{Title: "Scripts Help", Header: "Topics", Rows: names, Keys: names, Buttons: []string{"Open", "Close"}}
		l.OnClose = func(a *App) { a.dlg = ed }
		l.OnPress = func(a *App, l *listBox, _, key string) tea.Cmd {
			ed.helpTopic(a, key)
			return nil
		}
		a.openDialog(l)
	case "Search...":
		ed.prompt(a, "Search", "Name:", "", []string{"A command, or an identifier with its $."}, func(v string) error {
			if v == "" {
				return errors.New("give a name")
			}
			m := message(script.Lookup(v))
			m.Title = "Search"
			m.OnClose = func(a *App, _ int) tea.Cmd { return ed.back(a) }
			a.openDialog(m)
			return nil
		})
	case "Commands", "Identifiers":
		rows, header := script.Commands(), "Scripting commands; trout's own work too, as typed"
		if topic == "Identifiers" {
			rows, header = nil, "Identifiers"
			for _, id := range script.Identifiers() {
				rows = append(rows, "$"+id)
			}
		}
		l := &listBox{Title: topic, Header: header, Rows: rows, Keys: rows, Buttons: []string{"Close"}}
		l.OnClose = func(a *App) { a.dlg = ed }
		l.OnPress = func(a *App, _ *listBox, _, _ string) tea.Cmd { return ed.back(a) }
		a.openDialog(l)
	default:
		m := message(edTopics[topic]...)
		m.Title = topic
		m.OnClose = func(a *App, _ int) tea.Cmd { return ed.back(a) }
		a.openDialog(m)
	}
}

// The editor's menus.

func (ed *scriptEditor) stripLen() int { return len(edMenuNames) }

func (ed *scriptEditor) stripMenu(a *App, i int) ([]MenuItem, int, int) {
	d := ed.doc()
	files := d.tab == edAliases || d.tab == edRemote
	scripts := d.tab != edUsers && d.tab != edVars
	sep := MenuItem{Sep: true}
	do := func(f func(a *App)) func(*App) tea.Cmd { return func(a *App) tea.Cmd { f(a); return nil } }
	key := func(k string) func(*App) tea.Cmd { return func(a *App) tea.Cmd { return ed.key(a, k) } }
	o := &a.book().ScriptOpts
	var items []MenuItem
	switch edMenuNames[i] {
	case "File":
		// mIRC's Font... is left out: the terminal has the font.
		items = []MenuItem{
			{Label: "New", Disabled: !files, Action: do(ed.newDoc)},
			{Label: "Load...", Key: "Ctrl+L", Action: do(ed.loadPrompt)},
			{Label: "Unload", Disabled: d.def, Action: do(func(a *App) { ed.unload(a, d) })},
			sep,
			{Label: "Save", Key: "Ctrl+S", Action: func(a *App) tea.Cmd { return ed.apply(a, d) }},
			{Label: "Save As...", Disabled: d.def, Action: do(ed.saveAsPrompt)},
			{Label: "Save All", Action: func(a *App) tea.Cmd {
				if ed.invalid(a) {
					return nil
				}
				return ed.applyAll(a)
			}},
			sep,
			{Label: "Rename...", Disabled: d.def, Action: do(ed.renamePrompt)},
			{Label: "Delete...", Disabled: d.def, Action: do(ed.deletePrompt)},
			sep,
			{Label: "Order...", Disabled: !scripts, Action: do(ed.openOrder)},
			sep,
			{Label: "Save & Exit", Action: ed.ok},
			{Label: "Cancel", Action: ed.cancel},
		}
	case "Edit":
		// mIRC's More Files has Ctrl+M, which a terminal sends as Enter.
		noSel := d.selText() == ""
		items = []MenuItem{
			{Label: "Undo", Key: "Ctrl+Z", Disabled: len(d.undo) == 0, Action: key("ctrl+z")},
			{Label: "Redo", Key: "Ctrl+Y", Disabled: len(d.redo) == 0, Action: key("ctrl+y")},
			sep,
			{Label: "Cut", Key: "Ctrl+X", Disabled: noSel, Action: key("ctrl+x")},
			{Label: "Copy", Key: "Ctrl+C", Disabled: noSel, Action: key("ctrl+c")},
			{Label: "Paste", Key: "Ctrl+V", Action: key("ctrl+v")},
			{Label: "Delete", Disabled: noSel, Action: key("delete")},
			{Label: "Select All", Key: "Ctrl+A", Action: key("ctrl+a")},
			sep,
			{Label: "Check Brackets", Key: "Ctrl+H", Action: key("ctrl+h")},
			sep,
			{Label: "Go To Line...", Key: "Ctrl+G", Action: key("ctrl+g")},
			{Label: "More Files...", Action: do(ed.moreFiles)},
			sep,
			{Label: "Find...", Key: "Ctrl+F", Action: key("ctrl+f")},
			{Label: "Find Next", Key: "F3", Action: key("f3")},
		}
	case "View":
		items = ed.viewMenu()
	case "Index":
		items = ed.index(a, d)
	case "Listen":
		toggle := func(name string, off bool) MenuItem {
			return MenuItem{Label: name, Check: !off, Action: func(a *App) tea.Cmd {
				cmd, _ := a.scriptAdmin(strings.ToLower(name)+map[bool]string{true: " on", false: " off"}[off], false)
				return cmd
			}}
		}
		items = []MenuItem{toggle("CTCPs", o.CTCPsOff), toggle("Events", o.EventsOff), toggle("Raw", o.RawOff)}
	case "Options":
		// mIRC's Show on Desktop and Always on Top are left out: they're
		// about windows on the desktop, which a terminal doesn't have.
		opt := func(label string, p *bool, on bool) MenuItem {
			return MenuItem{Label: label, Check: *p == on, Action: func(a *App) tea.Cmd {
				*p = !*p
				a.saveConfig()
				a.applyScriptOpts()
				return nil
			}}
		}
		items = []MenuItem{
			opt("Initialization Warning", &o.NoInitWarning, false),
			opt("Ask to Save Files", &o.NoAskSave, false),
			opt("Monitor File Changes", &o.NoMonitor, false),
			opt("Backup File on Save", &o.Backup, true),
			opt("Find Selection", &o.NoFindSel, false),
			opt("Line Numbers", &o.NoLineNumbers, false),
			sep,
			opt("Identifier Warning", &o.IdentWarning, true),
			sep,
			{Label: "Default Level: " + strconv.Itoa(max(1, a.book().ScriptLevel)), Action: do(ed.levelPrompt)},
		}
	case "Help":
		topic := func(name string) MenuItem {
			return MenuItem{Label: name, Action: do(func(a *App) { ed.helpTopic(a, name) })}
		}
		items = []MenuItem{topic("Contents"), topic("Search..."), sep, topic("Aliases"), topic("Commands"),
			topic("Identifiers"), topic("If-Then-Else"), topic("Popups"), topic("Remote"), topic("Variables")}
	}
	x, y := ed.barPos(a, i)
	return items, x, y + 1
}

// viewMenu is the View menu: the tab's files, numbered, like mIRC's. On
// Users and Variables, which are part of the remote, it's the Remote
// tab's.
func (ed *scriptEditor) viewMenu() []MenuItem {
	tab := ed.tab
	if tab == edUsers || tab == edVars {
		tab = edRemote
	}
	var items []MenuItem
	for _, o := range ed.docs {
		if o.tab != tab {
			continue
		}
		label := o.label()
		if n := len(items) + 1; n <= 9 {
			label = strconv.Itoa(n) + " " + label
		}
		items = append(items, MenuItem{Label: label, Check: o == ed.cur[ed.tab],
			Action: func(a *App) tea.Cmd {
				ed.setTab(a, tab)
				ed.cur[tab] = o
				return nil
			}})
	}
	return items
}

// moreFiles is Edit > More Files...: the tab's files, to pick one.
func (ed *scriptEditor) moreFiles(a *App) {
	var rows []string
	tab := ed.tab
	for _, o := range ed.docs {
		if o.tab == tab {
			rows = append(rows, o.label())
		}
	}
	l := &listBox{Title: "More Files", Header: edTabNames[tab], Rows: rows, Keys: rows, Buttons: []string{"Open", "Cancel"}}
	l.OnClose = func(a *App) { a.dlg = ed }
	l.OnPress = func(a *App, _ *listBox, _, key string) tea.Cmd {
		for _, o := range ed.docs {
			if o.tab == tab && o.label() == key {
				ed.cur[tab] = o
			}
		}
		return ed.back(a)
	}
	a.openDialog(l)
}

// tabOf is the tab a config entry's file is on.
func tabOf(entry string) edTab {
	switch as, _ := splitLoad(entry); as {
	case "":
		return edRemote
	case "aliases":
		return edAliases
	}
	return edPopups
}

// openOrder is File > Order...: the order the tab's files load in, and
// so the order their events run in.
func (ed *scriptEditor) openOrder(a *App) {
	tab := ed.tab
	b := a.book()
	l := &listBox{Title: "Order", Header: edTabNames[tab] + " files, in the order they load",
		Buttons: []string{"Move Up", "Move Down", "Close"}}
	l.OnClose = func(a *App) { a.dlg = ed }
	var at []int // where the tab's entries are in b.Scripts
	fill := func() {
		l.Rows, l.Keys, at = nil, nil, nil
		for i, e := range b.Scripts {
			if tabOf(e) == tab {
				_, f := splitLoad(e)
				l.Rows = append(l.Rows, f)
				l.Keys = append(l.Keys, strconv.Itoa(len(at)))
				at = append(at, i)
			}
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
		if j < 0 || j >= len(at) {
			return nil
		}
		b.Scripts[at[i]], b.Scripts[at[j]] = b.Scripts[at[j]], b.Scripts[at[i]]
		a.saveConfig()
		var names []string
		for _, e := range b.Scripts {
			_, f := splitLoad(e)
			names = append(names, f)
		}
		a.scripts.Order(names)
		fill()
		l.Sel = j
		return nil
	}
	a.openDialog(l)
}

// renamePrompt is File > Rename...: the file, and the script loaded from
// it, get a new name now.
func (ed *scriptEditor) renamePrompt(a *App) {
	d := ed.doc()
	ed.prompt(a, "Rename", "File:", d.file, nil, func(file string) error {
		file = strings.Trim(file, `"`)
		if _, ok := a.scriptFile(file); !ok && !filepath.IsAbs(file) {
			return errors.New("a name in the scripts folder, or a full path")
		}
		if strings.EqualFold(file, d.file) {
			return nil
		}
		if _, err := a.readScript(file); err == nil {
			return errors.New(file + " is there already")
		}
		old := d.file
		if data, err := a.readScript(old); err == nil {
			if err := a.writeScript(file, data); err != nil {
				return err
			}
			a.removeScript(old)
		}
		b := a.book()
		for i, e := range b.Scripts {
			if _, f := splitLoad(e); strings.EqualFold(f, old) {
				b.Scripts[i] = strings.TrimSuffix(e, f) + file
				a.saveConfig()
				a.scripts.Rename(f, file)
				a.scriptProblems[strings.ToLower(file)] = a.scriptProblems[strings.ToLower(f)]
				delete(a.scriptProblems, strings.ToLower(f))
			}
		}
		d.file = file
		if strings.EqualFold(d.was, old) {
			d.was = file
		}
		return nil
	})
}

// deletePrompt is File > Delete...: the script is unloaded and its file
// deleted, now.
func (ed *scriptEditor) deletePrompt(a *App) {
	d := ed.doc()
	m := message("Delete "+d.file+"?", "", "It's unloaded, and the file is deleted.")
	m.Title = "Delete"
	m.Buttons, m.Cancel = []string{"Yes", "No"}, 1
	m.OnClose = func(a *App, button int) tea.Cmd {
		a.dlg = ed
		if button != 0 {
			return nil
		}
		var cmd tea.Cmd
		if name := cmp.Or(d.was, d.file); a.scriptLoaded(name) {
			cmd = a.unloadScript(name)
		}
		a.removeScript(d.file)
		d.load, d.dirty, d.was = true, false, "" // nothing left to unload on OK
		ed.unload(a, d)
		return cmd
	}
	a.openDialog(m)
}

// removeScript deletes a script's file.
func (a *App) removeScript(name string) {
	if filepath.IsAbs(name) {
		os.Remove(name)
		return
	}
	if _, ok := a.scriptFile(name); ok {
		a.scriptWriteFile(&script.File{Name: name, Delete: true})
	}
}

// levelPrompt is Options > Default Level: the level of people who aren't
// in the Users list, like /dlevel.
func (ed *scriptEditor) levelPrompt(a *App) {
	ed.prompt(a, "Default Level", "Level:", strconv.Itoa(max(1, a.book().ScriptLevel)), nil, func(v string) error {
		if n, err := strconv.Atoi(v); err != nil || n < 0 {
			return errors.New("give a level, like 1")
		}
		actions, _ := a.scripts.Command(a.active.Net.Name, "", "dlevel "+v, false, a.scriptHost())
		a.queueScript(a.runScript(actions))
		return nil
	})
}

// reloadChanged is Options > Monitor File Changes: a file changed by
// another program since it was read is read again, or, if it has changes
// here too, asks first. trout looks when the terminal gets the focus back.
func (ed *scriptEditor) reloadChanged(a *App) {
	for _, d := range ed.docs {
		if d.tab == edUsers || d.tab == edVars || d.load {
			continue
		}
		data, err := a.readScript(d.file)
		if err != nil || data == d.disk {
			continue
		}
		if !d.dirty {
			a.rereadDoc(d, data)
			continue
		}
		d.disk = data // asked once
		m := message(d.label()+" was changed by another program.", "", "Read it again, and lose the changes here?")
		m.Title = "Scripts Editor"
		m.Buttons, m.Cancel = []string{"Yes", "No"}, 1
		m.OnClose = func(a *App, button int) tea.Cmd {
			if button == 0 {
				a.rereadDoc(d, data)
			}
			return ed.back(a)
		}
		a.openDialog(m)
		return
	}
}

// rereadDoc fills d from its file's data again.
func (a *App) rereadDoc(d *scriptDoc, data string) {
	if d.tab == edPopups && d.def {
		secs, _, _ := iniSections(data)
		d.setText(strings.Join(secs[d.ini], "\n"))
	} else {
		a.readDoc(d)
	}
	d.disk, d.dirty, d.undo, d.redo = data, false, nil, nil
	d.moveTo(d.row, d.col, false)
}

// barPos is where menubar entry i is: where draw puts it, in the dialog
// that drawModal centers.
func (ed *scriptEditor) barPos(a *App, i int) (int, int) {
	w, h := ed.size(a)
	x, y := (a.w-min(w, a.w))/2, (a.h-min(h, a.h))/2
	x++
	for _, name := range edMenuNames[:i] {
		x += len(name) + 2
	}
	return x, y + 1
}

// edIndexLine is a line the Index menu lists in a script: a section or a
// group.
var edIndexLine = regexp.MustCompile(`(?i)^(alias|on|ctcp|raw|menu|dialog)\s|^#\S+\s+(on|off)\b`)

// index is the Index menu, from what's in the file: on the Remote tab a
// submenu for each kind of section (alias, on, ctcp, raw, menu, dialog,
// #groups), like mIRC's; on Aliases each alias, on Popups each top item.
// Choosing one goes to its line.
func (ed *scriptEditor) index(a *App, d *scriptDoc) []MenuItem {
	var items []MenuItem
	subs := map[string]int{} // kind -> its submenu in items
	most := max(1, a.h-6)
	for i, l := range d.lines {
		s := strings.TrimSpace(string(l))
		label, kind := "", ""
		switch {
		case d.tab == edRemote && edIndexLine.MatchString(s):
			kind, label, _ = strings.Cut(strings.TrimSpace(strings.TrimSuffix(s, "{")), " ")
			kind = strings.ToLower(kind)
			if strings.HasPrefix(kind, "#") {
				label, kind = kind, "#groups"
			}
		case d.tab == edAliases && s != "" && l[0] != ' ' && !strings.ContainsAny(s[:1], "{};"):
			label, _, _ = strings.Cut(s, " ")
		case d.tab == edPopups && s != "" && l[0] != '.' && s != "-" && !strings.ContainsAny(s[:1], "{};"):
			label, _, _ = strings.Cut(s, ":")
		}
		if label == "" {
			continue
		}
		it := MenuItem{Label: truncate(strings.TrimSpace(label), 40), Action: func(a *App) tea.Cmd {
			d.moveTo(i, 0, false)
			d.top = i
			return nil
		}}
		switch n, ok := subs[kind]; {
		case kind == "" && len(items) < most:
			items = append(items, it)
		case kind != "" && !ok:
			subs[kind] = len(items)
			items = append(items, MenuItem{Label: kind, Sub: []MenuItem{it}})
		case kind != "" && len(items[n].Sub) < most:
			items[n].Sub = append(items[n].Sub, it)
		}
	}
	if len(items) == 0 {
		items = []MenuItem{{Label: "(empty)", Disabled: true}}
	}
	return items
}

// newDoc is File > New: an empty script, script1.mrc or the next free
// name, saved and loaded on OK.
func (ed *scriptEditor) newDoc(a *App) {
	base, ext := "script", ".mrc"
	if ed.tab == edAliases {
		base, ext = "aliases", ".ini"
	}
	name := ""
	for n := 1; ; n++ {
		name = base + strconv.Itoa(n) + ext
		if _, err := a.readScript(name); err != nil && ed.findDoc(ed.tab, name) == nil {
			break
		}
	}
	d := &scriptDoc{tab: ed.tab, file: name, dirty: true, load: true, lines: [][]rune{{}}}
	if ext == ".ini" {
		d.ini = edSections[ed.tab]
	}
	ed.add(d)
	ed.cur[ed.tab] = d
}

func (ed *scriptEditor) unload(a *App, d *scriptDoc) {
	if d.def {
		return
	}
	if name := cmp.Or(d.was, d.file); a.scriptLoaded(name) {
		ed.unloads = append(ed.unloads, name)
	}
	ed.docs = slices.DeleteFunc(ed.docs, func(o *scriptDoc) bool { return o == d })
	ed.cur[ed.tab] = nil
	for _, o := range ed.docs {
		if o.tab == ed.tab && ed.cur[ed.tab] == nil {
			ed.cur[ed.tab] = o
		}
	}
}

// prompt asks for one value, then comes back to the editor.
func (ed *scriptEditor) prompt(a *App, title, label, value string, lines []string, ok func(v string) error) {
	f := &formBox{Title: title, Buttons: []string{"OK", "Cancel"}, Lines: lines,
		Fields:   []*formField{newField(label, value, false)},
		OnCancel: ed.back}
	f.OnOK = func(a *App, v []string) (tea.Cmd, error) {
		if err := ok(strings.TrimSpace(v[0])); err != nil {
			return nil, err
		}
		if a.dlg == f { // unless ok opened a message
			a.dlg = ed
		}
		return nil, nil
	}
	a.openDialog(f)
}

// loadPrompt is File > Load...: a script loaded on this tab when you
// press OK; on Users and Variables, a file of them added to the list.
func (ed *scriptEditor) loadPrompt(a *App) {
	ed.prompt(a, "Load", "File:", "", []string{"A file in the scripts folder, or a full path."}, func(file string) error {
		file = strings.Trim(file, `"`)
		if file == "" {
			return errors.New("give a file")
		}
		data, err := a.readScript(file)
		if err != nil {
			return errors.New("couldn't read " + file)
		}
		cur := ed.doc()
		if ed.tab == edUsers || ed.tab == edVars {
			lines := mircList(data, ed.tab == edVars)
			if len(lines) > 0 {
				cur.moveTo(len(cur.lines)-1, len(cur.lines[len(cur.lines)-1]), false)
				text := strings.Join(lines, "\n")
				if cur.text() != "" {
					text = "\n" + text
				}
				cur.edit("", func() bool { return cur.insert([]rune(text)) })
			}
			return nil
		}
		if ed.tab == edPopups && strings.EqualFold(file, "popups.ini") {
			return errors.New("popups.ini is this tab's own file")
		}
		if d := ed.findDoc(ed.tab, file); d != nil {
			ed.cur[ed.tab] = d
			return nil
		}
		d := &scriptDoc{tab: ed.tab, file: file, load: true}
		if ed.tab == edPopups {
			d.kind = cur.kind
		}
		a.readDoc(d)
		ed.add(d)
		ed.cur[ed.tab] = d
		return nil
	})
}

// saveAsPrompt is File > Save As...: the file gets a new name, and is
// loaded under it on OK.
func (ed *scriptEditor) saveAsPrompt(a *App) {
	d := ed.doc()
	ed.prompt(a, "Save As", "File:", d.file, nil, func(file string) error {
		file = strings.Trim(file, `"`)
		if _, ok := a.scriptFile(file); !ok && !filepath.IsAbs(file) {
			return errors.New("a name in the scripts folder, or a full path")
		}
		if o := ed.findDoc(d.tab, file); o != nil && o != d {
			return errors.New(file + " is open already")
		}
		if d.was == "" && a.scriptLoaded(d.file) {
			d.was = d.file
		}
		d.file, d.dirty, d.load = file, true, true
		return nil
	})
}

func (ed *scriptEditor) gotoPrompt(a *App) {
	d := ed.doc()
	ed.prompt(a, "Go To Line", "Line:", "", nil, func(v string) error {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return errors.New("give a line number")
		}
		d.moveTo(n-1, 0, false)
		return nil
	})
}

func (ed *scriptEditor) findPrompt(a *App) {
	find := ed.find
	if sel := ed.doc().selText(); sel != "" && !strings.Contains(sel, "\n") && !a.book().ScriptOpts.NoFindSel {
		find = sel // Options > Find Selection
	}
	ed.prompt(a, "Find", "Find:", find, nil, func(v string) error {
		if v == "" {
			return errors.New("give some text")
		}
		ed.find = v
		ed.findNext(a)
		return nil
	})
}

// findNext selects the next match after the caret, case-insensitively,
// going round to the top.
func (ed *scriptEditor) findNext(a *App) {
	d := ed.doc()
	if ed.find == "" {
		ed.findPrompt(a)
		return
	}
	needle := []rune(strings.ToLower(ed.find))
	for i := range len(d.lines) + 1 {
		row := (d.row + i) % len(d.lines)
		line := []rune(strings.ToLower(string(d.lines[row])))
		from := 0
		if i == 0 {
			from = d.col
		}
		if len(line) != len(d.lines[row]) { // a case change that changes the length
			continue
		}
		for c := from; c+len(needle) <= len(line); c++ {
			if slices.Equal(line[c:c+len(needle)], needle) {
				d.moveTo(row, c, false)
				d.moveTo(row, c+len(needle), true)
				return
			}
		}
	}
	m := message(`Can't find "` + ed.find + `".`)
	m.Title = "Find"
	m.OnClose = func(a *App, _ int) tea.Cmd { return ed.back(a) }
	a.openDialog(m)
}

// mircList reads a Users or variables file: mIRC's ini format, or one
// entry a line, as levels:address lines or %name value ones.
func mircList(data string, vars bool) []string {
	var lines []string
	for l := range strings.Lines(data) {
		l = strings.TrimSpace(l)
		if k, v, ok := strings.Cut(l, "="); ok && len(k) > 1 && k[0] == 'n' && isDigits(k[1:]) {
			l = v // the ini format's n0=
		}
		switch {
		case l == "" || strings.HasPrefix(l, "[") || strings.HasPrefix(l, ";"):
		case vars && strings.HasPrefix(l, "%"):
			lines = append(lines, l)
		case !vars && strings.Contains(l, ":"):
			lines = append(lines, l)
		}
	}
	return lines
}
