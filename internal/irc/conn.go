// Package irc is a small IRC client connection: TLS with key pinning,
// registration with CAP negotiation and SASL PLAIN, binding to a soju
// network, a read loop that answers PINGs, and thread-safe sending. It
// knows nothing about the UI.
package irc

import (
	"bufio"
	"cmp"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/ergochat/irc-go/ircmsg"
)

type Config struct {
	Addr     string // host:port
	TLS      bool
	Pin      string // trusted key fingerprint (see Fingerprint) for a certificate no CA signed
	Nick     string
	User     string // SASL and USER name
	Password string // SASL PLAIN password; empty skips SASL
	// ClientCert, on a TLS connection, is presented to the server; with
	// External, the login is SASL EXTERNAL (that certificate) instead of
	// PLAIN, when the server offers it.
	ClientCert *tls.Certificate
	External   bool
	Caps       []string // capabilities to request when the server offers them
	Bind       string   // soju network ID to bind to (soju.im/bouncer-networks)

	// Debug, if set, sees every raw line; dir is "<-" or "->".
	Debug func(dir, line string)

	// Liveness, for a server that vanishes without closing the connection
	// (its machine gone): whenever we send something, a PING with a token
	// follows, and Run gives up with ErrNoReply when its PONG doesn't come
	// within AfterSend; with nothing heard for Idle, the same with
	// PingWait. Lines still arriving while the PONG is due (a backlog over
	// a slow link) buy another AfterSend each; a frozen server's last lines
	// stop coming, so it's caught all the same. Zero means 60s, 15s and 20s.
	Idle, AfterSend, PingWait time.Duration

	// Dial, if set, makes the connection (through a proxy, say).
	Dial func(ctx context.Context, network, addr string) (net.Conn, error)
}

type Conn struct {
	cfg Config
	nc  net.Conn
	r   *bufio.Reader

	mu sync.Mutex // serializes writes

	// Set during registration and read-only afterwards.
	Caps map[string]bool // acknowledged capabilities
	Nick string
	// early holds what arrived during registration (RPL_WELCOME and
	// friends) for Run to hand over first.
	early []ircmsg.Message
	// Mech is the SASL mechanism used to log in: "PLAIN", "EXTERNAL", or "".
	Mech string

	live     sync.Mutex // guards the rest, shared by Run and Raw
	running  bool       // Run has started: liveness applies
	sent     bool       // sent something before Run started
	token    string     // our PING's, while its PONG is due
	due      time.Time  // when it's too late
	lastRead time.Time
	pings    int
	partial  string // a line cut off by a read deadline
}

// ErrNoReply is a server that stopped answering, even PINGs.
var ErrNoReply = errors.New("no answer, even to a ping")

func (c *Conn) timing() (idle, afterSend, pingWait time.Duration) {
	return cmp.Or(c.cfg.Idle, 60*time.Second), cmp.Or(c.cfg.AfterSend, 15*time.Second), cmp.Or(c.cfg.PingWait, 20*time.Second)
}

// Dial connects and registers. It returns once the server has sent
// RPL_WELCOME.
func Dial(ctx context.Context, cfg Config) (*Conn, error) {
	dial := cfg.Dial
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	nc, err := dial(ctx, "tcp", cfg.Addr)
	if err != nil {
		return nil, err
	}
	if cfg.TLS {
		tc := tls.Client(nc, cfg.TLSConfig())
		if err := tc.HandshakeContext(ctx); err != nil {
			nc.Close()
			return nil, err
		}
		nc = tc
	}
	c := &Conn{cfg: cfg, nc: nc, r: bufio.NewReader(nc), Caps: map[string]bool{}, Nick: cfg.Nick}
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(30 * time.Second)
	}
	nc.SetDeadline(deadline)
	if err := c.register(); err != nil {
		nc.Close()
		return nil, fmt.Errorf("registering with %s: %w", cfg.Addr, err)
	}
	nc.SetDeadline(time.Time{})
	c.sent = false // registration's own lines: nothing's due
	return c, nil
}

func (c *Conn) register() error {
	c.Send("CAP", "LS", "302")
	c.Send("NICK", c.cfg.Nick)
	c.Send("USER", c.cfg.User, "0", "*", "trout")
	var offered []string
	mechs := map[string]bool{} // sasl=PLAIN,EXTERNAL; no list means any
	for {
		m, err := c.read()
		if err != nil {
			return err
		}
		switch m.Command {
		case "PING":
			c.Send("PONG", m.Params...)
		case "CAP":
			if len(m.Params) < 3 {
				continue
			}
			list := strings.Fields(m.Params[len(m.Params)-1])
			switch m.Params[1] {
			case "LS":
				for _, cp := range list {
					name, value, _ := strings.Cut(cp, "=")
					offered = append(offered, name)
					if name == "sasl" {
						for m := range strings.SplitSeq(value, ",") {
							mechs[strings.ToUpper(m)] = true
						}
					}
				}
				if m.Params[2] == "*" {
					continue // more LS lines follow
				}
				var req []string
				for _, cp := range c.cfg.Caps {
					if slices.Contains(offered, cp) {
						req = append(req, cp)
					}
				}
				switch {
				case !slices.Contains(offered, "sasl"):
				case c.cfg.External && c.cfg.ClientCert != nil && (mechs["EXTERNAL"] || mechs[""]):
					c.Mech = "EXTERNAL"
				case c.cfg.Password != "":
					c.Mech = "PLAIN"
				}
				if c.Mech != "" {
					req = append(req, "sasl")
				}
				if len(req) == 0 {
					c.endCaps()
				} else {
					c.Send("CAP", "REQ", strings.Join(req, " "))
				}
			case "ACK":
				for _, cp := range list {
					c.Caps[cp] = true
				}
				if c.Caps["sasl"] && c.Mech != "" {
					c.Send("AUTHENTICATE", c.Mech)
				} else {
					c.endCaps()
				}
			case "NAK":
				c.endCaps()
			}
		case "AUTHENTICATE":
			if c.Mech == "EXTERNAL" { // the certificate says who we are
				c.Send("AUTHENTICATE", "+")
				break
			}
			creds := "\x00" + c.cfg.User + "\x00" + c.cfg.Password
			c.Send("AUTHENTICATE", base64.StdEncoding.EncodeToString([]byte(creds)))
		case "903": // RPL_SASLSUCCESS
			c.endCaps()
		case "902", "904", "905", "906":
			return fmt.Errorf("%w: %s", ErrLogin, last(m.Params))
		case "433": // nick in use
			c.Nick += "_"
			c.Send("NICK", c.Nick)
		case "ERROR", "FAIL":
			return errors.New(strings.Join(m.Params, " "))
		case "001":
			c.Nick = m.Params[0]
			c.early = append(c.early, m)
			return nil
		default:
			c.early = append(c.early, m)
		}
	}
}

func (c *Conn) endCaps() {
	if c.cfg.Bind != "" {
		c.Send("BOUNCER", "BIND", c.cfg.Bind)
	}
	c.Send("CAP", "END")
}

// Run reads messages until the connection fails, answering PINGs and
// passing everything else to handle. It returns the read error.
func (c *Conn) Run(handle func(ircmsg.Message)) error {
	for _, m := range c.early {
		handle(m)
	}
	c.early = nil
	idle, afterSend, pingWait := c.timing()
	c.live.Lock()
	c.running, c.lastRead = true, time.Now()
	if c.sent { // sent before Run started
		if err := c.pingLocked(afterSend); err != nil {
			c.live.Unlock()
			return err
		}
	}
	c.live.Unlock()
	// due is when to give up on the PONG: its deadline, or later while
	// lines keep coming.
	due := func() time.Time {
		if p := c.lastRead.Add(afterSend); p.After(c.due) {
			return p
		}
		return c.due
	}
	for {
		c.live.Lock()
		deadline := c.lastRead.Add(idle)
		if c.token != "" {
			deadline = due()
		}
		c.live.Unlock()
		c.nc.SetReadDeadline(deadline)
		m, err := c.read()
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			c.live.Lock()
			switch {
			case c.token != "" && !time.Now().Before(due()):
				c.live.Unlock()
				return ErrNoReply
			case c.token == "" && !time.Now().Before(c.lastRead.Add(idle)): // quiet: still there?
				err = c.pingLocked(pingWait)
			default:
				err = nil // a deadline that moved on meanwhile
			}
			c.live.Unlock()
			if err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		c.live.Lock()
		c.lastRead = time.Now()
		ours := m.Command == "PONG" && c.token != "" && len(m.Params) > 0 && m.Params[len(m.Params)-1] == c.token
		if ours {
			c.token = ""
		}
		c.live.Unlock()
		if ours {
			continue
		}
		if m.Command == "PING" {
			c.Send("PONG", m.Params...)
			continue
		}
		handle(m)
	}
}

func (c *Conn) read() (ircmsg.Message, error) {
	for {
		line, err := c.r.ReadString('\n')
		c.live.Lock()
		line, c.partial = c.partial+line, ""
		if err != nil {
			c.partial = line // a deadline can cut a line short; keep its start
		}
		c.live.Unlock()
		if err != nil {
			return ircmsg.Message{}, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			continue
		}
		if c.cfg.Debug != nil {
			c.cfg.Debug("<-", line)
		}
		m, err := ircmsg.ParseLine(line)
		if err != nil {
			continue // skip malformed lines rather than dropping the connection
		}
		return m, nil
	}
}

// Send writes a message; the last parameter may contain spaces.
func (c *Conn) Send(command string, params ...string) error {
	m := ircmsg.MakeMessage(nil, "", command, params...)
	line, err := m.Line()
	if err != nil {
		return err
	}
	return c.Raw(line)
}

// Raw writes a line as is, for /quote and commands trout doesn't know.
func (c *Conn) Raw(line string) error {
	line = strings.TrimRight(line, "\r\n")
	if c.cfg.Debug != nil {
		c.cfg.Debug("->", line)
	}
	if err := c.write(line); err != nil {
		return err
	}
	// Something sent: a dead server takes writes into the OS's buffer for
	// minutes, so ask it to answer (unless we're already asking).
	c.live.Lock()
	defer c.live.Unlock()
	switch {
	case !c.running:
		c.sent = true // Run asks when it starts
	case c.token == "" && !strings.HasPrefix(line, "PONG "):
		_, afterSend, _ := c.timing()
		return c.pingLocked(afterSend)
	}
	return nil
}

func (c *Conn) write(line string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err := c.nc.Write([]byte(line + "\r\n"))
	return err
}

// pingLocked sends a PING whose PONG must come within wait. c.live is held.
func (c *Conn) pingLocked(wait time.Duration) error {
	c.pings++
	c.token = fmt.Sprintf("trout-%d", c.pings)
	c.due = time.Now().Add(wait)
	c.nc.SetReadDeadline(c.due) // a Read in progress waits no longer than that
	line := "PING " + c.token
	if c.cfg.Debug != nil {
		c.cfg.Debug("->", line)
	}
	return c.write(line)
}

func (c *Conn) Close() error { return c.nc.Close() }

func last(params []string) string {
	if len(params) == 0 {
		return ""
	}
	return params[len(params)-1]
}

// ISupport parses an RPL_ISUPPORT message's tokens, without the leading
// nick and trailing "are supported" text.
func ISupport(m ircmsg.Message) map[string]string {
	out := map[string]string{}
	if len(m.Params) < 3 {
		return out
	}
	for _, tok := range m.Params[1 : len(m.Params)-1] {
		k, v, _ := strings.Cut(tok, "=")
		out[k] = v
	}
	return out
}

// ParsePrefix splits an ISUPPORT PREFIX value like "(ov)@+" into its modes
// and prefix characters.
func ParsePrefix(v string) (modes, prefixes string, ok bool) {
	inner, rest, found := strings.Cut(strings.TrimPrefix(v, "("), ")")
	if !found || len(inner) != len(rest) {
		return "", "", false
	}
	return inner, rest, true
}

// BouncerNetwork is one entry of soju's network list. Deleted means soju
// removed it.
type BouncerNetwork struct {
	ID      string
	Attrs   map[string]string
	Deleted bool
}

// ParseBouncerNetwork reads a "BOUNCER NETWORK <netid> <attributes>"
// message (soju.im/bouncer-networks).
func ParseBouncerNetwork(m ircmsg.Message) (BouncerNetwork, bool) {
	if m.Command != "BOUNCER" || len(m.Params) < 3 || !strings.EqualFold(m.Params[0], "NETWORK") {
		return BouncerNetwork{}, false
	}
	b := BouncerNetwork{ID: m.Params[1], Attrs: map[string]string{}}
	if m.Params[2] == "*" {
		b.Deleted = true
		return b, true
	}
	for kv := range strings.SplitSeq(m.Params[2], ";") {
		k, v, _ := strings.Cut(kv, "=")
		b.Attrs[k] = ircmsg.UnescapeTagValue(v)
	}
	return b, true
}
