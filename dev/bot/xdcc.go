package main

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ergochat/irc-go/ircmsg"

	"github.com/MaxRenaud/trout/internal/irc"
)

// The xdcc persona is a tiny XDCC bot for trying DCC Get: `/msg xdcc xdcc
// send #1` gets you a 3 MB test file, which it can resume. Each offer gets
// its own port from a small range compose publishes on 127.0.0.1, so two
// downloads (or a download and a resume) don't mix; the offers say
// 127.0.0.1, which is why trout warns that they point at this computer.

const packName = "trout-test.bin"

var packSize = int64(3 << 20)

// packByte is the test file's content: easy to check, not all zeros.
func packByte(i int64) byte { return byte(i * 7 % 251) }

// xdccPorts is how many ports, from the first one, offers rotate through.
const xdccPorts = 10

type xdcc struct {
	ip      net.IP
	first   int
	mu      sync.Mutex
	next    int           // the port for the next offer
	offsets map[int]int64 // where each port's next connection starts (DCC RESUME)
	chats   map[int]bool  // ports offered for a chat rather than the file
}

func runXDCC(addr string, ip string, port int) {
	x := &xdcc{ip: net.ParseIP(ip).To4(), first: port, next: port, offsets: map[int]int64{}, chats: map[int]bool{}}
	for p := port; p < port+xdccPorts; p++ {
		ln, err := net.Listen("tcp", fmt.Sprintf(":%d", p))
		if err != nil {
			log.Printf("xdcc: %v", err)
			return
		}
		go x.serve(ln, p)
	}
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		c, err := irc.Dial(ctx, irc.Config{Addr: addr, Nick: "xdcc", User: "xdcc"})
		cancel()
		if err == nil {
			err = c.Run(func(m ircmsg.Message) { x.handle(c, m) })
		}
		log.Printf("xdcc on %s: %v; reconnecting in 5s", addr, err)
		time.Sleep(5 * time.Second)
	}
}

func (x *xdcc) handle(c *irc.Conn, m ircmsg.Message) {
	if m.Command != "PRIVMSG" || len(m.Params) < 2 {
		return
	}
	nick, text := m.Nick(), m.Params[1]
	if inner, ok := strings.CutPrefix(text, "\x01DCC RESUME "); ok {
		f := strings.Fields(strings.TrimSuffix(inner, "\x01"))
		if len(f) == 3 {
			pos, _ := strconv.ParseInt(f[2], 10, 64)
			port, _ := strconv.Atoi(f[1])
			x.mu.Lock()
			x.offsets[port] = pos
			x.mu.Unlock()
			c.Send("PRIVMSG", nick, fmt.Sprintf("\x01DCC ACCEPT %s %s %d\x01", f[0], f[1], pos))
		}
		return
	}
	switch f := strings.Fields(strings.ToLower(text)); {
	case len(f) >= 2 && f[0] == "xdcc" && f[1] == "list":
		c.Send("NOTICE", nick, fmt.Sprintf("#1  %d MB  %s", packSize>>20, packName))
	case len(f) == 1 && f[0] == "chat": // a DCC chat that echoes what you say
		port := x.take(true)
		ip := binary.BigEndian.Uint32(x.ip)
		c.Send("PRIVMSG", nick, fmt.Sprintf("\x01DCC CHAT chat %d %d\x01", ip, port))
	case len(f) >= 3 && f[0] == "xdcc" && f[1] == "send":
		port := x.take(false)
		ip := binary.BigEndian.Uint32(x.ip)
		c.Send("PRIVMSG", nick, fmt.Sprintf("\x01DCC SEND %s %d %d %d\x01", packName, ip, port, packSize))
	case strings.HasPrefix(text, "\x01"):
		ctcpReply(c, nick, text)
	default:
		c.Send("NOTICE", nick, "Try: /msg xdcc xdcc list, xdcc send #1, or chat")
	}
}

// take picks the port for the next offer.
func (x *xdcc) take(chat bool) int {
	x.mu.Lock()
	defer x.mu.Unlock()
	port := x.next
	x.next = x.first + (x.next-x.first+1)%xdccPorts
	x.offsets[port], x.chats[port] = 0, chat
	return port
}

// chat greets whoever connects and echoes their lines back.
func chat(conn net.Conn) {
	defer conn.Close()
	fmt.Fprintf(conn, "hello from xdcc! say something\n")
	s := bufio.NewScanner(conn)
	for s.Scan() {
		fmt.Fprintf(conn, "you said: %s\n", s.Text())
	}
}

// serve sends the pack to whoever connects to port, from the offset
// agreed for it, reading the receiver's acks like a real sender.
func (x *xdcc) serve(ln net.Listener, port int) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		x.mu.Lock()
		from, isChat := x.offsets[port], x.chats[port]
		x.mu.Unlock()
		if isChat {
			go chat(conn)
			continue
		}
		go func() {
			defer conn.Close()
			go io.Copy(io.Discard, conn) // acks
			buf := make([]byte, 32<<10)
			for pos := from; pos < packSize; {
				n := min(int64(len(buf)), packSize-pos)
				for i := range n {
					buf[i] = packByte(pos + i)
				}
				if _, err := conn.Write(buf[:n]); err != nil {
					return
				}
				pos += n
				time.Sleep(5 * time.Millisecond) // slow enough to watch the progress
			}
			time.Sleep(time.Second) // let the last acks arrive
		}()
	}
}

// ctcpReply answers CTCP VERSION, PING and TIME, like a real client.
func ctcpReply(c *irc.Conn, nick, text string) {
	cmd, args, _ := strings.Cut(strings.Trim(text, "\x01"), " ")
	switch strings.ToUpper(cmd) {
	case "VERSION":
		c.Send("NOTICE", nick, "\x01VERSION trout dev bot\x01")
	case "PING":
		c.Send("NOTICE", nick, "\x01PING "+args+"\x01")
	case "TIME":
		c.Send("NOTICE", nick, "\x01TIME "+time.Now().Format(time.RFC1123Z)+"\x01")
	}
}
