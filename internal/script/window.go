package script

import (
	"cmp"
	"slices"
	"strconv"
	"strings"
)

// Custom windows: mIRC's @windows (/window, /aline and the rest, $window,
// $line). The client keeps them, since they're on screen, and scripts
// change them through Windows as they run rather than with actions
// afterwards, so an /aline is in $line on the next line, like mIRC's.
// Picture windows (-p and /draw*) aren't supported: they're drawing.
// What mIRC does where its help is silent was checked in mIRC 7.86
// (tmp/win-oracle.mrc and win-oracle2.mrc).

// Windows is a Host with custom windows; the client's is. With a Host
// that has none, the commands say the window isn't there.
type Windows interface {
	// CustomWindows is the open ones' names, in the order they opened.
	CustomWindows() []string
	// CustomWindow is the one called name (any case), or nil.
	CustomWindow(name string) Window
	// OpenWindow opens a new one; the engine activates it afterwards
	// unless it's minimized or hidden.
	OpenWindow(name string, o WinOpts) Window
	// CloseWindow closes one, without on CLOSE: like mIRC's, that's only
	// for the user closing it (checked).
	CloseWindow(name string)
	// ActivateWindow brings one to the front, restored.
	ActivateWindow(name string)
	RenameWindow(old, name string)
}

// Window is one custom window: its lines and options. side picks the
// side listbox's lines instead of the main ones.
type Window interface {
	Lines(side bool) []WinLine
	SetLines(side bool, lines []WinLine)
	Opts() WinOpts
	SetOpts(o WinOpts)
}

// WinLine is a line of a custom window.
type WinLine struct {
	Text  string
	Color int  // a mIRC color, or -1 for the window's own
	Sel   bool // selected, in a listbox
}

// WinOpts are a custom window's /window switches and parameters.
type WinOpts struct {
	Network    string // the connection it belongs to: $window().cid
	Listbox    bool   // -l: its lines are a list to select from
	Side       int    // -lN: a side listbox N characters wide
	Editbox    bool   // -e
	Sort       bool   // -s: lines are kept in order, ignoring case
	SortSide   bool   // -S: the side listbox's
	HidePrefix bool   // -k0: its button says the name without the @
	Hidden     bool   // -h: not shown at all
	NoButton   bool   // -w0: not on the switchbar or treebar
	Minimized  bool   // -n
	Title      string // /renwin's topic, after the name in the title bar
	Command    string // the editbox's default command: typed text goes to it
	Popup      string // its popup file, or a menu's name
	Buffer     int    // -j: lines kept, 0 for the client's
	Highlight  int    // -g: its button's color: 1 message, 2 highlight
	AnyConn    bool   // -i: it goes with whichever connection is active
}

// windows is the host's custom windows, or nil.
func (r *run) windows() Windows {
	w, _ := r.h.(Windows)
	return w
}

// customWindow finds an open custom window by name, or nil.
func (r *run) customWindow(name string) Window {
	if wh := r.windows(); wh != nil && strings.HasPrefix(name, "@") {
		return wh.CustomWindow(name)
	}
	return nil
}

// winSwitches reads mIRC's switch letters, each with the number after it
// if there is one: "l12e" is l=12, e=-1 (no number).
func winSwitches(sw string) map[byte]int {
	m := map[byte]int{}
	for i := 0; i < len(sw); {
		c := sw[i]
		i++
		j := i
		for j < len(sw) && (sw[j] >= '0' && sw[j] <= '9' || c == 't' && sw[j] == ',' || c == 'q' && sw[j] == ':') {
			j++
		}
		m[c] = -1
		if n, err := strconv.Atoi(sw[i:j]); err == nil {
			m[c] = n
		}
		i = j
	}
	return m
}

// cmdWindow is /window [-switches] [+appearance] @name [x y [w h]]
// [/command] [popup.txt] [font [size]] [iconfile [N]]. Positions, sizes,
// fonts, icons and appearance are the terminal's: they're read and
// dropped.
func (r *run) cmdWindow(fr *frame, line string) error {
	words := strings.Fields(line)
	var sw strings.Builder
	for len(words) > 0 && len(words[0]) > 1 && (words[0][0] == '-' || words[0][0] == '+') {
		if words[0][0] == '-' {
			sw.WriteString(words[0][1:])
		}
		words = words[1:]
	}
	if len(words) == 0 || len(words[0]) < 2 || words[0][0] != '@' {
		return errorf("/window: invalid parameters") // checked: a name without @
	}
	name := words[0]
	words = words[1:]
	for i := 0; i < 4 && len(words) > 0; i++ {
		if _, err := strconv.Atoi(words[0]); err != nil {
			break
		}
		words = words[1:]
	}
	command, popup := "", ""
	if len(words) > 0 && strings.HasPrefix(words[0], "/") {
		command, words = words[0], words[1:]
	}
	if len(words) > 0 {
		popup = words[0]
	}
	s := winSwitches(sw.String())
	if _, ok := s['p']; ok {
		return errorf("/window: picture windows aren't supported")
	}
	wh := r.windows()
	if wh == nil {
		return errorf("/window: custom windows aren't supported here")
	}
	w := wh.CustomWindow(name)
	if _, ok := s['c']; ok {
		if w != nil {
			wh.CloseWindow(name)
		}
		return nil
	}
	isNew := w == nil
	o := WinOpts{Network: fr.network}
	if !isNew {
		o = w.Opts()
	}
	for c, n := range s {
		switch c {
		case 'l':
			if isNew && n > 0 {
				o.Side = n
			} else if isNew {
				o.Listbox = true
			}
		case 'e':
			o.Editbox = true
		case 's':
			o.Sort = true
		case 'S':
			o.SortSide = true
		case 'k':
			o.HidePrefix = n <= 0
		case 'h':
			o.Hidden = true
		case 'w':
			o.NoButton = n == 0
		case 'n':
			o.Minimized = true
		case 'r', 'x':
			o.Minimized, o.Hidden = false, false
		case 'j':
			o.Buffer = max(n, 0)
		case 'g':
			o.Highlight = max(n, 0)
		case 'i':
			o.AnyConn = true
		}
	}
	if command != "" {
		o.Command = command
	}
	if popup != "" {
		o.Popup = popup
	}
	if isNew {
		wh.OpenWindow(name, o)
		if !o.Minimized && !o.Hidden { // checked: a new window comes to the front
			wh.ActivateWindow(name)
		}
		return nil
	}
	if _, ok := s['a']; ok {
		o.Minimized, o.Hidden = false, false
		w.SetOpts(o)
		wh.ActivateWindow(name)
		return nil
	}
	w.SetOpts(o)
	return nil
}

// lineArgs reads a line command's [-switches] [color] @name: the rest
// is what's left.
func lineArgs(cmd, line string) (sw map[byte]int, color int, name string, rest []string, err error) {
	words := strings.Fields(line)
	var raw strings.Builder
	color = -1
	for len(words) > 0 && !strings.HasPrefix(words[0], "@") {
		switch w := words[0]; {
		case len(w) > 1 && w[0] == '-':
			raw.WriteString(w[1:])
		case isDigits(w):
			color = atoi(w)
		default:
			return nil, 0, "", nil, errorf("/", cmd, ": invalid parameters")
		}
		words = words[1:]
	}
	if len(words) == 0 {
		return nil, 0, "", nil, errorf("/", cmd, ": insufficient parameters")
	}
	return winSwitches(raw.String()), color, words[0], words[1:], nil
}

// lineNum reads a line number: 0 for one that isn't.
func lineNum(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

// cmdLine is /aline, /iline, /rline, /dline, /cline and /sline.
func (r *run) cmdLine(fr *frame, cmd, line string) error {
	sw, color, name, rest, err := lineArgs(cmd, line)
	if err != nil {
		return err
	}
	w := r.customWindow(name)
	if w == nil {
		return errorf("/", cmd, ": invalid window") // checked
	}
	_, side := sw['l']
	o := w.Opts()
	lines := slices.Clone(w.Lines(side))
	sorted := o.Sort && !side || o.SortSide && side
	_, sel := sw['s']
	_, addSel := sw['a']
	selectOnly := func(i int) {
		if sel {
			for j := range lines {
				lines[j].Sel = false
			}
		}
		if sel || addSel {
			lines[i].Sel = true
		}
	}
	at := func(i int, l WinLine) int {
		if sorted {
			key := strings.ToLower(l.Text)
			i, _ = slices.BinarySearchFunc(lines, key, func(x WinLine, k string) int {
				return cmp.Compare(strings.ToLower(x.Text), k)
			})
			for i < len(lines) && strings.ToLower(lines[i].Text) == key {
				i++
			}
		}
		lines = slices.Insert(lines, i, l)
		return i
	}
	switch cmd {
	case "aline", "iline":
		pos := len(lines)
		if cmd == "iline" {
			if len(rest) < 2 || lineNum(rest[0]) < 1 {
				return errorf("/iline: invalid parameters") // checked: line 0
			}
			pos, rest = min(lineNum(rest[0])-1, len(lines)), rest[1:] // past the end adds (checked)
		}
		text := strings.Join(rest, " ")
		if _, ok := sw['n']; ok && slices.ContainsFunc(lines, func(l WinLine) bool { return l.Text == text }) {
			return nil // checked: an exact match, case and all
		}
		selectOnly(at(pos, WinLine{Text: text, Color: color}))
		if _, ok := sw['h']; ok && (o.Minimized || o.Hidden) {
			o.Highlight = max(o.Highlight, 1)
			w.SetOpts(o)
		}
	case "rline":
		if len(rest) < 2 {
			return errorf("/rline: insufficient parameters")
		}
		i := lineNum(rest[0]) - 1
		if i < 0 || i >= len(lines) {
			return nil // checked: no error past the end
		}
		lines[i] = WinLine{Text: strings.Join(rest[1:], " "), Color: color} // the selection goes (checked)
		selectOnly(i)
	case "dline":
		if len(rest) == 0 {
			return errorf("/dline: insufficient parameters")
		}
		a, b, isRange := strings.Cut(rest[0], "-")
		from, to := lineNum(a), lineNum(a)
		if isRange {
			to = lineNum(b)
		}
		to = min(to, len(lines))
		if from < 1 || to < from {
			return nil // checked: out of range or backwards does nothing
		}
		lines = slices.Delete(lines, from-1, to)
	case "cline":
		if len(rest) == 0 {
			return errorf("/cline: insufficient parameters")
		}
		if i := lineNum(rest[0]) - 1; i >= 0 && i < len(lines) {
			lines[i].Color = color
		}
	case "sline":
		if len(rest) == 0 {
			return errorf("/sline: insufficient parameters")
		}
		i := lineNum(rest[0]) - 1
		if !(o.Listbox || side) || i < 0 || i >= len(lines) {
			return nil // checked: in a text window it does nothing
		}
		if !addSel {
			for j := range lines {
				lines[j].Sel = false
			}
		}
		lines[i].Sel = true
	}
	if o.Buffer > 0 && len(lines) > o.Buffer {
		lines = lines[len(lines)-o.Buffer:]
	}
	w.SetLines(side, lines)
	return nil
}

// echoWindow is /echo to a custom window; a closed one is no error
// (checked).
func (r *run) echoWindow(name string, color int, text string) {
	if w := r.customWindow(name); w != nil {
		o := w.Opts()
		lines := append(slices.Clone(w.Lines(false)), WinLine{Text: text, Color: color})
		if o.Buffer > 0 && len(lines) > o.Buffer {
			lines = lines[len(lines)-o.Buffer:]
		}
		w.SetLines(false, lines)
	}
}

// cmdClear is /clear [-l] @name for custom windows; ok is false for
// any other window, which the client clears.
func (r *run) cmdClear(fr *frame, line string) (ok bool) {
	words := strings.Fields(line)
	side := false
	for len(words) > 0 && strings.HasPrefix(words[0], "-") {
		side = side || strings.Contains(words[0], "l")
		words = words[1:]
	}
	name := fr.window
	if len(words) > 0 {
		name = words[0]
	}
	if !strings.HasPrefix(name, "@") {
		return false
	}
	if w := r.customWindow(name); w != nil { // a closed one is no error (checked)
		w.SetLines(side, nil)
	}
	return true
}

// cmdRenwin is /renwin @old @new [topic].
func (r *run) cmdRenwin(line string) error {
	words := strings.Fields(line)
	if len(words) < 2 || !strings.HasPrefix(words[1], "@") {
		return errorf("/renwin: invalid parameters")
	}
	w := r.customWindow(words[0])
	if w == nil {
		return errorf("/renwin: invalid window") // checked
	}
	if len(words) > 2 {
		o := w.Opts()
		o.Title = strings.Join(words[2:], " ")
		w.SetOpts(o)
	}
	r.windows().RenameWindow(words[0], words[1])
	return nil
}

// pickLines is [lines] in /loadbuf and /savebuf: N is the last N, N-M
// lines N to M.
func pickLines(spec string, lines []string) []string {
	if spec == "" {
		return lines
	}
	a, b, isRange := strings.Cut(spec, "-")
	if !isRange {
		return lines[len(lines)-min(max(lineNum(a), 0), len(lines)):]
	}
	from, to := max(lineNum(a), 1), min(lineNum(b), len(lines))
	if to < from {
		return nil
	}
	return lines[from-1 : to]
}

// bufArgs reads /loadbuf's and /savebuf's [lines] [-switches] in any
// order, then the window and the file.
func bufArgs(cmd, line string) (spec string, sw map[byte]int, win, file string, err error) {
	words := strings.Fields(line)
	var raw strings.Builder
	for len(words) > 0 {
		w := words[0]
		switch {
		case len(w) > 1 && w[0] == '-':
			raw.WriteString(w[1:])
		case w != "" && w[0] >= '0' && w[0] <= '9':
			spec = w
		default:
			goto done
		}
		words = words[1:]
	}
done:
	sw = winSwitches(raw.String())
	_, toStatus := sw['s']
	_, toActive := sw['a']
	if toStatus || toActive {
		words = append([]string{""}, words...)
	}
	if len(words) < 2 {
		return "", nil, "", "", errorf("/", cmd, ": insufficient parameters")
	}
	return spec, sw, words[0], strings.Join(words[1:], " "), nil
}

// cmdLoadbuf is /loadbuf [lines] [-switches] <window> <file>: a custom
// window's lines, or echoes into any other window.
func (r *run) cmdLoadbuf(fr *frame, line string) error {
	spec, sw, win, file, err := bufArgs("loadbuf", line)
	if err != nil {
		return err
	}
	if _, ok := sw['o']; ok {
		return errorf("/loadbuf: dialogs aren't supported")
	}
	data, ok := r.readFile(file)
	if !ok {
		return errorf("/loadbuf: unable to open file ", file)
	}
	lines := fileLines(data)
	if _, ok := sw['t']; ok {
		return errorf("/loadbuf: -t isn't supported yet")
	}
	lines = pickLines(spec, lines)
	color := -1
	if c, ok := sw['c']; ok && c >= 0 {
		color = c
	}
	if _, ok := sw['e']; ok {
		for i, l := range lines {
			v, err := r.eval(fr, l)
			if err != nil {
				return err
			}
			lines[i] = v
		}
	}
	if strings.HasPrefix(win, "@") {
		w := r.customWindow(win)
		if w == nil {
			return errorf("/loadbuf: invalid window") // checked
		}
		_, side := sw['l']
		var out []WinLine
		if _, ok := sw['r']; !ok {
			out = slices.Clone(w.Lines(side))
		}
		for _, l := range lines {
			out = append(out, WinLine{Text: l, Color: color})
		}
		if b := w.Opts().Buffer; b > 0 && len(out) > b {
			out = out[len(out)-b:]
		}
		w.SetLines(side, out)
		return nil
	}
	net := fr.network
	if _, ok := sw['a']; ok {
		net, win = r.h.Active()
	} else if win != "" && !r.h.HasWindow(fr.network, win) {
		return errorf("/loadbuf: invalid window")
	}
	for _, l := range lines {
		r.actions = append(r.actions, Action{Echo: &Echo{Network: net, Window: win, Color: color, Text: l}})
	}
	return nil
}

// cmdSavebuf is /savebuf [-switches] [lines] @name file. trout only
// saves custom windows: the bouncer keeps the others' logs.
func (r *run) cmdSavebuf(fr *frame, line string) error {
	spec, sw, win, file, err := bufArgs("savebuf", line)
	if err != nil {
		return err
	}
	w := r.customWindow(win)
	if w == nil {
		return errorf("/savebuf: invalid window")
	}
	_, side := sw['l']
	_, strip := sw['p']
	var lines []string
	for _, l := range w.Lines(side) {
		lines = append(lines, iff(strip, stripCodes(l.Text, ""), l.Text))
	}
	lines = pickLines(spec, lines)
	data := ""
	if len(lines) > 0 {
		data = strings.Join(lines, "\r\n") + "\r\n"
	}
	_, add := sw['a']
	r.writeFile(file, data, add)
	return nil
}

// cmdFilter is /filter [-switches] [n-n2] [c s] <in> <out> [alias]
// <matchtext>: copies the lines of a file or custom window that match
// into another, or to an alias (-k). -a (sorting with an alias) isn't
// supported.
func (r *run) cmdFilter(fr *frame, line string) error {
	words := strings.Fields(line)
	raw := ""
	for len(words) > 0 && len(words[0]) > 1 && words[0][0] == '-' {
		raw += words[0][1:]
		words = words[1:]
	}
	sw := winSwitches(raw)
	has := func(c byte) bool { _, ok := sw[c]; return ok }
	if has('a') {
		return errorf("/filter: -a isn't supported yet")
	}
	if has('s') || has('d') || has('i') || has('o') {
		return errorf("/filter: trout only filters files and custom windows")
	}
	spec := ""
	if has('r') && len(words) > 0 {
		spec, words = words[0], words[1:]
	}
	col, sep := 0, " "
	if has('t') && len(words) > 1 {
		col = lineNum(words[0])
		if c, ok := charCode(words[1]); ok {
			sep = c
		}
		words = words[2:]
	}
	if len(words) < 2 {
		return errorf("/filter: insufficient parameters")
	}
	in, out := words[0], words[1]
	match := strings.Join(words[2:], " ")
	if match == "" {
		match = "*"
	}
	// Which are windows: -f and -w in order, else a name with an @.
	kinds := ""
	for _, c := range raw {
		if c == 'f' || c == 'w' {
			kinds += string(c)
		}
	}
	isWin := func(i int, name string) bool {
		if i < len(kinds) {
			return kinds[i] == 'w'
		}
		return strings.HasPrefix(name, "@")
	}
	type item struct {
		WinLine
		n int
	}
	var src []item
	if isWin(0, in) {
		w := r.customWindow(in)
		if w == nil {
			return errorf("/filter: invalid window")
		}
		for i, l := range w.Lines(has('l')) {
			src = append(src, item{l, i + 1})
		}
	} else {
		data, ok := r.readFile(in)
		if !ok {
			return errorf("/filter: unable to open file ", in)
		}
		for i, l := range fileLines(data) {
			src = append(src, item{WinLine{Text: l, Color: -1}, i + 1})
		}
	}
	if spec != "" {
		a, b, _ := strings.Cut(spec, "-")
		from, to := max(lineNum(a), 1), len(src)
		if b != "" {
			to = min(lineNum(b), len(src))
		}
		if to < from {
			src = nil
		} else {
			src = src[from-1 : to]
		}
	}
	var hits []item
	for _, it := range src {
		text := it.Text
		if has('b') {
			text = stripCodes(text, "")
		}
		var ok bool
		if has('g') {
			var err error
			if ok, err = r.regexMatch(match, text); err != nil {
				return err
			}
		} else {
			ok = wildMatch(match, text, false)
		}
		if ok != has('x') {
			hits = append(hits, it)
		}
	}
	if has('t') || has('e') || has('u') {
		key := func(it item) string {
			if col > 0 {
				return tokenAt(it.Text, col, sep)
			}
			return it.Text
		}
		slices.SortStableFunc(hits, func(a, b item) int {
			var c int
			if has('u') {
				x, _ := parseNum(key(a))
				y, _ := parseNum(key(b))
				c = cmp.Compare(x, y)
			} else {
				c = cmp.Compare(strings.ToLower(key(a)), strings.ToLower(key(b)))
			}
			if has('e') {
				return -c
			}
			return c
		})
	}
	r.e.filtered = len(hits)
	text := func(it item) string {
		if has('n') {
			return itoa(it.n) + " " + it.Text
		}
		return it.Text
	}
	switch {
	case has('k'):
		a := r.e.alias(fr.file, out)
		if a == nil {
			return errorf("/filter: no such alias ", out)
		}
		for _, it := range hits {
			if _, err := r.callAlias(fr, a, fields(text(it)), false, ""); err != nil {
				return err
			}
		}
	case isWin(1, out):
		w := r.customWindow(out)
		if w == nil {
			return errorf("/filter: invalid window") // checked: it stops there
		}
		side := has('L')
		var lines []WinLine
		if !has('c') {
			lines = slices.Clone(w.Lines(side))
		}
		for _, it := range hits {
			l := WinLine{Text: text(it), Color: -1}
			if has('z') {
				l.Color = it.Color
			}
			lines = append(lines, l)
		}
		w.SetLines(side, lines)
	default:
		var b strings.Builder
		for _, it := range hits {
			b.WriteString(text(it) + "\r\n")
		}
		r.writeFile(out, b.String(), !has('c'))
	}
	return nil
}

// tokenAt is the Nth sep-separated token of s, for /filter -t.
func tokenAt(s string, n int, sep string) string {
	toks := strings.Split(s, sep)
	if n < 1 || n > len(toks) {
		return ""
	}
	return toks[n-1]
}

var windowIdents = map[string]builtin{
	// $window(@name|N|@wild,N): a window's properties; N = 0 is how
	// many custom windows there are.
	"window": func(r *run, fr *frame, c *call) (string, error) {
		arg := c.arg(0)
		var name string
		wh := r.windows()
		var all []string
		if wh != nil {
			all = wh.CustomWindows()
		}
		switch n, isNum := parseNum(arg); {
		case len(c.args) > 1:
			var hits []string
			for _, w := range all {
				if wildMatch(arg, w, false) {
					hits = append(hits, w)
				}
			}
			i := lineNum(c.arg(1))
			if i == 0 {
				return itoa(len(hits)), nil
			}
			if i < 0 || i > len(hits) {
				return "", nil
			}
			name = hits[i-1]
		case isNum && !strings.HasPrefix(arg, "@"):
			i := int(n)
			if i == 0 {
				return itoa(len(all)), nil
			}
			if i < 0 || i > len(all) {
				return "", nil
			}
			name = all[i-1]
		case strings.HasPrefix(arg, "@"):
			name = arg
		default:
			return otherWindow(r, fr, arg, c.prop)
		}
		w := r.customWindow(name)
		if w == nil {
			return "", nil
		}
		if i := slices.IndexFunc(all, func(s string) bool { return strings.EqualFold(s, name) }); i >= 0 {
			name = all[i] // as it was opened
		}
		o := w.Opts()
		switch strings.ToLower(c.prop) {
		case "":
			return name, nil
		case "state":
			switch {
			case o.Hidden:
				return "hidden", nil
			case o.Minimized:
				return "minimized", nil
			}
			return "maximized", nil // trout's windows always are
		case "title":
			return o.Title, nil
		case "fulltitle":
			return strings.TrimSpace(name + " " + o.Title), nil
		case "type":
			return iff(o.Listbox, "listbox", "custom"), nil
		case "lb":
			switch {
			case o.Side > 0:
				return "2", nil
			case o.Listbox:
				return "1", nil
			}
			return "0", nil
		case "sbtext", "tbtext":
			return iff(o.HidePrefix, name[1:], name), nil
		case "cid":
			return itoa(slices.Index(r.h.Networks(), o.Network) + 1), nil
		case "anysc":
			return boolStr(o.AnyConn), nil
		case "mdi":
			return "$true", nil
		case "stamp":
			return "off", nil
		case "x", "y", "w", "h", "dw", "dh", "cx", "cy", "dx", "dy", "bw", "bh":
			return "0", nil // the terminal's, in cells, not pixels
		}
		return "", nil
	},
	// $line(@name,N[,T]): the Nth line, or how many with N = 0; T = 1 is
	// the side listbox.
	"line": func(r *run, fr *frame, c *call) (string, error) {
		w := r.customWindow(c.arg(0))
		if w == nil {
			return "", nil
		}
		lines := w.Lines(c.arg(2) == "1")
		n := lineNum(c.arg(1))
		if n == 0 {
			return itoa(len(lines)), nil
		}
		if n < 1 || n > len(lines) {
			return "", nil
		}
		l := lines[n-1]
		switch strings.ToLower(c.prop) {
		case "state":
			return iff(l.Sel, "1", "0"), nil
		case "color":
			if l.Color < 0 {
				return "1", nil // checked: the default
			}
			return itoa(l.Color), nil
		}
		return l.Text, nil
	},
	// $fline(@name,wildtext,N[,T]): the line number of the Nth match, or
	// how many with N = 0. T: 1 the side listbox, 2 a regular expression,
	// 3 both; .text is the line.
	"fline": func(r *run, fr *frame, c *call) (string, error) {
		w := r.customWindow(c.arg(0))
		if w == nil {
			return "", nil
		}
		t := lineNum(c.arg(3))
		n := lineNum(c.arg(2))
		count := 0
		for i, l := range w.Lines(t&1 == 1) {
			var ok bool
			if t&2 == 2 {
				var err error
				if ok, err = r.regexMatch(c.arg(1), l.Text); err != nil {
					return "", err
				}
			} else {
				ok = wildMatch(c.arg(1), l.Text, false) // checked: ignoring case
			}
			if !ok {
				continue
			}
			if count++; count == n {
				return iff(strings.EqualFold(c.prop, "text"), l.Text, itoa(i+1)), nil
			}
		}
		return iff(n == 0, itoa(count), ""), nil
	},
	// $sline(@name,N): the Nth selected line of a listbox, or how many
	// with N = 0; .ln is its line number.
	"sline": func(r *run, fr *frame, c *call) (string, error) {
		w := r.customWindow(c.arg(0))
		if w == nil {
			return "", nil
		}
		o := w.Opts()
		if !o.Listbox && o.Side == 0 {
			return "", nil // checked: not in a text window
		}
		n := lineNum(c.arg(1))
		count := 0
		for i, l := range w.Lines(!o.Listbox) {
			if !l.Sel {
				continue
			}
			if count++; count == n {
				return iff(strings.EqualFold(c.prop, "ln"), itoa(i+1), l.Text), nil
			}
		}
		return iff(n == 0, itoa(count), ""), nil
	},
	"filtered": func(r *run, fr *frame, c *call) (string, error) {
		return itoa(r.e.filtered), nil
	},
}

// otherWindow is $window() for a window that isn't a custom one: a
// channel, query or chat window that's open, or the status window.
func otherWindow(r *run, fr *frame, name, prop string) (string, error) {
	kind := ""
	switch {
	case strings.EqualFold(name, "Status Window"):
		name, kind = "Status Window", "status"
	case !r.h.HasWindow(fr.network, name):
		return "", nil
	case isChannel(name):
		kind = "channel"
	case strings.HasPrefix(name, "="):
		kind = "chat"
	default:
		kind = "query"
	}
	switch strings.ToLower(prop) {
	case "":
		return name, nil
	case "state":
		return "maximized", nil
	case "type":
		return kind, nil
	case "cid":
		return itoa(slices.Index(r.h.Networks(), fr.network) + 1), nil
	}
	return "", nil
}
