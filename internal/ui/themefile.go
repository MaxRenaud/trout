package ui

import (
	"cmp"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/MaxRenaud/trout/internal/state"
)

// Theme files are .toml files in the themes folder next to config.toml,
// read at startup; the file's name is the theme's. One starts from a
// built-in theme and changes what it lists (docs/themes.md):
//
//	label = "Paper"
//	base = "classic"
//	palette = ["#FFFFFF", "#000000", ...] # mIRC colors 00-15, all 16
//	[colors]
//	text-bg = "#F7F3E8"
//	normal = "#222222"

type themeFile struct {
	Label   string            `toml:"label"`
	Base    string            `toml:"base"`
	Palette []string          `toml:"palette"`
	Colors  map[string]string `toml:"colors"`
}

// themeColors are the [colors] keys, each a color of t.
func themeColors(t *Theme) map[string]*RGB {
	return map[string]*RGB{
		"normal": &t.Roles[state.RoleNormal], "own": &t.Roles[state.RoleOwn],
		"action": &t.Roles[state.RoleAction], "event": &t.Roles[state.RoleEvent],
		"quit": &t.Roles[state.RoleQuit], "notice": &t.Roles[state.RoleNotice],
		"info": &t.Roles[state.RoleInfo], "highlight": &t.Roles[state.RoleHighlight],
		"timestamp": &t.Roles[state.RoleTimestamp],
		"face":      &t.Face, "face-text": &t.FaceText, "light": &t.Light, "shadow": &t.Shadow,
		"hover": &t.Hover, "pressed": &t.Pressed, "button-down": &t.ButtonDown,
		"title-a": &t.TitleA, "title-b": &t.TitleB, "title-text": &t.TitleText,
		"menu-sel": &t.MenuSel, "menu-sel-text": &t.MenuSelText, "disabled": &t.Disabled,
		"text-bg": &t.TextBG, "text-fg": &t.TextFG, "sel-bg": &t.SelBG, "sel-fg": &t.SelFG,
		"shade-bg": &t.ShadeBG, "marker": &t.Marker, "lines": &t.Lines,
		"track-fg": &t.TrackFG, "track-bg": &t.TrackBG,
		"act-event": &t.ActEvent, "act-message": &t.ActMsg, "act-highlight": &t.ActHi,
		"warn": &t.Warn,
	}
}

// parseHex reads "#RRGGBB" (the # is optional).
func parseHex(s string) (RGB, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(s) != 6 {
		return 0, false
	}
	v, err := strconv.ParseUint(s, 16, 32)
	return RGB(v), err == nil
}

// parseThemeFile makes a theme from a theme file's text; problems are
// what's wrong with it, by line where TOML says.
func parseThemeFile(data string) (label string, t *Theme, problems []string) {
	var f themeFile
	md, err := toml.Decode(data, &f)
	if err != nil {
		return "", nil, []string{err.Error()}
	}
	for _, k := range md.Undecoded() {
		problems = append(problems, "unknown setting "+k.String())
	}
	base := cmp.Or(f.Base, "classic")
	i := slices.IndexFunc(builtinThemes, func(b namedTheme) bool { return b.name == base })
	if i < 0 {
		return "", nil, append(problems, fmt.Sprintf("base %q isn't a built-in theme", f.Base))
	}
	th := *builtinThemes[i].t
	if f.Palette != nil {
		if len(f.Palette) != 16 {
			problems = append(problems, fmt.Sprintf("palette has %d colors, not 16", len(f.Palette)))
		} else {
			for n, s := range f.Palette {
				if c, ok := parseHex(s); ok {
					th.Pal[n] = c
				} else {
					problems = append(problems, fmt.Sprintf("palette color %d: %q isn't #RRGGBB", n, s))
				}
			}
		}
	}
	fields := themeColors(&th)
	for _, k := range slices.Sorted(maps.Keys(f.Colors)) {
		p, ok := fields[k]
		if !ok {
			problems = append(problems, "unknown color "+k)
			continue
		}
		c, ok := parseHex(f.Colors[k])
		if !ok {
			problems = append(problems, fmt.Sprintf("%s: %q isn't #RRGGBB", k, f.Colors[k]))
			continue
		}
		*p = c
	}
	return f.Label, &th, problems
}

// loadThemeFiles reads the themes folder: a theme file with problems is
// still used, with the colors it got right, and the problems are said in
// the status window.
func (a *App) loadThemeFiles() (problems []string) {
	if a.cfg == nil {
		return nil
	}
	dir := filepath.Join(filepath.Dir(a.cfg.Path), "themes")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil // no folder, no theme files
	}
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".toml")
		if !ok || e.IsDir() {
			continue
		}
		name = strings.ToLower(name)
		if slices.ContainsFunc(a.themeList(), func(t namedTheme) bool { return t.name == name }) {
			problems = append(problems, "Theme file "+e.Name()+": a theme is called "+name+" already")
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			problems = append(problems, "Theme file "+e.Name()+": "+err.Error())
			continue
		}
		label, t, ps := parseThemeFile(string(data))
		for _, p := range ps {
			problems = append(problems, "Theme file "+e.Name()+": "+p)
		}
		if t != nil {
			a.userThemes = append(a.userThemes, namedTheme{name, cmp.Or(label, name), t})
		}
	}
	return problems
}
