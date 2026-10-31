//go:build unix

package ui

import (
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

// ttyIn is stdin, but a read never ends partway through an escape sequence
// while the rest is on its way. Bubble Tea v1 only waits for the rest when
// its buffer filled up; otherwise, a mouse report split across reads (over
// ssh, say) comes out as Alt+[ and typed text, "<35;111;26M".
type ttyIn struct{ *os.File }

func (t ttyIn) Read(p []byte) (int, error) {
	n, err := t.File.Read(p)
	for err == nil && n < len(p) && partialEsc(p[:n]) && readable(t.Fd(), escWait) {
		var m int
		m, err = t.File.Read(p[n:])
		n += m
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
	return tea.WithInput(ttyIn{os.Stdin})
}
