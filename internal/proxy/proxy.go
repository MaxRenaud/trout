// Package proxy dials through a SOCKS4, SOCKS5 or HTTP (CONNECT) proxy,
// like mIRC's Connect > Proxy options.
package proxy

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"path"
	"strconv"
	"strings"
)

// Dialer connects through a proxy. Type is "socks4", "socks5" or "http";
// a nil Dialer or an empty Type connects directly. Hosts matching one of
// Except (wildcards allowed, like "*.local") connect directly too.
type Dialer struct {
	Type, Addr     string // Addr is the proxy's host:port
	User, Password string
	Except         []string
}

// DialContext connects to addr (host:port) through the proxy.
func (d *Dialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	var direct net.Dialer
	if d == nil || d.Type == "" || d.excepted(addr) {
		return direct.DialContext(ctx, network, addr)
	}
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	port, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil {
		return nil, fmt.Errorf("bad port in %q", addr)
	}
	c, err := direct.DialContext(ctx, "tcp", d.Addr)
	if err != nil {
		return nil, fmt.Errorf("proxy %s: %w", d.Addr, err)
	}
	// The handshake gives up with the context.
	stop := context.AfterFunc(ctx, func() { c.Close() })
	defer stop()
	switch d.Type {
	case "socks4":
		err = d.socks4(c, host, uint16(port))
	case "socks5":
		err = d.socks5(c, host, uint16(port))
	case "http":
		err = d.connect(c, addr)
	default:
		err = fmt.Errorf("unknown proxy type %q", d.Type)
	}
	if err != nil {
		c.Close()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("proxy %s: %w", d.Addr, err)
	}
	return c, nil
}

func (d *Dialer) excepted(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	host = strings.ToLower(host)
	for _, e := range d.Except {
		if ok, _ := path.Match(strings.ToLower(e), host); ok {
			return true
		}
	}
	return false
}

// socks4 is SOCKS4, or SOCKS4a for a host name: the proxy looks it up.
func (d *Dialer) socks4(c net.Conn, host string, port uint16) error {
	req := []byte{4, 1, byte(port >> 8), byte(port)}
	ip := net.ParseIP(host).To4()
	if ip == nil {
		ip = net.IPv4(0, 0, 0, 1).To4() // SOCKS4a: the name follows the user
	}
	req = append(append(append(req, ip...), d.User...), 0)
	if net.ParseIP(host).To4() == nil {
		req = append(append(req, host...), 0)
	}
	if _, err := c.Write(req); err != nil {
		return err
	}
	var resp [8]byte
	if _, err := io.ReadFull(c, resp[:]); err != nil {
		return err
	}
	if resp[1] != 90 {
		return fmt.Errorf("SOCKS4 refused (code %d)", resp[1])
	}
	return nil
}

// socks5 is SOCKS5 (RFC 1928), with a user name and password (RFC 1929)
// when there is one.
func (d *Dialer) socks5(c net.Conn, host string, port uint16) error {
	methods := []byte{0}
	if d.User != "" {
		methods = []byte{0, 2}
	}
	if _, err := c.Write(append([]byte{5, byte(len(methods))}, methods...)); err != nil {
		return err
	}
	var pick [2]byte
	if _, err := io.ReadFull(c, pick[:]); err != nil {
		return err
	}
	switch pick[1] {
	case 0:
	case 2:
		if len(d.User) > 255 || len(d.Password) > 255 {
			return errors.New("user name or password too long for SOCKS5")
		}
		auth := append(append([]byte{1, byte(len(d.User))}, d.User...), byte(len(d.Password)))
		if _, err := c.Write(append(auth, d.Password...)); err != nil {
			return err
		}
		var ok [2]byte
		if _, err := io.ReadFull(c, ok[:]); err != nil {
			return err
		}
		if ok[1] != 0 {
			return errors.New("SOCKS5 login failed")
		}
	default:
		return errors.New("SOCKS5 proxy wants a login trout doesn't have")
	}
	req := []byte{5, 1, 0}
	switch ip := net.ParseIP(host); {
	case ip.To4() != nil:
		req = append(append(req, 1), ip.To4()...)
	case ip != nil:
		req = append(append(req, 4), ip.To16()...)
	case len(host) > 255:
		return errors.New("host name too long for SOCKS5")
	default:
		req = append(append(req, 3, byte(len(host))), host...)
	}
	req = binary.BigEndian.AppendUint16(req, port)
	if _, err := c.Write(req); err != nil {
		return err
	}
	var head [4]byte
	if _, err := io.ReadFull(c, head[:]); err != nil {
		return err
	}
	if head[1] != 0 {
		return fmt.Errorf("SOCKS5 refused (code %d)", head[1])
	}
	skip := 0 // the bound address and port, which trout doesn't need
	switch head[3] {
	case 1:
		skip = 4 + 2
	case 4:
		skip = 16 + 2
	case 3:
		var l [1]byte
		if _, err := io.ReadFull(c, l[:]); err != nil {
			return err
		}
		skip = int(l[0]) + 2
	default:
		return errors.New("bad SOCKS5 reply")
	}
	_, err := io.ReadFull(c, make([]byte, skip))
	return err
}

// connect is an HTTP proxy's CONNECT tunnel. It reads the reply a byte at
// a time so nothing after it is taken from the connection.
func (d *Dialer) connect(c net.Conn, addr string) error {
	req := "CONNECT " + addr + " HTTP/1.1\r\nHost: " + addr + "\r\n"
	if d.User != "" {
		req += "Proxy-Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte(d.User+":"+d.Password)) + "\r\n"
	}
	if _, err := c.Write([]byte(req + "\r\n")); err != nil {
		return err
	}
	var head []byte
	b := make([]byte, 1)
	for !strings.HasSuffix(string(head), "\r\n\r\n") {
		if len(head) > 8192 {
			return errors.New("HTTP proxy reply too long")
		}
		if _, err := io.ReadFull(c, b); err != nil {
			return err
		}
		head = append(head, b[0])
	}
	resp, err := http.ReadResponse(bufio.NewReader(strings.NewReader(string(head))), nil)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP proxy: %s", resp.Status)
	}
	return nil
}
