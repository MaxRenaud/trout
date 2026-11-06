package ui

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MaxRenaud/trout/internal/script"
)

// TestURLGetReusesConnections: $urlget's requests share connections, so
// asking again and again doesn't leave a new one open each time.
func TestURLGetReusesConnections(t *testing.T) {
	var conns atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ok")
	}))
	srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			conns.Add(1)
		}
	}
	srv.Start()
	t.Cleanup(srv.Close)
	for range 5 {
		if res := fetchURL(t.Context(), &script.URLGet{Method: "GET", URL: srv.URL}, "trout/test"); res.Err != nil || string(res.Body) != "ok" {
			t.Fatalf("fetch: %+v", res)
		}
	}
	if n := conns.Load(); n != 1 {
		t.Errorf("5 requests opened %d connections", n)
	}
	(&App{}).cancelURLs()
}

// TestScriptURLGet runs $urlget for real against a local web server: a
// POST with the script's headers and body, the reply into a &binvar, a
// redirect, and k refusing one.
func TestScriptURLGet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/echo":
			body, _ := io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "text/plain")
			io.WriteString(w, r.Method+" "+r.Header.Get("X-Trout")+" "+string(body))
		case "/moved":
			http.Redirect(w, r, "/echo", http.StatusFound)
		}
	}))
	t.Cleanup(srv.Close)

	a := New(Options{Demo: true})
	a.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	msgs := make(chan tea.Msg, 16)
	a.send = func(m tea.Msg) { msgs <- m }
	a.scriptMem = map[string]string{"web.mrc": `alias web {
  bset -t &hdr 1 X-Trout: yes
  bset -t &body 1 hello there
  noop $urlget(` + srv.URL + `/echo,pb,&page,shown,&hdr,&body)
  noop $urlget(` + srv.URL + `/moved,gb,&page,shown)
  noop $urlget(` + srv.URL + `/moved,gbk,&page,shown)
}
alias shown echo -s $urlget($1).state $urlget($1).method / $gettok($urlget($1).reply,1,13) / $bvar(&page,1,$bvar(&page,0)).text / $urlget($1).redirect`}
	st := a.nets[0].Status()
	a.activate(st)
	a.edit(st).Buf = []rune("/load -rs web.mrc")
	a.submit(st)
	a.edit(st).Buf = []rune("/web")
	a.submit(st)
	var got []string
	seen := len(st.Lines)
	for len(got) < 3 {
		select {
		case m := <-msgs:
			a.Update(m)
			for _, l := range st.Lines[seen:] {
				got = append(got, l.Text)
			}
			seen = len(st.Lines)
		case <-time.After(10 * time.Second):
			t.Fatalf("waiting for $urlget; got %q", got)
		}
	}
	want := map[string]bool{
		"ok post / HTTP/1.1 200 OK / POST yes hello there /":    true,
		"ok get / HTTP/1.1 200 OK / GET / " + srv.URL + "/echo": true,
		"fail get / HTTP/1.1 302 Found / /":                     true,
	}
	for _, l := range got {
		if !want[strings.TrimSpace(l)] {
			t.Errorf("unexpected: %q", l)
		}
	}
}
