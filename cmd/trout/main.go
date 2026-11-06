// Command trout is an mIRC-inspired IRC client for the terminal. It
// connects through a soju bouncer; `trout --demo` shows a fake world
// instead, for working on the UI.
package main

import (
	"cmp"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"runtime/debug"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MaxRenaud/trout/internal/config"
	"github.com/MaxRenaud/trout/internal/irc"
	"github.com/MaxRenaud/trout/internal/ui"
	"github.com/MaxRenaud/trout/internal/update"
)

// version is set by the release build (goreleaser's -X main.version).
var version string

// moduleVersion is the version go install …@v0.1.0 put in the build
// info, or "".
func moduleVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		return info.Main.Version
	}
	return ""
}

// appVersion is trout's version: the release's, the module version for
// `go install …@v0.1.0`, or "dev" and the commit for a build from source.
func appVersion() string {
	if version != "" {
		return version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	if v := info.Main.Version; v != "" && v != "(devel)" {
		return v
	}
	var v strings.Builder
	v.WriteString("dev")
	for _, s := range info.Settings {
		switch {
		case s.Key == "vcs.revision" && len(s.Value) >= 7:
			v.WriteString(" " + s.Value[:7])
		case s.Key == "vcs.modified" && s.Value == "true":
			v.WriteString("+changes")
		}
	}
	return v.String()
}

func main() {
	opts := ui.Options{Version: appVersion(), Install: update.Detect(version != "", moduleVersion())}
	defaultPath, err := config.DefaultPath()
	if err != nil {
		fail(err)
	}
	f, err := parseFlags(os.Args[1:], os.Getenv, defaultPath)
	switch {
	case errors.Is(err, flag.ErrHelp):
		return // the usage is printed
	case err != nil:
		os.Exit(2) // the flag package said what's wrong
	}
	opts.Demo = f.demo
	if f.version {
		fmt.Println("trout", opts.Version)
		return
	}

	// The demo neither reads nor writes the config.
	if !opts.Demo {
		cfg, err := config.Load(f.path)
		if err != nil {
			fail(err)
		}
		opts.Config = cfg
		// Done before the UI starts, so a password-command can use the
		// terminal.
		if opts.Bouncer, opts.Password, err = pickBouncer(cfg, f.name, f.addr, f.user, f.password); err != nil {
			fail(err)
		}
	}

	// The UI owns the terminal, so debug output goes to a file:
	// TROUT_DEBUG=1 logs every raw IRC line to trout-debug.log.
	if os.Getenv("TROUT_DEBUG") != "" {
		f, err := tea.LogToFile("trout-debug.log", "trout")
		if err != nil {
			fail(err)
		}
		defer f.Close()
		opts.Debug = func(dir, line string) { log.Println(dir, line) }
	}

	if err := ui.Run(opts); err != nil {
		fail(err)
	}
}

// flags are the command line's, each defaulting to its environment
// variable: a flag beats the environment, which beats the config.
type flags struct {
	demo, version                    bool
	path, name, addr, user, password string
}

func parseFlags(args []string, getenv func(string) string, defaultPath string) (flags, error) {
	var f flags
	fs := flag.NewFlagSet("trout", flag.ContinueOnError)
	fs.BoolVar(&f.demo, "demo", false, "show the fake demo world instead of connecting")
	fs.BoolVar(&f.version, "version", false, "print trout's version and exit")
	fs.StringVar(&f.path, "config", cmp.Or(getenv("TROUT_CONFIG"), defaultPath), "config file, or $TROUT_CONFIG")
	fs.StringVar(&f.name, "bouncer", "", "connect through this bouncer from the config instead of the active one")
	fs.StringVar(&f.addr, "addr", getenv("TROUT_ADDR"), "bouncer address, ircs://host[:port] or irc+insecure://host:port, or $TROUT_ADDR")
	fs.StringVar(&f.user, "user", getenv("TROUT_USER"), "bouncer username, or $TROUT_USER")
	fs.StringVar(&f.password, "password", getenv("TROUT_PASSWORD"), "bouncer password, or $TROUT_PASSWORD")
	err := fs.Parse(args)
	return f, err
}

// pickBouncer is the bouncer to connect to and its password: the one named
// by --bouncer, else the config's active one, with flags overriding its
// address, user and password. An --addr that no saved bouncer has is used
// for this run only.
func pickBouncer(cfg *config.Config, name, addr, user, password string) (config.Bouncer, string, error) {
	var b config.Bouncer
	switch {
	case name != "":
		saved := cfg.Find(name)
		if saved == nil {
			return b, "", fmt.Errorf("%s has no bouncer called %q", cfg.Path, name)
		}
		b = *saved
		b.Address = cmp.Or(addr, b.Address) // a new address for it, this run
	case addr != "":
		// A saved bouncer at that address keeps its pin and password.
		hp, _, err := irc.ParseAddress(addr)
		if err != nil {
			return b, "", err
		}
		if i := slices.IndexFunc(cfg.Bouncers, func(s config.Bouncer) bool {
			shp, _, _ := irc.ParseAddress(s.Address)
			return shp == hp && (user == "" || s.User == user)
		}); i >= 0 {
			b = cfg.Bouncers[i]
		}
		b.Address = addr
	default:
		b, _ = cfg.ActiveBouncer()
	}
	b.User = cmp.Or(user, b.User)
	if b.Address != "" {
		if _, _, err := irc.ParseAddress(b.Address); err != nil {
			return b, "", fmt.Errorf("bouncer %s: %w", b.Name, err)
		}
	}
	if password == "" && b.Name != "" {
		var err error
		if password, err = cfg.Password(b); err != nil {
			return b, "", err
		}
	}
	return b, password, nil
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "trout:", err)
	os.Exit(1)
}
