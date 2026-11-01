package ui

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MaxRenaud/trout/internal/state"
	"github.com/MaxRenaud/trout/internal/update"
)

// trout checking for and putting in a newer trout, like mIRC's Help >
// Check for Updates and Options > Other's "Check for updates" (once a
// day at most, when trout starts). How it updates depends on how it was
// installed (update.Detect): Homebrew upgrades the cask, a downloaded
// archive has its files replaced in place, and go install gets told the
// command. mIRC's checker downloads an installer; trout puts the new
// version in and asks you to start it again.

type (
	updateCheckedMsg struct {
		rel    update.Release
		err    error
		manual bool // Help > Check for Updates: say so when there's nothing new
	}
	updateStepMsg struct{ line string }
	updateDoneMsg struct {
		version string
		err     error
	}
)

// checkEvery is how often trout checks on its own.
const checkEvery = 24 * time.Hour

const goInstallCmd = "go install github.com/MaxRenaud/trout/cmd/trout@latest"

// updateStamp is the file saying when trout last checked on its own.
func (a *App) updateStamp() string {
	return filepath.Join(filepath.Dir(a.cfg.Path), "cache", "update-checked")
}

// autoCheckUpdate checks for a newer trout when it starts, if Options >
// Other says to and it hasn't in the last day. A downloaded trout also
// tidies up after its last update here.
func (a *App) autoCheckUpdate() tea.Cmd {
	in := a.opts.Install
	if a.opts.Demo || a.cfg == nil || in.How == update.FromSource {
		return nil
	}
	if in.How == update.Archive {
		update.Cleanup(in.Dir) // the old files, now that their trout has quit
	}
	if !a.checkUpdates {
		return nil
	}
	stamp := a.updateStamp()
	if b, err := os.ReadFile(stamp); err == nil {
		if t, err := time.Parse(time.RFC3339, strings.TrimSpace(string(b))); err == nil && time.Since(t) < checkEvery {
			return nil
		}
	}
	os.MkdirAll(filepath.Dir(stamp), 0o700)
	os.WriteFile(stamp, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o600)
	return a.fetchLatest(false)
}

func (a *App) fetchLatest(manual bool) tea.Cmd {
	latest := func(ctx context.Context) (update.Release, error) {
		return update.Latest(ctx, http.DefaultClient, "trout/"+a.opts.Version)
	}
	if a.hooks != nil && a.hooks.latest != nil {
		latest = a.hooks.latest
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		rel, err := latest(ctx)
		return updateCheckedMsg{rel, err, manual}
	}
}

// checkForUpdates is Help > Check for Updates.
func (a *App) checkForUpdates() tea.Cmd {
	switch {
	case a.opts.Demo:
		a.openDialog(updateBox("The demo doesn't check for updates: it never", "goes on the network."))
		return nil
	case a.opts.Install.How == update.FromSource:
		a.openDialog(updateBox("This trout ("+a.opts.Version+") was built from source,",
			"so there's no release to compare it with.", "Update it with git pull."))
		return nil
	case a.updating:
		a.openDialog(updateBox("trout is updating itself already."))
		return nil
	}
	a.addLine(a.home.Status(), state.RoleInfo, "* Checking for a newer trout...")
	return a.fetchLatest(true)
}

func updateBox(lines ...string) *msgBox {
	d := message(lines...)
	d.Title = "Check for Updates"
	return d
}

func (a *App) updateChecked(m updateCheckedMsg) tea.Cmd {
	cur := a.opts.Version
	switch {
	case m.err != nil:
		if m.manual {
			a.openDialog(updateBox("Couldn't check for updates:", m.err.Error()))
		}
	case !update.Newer(m.rel.Version, cur):
		if m.manual {
			a.openDialog(updateBox("You have the latest trout, " + update.Plain(cur) + "."))
		}
	case a.updatedTo != "" && update.Compare(a.updatedTo, m.rel.Version) >= 0:
		if m.manual {
			a.openDialog(updateBox("trout "+update.Plain(a.updatedTo)+" is in already:", "quit and start trout again to use it."))
		}
	case m.manual:
		a.openDialog(a.updateOffer(m.rel))
	default:
		rel := m.rel
		a.whenFree(func(a *App) tea.Cmd {
			if !a.updating && a.updatedTo == "" {
				a.openDialog(a.updateOffer(rel))
			}
			return nil
		})
	}
	return nil
}

// updateOffer says what's new and offers to put it in, the way this
// trout was installed.
func (a *App) updateOffer(rel update.Release) *msgBox {
	in := a.opts.Install
	d := updateBox("trout "+update.Plain(rel.Version)+" is out; you have "+update.Plain(a.opts.Version)+".", "")
	notes := func(a *App) tea.Cmd { return openURL(rel.URL) }
	switch {
	case in.How == update.Homebrew:
		d.Lines = append(d.Lines, "trout updates it with Homebrew:", "brew upgrade --cask maxrenaud/tap/trout",
			"then you start trout again to use it.")
		d.Buttons = []string{"Update", "What's New", "Not Now"}
		d.OnClose = func(a *App, b int) tea.Cmd {
			return pick(b, func(a *App) tea.Cmd { return a.startUpdate(rel) }, notes)(a)
		}
	case in.How == update.Archive && in.Writable:
		name := update.AssetName(rel.Version, runtime.GOOS, runtime.GOARCH)
		size := ""
		if n := rel.Sizes[name]; n > 0 {
			size = " (" + strconv.Itoa(int(n>>20)) + " MB)"
		}
		d.Lines = append(d.Lines, "trout downloads it from GitHub"+size+" and puts it in", in.Dir+",",
			"then you start trout again to use it.")
		d.Buttons = []string{"Update", "What's New", "Not Now"}
		d.OnClose = func(a *App, b int) tea.Cmd {
			return pick(b, func(a *App) tea.Cmd { return a.startUpdate(rel) }, notes)(a)
		}
	case in.How == update.Archive:
		d.Lines = append(d.Lines, "trout can't write to "+in.Dir+",", "so download it from its page:", rel.URL)
		d.Buttons = []string{"Open Page", "Not Now"}
		d.OnClose = func(a *App, b int) tea.Cmd { return pick(b, notes)(a) }
	default: // go install
		d.Lines = append(d.Lines, "Update it with:", goInstallCmd)
		d.Buttons = []string{"What's New", "OK"}
		d.OnClose = func(a *App, b int) tea.Cmd { return pick(b, notes)(a) }
	}
	d.Cancel = len(d.Buttons) - 1
	return d
}

// pick runs the action for button b, if it has one.
func pick(b int, actions ...func(*App) tea.Cmd) func(*App) tea.Cmd {
	return func(a *App) tea.Cmd {
		if b >= 0 && b < len(actions) {
			return actions[b](a)
		}
		return nil
	}
}

// startUpdate puts rel in, in the background, saying how it goes in the
// status window.
func (a *App) startUpdate(rel update.Release) tea.Cmd {
	if a.updating {
		return nil
	}
	a.updating = true
	in := a.opts.Install
	say := func(s string) { a.send(updateStepMsg{s}) }
	run := func(ctx context.Context) error {
		if in.How == update.Homebrew {
			return update.UpdateBrew(ctx, in.Brew, say)
		}
		return update.Apply(ctx, http.DefaultClient, rel, in.Dir, runtime.GOOS, runtime.GOARCH, say)
	}
	if a.hooks != nil && a.hooks.update != nil {
		run = func(ctx context.Context) error { return a.hooks.update(ctx, rel, say) }
	}
	a.addLine(a.home.Status(), state.RoleInfo, "* Update: getting trout "+update.Plain(rel.Version))
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		return updateDoneMsg{rel.Version, run(ctx)}
	}
}

func (a *App) updateStep(m updateStepMsg) {
	a.addLine(a.home.Status(), state.RoleInfo, "* Update: "+m.line)
}

func (a *App) updateDone(m updateDoneMsg) {
	a.updating = false
	if m.err != nil {
		a.addLine(a.home.Status(), state.RoleQuit, "* Update failed: "+m.err.Error())
		a.whenFree(func(a *App) tea.Cmd {
			a.openDialog(updateBox("trout couldn't update itself:", m.err.Error(), "",
				"It's as it was. Releases: https://github.com/"+update.Repo+"/releases"))
			return nil
		})
		return
	}
	a.updatedTo = m.version
	v := update.Plain(m.version)
	a.addLine(a.home.Status(), state.RoleInfo, "* Update: trout "+v+" is in; quit and start trout again to use it")
	a.whenFree(func(a *App) tea.Cmd {
		a.openDialog(updateBox("trout "+v+" is in.", "Quit and start trout again to use it."))
		return nil
	})
}
