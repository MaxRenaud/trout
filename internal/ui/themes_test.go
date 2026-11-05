package ui

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MaxRenaud/trout/internal/config"
	"github.com/MaxRenaud/trout/internal/state"
)

func TestThemeFile(t *testing.T) {
	label, th, ps := parseThemeFile(`
label = "Paper"
base = "dark"
palette = ["#F7F3E8", "#222222", "#00007F", "#009300", "#FF0000", "#7F0000", "#9C009C", "#FC7F00",
           "#FFFF00", "#00FC00", "#009393", "#00FFFF", "#0000FC", "#FF00FF", "#7F7F7F", "#D2D2D2"]
[colors]
text-bg = "#F7F3E8"
normal = "222222"
`)
	if len(ps) > 0 || label != "Paper" {
		t.Fatalf("label %q, problems %q", label, ps)
	}
	if th.TextBG != 0xF7F3E8 || th.Roles[state.RoleNormal] != 0x222222 || th.Pal[0] != 0xF7F3E8 {
		t.Fatalf("colors not applied: bg %06x normal %06x pal0 %06x", th.TextBG, th.Roles[state.RoleNormal], th.Pal[0])
	}
	if th.Face != darkTheme.Face {
		t.Fatalf("face %06x, want dark's %06x", th.Face, darkTheme.Face)
	}

	_, th, ps = parseThemeFile(`
palette = ["#000000"]
colour = 1
[colors]
text-bg = "white"
tint = "#123456"
face = "#101010"
`)
	want := []string{"unknown setting colour", "palette has 1 colors, not 16", `text-bg: "white" isn't #RRGGBB`, "unknown color tint"}
	for _, w := range want {
		if !slices.Contains(ps, w) {
			t.Errorf("problems %q, missing %q", ps, w)
		}
	}
	if th == nil || th.Face != 0x101010 || th.TextBG != classicTheme.TextBG {
		t.Fatal("a theme with problems should keep what it got right, on Classic")
	}
	if _, th, ps = parseThemeFile(`base = "neon"`); th != nil || len(ps) != 1 {
		t.Fatalf("an unknown base: %v, %q", th, ps)
	}
}

// TestThemes loads theme files from the config's folder, saves the theme
// by name, and switches themes from F2, View > Themes and the Colors
// dialog's Scheme list.
func TestThemes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	dir := filepath.Join(filepath.Dir(path), "themes")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{
		"paper.toml":  "label = \"Paper\"\n[colors]\ntext-bg = \"#F7F3E8\"\n",
		"Nord.toml":   "label = \"My Nord\"\n",
		"notes.txt":   "not a theme",
		"broken.toml": "[colors\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Settings.Theme = "paper"
	a := New(Options{Config: cfg, Bouncer: config.Bouncer{Address: "irc+insecure://127.0.0.1:6667", User: "max"}})
	a.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	if got := a.themeList()[a.themeIdx]; got.name != "paper" || got.label != "Paper" || a.theme().TextBG != 0xF7F3E8 {
		t.Fatalf("theme %q (%q), bg %06x; want the paper file's", got.name, got.label, a.theme().TextBG)
	}
	var said []string
	for _, l := range a.home.Status().Lines {
		said = append(said, l.Text)
	}
	all := strings.Join(said, "\n")
	if !strings.Contains(all, "Theme file Nord.toml: a theme is called nord already") || !strings.Contains(all, "Theme file broken.toml:") {
		t.Fatalf("theme file problems not said:\n%s", all)
	}

	// F2 is Classic and Dark; from another theme it's Classic.
	key := func(k tea.KeyType) { a.Update(tea.KeyMsg{Type: k}) }
	key(tea.KeyF2)
	if a.themeIdx != 0 {
		t.Fatalf("F2 from paper: theme %d, want Classic", a.themeIdx)
	}
	key(tea.KeyF2)
	if a.themeIdx != 1 {
		t.Fatalf("F2 from Classic: theme %d, want Dark", a.themeIdx)
	}

	// View > Themes lists them all, Dark checked; picking Nord saves "nord".
	view := a.menuBarItems(menuView)
	sub := view[len(view)-1].Sub
	var labels []string
	for _, it := range sub {
		if !it.Sep {
			labels = append(labels, it.Label)
		}
		if it.Check != (it.Label == "Dark") {
			t.Errorf("%s checked %v", it.Label, it.Check)
		}
	}
	if len(labels) != len(builtinThemes)+1 || labels[len(labels)-1] != "Paper" {
		t.Fatalf("View > Themes: %q", labels)
	}
	i := slices.IndexFunc(sub, func(it MenuItem) bool { return it.Label == "Nord" })
	sub[i].Action(a)
	if s := a.settings().Theme; s != "nord" {
		t.Fatalf("settings say theme %q, want nord", s)
	}

	// The Colors dialog: S drops the Scheme list down on Nord; Down twice
	// and Enter pick the theme file's, past Black and the separator.
	a.openColors()
	a.View() // lays the dialog out
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	if len(a.menus) != 1 || a.menus[0].Items[a.menus[0].Sel].Label != "Nord" {
		t.Fatalf("S in Colors: %d menus", len(a.menus))
	}
	key(tea.KeyDown) // Black, then over the separator
	key(tea.KeyDown)
	key(tea.KeyEnter)
	if got := a.themeList()[a.themeIdx].name; got != "paper" {
		t.Fatalf("picked %q from the Scheme list, want paper", got)
	}
	if _, ok := a.dlg.(*colorsBox); !ok || len(a.menus) != 0 {
		t.Fatalf("after picking: dialog %T, %d menus", a.dlg, len(a.menus))
	}
}
