// Command trout is an mIRC-inspired IRC client for the terminal. It
// connects through a soju bouncer; `trout --demo` shows a fake world
// instead, for working on the UI.
package main

import (
	"cmp"
	"flag"
	"fmt"
	"log"
	"os"
	"slices"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MaxRenaud/trout/internal/config"
	"github.com/MaxRenaud/trout/internal/irc"
	"github.com/MaxRenaud/trout/internal/ui"
)

func main() {
	opts := ui.Options{}
	defaultPath, err := config.DefaultPath()
	if err != nil {
		fail(err)
	}
	var path, name, addr, user, password string
	flag.BoolVar(&opts.Demo, "demo", false, "show the fake demo world instead of connecting")
	flag.StringVar(&path, "config", cmp.Or(os.Getenv("TROUT_CONFIG"), defaultPath), "config file, or $TROUT_CONFIG")
	flag.StringVar(&name, "bouncer", "", "connect through this bouncer from the config instead of the active one")
	flag.StringVar(&addr, "addr", os.Getenv("TROUT_ADDR"), "bouncer address, ircs://host[:port] or irc+insecure://host:port, or $TROUT_ADDR")
	flag.StringVar(&user, "user", os.Getenv("TROUT_USER"), "bouncer username, or $TROUT_USER")
	flag.StringVar(&password, "password", os.Getenv("TROUT_PASSWORD"), "bouncer password, or $TROUT_PASSWORD")
	flag.Parse()

	// The demo neither reads nor writes the config.
	if !opts.Demo {
		cfg, err := config.Load(path)
		if err != nil {
			fail(err)
		}
		opts.Config = cfg
		// Done before the UI starts, so a password-command can use the
		// terminal.
		if opts.Bouncer, opts.Password, err = pickBouncer(cfg, name, addr, user, password); err != nil {
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
