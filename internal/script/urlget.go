package script

import (
	"net"
	"net/url"
	"slices"
	"strings"
	"time"
)

// $urlget: a web request the client makes, like mIRC's (7.56 on), which
// calls an alias with its id when it's done. Checked in mIRC 7.86
// (tmp/url-oracle*.mrc against httpbin.org): ids start at 1025; a
// transfer's properties last until its alias returns; a 404 fails
// unless e saves it; k fails on a redirect; HEAD fills nothing; c
// cancels without calling the alias; bad parameters are an error.

// URLGet is a request for the client to make, or with Cancel to stop.
type URLGet struct {
	ID         int
	Method     string // GET, HEAD, POST, PUT, PATCH or DELETE
	URL        string
	Header     []byte // the headers &binvar: "Name: value" lines
	Body       []byte
	Insecure   bool  // i: ignore certificate errors
	NoRedirect bool  // k
	From       int64 // r: resume a file from this byte (a Range request)
	Cancel     bool
}

// URLResult is what came back: the status, and the reply's first line
// and headers as mIRC's .reply has them (CRLF, ending in a blank line).
// Size is the reply's Content-Length, -1 if it had none; Final is the
// address redirects ended at, "" without any. Err is set if there was
// no reply at all.
type URLResult struct {
	Status int
	Reply  string
	Body   []byte
	Size   int64
	Final  string
	Err    error
}

type urlGet struct {
	id                     int
	url, method, kind      string // kind: binvar or file
	target, alias, network string
	resume, saveErr        bool
	state, reply, redirect string
	size, rcvd             int64
	started                time.Time
	took                   time.Duration
}

var urlMethods = map[byte]string{'h': "HEAD", 'g': "GET", 'p': "POST", 'u': "PUT", 'a': "PATCH", 'd': "DELETE"}

func (e *Engine) urlByID(id int) *urlGet {
	i := slices.IndexFunc(e.urls, func(u *urlGet) bool { return u.id == id })
	if i < 0 {
		return nil
	}
	return e.urls[i]
}

// urlgetIdent is $urlget(url,options,target,alias[,headers,body]), and
// $urlget(N|id).property, $urlget(0), and $urlget(id,c) to cancel.
func urlgetIdent(r *run, fr *frame, c *call) (string, error) {
	e := r.e
	if len(c.vals) <= 2 {
		n := int(c.num(0))
		if len(c.vals) == 2 && strings.Contains(c.arg(1), "c") {
			if u := e.urlByID(n); u != nil {
				e.urls = slices.DeleteFunc(e.urls, func(o *urlGet) bool { return o == u })
				r.actions = append(r.actions, Action{URL: &URLGet{ID: u.id, Cancel: true}})
			}
			return "", nil
		}
		if n == 0 && c.arg(0) == "0" {
			return itoa(len(e.urls)), nil
		}
		// N is the Nth transfer, or else an id.
		var u *urlGet
		if n >= 1 && n <= len(e.urls) {
			u = e.urls[n-1]
		} else {
			u = e.urlByID(n)
		}
		if u == nil {
			return "", nil
		}
		return u.prop(c.prop), nil
	}
	return r.urlStart(fr, c)
}

func (u *urlGet) prop(p string) string {
	switch p {
	case "id":
		return itoa(u.id)
	case "url":
		return u.url
	case "redirect":
		return u.redirect
	case "method":
		return strings.ToLower(u.method)
	case "type":
		return u.kind
	case "target":
		return u.target
	case "alias":
		return u.alias
	case "state":
		return u.state
	case "size":
		return itoa(int(u.size))
	case "rcvd":
		return itoa(int(u.rcvd))
	case "resume":
		return boolStr01(u.resume)
	case "time":
		return itoa(int(u.took.Milliseconds()))
	case "reply":
		return u.reply
	}
	return "" // no property: mIRC gives nothing (checked)
}

func boolStr01(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func (r *run) urlStart(fr *frame, c *call) (string, error) {
	bad := errorf("Invalid parameters: $urlget")
	addr, opts, target, alias := c.arg(0), strings.ToLower(c.arg(1)), c.arg(2), c.arg(3)
	pu, err := url.Parse(addr)
	if err != nil || pu.Host == "" || pu.Scheme != "http" && pu.Scheme != "https" {
		return "", bad
	}
	method, kind := "", ""
	for i := range len(opts) {
		o := opts[i]
		switch {
		case urlMethods[o] != "":
			if method != "" {
				return "", bad
			}
			method = urlMethods[o]
		case o == 'f' || o == 'b':
			if kind != "" {
				return "", bad
			}
			kind = map[byte]string{'f': "file", 'b': "binvar"}[o]
		case !strings.ContainsRune("rtikce", rune(o)):
			return "", bad
		}
	}
	if method == "" || kind == "" || alias == "" || target == "" || kind == "binvar" && !isBinvar(target) {
		return "", bad
	}
	e := r.e
	if e.urlSeq < 1024 {
		e.urlSeq = 1024
	}
	e.urlSeq++
	u := &urlGet{id: e.urlSeq, url: addr, method: method, kind: kind, target: target, alias: alias,
		network: fr.network, resume: strings.Contains(opts, "r"), saveErr: strings.Contains(opts, "e"),
		state: "connect", started: r.h.Now()}
	e.urls = append(e.urls, u)
	req := &URLGet{ID: u.id, Method: method, URL: addr, Insecure: strings.Contains(opts, "i"),
		NoRedirect: strings.Contains(opts, "k")}
	if b, ok := r.bin(c.arg(4)); ok {
		req.Header = slices.Clone(b)
	}
	if b, ok := r.bin(c.arg(5)); ok {
		req.Body = slices.Clone(b)
	}
	if u.resume && kind == "file" {
		if data, ok := r.readFile(target); ok && len(data) > 0 {
			req.From = int64(len(data))
		}
	}
	r.actions = append(r.actions, Action{URL: req})
	return itoa(u.id), nil
}

// URLDone is the client's answer to a URLGet: the transfer gets its
// properties and its target, the alias runs with its id, and then it's
// gone.
func (e *Engine) URLDone(id int, res URLResult, h Host) []Action {
	u := e.urlByID(id)
	if u == nil { // cancelled
		return nil
	}
	r := e.newRun(h)
	u.took = h.Now().Sub(u.started)
	u.reply = res.Reply
	if res.Final != "" {
		u.redirect = withPort(res.Final)
	}
	ok := res.Err == nil && (res.Status >= 200 && res.Status < 300 || u.saveErr && res.Status >= 400)
	if res.Err == nil {
		u.size = res.Size
		if u.size < 0 {
			u.size = int64(len(res.Body))
		}
	}
	u.state = "fail"
	if ok {
		u.state = "ok"
		u.rcvd = int64(len(res.Body))
		if u.kind == "binvar" {
			r.setBin(u.target, res.Body)
		} else {
			// A resumed file whose server sent the rest (206) gets it
			// added; otherwise the whole file is what came.
			r.writeFile(u.target, string(res.Body), u.resume && res.Status == 206)
		}
	}
	if a := e.alias(nil, u.alias); a != nil {
		fr := &frame{network: u.network, params: []string{itoa(u.id)}}
		r.runAlias(fr, a)
	}
	e.urls = slices.DeleteFunc(e.urls, func(o *urlGet) bool { return o == u })
	return r.finish()
}

// withPort is an address with its port spelled out, as mIRC's .redirect
// has it: https://httpbin.org:443/get.
func withPort(addr string) string {
	pu, err := url.Parse(addr)
	if err != nil || pu.Port() != "" {
		return addr
	}
	port := "80"
	if pu.Scheme == "https" {
		port = "443"
	}
	pu.Host = net.JoinHostPort(pu.Hostname(), port)
	return pu.String()
}

var urlIdents = map[string]builtin{"urlget": urlgetIdent}
