// Package ui is trout's terminal interface: an mIRC-style window frame
// drawn into a cell canvas, driven by Bubble Tea. It talks to soju through
// internal/irc, or plays the fake world from internal/demo in demo mode.
package ui

import (
	"cmp"
	"crypto/tls"
	"io"
	"math/rand/v2"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/MaxRenaud/trout/internal/config"
	"github.com/MaxRenaud/trout/internal/demo"
	"github.com/MaxRenaud/trout/internal/irc"
	"github.com/MaxRenaud/trout/internal/local"
	"github.com/MaxRenaud/trout/internal/state"
	"github.com/MaxRenaud/trout/internal/termkeys"

	tea "github.com/charmbracelet/bubbletea"
)

// Holding the left button on a nick, or a treebar or switchbar entry, this
// long opens its popup, for terminals like iTerm2 that keep right-clicks to
// themselves.
const longPress = 500 * time.Millisecond

type pressHold struct {
	id   int
	r    Region
	x, y int
}

type holdMsg struct{ id int }

type dragKind int

const (
	dragNone dragKind = iota
	dragTree
	dragNick
	dragThumb  // a scrollbar thumb; see thumbKind
	dragSelect // selecting text; see sel
	dragEdit   // selecting text in an editbox; see editDrag
)

// Options configure a new App.
type Options struct {
	Demo bool // play the fake world instead of connecting

	// Config is config.toml, where settings and bouncers are saved as they
	// change. Without one (demo mode, tests) nothing is saved.
	Config *config.Config

	// Bouncer is the soju to connect to, with its password. Without an
	// address, trout opens the Connect dialog first.
	Bouncer  config.Bouncer
	Password string

	Debug func(dir, line string) // sees every raw IRC line, if set

	// Version is trout's, for About and CTCP VERSION: "v0.1.0", or a
	// development build's "dev" and commit.
	Version string
}

type App struct {
	opts   Options
	w, h   int
	nets   []*state.Network
	active *state.Window

	// Real mode: home is the bouncer's own status window, and the network
	// list soju reports; every network has its own connection.
	cfg         *config.Config
	bnc         config.Bouncer // the bouncer in use
	password    string
	saved       config.Settings  // as last written to config.toml
	stopped     bool             // not connected and not retrying, until File > Connect
	clientCert  *tls.Certificate // this computer\'s, for logging in to TLS bouncers
	certFailed  bool             // the bouncer refused it this session: use the password
	certNoted   bool             // said once that the bouncer can\'t take certificates
	send        func(tea.Msg)    // delivers messages from connection goroutines
	home        *state.Network
	control     *irc.Conn
	conns       map[*state.Network]*irc.Conn
	batches     map[string]*histBatch
	joining     map[string]bool // channels we asked to join, to focus them
	listing     map[*state.Network]*listBox
	listRows    map[*state.Network][]demo.ChanInfo
	chanLists   map[*state.Network]*chanList // the Channels List's last download, per network
	listWaiting map[*state.Network]int       // a LIST trout sent, by its listSeq
	listFilter  map[*state.Network]listFilter
	listSeq     int
	history     map[*state.Window]bool                  // chathistory already fetched
	serving     []*servCmd                              // BouncerServ commands awaiting replies
	focusNet    string                                  // a network being added, to focus when soju has it
	unread      map[*state.Network]map[string]time.Time // private chats to open if unread, by latest message
	probing     map[*state.Network]map[string]time.Time // missed private chats whose history decides if they get a window, by read marker

	focused  bool      // the terminal has focus (assumed until it reports otherwise)
	readSent time.Time // when trout last told soju what's been read
	readTick bool      // a readTickMsg is on its way

	beep      bool               // ring the bell for highlights and private messages
	colors    map[state.Role]int // the Colors dialog\'s palette choices; role -1 is the background
	hoverTab  *state.Window      // the switchbar button under the mouse, showing its ×
	lastHover *state.Window      // last frame\'s hoverTab, so a button stays hovered over its ×
	bellOut   io.Writer          // the terminal, for the bell and iTerm2's profile switch; nil in tests
	// The terminal and its Option key (optionkey.go): optionKey is ""
	// until asked, "fix" for iTerm2's profile switch, "never" to stop
	// asking; itermRestore switches the tab back when trout quits.
	term         termkeys.Terminal
	termHome     string
	optionKey    string
	itermRestore string
	bellDue      bool
	lastBell     time.Time

	find      *finder                  // Ctrl+F's highlighted match
	findText  string                   // the last search, to search again
	searching map[*state.Network]*goTo // a bouncer history search on its way
	goingTo   *goTo                    // a search result whose history is on its way

	lists [listKinds]map[string][]string // per-network lists without a config.toml (tests)

	// DCC and file uploads (dcc.go, upload.go).
	dccGet       int  // an index into dccModes: ask, auto or ignore
	dccAutoAsked bool // with "ask", auto-get from nicks you just messaged
	dccWarn      bool // show the privacy notice first
	dccChat      int  // DCC CHAT offers: an index into dccModes
	// Options > DCC: file types (an index into dccIgnoreModes) and the
	// time-out in seconds.
	dccIgnoreMode, dccTimeoutS int
	proxyPass                  string // the proxy's password without a config (tests)
	dccClose                   bool   // close a Get window when its file is in

	// Options > IRC and Other (see config.Settings).
	stampFormat    string
	rejoinOnKick   bool
	joinOnInvite   bool
	noticesActive  bool
	whoisOnQuery   bool
	confirmPaste   bool
	stripCodes     bool
	keepOpenOnPart bool
	prefixOwn      bool
	ctcpActive     bool
	awayCancel     bool
	invisible      bool
	popFavorites   bool
	popped         bool // the Favorites already popped up this session

	// Options > Other, Display and Messages (see config.Settings).
	confirmExit   bool
	cmdPrefix     string
	tabSuffix     string
	sortSwitch    bool
	fillSwitch    bool
	hiActive      bool
	privateTo     int // an index into privateModes
	showAddress   bool
	nicksOnJoin   bool
	notifyActive  bool
	memBook       config.Config // the Address Book without a config.toml (tests)
	nickLeftX     int           // a left nicklist's x, for dragging its edge; -1 on the right
	selfCTCP      string        // the last CTCP to ourselves (msgid, or what it said), which comes twice
	selfCTCPAt    time.Time
	editDrag      int                    // the editbox (1 or 2) a drag is selecting in
	namesOnJoin   map[*state.Window]bool // channels just joined, to list their nicks (Options > Messages)
	lastFrom      string                 // who last messaged you, for Tab, when private messages
	lastFromNet   *state.Network         // don't get query windows
	downloadDir   string
	messaged      map[string]time.Time // whom you last messaged, by network ID + " " + nick
	transfers     []*transfer
	chats         []*dccChat
	waiting       []func(a *App) tea.Cmd       // dialogs to open once nothing is (whenFree)
	installing    *installWizard               // the setup running, whose messages count
	hooks         *installHooks                // tests: no ssh, binaries or login for real
	moving        []movedNet                   // networks to make on a new bouncer once it's connected
	movingTo      string                       // that bouncer's name
	netsBatch     string                       // the batch of the bouncer's network list, while it comes
	highHalf      rune                         // Windows: an emoji's first UTF-16 half, waiting for its second
	unechoed      map[*state.Network][]sentMsg // sent, not yet echoed back by the bouncer
	liveness      [3]time.Duration             // tests: irc.Config's Idle, AfterSend, PingWait
	local         *local.Soju                  // trout's own soju, for the This Computer bouncer
	localStarting bool                         // one is starting
	cloudTokens   map[string]string            // without a config (tests): cloud API tokens
	dccSeq        int

	urls         []seenURL                    // URLs List
	ctcpSent     []time.Time                  // when trout answered CTCPs lately, for flood protection
	isonOn       bool                         // the notify poll for networks without MONITOR is running
	away         string                       // the away message, on every network
	quietWho     map[string]bool              // WHOs trout sent itself, by whoKey
	hopping      map[string]bool              // channels /hop is parting, to keep their windows
	upAt         map[*state.Network]time.Time // when each connection came up, for the online timer
	listFetch    map[string][]state.ListEntry // channel lists on their way, by listFetchKey
	chanListBox  *listBox                     // Channel Central's open list, to refresh
	chanListWin  *state.Window
	chanListMode byte
	invitedTo    map[*state.Network]string // the last channel invited to, for /join -i
	whois        map[*state.Network]string // the nick whose WHOIS reply is coming in

	// Settings, reachable from the View menu and Tools > Options.
	themeIdx                                int
	showToolbar, showSwitchbar, showTreebar bool
	showNicklist, stamps, modePrefix        bool
	showMarker, shading, nickColors         bool
	bouncer                                 int // 0 = none, else bouncers[bouncer-1]
	bouncers                                []bouncer
	treeW, nickW                            int

	// Rebuilt on every render, so mouse events are hit-tested against the
	// frame that's on screen.
	regions        []Region
	menuBarX       [len(menuBarNames)]int
	msgW, msgH     int // size of the active message pane
	msgX, msgY     int // where its text starts on screen
	msgTop         int // first visible row of windowRows
	caretX, caretY int // editbox cursor
	sbars          map[HitKind]scrollGeom

	menus   []openMenu // open popups, innermost last
	menuBar int        // menubar entry that opened menus[0], or -1
	dlg     modal      // drawn over everything and takes all input
	pal     *palette   // Ctrl+K color picker
	sel     *selection // text being selected in the message pane

	nickFocus bool          // keyboard focus is in the nicklist (Shift+Tab)
	lastQuery *state.Window // where someone last messaged you, for Tab on an empty line

	mx, my      int
	hold        *pressHold // a left press on a nick that may become a long-press
	holdSeq     int
	drag        dragKind
	thumbKind   HitKind // which scrollbar a dragThumb is moving
	thumbGrab   int     // row within the thumb that was grabbed
	pressedTool int
	lastHit     Region
	lastClick   time.Time
	title       string
}

// Run starts the UI and blocks until the user quits.
func Run(opts Options) error {
	a := New(opts)
	p := tea.NewProgram(a, tea.WithAltScreen(), tea.WithMouseAllMotion(), tea.WithReportFocus())
	a.send = p.Send
	a.bellOut = os.Stdout
	env := termkeys.SystemEnv()
	a.term, a.termHome = termkeys.Detect(env), env.Home
	_, err := p.Run()
	a.stopITermProfile() // back to the tab's own profile
	a.local.Stop()       // trout's own soju, if it started one
	return err
}

func New(opts Options) *App {
	a := &App{
		opts:    opts,
		cfg:     opts.Config,
		menuBar: -1, pressedTool: -1,
		sbars:       map[HitKind]scrollGeom{},
		conns:       map[*state.Network]*irc.Conn{},
		batches:     map[string]*histBatch{},
		joining:     map[string]bool{},
		listing:     map[*state.Network]*listBox{},
		listRows:    map[*state.Network][]demo.ChanInfo{},
		chanLists:   map[*state.Network]*chanList{},
		listWaiting: map[*state.Network]int{},
		listFilter:  map[*state.Network]listFilter{},
		history:     map[*state.Window]bool{},
		unread:      map[*state.Network]map[string]time.Time{},
		probing:     map[*state.Network]map[string]time.Time{},
		focused:     true,
		quietWho:    map[string]bool{},
		hopping:     map[string]bool{},
		namesOnJoin: map[*state.Window]bool{},
		upAt:        map[*state.Network]time.Time{},
		listFetch:   map[string][]state.ListEntry{},
		invitedTo:   map[*state.Network]string{},
		searching:   map[*state.Network]*goTo{},
		messaged:    map[string]time.Time{},
		whois:       map[*state.Network]string{},
		send:        func(tea.Msg) {},
	}
	a.applySettings(config.DefaultSettings())
	a.memBook.DCCIgnore = slices.Clone(config.DefaultDCCIgnore)
	if a.cfg != nil {
		a.applySettings(a.cfg.Settings)
	}
	a.saved = a.settings()
	if !opts.Demo {
		a.setBouncer(opts.Bouncer, opts.Password)
		return a
	}
	a.nets = demo.Seed()
	a.active = a.nets[0].Wins[1]
	// Treat the seeded history as read, so new traffic lands below each
	// window's line marker.
	for _, w := range a.order() {
		if w != a.active {
			w.Marker = len(w.Lines)
		}
	}
	return a
}

func (a *App) Init() tea.Cmd {
	if a.opts.Demo {
		return tick()
	}
	if !a.bouncerSet() {
		a.stopped = true
		a.openConnect("")
		return nil
	}
	return tea.Batch(a.connectBouncer(), a.optionKeyStart())
}

type tickMsg struct{}

func tick() tea.Cmd {
	d := time.Duration(1500+rand.IntN(3500)) * time.Millisecond
	return tea.Tick(d, func(time.Time) tea.Msg { return tickMsg{} })
}

func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.w, a.h = msg.Width, msg.Height
	case tickMsg:
		if ev, ok := demo.Traffic(a.nets); ok {
			a.addLine(ev.Win, ev.Role, ev.Text)
			if ev.Win.Kind == state.KQuery {
				a.lastQuery = ev.Win
			}
		}
		cmd = tick()
	case ircUpMsg, ircLineMsg, ircDownMsg, reconnectMsg:
		cmd = a.updateIRC(msg)
	case passwordMsg:
		cmd = a.gotPassword(msg)
	case isonTickMsg:
		cmd = a.pollNotify()
	case controlMsg:
		a.controlDue(msg)
	case listTimeoutMsg:
		if a.listWaiting[msg.net] == msg.seq {
			a.listFailed(msg.net, "no answer from the server; try Get List again")
		}
	case dccProgressMsg, dccDoneMsg:
		a.dccUpdate(msg)
	case chatUpMsg, chatLineMsg, chatDownMsg:
		a.chatUpdate(msg)
	case uploadDoneMsg:
		a.uploaded(msg)
	case instStepMsg, instPhaseMsg:
		cmd = a.installUpdate(msg)
	case localUpMsg:
		cmd = a.localUp(msg)
	case serverDeletedMsg:
		a.serverDeleted(msg)
	case phoneSHA1Msg:
		a.phoneSHA1(msg)
	case tea.FocusMsg:
		a.focused = true
	case tea.BlurMsg:
		a.focused = false
	case readTickMsg:
		a.readTick = false
	case wizardTickMsg:
		if s, ok := a.dlg.(*setupWizard); ok && s.seq == msg.seq && s.step == stepInstall {
			cmd = s.advance()
		}
	case holdMsg:
		if h := a.hold; h != nil && h.id == msg.id {
			a.hold = nil
			switch {
			case a.dlg != nil || len(a.menus) > 0:
			case (h.r.Kind == HitNick || h.r.Kind == HitNickText) && h.r.Win == a.active:
				if a.sel != nil && a.sel.moved {
					break // dragging to select, not holding
				}
				a.drag, a.sel = dragNone, nil
				a.nickMenu(h.r.Win, h.r.S, h.x, h.y)
			case h.r.Kind == HitSwitch || h.r.Kind == HitTree:
				a.windowMenu(h.r.Win, h.x, h.y)
			}
		}
	case tea.KeyMsg:
		if k, ok := a.joinSurrogates(msg); ok {
			cmd = a.key(k)
		}
	case tea.MouseMsg:
		cmd = a.mouse(msg)
	}
	cmd = tea.Batch(cmd, a.openWaiting())
	if t := "trout - [" + a.winTitle(a.active) + "]"; t != a.title {
		a.title = t
		cmd = tea.Batch(cmd, tea.SetWindowTitle(t))
	}
	a.saveSettings()
	return a, tea.Batch(cmd, a.syncRead(), a.bell())
}

func (a *App) key(k tea.KeyMsg) tea.Cmd {
	s := k.String()
	switch s {
	case "ctrl+c":
		// Like mIRC, Ctrl+C copies the editbox's selection and never
		// quits: File > Exit and /exit do. Text selected in the message
		// pane was copied when the mouse let go.
		if a.dlg != nil || len(a.menus) > 0 || a.active == nil {
			return nil
		}
		if e := a.edit(a.active); e.SelText() != "" {
			text := e.SelText()
			e.Sel = false
			return copyText(text)
		}
		a.addLine(a.active, state.RoleInfo, "* Ctrl+C copies what's selected; File > Exit or /exit quits")
		return nil
	case "f2":
		a.themeIdx = (a.themeIdx + 1) % len(themes)
		return nil
	}
	if a.dlg != nil {
		if f, ok := a.dlg.(*formBox); ok {
			if rs, ok := typedRunes(k); ok {
				return f.typed(a, rs)
			}
		}
		return a.dlg.key(a, s)
	}
	if len(a.menus) > 0 {
		return a.menuKey(s)
	}
	if a.pal != nil && a.paletteKey(s) {
		return nil
	}
	if rest, ok := strings.CutPrefix(s, "alt+"); ok && len(rest) == 1 {
		switch ch := rest[0]; {
		case ch >= '1' && ch <= '9':
			if i, order := int(ch-'1'), a.order(); i < len(order) {
				a.activate(order[i])
			}
			return nil
		case ch == 'o':
			a.openOptions(optDisplay)
			return nil
		case ch == 'l':
			return a.openChannelsList(false)
		case ch == 'j':
			a.openFavorites()
			return nil
		case ch == 'b': // the Address Book, like mIRC
			a.openAddressBook()
			return nil
		case ch == 'n': // its notify list
			a.openBook(tabNotify)
			return nil
		case ch == 'k':
			a.openColors()
			return nil
		case ch == 'u':
			a.openURLsList()
			return nil
		case ch == 'q': // the second editbox, like mIRC
			if a.active.Kind != state.KTransfer {
				a.toggleEdit2(a.active)
			}
			return nil
		default:
			if i := strings.IndexByte(menuMnemonics, ch); i >= 0 {
				a.openMenuBar(i)
				a.menus[0].Sel = 0
				return nil
			}
		}
	}
	if a.nickFocus {
		if cmd, ok := a.nickKey(s); ok {
			return cmd
		}
	}

	w := a.active
	e := a.edit(w)
	w.Act = state.ActNone // seen: for Display > Highlight on message
	if s != "tab" {
		e.ResetTab()
	}
	switch s {
	case "f1":
		a.openDialog(shortcutsBox())
	case "ctrl+f":
		a.openFind()
	case "f3":
		a.findNext(-1)
	case "shift+f3":
		a.findNext(1)
	case "esc":
		a.find = nil
	case "ctrl+n":
		a.cycleKind(state.KChannel)
	case "ctrl+q":
		a.cycleKind(state.KQuery)
	case "alt+right":
		a.cycle(1)
	case "alt+left":
		a.cycle(-1)
	case "ctrl+w":
		a.closeWindow(w)
	case "ctrl+l":
		a.jumpToMarker(w)
	case "pgup":
		w.Scroll += max(1, a.msgH-1)
	case "pgdown":
		w.Scroll -= max(1, a.msgH-1)
	case "shift+tab":
		if w.Kind == state.KChannel && a.showNicklist && len(w.Nicks) > 0 {
			a.nickFocus = true
			if slices.IndexFunc(w.Nicks, func(n state.Nick) bool { return n.Name == w.NickSel }) < 0 {
				w.NickSel = w.Nicks[0].Name
			}
		}
	case "enter":
		return a.submit(w)
	// A new line: mIRC's multi-line editbox. Ctrl+J (a line feed) is
	// the terminal's way, for Macs whose Option key isn't Alt.
	case "alt+enter", "ctrl+j":
		e.Insert('\n')
	case "alt+up", "alt+down": // between the two editboxes
		w.Edit2Focus = w.Edit2On && s == "alt+down"
	case "backspace":
		e.Backspace()
	case "delete":
		e.Delete()
	case "left", "right":
		// With a selection, the cursor goes to its edge, like Windows.
		if from, to, ok := e.Selection(); ok {
			e.Pos = map[bool]int{true: from, false: to}[s == "left"]
			e.Sel = false
			break
		}
		e.Move(map[bool]int{true: -1, false: 1}[s == "left"])
	case "home", "ctrl+a":
		e.Sel = false
		e.Pos, _ = e.Line()
	case "end":
		e.Sel = false
		_, e.Pos = e.Line()
	case "shift+left":
		e.Extend(func() { e.Pos-- })
	case "shift+right":
		e.Extend(func() { e.Pos++ })
	case "shift+home":
		e.Extend(func() { e.Pos, _ = e.Line() })
	case "shift+end":
		e.Extend(func() { _, e.Pos = e.Line() })
	case "shift+up", "shift+down":
		anchor, sel := e.Anchor, e.Sel
		if !sel {
			anchor = e.Pos
		}
		if e.LineMove(map[bool]int{true: -1, false: 1}[s == "shift+up"]) {
			e.Sel, e.Anchor = true, anchor
		}
	case "ctrl+x":
		if t := e.SelText(); t != "" {
			e.DeleteSel()
			return copyText(t)
		}
	case "ctrl+v":
		return pasteClipboard()
	case "ctrl+e": // strikethrough, like mIRC
		e.Wrap(0x1e, 0x1e)
	case "ctrl+pgup": // a line at a time, like mIRC
		w.Scroll++
	case "ctrl+pgdown":
		w.Scroll--
	case "ctrl+home": // the top of the scrollback, or the editbox's start
		if len(e.Buf) == 0 {
			w.Scroll = 1 << 30
		} else {
			e.Pos, e.Sel = 0, false
		}
	case "ctrl+end":
		if len(e.Buf) == 0 {
			w.Scroll = 0
		} else {
			e.Pos, e.Sel = len(e.Buf), false
		}
	case "up", "down":
		// A multi-line editbox moves between its lines; Ctrl+Up/Down
		// is the history there, like mIRC.
		if !e.Multi() || !e.LineMove(map[bool]int{true: -1, false: 1}[s == "up"]) {
			e.History(map[bool]int{true: -1, false: 1}[s == "up"])
		}
	case "ctrl+up":
		e.History(-1)
	case "ctrl+down":
		e.History(1)
	case "tab":
		// Like mIRC, Tab on an empty line starts a reply to whoever last
		// messaged you; otherwise it completes nicks and channels.
		if len(e.Buf) == 0 && a.lastFrom != "" && a.lastFromNet == w.Net && (a.lastQuery == nil || a.lastQuery.Name == messagesWindow) {
			e.Insert([]rune("/msg " + a.lastFrom + " ")...)
			break
		}
		if len(e.Buf) == 0 && a.lastQuery != nil && a.lastQuery.Name != messagesWindow {
			e.Insert([]rune("/msg " + a.lastQuery.Name + " ")...)
			break
		}
		var cands []string
		for _, n := range w.Nicks {
			cands = append(cands, n.Name)
		}
		if w.Kind == state.KQuery {
			cands = append(cands, w.Name)
		}
		for _, x := range w.Net.Wins {
			if x.Kind == state.KChannel {
				cands = append(cands, x.Name)
			}
		}
		e.Complete(cands, func(word string) string {
			if w.Net.IsChannel(word) {
				return ""
			}
			return a.tabSuffix // Options > Other: after a nick at the start of the line
		})
	// mIRC's formatting keys: bold, color, underline, reverse, reset.
	// With text selected, they go around it.
	case "ctrl+b":
		e.Wrap(0x02, 0x02)
	case "ctrl+k":
		a.openPalette()
		e.Wrap(0x03, 0x03)
	case "ctrl+u":
		e.Wrap(0x1f, 0x1f)
	case "ctrl+r":
		e.Wrap(0x16, 0x16)
	case "ctrl+o":
		e.Insert(0x0f)
	default:
		if k.Paste && strings.ContainsAny(string(k.Runes), "\r\n") {
			return a.pasteLines(w, string(k.Runes))
		}
		if rs, ok := typedRunes(k); ok {
			e.Insert(rs...)
		}
	}
	return nil
}

// nickKey handles keys while the nicklist has focus. Keys it doesn't use
// hand focus back to the editbox, so typing a message just works.
func (a *App) nickKey(s string) (tea.Cmd, bool) {
	w := a.active
	if len(w.Nicks) == 0 {
		a.nickFocus = false
		return nil, false
	}
	i := slices.IndexFunc(w.Nicks, func(n state.Nick) bool { return n.Name == w.NickSel })
	move := func(d int) {
		j := clamp(i+d, 0, len(w.Nicks)-1)
		w.NickSel = w.Nicks[j].Name
		w.NickTop = clamp(w.NickTop, j-a.msgH+1, j) // keep it in view
	}
	switch s {
	case "up":
		move(-1)
	case "down":
		move(1)
	case "pgup":
		move(-max(1, a.msgH-1))
	case "pgdown":
		move(max(1, a.msgH-1))
	case "home":
		move(-len(w.Nicks))
	case "end":
		move(len(w.Nicks))
	case "enter":
		if w.NickSel != "" {
			a.openQuery(w.Net, w.NickSel)
		}
	case " ", "shift+f10":
		if w.NickSel != "" {
			x, y := a.caretX, a.caretY
			for _, r := range a.regions {
				if r.Kind == HitNick && r.S == w.NickSel {
					x, y = r.X+2, r.Y
				}
			}
			a.nickMenu(w, w.NickSel, x, y)
		}
	case "tab", "shift+tab", "esc":
		a.nickFocus = false
	default:
		a.nickFocus = false
		return nil, false
	}
	return nil, true
}

func (a *App) mouse(m tea.MouseMsg) tea.Cmd {
	a.mx, a.my = m.X, m.Y
	if a.drag != dragNone {
		switch {
		case m.Action == tea.MouseActionRelease:
			d := a.drag
			a.drag = dragNone
			if d == dragSelect {
				return a.finishSelection()
			}
		case a.drag == dragTree:
			a.treeW = m.X
		case a.drag == dragNick && a.nickLeftX >= 0:
			a.nickW = m.X - a.nickLeftX
		case a.drag == dragNick:
			a.nickW = a.w - m.X - 1
		case a.drag == dragEdit:
			a.dragEditTo(m.X, m.Y)
		case a.drag == dragSelect:
			a.extendSelection(m.X, m.Y)
		default:
			a.dragThumbTo(m.Y)
		}
		return nil
	}
	if m.Action == tea.MouseActionPress && m.Button == tea.MouseButtonLeft {
		r, ok := a.hit(m.X, m.Y)
		if ok && r.S == "thumb" && (r.Kind == HitMsgScroll || r.Kind == HitNickScroll || r.Kind == HitDlg) {
			a.drag, a.thumbKind, a.thumbGrab = dragThumb, r.Kind, m.Y-r.Y
			return nil
		}
	}
	if a.dlg != nil {
		r, ok := a.hit(m.X, m.Y)
		if !ok || r.Kind != HitDlg {
			r = Region{}
		}
		double := false
		if m.Action == tea.MouseActionPress && m.Button == tea.MouseButtonLeft {
			double = r.Kind == HitDlg && r == a.lastHit && time.Since(a.lastClick) < 400*time.Millisecond
			a.lastHit, a.lastClick = r, time.Now()
		}
		return a.dlg.mouse(a, m, r, double)
	}
	if m.Action == tea.MouseActionRelease {
		a.pressedTool, a.hold = -1, nil
		return nil
	}
	if a.hold != nil && m.Action == tea.MouseActionMotion {
		if r, ok := a.hit(m.X, m.Y); !ok || r != a.hold.r {
			a.hold = nil // moved off the nick
		}
		return nil
	}
	if a.pal != nil {
		r, ok := a.hit(m.X, m.Y)
		swatch := ok && r.Kind == HitSwatch
		switch {
		case m.Action == tea.MouseActionMotion:
			if swatch {
				a.pal.sel = r.I
			}
		case m.Button == tea.MouseButtonLeft && swatch:
			a.pickColor(r.I)
		case m.Action == tea.MouseActionPress:
			a.pal = nil
		}
		return nil
	}
	if len(a.menus) > 0 {
		switch {
		case m.Action == tea.MouseActionMotion:
			a.menuMotion(m.X, m.Y)
		case m.Button == tea.MouseButtonLeft || m.Button == tea.MouseButtonRight:
			return a.menuClick(m.X, m.Y)
		}
		return nil
	}
	if m.Action != tea.MouseActionPress {
		return nil
	}
	r, ok := a.hit(m.X, m.Y)
	if !ok {
		return nil
	}
	w := a.active
	switch m.Button {
	case tea.MouseButtonWheelUp, tea.MouseButtonWheelDown:
		d := 3
		if m.Button == tea.MouseButtonWheelDown {
			d = -3
		}
		switch r.Kind {
		case HitMsg, HitMsgScroll, HitURL, HitNickText:
			w.Scroll += d
		case HitNick, HitNickScroll:
			w.NickTop -= d
		}
		return nil
	case tea.MouseButtonLeft, tea.MouseButtonRight:
	default:
		return nil
	}

	// macOS terminals report Ctrl+click as a left click with Ctrl held.
	right := m.Button == tea.MouseButtonRight || m.Ctrl
	double := !right && r == a.lastHit && time.Since(a.lastClick) < 400*time.Millisecond
	a.lastHit, a.lastClick = r, time.Now()
	switch r.Kind {
	case HitCaption:
		return a.quit()
	case HitChildBtn: // a switchbar button's ×
		a.closeWindow(r.Win)
	case HitMenuBar:
		a.openMenuBar(r.I)
	case HitTool:
		a.pressedTool = r.I
		return a.toolAction(toolButtons[r.I])
	case HitSwitch, HitTree:
		switch {
		case m.Shift && r.Kind == HitSwitch:
			a.closeWindow(r.Win)
		case right:
			a.windowMenu(r.Win, m.X, m.Y)
		default:
			a.activate(r.Win)
			return a.startHold(r, m.X, m.Y) // a long press opens its popup too
		}
	case HitTreeToggle:
		r.Net.Collapsed = !r.Net.Collapsed
	case HitTreeSplit:
		a.drag = dragTree
	case HitNickSplit:
		a.drag = dragNick
	case HitMsg, HitURL, HitNickText:
		a.nickFocus = false
		switch {
		case right && r.Kind == HitNickText: // a nick hotlink's popup
			a.nickMenu(w, r.S, m.X, m.Y)
		case m.Ctrl && m.Button == tea.MouseButtonLeft: // copy with codes, like mIRC
			a.startSelection(w, m.X, m.Y)
			a.sel.codes = true
		case right:
		case double && r.Kind == HitNickText:
			a.openQuery(w.Net, r.S)
		case double && r.Kind == HitURL && w.Net.IsChannel(r.S):
			a.join(w.Net, r.S) // a channel hotlink
		case double && r.Kind == HitURL:
			return openURL(r.S)
		default:
			a.startSelection(w, m.X, m.Y)
			a.sel.breaks = m.Shift // copy with the line breaks shown, like mIRC
			if r.Kind == HitNickText {
				return a.startHold(r, m.X, m.Y)
			}
		}
	case HitMsgScroll:
		w.Scroll -= scrollStep(r.I, a.msgH)
	case HitNickScroll:
		w.NickTop += scrollStep(r.I, a.msgH)
	case HitNick:
		w.NickSel = r.S
		a.nickFocus = r.S != ""
		switch {
		case r.S == "":
		case right:
			a.nickMenu(w, r.S, m.X, m.Y)
		case double:
			a.openQuery(w.Net, r.S)
		default:
			return a.startHold(r, m.X, m.Y)
		}
	case HitTransfer:
		return a.transferButton(r.Win)
	case HitEdit:
		a.nickFocus = false
		w.Edit2Focus = r.S == "edit2"
		e := a.edit(w)
		pos := editPosAt(e, r.I, m.X-r.X, r.W)
		if m.Shift {
			e.Extend(func() { e.Pos = pos })
		} else {
			e.Pos, e.Sel, e.Anchor = pos, false, pos
		}
		a.drag, a.editDrag = dragEdit, map[bool]int{true: 2, false: 1}[w.Edit2Focus]
	}
	return nil
}

// startHold starts timing a left press on r: held for longPress, it opens
// r's popup, for terminals that keep right-clicks to themselves.
func (a *App) startHold(r Region, x, y int) tea.Cmd {
	a.holdSeq++
	a.hold = &pressHold{id: a.holdSeq, r: r, x: x, y: y}
	id := a.holdSeq
	return tea.Tick(longPress, func(time.Time) tea.Msg { return holdMsg{id} })
}

// dragThumbTo moves the dragged scrollbar thumb so the row that was
// grabbed follows the pointer.
func (a *App) dragThumbTo(y int) {
	g, ok := a.sbars[a.thumbKind]
	if !ok || g.track <= g.size {
		return
	}
	span := g.track - g.size
	pos := clamp(y-g.trackY-a.thumbGrab, 0, span)
	top := (pos*(g.total-g.visible) + span/2) / span
	switch a.thumbKind {
	case HitMsgScroll:
		a.active.Scroll = g.total - g.visible - top
	case HitNickScroll:
		a.active.NickTop = top
	case HitDlg:
		if l, ok := a.dlg.(*listBox); ok {
			l.Top = top
		}
	}
}

// scrollStep turns a scrollbar part (-1/1 arrows, -2/2 track) into rows,
// negative meaning towards the top.
func scrollStep(part, page int) int {
	switch part {
	case -2:
		return -max(1, page-1)
	case 2:
		return max(1, page-1)
	}
	return part
}

func (a *App) toolAction(label string) tea.Cmd {
	switch label {
	case "Connect": // or Disconnect: a toggle, like mIRC's
		switch n := a.active.Net; {
		case a.opts.Demo:
		case a.connected(n):
			a.disconnectNet(n)
		default:
			return a.connectNet(n)
		}
	case "Options":
		a.openOptions(optDisplay)
	case "Colors": // mIRC's Colors dialog, not the Display options
		a.openColors()
	case "Favorites":
		a.openFavorites()
	case "Channels":
		return a.openChannelsList(false)
	case "Address Book":
		a.openAddressBook()
	case "Notify":
		a.openBook(tabNotify)
	case "Send File":
		a.openSendFile("")
	case "Received":
		return a.openReceived()
	}
	return nil
}

// openURL hands an http(s) link to the desktop's browser.
func openURL(u string) tea.Cmd {
	return func() tea.Msg {
		if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
			return nil
		}
		var cmd *exec.Cmd
		switch runtime.GOOS {
		case "darwin":
			cmd = exec.Command("open", u)
		case "windows":
			cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
		default:
			cmd = exec.Command("xdg-open", u)
		}
		_ = cmd.Start()
		return nil
	}
}

// jumpToMarker scrolls the line marker to the top of the view (Ctrl+L).
func (a *App) jumpToMarker(w *state.Window) {
	rows, marker := a.windowRows(w, a.msgW)
	if marker >= 0 {
		w.Scroll = len(rows) - a.msgH - max(0, marker-1)
	}
}

func (a *App) submit(w *state.Window) tea.Cmd {
	text := a.edit(w).Take()
	if strings.TrimSpace(text) == "" {
		return nil
	}
	w.Scroll = 0
	if w.Kind == state.KTransfer { // nothing to say to a download
		return nil
	}
	// A multi-line editbox sends each line as if typed on its own.
	var cmds []tea.Cmd
	for line := range strings.SplitSeq(text, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if cmd, ok := strings.CutPrefix(line, cmp.Or(a.cmdPrefix, "/")); ok { // Options > Other
			cmds = append(cmds, a.command(w, cmd))
			continue
		}
		a.say(w, line)
	}
	return tea.Batch(cmds...)
}

func (a *App) say(w *state.Window, text string) {
	if w.Kind == state.KStatus {
		a.addLine(w, state.RoleInfo, "* You are not on a channel")
		return
	}
	if a.awayCancel && a.away != "" && (w.Kind == state.KChannel || w.Kind == state.KQuery) {
		a.setAway("") // Options > Messages: back when you talk, like mIRC
	}
	if w.Kind == state.KChat {
		a.chatSay(w, text, false)
		return
	}
	if w.Name == messagesWindow {
		a.addLine(w, state.RoleInfo, "* Reply with /msg nick, or Tab on an empty line")
		return
	}
	if !a.opts.Demo {
		a.privmsg(w, w.Name, text)
		return
	}
	a.addLine(w, state.RoleOwn, "<"+w.Net.Nick+"> "+text)
}

func (a *App) command(w *state.Window, line string) tea.Cmd {
	if !a.opts.Demo {
		return a.ircCommand(w, line)
	}
	return a.demoCommand(w, line)
}

// demoCommand handles a few slash commands on the UI side only.
func (a *App) demoCommand(w *state.Window, line string) tea.Cmd {
	name, rest, _ := strings.Cut(line, " ")
	rest = strings.TrimSpace(rest)
	arg, tail, _ := strings.Cut(rest, " ")
	n := w.Net
	need := func() { a.addLine(w, state.RoleInfo, "* /"+name+": insufficient parameters") }
	switch strings.ToLower(name) {
	case "join", "j":
		if arg == "" {
			need()
			break
		}
		if !strings.HasPrefix(arg, "#") {
			arg = "#" + arg
		}
		a.joinChannel(n, arg)
	case "part", "close":
		a.closeWindow(w)
	case "query":
		if arg == "" {
			need()
			break
		}
		if q := a.openQuery(n, arg); tail != "" {
			a.say(q, tail)
		}
	case "msg":
		if tail == "" {
			need()
			break
		}
		a.addLine(w, state.RoleOwn, "-> *"+arg+"* "+tail)
	case "me":
		if w.Kind == state.KStatus || rest == "" {
			need()
			break
		}
		a.addLine(w, state.RoleAction, "* "+n.Nick+" "+rest)
	case "nick":
		if arg == "" {
			need()
			break
		}
		old := n.Nick
		n.Nick = arg
		for _, x := range n.Wins {
			for i := range x.Nicks {
				if x.Nicks[i].Name == old {
					x.Nicks[i].Name = arg
				}
			}
			x.SortNicks()
			a.addLine(x, state.RoleEvent, "* Your nick is now "+arg)
		}
	case "topic":
		if w.Kind != state.KChannel || rest == "" {
			need()
			break
		}
		w.Topic = rest
		a.addLine(w, state.RoleEvent, "* "+n.Nick+" changes topic to '"+rest+"'")
	case "clear":
		w.Lines, w.Scroll, w.Marker = nil, 0, 0
	case "exit":
		return a.quit()
	default:
		a.addLine(w, state.RoleInfo, "* /"+name+" isn't implemented in this UI mock")
	}
	return nil
}

func (a *App) order() []*state.Window {
	var out []*state.Window
	for _, n := range a.nets {
		out = append(out, n.Wins...)
	}
	return out
}

func (a *App) activate(w *state.Window) {
	if old := a.active; old != nil && old != w {
		old.Marker = len(old.Lines) // what you'd seen when you left
		a.sendRead(old)
	}
	a.active = w
	a.nickFocus = false
	if a.find != nil && a.find.win != w {
		a.find = nil
	}
	w.Act = state.ActNone
}

func (a *App) cycle(d int) {
	order := a.order()
	i := slices.Index(order, a.active)
	a.activate(order[(i+d+len(order))%len(order)])
}

// cycleKind activates the next window of kind k (Ctrl+N channels, Ctrl+Q
// queries, as in mIRC).
func (a *App) cycleKind(k state.Kind) {
	order := a.order()
	start := slices.Index(order, a.active)
	for i := 1; i <= len(order); i++ {
		if w := order[(start+i)%len(order)]; w.Kind == k {
			a.activate(w)
			return
		}
	}
}

// closeWindow is the user closing a window. Leaving a channel you're in
// goes through the server; the window closes when it confirms the PART.
func (a *App) closeWindow(w *state.Window) {
	switch {
	case w.Kind == state.KStatus:
		a.addLine(w, state.RoleInfo, "* Status windows stay open")
		return
	case !a.opts.Demo && w.Kind == state.KChannel && w.Joined:
		a.sendTo(w, "PART", w.Name)
		return
	case w.Kind == state.KTransfer: // closing a Get window cancels it, like mIRC
		a.cancelTransfer(a.transferFor(w))
	case w.Kind == state.KChat:
		a.chatClose(w)
	}
	a.removeWindow(w)
}

func (a *App) removeWindow(w *state.Window) {
	order := a.order()
	i := slices.Index(order, w)
	w.Net.Wins = slices.DeleteFunc(w.Net.Wins, func(x *state.Window) bool { return x == w })
	if a.active == w {
		a.activate(order[i-1]) // there's always a status window before it
	}
}

// openQuery focuses the private message window for nick, creating it if
// needed.
func (a *App) openQuery(n *state.Network, nick string) *state.Window {
	w := a.query(n, nick, "")
	a.activate(w)
	return w
}

// query finds or creates nick's query window without focusing it.
func (a *App) query(n *state.Network, nick, host string) *state.Window {
	if w := n.Find(state.KQuery, nick); w != nil {
		if host != "" {
			w.Host = host
		}
		return w
	}
	if host == "" && a.opts.Demo {
		host = "~" + strings.ToLower(nick) + "@user/" + strings.ToLower(nick)
	}
	w := n.Add(&state.Window{Kind: state.KQuery, Name: nick, Host: host})
	if !a.opts.Demo {
		a.fetchHistory(w)
	}
	return w
}

// addLine adds a line to a window now; addLineAt adds one with a given time,
// like a message's server-time.
func (a *App) addLine(w *state.Window, role state.Role, text string) {
	a.addLineAt(w, role, text, time.Now())
}

func (a *App) addLineAt(w *state.Window, role state.Role, text string, t time.Time) {
	a.addLineL(w, state.Line{Time: t, Role: role, Text: text})
}

// addLineL adds a whole line, a highlight rule's color and all.
func (a *App) addLineL(w *state.Window, l state.Line) {
	w.Lines = append(w.Lines, l)
	if n := len(w.Lines) - state.MaxLines; n > 0 {
		w.Lines = slices.Delete(w.Lines, 0, n)
		w.Marker = max(0, w.Marker-n)
	}
	if w == a.active {
		if w.Scroll > 0 && a.msgW > 0 {
			w.Scroll += len(a.lineRows(w, l, a.msgW, false)) // keep a scrolled-up view still
		}
		if !a.hiActive { // Display > Highlight on message colors even this one
			return
		}
	}
	if lvl := state.ActivityFor(l.Role); lvl > w.Act {
		w.Act = lvl
	}
}

// joinChannel is /join in demo mode: a made-up channel with a few people.
func (a *App) joinChannel(n *state.Network, name string) {
	if w := n.Find(state.KChannel, name); w != nil {
		a.activate(w)
		return
	}
	w := &state.Window{Kind: state.KChannel, Name: name, Modes: "nt", Joined: true,
		Nicks: []state.Nick{{Prefix: "@", Name: n.Nick}}}
	for range 3 + rand.IntN(8) {
		w.Nicks = append(w.Nicks, state.Nick{Name: demo.GuestNick()})
	}
	n.AddChannel(w)
	w.SortNicks()
	a.activate(w)
	a.addLine(w, state.RoleEvent, "* Now talking in "+name)
}

func clamp(v, lo, hi int) int {
	if hi < lo {
		hi = lo
	}
	return max(lo, min(v, hi))
}

// quit leaves trout, asking first if Options > Other says to, like mIRC's
// "Confirm exit". The bouncer stays on IRC either way.
func (a *App) quit() tea.Cmd {
	if !a.confirmExit || a.opts.Demo {
		return tea.Quit
	}
	d := message("Quit trout?", "", "The bouncer keeps you on your networks.")
	d.Title = "Quit"
	d.Buttons, d.Cancel = []string{"Yes", "No"}, 1
	d.OnClose = func(a *App, button int) tea.Cmd {
		if button == 0 {
			return tea.Quit
		}
		return nil
	}
	a.openDialog(d)
	return nil
}

// dragEditTo selects in the editbox being dragged in, up to the pointer.
func (a *App) dragEditTo(x, y int) {
	w := a.active
	r, ok := a.hit(x, y)
	if !ok || r.Kind != HitEdit || r.S != editBoxName[a.editDrag] {
		return
	}
	e := a.edit(w)
	pos := editPosAt(e, r.I, x-r.X, r.W)
	e.Extend(func() { e.Pos = pos })
}
