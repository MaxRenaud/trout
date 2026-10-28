package irc

import (
	"bufio"
	"context"
	"encoding/base64"
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
