package ui

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/textproto"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MaxRenaud/trout/internal/script"
)

// Scripts' web requests ($urlget): the engine keeps the transfers, trout
// makes the requests here, and the answer reaches the engine in Update.

type urlDoneMsg struct {
	id  int
	res script.URLResult
}

// urlMax is the most a $urlget takes in: it's all kept in memory.
const urlMax = 64 << 20

// scriptURL does what the engine asked: a request, or cancelling one.
func (a *App) scriptURL(u *script.URLGet) {
	if u.Cancel {
		if cancel := a.webGets[u.ID]; cancel != nil {
			delete(a.webGets, u.ID)
			cancel()
		}
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	if a.webGets == nil {
		a.webGets = map[int]context.CancelFunc{}
	}
	a.webGets[u.ID] = cancel
	send, agent := a.send, "trout/"+strings.TrimPrefix(cmp.Or(a.opts.Version, "dev"), "v")
	go func() {
		defer cancel()
		res := fetchURL(ctx, u, agent)
		if ctx.Err() == context.Canceled && res.Err != nil {
			return // cancelled: the engine has forgotten it
		}
		send(urlDoneMsg{u.ID, res})
	}()
}

func (a *App) urlDone(m urlDoneMsg) tea.Cmd {
	if _, ok := a.webGets[m.id]; !ok {
		return nil
	}
	delete(a.webGets, m.id)
	return a.scriptAsking(func() []script.Action { return a.scripts.URLDone(m.id, m.res, a.scriptHost()) })
}

// cancelURLs stops scripts' requests as trout quits.
func (a *App) cancelURLs() {
	for id, cancel := range a.webGets {
		delete(a.webGets, id)
		cancel()
	}
}

func fetchURL(ctx context.Context, u *script.URLGet, agent string) script.URLResult {
	var body io.Reader
	if u.Body != nil {
		body = bytes.NewReader(u.Body)
	}
	req, err := http.NewRequestWithContext(ctx, u.Method, u.URL, body)
	if err != nil {
		return script.URLResult{Err: err}
	}
	req.Header.Set("User-Agent", agent)
	// The script's headers: "Name: value" lines, CRLF or LF.
	if len(u.Header) > 0 {
		tp := textproto.NewReader(bufio.NewReader(io.MultiReader(bytes.NewReader(u.Header), strings.NewReader("\r\n\r\n"))))
		if h, err := tp.ReadMIMEHeader(); err == nil || errors.Is(err, io.EOF) {
			maps.Copy(req.Header, h)
		}
	}
	if u.From > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", u.From))
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if u.Insecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	client := &http.Client{Transport: tr}
	if u.NoRedirect {
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}
	resp, err := client.Do(req)
	if err != nil {
		return script.URLResult{Err: err}
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, urlMax+1))
	if err == nil && len(data) > urlMax {
		err = fmt.Errorf("more than %d MB", urlMax>>20)
	}
	if err != nil {
		return script.URLResult{Err: err}
	}
	res := script.URLResult{Status: resp.StatusCode, Body: data, Size: resp.ContentLength, Reply: replyText(resp)}
	if final := resp.Request.URL.String(); final != u.URL {
		res.Final = final
	}
	return res
}

// replyText is the reply's status line and headers the way mIRC's .reply
// has them: CRLF lines, a blank one at the end. Go doesn't keep the
// headers' order, so they're sorted.
func replyText(resp *http.Response) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s\r\n", resp.Proto, resp.Status)
	for _, k := range slices.Sorted(maps.Keys(resp.Header)) {
		for _, v := range resp.Header[k] {
			fmt.Fprintf(&b, "%s: %s\r\n", strings.ToLower(k), v)
		}
	}
	b.WriteString("\r\n")
	return b.String()
}
