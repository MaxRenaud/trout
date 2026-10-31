package script

import (
	"regexp"
	"strings"
)

// Things a script can use that trout won't run. A script using one isn't
// loaded at all, so it never half-runs.
var (
	dlls, com, dialogs, sockets = "DLLs aren't supported", "COM objects aren't supported",
		"dialogs aren't supported", "sockets aren't supported"
	fatalCommands = map[string]string{
		"dll": dlls, "dllcall": dlls, "comopen": com, "comclose": com, "comreg": com,
		"dialog": dialogs, "did": dialogs, "run": "running programs isn't supported",
		"sockopen": sockets, "socklisten": sockets, "sockudp": sockets,
	}
	fatalIdents = map[string]string{
		"dll": dlls, "dllcall": dlls, "com": com, "comcall": com,
		"dialog": dialogs, "did": dialogs, "sock": sockets,
	}
	// An identifier at the start of a word, or as an argument.
	// unsupported are mIRC's own commands for what trout doesn't have, like
	// custom windows: a script using them loads (the rest of it works),
	// with a Problem, and the client says so when it gets to them, rather
	// than sending them to the server.
	windows     = "custom windows aren't supported"
	unsupported = map[string]string{
		"window": windows, "aline": windows, "cline": windows, "dline": windows, "iline": windows,
		"rline": windows, "sline": windows, "renwin": windows, "loadbuf": windows, "filter": windows,
		"drawtext": windows, "drawline": windows, "drawrect": windows, "drawdot": windows,
		"drawfill": windows, "drawpic": windows, "drawsave": windows, "drawcopy": windows,
		"drawreplace": windows, "drawscroll": windows, "drawsize": windows,
		"titlebar": "the title bar is trout's", "toolbar": "the toolbar is trout's",
		"background": "backgrounds aren't supported", "font": "fonts are the terminal's",
		"editbox": "the editbox is yours", "showmirc": "trout's window is the terminal's",
		"tray": "there's no tray", "clipboard": "the clipboard isn't supported",
	}
	identRe = regexp.MustCompile(`(?:^|[\s(,\[#!=])\$\$?([A-Za-z][A-Za-z0-9]*|\?)`)
)

// check looks through a parsed script for what trout can't run: fatal
// things (DLLs, dialogs, sockets) and mIRC identifiers trout doesn't
// support yet, which stop the script with an error when it gets to them.
func (e *Engine) check(f *file) []Problem {
	var problems []Problem
	seen := map[string]bool{}
	ident := func(n int, text string) {
		for _, m := range identRe.FindAllStringSubmatch(text, -1) {
			name := strings.ToLower(m[1])
			if seen[name] {
				continue
			}
			seen[name] = true
			switch {
			case name == "?":
				problems = append(problems, Problem{Line: n, Text: "$? asks for input, which isn't supported"})
			case fatalIdents[name] != "":
				problems = append(problems, Problem{Line: n, Fatal: true, Text: "$" + name + ": " + fatalIdents[name]})
			case builtins[name] == nil && mircIdents[name] && e.alias(f, name) == nil:
				problems = append(problems, Problem{Line: n, Text: "$" + m[1] + " isn't supported yet"})
			}
		}
	}
	var walk func([]stmt)
	walk = func(body []stmt) {
		for _, s := range body {
			switch s := s.(type) {
			case *cmdStmt:
				cmd := strings.ToLower(strings.TrimLeft(firstWord(s.text), "/.!"))
				if what := fatalCommands[cmd]; what != "" && !seen["/"+cmd] {
					seen["/"+cmd] = true
					problems = append(problems, Problem{Line: s.n, Fatal: true, Text: "/" + cmd + ": " + what})
				}
				if what := unsupported[cmd]; what != "" && !seen["/"+cmd] {
					seen["/"+cmd] = true
					problems = append(problems, Problem{Line: s.n, Text: "/" + cmd + ": " + what})
				}
				ident(s.n, s.text)
			case *ifStmt:
				for _, b := range s.branches {
					ident(s.n, b.cond)
					walk(b.body)
				}
			case *whileStmt:
				ident(s.n, s.cond)
				walk(s.body)
			}
		}
	}
	for _, a := range f.aliases {
		walk(a.body)
	}
	for _, ev := range f.events {
		walk(ev.body)
	}
	for _, pop := range f.popups {
		for _, it := range pop.items {
			ident(0, it.label)
			walk(it.body)
		}
	}
	return problems
}

// Unsupported says why trout doesn't run one of mIRC's own commands, like
// /aline (custom windows), or "" if it's not one of those.
func Unsupported(command string) string {
	return unsupported[strings.ToLower(strings.TrimLeft(command, "/.!"))]
}
