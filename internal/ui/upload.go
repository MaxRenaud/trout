package ui

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MaxRenaud/trout/internal/irc"
	"github.com/MaxRenaud/trout/internal/state"
)

// Send File: the modern stand-in for DCC SEND. The file goes to the
// bouncer's file host (soju's file-upload, advertised as soju.im/FILEHOST)
// and the link goes to the chat, so nobody connects to this computer.

type uploadDoneMsg struct {
	w      *state.Window
	target string
	link   string
	err    error
}

// openSendFile asks for a file and who gets the link.
func (a *App) openSendFile(to string) {
	w := a.active
	if to == "" && w.Kind != state.KStatus {
		to = w.Name
	}
	a.openDialog(&formBox{Title: "Send File", Buttons: []string{"Send", "Cancel"},
		Lines:  []string{"The file goes to the bouncer, and the link to the chat."},
		Fields: []*formField{newField("File:", "", false), newField("To:", to, false)},
		OnOK: func(a *App, v []string) (tea.Cmd, error) {
			path, target := strings.TrimSpace(v[0]), strings.TrimSpace(v[1])
			if home, err := os.UserHomeDir(); err == nil {
				if rest, ok := strings.CutPrefix(path, "~/"); ok {
					path = filepath.Join(home, rest)
				}
			}
			if target == "" {
				return nil, errors.New("say who gets the link: a nick or a channel")
			}
			info, err := os.Stat(path)
			if err != nil || !info.Mode().IsRegular() {
				return nil, fmt.Errorf("can't read the file %q", v[0])
			}
			if w.Net.FileHost == "" {
				return nil, errors.New("this bouncer doesn't take file uploads")
			}
			a.addLine(w, state.RoleInfo, fmt.Sprintf("* Uploading %s (%s)...", filepath.Base(path), sizeText(info.Size())))
			return a.upload(w, target, path), nil
		}})
}

// upload sends the file to the file host, logging in as the bouncer user,
// and trusting a pinned bouncer key the way the IRC connection does.
func (a *App) upload(w *state.Window, target, path string) tea.Cmd {
	host, user, password, pin, dial := w.Net.FileHost, a.bnc.User, a.password, a.bnc.Pin, a.dialer(false)
	return func() tea.Msg {
		link, err := uploadFile(host, user, password, pin, path, dial)
		return uploadDoneMsg{w, target, link, err}
	}
}

func uploadFile(host, user, password, pin, path string, dial func(context.Context, string, string) (net.Conn, error)) (string, error) {
	u, err := url.Parse(host)
	if err != nil {
		return "", err
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, host, f)
	if err != nil {
		return "", err
	}
	name := filepath.Base(path)
	ctype := mime.TypeByExtension(filepath.Ext(name))
	if ctype == "" {
		ctype = "application/octet-stream"
	}
	req.Header.Set("Content-Type", ctype)
	req.Header.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	req.SetBasicAuth(user, password)

	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy, tr.DialContext = nil, dial // Options > Connect > Proxy, not the environment's
	if u.Scheme == "https" {
		port := u.Port()
		if port == "" {
			port = "443"
		}
		tr.TLSClientConfig = irc.Config{Addr: net.JoinHostPort(u.Hostname(), port), Pin: pin}.TLSConfig()
	}
	resp, err := (&http.Client{Transport: tr}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusCreated:
	case http.StatusRequestEntityTooLarge:
		return "", errors.New("the file is too big for the bouncer (soju takes up to 50 MB)")
	default:
		return "", fmt.Errorf("the bouncer said %s", resp.Status)
	}
	loc, err := u.Parse(resp.Header.Get("Location"))
	if err != nil {
		return "", err
	}
	return loc.String(), nil
}

// uploaded sends the link once the upload is done.
func (a *App) uploaded(msg uploadDoneMsg) {
	if msg.err != nil {
		a.addLine(msg.w, state.RoleQuit, "* Upload failed: "+msg.err.Error())
		return
	}
	if a.sendNet(msg.w.Net, "PRIVMSG", msg.target, msg.link) && !a.echoed(msg.w.Net) {
		a.addLine(msg.w, state.RoleOwn, "-> *"+msg.target+"* "+msg.link)
	}
}
