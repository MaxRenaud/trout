package script

import (
	"net"
	"os"
	"slices"
	"strings"
	"syscall"
	"testing"
)

// sockOracle is tmp/sock-oracle.mrc's client side: what it wrote in real
// mIRC is what TestSockets wants, with the sockets' events played by
// hand instead of over 127.0.0.1.
const sockOracle = `on *:START:{
  sockopen tc 127.0.0.1 47123
  o after-open sockerr= $+ $sockerr status= $+ $sock(tc).status addr= $+ $sock(tc).addr ip= $+ $sock(tc).ip port= $+ $sock(tc).port type= $+ $sock(tc).type ssl= $+ $sock(tc).ssl count= $+ $sock(t*,0) first= $+ $sock(t*,1)
  o.try sockopen tc 127.0.0.1 47123
  o.try sockwrite tc early
  o.try sockwrite nosuch hi
  o.try sockread %x
  o.try sockclose nosuch
  o.try sockmark nosuch m
  sockmark tc hello there
  o mark= $+ $sock(tc).mark
  sockmark tc
  o cleared= $+ $sock(tc).mark $+ .
  sockopen bad1 127.0.0.1 1
  sockopen bad2 nonexistent.invalid 80
}
alias o { write out.txt $1- }
alias o.try {
  $1-
  o ok: $1-
  return
  :error
  o err: $gettok($error, 1, 40)
  reseterror
}
on *:SOCKOPEN:tc:{
  o sockopen sockerr= $+ $sockerr status= $+ $sock(tc).status
  sockwrite -n tc hello
}
on *:SOCKOPEN:bad*:{
  o sockopen $sockname sockerr= $+ $sockerr wserr= $+ $sock($sockname).wserr wsmsg= $+ $sock($sockname).wsmsg status= $+ $sock($sockname).status exists= $+ $sock($sockname,0)
}
on *:SOCKREAD:tc:{
  inc %o.reads
  sockread %line
  o read %o.reads br= $+ $sockbr line=[ $+ %line $+ ] rq= $+ $sock(tc).rq rcvd= $+ $sock(tc).rcvd
}
on *:SOCKWRITE:tc:{ o sockwrite tc sockerr= $+ $sockerr sq= $+ $sock(tc).sq sent= $+ $sock(tc).sent }
on *:SOCKCLOSE:tc:{
  sockread -f %rest
  o sockclose tc sockerr= $+ $sockerr br= $+ $sockbr rest=[ $+ %rest $+ ] exists= $+ $sock(tc,0) status= $+ $sock(tc).status
}
`

// TestSockets plays mIRC's socket behavior, as tmp/sock-oracle.mrc found
// it in real mIRC, through the engine: the error messages, $sock's
// properties, one line per /sockread with SOCKREAD firing again while a
// whole line waits (a bare LF ends one), and failed connections.
func TestSockets(t *testing.T) {
	e := New(Options{})
	if p := e.Load("t.mrc", sockOracle); len(p) > 0 {
		t.Fatal(p)
	}
	h := evalHost()
	var out []string
	var socks []*Sock
	take := func(actions []Action) {
		for _, a := range actions {
			if a.File != nil && a.File.Name == "out.txt" {
				out = append(out, strings.Split(strings.TrimRight(a.File.Data, "\r\n"), "\r\n")...)
			}
			if a.Sock != nil {
				socks = append(socks, a.Sock)
			}
		}
	}
	take(e.Init("t.mrc", false, h))
	if len(socks) != 3 || !socks[0].Open || socks[0].Host != "127.0.0.1" || socks[0].Port != 47123 || socks[0].TLS {
		t.Fatalf("the opens: %+v", socks)
	}
	tc, bad1, bad2 := socks[0].ID, socks[1].ID, socks[2].ID
	take(e.SockOpened(tc, "127.0.0.1:47123", nil, h))
	if s := socks[len(socks)-1]; string(s.Data) != "hello\r\n" || s.ID != tc {
		t.Fatalf("/sockwrite -n sent %+v", s)
	}
	take(e.SockWritten(tc, 7, nil, h))
	take(e.SockRead(tc, []byte("one\r\ntwo\r\nthree\nfo"), h))
	take(e.SockOpened(bad2, "", &net.DNSError{Err: "no such host", Name: "nonexistent.invalid", IsNotFound: true}, h))
	take(e.SockRead(tc, []byte("ur\r\n"), h))
	take(e.SockOpened(bad1, "", &net.OpError{Op: "dial", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}, h))
	take(e.SockClosed(tc, nil, h))

	want := []string{
		"after-open sockerr= status=connecting addr=127.0.0.1 ip=127.0.0.1 port=47123 type=tcp ssl=$false count=1 first=tc",
		"err: * /sockopen: 'tc' socket in use",
		"err: * /sockwrite: 'tc' not connected",
		"err: * /sockwrite: 'nosuch' no such socket",
		"err: * /sockread: socket unavailable",
		"ok: sockclose nosuch",
		"ok: sockmark nosuch m",
		"mark=hello there",
		"cleared=.",
		"sockopen sockerr=0 status=active",
		"sockwrite tc sockerr=0 sq=0 sent=7",
		"read 1 br=5 line=[one] rq=13 rcvd=5",
		"read 2 br=5 line=[two] rq=8 rcvd=10",
		"read 3 br=6 line=[three] rq=2 rcvd=16",
		"sockopen bad2 sockerr=4 wserr=0 wsmsg=[0] Network error status=connecting exists=1",
		"read 4 br=6 line=[four] rq=0 rcvd=22",
		"sockopen bad1 sockerr=3 wserr=10061 wsmsg=[10061] Connection refused status=connecting exists=1",
		"sockclose tc sockerr=0 br=0 rest=[] exists=1 status=active",
	}
	if !slices.Equal(out, want) {
		t.Fatalf("got\n%q\nwant\n%q", out, want)
	}
	// The failed ones are gone after their events, like tc after its close
	// (all checked); the client was told to drop them.
	if n := evalIn(t, e, "$sock(*,0)"); n != "0" {
		t.Fatalf("%s sockets left", n)
	}
	if !slices.ContainsFunc(socks, func(s *Sock) bool { return s.ID == bad1 && s.Close }) {
		t.Fatal("the client wasn't told to drop a socket that couldn't connect")
	}
}

// TestSockUnread: a SOCKREAD nobody reads in loses its data, like mIRC's;
// /sockclose tells the client and fires no SOCKCLOSE; -e with -a opens an
// SSL socket that accepts any certificate.
func TestSockUnread(t *testing.T) {
	e := New(Options{})
	e.Load("t.mrc", `alias go sockopen -ea web example.com 443
on *:SOCKREAD:web:noop
on *:SOCKCLOSE:web:set %closed yes`)
	h := evalHost()
	acts, _ := e.Alias("TestNet", "", "go", "", h)
	if len(acts) != 1 || acts[0].Sock == nil || !acts[0].Sock.TLS || !acts[0].Sock.Insecure {
		t.Fatalf("/sockopen -ea: %+v", acts)
	}
	id := acts[0].Sock.ID
	e.SockOpened(id, "93.184.215.14:443", nil, h)
	if ip := evalIn(t, e, "$sock(web).ip $sock(web).ssl"); ip != "93.184.215.14 $true" {
		t.Fatalf("$sock(web).ip .ssl: %s", ip)
	}
	e.SockRead(id, []byte("HTTP/1.1 200 OK\r\n"), h)
	if rq := evalIn(t, e, "$sock(web).rq"); rq != "0" {
		t.Fatalf("an unread buffer kept %s bytes", rq)
	}
	acts, _ = e.Command("TestNet", "", "sockclose web", false, h)
	if len(acts) != 1 || acts[0].Sock == nil || !acts[0].Sock.Close || acts[0].Sock.ID != id {
		t.Fatalf("/sockclose: %+v", acts)
	}
	e.SockClosed(id, nil, h) // the client's read ending, after
	if v := evalIn(t, e, "%closed"); v != "" {
		t.Fatal("/sockclose fired SOCKCLOSE")
	}
}

func TestIPType(t *testing.T) {
	e := New(Options{})
	for in, want := range map[string]string{
		"$iptype(8.8.8.8)": "ipv4", "$iptype(2001:4860:4860::8888)": "ipv6", "$iptype(irc.libera.chat)": "",
		"$iptype(2001:db8:0:0:0:0:0:1).compress": "2001:db8::1",
		"$iptype(2001:db8::1).expand":            "2001:0db8:0000:0000:0000:0000:0000:0001",
	} {
		if got := evalIn(t, e, in); got != want {
			t.Errorf("%s = %q, want %q", in, got, want)
		}
	}
}

// TestSockBinvars reads into and writes from binary variables: /sockread
// [numbytes] &binvar takes bytes, not lines (zero bytes too), SOCKREAD
// fires again while any are left, and /sockwrite [-b numbytes] sends a
// &binvar's bytes. Each SOCKREAD is its own run: its &binvars start empty.
func TestSockBinvars(t *testing.T) {
	e := New(Options{})
	if p := e.Load("t.mrc", `alias go sockopen raw example.com 80
on *:SOCKOPEN:raw:{
  bset &req 1 1 2 0 3 4 5
  sockwrite raw &req
  sockwrite -b raw 2 &req
}
on *:SOCKREAD:raw:{
  inc %reads
  if ($bvar(&got,0)) set %leak yes
  sockread 4 &got
  set %got %got $bvar(&got,1,$bvar(&got,0)) / $sockbr
}`); len(p) > 0 {
		t.Fatal(p)
	}
	h := evalHost()
	acts, _ := e.Alias("TestNet", "", "go", "", h)
	id := acts[0].Sock.ID
	var sent [][]byte
	for _, a := range e.SockOpened(id, "93.184.215.14:80", nil, h) {
		if a.Sock != nil && a.Sock.Data != nil {
			sent = append(sent, a.Sock.Data)
		}
	}
	if len(sent) != 2 || string(sent[0]) != "\x01\x02\x00\x03\x04\x05" || string(sent[1]) != "\x01\x02" {
		t.Fatalf("sent %q", sent)
	}
	e.SockRead(id, []byte("ab\x00cdefghi"), h)
	if got := evalIn(t, e, "%reads %got"); got != "3 97 98 0 99 / 4 100 101 102 103 / 4 104 105 / 2" {
		t.Fatalf("reads: %q", got)
	}
	if leak := evalIn(t, e, "%leak"); leak != "" {
		t.Fatal("a &binvar outlived its SOCKREAD")
	}
}

// TestSockWriteFails: a send that fails is on SOCKWRITE with $sockerr,
// and then the socket is gone, so what a script writes next isn't queued
// for a connection that can't send it.
func TestSockWriteFails(t *testing.T) {
	e := New(Options{})
	if p := e.Load("t.mrc", `alias go sockopen raw example.com 80
on *:SOCKWRITE:raw:set %wrote $sockerr $sock(raw).wserr
on *:SOCKCLOSE:raw:set %closed yes`); len(p) > 0 {
		t.Fatal(p)
	}
	h := evalHost()
	acts, _ := e.Alias("TestNet", "", "go", "", h)
	id := acts[0].Sock.ID
	e.SockOpened(id, "93.184.215.14:80", nil, h)
	e.Command("TestNet", "", "sockwrite -n raw one", false, h)
	reset := &net.OpError{Op: "write", Err: os.NewSyscallError("write", syscall.ECONNRESET)}
	acts = e.SockWritten(id, 0, reset, h)
	if !slices.ContainsFunc(acts, func(a Action) bool { return a.Sock != nil && a.Sock.Close && a.Sock.ID == id }) {
		t.Fatalf("the client wasn't told to drop the socket: %+v", acts)
	}
	if got := evalIn(t, e, "%wrote $sock(raw,0) %closed"); got != "3 10054 0" {
		t.Fatalf("after the failed send: %q", got)
	}
	if acts, _ := e.Command("TestNet", "", "sockwrite -n raw two", false, h); slices.ContainsFunc(acts, func(a Action) bool { return a.Sock != nil }) {
		t.Fatalf("/sockwrite after it failed queued %+v", acts)
	}
}

// TestSockRecvLimit: data that never ends a line waits, and SockWaiting
// says how much, so the client stops reading at SockHold, like mIRC. Lines
// that do end are read as they come, however many arrive.
func TestSockRecvLimit(t *testing.T) {
	e := New(Options{})
	if p := e.Load("t.mrc", `alias go sockopen raw example.com 80
on *:SOCKREAD:raw:{
  sockread %l
  if ($sockbr) inc %lines
}`); len(p) > 0 {
		t.Fatal(p)
	}
	h := evalHost()
	acts, _ := e.Alias("TestNet", "", "go", "", h)
	id := acts[0].Sock.ID
	e.SockOpened(id, "93.184.215.14:80", nil, h)
	for range 1000 {
		e.SockRead(id, []byte(strings.Repeat("x", 99)+"\n"), h)
	}
	if got := evalIn(t, e, "%lines $sock(raw).rq"); got != "1000 0" || e.SockWaiting(id) != 0 {
		t.Fatalf("lines and rq: %s, waiting %d", got, e.SockWaiting(id))
	}
	chunk := []byte(strings.Repeat("y", 4096))
	for range 5 {
		if acts := e.SockRead(id, chunk, h); slices.ContainsFunc(acts, func(a Action) bool { return a.Sock != nil && a.Sock.Close }) {
			t.Fatal("unread data closed the socket")
		}
	}
	if got := e.SockWaiting(id); got != 5*4096 || got < SockHold {
		t.Fatalf("waiting %d", got)
	}
	e.SockRead(id, []byte("\n"), h)
	if got := evalIn(t, e, "%lines"); got != "1001" || e.SockWaiting(id) != 0 {
		t.Fatalf("after the LF: lines %s, waiting %d", got, e.SockWaiting(id))
	}
}
