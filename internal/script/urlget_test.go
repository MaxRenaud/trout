package script

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// TestURLGet plays tmp/url-oracle*.mrc's answers from real mIRC through
// the engine, with the client's replies made up: the request it asks
// for, the properties before and in the alias, a 404 with and without e,
// a redirect, a failed lookup, HEAD, cancelling, and the errors.
func TestURLGet(t *testing.T) {
	e := New(Options{})
	if p := e.Load("u.mrc", `alias go {
  bset -t &hdr 1 X-Trout: yes $+ $crlf
  bset -t &body 1 hello=there
  set %a $urlget(https://httpbin.org/post,pb,&post,done,&hdr,&body)
  set %b $urlget(https://httpbin.org/status/404,gb,&nf,done)
  set %c $urlget(https://httpbin.org/status/404,gbe,&nfe,done)
  set %d $urlget(https://httpbin.org/redirect/2,gf,page.txt,done)
  set %f $urlget(https://nonexistent.invalid/x,hb,&x,done)
  set %now $urlget(%a).state $urlget(0) $urlget(1).id $urlget(1) $bvar(&post,0)
}
alias done {
  var %t $urlget($1).target
  set %out %out $+ $chr(10) $+ $1 $urlget($1).state $urlget($1).method $urlget($1).type %t size= $+ $urlget($1).size rcvd= $+ $urlget($1).rcvd redirect= $+ $urlget($1).redirect time= $+ $urlget($1).time count= $+ $urlget(0)
  if ($left(%t,1) == &) set %out %out $bvar(%t,1,$bvar(%t,0)).text
  set %reply1 $gettok($urlget($1).reply,1,13)
}
alias bad set %err $urlget($1,$2,$3,$4)
alias cancel {
  set %slow $urlget(https://httpbin.org/delay/5,gb,&slow,done)
  noop $urlget(%slow,c)
  set %cancelled $urlget(%slow).state $+ / $+ $urlget(0)
}`); len(p) > 0 {
		t.Fatal(p)
	}
	h := evalHost()
	acts, _ := e.Alias("TestNet", "", "go", "", h)
	var reqs []*URLGet
	for _, a := range acts {
		if a.URL != nil {
			reqs = append(reqs, a.URL)
		}
	}
	if len(reqs) != 5 || reqs[0].ID != 1025 || reqs[0].Method != "POST" || strings.TrimSpace(string(reqs[0].Header)) != "X-Trout: yes" ||
		string(reqs[0].Body) != "hello=there" || reqs[3].URL != "https://httpbin.org/redirect/2" || reqs[4].Method != "HEAD" {
		for _, r := range reqs {
			t.Logf("%+v", *r)
		}
		t.Fatal("requests")
	}
	// Right after: connecting, 5 of them, the 1st's id, nothing without a
	// property, and the target not there yet.
	if now := evalIn(t, e, "%now"); now != "connect 5 1025 0" {
		t.Fatalf("right after $urlget: %q", now)
	}

	h.now = h.now.Add(953 * time.Millisecond)
	reply := "HTTP/1.1 200 OK\r\ncontent-length: 11\r\n\r\n"
	e.URLDone(1025, URLResult{Status: 200, Reply: reply, Body: []byte("hello there"), Size: 11}, h)
	e.URLDone(1026, URLResult{Status: 404, Reply: "HTTP/1.1 404 Not Found\r\n\r\n", Body: []byte("gone"), Size: 0}, h)
	e.URLDone(1027, URLResult{Status: 404, Reply: "HTTP/1.1 404 Not Found\r\n\r\n", Body: []byte("gone"), Size: 4}, h)
	e.URLDone(1028, URLResult{Status: 200, Body: []byte("{}"), Size: 2, Final: "https://httpbin.org/get"}, h)
	acts = e.URLDone(1029, URLResult{Err: errors.New("no such host")}, h)
	want := []string{"",
		"1025 ok post binvar &post size=11 rcvd=11 redirect= time=953 count=5 hello there",
		"1026 fail get binvar &nf size=0 rcvd=0 redirect= time=953 count=4",
		"1027 ok get binvar &nfe size=4 rcvd=4 redirect= time=953 count=3 gone",
		"1028 ok get file page.txt size=2 rcvd=2 redirect=https://httpbin.org:443/get time=953 count=2",
		"1029 fail head binvar &x size=0 rcvd=0 redirect= time=953 count=1"}
	if got := evalIn(t, e, "%out"); got != strings.Join(want, "\n") {
		t.Fatalf("the aliases saw\n%s\nwant\n%s", got, strings.Join(want, "\n"))
	}
	if evalIn(t, e, "$urlget(0) $urlget(1025).state") != "0" {
		t.Error("transfers outlived their aliases")
	}
	if len(acts) != 1 || acts[0].Save == nil {
		t.Errorf("the last alias's actions: %+v", acts)
	}

	// Cancelling: gone at once, and its alias never runs.
	acts, _ = e.Alias("TestNet", "", "cancel", "", h)
	if len(acts) < 2 || acts[1].URL == nil || !acts[1].URL.Cancel || acts[1].URL.ID != 1030 {
		t.Fatalf("cancelling: %+v", acts)
	}
	if got := evalIn(t, e, "%cancelled"); got != "/0" {
		t.Fatalf("after cancelling: %q", got)
	}
	if acts := e.URLDone(1030, URLResult{Status: 200}, h); acts != nil {
		t.Fatal("a cancelled transfer's alias ran")
	}

	// Parameters mIRC refuses: not http, no f or b, two methods, a target
	// that isn't a &binvar for b, an unknown option, no alias.
	for _, args := range []string{"ftp://example.com/x gb &z x", "https://example.com g &z x", "https://example.com gpb &z x",
		"https://example.com gb nobinvar x", "https://example.com gz &z x", "notaurl gb &z x"} {
		acts, _ := e.Alias("TestNet", "", "bad", args, h)
		if len(acts) == 0 || acts[0].Echo == nil || !strings.Contains(acts[0].Echo.Text, "Invalid parameters: $urlget") {
			t.Errorf("$urlget(%s): %+v", args, acts)
		}
	}
	if acts, _ := e.Command("TestNet", "", "echo -s $urlget(https://example.com/,gb,&z)", true, h); len(acts) == 0 ||
		!strings.Contains(acts[0].Echo.Text, "Invalid parameters") {
		t.Errorf("no alias: %q", acts[0].Echo.Text)
	}
}
