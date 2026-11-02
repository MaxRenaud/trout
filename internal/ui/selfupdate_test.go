package ui

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MaxRenaud/trout/internal/config"
	"github.com/MaxRenaud/trout/internal/update"
)

// updateApp is trout 0.3.5, installed the way how says, with a temp
// config and a GitHub whose latest release is latest; updates it puts in
// are recorded (or fail with fail).
func updateApp(t *testing.T, how update.How, latest string, fail error) (*App, *[]string) {
	t.Helper()
	a, _ := fakeSoju(t)
	cfg, err := config.Load(filepath.Join(t.TempDir(), "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	a.cfg = cfg
	a.applySettings(cfg.Settings)
	a.opts.Version = "0.3.5"
	a.opts.Install = update.Install{How: how, Dir: t.TempDir(), Writable: true, Brew: "/opt/homebrew/bin/brew"}
	var mu sync.Mutex
	var put []string
	a.hooks = &installHooks{
		latest: func(context.Context) (update.Release, error) {
			if latest == "" {
				return update.Release{}, errors.New("no network")
			}
			name := update.AssetName(latest, runtime.GOOS, runtime.GOARCH) // this computer's, as the offer looks it up
			return update.Release{Version: latest, URL: "https://github.com/MaxRenaud/trout/releases/tag/" + latest,
				Sizes: map[string]int64{name: 45 << 20}}, nil
		},
		update: func(_ context.Context, rel update.Release, progress func(string)) error {
			progress("downloading")
			mu.Lock()
			defer mu.Unlock()
			put = append(put, rel.Version)
			return fail
		},
	}
	return a, &put
}

// run runs cmd and delivers what it and its sends lead to.
func run(a *App, cmd tea.Cmd) {
	var mu sync.Mutex
	var sent []tea.Msg
	a.send = func(m tea.Msg) { mu.Lock(); sent = append(sent, m); mu.Unlock() }
	for cmd != nil {
		msg := cmd()
		mu.Lock()
		steps := sent
		sent = nil
		mu.Unlock()
		for _, s := range steps {
			a.Update(s)
		}
		_, cmd = a.Update(msg)
	}
}

func updateDialog(t *testing.T, a *App) *msgBox {
	t.Helper()
	d, ok := a.dlg.(*msgBox)
	if !ok || d.Title != "Check for Updates" {
		t.Fatalf("the dialog is %T %+v, not Check for Updates", a.dlg, a.dlg)
	}
	return d
}

// TestAutoUpdateCheck: when trout starts it checks once a day, unless
// Options > Other says not to, and offers a newer trout; Update puts it
// in and says to start trout again.
func TestAutoUpdateCheck(t *testing.T) {
	a, put := updateApp(t, update.Archive, "v0.4.0", nil)
	if !a.checkUpdates {
		t.Fatal("Check for updates is off by default")
	}
	run(a, a.autoCheckUpdate())
	d := updateDialog(t, a)
	text := strings.Join(d.Lines, "\n")
	if !strings.Contains(text, "trout 0.4.0 is out; you have 0.3.5.") || !strings.Contains(text, "(45 MB)") ||
		!strings.Contains(text, a.opts.Install.Dir) || !slices.Equal(d.Buttons, []string{"Update", "What's New", "Not Now"}) {
		t.Fatalf("offer %q %q", d.Lines, d.Buttons)
	}
	run(a, d.close(a, 0)) // Update
	if !slices.Equal(*put, []string{"v0.4.0"}) || a.updating || a.updatedTo != "v0.4.0" {
		t.Fatalf("put in %q, updating %v, updated to %q", *put, a.updating, a.updatedTo)
	}
	lines := strings.Join(lineTexts(a.home.Status()), "\n")
	for _, want := range []string{"* Update: getting trout 0.4.0", "* Update: downloading", "* Update: trout 0.4.0 is in; quit and start trout again"} {
		if !strings.Contains(lines, want) {
			t.Errorf("the status window has no %q:\n%s", want, lines)
		}
	}
	if d := updateDialog(t, a); !strings.Contains(d.Lines[0], "0.4.0 is in") {
		t.Fatalf("after updating: %q", d.Lines)
	}
	a.dlg = nil

	if a.autoCheckUpdate() != nil {
		t.Error("checked twice in a day")
	}
	run(a, a.checkForUpdates()) // Help > Check for Updates: always checks
	if d := updateDialog(t, a); !strings.Contains(d.Lines[0], "0.4.0 is in already") {
		t.Errorf("checking after updating: %q", d.Lines)
	}

	a, _ = updateApp(t, update.Archive, "v0.4.0", nil)
	a.checkUpdates = false
	if a.autoCheckUpdate() != nil {
		t.Error("checked with Check for updates off")
	}
	a, _ = updateApp(t, update.FromSource, "v0.4.0", nil)
	if a.autoCheckUpdate() != nil {
		t.Error("checked a build from source")
	}
	a, _ = updateApp(t, update.Archive, "", nil)
	run(a, a.autoCheckUpdate())
	if a.dlg != nil {
		t.Errorf("a failed check on its own opened %T", a.dlg)
	}
	a, _ = updateApp(t, update.Archive, "v0.3.5", nil)
	run(a, a.autoCheckUpdate())
	if a.dlg != nil {
		t.Errorf("no newer trout, but it opened %T", a.dlg)
	}
}

// TestUpdateOffers: what Check for Updates offers depends on how trout
// was installed.
func TestUpdateOffers(t *testing.T) {
	for _, c := range []struct {
		what    string
		how     update.How
		latest  string
		lock    bool // trout's folder isn't writable
		want    string
		buttons []string
	}{
		{"homebrew", update.Homebrew, "v0.4.0", false, "brew upgrade --cask maxrenaud/tap/trout", []string{"Update", "What's New", "Not Now"}},
		{"read-only archive", update.Archive, "v0.4.0", true, "can't write to", []string{"Open Page", "Not Now"}},
		{"go install", update.GoInstall, "v0.4.0", false, goInstallCmd, []string{"What's New", "OK"}},
		{"from source", update.FromSource, "v0.4.0", false, "built from source", []string{"OK"}},
		{"latest", update.Archive, "v0.3.5", false, "You have the latest trout, 0.3.5.", []string{"OK"}},
		{"offline", update.Archive, "", false, "Couldn't check for updates", []string{"OK"}},
	} {
		t.Run(c.what, func(t *testing.T) {
			a, put := updateApp(t, c.how, c.latest, nil)
			a.opts.Install.Writable = !c.lock
			run(a, a.checkForUpdates())
			d := updateDialog(t, a)
			if !strings.Contains(strings.Join(d.Lines, "\n"), c.want) || !slices.Equal(d.Buttons, c.buttons) {
				t.Fatalf("lines %q buttons %q, want %q and %q", d.Lines, d.Buttons, c.want, c.buttons)
			}
			if c.how == update.Homebrew {
				run(a, d.close(a, 0))
				if !slices.Equal(*put, []string{"v0.4.0"}) {
					t.Errorf("Homebrew's Update put in %q", *put)
				}
			}
		})
	}
}

// TestUpdateFails: a failed update says why, leaves trout as it was, and
// can be tried again.
func TestUpdateFails(t *testing.T) {
	a, put := updateApp(t, update.Archive, "v0.4.0", errors.New("its checksum is wrong"))
	run(a, a.checkForUpdates())
	run(a, updateDialog(t, a).close(a, 0))
	if a.updating || a.updatedTo != "" || len(*put) != 1 {
		t.Fatalf("updating %v, updated to %q, tried %q", a.updating, a.updatedTo, *put)
	}
	if d := updateDialog(t, a); !strings.Contains(strings.Join(d.Lines, "\n"), "its checksum is wrong") {
		t.Fatalf("failure says %q", d.Lines)
	}
	if !slices.Contains(lineTexts(a.home.Status()), "* Update failed: its checksum is wrong") {
		t.Errorf("status: %q", lineTexts(a.home.Status()))
	}
	a.dlg = nil
	run(a, a.checkForUpdates())
	run(a, updateDialog(t, a).close(a, 0))
	if len(*put) != 2 {
		t.Errorf("couldn't try again: %q", *put)
	}
}

// TestUpdateMenus: Help > Check for Updates and Options > Other's
// checkbox, saved in config.toml, like mIRC's.
func TestUpdateMenus(t *testing.T) {
	a, _ := updateApp(t, update.Archive, "v0.4.0", nil)
	if !slices.ContainsFunc(a.menuBarItems(menuHelp), func(m MenuItem) bool { return m.Label == "Check for Updates..." }) {
		t.Error("the Help menu has no Check for Updates")
	}
	a.openOptions(optOther)
	o := a.dlg.(*optionsBox)
	i := slices.IndexFunc(o.pages[optOther].Items, func(it optItem) bool { return it.Label == "Check for updates" })
	if i < 0 {
		t.Fatal("Options > Other has no Check for updates")
	}
	*o.pages[optOther].Items[i].Bool = false
	if a.settings().CheckUpdates {
		t.Error("turning it off isn't saved")
	}
	if !config.DefaultSettings().CheckUpdates {
		t.Error("it's off by default; mIRC's is on")
	}
}
