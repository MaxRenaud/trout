// Package termkeys finds out which terminal trout runs in and whether its
// Option key sends Alt, which trout's mIRC-style shortcuts need on a Mac,
// and fixes that where it safely can. WeeChat, irssi and Emacs ask Mac
// users for the same terminal setting.
package termkeys

import (
	"bufio"
	"bytes"
	"cmp"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
)

// Fix is what trout can do about a terminal whose Option key types
// characters.
type Fix int

const (
	FixManual       Fix = iota // only say how
	FixITermProfile            // an iTerm2 dynamic profile, used while trout runs
	FixConfigLine              // add Line to Config
)

// Terminal is the terminal trout runs in, as far as it can tell.
type Terminal struct {
	Name    string // "iTerm2", "Terminal", "Ghostty", "kitty", "Alacritty", "VS Code"; "" if unknown
	OK      bool   // Option sends Alt already (or there's no Option key to worry about)
	Fix     Fix
	Profile string   // iTerm2: the profile in use
	Config  string   // FixConfigLine: the file
	Line    string   // FixConfigLine: the line
	After   string   // FixConfigLine: what to do once it's added
	Steps   []string // FixManual: how, by hand
}

// Env is where Detect looks: the environment, the home directory, the
// OS, and how to read iTerm2's and Terminal's settings (tests replace
// these).
type Env struct {
	Getenv    func(string) string
	Home      string
	GOOS      string
	ITerm     func() ([]byte, error)           // iTerm2's profiles as JSON
	AppleTerm func(key string) (string, error) // a Terminal.app setting
}

// SystemEnv is the real environment.
func SystemEnv() Env {
	home, _ := os.UserHomeDir()
	return Env{Getenv: os.Getenv, Home: home, GOOS: runtime.GOOS,
		ITerm: func() ([]byte, error) {
			return exec.Command("plutil", "-extract", "New Bookmarks", "json", "-o", "-",
				filepath.Join(home, "Library/Preferences/com.googlecode.iterm2.plist")).Output()
		},
		AppleTerm: func(key string) (string, error) {
			out, err := exec.Command("/usr/libexec/PlistBuddy", "-c", "Print :"+key,
				filepath.Join(home, "Library/Preferences/com.apple.Terminal.plist")).Output()
			return strings.TrimSpace(string(out)), err
		}}
}

// Detect finds the terminal and checks its Option key. Off a Mac, or in
// a terminal it doesn't know, it reports OK: there's nothing to ask.
func Detect(e Env) Terminal {
	get := e.Getenv
	tmux := get("TMUX") != ""
	remote := get("SSH_CONNECTION") != "" || get("SSH_TTY") != ""
	switch {
	case get("LC_TERMINAL") == "iTerm2" || get("TERM_PROGRAM") == "iTerm.app":
		t := Terminal{Name: "iTerm2", Profile: get("ITERM_PROFILE"), Steps: []string{
			"iTerm2 > Settings > Profiles > Keys > General:",
			"set Left Option key to Esc+.",
		}}
		if remote || tmux || t.Profile == "" || e.GOOS != "darwin" {
			// Its settings are on another computer, or tmux won't pass the
			// profile switch along: say how.
			return t
		}
		t.Fix = FixITermProfile
		t.OK = itermAlt(e, t.Profile)
		return t
	case e.GOOS != "darwin" || remote:
		return Terminal{OK: true}
	case get("GHOSTTY_RESOURCES_DIR") != "" || get("TERM_PROGRAM") == "ghostty":
		return ghostty(e)
	case get("KITTY_WINDOW_ID") != "" || get("TERM") == "xterm-kitty":
		return kitty(e)
	case get("ALACRITTY_WINDOW_ID") != "" || get("TERM") == "alacritty":
		return alacritty(e)
	case get("TERM_PROGRAM") == "Apple_Terminal":
		return appleTerminal(e)
	case get("TERM_PROGRAM") == "vscode":
		return vscode(e)
	}
	// WezTerm's left Option is Alt unless you change it; others, who knows.
	return Terminal{OK: true}
}

// itermAlt reports whether an iTerm2 profile's Option keys send Esc+.
func itermAlt(e Env, profile string) bool {
	if e.ITerm == nil {
		return false
	}
	out, err := e.ITerm()
	if err != nil {
		return false
	}
	var profiles []map[string]any
	if json.Unmarshal(out, &profiles) != nil {
		return false
	}
	for _, p := range profiles {
		if p["Name"] == profile {
			return p["Option Key Sends"] == 2.0 || p["Right Option Key Sends"] == 2.0
		}
	}
	return false
}

// ITermProfile is the name of the dynamic profile trout uses on top of
// parent.
func ITermProfile(parent string) string { return "trout (" + parent + ")" }

func itermDir(home string) string {
	return filepath.Join(home, "Library/Application Support/iTerm2/DynamicProfiles")
}

// InstallITermProfile writes (or updates) trout's iTerm2 dynamic profile
// for parent: a copy of it whose left Option key sends Esc+. It reports
// whether the file changed, since iTerm2 takes a moment to load a new one.
func InstallITermProfile(home, parent string) (changed bool, err error) {
	path := filepath.Join(itermDir(home), "trout.json")
	type profile struct {
		Name   string `json:"Name"`
		Guid   string `json:"Guid"`
		Parent string `json:"Dynamic Profile Parent Name"`
		Option int    `json:"Option Key Sends"`
	}
	var doc struct {
		Profiles []profile `json:"Profiles"`
	}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &doc)
	}
	sum := sha1.Sum([]byte(parent))
	p := profile{Name: ITermProfile(parent), Guid: "trout-option-alt-" + hex.EncodeToString(sum[:6]), Parent: parent, Option: 2}
	if slices.Contains(doc.Profiles, p) {
		return false, nil
	}
	doc.Profiles = slices.DeleteFunc(doc.Profiles, func(o profile) bool { return o.Guid == p.Guid })
	doc.Profiles = append(doc.Profiles, p)
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return false, err
	}
	if err := os.MkdirAll(itermDir(home), 0o755); err != nil {
		return false, err
	}
	return true, os.WriteFile(path, append(b, '\n'), 0o644)
}

// SetITermProfile is the escape sequence that switches the iTerm2 tab
// to a profile.
func SetITermProfile(name string) string { return "\x1b]1337;SetProfile=" + name + "\x07" }

// AddLine appends t's Line to its Config, making the file if needed.
func AddLine(t Terminal) error {
	if t.Fix != FixConfigLine {
		return errors.New("nothing to add")
	}
	if err := os.MkdirAll(filepath.Dir(t.Config), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(t.Config, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if info, err := f.Stat(); err == nil && info.Size() > 0 {
		if b, err := os.ReadFile(t.Config); err == nil && !bytes.HasSuffix(b, []byte("\n")) {
			f.WriteString("\n")
		}
	}
	_, err = fmt.Fprintf(f, "\n# Added by trout: the left Option key sends Alt.\n%s\n", t.Line)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// setting finds key's value in a "key = value" or "key value" config
// file, the last one winning; ok is false if it isn't there.
func setting(path, key string) (val string, ok bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		rest, found := strings.CutPrefix(strings.TrimSpace(s.Text()), key)
		if !found || rest == "" || !strings.ContainsRune(" \t=", rune(rest[0])) {
			continue // another key, or a longer one that starts the same
		}
		rest = strings.TrimSpace(rest)
		rest = strings.TrimSpace(strings.TrimPrefix(rest, "="))
		val, ok = strings.Trim(rest, `"`), true
	}
	return val, ok
}

func ghostty(e Env) Terminal {
	var files []string
	xdg := cmp.Or(e.Getenv("XDG_CONFIG_HOME"), filepath.Join(e.Home, ".config"))
	for _, dir := range []string{filepath.Join(xdg, "ghostty"), filepath.Join(e.Home, "Library/Application Support/com.mitchellh.ghostty")} {
		for _, name := range []string{"config", "config.ghostty"} {
			files = append(files, filepath.Join(dir, name))
		}
	}
	t := Terminal{Name: "Ghostty", Fix: FixConfigLine, Config: files[0], Line: "macos-option-as-alt = left",
		After: "Reload Ghostty's config (Cmd+Shift+,) or open a new window."}
	for _, f := range files {
		if _, err := os.Stat(f); err == nil && t.Config == files[0] {
			t.Config = f // add to the first one that's there
		}
		if v, ok := setting(f, "macos-option-as-alt"); ok {
			t.OK = v == "true" || v == "left" || v == "right"
		}
	}
	return t
}

func kitty(e Env) Terminal {
	dir := e.Getenv("KITTY_CONFIG_DIRECTORY")
	if dir == "" {
		dir = filepath.Join(cmp.Or(e.Getenv("XDG_CONFIG_HOME"), filepath.Join(e.Home, ".config")), "kitty")
	}
	t := Terminal{Name: "kitty", Fix: FixConfigLine, Config: filepath.Join(dir, "kitty.conf"),
		Line: "macos_option_as_alt left", After: "Quit and reopen kitty, which reads this setting when it starts."}
	if v, ok := setting(t.Config, "macos_option_as_alt"); ok {
		t.OK = v != "no"
	}
	return t
}

func alacritty(e Env) Terminal {
	t := Terminal{Name: "Alacritty", Steps: []string{
		"Add to ~/.config/alacritty/alacritty.toml:",
		"    [window]",
		`    option_as_alt = "OnlyLeft"`,
	}}
	path := filepath.Join(cmp.Or(e.Getenv("XDG_CONFIG_HOME"), filepath.Join(e.Home, ".config")), "alacritty/alacritty.toml")
	var cfg struct {
		Window struct {
			OptionAsAlt string `toml:"option_as_alt"`
		} `toml:"window"`
	}
	if _, err := toml.DecodeFile(path, &cfg); err == nil {
		t.OK = cfg.Window.OptionAsAlt != "" && cfg.Window.OptionAsAlt != "None"
	}
	return t
}

func appleTerminal(e Env) Terminal {
	t := Terminal{Name: "Terminal", Steps: []string{
		"Terminal > Settings > Profiles > (your profile) > Keyboard:",
		"turn on Use Option as Meta key.",
	}}
	if e.AppleTerm == nil {
		return t
	}
	profile, err := e.AppleTerm("Default Window Settings")
	if err != nil || profile == "" {
		return t
	}
	v, err := e.AppleTerm("Window Settings:" + profile + ":useOptionAsMetaKey")
	t.OK = err == nil && v == "true"
	return t
}

func vscode(e Env) Terminal {
	t := Terminal{Name: "VS Code", Steps: []string{
		"In VS Code's settings, turn on",
		"Terminal > Integrated: Mac Option Is Meta.",
	}}
	b, err := os.ReadFile(filepath.Join(e.Home, "Library/Application Support/Code/User/settings.json"))
	if err == nil {
		compact := strings.Join(strings.Fields(string(b)), "")
		t.OK = strings.Contains(compact, `"terminal.integrated.macOptionIsMeta":true`)
	}
	return t
}
