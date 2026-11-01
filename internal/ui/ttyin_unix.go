//go:build unix

package ui

import (
	"bytes"
	"os"
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
type ttyIn struct {
	*os.File
	held []byte // the start of a sequence a full read ended in, for the next read
}

func (t *ttyIn) Read(p []byte) (int, error) {
	n := copy(p, t.held)
	t.held = t.held[n:]
	var err error
	if n == 0 {
		n, err = t.File.Read(p)
	}
	for err == nil && n < len(p) && partialEsc(p[:n]) && readable(t.Fd(), escWait) {
		var m int
		m, err = t.File.Read(p[n:])
		n += m
	}
	if n == len(p) && partialEsc(p[:n]) {
		if i := bytes.LastIndexByte(p[:n], 0x1b); i > 0 {
			t.held = append([]byte(nil), p[i:n]...)
			n = i
		}
	}
	return n, err
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

// inputOption reads stdin through ttyIn when it's a terminal; otherwise
// Bubble Tea opens /dev/tty itself, as before.
func inputOption() tea.ProgramOption {
	if !term.IsTerminal(os.Stdin.Fd()) {
		return func(*tea.Program) {}
	}
	return tea.WithInput(&ttyIn{File: os.Stdin})
}
