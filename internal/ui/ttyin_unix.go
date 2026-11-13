//go:build unix

package ui

import (
	"bytes"
	"os"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/term"
	"golang.org/x/sys/unix"
)

// escWait is how long a read that ends inside an escape sequence waits for
// the rest of it, as long as vim's usual ttimeoutlen. A lone Esc key waits
// this long too.
const escWait = 100 * time.Millisecond

// ttyIn is stdin, but a read never ends partway through an escape sequence.
// Bubble Tea v1 takes a read's end for the end of the input unless its
// buffer filled up, and even then it only keeps back text, not the start
// of a sequence; either way a mouse report cut in two comes out as Alt+[
// and typed text, "<35;111;26M". Over ssh a report can come in pieces, and
// on a slow computer (a Raspberry Pi 1) the mouse's motion reports pile
// up past the buffer's 256 bytes.
//
// It also watches for the break key (scriptbreak.go), in what it reads
// and, through poll, while a script runs.
type ttyIn struct {
	*os.File
	brk   *breakKey
	mu    sync.Mutex // held while reading the terminal: Read and poll never both do
	held  []byte     // read but not yet given to Bubble Tea: the start of a sequence a full read ended in, or what poll read
	since time.Time  // when poll first found input Read hadn't taken
}

func (t *ttyIn) Read(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.since = time.Time{}
	n := copy(p, t.held)
	t.held = t.held[n:]
	fresh := n
	var err error
	if n == 0 {
		n, err = t.File.Read(p)
	}
	for err == nil && n < len(p) && partialEsc(p[:n]) && readable(t.Fd(), escWait) {
		var m int
		m, err = t.File.Read(p[n:])
		n += m
	}
	if t.brk != nil {
		t.brk.saw(p[fresh:n])
	}
	// An Esc key with a mouse report right behind it (moving the mouse
	// while the lone Esc waited, or both in one read over a slow ssh)
	// would be ESC ESC [<..., which Bubble Tea reads as Alt+[ and typed
	// "<35;103;31M", losing the Esc (Jinx's Channel Central). No
	// terminal sends a mouse report with Alt's ESC before it, so that
	// ESC is the Esc key: it goes on its own.
	for _, seq := range [][]byte{[]byte("\x1b\x1b[<"), []byte("\x1b\x1b[M")} {
		if i := bytes.Index(p[:n], seq); i >= 0 {
			cut := max(i, 1)
			t.held = append(append([]byte(nil), p[cut:n]...), t.held...)
			n = cut
		}
	}
	if n == len(p) && partialEsc(p[:n]) {
		if i := bytes.LastIndexByte(p[:n], 0x1b); i > 0 {
			t.held = append([]byte(nil), p[i:n]...)
			n = i
		}
	}
	return n, err
}

// stuckWait is how long input waits for Bubble Tea's reader before poll
// takes it: the reader takes input at once unless it's stuck, waiting for
// Update to take what it read before.
const stuckWait = 50 * time.Millisecond

// poll is breakKey.poll, called every so often while a script runs on the
// Update goroutine. Bubble Tea's reader hands each key to Update before
// it reads the next, so with a key or mouse report already waiting for
// Update, Ctrl+\ would never be read: poll reads it then, keeping what it
// read for the next Read, in order. It reads only input the reader has
// left there for a while, and never while Read is reading.
//
// What it read reaches Bubble Tea once the terminal has more input (its
// reader waits for the terminal to have some before it calls Read): the
// next key or mouse movement. That's only ever what was typed while a
// script had trout frozen.
func (t *ttyIn) poll() {
	if !t.mu.TryLock() {
		return
	}
	defer t.mu.Unlock()
	switch {
	case !readable(t.Fd(), 0):
		t.since = time.Time{}
	case t.since.IsZero():
		t.since = time.Now()
	case time.Since(t.since) >= stuckWait:
		var buf [256]byte
		if n, err := t.File.Read(buf[:]); err == nil { // it's readable: this doesn't wait
			t.held = append(t.held, buf[:n]...)
			t.brk.saw(buf[:n])
		}
	}
}

// partialEsc says if b ends in an escape sequence that isn't finished: an
// ESC, ESC [ or ESC O, or a CSI without its final byte yet.
func partialEsc(b []byte) bool {
	i := len(b) - 1
	for i >= 0 && b[i] != 0x1b {
		i--
	}
	if i < 0 {
		return false
	}
	switch tail := b[i+1:]; {
	case len(tail) == 0:
		return true
	case tail[0] == 'O':
		return len(tail) == 1
	case tail[0] != '[':
		return false
	case len(tail) >= 2 && tail[1] == 'M':
		// An X10 mouse report, which GNU screen 4 sends (it has no SGR
		// mouse): ESC [ M and three bytes, button, x and y, each plus 32,
		// so never an ESC. Its M isn't a final byte.
		return len(tail) < 5
	default:
		for _, c := range tail[1:] {
			if c < 0x20 || c > 0x3f { // a final byte (or not a CSI after all)
				return false
			}
		}
		return true
	}
}

// readable waits for fd to have input. select, not poll: macOS's poll
// doesn't work on terminals (cancelreader uses select for /dev/tty too).
func readable(fd uintptr, wait time.Duration) bool {
	for {
		var set unix.FdSet
		set.Set(int(fd))
		tv := unix.NsecToTimeval(wait.Nanoseconds())
		n, err := unix.Select(int(fd)+1, &set, nil, nil, &tv)
		if err != unix.EINTR {
			return err == nil && n > 0
		}
	}
}

// inputOption reads stdin through ttyIn when it's a terminal, watching
// for brk's key; otherwise Bubble Tea opens /dev/tty itself, as before.
func inputOption(brk *breakKey) tea.ProgramOption {
	if !term.IsTerminal(os.Stdin.Fd()) {
		return func(*tea.Program) {}
	}
	t := &ttyIn{File: os.Stdin, brk: brk}
	brk.poll = t.poll
	return tea.WithInput(t)
}

// watchBreak is nothing here: ttyIn watches for the break key.
func watchBreak(*breakKey, func()) (stop func()) { return func() {} }
