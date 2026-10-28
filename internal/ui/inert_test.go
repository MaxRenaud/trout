package ui

import (
	"testing"

	"github.com/MaxRenaud/trout/internal/state"
)

// TestNothingInert walks every menubar menu, submenu and popup in real
// mode, and every toolbar button, and fails on any that does nothing:
// the mock-up's leftovers. Disabled items and separators are fine.
func TestNothingInert(t *testing.T) {
	a, n, _ := realApp(t)
	ch := n.Find(state.KChannel, "#go")
	q := a.query(n, "alice", "")
	var walk func(where string, items []MenuItem)
	walk = func(where string, items []MenuItem) {
		for _, it := range items {
			switch {
			case it.Sep || it.Disabled:
			case len(it.Sub) > 0:
				walk(where+" > "+it.Label, it.Sub)
			case it.Action == nil:
				t.Errorf("%s > %s does nothing", where, it.Label)
			}
		}
	}
	for _, w := range []*state.Window{n.Status(), ch, q} {
		a.activate(w)
		for i, name := range menuBarNames {
			walk(name+" (in "+w.Name+")", a.menuBarItems(i))
		}
		a.windowMenu(w, 0, 0)
		walk("popup of "+w.Name, a.menus[0].Items)
		a.closeMenus()
	}
	a.activate(ch)
	a.nickMenu(ch, "bob", 0, 0)
	walk("nick popup", a.menus[0].Items)
	a.closeMenus()
	for _, b := range toolButtons {
		if b == "" {
			continue
		}
		a.dlg = nil
		before := a.dlg
		a.toolAction(b)
		if a.dlg == before && b != "Connect" && b != "Received" {
			t.Errorf("toolbar %s opens nothing", b)
		}
	}
}
