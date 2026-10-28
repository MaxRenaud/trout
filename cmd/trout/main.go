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

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MaxRenaud/trout/internal/ui"
)

func main() {
	opts := ui.Options{}
	flag.BoolVar(&opts.Demo, "demo", false, "show the fake demo world instead of connecting")
	flag.StringVar(&opts.Addr, "addr", cmp.Or(os.Getenv("TROUT_ADDR"), "127.0.0.1:6667"), "soju address (plain-text TCP), or $TROUT_ADDR")
	flag.StringVar(&opts.User, "user", cmp.Or(os.Getenv("TROUT_USER"), "trout"), "soju username, or $TROUT_USER")
	flag.StringVar(&opts.Password, "password", cmp.Or(os.Getenv("TROUT_PASSWORD"), "trout"), "soju password, or $TROUT_PASSWORD")
	flag.Parse()

	// The UI owns the terminal, so debug output goes to a file:
	// TROUT_DEBUG=1 logs every raw IRC line to trout-debug.log.
	if os.Getenv("TROUT_DEBUG") != "" {
		f, err := tea.LogToFile("trout-debug.log", "trout")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		defer f.Close()
		opts.Debug = func(dir, line string) { log.Println(dir, line) }
	}

	if err := ui.Run(opts); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
