package ui

import (
	"bytes"
	"sync/atomic"
)

// breakKey is mIRC's Ctrl+Break, which halts a script that never ends.
// Scripts run on the Update goroutine, so Bubble Tea can't hand the key
// to Update while one does: the terminal's reader (ttyin_*) sets pressed
// as it reads the key, and poll looks at the terminal itself when Bubble
// Tea's reader is stuck, waiting to give Update what it read before.
// Ctrl+Break reaches trout on Windows (a console signal); most terminals
// elsewhere send nothing for it, so there it's Ctrl+\, which every
// terminal sends and trout has no other use for.
type breakKey struct {
	pressed atomic.Bool
	poll    func() // nil where the reader needs no help
}

// breakByte is Ctrl+\.
const breakByte = 0x1c

// saw notes the break key if b, read from the terminal, has it.
func (k *breakKey) saw(b []byte) {
	if bytes.IndexByte(b, breakByte) >= 0 {
		k.pressed.Store(true)
	}
}

// interrupted is the engine's Options.Interrupted: has the break key been
// pressed since this Update began?
func (k *breakKey) interrupted() bool {
	if k == nil {
		return false
	}
	if k.poll != nil {
		k.poll()
	}
	return k.pressed.Load()
}

// reset forgets a break pressed before: each Update starts without one,
// so a Ctrl+\ with no script running doesn't halt the next script.
func (k *breakKey) reset() {
	if k != nil {
		k.pressed.Store(false)
	}
}
