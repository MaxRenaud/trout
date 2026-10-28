// Package irc is a small IRC client connection: registration with CAP
// negotiation and SASL PLAIN, binding to a soju network, a read loop that
// answers PINGs, and thread-safe sending. It knows nothing about the UI.
package irc

import (
	"bufio"
	"context"
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
	Addr     string // host:port, plain-text TCP
	Nick     string
	User     string   // SASL and USER name
	Password string   // SASL PLAIN password; empty skips SASL
	Caps     []string // capabilities to request when the server offers them
	Bind     string   // soju network ID to bind to (soju.im/bouncer-networks)

	// Debug, if set, sees every raw line; dir is "<-" or "->".
	Debug func(dir, line string)
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
}

// Dial connects and registers. It returns once the server has sent
// RPL_WELCOME.
func Dial(ctx context.Context, cfg Config) (*Conn, error) {
	var d net.Dialer
	nc, err := d.DialContext(ctx, "tcp", cfg.Addr)
	if err != nil {
		return nil, err
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
	return c, nil
}

func (c *Conn) register() error {
	c.Send("CAP", "LS", "302")
	c.Send("NICK", c.cfg.Nick)
	c.Send("USER", c.cfg.User, "0", "*", "trout")
	var offered []string
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
					name, _, _ := strings.Cut(cp, "=")
					offered = append(offered, name)
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
				if c.cfg.Password != "" && slices.Contains(offered, "sasl") {
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
				if c.Caps["sasl"] {
					c.Send("AUTHENTICATE", "PLAIN")
				} else {
					c.endCaps()
				}
			case "NAK":
				c.endCaps()
			}
		case "AUTHENTICATE":
			creds := "\x00" + c.cfg.User + "\x00" + c.cfg.Password
			c.Send("AUTHENTICATE", base64.StdEncoding.EncodeToString([]byte(creds)))
		case "903": // RPL_SASLSUCCESS
			c.endCaps()
		case "902", "904", "905", "906":
			return errors.New("SASL login failed: " + last(m.Params))
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
	for {
		m, err := c.read()
		if err != nil {
			return err
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
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err := c.nc.Write([]byte(line + "\r\n"))
	return err
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
