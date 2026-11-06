package ui

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"strconv"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MaxRenaud/trout/internal/script"
)

// Scripts' sockets (/sockopen): the engine keeps their state, and trout
// does the network side here. The goroutines only send messages; what
// they report reaches the engine in Update.

type sockOpenedMsg struct {
	id   int
	conn net.Conn
	err  error
}

type sockReadMsg struct {
	id   int
	data []byte
}

type sockWrittenMsg struct {
	id, n int
	err   error
}

type sockClosedMsg struct {
	id  int
	err error
}

// scriptSock is one socket's connection. Its send queue is a list the
// writer takes from, so Update never waits on the network.
type scriptSock struct {
	cancel context.CancelFunc // stops the dial
	conn   net.Conn           // nil until it's connected
	mu     sync.Mutex
	queue  [][]byte
	wake   chan struct{} // there's something in queue
	done   chan struct{} // closed: the writer stops
}

// sockHandshakeTimeout is how long an SSL socket's handshake can take.
var sockHandshakeTimeout = 30 * time.Second

// scriptSocket does what the engine asked with a socket.
func (a *App) scriptSocket(s *script.Sock) {
	switch {
	case s.Open:
		a.sockOpen(s)
	case s.Close:
		if sc := a.socks[s.ID]; sc != nil {
			delete(a.socks, s.ID)
			sc.close()
		}
	case s.Data != nil:
		if sc := a.socks[s.ID]; sc != nil {
			sc.mu.Lock()
			sc.queue = append(sc.queue, s.Data)
			sc.mu.Unlock()
			select {
			case sc.wake <- struct{}{}:
			default:
			}
		}
	}
}

func (a *App) sockOpen(s *script.Sock) {
	ctx, cancel := context.WithCancel(context.Background())
	if a.socks == nil {
		a.socks = map[int]*scriptSock{}
	}
	a.socks[s.ID] = &scriptSock{cancel: cancel, wake: make(chan struct{}, 1), done: make(chan struct{})}
	send := a.send
	go func() {
		d := net.Dialer{Timeout: 30 * time.Second}
		conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(s.Host, strconv.Itoa(s.Port)))
		if err == nil && s.TLS {
			// The dialer's timeout is only the connection's: a server that
			// takes it and says nothing would leave the handshake waiting.
			hctx, stop := context.WithTimeout(ctx, sockHandshakeTimeout)
			tc := tls.Client(conn, &tls.Config{ServerName: s.Host, InsecureSkipVerify: s.Insecure})
			if err = tc.HandshakeContext(hctx); err != nil {
				conn.Close()
			}
			stop()
			conn = tc
		}
		send(sockOpenedMsg{s.ID, conn, err})
	}()
}

func (a *App) sockOpened(m sockOpenedMsg) tea.Cmd {
	sc := a.socks[m.id]
	if sc == nil { // closed while it connected
		if m.err == nil {
			m.conn.Close()
		}
		return nil
	}
	// on SOCKOPEN waits its turn behind a script that's asking, and can
	// ask itself, like on SOCKREAD.
	h := a.scriptHost()
	if m.err != nil {
		delete(a.socks, m.id)
		return a.scriptAsking(func() []script.Action { return a.scripts.SockOpened(m.id, "", m.err, h) })
	}
	sc.conn = m.conn
	go sc.read(m.id, a.send)
	go sc.write(m.id, a.send)
	remote := m.conn.RemoteAddr().String()
	return a.scriptAsking(func() []script.Action { return a.scripts.SockOpened(m.id, remote, nil, h) })
}

// sockMsg hands the engine what a socket's goroutines reported.
func (a *App) sockMsg(msg tea.Msg) tea.Cmd {
	h := a.scriptHost()
	switch m := msg.(type) {
	case sockOpenedMsg:
		return a.sockOpened(m)
	case sockReadMsg:
		return a.scriptAsking(func() []script.Action { return a.scripts.SockRead(m.id, m.data, h) })
	case sockWrittenMsg:
		return a.scriptAsking(func() []script.Action { return a.scripts.SockWritten(m.id, m.n, m.err, h) })
	case sockClosedMsg:
		if sc := a.socks[m.id]; sc != nil {
			delete(a.socks, m.id)
			sc.close()
			return a.scriptAsking(func() []script.Action { return a.scripts.SockClosed(m.id, m.err, h) })
		}
	}
	return nil
}

func (sc *scriptSock) read(id int, send func(tea.Msg)) {
	buf := make([]byte, 4096)
	for {
		n, err := sc.conn.Read(buf)
		if n > 0 {
			send(sockReadMsg{id, append([]byte(nil), buf[:n]...)})
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				err = nil
			}
			if !errors.Is(err, net.ErrClosed) { // not our own /sockclose
				send(sockClosedMsg{id, err})
			}
			return
		}
	}
}

func (sc *scriptSock) write(id int, send func(tea.Msg)) {
	for {
		select {
		case <-sc.done:
			return
		case <-sc.wake:
		}
		sc.mu.Lock()
		queue := sc.queue
		sc.queue = nil
		sc.mu.Unlock()
		for _, data := range queue {
			n, err := sc.conn.Write(data)
			send(sockWrittenMsg{id, n, err})
			if err != nil {
				return
			}
		}
	}
}

func (sc *scriptSock) close() {
	sc.cancel()
	close(sc.done)
	if sc.conn != nil {
		sc.conn.Close()
	}
}

// closeSockets closes scripts' sockets as trout quits.
func (a *App) closeSockets() {
	for id, sc := range a.socks {
		delete(a.socks, id)
		sc.close()
	}
}
