package termkeys

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func env(t *testing.T, vars map[string]string) Env {
	home := t.TempDir()
	return Env{Getenv: func(k string) string { return vars[k] }, Home: home, GOOS: "darwin"}
}

func TestITerm(t *testing.T) {
	e := env(t, map[string]string{"TERM_PROGRAM": "iTerm.app", "LC_TERMINAL": "iTerm2", "ITERM_PROFILE": "Default"})
	sends := 0.0
	e.ITerm = func() ([]byte, error) {
		return json.Marshal([]map[string]any{{"Name": "tmux", "Option Key Sends": 2}, {"Name": "Default", "Option Key Sends": sends}})
	}
	term := Detect(e)
	if term.Name != "iTerm2" || term.OK || term.Fix != FixITermProfile || term.Profile != "Default" {
		t.Fatalf("Option types characters: %+v", term)
	}
	sends = 2
	if !Detect(e).OK {
		t.Fatal("Esc+ is Alt")
	}

	// The dynamic profile: written once per parent, kept for others.
	changed, err := InstallITermProfile(e.Home, "Default")
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	if changed, _ := InstallITermProfile(e.Home, "Default"); changed {
		t.Fatal("rewrote an unchanged profile")
	}
	InstallITermProfile(e.Home, "Work")
	b, _ := os.ReadFile(filepath.Join(e.Home, "Library/Application Support/iTerm2/DynamicProfiles/trout.json"))
	var doc struct{ Profiles []map[string]any }
	json.Unmarshal(b, &doc)
	if len(doc.Profiles) != 2 || doc.Profiles[0]["Dynamic Profile Parent Name"] != "Default" ||
		doc.Profiles[0]["Option Key Sends"] != 2.0 || doc.Profiles[0]["Name"] != "trout (Default)" {
		t.Fatalf("dynamic profiles: %s", b)
	}
	if SetITermProfile("trout (Default)") != "\x1b]1337;SetProfile=trout (Default)\x07" {
		t.Fatal("escape sequence")
	}

	// In tmux or over SSH, it's steps to follow.
	e.Getenv = func(k string) string {
		return map[string]string{"LC_TERMINAL": "iTerm2", "ITERM_PROFILE": "Default", "TMUX": "/tmp/tmux"}[k]
	}
	if term := Detect(e); term.Fix != FixManual || len(term.Steps) == 0 {
		t.Fatalf("iTerm2 in tmux: %+v", term)
	}
}

func TestConfigLine(t *testing.T) {
	e := env(t, map[string]string{"TERM_PROGRAM": "ghostty"})
	term := Detect(e)
	if term.Name != "Ghostty" || term.OK || term.Fix != FixConfigLine || term.Config != filepath.Join(e.Home, ".config/ghostty/config") {
		t.Fatalf("ghostty: %+v", term)
	}
	os.MkdirAll(filepath.Dir(term.Config), 0o755)
	os.WriteFile(term.Config, []byte("font-size = 14\nmacos-option-as-alt-ish = x"), 0o644)
	if err := AddLine(term); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(term.Config)
	if !strings.HasPrefix(string(b), "font-size = 14\nmacos-option-as-alt-ish = x\n") || !strings.HasSuffix(string(b), "macos-option-as-alt = left\n") {
		t.Fatalf("config after: %q", b)
	}
	if !Detect(e).OK {
		t.Fatal("the added line should count")
	}

	k := env(t, map[string]string{"KITTY_WINDOW_ID": "1"})
	term = Detect(k)
	if term.Name != "kitty" || term.OK || !strings.HasSuffix(term.Config, ".config/kitty/kitty.conf") {
		t.Fatalf("kitty: %+v", term)
	}
	os.MkdirAll(filepath.Dir(term.Config), 0o755)
	os.WriteFile(term.Config, []byte("# macos_option_as_alt yes\nmacos_option_as_alt\tboth\n"), 0o644)
	if !Detect(k).OK {
		t.Fatal("kitty with both")
	}
}

func TestManual(t *testing.T) {
	e := env(t, map[string]string{"TERM_PROGRAM": "Apple_Terminal"})
	e.AppleTerm = func(key string) (string, error) {
		switch key {
		case "Default Window Settings":
			return "Basic", nil
		case "Window Settings:Basic:useOptionAsMetaKey":
			return "", errors.New("missing")
		}
		return "", errors.New("?")
	}
	if term := Detect(e); term.Name != "Terminal" || term.OK || term.Fix != FixManual {
		t.Fatalf("Terminal.app: %+v", term)
	}

	a := env(t, map[string]string{"TERM": "alacritty"})
	path := filepath.Join(a.Home, ".config/alacritty/alacritty.toml")
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.WriteFile(path, []byte("[window]\noption_as_alt = \"OnlyLeft\"\n"), 0o644)
	if !Detect(a).OK {
		t.Fatal("alacritty with OnlyLeft")
	}

	// Off a Mac, or in a terminal it doesn't know, there's nothing to ask.
	if !Detect(env(t, map[string]string{"TERM_PROGRAM": "WezTerm"})).OK {
		t.Fatal("WezTerm's left Option is Alt already")
	}
	l := env(t, map[string]string{"TERM_PROGRAM": "ghostty"})
	l.GOOS = "linux"
	if !Detect(l).OK {
		t.Fatal("Linux has no Option key")
	}
}
