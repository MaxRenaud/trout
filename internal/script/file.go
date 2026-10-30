package script

import "strings"

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
