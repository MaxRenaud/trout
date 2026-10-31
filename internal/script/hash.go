package script

import (
	"cmp"
	"maps"
	"slices"
	"strings"
	"time"
)

// Hash tables, from mIRC's Hash Tables section: named tables of items and
// their data, kept in memory until /hsave writes them to a file.

// table is a hash table. mIRC's order is the hash's, which scripts aren't
// to depend on ("items are stored in hash tables in a random order"); this
// keeps the order items were added in, so it's at least the same each time.
type table struct {
	expires map[string]time.Time // /hadd -uN: lower-case item -> when it's unset
	name    string
	size    int
	items   []string          // item names, in the order they were added
	data    map[string]string // by lower-case item name
	names   map[string]string // lower-case item name -> as added
}

// newTable makes a table. Like mIRC, its size is made odd: 10 slots are
// 11, and the default 100 is 101.
func newTable(name string, size int) *table {
	size = cmp.Or(max(size, 0), 100)
	return &table{name: name, size: size | 1, data: map[string]string{}, names: map[string]string{}}
}

func (t *table) set(item, value string) {
	key := strings.ToLower(item)
	if _, ok := t.data[key]; !ok {
		t.items = append(t.items, item)
		t.names[key] = item
	}
	t.data[key] = value
}

func (t *table) del(item string) {
	key := strings.ToLower(item)
	if _, ok := t.data[key]; !ok {
		return
	}
	delete(t.data, key)
	delete(t.names, key)
	delete(t.expires, key)
	t.items = slices.DeleteFunc(t.items, func(s string) bool { return strings.EqualFold(s, item) })
}

// table finds a table by name, or by number with N, like $hget(N). A
// table named 2 wins over the second table (checked in mIRC).
func (r *run) table(nameOrN string) *table {
	if t := r.e.tables[strings.ToLower(nameOrN)]; t != nil {
		return t
	}
	if n, ok := parseNum(nameOrN); ok && n >= 1 {
		names := slices.Sorted(maps.Keys(r.e.tables))
		if i := int(n) - 1; i < len(names) {
			return r.e.tables[names[i]]
		}
		return nil
	}
	return r.e.tables[strings.ToLower(nameOrN)]
}

// switchNum reads a switch's number, like the 10 in -m10.
func switchNum(sw string, letter byte) (int, bool) {
	i := strings.IndexByte(sw, letter)
	if i < 0 {
		return 0, false
	}
	n := digitsAt(sw, i+1, len(sw))
	return atoi(sw[i+1 : i+1+n]), true
}

// cmdHash runs the hash table commands: /hmake, /hfree, /hadd, /hdel,
// /hinc, /hdec, /hsave and /hload.
func (r *run) cmdHash(fr *frame, name, rest string) error {
	line, err := r.eval(fr, rest)
	if err != nil {
		return err
	}
	sw, line := switches(line)
	args := strings.SplitN(line, " ", 3)
	arg := func(i int) string {
		if i < len(args) {
			return args[i]
		}
		return ""
	}
	tname := arg(0)
	if tname == "" {
		return errorf("/", name, ": insufficient parameters")
	}
	// The commands take a table's name, even one that's a number: only
	// $hget and $hfind take N.
	t := r.e.tables[strings.ToLower(tname)]
	need := func() error {
		if t == nil {
			if m, ok := switchNum(sw, 'm'); ok {
				t = newTable(tname, m)
				r.e.tables[strings.ToLower(tname)] = t
				return nil
			}
			return errorf("/", name, ": no such table '", tname, "'")
		}
		return nil
	}
	switch name {
	case "hmake":
		if t != nil {
			return errorf("/hmake: table '", tname, "' exists")
		}
		r.e.tables[strings.ToLower(tname)] = newTable(tname, atoi(arg(1)))
	case "hfree":
		if strings.Contains(sw, "w") {
			maps.DeleteFunc(r.e.tables, func(k string, _ *table) bool { return wildMatch(tname, k, false) })
			return nil
		}
		if t == nil {
			return errorf("/hfree: no such table '", tname, "'")
		}
		delete(r.e.tables, strings.ToLower(t.name))
	case "hadd":
		if err := need(); err != nil {
			return err
		}
		if arg(1) == "" {
			return errorf("/hadd: insufficient parameters")
		}
		t.set(arg(1), arg(2))
		r.itemExpiry(t, arg(1), sw)
	case "hinc", "hdec":
		if err := need(); err != nil {
			return err
		}
		by := 1.0
		if arg(2) != "" {
			by, _ = parseNum(arg(2))
		}
		if name == "hdec" {
			by = -by
		}
		cur, _ := parseNum(t.data[strings.ToLower(arg(1))])
		t.set(arg(1), formatNum(cur+by))
		r.itemExpiry(t, arg(1), sw)
	case "hdel":
		if t == nil {
			return errorf("/hdel: no such table '", tname, "'")
		}
		if strings.Contains(sw, "w") {
			for _, item := range slices.Clone(t.items) {
				if wildMatch(arg(1), item, false) {
					t.del(item)
				}
			}
			return nil
		}
		t.del(arg(1))
	case "hsave":
		if t == nil {
			return errorf("/hsave: no such table '", tname, "'")
		}
		if arg(1) == "" {
			return errorf("/hsave: insufficient parameters")
		}
		r.writeFile(strings.SplitN(arg(1)+" "+arg(2), " ", 2)[0], t.text(strings.Contains(sw, "n")), strings.Contains(sw, "a"))
	case "hload":
		if err := need(); err != nil {
			return err
		}
		file := strings.SplitN(arg(1)+" "+arg(2), " ", 2)[0]
		data, ok := r.readFile(file)
		if !ok {
			return errorf("/hload: unable to open '", file, "'")
		}
		t.load(data, strings.Contains(sw, "n"))
	}
	return nil
}

// text is the table as /hsave writes it: each item, then its data, on
// lines of their own; or with -n just the data. $cr and $lf are taken
// out, like mIRC.
func (t *table) text(dataOnly bool) string {
	clean := func(s string) string { return strings.NewReplacer("\r", "", "\n", "").Replace(s) }
	var b strings.Builder
	for _, item := range t.items {
		if !dataOnly {
			b.WriteString(clean(item) + "\r\n")
		}
		b.WriteString(clean(t.data[strings.ToLower(item)]) + "\r\n")
	}
	return b.String()
}

// load adds what /hsave wrote to the table: item and data lines, or with
// -n data lines, numbered from 1.
func (t *table) load(data string, dataOnly bool) {
	lines := fileLines(data)
	if dataOnly {
		for i, l := range lines {
			t.set(itoa(i+1), l)
		}
		return
	}
	for i := 0; i+1 < len(lines); i += 2 {
		t.set(lines[i], lines[i+1])
	}
}

// hashIdents are $hget and $hfind.
var hashIdents = map[string]builtin{
	// $hget(name/N) is the table's name, $hget(name, item) an item's data,
	// $hget(name, N).item and .data the Nth item; N = 0 counts.
	"hget": func(r *run, fr *frame, c *call) (string, error) {
		if len(c.vals) <= 1 && strings.TrimSpace(c.arg(0)) == "0" {
			return itoa(len(r.e.tables)), nil
		}
		t := r.table(c.arg(0))
		if t == nil {
			return "", nil
		}
		if len(c.vals) < 2 {
			if c.prop == "size" {
				return itoa(t.size), nil
			}
			return t.name, nil
		}
		if c.prop == "item" || c.prop == "data" {
			n, ok := parseNum(c.arg(1))
			if !ok {
				return "", nil
			}
			i := int(n)
			switch {
			case i == 0:
				return itoa(len(t.items)), nil
			case i < 0 || i > len(t.items):
				return "", nil
			case c.prop == "item":
				return t.items[i-1], nil
			}
			return t.data[strings.ToLower(t.items[i-1])], nil
		}
		return t.data[strings.ToLower(c.arg(1))], nil
	},
	// $hfind(name, text, N, M) is the Nth item matching text, or with N = 0
	// how many match. M is n (the same text), w (text is a wildcard), W
	// (the items are), r or R (the same with regular expressions); .data
	// searches the data instead, still returning the item.
	"hfind": func(r *run, fr *frame, c *call) (string, error) {
		t := r.table(c.arg(0))
		if t == nil {
			return "", nil
		}
		text, want, mode := c.arg(1), 1, cmp.Or(c.arg(3), "n")
		if len(c.vals) > 2 {
			want = int(c.num(2))
		}
		n := 0
		for _, item := range t.items {
			s := item
			if c.prop == "data" {
				s = t.data[strings.ToLower(item)]
			}
			var ok bool
			switch mode {
			case "w":
				ok = wildMatch(text, s, false)
			case "W":
				ok = wildMatch(s, text, false)
			case "r", "R":
				pattern, subject := text, s
				if mode == "R" {
					pattern, subject = s, text
				}
				m, err := r.regexMatch(pattern, subject)
				if err != nil {
					return "", err
				}
				ok = m
			default:
				ok = strings.EqualFold(s, text)
			}
			if ok {
				n++
				if n == want {
					return item, nil
				}
			}
		}
		if want == 0 {
			return itoa(n), nil
		}
		return "", nil
	},
}

// itemExpiry applies /hadd -uN to an item: it's deleted after N seconds,
// unless it's set again without -u (or with -k, which keeps the time).
func (r *run) itemExpiry(t *table, item, sw string) {
	key := strings.ToLower(item)
	n, u := switchNum(sw, 'u')
	switch {
	case u && n > 0:
		if t.expires == nil {
			t.expires = map[string]time.Time{}
		}
		t.expires[key] = r.h.Now().Add(time.Duration(n) * time.Second)
	case !strings.Contains(sw, "k"):
		delete(t.expires, key)
	}
}
