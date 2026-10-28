package ui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MaxRenaud/trout/internal/config"
	"github.com/MaxRenaud/trout/internal/state"
)

// Options > DCC's lists, like mIRC's DCC options: trusted users, a folder
// per file type, file types to ignore, and the time-out.

var dccIgnoreModes = []string{"ignore", "accept", "off"}

func (a *App) dccTimeout() time.Duration {
	return time.Duration(max(10, a.dccTimeoutS)) * time.Second
}

// dccTrusted reports whether the sender (nick!user@host) is on the
// trusted list, whose offers are taken without asking.
func (a *App) dccTrusted(source string) bool {
	return slices.ContainsFunc(a.book().DCCTrusted, func(m string) bool { return wildMatch(ignoreMask(m), source) })
}

// dccFolder is where a file of that name goes: the first DCC folder whose
// mask matches, else the download folder.
func (a *App) dccFolder(name string) (string, error) {
	for _, f := range a.book().DCCFolders {
		if wildMatch(f.Mask, name) && f.Dir != "" {
			return f.Dir, os.MkdirAll(f.Dir, 0o755)
		}
	}
	return a.downloads()
}

// dccTypeIgnored reports whether a file's type is ignored: it matches
// the list, or with "accept only", doesn't.
func (a *App) dccTypeIgnored(name string) bool {
	if a.dccIgnoreMode == 2 {
		return false
	}
	listed := slices.ContainsFunc(a.book().DCCIgnore, func(m string) bool { return wildMatch(m, name) })
	return listed == (a.dccIgnoreMode == 0)
}

// editList is a list dialog with Add... and Remove for a list of strings
// in the config, going back to back when closed.
func (a *App) editList(title, header, empty, what, label string, list *[]string, back func(*App)) {
	l := &listBox{Title: title, Header: header, Buttons: []string{"Add...", "Remove", "Close"}}
	fill := func() {
		l.Rows, l.Keys = nil, nil
		for i, v := range *list {
			l.Rows = append(l.Rows, v)
			l.Keys = append(l.Keys, strconv.Itoa(i))
		}
		if len(l.Rows) == 0 {
			l.Rows = []string{empty}
		}
	}
	fill()
	l.OnPress = func(a *App, l *listBox, button, key string) tea.Cmd {
		switch button {
		case "Add...":
			a.openDialog(a.prompt(title, what, label, "", func(a *App, v string) {
				*list = append(*list, v)
				a.saveConfig()
				fill()
				a.openDialog(l)
			}))
		case "Remove":
			if i, err := strconv.Atoi(key); err == nil && i < len(*list) {
				*list = slices.Delete(*list, i, i+1)
				a.saveConfig()
				fill()
			}
		}
		return nil
	}
	l.OnClose = back
	a.openDialog(l)
}

func backToDCC(a *App) { a.openOptions(optDCC) }

func (a *App) openDCCTrusted() {
	a.editList("Trusted Users", "Nick or mask", "Nobody. Their files and chats are taken without asking.",
		"Someone whose DCC offers are taken without asking.", "Nick or mask:", &a.book().DCCTrusted, backToDCC)
}

// openDCCFolders is mIRC's DCC Folders: files matching a mask, like
// *.mp3, saved in their own folder.
func (a *App) openDCCFolders() {
	b := a.book()
	l := &listBox{Title: "DCC Folders", Header: "Files like     go to", Buttons: []string{"Add...", "Remove", "Close"}}
	fill := func() {
		l.Rows, l.Keys = nil, nil
		for i, f := range b.DCCFolders {
			l.Rows = append(l.Rows, fmt.Sprintf("%-14s %s", f.Mask, f.Dir))
			l.Keys = append(l.Keys, strconv.Itoa(i))
		}
		if len(l.Rows) == 0 {
			l.Rows = []string{"None. Everything goes to " + a.downloadLabel() + "."}
		}
	}
	fill()
	l.OnPress = func(a *App, l *listBox, button, key string) tea.Cmd {
		switch button {
		case "Add...":
			f := &formBox{Title: "DCC Folder", Buttons: []string{"OK", "Cancel"},
				Fields: []*formField{newField("Files like:", "*.", false), newField("Folder:", "", false)}}
			f.OnOK = func(a *App, v []string) (tea.Cmd, error) {
				mask, dir := strings.TrimSpace(v[0]), strings.TrimSpace(v[1])
				if mask == "" || mask == "*." || dir == "" {
					return nil, errors.New("type a mask, like *.mp3, and a folder")
				}
				dir, err := absDir(dir)
				if err != nil {
					return nil, err
				}
				b.DCCFolders = append(b.DCCFolders, config.DCCFolder{Mask: mask, Dir: dir})
				a.saveConfig()
				fill()
				a.openDialog(l)
				return nil, nil
			}
			f.OnCancel = func(a *App) tea.Cmd { a.openDialog(l); return nil }
			a.openDialog(f)
		case "Remove":
			if i, err := strconv.Atoi(key); err == nil && i < len(b.DCCFolders) {
				b.DCCFolders = slices.Delete(b.DCCFolders, i, i+1)
				a.saveConfig()
				fill()
			}
		}
		return nil
	}
	l.OnClose = backToDCC
	a.openDialog(l)
}

// absDir is a folder typed by someone: ~ expanded, made absolute, and
// made if it isn't there.
func absDir(dir string) (string, error) {
	if home, err := os.UserHomeDir(); err == nil {
		if rest, ok := strings.CutPrefix(dir, "~"); ok {
			dir = filepath.Join(home, rest)
		}
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return "", fmt.Errorf("can't use that folder: %v", err)
	}
	return abs, nil
}

// openDCCIgnore is mIRC's DCC Ignore: file types to turn down, or the
// only ones to take.
func (a *App) openDCCIgnore() {
	b := a.book()
	f := &formBox{Title: "DCC File Types", Buttons: []string{"OK", "Cancel"},
		Lines: []string{"File offers of these types, separated by spaces:"},
		Fields: []*formField{
			newRadio("mode", "Ignore these", a.dccIgnoreMode == 0),
			newRadio("mode", "Accept only these", a.dccIgnoreMode == 1),
			newRadio("mode", "Accept every type", a.dccIgnoreMode == 2),
			newField("Types:", strings.Join(b.DCCIgnore, " "), false),
		}}
	f.OnButton = map[string]func(*App) tea.Cmd{"Defaults": func(a *App) tea.Cmd {
		f.Fields[3] = newField("Types:", strings.Join(config.DefaultDCCIgnore, " "), false)
		return nil
	}}
	f.Buttons = []string{"OK", "Defaults", "Cancel"}
	f.OnOK = func(a *App, v []string) (tea.Cmd, error) {
		a.dccIgnoreMode = max(0, slices.Index(v[:3], "1"))
		b.DCCIgnore = strings.Fields(v[3])
		if b.DCCIgnore == nil {
			b.DCCIgnore = []string{}
		}
		a.saveConfig()
		a.openOptions(optDCC)
		return nil, nil
	}
	f.OnCancel = func(a *App) tea.Cmd { a.openOptions(optDCC); return nil }
	a.openDialog(f)
}

func (a *App) dccIgnoreLabel() string {
	switch a.dccIgnoreMode {
	case 1:
		return "File types: accept only " + strings.Join(a.book().DCCIgnore, " ")
	case 2:
		return "File types: accept every type"
	}
	return "File types: ignore " + strings.Join(a.book().DCCIgnore, " ")
}

func (a *App) openDCCTimeout() {
	a.openDialog(a.prompt("DCC Time-out", "How long a Get or Chat waits to connect, or for data.", "Seconds:",
		strconv.Itoa(a.dccTimeoutS), func(a *App, v string) {
			if s, err := strconv.Atoi(v); err == nil && s >= 10 {
				a.dccTimeoutS = s
			}
			a.openOptions(optDCC)
		}))
	a.dlg.(*formBox).OnCancel = func(a *App) tea.Cmd { a.openOptions(optDCC); return nil }
}

// dccIgnoredLine says a file offer was turned down for its type.
func (a *App) dccIgnoredLine(n *state.Network, file, nick string, t time.Time) {
	a.addLineAt(n.Status(), state.RoleInfo, "* Ignored "+file+" from "+nick+": Options > DCC > File types", t)
}
