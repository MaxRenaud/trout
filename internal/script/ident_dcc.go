package script

import (
	"encoding/binary"
	"net/netip"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"
)

// DCC (and Send File) for scripts: the on FILERCVD, GETFAIL, FILESENT,
// SENDFAIL and CHAT events, and $get, $send, $chat, $filename. Then the
// smaller identifiers from the corpus: $readn, $longip, $isalias, $alias,
// $finddir, $notify, $mklogfn, $sfile, and the ones trout has no use for.
// Checked in mIRC 7.86 (tmp/misc-oracle3.mrc) where the help is silent.

// Transfer is a file being got or sent, or a DCC chat, for $get, $send,
// $chat and the DCC events.
type Transfer struct {
	Kind    string // get, send or chat
	Network string
	Nick    string
	IP      string
	File    string // the file's full name: where a get is saved, what was sent
	Size    int64  // 0 if not known
	Done    int64  // bytes received or sent
	Resume  int64  // where a get resumed from
	Status  string // active, waiting, sent (finished) or failed
	Started time.Time
	Rate    float64 // bytes a second
}

// NotifyEntry is a nick on the notify list, for $notify.
type NotifyEntry struct {
	Nick, Note string
	Whois, On  bool
	Addr       string // user@host, when known
}

// transfers is the client's transfers of kind (get, send or chat).
func (r *run) transfers(kind string) []Transfer {
	var out []Transfer
	for _, t := range r.h.Transfers() {
		if t.Kind == kind {
			out = append(out, t)
		}
	}
	return out
}

// dccIdent is $get, $send or $chat (N|nick[,N]).property: N = 0 is how
// many, -1 the one the event is about, a nick its Nth one.
func dccIdent(kind string) builtin {
	return func(r *run, fr *frame, c *call) (string, error) {
		ts := r.transfers(kind)
		var t *Transfer
		arg := c.arg(0)
		n, isNum := parseNum(arg)
		switch {
		case isNum && n == 0 && arg != "":
			return itoa(len(ts)), nil
		case isNum && n == -1:
			if ev := fr.ev; ev != nil && ev.Transfer != nil && ev.Transfer.Kind == kind {
				t = ev.Transfer
			}
		case isNum && n >= 1 && int(n) <= len(ts):
			t = &ts[int(n)-1]
		case !isNum && arg != "":
			want := max(int(c.num(1)), 1)
			for i := range ts {
				if strings.EqualFold(ts[i].Nick, arg) {
					if want--; want == 0 {
						t = &ts[i]
						break
					}
				}
			}
		}
		if t == nil {
			return "", nil
		}
		return t.prop(c.prop, r.h.Now()), nil
	}
}

func (t *Transfer) prop(p string, now time.Time) string {
	secs := int(now.Sub(t.Started) / time.Second)
	switch p {
	case "":
		return t.Nick
	case "ip":
		return t.IP
	case "status":
		return t.Status
	case "file":
		return path.Base(strings.ReplaceAll(t.File, `\`, "/"))
	case "path":
		dir, _ := path.Split(strings.ReplaceAll(t.File, `\`, "/"))
		return dir
	case "size":
		return strconv.FormatInt(t.Size, 10)
	case "rcvd", "sent":
		return strconv.FormatInt(t.Done, 10)
	case "cps":
		return itoa(int(t.Rate))
	case "pc":
		if t.Size <= 0 {
			return "0"
		}
		return itoa(int(t.Done * 100 / t.Size))
	case "secs", "idle":
		return itoa(max(secs, 0))
	case "done":
		return boolStr(t.Status == "sent")
	case "resume":
		return strconv.FormatInt(t.Resume, 10)
	}
	return ""
}

// filenameIdent is $filename in a DCC event: the file's full name.
func filenameIdent(r *run, fr *frame, c *call) (string, error) {
	if ev := fr.ev; ev != nil && ev.Transfer != nil {
		return ev.Transfer.File, nil
	}
	return "", nil
}

// longipIdent is $longip(address): an IPv4 address as a number, and a
// number back as one.
func longipIdent(r *run, fr *frame, c *call) (string, error) {
	s := strings.TrimSpace(c.arg(0))
	if ip, err := netip.ParseAddr(s); err == nil && ip.Is4() {
		b := ip.As4()
		return strconv.FormatUint(uint64(binary.BigEndian.Uint32(b[:])), 10), nil
	}
	if n, err := strconv.ParseUint(s, 10, 32); err == nil {
		var b [4]byte
		binary.BigEndian.PutUint32(b[:], uint32(n))
		return netip.AddrFrom4(b).String(), nil
	}
	return "", nil
}

// isaliasIdent is $isalias(name): whether an alias exists, with .fname
// its file, .alias its definition and .ftype alias (an aliases file) or
// remote. Like mIRC's, a script's own -l aliases count (checked).
func isaliasIdent(r *run, fr *frame, c *call) (string, error) {
	a := r.e.alias(fr.file, strings.TrimLeft(c.arg(0), "/"))
	switch {
	case a == nil && c.prop == "":
		return "$false", nil
	case a == nil:
		return "", nil
	}
	switch c.prop {
	case "fname":
		return a.file.name, nil
	case "alias":
		return a.name + " " + a.src, nil
	case "ftype":
		if a.file.kind == "aliases" {
			return "alias", nil
		}
		return "remote", nil
	}
	return "$true", nil
}

// aliasIdent is $alias(N|filename): the Nth aliases file loaded (0: how
// many), or a file's name if it's one.
func aliasIdent(r *run, fr *frame, c *call) (string, error) {
	var files []string
	for _, f := range r.e.scripts {
		if f.kind == "aliases" {
			files = append(files, f.name)
		}
	}
	arg := c.arg(0)
	if n, ok := parseNum(arg); ok {
		switch {
		case n == 0:
			return itoa(len(files)), nil
		case n >= 1 && int(n) <= len(files):
			return files[int(n)-1], nil
		}
		return "", nil
	}
	if i := slices.IndexFunc(files, func(f string) bool { return strings.EqualFold(f, arg) }); i >= 0 {
		return files[i], nil
	}
	return "", nil
}

// notifyIdent is $notify(N|nick): the Nth nick on the notify list (0: how
// many), or a nick's place in it, with .ison, .note, .whois, .addr and
// .network. $notify alone is whether the list is on: always, in trout.
func notifyIdent(r *run, fr *frame, c *call) (string, error) {
	if !c.hasArgs {
		return "$true", nil
	}
	list := r.h.Notify(fr.network)
	arg := c.arg(0)
	var e *NotifyEntry
	if n, ok := parseNum(arg); ok {
		switch {
		case n == 0:
			return itoa(len(list)), nil
		case n >= 1 && int(n) <= len(list):
			e = &list[int(n)-1]
		}
	} else if i := slices.IndexFunc(list, func(e NotifyEntry) bool { return strings.EqualFold(e.Nick, arg) }); i >= 0 {
		if c.prop == "" {
			return itoa(i + 1), nil
		}
		e = &list[i]
	}
	if e == nil {
		return "", nil
	}
	switch c.prop {
	case "":
		return e.Nick, nil
	case "ison":
		return boolStr(e.On), nil
	case "note":
		return e.Note, nil
	case "whois":
		return boolStr(e.Whois), nil
	case "addr":
		return e.Addr, nil
	case "network":
		return fr.network, nil
	}
	return "", nil
}

// mklogfnIdent is $mklogfn(window): the log file mIRC would use for it,
// window.Network.log, "status" for the status window (checked). trout
// keeps no logs, but scripts build their own names with it.
func mklogfnIdent(r *run, fr *frame, c *call) (string, error) {
	name := c.arg(0)
	if strings.EqualFold(name, "Status Window") {
		name = "status"
	}
	name = strings.Map(func(r rune) rune {
		if strings.ContainsRune(`\/:*?"<>|`, r) {
			return '_'
		}
		return r
	}, name)
	return name + "." + fr.network + ".log", nil
}

// sfileIdent is $sfile(dir,title,oktext): mIRC's select file dialog, here
// an input box for a file name (dir filled in), answered like $? is.
// $sfstate is cancel when it was cancelled.
func sfileIdent(r *run, fr *frame, c *call) (string, error) {
	r.e.sfstate = ""
	var v string
	switch {
	case len(r.e.answers) > 0:
		v = r.e.answers[0]
		r.e.answers = r.e.answers[1:]
	case r.e.ask != nil:
		title := cmpOr(c.arg(1), "Select a file")
		v = r.e.ask(Prompt{Text: title, File: true, Default: c.arg(0)})
	default:
		return "", errorf("$sfile: input boxes don't work here")
	}
	if v == "" {
		r.e.sfstate = "cancel"
	}
	return v, nil
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

var dccIdents = map[string]builtin{
	"get":      dccIdent("get"),
	"send":     dccIdent("send"),
	"chat":     dccIdent("chat"),
	"filename": filenameIdent,
	"longip":   longipIdent,
	"isalias":  isaliasIdent,
	"alias":    aliasIdent,
	"notify":   notifyIdent,
	"mklogfn":  mklogfnIdent,
	"sfile":    sfileIdent,
	"sfstate":  func(r *run, fr *frame, c *call) (string, error) { return r.e.sfstate, nil },
	"readn":    func(r *run, fr *frame, c *call) (string, error) { return itoa(r.e.readn), nil },
	"finddir": func(r *run, fr *frame, c *call) (string, error) {
		return findIdent(r, fr, c, true)
	},
	// $fullname is the real name the network has for you.
	"fullname": func(r *run, fr *frame, c *call) (string, error) { return r.h.Info(fr.network).RealName, nil },
	// $mircexe is trout's program file.
	"mircexe": func(r *run, fr *frame, c *call) (string, error) { return r.e.opts.Exe, nil },
	// trout has no email address, alternate nick (the bouncer handles a
	// nick that's taken) or sounds.
	"emailaddr": str(""),
	"anick":     str(""),
	"insong":    func(r *run, fr *frame, c *call) (string, error) { return iff(c.prop == "", "$false", ""), nil },
	"inwave":    func(r *run, fr *frame, c *call) (string, error) { return iff(c.prop == "", "$false", ""), nil },
}
