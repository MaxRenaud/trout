package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trout", "config.toml")
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Settings != DefaultSettings() || len(c.Bouncers) != 0 {
		t.Fatalf("a missing config loaded as %+v, want defaults and no bouncers", c)
	}
	c.Use(Bouncer{Name: "bnc.example.net", Address: "ircs://bnc.example.net", User: "max", Pin: "SHA256:abc"})
	c.Settings.Theme, c.Settings.Toolbar = "dark", false
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	if err := c.SetPassword("bnc.example.net", `pa"ss`); err != nil {
		t.Fatal(err)
	}

	c2, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	b, ok := c2.ActiveBouncer()
	if !ok || b != c.Bouncers[0] || c2.Settings != c.Settings {
		t.Fatalf("reloaded %+v, want %+v", c2, c)
	}
	if pw, err := c2.Password(b); err != nil || pw != `pa"ss` {
		t.Fatalf("Password = %q, %v", pw, err)
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(c.credentialsPath())
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("credentials file mode %o, want 600", perm)
		}
		os.Chmod(c.credentialsPath(), 0o644)
		if _, err := c2.Password(b); err == nil || !strings.Contains(err.Error(), "chmod 600") {
			t.Errorf("a world-readable credentials file gave %v, want a chmod hint", err)
		}
	}
}

func TestPasswordCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses /bin/sh")
	}
	c := &Config{Path: filepath.Join(t.TempDir(), "config.toml")}
	pw, err := c.Password(Bouncer{Name: "x", PasswordCommand: "printf 'hunter2\\nsecond line\\n'"})
	if err != nil || pw != "hunter2" {
		t.Fatalf("Password = %q, %v; want the command's first line", pw, err)
	}
	c.SetPassword("x", "stored")
	if pw, _ := c.Password(Bouncer{Name: "x", PasswordCommand: "echo nope"}); pw != "stored" {
		t.Errorf("Password = %q; a stored password should win over the command", pw)
	}
}

func TestUnknownKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(path, []byte("[settings]\ntoolbr = false\n"), 0o600)
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "settings.toolbr") {
		t.Errorf("Load = %v, want an error naming settings.toolbr", err)
	}
}

// TestScriptOpts: the Scripts Editor's options are saved, and left out
// of config.toml while they're mIRC's defaults.
func TestScriptOpts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	c, _ := Load(path)
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); strings.Contains(string(data), "script-options") {
		t.Errorf("defaults saved:\n%s", data)
	}
	c.ScriptOpts.RawOff, c.ScriptOpts.IdentWarning = true, true
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	c2, err := Load(path)
	if err != nil || c2.ScriptOpts != c.ScriptOpts {
		t.Errorf("reloaded %+v, %v", c2.ScriptOpts, err)
	}
}

// TestWriteFileSymlink saves through a symlink, as a dotfiles repo makes:
// the link stays, its target gets the new data, and no temp file is left.
func TestWriteFileSymlink(t *testing.T) {
	dir := t.TempDir()
	real, link := filepath.Join(dir, "real.ini"), filepath.Join(dir, "sub", "vars.ini")
	os.WriteFile(real, []byte("old"), 0o600)
	os.Mkdir(filepath.Dir(link), 0o700)
	if err := os.Symlink(real, link); err != nil {
		t.Skip(err) // Windows without the privilege
	}
	if err := WriteFile(link, []byte("new")); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the symlink was replaced: %v %v", fi.Mode(), err)
	}
	if b, _ := os.ReadFile(real); string(b) != "new" {
		t.Errorf("target = %q", b)
	}
	for _, d := range []string{dir, filepath.Dir(link)} {
		if es, _ := os.ReadDir(d); len(es) != map[string]int{dir: 2, filepath.Dir(link): 1}[d] {
			t.Errorf("%s holds %v", d, es)
		}
	}
}
