package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MaxRenaud/trout/internal/config"
	"github.com/MaxRenaud/trout/internal/local"
)

// TestThisComputer picks This Computer on the first-run Connect dialog:
// trout starts its own soju (the real one, from dev/build-soju), saves the
// bouncer and its password, and connects; starting again reconnects to it.
func TestThisComputer(t *testing.T) {
	if _, _, err := local.Find(); err != nil {
		t.Skip(err)
	}
	dir, err := os.MkdirTemp("/tmp", "trout-ui-") // short: soju's admin socket path
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	cfg, err := config.Load(filepath.Join(dir, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	a := New(Options{Config: cfg})
	a.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	a.Init()
	f, ok := a.dlg.(*formBox)
	if !ok || !strings.Contains(strings.Join(f.Buttons, ","), "This Computer") {
		t.Fatalf("first run: %T", a.dlg)
	}
	cmd := f.OnButton["This Computer"](a)
	t.Cleanup(func() { a.local.Stop() })
	up := runUntil[localUpMsg](t, cmd)
	if up.err != nil {
		t.Fatal(up.err)
	}
	_, cmd = a.Update(up)
	_, _ = a.Update(runUntil[ircUpMsg](t, cmd))
	if a.control == nil || !strings.Contains(lastLine(a.home.Status()), "Connected to the bouncer") &&
		!strings.Contains(strings.Join(lineTexts(a.home.Status()), "\n"), "Connected to the bouncer") {
		t.Fatalf("not connected: %q", lineTexts(a.home.Status()))
	}
	b := cfg.Find(localName)
	if b == nil || !b.Local || b.Address != "" || cfg.Active != localName {
		t.Fatalf("saved %+v", b)
	}
	pw, _ := cfg.StoredPassword(localName)
	if len(pw) != 24 || pw != a.password {
		t.Fatalf("password %q", pw)
	}

	// trout starts again from config.toml: no dialog, it starts soju (or
	// finds it running) and connects.
	a.control.Close()
	a.local.Stop()
	cfg2, _ := config.Load(cfg.Path)
	bnc, _ := cfg2.ActiveBouncer()
	pw2, _ := cfg2.Password(bnc)
	a2 := New(Options{Config: cfg2, Bouncer: bnc, Password: pw2})
	a2.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	t.Cleanup(func() { a2.local.Stop() })
	up = runUntil[localUpMsg](t, a2.Init())
	if a2.dlg != nil || up.err != nil {
		t.Fatalf("second start: %T %v", a2.dlg, up.err)
	}
	_, cmd = a2.Update(up)
	_, _ = a2.Update(runUntil[ircUpMsg](t, cmd))
	if a2.control == nil {
		t.Fatal("not connected the second time")
	}
}
