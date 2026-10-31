package script

import (
	"slices"
	"strings"
	"time"
)

// Connections: what scripts know about the networks the client is on, from
// mIRC's Multi-server, Internal Address List and Other Identifiers
// sections, and the mode events a MODE line turns into.

// NetInfo is what the client knows about a network's connection.
type NetInfo struct {
	Server     string // the server's name, "" when not connected
	Port       int
	SSL        bool
	Connected  bool
	Connecting bool
	ISupport   map[string]string // 005 tokens: PREFIX, CHANMODES, CHANTYPES, MODES...
	UserModes  string            // ours, like +iw
	Away       bool
	AwayMsg    string
	Idle       time.Duration // since we last said something
}

// isupport is a 005 token, or mIRC's default for it when not connected.
func (r *run) isupport(network, token string) string {
	if v, ok := r.h.Info(network).ISupport[token]; ok {
		return v
	}
	return map[string]string{"PREFIX": "(ohv)@%+", "CHANMODES": "bIe,k,l", "CHANTYPES": "#&", "MODES": "3"}[token]
}

// cid is a network's connection id: its place in the client's list, from 1.
func (r *run) cid(network string) int {
	for i, n := range r.h.Networks() {
		if strings.EqualFold(n, network) {
			return i + 1
		}
	}
	return 0
}

// modeChange is one change in a MODE line, like +o bob.
type modeChange struct {
	plus  bool
	mode  byte
	param string
}

// splitModes reads a channel MODE line's changes. Which modes take a
// parameter comes from the server's PREFIX and CHANMODES: prefix modes and
// list modes always do, CHANMODES' second group always, its third only
// when set.
func (r *run) splitModes(network string, params []string) []modeChange {
	if len(params) == 0 {
		return nil
	}
	prefix := r.isupport(network, "PREFIX")
	prefixModes := ""
	if strings.HasPrefix(prefix, "(") {
		prefixModes, _, _ = strings.Cut(prefix[1:], ")")
	}
	groups := strings.SplitN(r.isupport(network, "CHANMODES")+",,,", ",", 4)
	always := prefixModes + groups[0] + groups[1]
	var out []modeChange
	plus, args := true, params[1:]
	for i := 0; i < len(params[0]); i++ {
		c := params[0][i]
		switch {
		case c == '+' || c == '-':
			plus = c == '+'
			continue
		}
		mc := modeChange{plus: plus, mode: c}
		if strings.IndexByte(always, c) >= 0 || plus && strings.IndexByte(groups[2], c) >= 0 {
			if len(args) > 0 {
				mc.param, args = args[0], args[1:]
			}
		}
		out = append(out, mc)
	}
	return out
}

// modeEvents are the events a channel MODE line turns into after
// RAWMODE: OP, DEOP, VOICE, DEVOICE, HELP, DEHELP, OWNER and DEOWNER for
// prefix modes (SERVEROP when a server ops someone), BAN and UNBAN, and a
// MODE (SERVERMODE from a server) for the rest of the line.
func (r *run) modeEvents(ev *Event) []Event {
	changes := ev.modes
	server := ev.Address == "" && strings.Contains(ev.Nick, ".")
	kinds := r.modeKinds(ev.Network)
	var out []Event
	other := false
	for _, mc := range changes {
		k, ok := kinds[mc.mode]
		if !ok || mc.param == "" {
			other = true
			continue
		}
		kind := k[0]
		if !mc.plus {
			kind = k[1]
		}
		if kind == "OP" && server {
			kind = "SERVEROP"
		}
		e := *ev
		e.Kind, e.Target, e.Text = kind, mc.param, ""
		e.modes = changes
		out = append(out, e)
	}
	if other {
		e := *ev
		e.Kind = "MODE"
		if server {
			e.Kind = "SERVERMODE"
		}
		e.modes = changes
		out = append(out, e)
	}
	for i := range out {
		out[i].modeFirst, out[i].modeLast = i == 0, i == len(out)-1
	}
	return out
}

// modeKinds is which modes make which events on a network: o, v, h and q
// only when they're nick prefixes there (PREFIX), since some networks use
// q for a quiet list; b when it's a list mode (CHANMODES).
func (r *run) modeKinds(network string) map[byte][2]string {
	all := map[byte][2]string{'o': {"OP", "DEOP"}, 'v': {"VOICE", "DEVOICE"},
		'h': {"HELP", "DEHELP"}, 'q': {"OWNER", "DEOWNER"}}
	prefixModes, _, _ := strings.Cut(strings.TrimPrefix(r.isupport(network, "PREFIX"), "("), ")")
	kinds := map[byte][2]string{}
	for m, k := range all {
		if strings.IndexByte(prefixModes, m) >= 0 {
			kinds[m] = k
		}
	}
	if lists, _, _ := strings.Cut(r.isupport(network, "CHANMODES"), ","); strings.IndexByte(lists, 'b') >= 0 {
		kinds['b'] = [2]string{"BAN", "UNBAN"}
	}
	return kinds
}

// bnick is the nick in a banmask, if it has one: bob in bob!*@*.
func bnick(mask string) string {
	nick, _, ok := strings.Cut(mask, "!")
	if !ok || strings.ContainsAny(nick, "*?") {
		return ""
	}
	return nick
}

var connIdents = map[string]builtin{
	// $cid is this script's connection id; $activecid the active window's.
	"cid": func(r *run, fr *frame, c *call) (string, error) { return itoa(r.cid(fr.network)), nil },
	"activecid": func(r *run, fr *frame, c *call) (string, error) {
		net, _ := r.h.Active()
		return itoa(r.cid(net)), nil
	},
	// $scid(N) is connection id N if there is one, and $scon(N) the Nth
	// connection's id; 0 counts them, -1 is the active one. A property
	// that's an identifier is evaluated on that connection:
	// $scid(2).network.
	"scid": func(r *run, fr *frame, c *call) (string, error) { return r.scidIdent(fr, c, true) },
	"scon": func(r *run, fr *frame, c *call) (string, error) { return r.scidIdent(fr, c, false) },
	"server": func(r *run, fr *frame, c *call) (string, error) {
		if c.hasArgs {
			return "", errorf("$server(N) isn't supported")
		}
		return r.h.Info(fr.network).Server, nil
	},
	"port": func(r *run, fr *frame, c *call) (string, error) {
		if p := r.h.Info(fr.network).Port; p > 0 {
			return itoa(p), nil
		}
		return "", nil
	},
	"ssl": func(r *run, fr *frame, c *call) (string, error) { return boolStr(r.h.Info(fr.network).SSL), nil },
	"status": func(r *run, fr *frame, c *call) (string, error) {
		info := r.h.Info(fr.network)
		switch {
		case info.Connected:
			return "connected", nil
		case info.Connecting:
			return "connecting", nil
		}
		return "disconnected", nil
	},
	"chantypes": func(r *run, fr *frame, c *call) (string, error) { return r.isupport(fr.network, "CHANTYPES"), nil },
	"chanmodes": func(r *run, fr *frame, c *call) (string, error) { return r.isupport(fr.network, "CHANMODES"), nil },
	"modespl":   func(r *run, fr *frame, c *call) (string, error) { return r.isupport(fr.network, "MODES"), nil },
	// $prefix is the nick prefixes, like @%+, and $nickmode their modes.
	"prefix": func(r *run, fr *frame, c *call) (string, error) {
		_, chars, _ := strings.Cut(r.isupport(fr.network, "PREFIX"), ")")
		return chars, nil
	},
	"nickmode": func(r *run, fr *frame, c *call) (string, error) {
		modes, _, _ := strings.Cut(strings.TrimPrefix(r.isupport(fr.network, "PREFIX"), "("), ")")
		return modes, nil
	},
	"usermode": func(r *run, fr *frame, c *call) (string, error) { return r.h.Info(fr.network).UserModes, nil },
	"away":     func(r *run, fr *frame, c *call) (string, error) { return boolStr(r.h.Info(fr.network).Away), nil },
	"awaymsg":  func(r *run, fr *frame, c *call) (string, error) { return r.h.Info(fr.network).AwayMsg, nil },
	"idle": func(r *run, fr *frame, c *call) (string, error) {
		return itoa(int(r.h.Info(fr.network).Idle.Seconds())), nil
	},
	// $query(N/nick) is the Nth open query window, or that nick's.
	"query": func(r *run, fr *frame, c *call) (string, error) {
		return nthOrName(r.h.Queries(fr.network), c.arg(0)), nil
	},
	// $ial(mask,N) is the Nth address in the client's address list that
	// matches mask (or a nick); 0 counts them. .nick, .user, .host and
	// .addr are its parts.
	"ial": func(r *run, fr *frame, c *call) (string, error) {
		if !c.hasArgs {
			return "$true", nil
		}
		return ialMatch(r.h.IAL(fr.network), c.arg(0), c, 1, nil)
	},
	// $ialchan(mask,#,N) is the same, among the nicks on #.
	"ialchan": func(r *run, fr *frame, c *call) (string, error) {
		on := map[string]bool{}
		for _, n := range r.h.Nicks(fr.network, c.arg(1)) {
			on[strings.ToLower(n.Name)] = true
		}
		return ialMatch(r.h.IAL(fr.network), c.arg(0), c, 2, on)
	},
	// The mode events' identifiers: $opnick, $vnick and $hnick are who was
	// opped, voiced or helped, $banmask the mask and $bnick the nick in it.
	"opnick":  modeTarget("OP", "DEOP", "SERVEROP", "OWNER", "DEOWNER"),
	"vnick":   modeTarget("VOICE", "DEVOICE"),
	"hnick":   modeTarget("HELP", "DEHELP"),
	"banmask": modeTarget("BAN", "UNBAN"),
	"bnick": func(r *run, fr *frame, c *call) (string, error) {
		return evField(fr, func(ev *Event) string {
			return iff(ev.Kind == "BAN" || ev.Kind == "UNBAN", bnick(ev.Target), "")
		}), nil
	},
	"modefirst": func(r *run, fr *frame, c *call) (string, error) {
		return boolStr(fr.ev != nil && fr.ev.modeFirst), nil
	},
	"modelast": func(r *run, fr *frame, c *call) (string, error) {
		return boolStr(fr.ev != nil && fr.ev.modeLast), nil
	},
	// $mode(N) is the Nth nick a mode line affected; .op, .deop, .voice,
	// .devoice, .help, .dehelp, .ban and .unban only those. 0 counts them.
	"mode": func(r *run, fr *frame, c *call) (string, error) {
		if fr.ev == nil {
			return "", nil
		}
		want := map[string]string{"op": "+o", "deop": "-o", "voice": "+v", "devoice": "-v",
			"help": "+h", "dehelp": "-h", "ban": "+b", "unban": "-b"}[c.prop]
		kinds := r.modeKinds(fr.network)
		var nicks []string
		for _, mc := range fr.ev.modes {
			sign := iff(mc.plus, "+", "-") + string(mc.mode)
			if _, ok := kinds[mc.mode]; mc.param == "" || !ok || want != "" && sign != want {
				continue
			}
			nicks = append(nicks, mc.param)
		}
		return nthOrName(nicks, c.arg(0)), nil
	},
}

func modeTarget(kinds ...string) builtin {
	return func(r *run, fr *frame, c *call) (string, error) {
		return evField(fr, func(ev *Event) string {
			if slices.Contains(kinds, ev.Kind) {
				return ev.Target
			}
			return ""
		}), nil
	}
}

// nthOrName is the Nth item (N = 0 counts them), or the item with that
// name.
func nthOrName(items []string, arg string) string {
	if n, ok := parseNum(arg); ok {
		switch i := int(n); {
		case i == 0:
			return itoa(len(items))
		case i > 0 && i <= len(items):
			return items[i-1]
		}
		return ""
	}
	for _, it := range items {
		if strings.EqualFold(it, arg) {
			return it
		}
	}
	return ""
}

// ialMatch is $ial and $ialchan: the Nth address (argument nArg, 1 by
// default) matching a mask or nick, among the nicks in on if that's set.
func ialMatch(ial []string, mask string, c *call, nArg int, on map[string]bool) (string, error) {
	if !strings.ContainsAny(mask, "!@") {
		mask += "!*@*"
	}
	var hits []string
	for _, a := range ial {
		nick, _, _ := strings.Cut(a, "!")
		if (on == nil || on[strings.ToLower(nick)]) && wildMatch(mask, a, false) {
			hits = append(hits, a)
		}
	}
	n := 1
	if len(c.vals) > nArg {
		n = int(c.num(nArg))
	}
	if n == 0 {
		return itoa(len(hits)), nil
	}
	if n < 1 || n > len(hits) {
		return "", nil
	}
	a := hits[n-1]
	nick, rest, _ := strings.Cut(a, "!")
	user, host, _ := strings.Cut(rest, "@")
	switch c.prop {
	case "nick":
		return nick, nil
	case "user":
		return user, nil
	case "host":
		return host, nil
	case "addr":
		return rest, nil
	}
	return a, nil
}

// scidIdent is $scid(N) and $scon(N), with byID for $scid.
func (r *run) scidIdent(fr *frame, c *call, byID bool) (string, error) {
	nets := r.h.Networks()
	n := int(c.num(0))
	var net string
	switch {
	case n == 0:
		return itoa(len(nets)), nil
	case n == -1:
		net, _ = r.h.Active()
	case n >= 1 && n <= len(nets):
		// Connection ids are positions in trout, so both are the same.
		net = nets[n-1]
	default:
		return "", nil
	}
	if c.prop == "" {
		return itoa(r.cid(net)), nil
	}
	other := *fr
	other.network = net
	v, err := r.ident(&other, c.prop, &call{})
	if err == errNoIdent {
		return "", nil
	}
	return v, err
}

// cmdScid is /scid and /scon: [-a] [-tM] [-r] <N> [command] runs the
// command on that connection (evaluated again there, so $!identifiers are
// that connection's); without a command, the rest of the script runs on
// it, until /scid -r.
func (r *run) cmdScid(fr *frame, name, rest string) error {
	line, err := r.eval(fr, rest)
	if err != nil {
		return err
	}
	// -1 (the active connection) isn't a switch.
	sw := ""
	for strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "-1") {
		w, more, _ := strings.Cut(line, " ")
		sw += w[1:]
		line = strings.TrimSpace(more)
	}
	if strings.Contains(sw, "r") {
		if fr.home != "" {
			fr.network = fr.home
		}
		return nil
	}
	nets := r.h.Networks()
	var targets []string
	cmd := line
	if strings.Contains(sw, "a") {
		targets = nets
	} else {
		var id string
		id, cmd, _ = strings.Cut(line, " ")
		n := atoi(id)
		if n == -1 {
			net, _ := r.h.Active()
			targets = []string{net}
		} else if n >= 1 && n <= len(nets) {
			targets = []string{nets[n-1]}
		} else {
			return errorf("/", name, ": invalid connection id")
		}
	}
	if m, ok := switchNum(sw, 't'); ok {
		var keep []string
		for _, t := range targets {
			info := r.h.Info(t)
			status := map[bool]int{true: 1, false: 2}[info.Connected] | map[bool]int{true: 4, false: 8}[info.Connecting]
			if status&m != 0 {
				keep = append(keep, t)
			}
		}
		targets = keep
	}
	if strings.TrimSpace(cmd) == "" {
		if len(targets) == 1 {
			if fr.home == "" {
				fr.home = fr.network
			}
			fr.network = targets[0]
		}
		return nil
	}
	for _, t := range targets {
		other := *fr
		other.network = t
		if err := r.command(&other, cmd); err != nil {
			return err
		}
	}
	return nil
}

// ialEntry is nick's nick!user@host in the client's address list.
func (r *run) ialEntry(network, nick string) (string, bool) {
	for _, a := range r.h.IAL(network) {
		if n, _, _ := strings.Cut(a, "!"); strings.EqualFold(n, nick) {
			return a, true
		}
	}
	return "", false
}
