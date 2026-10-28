//go:build integration

package ui

import (
	"bufio"
	"cmp"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MaxRenaud/trout/internal/config"
	"github.com/MaxRenaud/trout/internal/state"
)

// TestControlLive runs the Address Book's Control tab against real
// servers: tester (trout) ops a new channel, tester2 joins and is
// auto-opped after the delay, tester3 (auto-opped too) deops the
// protected tester2 and is deopped for it, then bans tester2 and is
// deopped again with the ban lifted.
//
//	go test -tags integration ./internal/ui -run TestControlLive -v
func TestControlLive(t *testing.T) {
	a, pump := liveApp(t)
	var tn *state.Network
	pump("TroutNet", func() bool {
		for _, n := range a.nets[1:] {
			if n.Name == "TroutNet" && a.conns[n] != nil && n.Nick != "" {
				tn = n
			}
		}
		return tn != nil
	})
	ch := "#ctl" + strconv.FormatInt(time.Now().UnixNano()%1e6, 10)
	a.ircCommand(tn.Status(), "join "+ch)
	var w *state.Window
	pump("ops in "+ch, func() bool {
		w = tn.Find(state.KChannel, ch)
		return w != nil && w.Joined && isOp(w)
	})
	defer a.ircCommand(w, "part "+ch)
	a.memBook.Control = []config.ControlEntry{
		{Mask: "tester2", Kinds: "op", Network: "TroutNet"},
		{Mask: "tester3", Kinds: "o", Channels: []string{ch}},
	}
	has := func(nick, prefix string) func() bool {
		return func() bool { return w.NickIndex(nick) >= 0 && strings.Contains(w.PrefixOf(nick), prefix) }
	}
	lacks := func(nick, prefix string) func() bool {
		return func() bool { return w.NickIndex(nick) >= 0 && !strings.Contains(w.PrefixOf(nick), prefix) }
	}

	t2 := person(t, "tester2")
	start := time.Now()
	t2.send("JOIN " + ch)
	pump("tester2 auto-opped", has("tester2", "@"))
	if d := time.Since(start); d < controlDelay {
		t.Errorf("auto-op after %v, want at least %v", d, controlDelay)
	}

	t3 := person(t, "tester3")
	t3.send("JOIN " + ch)
	pump("tester3 auto-opped", has("tester3", "@"))
	t3.send("MODE " + ch + " -o tester2")
	pump("tester2 re-opped and tester3 deopped", func() bool { return has("tester2", "@")() && lacks("tester3", "@")() })

	a.sendNet(tn, "MODE", ch, "+o", "tester3")
	pump("tester3 opped again", has("tester3", "@"))
	t3.send("MODE " + ch + " +b tester2!*@*")
	pump("tester3 deopped for the ban", lacks("tester3", "@"))
	delete(w.Lists, 'b') // ask the server, as Channel Central does
	a.listFetch[listFetchKey(tn, ch, 'b')] = []state.ListEntry{}
	a.sendNet(tn, "MODE", ch, "+b")
	pump("the ban list", func() bool { _, ok := w.Lists['b']; return ok })
	for _, b := range w.Lists['b'] {
		if b.Mask == "tester2!*@*" {
			t.Errorf("tester2's ban is still set: %+v", w.Lists['b'])
		}
	}
}

// ircPerson is someone else on TroutNet: a raw connection to the dev soju
// as user/TroutNet. It leaves its channels when the test ends, so soju
// doesn't keep them.
type ircPerson struct {
	conn  net.Conn
	chans []string
}

func person(t *testing.T, user string) *ircPerson {
	t.Helper()
	addr := strings.TrimPrefix(cmp.Or(os.Getenv("TROUT_ADDR"), "irc+insecure://127.0.0.1:6667"), "irc+insecure://")
	c, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	p := &ircPerson{conn: c}
	p.send("PASS " + user)
	p.send("NICK " + user)
	p.send("USER " + user + "/TroutNet 0 * :" + user)
	r := bufio.NewReader(c)
	c.SetReadDeadline(time.Now().Add(10 * time.Second))
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("%s logging in: %v", user, err)
		}
		if f := strings.Fields(line); len(f) > 1 && f[1] == "001" {
			break
		}
	}
	c.SetReadDeadline(time.Time{})
	go func() { // answer PINGs, ignore the rest
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			if rest, ok := strings.CutPrefix(line, "PING "); ok {
				p.send("PONG " + strings.TrimSpace(rest))
			}
		}
	}()
	t.Cleanup(func() {
		for _, ch := range p.chans {
			p.send("PART " + ch)
		}
		time.Sleep(200 * time.Millisecond)
		c.Close()
	})
	return p
}

func (p *ircPerson) send(line string) {
	if ch, ok := strings.CutPrefix(line, "JOIN "); ok {
		p.chans = append(p.chans, ch)
	}
	p.conn.Write([]byte(line + "\r\n"))
}

// TestProxyLive logs in to the dev soju through a SOCKS5 proxy (a tiny
// one here), as Options > Connect > Proxy does.
//
//	go test -tags integration ./internal/ui -run TestProxyLive -v
func TestProxyLive(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	used := make(chan string, 4)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go socks5Relay(c, used)
		}
	}()
	cfg, err := config.Load(filepath.Join(t.TempDir(), "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Proxy = config.Proxy{Type: "socks5", Host: "127.0.0.1", Port: ln.Addr().(*net.TCPAddr).Port, Bouncer: true}
	a, pump := liveAppWith(t, Options{Config: cfg,
		Bouncer:  config.Bouncer{Address: cmp.Or(os.Getenv("TROUT_ADDR"), "irc+insecure://127.0.0.1:6667"), User: "tester"},
		Password: "tester",
	})
	pump("soju's networks through the proxy", func() bool { return len(a.nets) > 1 && a.control != nil })
	select {
	case to := <-used:
		t.Logf("proxied to %s", to)
	default:
		t.Fatal("connected without the proxy")
	}
}

// socks5Relay is just enough SOCKS5 for the test: no login, an IPv4 or
// name target, then bytes both ways.
func socks5Relay(c net.Conn, used chan<- string) {
	defer c.Close()
	r := bufio.NewReader(c)
	head := make([]byte, 2)
	io.ReadFull(r, head)
	io.ReadFull(r, make([]byte, head[1]))
	c.Write([]byte{5, 0})
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
	to := net.JoinHostPort(host, strconv.Itoa(int(port[0])<<8|int(port[1])))
	up, err := net.Dial("tcp", to)
	if err != nil {
		c.Write([]byte{5, 1, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	defer up.Close()
	used <- to
	c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
	go io.Copy(up, r)
	io.Copy(c, up)
}
