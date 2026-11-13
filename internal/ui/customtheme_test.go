package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MaxRenaud/trout/internal/config"
	"github.com/MaxRenaud/trout/internal/state"
)

// TestCustomTheme walks changing a color: Edit Color makes "Classic*", a
// theme file that's there after a restart; back on Classic, a change
// asks to edit Classic* or replace it; a script's /color edits it without
// asking, colors 16-98 too; Delete Theme goes back to Classic.
func TestCustomTheme(t *testing.T) {
	cfg, err := config.Load(filepath.Join(t.TempDir(), "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	open := func() *App {
		a := New(Options{Config: cfg, Bouncer: config.Bouncer{Address: "irc+insecure://127.0.0.1:6667", User: "max"}})
		a.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
		return a
	}
	a := open()
	cur := func() namedTheme { return a.themeList()[a.themeIdx] }
	editColor := func(n, hex string) {
		t.Helper()
		a.openColors()
		a.dlg.key(a, "e")
		f, ok := a.dlg.(*formBox)
		if !ok {
			t.Fatalf("E opened %T", a.dlg)
		}
		f.Fields[0].Edit.Buf, f.Fields[1].Edit.Buf = []rune(n), []rune(hex)
		f.focus = len(f.Fields)
		f.key(a, "enter")
	}
	file := filepath.Join(filepath.Dir(cfg.Path), "themes", "classic-custom.toml")

	editColor("4", "#123456")
	if c := cur(); c.name != "classic-custom" || c.label != "Classic*" || c.fork != "classic" || c.t.Pal[4] != 0x123456 {
		t.Fatalf("after Edit Color: %q %q fork %q, color 4 %06X", c.name, c.label, c.fork, uint32(c.t.Pal[4]))
	}
	if classicTheme.Pal[4] != 0xFF0000 {
		t.Fatal("Classic itself changed")
	}
	if _, ok := a.dlg.(*colorsBox); !ok {
		t.Fatalf("not back in Colors: %T", a.dlg)
	}
	if data, err := os.ReadFile(file); err != nil || !strings.Contains(string(data), `"#123456"`) ||
		!strings.Contains(string(data), `fork = "classic"`) {
		t.Fatalf("the theme file: %v\n%s", err, data)
	}
	editColor("4", "#654321") // on Classic*: no question
	if a.dlg.(*colorsBox) == nil || cur().t.Pal[4] != 0x654321 {
		t.Fatal("a second change didn't go into Classic*")
	}
	a.dlg = nil
	a.saveSettings()

	// After a restart it's there, in use, still a copy of Classic.
	a = open()
	if c := cur(); c.name != "classic-custom" || c.fork != "classic" || c.t.Pal[4] != 0x654321 {
		t.Fatalf("after a restart: %q fork %q color 4 %06X", c.name, c.fork, uint32(c.t.Pal[4]))
	}

	// Back on Classic, a change asks; Edit Classic* has the focus.
	a.themeIdx = 0
	editColor("2", "#ABCDEF")
	d, ok := a.dlg.(*msgBox)
	if !ok || d.Title != "Custom Theme" || d.Buttons[d.Sel] != "Edit Classic*" {
		t.Fatalf("no question: %#v", a.dlg)
	}
	d.key(a, "enter")
	if c := cur(); c.name != "classic-custom" || c.t.Pal[2] != 0xABCDEF || c.t.Pal[4] != 0x654321 {
		t.Fatalf("Edit Classic*: %q, 2 %06X, 4 %06X", c.name, uint32(c.t.Pal[2]), uint32(c.t.Pal[4]))
	}
	a.themeIdx = 0
	editColor("2", "#111111")
	d = a.dlg.(*msgBox)
	d.Sel = 1 // Replace: a new copy of Classic
	d.key(a, "enter")
	if c := cur(); c.t.Pal[2] != 0x111111 || c.t.Pal[4] != classicTheme.Pal[4] {
		t.Fatalf("Replace: 2 %06X, 4 %06X", uint32(c.t.Pal[2]), uint32(c.t.Pal[4]))
	}
	a.dlg = nil

	// A script's /color: on Classic, into Classic* without asking; an
	// extended color; -r; a kind of text.
	a.themeIdx = 0
	typed := func(text string) {
		t.Helper()
		w := a.active
		a.edit(w).Buf = []rune(text)
		a.submit(w)
	}
	typed("//color 20 $rgb(1,2,3)")
	if c := cur(); a.dlg != nil || c.name != "classic-custom" || c.t.Ext == nil || c.t.Ext[4] != 0x010203 {
		t.Fatalf("/color 20: dialog %T, theme %q", a.dlg, c.name)
	}
	if data, _ := os.ReadFile(file); !strings.Contains(string(data), "[extended]\n20 = \"#010203\"") {
		t.Fatalf("extended color not saved:\n%s", data)
	}
	typed("//color -r 20 | color 3 $rgb(255,0,128) | color action 4")
	if c := cur(); c.t.Ext != nil || c.t.Pal[3] != 0xFF0080 || a.colors[state.RoleAction] != 4 {
		t.Fatalf("-r, 3 and action: ext %v, 3 %06X, action %v", c.t.Ext, uint32(c.t.Pal[3]), a.colors[state.RoleAction])
	}
	typed("//color -r action")
	if _, ok := a.colors[state.RoleAction]; ok {
		t.Fatal("/color -r action kept the choice")
	}

	// Delete Theme: asked, Cancel focused; then back on Classic.
	a.openColors()
	if labels := a.dlg.(*colorsBox).buttonLabels(a); labels[len(labels)-1] != "Delete Theme" {
		t.Fatalf("buttons on Classic*: %q", labels)
	}
	a.dlg.key(a, "d")
	d = a.dlg.(*msgBox)
	if d.Buttons[d.Sel] != "Cancel" {
		t.Fatalf("Delete's question: %#v", d)
	}
	d.Sel = 0
	d.key(a, "enter")
	if c := cur(); c.name != "classic" || a.forkIndex("classic") >= 0 {
		t.Fatalf("after Delete: on %q", c.name)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("the theme file is still there: %v", err)
	}
}
