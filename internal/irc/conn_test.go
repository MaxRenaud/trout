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

// TestDialRegisters runs Dial against a scripted server: CAP negotiation,
// SASL PLAIN, BOUNCER BIND before CAP END, then RPL_WELCOME.
func TestDialRegisters(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	got := make(chan []string, 1)
	go func() {
		nc, err := ln.Accept()
		if err != nil {
			return
		}
		defer nc.Close()
		r := bufio.NewReader(nc)
		var seen []string
		write := func(s string) { nc.Write([]byte(s + "\r\n")) }
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				got <- seen
				return
			}
			line = strings.TrimSpace(line)
			seen = append(seen, line)
			switch {
			case line == "CAP LS 302":
				write(":soju CAP * LS :sasl server-time soju.im/bouncer-networks")
			case strings.HasPrefix(line, "CAP REQ"):
				write(":soju CAP * ACK :" + strings.TrimPrefix(strings.TrimPrefix(line, "CAP REQ "), ":"))
			case line == "AUTHENTICATE PLAIN":
				write("AUTHENTICATE +")
			case strings.HasPrefix(line, "AUTHENTICATE "):
				write(":soju 903 trout :SASL authentication successful")
			case line == "CAP END":
				write(":soju 001 trout :Welcome to soju, trout")
				got <- seen
				return
			}
		}
	}()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	c, err := Dial(ctx, Config{Addr: ln.Addr().String(), Nick: "trout", User: "trout", Password: "pw",
		Caps: []string{"server-time", "soju.im/bouncer-networks", "batch"}, Bind: "7"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	seen := <-got

	if !c.Caps["server-time"] || !c.Caps["sasl"] || c.Caps["batch"] {
		t.Errorf("acknowledged caps = %v; want server-time and sasl but not batch, which wasn't offered", c.Caps)
	}
	creds := base64.StdEncoding.EncodeToString([]byte("\x00trout\x00pw"))
	want := []string{"AUTHENTICATE " + creds, "BOUNCER BIND 7", "CAP END"}
	if tail := seen[len(seen)-3:]; strings.Join(tail, "|") != strings.Join(want, "|") {
		t.Errorf("last lines sent = %q, want %q", tail, want)
	}
	if c.Nick != "trout" || len(c.early) != 1 || c.early[0].Command != "001" {
		t.Errorf("after registration nick = %q, early = %v", c.Nick, c.early)
	}
}

func TestParseBouncerNetwork(t *testing.T) {
	m, _ := ircmsg.ParseLine(`BOUNCER NETWORK 42 name=Libera\sChat;host=irc.libera.chat;state=connected`)
	b, ok := ParseBouncerNetwork(m)
	if !ok || b.ID != "42" || b.Attrs["name"] != "Libera Chat" || b.Attrs["state"] != "connected" {
		t.Errorf("ParseBouncerNetwork = %+v, %v", b, ok)
	}
	m, _ = ircmsg.ParseLine("BOUNCER NETWORK 42 *")
	if b, _ := ParseBouncerNetwork(m); !b.Deleted {
		t.Error("a * attribute list should mean the network was deleted")
	}
	if modes, prefixes, ok := ParsePrefix("(qaohv)~&@%+"); !ok || modes != "qaohv" || prefixes != "~&@%+" {
		t.Errorf("ParsePrefix = %q %q %v", modes, prefixes, ok)
	}
}

// TestLiveness: a server that goes silent without closing the connection
// (its machine gone) is noticed: after a quiet spell, or sooner after we
// send something, Run pings, and gives up when that goes unanswered. One
// that answers the ping stays.
func TestLiveness(t *testing.T) {
	for _, c := range []struct {
		name    string
		answers bool          // PONGs
		backlog time.Duration // sends lines this long (one every 30ms) before PONGing or freezing
		send    bool          // we send something first
		want    error
	}{
		{"silent", false, 0, false, ErrNoReply},
		{"silent after a send", false, 0, true, ErrNoReply},
		// Lines a server sent before it froze aren't proof it's there.
		{"talks, then freezes", false, 400 * time.Millisecond, true, ErrNoReply},
		// A backlog over a slow link, longer than AfterSend, before the PONG.
		{"slow backlog, then answers", true, 400 * time.Millisecond, true, nil},
		{"answers pings", true, 0, false, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close()
			go func() {
				nc, err := ln.Accept()
				if err != nil {
					return
				}
				defer nc.Close()
				r := bufio.NewReader(nc)
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					switch line = strings.TrimSpace(line); {
					case line == "CAP LS 302":
						nc.Write([]byte(":soju CAP * LS :\r\n"))
					case line == "CAP END":
						nc.Write([]byte(":soju 001 trout :Welcome\r\n"))
					case strings.HasPrefix(line, "PING "):
						for end := time.Now().Add(c.backlog); time.Now().Before(end); time.Sleep(30 * time.Millisecond) {
							nc.Write([]byte(":soju NOTICE trout :backlog\r\n"))
						}
						if c.answers {
							nc.Write([]byte(":soju PONG soju " + strings.TrimPrefix(line, "PING ") + "\r\n"))
						}
					}
				}
			}()
			idle := time.Second
			if c.send {
				idle = time.Hour // only the send should make it check
			}
			conn, err := Dial(t.Context(), Config{Addr: ln.Addr().String(), Nick: "trout", User: "trout",
				Idle: idle, AfterSend: 150 * time.Millisecond, PingWait: 200 * time.Millisecond})
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			done := make(chan error, 1)
			go func() { done <- conn.Run(func(ircmsg.Message) {}) }()
			if c.send {
				conn.Send("PRIVMSG", "bob", "hello?")
			}
			select {
			case err := <-done:
				if c.want == nil || !errors.Is(err, c.want) {
					t.Fatalf("Run ended with %v, want %v", err, c.want)
				}
			case <-time.After(3 * time.Second):
				if c.want != nil {
					t.Fatal("still waiting on a silent server")
				}
			}
		})
	}
}
