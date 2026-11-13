package script

import (
	"slices"
	"strings"
)

// ChanInfo is what the client knows of a channel we're on, for $chan's
// properties.
type ChanInfo struct {
	Topic  string // with its color and format codes, like mIRC's (checked)
	Mode   string // the modes with their arguments: "+ntkl sesame 42" (checked)
	Key    string
	Limit  string
	Status string // "joined", or "kicked" for a window we've been put out of
	// IAL is whether the client knows everyone's address (after WHO);
	// InWho whether a WHO for it is under way.
	IAL, InWho bool
	Stamp      bool   // timestamps on in its window
	Lists      string // the list modes it has fetched, of "beIq"
	Idle       int    // seconds since anyone else said anything there, or since we joined
	Logfile    string // "" when it isn't logged
}

// ChanInfos is a Host that can say what ChanInfo has; one that can't has
// no properties for $chan.
type ChanInfos interface {
	ChanInfo(network, channel string) (ChanInfo, bool)
}

// chanProp is $chan(#).prop. mIRC's properties are topic, mode, key,
// limit, ial, logfile, stamp, status, inwho, wid, cid, hwnd, ibl, iel,
// iil, iql and idle; each checked in mIRC on a channel. wid and hwnd are
// a window's id and its Windows handle, which a terminal doesn't have.
func (r *run) chanProp(network, ch, prop string) string {
	h, ok := r.h.(ChanInfos)
	if !ok {
		return ""
	}
	info, ok := h.ChanInfo(network, ch)
	if !ok {
		return ""
	}
	list := func(mode byte) string { return boolStr(strings.IndexByte(info.Lists, mode) >= 0) }
	switch strings.ToLower(prop) {
	case "topic":
		return info.Topic
	case "mode":
		return info.Mode
	case "key":
		return info.Key
	case "limit":
		return info.Limit
	case "ial":
		return boolStr(info.IAL)
	case "inwho":
		return boolStr(info.InWho)
	case "logfile":
		return info.Logfile
	case "stamp":
		return boolStr(info.Stamp)
	case "status":
		return info.Status
	case "cid":
		return itoa(slices.Index(r.h.Networks(), network) + 1)
	case "ibl":
		return list('b')
	case "iel":
		return list('e')
	case "iil":
		return list('I')
	case "iql":
		return list('q')
	case "idle":
		return itoa(info.Idle)
	}
	return ""
}

// NickSelections is a Host that can say which nicks are selected in a
// channel's nicklist, for $snicks and $snick.
type NickSelections interface {
	SelectedNicks(network, channel string) []string
}

func (r *run) selectedNicks(network, channel string) []string {
	if h, ok := r.h.(NickSelections); ok {
		return h.SelectedNicks(network, channel)
	}
	return nil
}

// $snicks is the nicks selected in the active channel's nicklist, as
// nick1,nick2; $snick(#,N) the Nth on channel #, N = 0 how many, and
// with no N all of them like $snicks (checked: "bob,carol").
var nickSelIdents = map[string]builtin{
	"snicks": func(r *run, fr *frame, c *call) (string, error) {
		network, window := r.h.Active()
		return strings.Join(r.selectedNicks(network, window), ","), nil
	},
	"snick": func(r *run, fr *frame, c *call) (string, error) {
		sel := r.selectedNicks(fr.network, c.arg(0))
		if len(c.vals) < 2 {
			return strings.Join(sel, ","), nil
		}
		switch n := int(c.num(1)); {
		case n == 0:
			return itoa(len(sel)), nil
		case n > 0 && n <= len(sel):
			return sel[n-1], nil
		}
		return "", nil
	},
}
