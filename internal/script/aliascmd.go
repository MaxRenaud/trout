package script

import (
	"path/filepath"
	"strconv"
	"strings"
)

// cmdAlias is /alias [-l] [filename] <name> [commands]: it adds, replaces
// or (with no commands) removes a one-line alias in an aliases file, like
// mIRC's (checked):
//   - with no filename, in the first aliases file loaded (aliases.ini, made
//     if there isn't one); a filename that isn't a loaded aliases file
//     does nothing;
//   - a / in front of the name is dropped, names match in any case, and
//     the commands are kept as they come out of evaluation;
//   - an alias of several lines ({ }) isn't touched, nor one in a remote
//     script: the new one goes in the aliases file;
//   - -l is taken and makes no difference (the alias can still be called
//     from anywhere);
//   - removing one that isn't there is fine; no name at all is
//     "insufficient parameters".
//
// mIRC keeps the change in memory until /saveini or exit; trout writes the
// file straight away, as it does vars.ini.
func (r *run) cmdAlias(fr *frame, rest string) error {
	line, err := r.eval(fr, rest)
	if err != nil {
		return err
	}
	words := strings.Fields(line)
	if len(words) > 0 && strings.EqualFold(words[0], "-l") {
		words = words[1:]
	}
	var target *file
	if len(words) > 0 && isAliasFileName(words[0]) {
		name := words[0]
		words = words[1:]
		target = r.e.aliasFile(name)
		if target == nil {
			return nil // not a loaded aliases file: nothing, like mIRC
		}
	}
	if len(words) == 0 || strings.TrimLeft(words[0], "/") == "" {
		return errorf("/alias: insufficient parameters")
	}
	name, commands := strings.TrimLeft(words[0], "/"), strings.Join(words[1:], " ")
	if target == nil {
		target = r.e.aliasFile("")
	}
	src, kind := "", "aliases"
	if target != nil {
		src = target.src
	}
	lines, ini := aliasLines(src)
	i, multi := -1, false
	for j, l := range lines {
		first, after, _ := strings.Cut(strings.TrimSpace(l), " ")
		if strings.EqualFold(strings.TrimLeft(first, "/"), name) {
			if a := strings.TrimSpace(after); a == "{" || strings.HasPrefix(a, "{ ") {
				multi = true
			}
			i = j
			break
		}
	}
	switch {
	case multi:
		return nil // several lines: left as they are, like mIRC's
	case commands == "" && i < 0:
		return nil
	case commands == "":
		lines = append(lines[:i], lines[i+1:]...)
	case i >= 0:
		lines[i] = name + " " + commands
	default:
		lines = append(lines, name+" "+commands)
	}
	fname := "aliases.ini"
	if target != nil {
		fname = target.name
	} else {
		ini = true
	}
	out := writeAliasLines(lines, ini)
	r.e.LoadAs(fname, out, kind) // in effect now; problems show when it's next loaded
	// Written as its own action, not through r.writeFile (whose writes go
	// out after the run's commands), so it's there for the /load below.
	r.actions = append(r.actions, Action{File: &File{Name: fname, Data: out}})
	if target == nil { // a new aliases.ini: loaded from now on
		r.actions = append(r.actions, Action{Command: "/.load -a " + fname, Network: fr.network, Window: fr.window})
	}
	return nil
}

// isAliasFileName reports whether /alias's first word is a file's name,
// not an alias's: one with a script file's extension.
func isAliasFileName(s string) bool {
	switch strings.ToLower(filepath.Ext(s)) {
	case ".ini", ".mrc", ".txt":
		return true
	}
	return false
}

// aliasFile is the loaded aliases file called name (as loaded, or its
// base name), or with name "", the first one loaded.
func (e *Engine) aliasFile(name string) *file {
	for _, f := range e.scripts {
		if f.kind != "aliases" {
			continue
		}
		if name == "" || strings.EqualFold(f.name, name) || strings.EqualFold(filepath.Base(strings.ReplaceAll(f.name, `\`, "/")), name) {
			return f
		}
	}
	return nil
}

// aliasLines is an aliases file's definitions, one a line, and whether it
// was in mIRC's ini form ([aliases] n0=...).
func aliasLines(src string) (lines []string, ini bool) {
	for l := range strings.Lines(src) {
		l = strings.TrimRight(l, "\r\n")
		if strings.HasPrefix(strings.TrimSpace(l), "[") {
			ini = true
			continue
		}
		if ini {
			if k, v, ok := strings.Cut(l, "="); ok && strings.HasPrefix(strings.ToLower(k), "n") {
				lines = append(lines, v)
			}
			continue
		}
		lines = append(lines, l)
	}
	return lines, ini
}

func writeAliasLines(lines []string, ini bool) string {
	var b strings.Builder
	if ini {
		b.WriteString("[aliases]\r\n")
	}
	for i, l := range lines {
		if ini {
			b.WriteString("n" + strconv.Itoa(i) + "=")
		}
		b.WriteString(l + "\r\n")
	}
	return b.String()
}
