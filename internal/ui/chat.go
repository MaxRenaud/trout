package ui

import (
	"bufio"
	"context"
	"net"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MaxRenaud/trout/internal/state"
)

// DCC CHAT, the accepting side: like DCC Get, trout connects out to the
// person offering, so no open port is needed here (and they see this
// computer's address, not the bouncer's). Like mIRC, a chat is a window
// called =nick. Starting a chat would need a listening port, so trout
// doesn't.

// dccChat is one chat connection.
type dccChat struct {
	net  *state.Network
	nick string
	addr string
	win  *state.Window
	conn net.Conn // set when connected
}

type (
	chatUpMsg struct {
		c    *dccChat
		conn net.Conn
		err  error
	}
	chatLineMsg struct {
		c    *dccChat
		line string
	}
	chatDownMsg struct {
		c   *dccChat
		err error
	}
)

// chatOffer handles "DCC CHAT chat <ip> <port>".
func (a *App) chatOffer(n *state.Network, nick, rest string, trusted bool, t time.Time) {
	f := strings.Fields(rest)
	status := n.Status()
	if len(f) < 3 {
		a.addLineAt(status, state.RoleInfo, "* "+nick+" sent a DCC chat offer trout can't read", t)
		return
	}
	_, ip, port, _, err := parseDCCSend(rest) // the same shape: name ip port
	switch {
	case err != nil:
		a.addLineAt(status, state.RoleInfo, "* "+nick+" sent a DCC chat offer trout can't read: "+err.Error(), t)
		return
	case port == "0":
		a.addLineAt(status, state.RoleInfo, "* "+nick+" offers a passive DCC chat, which needs an open port here; trout doesn't do that", t)
		return
	case a.dccChat == 2:
		a.addLineAt(status, state.RoleInfo, "* Ignored a DCC chat from "+nick+"; Options > DCC changes that", t)
		return
	}
	c := &dccChat{net: n, nick: nick, addr: net.JoinHostPort(ip.String(), port)}
	decide := func(a *App) tea.Cmd {
		if (a.dccChat == 1 || trusted) && !localAddr(ip) {
			return a.chatStart(c, false)
		}
		d := message(nick+" on "+n.Name+" wants to chat with you over DCC.", "",
			"Accepting opens a chat window, ="+nick+".")
		d.Title = "DCC Chat"
		d.Buttons, d.Cancel = []string{"Accept", "Decline"}, 1
		if localAddr(ip) {
			d.Lines = append(d.Lines, "", "Careful: it points at "+ip.String()+", on this computer or your",
				"own network. Only accept if you expect that.")
			d.Sel = d.Cancel
		}
		d.OnClose = func(a *App, button int) tea.Cmd {
			if button != 0 {
				a.addLine(status, state.RoleInfo, "* Declined a DCC chat from "+nick)
				return nil
			}
			return a.chatStart(c, true)
		}
		a.openDialog(d)
		return nil
	}
	a.whenFree(func(a *App) tea.Cmd {
		if !a.dccWarn {
			return decide(a)
		}
		d := a.dccPrivacyBox(&transfer{net: n, nick: nick, file: "a chat"}, ip)
		d.Lines[0] = nick + " on " + n.Name + " wants to chat with you over DCC."
		d.OnClose = func(a *App, button int) tea.Cmd {
			if d.Checked {
				a.dccWarn = false
			}
			if button == 0 {
				return decide(a)
			}
			if d.Checked {
				a.dccChat = 2
				a.addLine(status, state.RoleInfo, "* DCC chat offers will be ignored; Options > DCC changes that")
			}
			return nil
		}
		a.openDialog(d)
		return nil
	})
}

// chatStart opens the =nick window and connects.
func (a *App) chatStart(c *dccChat, focus bool) tea.Cmd {
	n := c.net
	c.win = n.Find(state.KChat, "="+c.nick)
	if c.win == nil {
		c.win = n.Add(&state.Window{Kind: state.KChat, Name: "=" + c.nick, Host: c.addr})
	}
	// A new chat in an old =nick window replaces the one it showed.
	a.chatClose(c.win)
	a.chats = append(a.chats, c)
	if focus {
		a.activate(c.win)
	}
	a.addLine(c.win, state.RoleInfo, "* Connecting to "+c.nick+" ("+c.addr+")...")
	addr, dial, timeout := c.addr, a.dialer(true), a.dccTimeout()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		conn, err := dial(ctx, "tcp", addr)
		return chatUpMsg{c, conn, err}
	}
}

// chatUpdate handles a chat's connection, lines and end.
func (a *App) chatUpdate(msg tea.Msg) {
	switch msg := msg.(type) {
	case chatUpMsg:
		c := msg.c
		if msg.err != nil {
			a.addLine(c.win, state.RoleQuit, "* DCC chat with "+c.nick+" failed: "+msg.err.Error())
			return
		}
		if !a.chatOpen(c) { // its window closed while connecting
			msg.conn.Close()
			return
		}
		c.conn = msg.conn
		a.addLine(c.win, state.RoleInfo, "* DCC chat with "+c.nick+" started")
		go func() {
			s := bufio.NewScanner(c.conn)
			s.Buffer(make([]byte, 4096), 64<<10)
			for s.Scan() {
				a.send(chatLineMsg{c, strings.TrimSuffix(s.Text(), "\r")})
			}
			a.send(chatDownMsg{c, s.Err()})
		}()
	case chatLineMsg:
		c := msg.c
		l := state.Line{Time: time.Now(), Role: state.RoleNormal, Text: "<" + c.nick + "> " + msg.line}
		if inner, ok := strings.CutPrefix(msg.line, "\x01ACTION "); ok {
			l.Role, l.Text = state.RoleAction, "* "+c.nick+" "+strings.TrimSuffix(inner, "\x01")
		} else if hit, color, beep := a.highlight(c.win, c.nick, msg.line); hit {
			l.Role, l.NoBeep, l.Color = state.RoleHighlight, !beep, color+1
		}
		a.addLineL(c.win, l)
		a.beepFor(c.win, l)
	case chatDownMsg:
		c := msg.c
		if c.conn != nil {
			c.conn.Close()
		}
		c.conn = nil
		if a.chatOpen(c) {
			a.addLine(c.win, state.RoleQuit, "* DCC chat with "+c.nick+" ended")
		}
	}
}

// chatOpen reports whether c still has its window: it hasn't closed, and
// no newer chat has taken it over.
func (a *App) chatOpen(c *dccChat) bool {
	return slices.Contains(a.chats, c) && c.win != nil && slices.Contains(c.net.Wins, c.win)
}

// chatFor is the chat a =nick window shows.
func (a *App) chatFor(w *state.Window) *dccChat {
	if i := slices.IndexFunc(a.chats, func(c *dccChat) bool { return c.win == w }); i >= 0 {
		return a.chats[i]
	}
	return nil
}

// chatSay sends a line (or a /me) over a chat.
func (a *App) chatSay(w *state.Window, text string, action bool) {
	c := a.chatFor(w)
	if c == nil || c.conn == nil {
		a.addLine(w, state.RoleInfo, "* The chat isn't connected")
		return
	}
	line := text
	if action {
		line = "\x01ACTION " + text + "\x01"
	}
	if _, err := c.conn.Write([]byte(line + "\n")); err != nil {
		a.addLine(w, state.RoleQuit, "* Couldn't send: "+err.Error())
		return
	}
	if action {
		a.addLine(w, state.RoleAction, "* "+w.Net.Nick+" "+text)
	} else {
		a.addLine(w, state.RoleOwn, "<"+w.Net.Nick+"> "+text)
	}
}

// chatClose ends the chat a window shows, when it closes or a new one
// takes it over.
func (a *App) chatClose(w *state.Window) {
	a.chats = slices.DeleteFunc(a.chats, func(c *dccChat) bool {
		if c.win != w {
			return false
		}
		if c.conn != nil {
			c.conn.Close()
		}
		return true
	})
}
