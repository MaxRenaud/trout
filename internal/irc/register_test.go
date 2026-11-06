package irc

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/ergochat/irc-go/ircmsg"
)

// scripted runs a server that answers each line trout sends with what
// answer returns (lines without CRLF), and hands back everything it got.
func scripted(t *testing.T, answer func(line string) []string) (addr string, got <-chan []string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	ch := make(chan []string, 1)
	go func() {
		nc, err := ln.Accept()
		if err != nil {
			return
		}
		defer nc.Close()
		r := bufio.NewReader(nc)
		var seen []string
		defer func() { ch <- seen }()
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimSpace(line)
			seen = append(seen, line)
			for _, out := range answer(line) {
				if out == "" { // hang up
					return
				}
				nc.Write([]byte(out + "\r\n"))
			}
		}
	}()
	return ln.Addr().String(), ch
}

func quickDial(t *testing.T, cfg Config) (*Conn, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	start := time.Now()
	c, err := Dial(ctx, cfg)
	if time.Since(start) > 2*time.Second {
		t.Errorf("Dial took %v: it waited out the deadline", time.Since(start))
	}
	return c, err
}

// An empty user name (no --user, no saved bouncer) can't be sent as USER:
// Dial says so instead of waiting for a welcome that never comes.
func TestRegisterBadName(t *testing.T) {
	for _, user := range []string{"", "max renaud"} {
		addr, _ := scripted(t, func(string) []string { return nil })
		_, err := quickDial(t, Config{Addr: addr, Nick: "trout", User: user})
		if !errors.Is(err, ErrBadName) {
			t.Errorf("user %q: Dial = %v, want ErrBadName", user, err)
		}
	}
}

// 432 during registration ends it with the server's reason.
func TestRegisterErroneousNick(t *testing.T) {
	addr, _ := scripted(t, func(line string) []string {
		if nick, ok := strings.CutPrefix(line, "NICK "); ok {
			return []string{":srv 432 * " + nick + " :Erroneous nickname"}
		}
		return nil
	})
	_, err := quickDial(t, Config{Addr: addr, Nick: "1trout", User: "trout"})
	if err == nil || !strings.Contains(err.Error(), "Erroneous nickname") {
		t.Fatalf("Dial = %v, want the 432's reason", err)
	}
}

// A NAK refuses one cap of the request: trout asks again without it and
// still logs in with SASL.
func TestRegisterNAK(t *testing.T) {
	addr, got := scripted(t, func(line string) []string {
		switch {
		case line == "CAP LS 302":
			return []string{":srv CAP * LS :sasl batch server-time"}
		case strings.HasPrefix(line, "CAP REQ "):
			caps := strings.TrimPrefix(strings.TrimPrefix(line, "CAP REQ "), ":")
			if strings.Contains(caps, "batch") {
				return []string{":srv CAP * NAK :" + caps}
			}
			return []string{":srv CAP * ACK :" + caps}
		case line == "AUTHENTICATE PLAIN":
			return []string{"AUTHENTICATE +"}
		case strings.HasPrefix(line, "AUTHENTICATE "):
			return []string{":srv 903 trout :SASL authentication successful"}
		case line == "CAP END":
			return []string{":srv 001 trout :Welcome", ""}
		}
		return nil
	})
	c, err := quickDial(t, Config{Addr: addr, Nick: "trout", User: "trout", Password: "pw", Caps: []string{"batch", "server-time"}})
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	seen := strings.Join(<-got, "\n")
	if !c.Caps["sasl"] || !c.Caps["server-time"] || c.Caps["batch"] || c.Mech != "PLAIN" ||
		!strings.Contains(seen, "CAP REQ batch\nCAP REQ server-time\nCAP REQ sasl\nAUTHENTICATE PLAIN") {
		t.Fatalf("caps %v, mech %q; sent:\n%s", c.Caps, c.Mech, seen)
	}
}

func TestSASLChunks(t *testing.T) {
	for _, c := range []struct{ n, chunks, last int }{{0, 1, 1}, {10, 1, 10}, {399, 1, 399}, {400, 2, 1}, {800, 3, 1}, {801, 3, 1}} {
		got := saslChunks(strings.Repeat("a", c.n))
		if len(got) != c.chunks || len(got[len(got)-1]) != c.last || strings.Join(got, "") != strings.Repeat("a", c.n)+map[bool]string{true: "+"}[c.n%400 == 0] {
			t.Errorf("%d bytes: %d chunks, last %q", c.n, len(got), got[len(got)-1])
		}
	}
	// A login whose payload is exactly 400 bytes ends with "+".
	pw := strings.Repeat("p", 300-len("\x00trout\x00"))
	addr, got := scripted(t, func(line string) []string {
		switch {
		case line == "CAP LS 302":
			return []string{":srv CAP * LS :sasl"}
		case strings.HasPrefix(line, "CAP REQ "):
			return []string{":srv CAP * ACK :sasl"}
		case line == "AUTHENTICATE PLAIN":
			return []string{"AUTHENTICATE +"}
		case line == "AUTHENTICATE +":
			return []string{":srv 903 trout :SASL authentication successful"}
		case line == "CAP END":
			return []string{":srv 001 trout :Welcome", ""}
		}
		return nil
	})
	c, err := quickDial(t, Config{Addr: addr, Nick: "trout", User: "trout", Password: pw})
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	var auth []string
	for _, l := range <-got {
		if a, ok := strings.CutPrefix(l, "AUTHENTICATE "); ok && a != "PLAIN" {
			auth = append(auth, a)
		}
	}
	if len(auth) != 2 || len(auth[0]) != 400 || auth[1] != "+" || auth[0] != base64.StdEncoding.EncodeToString([]byte("\x00trout\x00"+pw)) {
		t.Fatalf("AUTHENTICATE sent %q", auth)
	}
}

func TestISupport(t *testing.T) {
	m, _ := ircmsg.ParseLine(":srv 005 max CHANTYPES=# MONITOR=100 -EXCEPTS -MONITOR :are supported by this server")
	set, unset := ISupport(m)
	if set["CHANTYPES"] != "#" || len(set) != 1 || strings.Join(unset, " ") != "EXCEPTS MONITOR" {
		t.Fatalf("set %v, unset %q", set, unset)
	}
}

// A line far longer than IRC allows is dropped; the next one still comes.
func TestLongLine(t *testing.T) {
	addr, _ := scripted(t, func(line string) []string {
		switch line {
		case "CAP LS 302":
			return []string{":srv CAP * LS :"}
		case "CAP END":
			return []string{":srv 001 trout :Welcome",
				":srv NOTICE trout :" + strings.Repeat("x", 5*maxLine),
				":srv NOTICE trout :after"}
		}
		return nil
	})
	c, err := quickDial(t, Config{Addr: addr, Nick: "trout", User: "trout"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	msgs := make(chan ircmsg.Message, 4)
	go c.Run(func(m ircmsg.Message) { msgs <- m })
	<-msgs // 001
	select {
	case m := <-msgs:
		if last(m.Params) != "after" {
			t.Fatalf("got a %d-byte line", len(last(m.Params)))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the line after the long one never came")
	}
}
