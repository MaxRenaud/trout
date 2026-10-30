package script

import (
	"slices"
	"strconv"
	"strings"
)

// Popup menus, from mIRC's Popups section: menu sections add items to the
// status, channel, query, nicklist and menubar popups. The client asks for
// a popup's items with Popups and runs a chosen one with RunMenu.

// MenuItem is a popup menu item, ready to show.
type MenuItem struct {
	ID        string // for RunMenu; "" for a submenu or separator
	Label     string
	Separator bool
	Checked   bool // $style(1)
	Disabled  bool // $style(2)
	Items     []MenuItem
}

// popup is a menu section.
type popup struct {
	kinds []string // lower-case: status, channel, query, nicklist, menubar
	items []popupItem
}

type popupItem struct {
	depth int    // how many dots: 0 is the top level
	label string // not evaluated: that happens each time it's shown
	sep   bool
	body  []stmt // nil for a submenu's title
}

// menuDef parses a menu section's lines.
func (p *parser) menuDef(l srcLine, rest string) {
	names, first, _ := strings.Cut(strings.TrimSpace(rest), " ")
	pop := &popup{}
	for k := range strings.SplitSeq(names, ",") {
		pop.kinds = append(pop.kinds, strings.ToLower(strings.TrimSpace(k)))
	}
	lines := p.body(l, first)
	for i := 0; i < len(lines); i++ {
		t := lines[i].text
		depth := len(t) - len(strings.TrimLeft(t, "."))
		t = t[depth:]
		if t == "-" {
			pop.items = append(pop.items, popupItem{depth: depth, sep: true})
			continue
		}
		label, ok := strings.CutSuffix(t, " {")
		if !ok {
			label, ok = strings.CutSuffix(t, ":{")
		}
		if ok {
			// Label { or Label:{, with commands on the lines up to its }.
			var block []srcLine
			level := 1
			for i++; i < len(lines); i++ {
				for _, w := range splitWords(lines[i].text) {
					level += braceDelta(w)
				}
				if level <= 0 {
					break
				}
				block = append(block, lines[i])
			}
			pop.items = append(pop.items, popupItem{depth: depth, label: label, body: p.stmts(block)})
			continue
		}
		f := splitFields(t, 2)
		item := popupItem{depth: depth, label: strings.TrimSpace(f[0])}
		if len(f) == 2 {
			cmds := strings.TrimSpace(f[1])
			item.body = p.stmts([]srcLine{{lines[i].n, cmds}})
			if item.body == nil {
				item.body = []stmt{}
			}
		}
		pop.items = append(pop.items, item)
	}
	p.f.popups = append(p.f.popups, pop)
}

// styleMark is what $style(N) evaluates to: a marker the label reader
// takes back out.
const styleMark = "\x00style"

// Popups is the scripts' items for a popup menu: kind is status, channel,
// query, nicklist or menubar, and params are its $1- (the selected nick
// in the nicklist). Their labels are evaluated now; an item whose label
// is $null isn't shown, like mIRC's. Labels can call aliases, so building
// the menu can have actions too, like an error to show.
func (e *Engine) Popups(kind, network, window string, params []string, h Host) ([]MenuItem, []Action) {
	r := e.newRun(h)
	kind = strings.ToLower(kind)
	var out []MenuItem
	for si, f := range e.scripts {
		for pi, pop := range f.popups {
			if slices.Contains(pop.kinds, kind) {
				fr := r.menuFrame(f, kind, network, window, params)
				items, _ := r.menuItems(fr, pop.items, 0, 0, itoa(si)+"/"+itoa(pi))
				out = append(out, items...)
			}
		}
	}
	return out, r.finish()
}

func (r *run) menuFrame(f *file, kind, network, window string, params []string) *frame {
	fr := &frame{file: f, network: network, window: window, params: params, typed: true, menu: kind}
	if isChannel(window) {
		fr.channel = window
	}
	return fr
}

// menuItems builds items[i:] at depth, and returns where it stopped.
func (r *run) menuItems(fr *frame, items []popupItem, i, depth int, id string) ([]MenuItem, int) {
	var out []MenuItem
	for i < len(items) && items[i].depth >= depth {
		it := items[i]
		if it.depth > depth {
			// Deeper than its parent allows: skip it.
			i++
			continue
		}
		if it.sep {
			out = append(out, MenuItem{Separator: true})
			i++
			continue
		}
		if inner, ok := submenuCall(it.label); ok {
			out = append(out, r.submenu(fr, inner, id+"/"+itoa(i))...)
			i++
			continue
		}
		r.steps = 0
		label, err := r.eval(fr, it.label)
		m := MenuItem{}
		if err == nil {
			m.Label, m.Checked, m.Disabled = readStyle(label)
		} else {
			r.report(fr, err)
		}
		index := i
		i++
		if i < len(items) && items[i].depth > depth {
			m.Items, i = r.menuItems(fr, items, i, depth+1, id)
		} else {
			m.ID = id + "/" + itoa(index)
		}
		if m.Label != "" {
			out = append(out, m)
		}
	}
	return out, i
}

// readStyle takes $style(N)'s marker off the front of a label.
func readStyle(label string) (text string, checked, disabled bool) {
	rest, ok := strings.CutPrefix(label, styleMark)
	if !ok || rest == "" {
		return label, false, false
	}
	n := int(rest[0] - '0')
	return strings.TrimSpace(rest[1:]), n&1 != 0, n&2 != 0
}

// submenuCall reports whether a label is $submenu($id($1)), and returns
// what's inside it.
func submenuCall(label string) (string, bool) {
	inner, ok := strings.CutPrefix(label, "$submenu(")
	if !ok || !strings.HasSuffix(inner, ")") {
		return "", false
	}
	return inner[:len(inner)-1], true
}

// submenu is $submenu($id($1)): it evaluates inner with $1 = begin, 1,
// 2... until it's $null, then end, and each value is an item's
// definition, Label:commands, or - for a separator.
func (r *run) submenu(fr *frame, inner, id string) []MenuItem {
	var out []MenuItem
	add := func(def, itemID string) {
		if def == "-" {
			out = append(out, MenuItem{Separator: true})
			return
		}
		label, cmds, _ := strings.Cut(def, ":")
		text, checked, disabled := readStyle(label)
		out = append(out, MenuItem{ID: itemID + ":" + cmds, Label: text, Checked: checked, Disabled: disabled})
	}
	call := func(param string) string {
		if r.step() != nil {
			return ""
		}
		saved := fr.params
		fr.params = []string{param}
		v, _ := r.eval(fr, inner)
		fr.params = saved
		return v
	}
	if v := call("begin"); v != "" {
		add(v, id)
	}
	for n := 1; n <= 1000; n++ {
		v := call(itoa(n))
		if v == "" {
			break
		}
		add(v, id)
	}
	if v := call("end"); v != "" {
		add(v, id)
	}
	return out
}

// RunMenu runs the popup item with this ID, from Popups, with the same
// $1- the popup had.
func (e *Engine) RunMenu(id, kind, network, window string, params []string, h Host) []Action {
	r := e.newRun(h)
	parts := strings.SplitN(id, "/", 3)
	if len(parts) < 3 {
		return nil
	}
	si, err1 := strconv.Atoi(parts[0])
	pi, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || si >= len(e.scripts) || pi >= len(e.scripts[si].popups) {
		return nil
	}
	f := e.scripts[si]
	fr := r.menuFrame(f, strings.ToLower(kind), network, window, params)
	itemPart, cmds, dynamic := strings.Cut(parts[2], ":")
	if dynamic {
		// A $submenu item: its commands came with its definition.
		r.report(fr, r.block(fr, (&parser{f: f}).stmts([]srcLine{{0, cmds}})))
		return r.finish()
	}
	ii, err := strconv.Atoi(itemPart)
	items := e.scripts[si].popups[pi].items
	if err != nil || ii >= len(items) {
		return nil
	}
	r.report(fr, r.block(fr, items[ii].body))
	return r.finish()
}

var menuIdents = map[string]builtin{
	// $style(N) in a popup label: 1 checks it, 2 disables it, 3 both.
	"style": func(r *run, fr *frame, c *call) (string, error) {
		return styleMark + itoa(clamp(int(c.num(0)), 0, 3)), nil
	},
	// $submenu($id($1)) only works as a popup item's label, where Popups
	// reads it.
	"submenu": func(r *run, fr *frame, c *call) (string, error) {
		return "", errorf("$submenu can only be used in a popup menu definition")
	},
	// $menu and $menutype are the popup being built: status, channel...
	"menu":     func(r *run, fr *frame, c *call) (string, error) { return fr.menu, nil },
	"menutype": func(r *run, fr *frame, c *call) (string, error) { return fr.menu, nil },
}
