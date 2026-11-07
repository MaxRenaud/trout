package script

import "net/netip"

// DNS is what a /dns found, for on DNS: $dns(N), $iaddress, $naddress
// and $raddress. The event's Nick is the nick looked up, if it was one,
// and its Address is Addr.
type DNS struct {
	// Addr is what was looked up: a host name, an IP address, or the nick
	// if they weren't on the server (mIRC's $address then, checked).
	Addr string
	IPs  []string // a host name's addresses, in the order found
	Name string   // an IP address's host name
}

// reverse reports whether this was an IP address looked up for its name.
func (d *DNS) reverse() bool {
	_, err := netip.ParseAddr(d.Addr)
	return err == nil
}

// results is how many addresses were found: $dns(0).
func (d *DNS) results() int {
	switch {
	case !d.reverse():
		return len(d.IPs)
	case d.Name != "":
		return 1
	}
	return 0
}

// dnsIdents are mIRC's on DNS identifiers, as mIRC fills them (checked):
// $iaddress is the IP address, $naddress the name, $raddress what was
// found, so a name's first address or an address's name; $dns(N) is
// what was looked up, its .ip and .addr the Nth result's address and
// name, its .nick the nick. Outside on DNS they're empty, and $dns(0) 0.
var dnsIdents = map[string]builtin{
	"dns": func(r *run, fr *frame, c *call) (string, error) {
		if !c.hasArgs {
			return "", nil
		}
		var d *DNS
		if fr.ev != nil {
			d = fr.ev.DNS
		}
		i := int(c.num(0))
		switch {
		case d == nil && i == 0:
			return "0", nil
		case d == nil:
			return "", nil
		case i == 0:
			return itoa(d.results()), nil
		case i < 0 || i > d.results():
			return "", nil
		}
		switch c.prop {
		case "ip":
			if d.reverse() {
				return d.Addr, nil
			}
			return d.IPs[i-1], nil
		case "addr":
			if d.reverse() {
				return d.Name, nil
			}
			return d.Addr, nil
		case "nick":
			return fr.ev.Nick, nil
		}
		return d.Addr, nil
	},
	"iaddress": dnsField(func(d *DNS) string {
		if d.reverse() {
			return d.Addr
		}
		return firstIP(d.IPs)
	}),
	"naddress": dnsField(func(d *DNS) string {
		if d.reverse() {
			return d.Name
		}
		return d.Addr
	}),
	"raddress": dnsField(func(d *DNS) string {
		if d.reverse() {
			return d.Name
		}
		return firstIP(d.IPs)
	}),
}

// dnsField is an on DNS identifier, empty outside it.
func dnsField(f func(*DNS) string) builtin {
	return func(r *run, fr *frame, c *call) (string, error) {
		if fr.ev == nil || fr.ev.DNS == nil {
			return "", nil
		}
		return f(fr.ev.DNS), nil
	}
}

func firstIP(s []string) string {
	if len(s) == 0 {
		return ""
	}
	return s[0]
}
