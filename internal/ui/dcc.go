package ui

import (
	"cmp"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ergochat/irc-go/ircmsg"

	"github.com/MaxRenaud/trout/internal/state"
)

// DCC Get: files offered with DCC SEND, which is how XDCC bots send.
// The bouncer passes the offer through untouched, and trout connects
// straight to the sender from this computer: no open ports needed, but
// the sender sees this computer's address, not the bouncer's.

// dccModes are config.toml's dcc-get values, by Options radio button.
var dccModes = []string{"ask", "auto", "ignore"}

// askedWithin is how recently you must have messaged a nick for its offer
// to count as one you asked for (/msg Bot xdcc send #5).
const askedWithin = 5 * time.Minute

// errCancelled ends a download you stopped.
var errCancelled = errors.New("cancelled")

// transfer is one DCC download.
type transfer struct {
	net        *state.Network
	nick       string
	trusted    bool   // on Options > DCC's trusted list
	file       string // the name offered
	path       string // where it's saved
	addr, port string // the sender's
	size       int64  // 0 if not said
	from, got  int64  // resumed from, and received so far
	started    time.Time
	ended      time.Time // when it failed or finished
	resuming   bool      // sent DCC RESUME, waiting for DCC ACCEPT
	done       bool
	err        error
	cancel     context.CancelFunc
	win        *state.Window // its Get window
	base       string        // the folder and its name, before any renaming
	existing   int64         // the size of a file already at base, or -1
	renamed    string        // a free name next to base, for Rename

	rate   float64 // bytes a second, smoothed
	lastAt time.Time
}

type (
	dccProgressMsg struct {
		t   *transfer
		got int64
	}
	dccDoneMsg struct {
		t   *transfer
		got int64
		err error
	}
)

// dccOffer handles a DCC CTCP from someone: SEND offers a file, ACCEPT
// answers our RESUME. Anything else (CHAT) trout doesn't do.
func (a *App) dccOffer(n *state.Network, m ircmsg.Message, args string, t time.Time) {
	nick, status := m.Nick(), n.Status()
	sub, rest, _ := strings.Cut(args, " ")
	switch strings.ToUpper(sub) {
	case "SEND":
	case "ACCEPT":
		a.dccAccept(n, nick, rest)
		return
	case "CHAT":
		a.chatOffer(n, nick, rest, a.dccTrusted(m.Source), t)
		return
	default:
		a.addLineAt(status, state.RoleInfo, "* "+nick+" offers a DCC "+strings.ToUpper(sub)+", which trout doesn't do", t)
		return
	}
	file, ip, port, size, err := parseDCCSend(rest)
	switch {
	case err != nil:
		a.addLineAt(status, state.RoleInfo, "* "+nick+" sent a DCC offer trout can't read: "+err.Error(), t)
		return
	case time.Since(t) > 2*time.Minute: // replayed: the sender gave up long ago
		return
	case port == "0":
		a.addLineAt(status, state.RoleInfo, "* "+nick+" offers "+file+" with passive DCC, which trout doesn't do yet", t)
		return
	case a.dccGet == 2:
		a.addLineAt(status, state.RoleInfo, "* Ignored "+file+" ("+sizeText(size)+") from "+nick+"; Options > DCC changes that", t)
		return
	case a.dccTypeIgnored(safeName(file)):
		a.dccIgnoredLine(n, file, nick, t)
		return
	}
	tr := &transfer{net: n, nick: nick, trusted: a.dccTrusted(m.Source), file: file, addr: net.JoinHostPort(ip.String(), port), port: port, size: size}
	a.whenFree(func(a *App) tea.Cmd {
		if a.dccWarn {
			a.openDialog(a.dccPrivacyBox(tr, ip))
			return nil
		}
		return a.dccDecide(tr, ip)
	})
}

// parseDCCSend reads `filename ip port size [token]`; the file name may
// be quoted, and the IP is a number for IPv4 or an IPv6 address.
func parseDCCSend(s string) (file string, ip net.IP, port string, size int64, err error) {
	s = strings.TrimSpace(s)
	if rest, ok := strings.CutPrefix(s, `"`); ok {
		var found bool
		file, s, found = strings.Cut(rest, `"`)
		if !found {
			return "", nil, "", 0, errors.New("unclosed quote")
		}
	} else {
		file, s, _ = strings.Cut(s, " ")
	}
	f := strings.Fields(s)
	if file == "" || len(f) < 2 {
		return "", nil, "", 0, errors.New("missing fields")
	}
	if v, err := strconv.ParseUint(f[0], 10, 32); err == nil {
		ip = net.IPv4(byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
	} else if ip = net.ParseIP(f[0]); ip == nil {
		return "", nil, "", 0, fmt.Errorf("bad address %q", f[0])
	}
	if _, err := strconv.ParseUint(f[1], 10, 16); err != nil {
		return "", nil, "", 0, fmt.Errorf("bad port %q", f[1])
	}
	if len(f) > 2 {
		size, _ = strconv.ParseInt(f[2], 10, 64)
	}
	return file, ip, f[1], size, nil
}

// localAddr reports whether an offer points at this computer or the
// local network, which a stranger could use to make trout connect to
// things there.
func localAddr(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified()
}

// dccPrivacyBox explains, once, what accepting a DCC file means.
func (a *App) dccPrivacyBox(tr *transfer, ip net.IP) *msgBox {
	d := message(
		tr.nick+" on "+tr.net.Name+" offers you a file with DCC.",
		"",
		"Accepting connects this computer straight to the sender,",
		"so they see this computer's IP address, not the bouncer's.",
		"Everything else you do goes through the bouncer.",
		"",
		"Decline if that matters to you.",
	)
	d.Title = "DCC and Your Privacy"
	d.Buttons, d.Cancel = []string{"Continue", "Decline"}, 1
	d.Check = "Don't show this again"
	d.OnClose = func(a *App, button int) tea.Cmd {
		if d.Checked {
			a.dccWarn = false
		}
		if button == 0 {
			return a.dccDecide(tr, ip)
		}
		if d.Checked { // declined for good: stop asking
			a.dccGet = 2
			a.addLine(tr.net.Status(), state.RoleInfo, "* DCC file offers will be ignored; Options > DCC changes that")
		} else {
			a.addLine(tr.net.Status(), state.RoleInfo, "* Declined "+tr.file+" from "+tr.nick)
		}
		return nil
	}
	return d
}

// dccDecide gets the file without asking when that's the setting, or asks.
// Offers pointing at this computer or its network always ask.
func (a *App) dccDecide(tr *transfer, ip net.IP) tea.Cmd {
	if err := a.dccTarget(tr); err != nil {
		a.addLine(tr.net.Status(), state.RoleQuit, "* Can't get "+tr.file+": "+err.Error())
		return nil
	}
	// Asked for: you messaged them just now (XDCC), or it's the same file
	// from the same sender just after a failed try, which senders often
	// re-offer. One you cancelled isn't asked for again.
	asked := time.Since(a.messaged[tr.net.ID+" "+strings.ToLower(tr.nick)]) < askedWithin
	if o := a.earlierTry(tr); o != nil && !errors.Is(o.err, errCancelled) && time.Since(o.ended) < askedWithin {
		asked = true
	}
	if !localAddr(ip) && (a.dccGet == 1 || tr.trusted || (a.dccAutoAsked && asked)) {
		return a.dccStart(tr)
	}
	d := message(
		tr.nick+" on "+tr.net.Name+" offers you a file:",
		"",
		"    "+tr.file+"  ("+sizeText(tr.size)+")",
		"",
		"Save to: "+tr.path,
	)
	d.Title = "DCC Get"
	d.Buttons, d.Cancel = []string{"Accept", "Decline"}, 1
	// A file of that name is already there: like mIRC, ask whether to
	// resume it, overwrite it, or save under another name.
	if tr.existing >= 0 {
		d.Lines[4] = fmt.Sprintf("%s is already there (%s).", tr.base, sizeText(tr.existing))
		d.Buttons = []string{"Overwrite", "Rename", "Decline"}
		if tr.existing < tr.size {
			d.Buttons = slices.Insert(d.Buttons, 0, "Resume")
		}
		d.Cancel = len(d.Buttons) - 1
	}
	if localAddr(ip) {
		d.Lines = append(d.Lines, "", "Careful: it points at "+ip.String()+", on this computer or your",
			"own network. Only accept if you expect that.")
		d.Sel = d.Cancel
	}
	d.OnClose = func(a *App, button int) tea.Cmd {
		switch d.Buttons[button] {
		case "Decline":
			a.addLine(tr.net.Status(), state.RoleInfo, "* Declined "+tr.file+" from "+tr.nick)
			return nil
		case "Resume":
			tr.path, tr.from = tr.base, tr.existing
		case "Overwrite":
			tr.path, tr.from = tr.base, 0
		case "Rename":
			tr.path, tr.from = tr.renamed, 0
		}
		return a.dccStart(tr)
	}
	a.openDialog(d)
	return nil
}

// dccTarget picks where tr goes in the download folder when nobody's
// asked: what an earlier try at it from the same sender left is resumed,
// any other file of that name is kept (a new name instead: it may have
// nothing to do with the offer). It notes the file that's there, if any,
// for the get dialog's Resume / Overwrite / Rename.
func (a *App) dccTarget(tr *transfer) error {
	name := safeName(tr.file)
	dir, err := a.dccFolder(name) // Options > DCC > Folders
	if err != nil {
		return err
	}
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	tr.base, tr.existing, tr.renamed = filepath.Join(dir, name), -1, ""
	if info, err := os.Stat(tr.base); err == nil && info.Mode().IsRegular() {
		tr.existing = info.Size()
	}
	for i := 1; tr.renamed == ""; i++ { // the first free "name (n).ext"
		path := filepath.Join(dir, fmt.Sprintf("%s (%d)%s", stem, i, ext))
		switch _, err := os.Stat(path); {
		case errors.Is(err, os.ErrNotExist):
			tr.renamed = path
		case err != nil:
			return err
		}
	}
	tr.path = tr.base
	if tr.existing >= 0 {
		tr.path = tr.renamed
	}
	if o := a.earlierTry(tr); o != nil {
		if info, err := os.Stat(o.path); err == nil && info.Mode().IsRegular() && info.Size() > 0 && info.Size() < tr.size {
			tr.path, tr.from = o.path, info.Size()
		}
	}
	return nil
}

// earlierTry is the latest download of the same file from the same
// sender that didn't finish, or nil.
func (a *App) earlierTry(tr *transfer) *transfer {
	for _, o := range slices.Backward(a.transfers) {
		if o.err != nil && o.net == tr.net && strings.EqualFold(o.nick, tr.nick) && o.file == tr.file {
			return o
		}
	}
	return nil
}

// dccStart gets the file: right away, or after the sender agrees to
// resume (DCC RESUME, then its DCC ACCEPT).
func (a *App) dccStart(tr *transfer) tea.Cmd {
	a.transfers = append(a.transfers, tr)
	// Like mIRC, each download gets its own window, which doesn't take
	// the focus from what you're typing in.
	tr.win = tr.net.Add(&state.Window{Kind: state.KTransfer, Name: "Get " + tr.file})
	if tr.from > 0 {
		tr.resuming = true
		file := tr.file
		if strings.Contains(file, " ") {
			file = `"` + file + `"`
		}
		a.sendNet(tr.net, "PRIVMSG", tr.nick, fmt.Sprintf("\x01DCC RESUME %s %s %d\x01", file, tr.port, tr.from))
		a.addLine(tr.net.Status(), state.RoleInfo, "* DCC: asking "+tr.nick+" to resume "+tr.file+" at "+sizeText(tr.from))
		return nil
	}
	return a.dccGo(tr)
}

// dccAccept is the sender agreeing to resume: `filename port position`.
func (a *App) dccAccept(n *state.Network, nick, args string) {
	f := strings.Fields(args)
	if len(f) < 3 {
		return
	}
	port := f[len(f)-2]
	for _, tr := range a.transfers {
		if tr.resuming && tr.net == n && strings.EqualFold(tr.nick, nick) && tr.port == port {
			tr.resuming = false
			a.later(a.dccGo(tr))
			return
		}
	}
}

// dccGo connects to the sender and downloads in the background.
func (a *App) dccGo(tr *transfer) tea.Cmd {
	ctx, cancel := context.WithCancel(context.Background())
	tr.cancel, tr.started, tr.got, tr.lastAt = cancel, time.Now(), tr.from, time.Now()
	a.addLine(tr.net.Status(), state.RoleInfo, fmt.Sprintf("* DCC: getting %s (%s) from %s", tr.file, sizeText(tr.size), tr.nick))
	send, addr, path, from, size := a.send, tr.addr, tr.path, tr.from, tr.size
	dial, timeout := a.dialer(true), a.dccTimeout()
	return func() tea.Msg {
		got, err := download(ctx, dial, timeout, addr, path, from, size, func(got int64) { send(dccProgressMsg{tr, got}) })
		if ctx.Err() != nil {
			err = errCancelled
		}
		return dccDoneMsg{tr, got, err}
	}
}

// download gets a DCC SEND into path, from byte from on. Like every DCC
// client, it acknowledges each read with the total received as a 32-bit
// number; some senders wait for that.
// It gives up when connecting or a read takes longer than timeout
// (Options > DCC).
func download(ctx context.Context, dial func(context.Context, string, string) (net.Conn, error), timeout time.Duration,
	addr, path string, from, size int64, progress func(int64)) (int64, error) {
	dialCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	c, err := dial(dialCtx, "tcp", addr)
	if err != nil {
		return from, err
	}
	defer c.Close()
	stop := context.AfterFunc(ctx, func() { c.Close() })
	defer stop()

	flags := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	if from > 0 {
		flags = os.O_WRONLY | os.O_APPEND
	}
	f, err := os.OpenFile(path, flags, 0o644)
	if err != nil {
		return from, err
	}
	got, buf, ack := from, make([]byte, 64<<10), make([]byte, 4)
	last := time.Now()
	for size == 0 || got < size {
		c.SetReadDeadline(time.Now().Add(timeout))
		k, rerr := c.Read(buf)
		if k > 0 {
			if _, err := f.Write(buf[:k]); err != nil {
				f.Close()
				return got, err
			}
			got += int64(k)
			binary.BigEndian.PutUint32(ack, uint32(got))
			c.Write(ack)
			if time.Since(last) > 250*time.Millisecond {
				last = time.Now()
				progress(got)
			}
		}
		if errors.Is(rerr, io.EOF) {
			break
		}
		if rerr != nil {
			f.Close()
			return got, rerr
		}
	}
	if err := f.Close(); err != nil {
		return got, err
	}
	if size > 0 && got < size {
		return got, fmt.Errorf("the sender stopped at %s of %s", sizeText(got), sizeText(size))
	}
	return got, nil
}

// dccUpdate handles a download's progress and end.
func (a *App) dccUpdate(msg tea.Msg) {
	switch msg := msg.(type) {
	case dccProgressMsg:
		tr := msg.t
		if dt := time.Since(tr.lastAt).Seconds(); dt > 0 {
			now := float64(msg.got-tr.got) / dt
			tr.rate = cmp.Or(tr.rate*0.7+now*0.3, now)
		}
		tr.got, tr.lastAt = msg.got, time.Now()
	case dccDoneMsg:
		tr := msg.t
		tr.got, tr.done, tr.err, tr.ended = msg.got, true, msg.err, time.Now()
		if tr.win != nil && tr.win != a.active {
			tr.win.Act = state.ActMsg
		}
		if tr.err != nil {
			got := ""
			if tr.got > 0 {
				got = fmt.Sprintf(" after %s of %s; a new offer resumes it", sizeText(tr.got), sizeText(tr.size))
			}
			a.addLine(tr.net.Status(), state.RoleQuit, "* DCC: "+tr.file+" from "+tr.nick+" failed"+got+": "+tr.err.Error())
			return
		}
		secs := max(time.Since(tr.started).Seconds(), 0.001)
		a.addLine(tr.net.Status(), state.RoleInfo, fmt.Sprintf("* DCC: got %s (%s in %.0fs, %s/s) in %s",
			tr.file, sizeText(tr.got), secs, sizeText(int64(float64(tr.got-tr.from)/secs)), tr.path))
		// Like mIRC: a beep when you're not looking, and the window can
		// close by itself (Options > DCC).
		if a.beep && (tr.win != a.active || !a.focused) {
			a.bellDue = true
		}
		if a.dccClose && tr.win != nil && slices.Contains(tr.net.Wins, tr.win) {
			a.removeWindow(tr.win)
		}
	}
}

// transferFor finds the download a Get window shows.
func (a *App) transferFor(w *state.Window) *transfer {
	if i := slices.IndexFunc(a.transfers, func(tr *transfer) bool { return tr.win == w }); i >= 0 {
		return a.transfers[i]
	}
	return nil
}

// cancelTransfer stops a download, or one waiting for the sender to
// agree to resume.
func (a *App) cancelTransfer(tr *transfer) {
	switch {
	case tr == nil || tr.done:
	case tr.cancel != nil:
		tr.cancel()
	default:
		tr.done, tr.err, tr.resuming, tr.ended = true, errCancelled, false, time.Now()
	}
}

// drawTransfer is a Get window, like mIRC's: who's sending what, how far
// along it is with a progress bar, how fast, and where it goes.
func (a *App) drawTransfer(c *Canvas, win *state.Window, x, y, w, h int) {
	t := a.theme()
	face := Style{FG: t.FaceText, BG: t.Face}
	bold := Style{FG: t.FaceText, BG: t.Face, Bold: true}
	c.Fill(x, y, w, h, face)
	tr := a.transferFor(win)
	if tr == nil {
		return
	}
	tx, tw, row := x+2, w-4, y+1
	text := func(label, value string) {
		c.Text(tx, row, 12, label, bold)
		c.Text(tx+12, row, tw-12, value, face)
		row++
	}
	c.Text(tx, row, tw, "Receiving "+tr.file+" from "+tr.nick+" on "+tr.net.Name, bold)
	row += 2
	pct := int64(0)
	if tr.size > 0 {
		pct = tr.got * 100 / tr.size
	}
	text("Received:", fmt.Sprintf("%s of %s (%d%%)", sizeText(tr.got), sizeText(tr.size), pct))
	// A Windows 98 progress bar, as in the setup wizard.
	bw := min(tw, 60)
	c.Fill(tx, row, bw, 1, Style{BG: t.TextBG})
	for i := range int(int64(bw) * pct / 100) {
		if i%2 == 0 {
			c.Set(tx+i, row, ' ', Style{BG: t.MenuSel})
		}
	}
	row += 2
	status, left := "", ""
	switch {
	case tr.err != nil:
		status = "Failed: " + tr.err.Error()
	case tr.done:
		status = fmt.Sprintf("Done in %s", time.Since(tr.started).Round(time.Second))
	case tr.resuming:
		status = "Waiting for " + tr.nick + " to agree to resume"
	default:
		status = sizeText(int64(tr.rate)) + "/s"
		if tr.rate > 0 && tr.size > tr.got {
			left = (time.Duration(float64(tr.size-tr.got)/tr.rate) * time.Second).Round(time.Second).String()
		}
	}
	text("Status:", status)
	if left != "" {
		text("Time left:", left)
	}
	text("Saved to:", tr.path)
	row++
	label := "Cancel"
	if tr.done {
		label = "Open Folder"
	}
	n := a.button(c, tx, row, "  "+label+"  ", Style{FG: t.FaceText, BG: t.Face}, 1)
	a.addRegion(Region{X: tx, Y: row, W: n, H: 1, Kind: HitTransfer, Win: win})
}

// transferButton is the Get window's Cancel or Open Folder.
func (a *App) transferButton(win *state.Window) tea.Cmd {
	tr := a.transferFor(win)
	if tr == nil {
		return nil
	}
	if !tr.done {
		a.cancelTransfer(tr)
		return nil
	}
	return openPath(filepath.Dir(tr.path))
}

// openReceived is the toolbar's Received: the download folder, like
// mIRC's.
func (a *App) openReceived() tea.Cmd {
	dir, err := a.downloads()
	if err != nil {
		a.addLine(a.active, state.RoleInfo, "* "+err.Error())
		return nil
	}
	return openPath(dir)
}

// later runs cmd as if Update had returned it.
func (a *App) later(cmd tea.Cmd) {
	if cmd != nil {
		go func() { a.send(cmd()) }()
	}
}

// downloads is the folder files are saved to, made if needed.
func (a *App) downloads() (string, error) {
	dir := a.downloadDir
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, "Downloads")
	}
	return dir, os.MkdirAll(dir, 0o755)
}

// safeName keeps a file name from going anywhere but where it's put: no
// folders, no hidden files, no control characters.
func safeName(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, `\`, "/"))
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == '/' {
			return -1
		}
		return r
	}, name)
	name = strings.TrimLeft(strings.TrimSpace(name), ".")
	if len(name) > 200 {
		name = name[:200]
	}
	return cmp.Or(name, "download")
}

// sizeText is a byte count for people: "12.3 MB".
func sizeText(n int64) string {
	switch {
	case n <= 0:
		return "size unknown"
	case n < 1<<10:
		return fmt.Sprintf("%d bytes", n)
	case n < 1<<20:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	case n < 1<<30:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%.2f GB", float64(n)/(1<<30))
}

// openPath shows a folder in the desktop's file manager.
func openPath(p string) tea.Cmd {
	return func() tea.Msg {
		var cmd *exec.Cmd
		switch runtime.GOOS {
		case "darwin":
			cmd = exec.Command("open", p)
		case "windows":
			cmd = exec.Command("explorer", p)
		default:
			cmd = exec.Command("xdg-open", p)
		}
		_ = cmd.Start()
		return nil
	}
}
