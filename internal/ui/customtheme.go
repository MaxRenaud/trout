package ui

import (
	"cmp"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MaxRenaud/trout/internal/state"
)

// Custom themes: changing what a color number looks like (Colors > Edit
// Color, or a script's /color) changes a copy of the theme in use,
// "Classic*", which trout switches to, so the theme itself stays as it
// was. mIRC has one palette, changed in place; trout has themes, which a
// change in place would quietly spoil. The copy is a theme file,
// classic-custom.toml (Windows can't have a * in a file's name), with
// fork naming the theme it copies; later changes go into it. Back on
// Classic, a change asks whether to edit Classic* or replace it; a
// script, which can't stop to ask, edits Classic*.

// forkIndex is where in themeList the custom copy of theme name is, or -1.
func (a *App) forkIndex(name string) int {
	return slices.IndexFunc(a.themeList(), func(t namedTheme) bool { return t.fork == name })
}

// copyTheme is a copy of t that shares nothing with it.
func copyTheme(t *Theme) *Theme {
	c := *t
	if t.Ext != nil {
		c.Ext = new(*t.Ext)
	}
	return &c
}

// changeTheme makes change to the theme in use's custom copy, making the
// copy if there's none. ask is for the user, who's asked before a copy
// is replaced; a script edits the copy there is.
func (a *App) changeTheme(change func(*Theme), ask bool) {
	cur := a.themeList()[a.themeIdx]
	if cur.fork != "" {
		a.editFork(a.themeIdx, change)
		return
	}
	i := a.forkIndex(cur.name)
	if i < 0 {
		a.newFork(cur, change)
		return
	}
	if !ask {
		a.themeIdx = i
		a.editFork(i, change)
		return
	}
	fork := a.themeList()[i]
	d := message(
		"You already have "+fork.label+", your copy of "+cur.label,
		"with the colors you changed.",
		"",
		"Change "+fork.label+" instead, or replace it with a new",
		"copy of "+cur.label+"? Replacing loses its colors.")
	d.Title = "Custom Theme"
	// Editing it has the focus, so Enter never loses your colors.
	d.Buttons, d.Sel, d.Cancel = []string{"Edit " + fork.label, "Replace", "Cancel"}, 0, 2
	d.OnClose = func(a *App, button int) tea.Cmd {
		switch i := a.forkIndex(cur.name); {
		case i < 0 || button == 2:
		case button == 0:
			a.themeIdx = i
			a.editFork(i, change)
		default:
			a.themeIdx = i
			t := copyTheme(cur.t)
			change(t)
			a.userThemes[i-len(builtinThemes)].t = t
			a.saveFork(i)
		}
		a.openColors()
		return nil
	}
	a.openDialog(d)
}

// newFork makes cur's custom copy, with change, and switches to it.
func (a *App) newFork(cur namedTheme, change func(*Theme)) {
	name := cur.name + "-custom"
	for n := 2; slices.ContainsFunc(a.themeList(), func(t namedTheme) bool { return t.name == name }); n++ {
		name = fmt.Sprintf("%s-custom%d", cur.name, n) // a theme file of that name that isn't a copy
	}
	t := copyTheme(cur.t)
	change(t)
	a.userThemes = append(a.userThemes, namedTheme{name, cur.label + "*", t, cur.name, cmp.Or(cur.base, cur.name)})
	a.themeIdx = len(a.themeList()) - 1
	a.saveFork(a.themeIdx)
}

// editFork makes change to the custom theme at i in themeList.
func (a *App) editFork(i int, change func(*Theme)) {
	u := &a.userThemes[i-len(builtinThemes)]
	t := copyTheme(u.t)
	change(t)
	u.t = t
	a.saveFork(i)
}

// forkOrigin is the theme a custom one copies: the theme itself if it's
// still there, or the built-in one it started from.
func (a *App) forkOrigin(f namedTheme) *Theme {
	if i := slices.IndexFunc(a.themeList(), func(t namedTheme) bool { return t.name == f.fork }); i >= 0 {
		return a.themeList()[i].t
	}
	return a.builtin(f.base)
}

// builtin is the built-in theme called name, or Classic.
func (a *App) builtin(name string) *Theme {
	i := max(0, slices.IndexFunc(builtinThemes, func(t namedTheme) bool { return t.name == name }))
	return builtinThemes[i].t
}

// resetColor puts color n (0-98) of the theme in use back as the theme
// it copies has it, for /color -r. Not on a custom theme, it's that
// already.
func (a *App) resetColor(n int) {
	cur := a.themeList()[a.themeIdx]
	if cur.fork == "" {
		return
	}
	orig := a.forkOrigin(cur)
	a.editFork(a.themeIdx, func(t *Theme) {
		if n < 16 {
			t.Pal[n] = orig.Pal[n]
			return
		}
		if t.Ext != nil {
			t.Ext[n-16] = orig.ext()[n-16]
			if *t.Ext == extendedColors {
				t.Ext = nil
			}
		}
	})
}

// setColor makes color n (0-98) rgb in the theme in use's custom copy.
func (a *App) setColor(n int, rgb RGB, ask bool) {
	a.changeTheme(func(t *Theme) {
		if n < 16 {
			t.Pal[n] = rgb
			return
		}
		if t.Ext == nil {
			t.Ext = new(extendedColors)
		}
		t.Ext[n-16] = rgb
	}, ask)
}

// themesDir is the theme files' folder, next to config.toml.
func (a *App) themesDir() string { return filepath.Join(filepath.Dir(a.cfg.Path), "themes") }

// saveFork writes the custom theme at i in themeList to its theme file:
// its palette whole, and the rest as changes to the built-in theme it
// starts from. Without a config (tests, the demo) it's only in memory.
func (a *App) saveFork(i int) {
	if a.cfg == nil {
		return
	}
	nt := a.themeList()[i]
	root, t := a.builtin(nt.base), nt.t
	var b strings.Builder
	fmt.Fprintf(&b, "# %s: trout's copy of the %s theme, with the colors you changed (Colors > Edit\n", nt.label, nt.fork)
	b.WriteString("# Color, or a script's /color). docs/themes.md says what's in a theme file.\n")
	fmt.Fprintf(&b, "label = %s\nbase = %s\nfork = %s\n\npalette = [\n", tomlString(nt.label), tomlString(nt.base), tomlString(nt.fork))
	for row := range 2 {
		b.WriteString(" ")
		for _, c := range t.Pal[row*8 : row*8+8] {
			fmt.Fprintf(&b, ` "#%06X",`, uint32(c))
		}
		b.WriteString("\n")
	}
	b.WriteString("]\n")
	rf, tf := themeColors(root), themeColors(t)
	var changed []string
	for _, k := range slices.Sorted(maps.Keys(tf)) {
		if *tf[k] != *rf[k] || k == "split-line" && t.ThinSplits && !root.ThinSplits {
			changed = append(changed, fmt.Sprintf("%s = \"#%06X\"\n", k, uint32(*tf[k])))
		}
	}
	if len(changed) > 0 {
		b.WriteString("\n[colors]\n" + strings.Join(changed, ""))
	}
	if t.Ext != nil {
		b.WriteString("\n[extended]\n")
		for j, c := range t.Ext {
			if c != extendedColors[j] {
				fmt.Fprintf(&b, "%d = \"#%06X\"\n", j+16, uint32(c))
			}
		}
	}
	err := os.MkdirAll(a.themesDir(), 0o755)
	if err == nil {
		err = os.WriteFile(filepath.Join(a.themesDir(), nt.name+".toml"), []byte(b.String()), 0o644)
	}
	if err != nil {
		a.addLine(a.home.Status(), state.RoleInfo, "* Can't save "+nt.label+": "+err.Error())
	}
}

// tomlString is s as a TOML basic string.
func tomlString(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// deleteFork deletes the custom theme in use, file and all, and goes
// back to the theme it copied.
func (a *App) deleteFork() {
	cur := a.themeList()[a.themeIdx]
	if cur.fork == "" {
		return
	}
	if a.cfg != nil {
		if err := os.Remove(filepath.Join(a.themesDir(), cur.name+".toml")); err != nil && !os.IsNotExist(err) {
			a.addLine(a.home.Status(), state.RoleInfo, "* Can't delete "+cur.label+": "+err.Error())
			return
		}
	}
	a.userThemes = slices.Delete(a.userThemes, a.themeIdx-len(builtinThemes), a.themeIdx-len(builtinThemes)+1)
	a.themeIdx = a.themeIndex(cur.fork)
}

// SetColor is a script's /color N rgb, or /color -r N: in the theme's
// custom copy, without asking (customtheme.go).
func (h scriptHost) SetColor(n int, rgb uint32, reset bool) {
	if reset {
		h.a.resetColor(n)
		return
	}
	h.a.setColor(n, RGB(rgb), false)
}

// SetKindColor is /color <kind of text> N: the Colors dialog's choice.
func (h scriptHost) SetKindColor(kind string, idx int, reset bool) {
	i := slices.IndexFunc(colorItems, func(it colorItem) bool { return it.key == kind })
	if i < 0 {
		return
	}
	if reset {
		delete(h.a.colors, colorItems[i].role)
		return
	}
	if h.a.colors == nil {
		h.a.colors = map[state.Role]int{}
	}
	h.a.colors[colorItems[i].role] = idx
}
