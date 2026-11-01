package script

import (
	"errors"
	"net"
	"net/netip"
	"os"
	"slices"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

// Sockets, from mIRC's /sockopen: TCP connections a script opens, writes
// to and reads from, with on SOCKOPEN, SOCKREAD, SOCKWRITE and SOCKCLOSE.
// The engine keeps their state; the client does the network side, from
// the Sock actions, and tells the engine what happened with SockOpened,
// SockRead, SockWritten and SockClosed. Listening and UDP sockets aren't
// supported (check.go). Where mIRC's help doesn't say, what it does was
// checked in real mIRC (tmp/sock-oracle.mrc).

// Sock is something for the client to do with a socket: open it, send on
// it or close it. ID is the engine's for that /sockopen; a name can be
// used again, an ID never is.
type Sock struct {
	ID       int
	Open     bool
	Host     string
	Port     int
	TLS      bool
	Insecure bool   // -e with -a: accept a certificate that doesn't check out
	Data     []byte // to send
	Close    bool
}

type socket struct {
	id       int
	name     string
	addr     string // the address as given
	ip       string // the address connected to
	port     int
	tls      bool
	active   bool // connected; before, it's connecting
	mark     string
	buf      []byte // received, not yet /sockread
	sent     int
	rcvd     int // read by the script, like mIRC's .rcvd (checked)
	sq       int // queued to send
	network  string
	opened   time.Time
	lastSent time.Time
	lastRead time.Time
	wserr    int
	wsmsg    string
}

// sockMax is how much can wait in a socket's send queue, like mIRC's.
const sockMax = 16384

func (e *Engine) sockByName(name string) *socket {
	for _, s := range e.socks {
		if strings.EqualFold(s.name, name) {
			return s
		}
	}
	return nil
}

func (e *Engine) sockByID(id int) *socket {
	for _, s := range e.socks {
		if s.id == id {
			return s
		}
	}
	return nil
}

// socksMatching are the sockets a name matches, which can be a wildcard.
func (e *Engine) socksMatching(name string) []*socket {
	var m []*socket
	for _, s := range e.socks {
		if wildMatch(name, s.name, false) {
			m = append(m, s)
		}
	}
	return m
}

func (e *Engine) dropSock(s *socket) {
	e.socks = slices.DeleteFunc(e.socks, func(x *socket) bool { return x == s })
}

// cmdSock runs the socket commands: /sockopen, /sockwrite, /sockread,
// /sockclose, /sockmark and /sockrename.
func (r *run) cmdSock(fr *frame, name, rest string) error {
	if name == "sockread" {
		return r.cmdSockRead(fr, rest)
	}
	line, err := r.eval(fr, rest)
	if err != nil {
		return err
	}
	sw, line := switches(line)
	words := strings.Fields(line)
	switch name {
	case "sockopen":
		return r.cmdSockOpen(fr, sw, words)
	case "sockwrite":
		return r.cmdSockWrite(sw, line)
	case "sockclose":
		if len(words) == 0 {
			return errorf("/sockclose: insufficient parameters")
		}
		// No such socket isn't an error (checked).
		for _, s := range r.e.socksMatching(words[0]) {
			r.e.dropSock(s)
			r.actions = append(r.actions, Action{Sock: &Sock{ID: s.id, Close: true}})
		}
		return nil
	case "sockmark":
		if len(words) == 0 {
			return errorf("/sockmark: insufficient parameters")
		}
		mark := strings.TrimSpace(strings.TrimPrefix(line, words[0]))
		for _, s := range r.e.socksMatching(words[0]) {
			s.mark = mark
		}
		return nil
	case "sockrename":
		if len(words) < 2 {
			return errorf("/sockrename: insufficient parameters")
		}
		s := r.e.sockByName(words[0])
		if s == nil {
			return errorf("/sockrename: '", words[0], "' no such socket")
		}
		if o := r.e.sockByName(words[1]); o != nil && o != s {
			return errorf("/sockrename: '", words[1], "' socket in use")
		}
		s.name = words[1]
		return nil
	case "socklist":
		return errorf("/socklist: listing sockets isn't supported; use $sock")
	}
	return errorf("/", name, ": pausing sockets isn't supported")
}

// cmdSockOpen is /sockopen [-deswap46nt] [bindip] <name> <address> <port>.
func (r *run) cmdSockOpen(fr *frame, sw string, words []string) error {
	if strings.Contains(sw, "d") && len(words) > 0 {
		words = words[1:] // a bind address: the system picks one
	}
	if strings.Contains(sw, "t") {
		return errorf("/sockopen: STARTTLS isn't supported")
	}
	if len(words) < 3 {
		return errorf("/sockopen: insufficient parameters")
	}
	name, addr := words[0], words[1]
	port, ok := parseNum(words[2])
	if !ok || port < 1 || port > 65535 {
		return errorf("/sockopen: invalid port")
	}
	if r.e.sockByName(name) != nil {
		return errorf("/sockopen: '", name, "' socket in use")
	}
	r.e.sockSeq++
	s := &socket{id: r.e.sockSeq, name: name, addr: addr, port: int(port), tls: strings.Contains(sw, "e"),
		network: fr.network, opened: r.h.Now()}
	if ip, err := netip.ParseAddr(addr); err == nil {
		s.ip = ip.String() // an address is its own ip before it connects (checked)
	}
	r.e.socks = append(r.e.socks, s)
	r.actions = append(r.actions, Action{Sock: &Sock{ID: s.id, Open: true, Host: addr, Port: s.port,
		TLS: s.tls, Insecure: strings.Contains(sw, "a")}})
	return nil
}

// cmdSockWrite is /sockwrite [-tnba] <name> [numbytes] <text>.
func (r *run) cmdSockWrite(sw, line string) error {
	name, text, _ := strings.Cut(line, " ")
	if name == "" {
		return errorf("/sockwrite: insufficient parameters")
	}
	if strings.Contains(sw, "b") {
		n, after, _ := strings.Cut(text, " ")
		count, ok := parseNum(n)
		if !ok {
			return errorf("/sockwrite: invalid parameters")
		}
		text = after
		if int(count) < len(text) {
			text = text[:int(count)]
		}
	}
	if strings.HasPrefix(text, "&") && !strings.Contains(sw, "t") {
		return errorf("/sockwrite: binary variables aren't supported")
	}
	if strings.Contains(sw, "n") && !strings.HasSuffix(text, "\r\n") {
		text += "\r\n"
	}
	data := []byte(text)
	if strings.Contains(sw, "a") {
		data = latin1(text)
	}
	socks := r.e.socksMatching(name)
	if len(socks) == 0 {
		return errorf("/sockwrite: '", name, "' no such socket")
	}
	for _, s := range socks {
		switch {
		case !s.active:
			return errorf("/sockwrite: '", s.name, "' not connected")
		case s.sq+len(data) > sockMax:
			// mIRC's message for this wasn't checked.
			return errorf("/sockwrite: '", s.name, "' queue would exceed ", itoa(sockMax), " bytes")
		}
	}
	for _, s := range socks {
		s.sq += len(data)
		r.actions = append(r.actions, Action{Sock: &Sock{ID: s.id, Data: data}})
	}
	return nil
}

// latin1 is text as single bytes, for /sockwrite -a, if every character
// fits in one; otherwise UTF-8, like mIRC's.
func latin1(text string) []byte {
	b := make([]byte, 0, len(text))
	for _, c := range text {
		if c > 255 {
			return []byte(text)
		}
		b = append(b, byte(c))
	}
	return b
}

// cmdSockRead is /sockread [-fn] [numbytes] <%var>, in a socket event: a
// line ending in LF (or CRLF) from the receive buffer, without it, or
// with -f whatever's there.
func (r *run) cmdSockRead(fr *frame, rest string) error {
	// Outside a socket event that's the error, whatever the parameters
	// (checked).
	s := r.sock
	if s == nil || r.e.sockByID(s.id) == nil {
		return errorf("/sockread: socket unavailable")
	}
	sw, rest := switches(strings.TrimSpace(rest))
	if w, after, _ := strings.Cut(rest, " "); isDigits(w) {
		rest = strings.TrimSpace(after) // numbytes is for binary variables
	}
	if strings.HasPrefix(rest, "&") {
		return errorf("/sockread: binary variables aren't supported")
	}
	if !strings.HasPrefix(rest, "%") {
		return errorf("/sockread: insufficient parameters")
	}
	name, _, err := r.varName(fr, rest)
	if err != nil {
		return err
	}
	r.sockReads++
	n := 0
	if i := slices.Index(s.buf, '\n'); i >= 0 {
		n = i + 1
	} else if strings.Contains(sw, "f") {
		n = len(s.buf)
	}
	line := s.buf[:n]
	s.buf = slices.Clone(s.buf[n:])
	s.rcvd += n
	r.sockBR = n
	line = []byte(strings.TrimSuffix(strings.TrimSuffix(string(line), "\n"), "\r"))
	r.setVar(fr, name, decodeText(line), r.isTemp(name))
	return nil
}

// decodeText is received bytes as text: UTF-8 if they are, else each
// byte a character, like mIRC's.
func decodeText(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	r := make([]rune, len(b))
	for i, c := range b {
		r[i] = rune(c)
	}
	return string(r)
}

// sockEvent runs a socket event's handlers, with $sockname and $sockerr.
func (r *run) sockEvent(kind string, s *socket, sockerr int) {
	r.sock, r.sockErr, r.sockBR = s, itoa(sockerr), 0
	ev := Event{Kind: kind, Network: s.network, Target: s.name}
	for _, f := range slices.Clone(r.e.scripts) {
		r.dispatchFile(f, &ev)
	}
}

// SockOpened is the client saying a /sockopen connected (to remote, its
// address), or couldn't: on SOCKOPEN, with $sockerr. A socket that
// couldn't connect is gone after its event, like mIRC's.
func (e *Engine) SockOpened(id int, remote string, err error, h Host) []Action {
	r := e.newRun(h)
	s := e.sockByID(id)
	if s == nil {
		return nil
	}
	if err != nil {
		var code int
		s.wserr, s.wsmsg, code = sockError(err)
		r.sockEvent("SOCKOPEN", s, code)
		if e.sockByID(id) != nil {
			e.dropSock(s)
			r.actions = append(r.actions, Action{Sock: &Sock{ID: id, Close: true}})
		}
		return r.finish()
	}
	s.active = true
	if host, _, err := net.SplitHostPort(remote); err == nil {
		s.ip = host
	}
	r.sockEvent("SOCKOPEN", s, 0)
	return r.finish()
}

// SockRead is data the client received on a socket: on SOCKREAD, again
// while a handler read a line with /sockread and there's another whole
// one waiting (checked). If no handler reads, the data is thrown away,
// like mIRC's.
func (e *Engine) SockRead(id int, data []byte, h Host) []Action {
	r := e.newRun(h)
	s := e.sockByID(id)
	if s == nil {
		return nil
	}
	s.buf = append(s.buf, data...)
	s.lastRead = h.Now()
	for range 10000 {
		r.sockReads = 0
		before := len(s.buf)
		r.sockEvent("SOCKREAD", s, 0)
		if e.sockByID(id) == nil {
			break
		}
		if r.sockReads == 0 {
			s.buf = nil
			break
		}
		if len(s.buf) == before || !slices.Contains(s.buf, '\n') {
			break
		}
	}
	return r.finish()
}

// SockWritten is the client saying it sent n bytes queued on a socket, or
// couldn't: on SOCKWRITE.
func (e *Engine) SockWritten(id, n int, err error, h Host) []Action {
	r := e.newRun(h)
	s := e.sockByID(id)
	if s == nil {
		return nil
	}
	s.sq = max(0, s.sq-n)
	s.sent += n
	s.lastSent = h.Now()
	code := 0
	if err != nil {
		s.wserr, s.wsmsg, code = sockError(err)
	}
	r.sockEvent("SOCKWRITE", s, code)
	return r.finish()
}

// SockClosed is the client saying the other end closed a socket, or the
// connection failed: on SOCKCLOSE, where what's left can still be read
// (checked), and then it's gone.
func (e *Engine) SockClosed(id int, err error, h Host) []Action {
	r := e.newRun(h)
	s := e.sockByID(id)
	if s == nil {
		return nil
	}
	code := 0
	if err != nil {
		s.wserr, s.wsmsg, code = sockError(err)
	}
	r.sockEvent("SOCKCLOSE", s, code)
	e.dropSock(s)
	return r.finish()
}

// winsock are the Windows Sockets error numbers mIRC gives as
// $sock().wserr, for the errors other systems have numbers of their own
// for.
var winsock = map[syscall.Errno]int{
	syscall.ECONNREFUSED: 10061, syscall.ETIMEDOUT: 10060, syscall.EHOSTUNREACH: 10065,
	syscall.ENETUNREACH: 10051, syscall.ECONNRESET: 10054, syscall.ECONNABORTED: 10053,
	syscall.EADDRNOTAVAIL: 10049,
}

var winsockText = map[int]string{
	10061: "Connection refused", 10060: "Connection timed out", 10065: "No route to host",
	10051: "Network is unreachable", 10054: "Connection reset by peer",
	10053: "Software caused connection abort", 10049: "Cannot assign requested address",
}

// sockError is an error as mIRC has it: $sock().wserr and .wsmsg, and
// $sockerr, which is 4 for an address that didn't resolve and 3 for the
// rest (both checked, as is the refused connection's message).
func sockError(err error) (wserr int, wsmsg string, sockerr int) {
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return 0, "[0] Network error", 4
	}
	var errno syscall.Errno
	switch {
	case errors.As(err, &errno) && winsock[errno] != 0:
		wserr = winsock[errno]
	case errors.As(err, &errno) && errno >= 10000 && errno < 12000: // Windows' own number
		wserr = int(errno)
	case errors.Is(err, os.ErrDeadlineExceeded):
		wserr = 10060
	}
	if text, ok := winsockText[wserr]; ok {
		return wserr, "[" + itoa(wserr) + "] " + text, 3
	}
	return wserr, "[" + itoa(wserr) + "] " + err.Error(), 3
}

var sockIdents = map[string]builtin{
	// $sock(name, N).prop: the Nth socket the name (a wildcard) matches;
	// N is 1 if it isn't given, and 0 counts them.
	"sock": func(r *run, fr *frame, c *call) (string, error) {
		m := r.e.socksMatching(c.arg(0))
		n := 1
		if len(c.args) > 1 {
			v, ok := parseNum(c.arg(1))
			if !ok {
				return "", nil
			}
			n = int(v)
		}
		if n == 0 {
			return itoa(len(m)), nil
		}
		if n < 1 || n > len(m) {
			return "", nil
		}
		s := m[n-1]
		since := func(t time.Time) string {
			if t.IsZero() {
				return "0"
			}
			return itoa(int(r.h.Now().Sub(t).Seconds()))
		}
		switch c.prop {
		case "", "name":
			return s.name, nil
		case "addr":
			return s.addr, nil
		case "ip":
			return s.ip, nil
		case "port":
			return itoa(s.port), nil
		case "status":
			return iff(s.active, "active", "connecting"), nil
		case "sent":
			return itoa(s.sent), nil
		case "rcvd":
			return itoa(s.rcvd), nil
		case "sq":
			return itoa(s.sq), nil
		case "rq":
			return itoa(len(s.buf)), nil
		case "ls":
			return since(s.lastSent), nil
		case "lr":
			return since(s.lastRead), nil
		case "to":
			return since(s.opened), nil
		case "mark":
			return s.mark, nil
		case "type":
			return "tcp", nil
		case "ssl":
			return boolStr(s.tls), nil
		case "wserr":
			return itoa(s.wserr), nil
		case "wsmsg":
			return s.wsmsg, nil
		case "pause", "starttls", "upnp":
			return boolStr(false), nil
		}
		return "", nil // saddr, sport, bindip, bindport
	},
	// $sockname is the socket an event is for; $sockerr its error (0 for
	// none), $null outside socket events (checked); $sockbr the bytes the
	// last /sockread took, with its line ending.
	"sockname": func(r *run, fr *frame, c *call) (string, error) {
		if r.sock == nil {
			return "", nil
		}
		return r.sock.name, nil
	},
	"sockerr": func(r *run, fr *frame, c *call) (string, error) { return r.sockErr, nil },
	"sockbr":  func(r *run, fr *frame, c *call) (string, error) { return itoa(r.sockBR), nil },
	// $iptype(text) is ipv4 or ipv6 for an IP address; .compress and
	// .expand write an IPv6 one short or in full.
	"iptype": func(r *run, fr *frame, c *call) (string, error) {
		ip, err := netip.ParseAddr(c.arg(0))
		if err != nil || ip.Zone() != "" {
			return "", nil
		}
		switch {
		case c.prop == "compress" && ip.Is6():
			return ip.String(), nil
		case c.prop == "expand" && ip.Is6():
			return ip.StringExpanded(), nil
		case c.prop != "":
			return c.arg(0), nil
		case ip.Is4():
			return "ipv4", nil
		}
		return "ipv6", nil
	},
}
