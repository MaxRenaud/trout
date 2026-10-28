package proxy

import (
	"bufio"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// echoServer answers each line with "echo: line".
func echoServer(t *testing.T) string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				r := bufio.NewReader(c)
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					c.Write([]byte("echo: " + line))
				}
			}()
		}
	}()
	return l.Addr().String()
}

// fakeProxy runs handshake on each connection, which returns where to
// connect (or "" to refuse), then relays bytes both ways. It records what
// each handshake asked for.
func fakeProxy(t *testing.T, handshake func(c net.Conn, r *bufio.Reader) string) (addr string, asked *[]string) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	asked = new([]string)
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			r := bufio.NewReader(c)
			to := handshake(c, r)
			*asked = append(*asked, to)
			if to == "" {
				c.Close()
				continue
			}
			up, err := net.Dial("tcp", to)
			if err != nil {
				c.Close()
				continue
			}
			go func() { io.Copy(up, r); up.Close() }()
			go func() { io.Copy(c, up); c.Close() }()
		}
	}()
	return l.Addr().String(), asked
}

func roundTrip(t *testing.T, d *Dialer, to string) {
	t.Helper()
	c, err := d.DialContext(t.Context(), "tcp", to)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.Write([]byte("hi\n"))
	line, err := bufio.NewReader(c).ReadString('\n')
	if err != nil || line != "echo: hi\n" {
		t.Fatalf("got %q, %v", line, err)
	}
}

func TestSOCKS5(t *testing.T) {
	echo := echoServer(t)
	addr, asked := fakeProxy(t, func(c net.Conn, r *bufio.Reader) string {
		head := make([]byte, 2)
		io.ReadFull(r, head)
		io.ReadFull(r, make([]byte, head[1]))
		c.Write([]byte{5, 2}) // user and password
		io.ReadFull(r, head)
		user := make([]byte, head[1])
		io.ReadFull(r, user)
		pl, _ := r.ReadByte()
		pass := make([]byte, pl)
		io.ReadFull(r, pass)
		if string(user) != "me" || string(pass) != "secret" {
			c.Write([]byte{1, 1})
			return ""
		}
		c.Write([]byte{1, 0})
		req := make([]byte, 4)
		io.ReadFull(r, req)
		var host string
		switch req[3] {
		case 1:
			ip := make([]byte, 4)
			io.ReadFull(r, ip)
			host = net.IP(ip).String()
		case 3:
			l, _ := r.ReadByte()
			name := make([]byte, l)
			io.ReadFull(r, name)
			host = string(name)
		}
		port := make([]byte, 2)
		io.ReadFull(r, port)
		c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
		if host == "localhost" {
			host = "127.0.0.1"
		}
		return net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(port))))
	})
	d := &Dialer{Type: "socks5", Addr: addr, User: "me", Password: "secret"}
	roundTrip(t, d, echo)
	_, port, _ := net.SplitHostPort(echo)
	roundTrip(t, d, "localhost:"+port) // a name the proxy looks up
	if len(*asked) != 2 {
		t.Fatalf("proxy asked %v", *asked)
	}
	d.Password = "wrong"
	if _, err := d.DialContext(t.Context(), "tcp", echo); err == nil || !strings.Contains(err.Error(), "login failed") {
		t.Fatalf("wrong password: %v", err)
	}
}

func TestSOCKS4(t *testing.T) {
	echo := echoServer(t)
	addr, _ := fakeProxy(t, func(c net.Conn, r *bufio.Reader) string {
		req := make([]byte, 8)
		io.ReadFull(r, req)
		user, _ := r.ReadString(0)
		host := net.IP(req[4:8]).String()
		if req[4] == 0 { // SOCKS4a
			name, _ := r.ReadString(0)
			host = strings.TrimSuffix(name, "\x00")
			if host == "localhost" {
				host = "127.0.0.1"
			}
		}
		if user != "me\x00" {
			c.Write([]byte{0, 91, 0, 0, 0, 0, 0, 0})
			return ""
		}
		c.Write([]byte{0, 90, 0, 0, 0, 0, 0, 0})
		return net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(req[2:4]))))
	})
	d := &Dialer{Type: "socks4", Addr: addr, User: "me"}
	roundTrip(t, d, echo)
	_, port, _ := net.SplitHostPort(echo)
	roundTrip(t, d, "localhost:"+port)
	d.User = "someone"
	if _, err := d.DialContext(t.Context(), "tcp", echo); err == nil {
		t.Fatal("a refused SOCKS4 request connected")
	}
}

func TestHTTPConnect(t *testing.T) {
	echo := echoServer(t)
	addr, _ := fakeProxy(t, func(c net.Conn, r *bufio.Reader) string {
		req, err := http.ReadRequest(r)
		if err != nil || req.Method != "CONNECT" {
			return ""
		}
		if user, pass, ok := req.BasicAuth(); !ok && req.Header.Get("Proxy-Authorization") == "" || ok && (user != "me" || pass != "pw") {
			c.Write([]byte("HTTP/1.1 407 Proxy Authentication Required\r\n\r\n"))
			return ""
		}
		c.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n"))
		return req.Host
	})
	d := &Dialer{Type: "http", Addr: addr, User: "me", Password: "pw"}
	roundTrip(t, d, echo)
	d.User = ""
	if _, err := d.DialContext(t.Context(), "tcp", echo); err == nil || !strings.Contains(err.Error(), "407") {
		t.Fatalf("no login: %v", err)
	}
}

func TestDirect(t *testing.T) {
	echo := echoServer(t)
	roundTrip(t, nil, echo)
	roundTrip(t, &Dialer{}, echo)
	// An exception skips the proxy, which isn't even listening.
	roundTrip(t, &Dialer{Type: "socks5", Addr: "127.0.0.1:1", Except: []string{"127.0.0.*"}}, echo)
}
