package script

import (
	"maps"
	"path"
	"slices"
	"strings"
)

// Script files: /write and $read, from mIRC's File Handling section. The
// client keeps them in the scripts folder; see Host.ReadFile and File.

// cmdWrite is /write [-c] <file> [text]: it adds a line to the end of the
// file, and -c empties it first.
func (r *run) cmdWrite(fr *frame, rest string) error {
	line, err := r.eval(fr, rest)
	if err != nil {
		return err
	}
	sw, line := switches(line)
	file, text, _ := strings.Cut(line, " ")
	if file == "" {
		return errorf("/write: insufficient parameters")
	}
	if strings.ContainsAny(sw, "ildsw") {
		return errorf("/write -", sw, " isn't supported yet")
	}
	clearFirst := strings.Contains(sw, "c")
	if clearFirst && text == "" {
		r.writeFile(file, "", false)
		return nil
	}
	r.writeFile(file, text+"\r\n", !clearFirst)
	return nil
}

// fileLines is a file's lines, without an empty last one.
func fileLines(data string) []string {
	lines := strings.Split(strings.ReplaceAll(data, "\r\n", "\n"), "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	return lines
}

var fileIdents = map[string]builtin{
	// $exists(file) and $isfile(file): whether the file is there.
	"exists": func(r *run, fr *frame, c *call) (string, error) {
		_, ok := r.readFile(c.arg(0))
		return boolStr(ok), nil
	},
	"isfile": func(r *run, fr *frame, c *call) (string, error) {
		_, ok := r.readFile(c.arg(0))
		return boolStr(ok), nil
	},
	// $lines(file) is how many lines it has.
	"lines": func(r *run, fr *frame, c *call) (string, error) {
		data, ok := r.readFile(c.arg(0))
		if !ok {
			return "", nil
		}
		return itoa(len(fileLines(data))), nil
	},
	"read": readIdent,
}

// readIdent is $read(file, [nswrt], [matchtext], [N]): a random line, or
// line N, or with s the rest of the first line starting with the word
// matchtext, with w the first line matching it as a wildcard, and with r
// as a regular expression (searching from line N). The line is evaluated
// unless n is given, like mIRC.
func readIdent(r *run, fr *frame, c *call) (string, error) {
	data, ok := r.readFile(c.arg(0))
	if !ok {
		return "", nil
	}
	lines := fileLines(data)
	args := c.vals[min(1, len(c.vals)):]
	sw := ""
	if len(args) > 0 && !isDigits(strings.TrimSpace(args[0])) {
		sw, args = strings.TrimSpace(args[0]), args[1:]
	}
	if strings.Contains(sw, "p") {
		return "", errorf("$read(file,", sw, ") isn't supported yet")
	}
	// A first line that's a number is the file's line count, from older
	// mIRCs, and isn't read (checked in mIRC 7.86); t reads it as text.
	if len(lines) > 0 && isDigits(lines[0]) && !strings.Contains(sw, "t") {
		lines = lines[1:]
	}
	search := strings.ContainsAny(sw, "swr")
	match := ""
	if search && len(args) > 0 {
		match, args = args[0], args[1:]
	}
	at := -1
	if len(args) > 0 {
		at = atoi(strings.TrimSpace(args[0]))
	}
	line, found := "", false
	switch {
	case search:
		for i := max(at, 1) - 1; i < len(lines) && !found; i++ {
			l := lines[i]
			switch {
			case strings.Contains(sw, "s"):
				if w, rest, _ := strings.Cut(l, " "); strings.EqualFold(w, match) {
					line, found = rest, true
				}
			case strings.Contains(sw, "w"):
				found, line = wildMatch(match, l, false), l
			default:
				m, err := r.regexMatch(match, l)
				if err != nil {
					return "", err
				}
				found, line = m, l
			}
		}
	case at < 0:
		if len(lines) > 0 {
			line, found = lines[r.e.opts.Rand.IntN(len(lines))], true
		}
	case at >= 1 && at <= len(lines):
		line, found = lines[at-1], true
	}
	if !found {
		return "", nil
	}
	if strings.Contains(sw, "n") {
		return line, nil
	}
	return r.eval(fr, line)
}

// INI files, from mIRC's /writeini, /remini and $readini: [section]
// lines, each followed by item=value lines.

type iniFile struct {
	sections []*iniSection
}

type iniSection struct {
	name  string
	items [][2]string // item, value, in order
}

func parseINI(data string) *iniFile {
	f := &iniFile{}
	var cur *iniSection
	for _, l := range fileLines(data) {
		l = strings.TrimSpace(l)
		switch {
		case strings.HasPrefix(l, "[") && strings.HasSuffix(l, "]"):
			cur = &iniSection{name: l[1 : len(l)-1]}
			f.sections = append(f.sections, cur)
		case cur != nil && strings.Contains(l, "="):
			k, v, _ := strings.Cut(l, "=")
			cur.items = append(cur.items, [2]string{k, v})
		}
	}
	return f
}

func (f *iniFile) String() string {
	var b strings.Builder
	for _, s := range f.sections {
		b.WriteString("[" + s.name + "]\r\n")
		for _, it := range s.items {
			b.WriteString(it[0] + "=" + it[1] + "\r\n")
		}
	}
	return b.String()
}

func (f *iniFile) section(name string) (*iniSection, int) {
	for i, s := range f.sections {
		if strings.EqualFold(s.name, name) {
			return s, i
		}
	}
	return nil, -1
}

func (s *iniSection) item(name string) int {
	for i, it := range s.items {
		if strings.EqualFold(it[0], name) {
			return i
		}
	}
	return -1
}

// readINI reads an INI file the script can see, or an empty one.
func (r *run) readINI(name string) *iniFile {
	data, _ := r.readFile(name)
	return parseINI(data)
}

// cmdWriteini is /writeini [-z] <file> <section> <item> <value>.
func (r *run) cmdWriteini(fr *frame, rest string) error {
	line, err := r.eval(fr, rest)
	if err != nil {
		return err
	}
	sw, line := switches(line)
	a := strings.SplitN(line, " ", 4)
	if len(a) < 3 || len(a) < 4 && !strings.Contains(sw, "z") {
		return errorf("/writeini: insufficient parameters")
	}
	value := ""
	if len(a) == 4 {
		value = a[3]
	}
	f := r.readINI(a[0])
	s, _ := f.section(a[1])
	if s == nil {
		s = &iniSection{name: a[1]}
		f.sections = append(f.sections, s)
	}
	if i := s.item(a[2]); i >= 0 {
		s.items[i][1] = value
	} else {
		s.items = append(s.items, [2]string{a[2], value})
	}
	r.writeFile(a[0], f.String(), false)
	return nil
}

// cmdRemini is /remini <file> <section> [item]: it deletes the item, or
// the whole section.
func (r *run) cmdRemini(fr *frame, rest string) error {
	line, err := r.eval(fr, rest)
	if err != nil {
		return err
	}
	a := strings.Fields(line)
	if len(a) < 2 {
		return errorf("/remini: insufficient parameters")
	}
	f := r.readINI(a[0])
	s, si := f.section(a[1])
	if s == nil {
		return nil
	}
	if len(a) == 2 {
		f.sections = append(f.sections[:si], f.sections[si+1:]...)
	} else if i := s.item(a[2]); i >= 0 {
		s.items = append(s.items[:i], s.items[i+1:]...)
	}
	r.writeFile(a[0], f.String(), false)
	return nil
}

// cmdRemove is /remove <file>.
func (r *run) cmdRemove(fr *frame, rest string) error {
	line, err := r.eval(fr, rest)
	if err != nil {
		return err
	}
	_, name := switches(line)
	if name == "" {
		return errorf("/remove: insufficient parameters")
	}
	if _, ok := r.readFile(name); ok {
		r.deleteFile(name)
	}
	return nil
}

// cmdCopy is /copy [-ao] <file> <file>, and /rename [-o] <file> <file>,
// which deletes the first: -a adds to the end of the second, -o
// overwrites it (without either, an existing one is an error).
func (r *run) cmdCopy(fr *frame, rest string, rename bool) error {
	line, err := r.eval(fr, rest)
	if err != nil {
		return err
	}
	sw, line := switches(line)
	a := strings.Fields(line)
	cmd := iff(rename, "/rename", "/copy")
	if len(a) < 2 {
		return errorf(cmd, ": insufficient parameters")
	}
	data, ok := r.readFile(a[0])
	if !ok {
		return errorf(cmd, ": unable to open '", a[0], "'")
	}
	if strings.EqualFold(a[0], a[1]) {
		// The same file (names don't depend on case): a rename only
		// changes its case, and a copy does nothing.
		if rename && a[0] != a[1] {
			r.deleteFile(a[0])
			r.writeFile(a[1], data, false)
		}
		return nil
	}
	_, exists := r.readFile(a[1])
	switch {
	case strings.Contains(sw, "a") && !rename:
		r.writeFile(a[1], data, true)
	case exists && !strings.Contains(sw, "o"):
		return errorf(cmd, ": '", a[1], "' exists")
	default:
		r.writeFile(a[1], data, false)
	}
	if rename {
		r.deleteFile(a[0])
	}
	return nil
}

// handle is a file /fopen opened: its contents, and where reading or
// writing is up to.
type handle struct {
	name, file string
	data       string
	pos        int
	eof, err   bool
}

// cmdFile is /fopen, /fclose, /fwrite and /fseek.
func (r *run) cmdFile(fr *frame, cmd, rest string) error {
	line, err := r.eval(fr, rest)
	if err != nil {
		return err
	}
	sw, line := switches(line)
	name, arg, _ := strings.Cut(line, " ")
	if name == "" {
		return errorf("/", cmd, ": insufficient parameters")
	}
	key := strings.ToLower(name)
	h := r.e.handles[key]
	r.e.ferr, r.e.feof = false, false
	switch cmd {
	case "fopen":
		// A failed /fopen doesn't stop the script: $ferr says so. Opening a
		// file that isn't there still makes the handle, with .err set, like
		// mIRC's (checked); /fclose it.
		data, exists := r.readFile(arg)
		switch {
		case h != nil, arg == "":
			r.e.ferr = true
		case strings.Contains(sw, "n") && exists:
			r.e.ferr = true
		case !strings.ContainsAny(sw, "no") && !exists:
			r.e.ferr = true
			r.e.handles[key] = &handle{name: name, file: arg, err: true}
		case strings.ContainsAny(sw, "no"):
			r.e.handles[key] = &handle{name: name, file: arg}
			if strings.Contains(sw, "o") || !exists {
				r.writeFile(arg, "", false)
			}
		default:
			r.e.handles[key] = &handle{name: name, file: arg, data: data}
		}
	case "fclose":
		for k, h := range r.e.handles {
			if wildMatch(key, k, false) || strings.EqualFold(h.name, name) {
				delete(r.e.handles, k)
			}
		}
	case "fwrite":
		if h == nil {
			r.e.ferr = true
			return nil
		}
		text := arg
		if strings.Contains(sw, "n") {
			text += "\r\n"
		}
		end := min(h.pos+len(text), len(h.data))
		h.data = h.data[:h.pos] + text + h.data[end:]
		h.pos += len(text)
		r.writeFile(h.file, h.data, false)
	case "fseek":
		if h == nil {
			r.e.ferr = true
			return nil
		}
		r.fseek(h, sw, arg)
	}
	return nil
}

// fseek moves a handle: to a byte position, or with -l to line N, -n to
// the next line, -w to the next line matching a wildcard, -r a regex.
func (r *run) fseek(h *handle, sw, arg string) {
	lineStarts := func() []int {
		starts := []int{0}
		for i := 0; i < len(h.data); i++ {
			if h.data[i] == '\n' && i+1 < len(h.data) {
				starts = append(starts, i+1)
			}
		}
		return starts
	}
	switch {
	case strings.Contains(sw, "l"):
		starts := lineStarts()
		if n := atoi(arg); n >= 1 && n <= len(starts) {
			h.pos = starts[n-1]
		} else {
			h.pos, r.e.feof = len(h.data), true
		}
	case strings.Contains(sw, "n"):
		if i := strings.IndexByte(h.data[h.pos:], '\n'); i >= 0 {
			h.pos += i + 1
		} else {
			h.pos, r.e.feof = len(h.data), true
		}
	case strings.ContainsAny(sw, "wr"):
		for _, start := range lineStarts() {
			if start < h.pos {
				continue
			}
			l, _, _ := strings.Cut(h.data[start:], "\n")
			l = strings.TrimSuffix(l, "\r")
			var ok bool
			if strings.Contains(sw, "w") {
				ok = wildMatch(arg, l, false)
			} else {
				ok, _ = r.regexMatch(arg, l)
			}
			if ok {
				h.pos = start
				return
			}
		}
		h.pos, r.e.feof = len(h.data), true
	default:
		h.pos = clamp(atoi(arg), 0, len(h.data))
	}
}

// fileHandle finds a handle by name, or by number like $fopen(N).
func (r *run) fileHandle(nameOrN string) *handle {
	if h := r.e.handles[strings.ToLower(nameOrN)]; h != nil {
		return h
	}
	if n, ok := parseNum(nameOrN); ok && n >= 1 {
		names := slices.Sorted(maps.Keys(r.e.handles))
		if i := int(n) - 1; i < len(names) {
			return r.e.handles[names[i]]
		}
	}
	return nil
}

var fileHandleIdents = map[string]builtin{
	// $fopen(name|N) is the handle's name; .fname its file, .pos where it
	// is, .eof and .err how the last access went.
	"fopen": func(r *run, fr *frame, c *call) (string, error) {
		if strings.TrimSpace(c.arg(0)) == "0" {
			return itoa(len(r.e.handles)), nil
		}
		h := r.fileHandle(c.arg(0))
		if h == nil {
			return "", nil
		}
		switch c.prop {
		case "fname":
			return h.file, nil
		case "pos":
			return itoa(h.pos), nil
		case "eof":
			return iff(h.eof, "1", "0"), nil
		case "err":
			return iff(h.err, "1", "0"), nil
		}
		return h.name, nil
	},
	// $fread(name) is the next line.
	"fread": func(r *run, fr *frame, c *call) (string, error) {
		h := r.fileHandle(c.arg(0))
		r.e.ferr, r.e.feof = h == nil, false
		if h == nil {
			return "", nil
		}
		if h.pos >= len(h.data) {
			r.e.feof, h.eof = true, true
			return "", nil
		}
		l, _, found := strings.Cut(h.data[h.pos:], "\n")
		h.pos += len(l)
		if found {
			h.pos++
		}
		return strings.TrimSuffix(l, "\r"), nil
	},
	"fgetc": func(r *run, fr *frame, c *call) (string, error) {
		h := r.fileHandle(c.arg(0))
		if h == nil || h.pos >= len(h.data) {
			r.e.feof = h != nil
			return "", nil
		}
		h.pos++
		return itoa(int(h.data[h.pos-1])), nil
	},
	"feof": func(r *run, fr *frame, c *call) (string, error) { return iff(r.e.feof, "1", "0"), nil },
	"ferr": func(r *run, fr *frame, c *call) (string, error) { return iff(r.e.ferr, "1", "0"), nil },
	// $readini(file, [n], section, item): the item's value, evaluated
	// unless n is given, like mIRC.
	"readini": func(r *run, fr *frame, c *call) (string, error) {
		args := c.vals
		noEval := false
		if len(args) == 4 {
			noEval = strings.Contains(args[1], "n")
			args = []string{args[0], args[2], args[3]}
		}
		if len(args) < 3 {
			return "", errorf("$readini: insufficient parameters")
		}
		s, _ := r.readINI(args[0]).section(args[1])
		if s == nil {
			return "", nil
		}
		i := s.item(args[2])
		if i < 0 {
			return "", nil
		}
		if noEval {
			return s.items[i][1], nil
		}
		return r.eval(fr, s.items[i][1])
	},
	// $ini(file, section/N, item/N): a section's or item's name or
	// position; N = 0 counts them.
	"ini": func(r *run, fr *frame, c *call) (string, error) {
		f := r.readINI(c.arg(0))
		names := func(n int, name func(int) string, find func(string) int) string {
			if v, ok := parseNum(c.arg(len(c.vals) - 1)); ok {
				switch i := int(v); {
				case i == 0:
					return itoa(n)
				case i >= 1 && i <= n:
					return name(i - 1)
				}
				return ""
			}
			// A name that isn't there is $null (checked in mIRC 7.86).
			if i := find(c.arg(len(c.vals) - 1)); i >= 0 {
				return itoa(i + 1)
			}
			return ""
		}
		if len(c.vals) < 3 {
			return names(len(f.sections), func(i int) string { return f.sections[i].name },
				func(n string) int { _, i := f.section(n); return i }), nil
		}
		s := f.sections
		var sec *iniSection
		if v, ok := parseNum(c.arg(1)); ok && int(v) >= 1 && int(v) <= len(s) {
			sec = s[int(v)-1]
		} else {
			sec, _ = f.section(c.arg(1))
		}
		if sec == nil {
			return "", nil
		}
		return names(len(sec.items), func(i int) string { return sec.items[i][0] }, sec.item), nil
	},
	// $file(name).size: how long it is; .name, .ext and .path its parts.
	"file": func(r *run, fr *frame, c *call) (string, error) {
		name := c.arg(0)
		data, ok := r.readFile(name)
		if !ok {
			return "", nil
		}
		base := name[strings.LastIndexAny(name, `\/`)+1:]
		switch c.prop {
		case "name":
			return strings.TrimSuffix(base, path.Ext(base)), nil
		case "ext":
			return path.Ext(base), nil
		case "path":
			return name[:len(name)-len(base)], nil
		case "", "size":
			return itoa(len(data)), nil
		}
		return "", errorf("$file().", c.prop, " isn't supported")
	},
	"isdir": func(r *run, fr *frame, c *call) (string, error) { return "$false", nil },
}
