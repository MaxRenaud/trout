package ui

import (
	"errors"
	"runtime"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"
)

// Windows' clipboard through its API, as UTF-16 both ways: clip.exe and
// PowerShell's Get-Clipboard go through the console's code page, which
// loses anything outside it (emoji, most scripts).

var (
	user32           = syscall.NewLazyDLL("user32.dll")
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	openClipboard    = user32.NewProc("OpenClipboard")
	closeClipboard   = user32.NewProc("CloseClipboard")
	emptyClipboard   = user32.NewProc("EmptyClipboard")
	getClipboardData = user32.NewProc("GetClipboardData")
	setClipboardData = user32.NewProc("SetClipboardData")
	globalAlloc      = kernel32.NewProc("GlobalAlloc")
	globalFree       = kernel32.NewProc("GlobalFree")
	globalLock       = kernel32.NewProc("GlobalLock")
	globalUnlock     = kernel32.NewProc("GlobalUnlock")
	lstrlenW         = kernel32.NewProc("lstrlenW")
	moveMemory       = kernel32.NewProc("RtlMoveMemory")
)

const (
	cfUnicodeText = 13
	gmemMoveable  = 0x0002
)

// openClip opens the clipboard, waiting a little if another program has it.
func openClip() error {
	for range 50 {
		if r, _, _ := openClipboard.Call(0); r != 0 {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return errors.New("the clipboard is busy")
}

func systemCopy(text string) error {
	// The clipboard is opened and closed by a thread: the goroutine mustn't
	// move to another one in between, or it stays open.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	u, err := syscall.UTF16FromString(text) // with its NUL
	if err != nil {
		return err
	}
	if err := openClip(); err != nil {
		return err
	}
	defer closeClipboard.Call()
	emptyClipboard.Call()
	size := uintptr(len(u) * 2)
	h, _, _ := globalAlloc.Call(gmemMoveable, size)
	if h == 0 {
		return errors.New("no memory for the clipboard")
	}
	p, _, _ := globalLock.Call(h)
	if p == 0 {
		globalFree.Call(h)
		return errors.New("can't lock the clipboard's memory")
	}
	moveMemory.Call(p, uintptr(unsafe.Pointer(&u[0])), size)
	globalUnlock.Call(h)
	if r, _, _ := setClipboardData.Call(cfUnicodeText, h); r == 0 {
		globalFree.Call(h)
		return errors.New("the clipboard didn't take the text")
	}
	return nil // the clipboard owns h now
}

func systemPaste() (string, error) {
	runtime.LockOSThread() // see systemCopy
	defer runtime.UnlockOSThread()
	if err := openClip(); err != nil {
		return "", err
	}
	defer closeClipboard.Call()
	h, _, _ := getClipboardData.Call(cfUnicodeText)
	if h == 0 {
		return "", nil // no text on it
	}
	p, _, _ := globalLock.Call(h)
	if p == 0 {
		return "", errors.New("can't lock the clipboard's memory")
	}
	defer globalUnlock.Call(h)
	n, _, _ := lstrlenW.Call(p)
	if n == 0 {
		return "", nil
	}
	u := make([]uint16, n)
	moveMemory.Call(uintptr(unsafe.Pointer(&u[0])), p, n*2)
	return string(utf16.Decode(u)), nil
}
