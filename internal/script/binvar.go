package script

import (
	"encoding/binary"
	"strings"
	"unicode/utf8"
)

// Binary variables (&name): bytes rather than text, for sockets, files
// and hashes. Like mIRC's, they last until the script run that made them
// is done: an alias sees its caller's, a timer started later doesn't
// (checked in mIRC 7.86, tmp/bin-oracle*.mrc, as is the rest unless it
// says otherwise).

// bin is binary variable name's bytes, and whether it exists.
func (r *run) bin(name string) ([]byte, bool) {
	b, ok := r.bins[strings.ToLower(name)]
	return b, ok
}

func (r *run) setBin(name string, b []byte) {
	if r.bins == nil {
		r.bins = map[string][]byte{}
	}
	r.bins[strings.ToLower(name)] = b
}

func isBinvar(s string) bool { return len(s) > 1 && s[0] == '&' }

// putBytes writes src into dst at 1-based position n, padding with zeros
// past the end; chop drops whatever follows.
func putBytes(dst []byte, n int, src []byte, chop bool) []byte {
	end := n - 1 + len(src)
	if end > len(dst) {
		dst = append(dst, make([]byte, end-len(dst))...)
	}
	copy(dst[n-1:], src)
	if chop {
		dst = dst[:end]
	}
	return dst
}

// byteValue is one of /bset's ascii values: a number (wrapping at 256: 300
// is 44), or else a word's first character.
func byteValue(w string) byte {
	if n, ok := parseNum(w); ok {
		return byte(int64(n))
	}
	r, _ := utf8.DecodeRuneInString(w)
	return byte(r)
}

// textBytes is text as UTF-8, or with ansi each character a byte (UTF-8
// if one doesn't fit), like /bset -a.
func textBytes(text string, ansi bool) []byte {
	if ansi {
		return latin1(text)
	}
	return []byte(text)
}

// cmdBset is /bset [-tacz] <&binvar> <N> <asciivalue> [asciivalue...] or
// with -t text. -z empties it, whatever follows; -c chops it after what
// was set.
func (r *run) cmdBset(fr *frame, rest string) error {
	line, err := r.eval(fr, rest)
	if err != nil {
		return err
	}
	sw, line := switches(line)
	words := strings.Fields(line)
	if len(words) < 1 || !isBinvar(words[0]) {
		return errorf("/bset: insufficient parameters")
	}
	if strings.Contains(sw, "z") {
		r.setBin(words[0], []byte{})
		return nil
	}
	if len(words) < 3 {
		return errorf("/bset: insufficient parameters")
	}
	n, ok := parseNum(words[1])
	if !ok || n < 1 {
		return errorf("/bset: invalid parameters")
	}
	var data []byte
	if strings.Contains(sw, "t") {
		data = textBytes(strings.Join(words[2:], " "), strings.Contains(sw, "a"))
	} else {
		for _, w := range words[2:] {
			data = append(data, byteValue(w))
		}
	}
	b, _ := r.bin(words[0])
	r.setBin(words[0], putBytes(b, int(n), data, strings.Contains(sw, "c")))
	return nil
}

// cmdBunset is /bunset <&binvar> [&binvar...].
func (r *run) cmdBunset(fr *frame, rest string) error {
	line, err := r.eval(fr, rest)
	if err != nil {
		return err
	}
	for w := range strings.FieldsSeq(line) {
		delete(r.bins, strings.ToLower(w))
	}
	return nil
}

// cmdBcopy is /bcopy [-zc] <&binvar> <N> <&binvar> <S> <M>: M bytes of the
// second from position S into the first at N (made if it isn't there).
// M -1 is to the end. -z zeroes the copied bytes in the second, -c chops
// the first after them.
func (r *run) cmdBcopy(fr *frame, rest string) error {
	line, err := r.eval(fr, rest)
	if err != nil {
		return err
	}
	sw, line := switches(line)
	w := strings.Fields(line)
	if len(w) < 5 {
		return errorf("/bcopy: insufficient parameters")
	}
	src, ok := r.bin(w[2])
	n, ok1 := parseNum(w[1])
	s, ok2 := parseNum(w[3])
	m, ok3 := parseNum(w[4])
	if !ok || !ok1 || !ok2 || !ok3 || n < 1 || s < 1 || int(s) > len(src) {
		return errorf("/bcopy: invalid parameters")
	}
	from := int(s) - 1
	to := len(src)
	if m >= 0 {
		to = min(to, from+int(m))
	}
	part := append([]byte(nil), src[from:to]...)
	if strings.Contains(sw, "z") {
		clear(src[from:to])
	}
	dst, _ := r.bin(w[0])
	r.setBin(w[0], putBytes(dst, int(n), part, strings.Contains(sw, "c")))
	return nil
}

// cmdBreplace is /breplace <&binvar> <oldvalue> <newvalue> [old new...]:
// byte values.
func (r *run) cmdBreplace(fr *frame, rest string) error {
	line, err := r.eval(fr, rest)
	if err != nil {
		return err
	}
	w := strings.Fields(line)
	b, ok := r.bin(cmpFirst(w))
	if len(w) < 3 || !ok {
		return errorf("/breplace: invalid parameters")
	}
	pairs := map[byte]byte{}
	for i := 1; i+1 < len(w); i += 2 {
		pairs[byteValue(w[i])] = byteValue(w[i+1])
	}
	for i, c := range b {
		if v, ok := pairs[c]; ok {
			b[i] = v
		}
	}
	return nil
}

func cmpFirst(w []string) string {
	if len(w) == 0 {
		return ""
	}
	return w[0]
}

// cmdBread is /bread <filename> <S> <N> <&binvar>: N bytes of the file
// from byte S (the first is 0) into the binary variable, replacing it.
// Not checked in mIRC: its test copy hangs loading any script with
// /bwrite, so both follow the help file.
func (r *run) cmdBread(fr *frame, rest string) error {
	line, err := r.eval(fr, rest)
	if err != nil {
		return err
	}
	sw, line := switches(line)
	if sw != "" {
		return errorf("/bread -", sw, " isn't supported yet")
	}
	w := strings.Fields(line)
	if len(w) < 4 || !isBinvar(w[len(w)-1]) {
		return errorf("/bread: insufficient parameters")
	}
	name := strings.Join(w[:len(w)-3], " ")
	s, ok1 := parseNum(w[len(w)-3])
	n, ok2 := parseNum(w[len(w)-2])
	if !ok1 || !ok2 || s < 0 || n < 0 {
		return errorf("/bread: invalid parameters")
	}
	data, ok := r.readFile(name)
	if !ok {
		return errorf("/bread: unable to open '", name, "'")
	}
	from := min(int(s), len(data))
	to := min(len(data), from+int(n))
	r.setBin(w[len(w)-1], []byte(data[from:to]))
	return nil
}

// cmdBwrite is /bwrite [-tac] <filename> <S> [N] <text|%var|&binvar>: N
// bytes (-1 or none: all) written into the file from byte S (-1: its
// end). -t takes a &binvar's name as text, -a writes text without UTF-8,
// -c cuts the file after what was written.
func (r *run) cmdBwrite(fr *frame, rest string) error {
	line, err := r.eval(fr, rest) // a &binvar's name stays as it is
	if err != nil {
		return err
	}
	sw, line := switches(line)
	w := strings.SplitN(line, " ", 4)
	if len(w) < 3 {
		return errorf("/bwrite: insufficient parameters")
	}
	name, data := w[0], strings.Join(w[2:], " ")
	s, ok := parseNum(w[1])
	if !ok {
		return errorf("/bwrite: invalid parameters")
	}
	count := -1
	if len(w) == 4 {
		if n, ok := parseNum(w[2]); ok {
			count, data = int(n), w[3]
		}
	}
	var b []byte
	if isBinvar(data) && !strings.Contains(data, " ") && !strings.Contains(sw, "t") {
		v, ok := r.bin(data)
		if !ok {
			return errorf("/bwrite: invalid parameters")
		}
		b = v
	} else {
		b = textBytes(data, strings.Contains(sw, "a"))
	}
	if count >= 0 && count < len(b) {
		b = b[:count]
	}
	old, _ := r.readFile(name)
	file := []byte(old)
	at := int(s)
	if at < 0 || at > len(file) {
		at = len(file)
	}
	file = putBytes(file, at+1, b, strings.Contains(sw, "c"))
	r.writeFile(name, string(file), false)
	return nil
}

// cmdBtrunc is /btrunc <filename> <bytes>: the file cut to that size, or
// padded with zero bytes to it (the help file's).
func (r *run) cmdBtrunc(fr *frame, rest string) error {
	line, err := r.eval(fr, rest)
	if err != nil {
		return err
	}
	i := strings.LastIndexByte(strings.TrimSpace(line), ' ')
	if i < 0 {
		return errorf("/btrunc: insufficient parameters")
	}
	line = strings.TrimSpace(line)
	name, size := strings.TrimSpace(line[:i]), line[i+1:]
	n, ok := parseNum(size)
	if !ok || n < 0 {
		return errorf("/btrunc: invalid parameters")
	}
	data, _ := r.readFile(name)
	b := []byte(data)
	if int(n) < len(b) {
		b = b[:int(n)]
	} else {
		b = append(b, make([]byte, int(n)-len(b))...)
	}
	r.writeFile(name, string(b), false)
	return nil
}

// bvarIdent is $bvar(&binvar,N,M): M byte values from position N (N- is
// to the end), its length for N = 0. .text is them as text (up to a zero
// byte), .word, .nword, .long and .nlong a number from N (n: network
// order).
func bvarIdent(r *run, fr *frame, c *call) (string, error) {
	if len(c.vals) > 3 {
		return "", errorf("Invalid parameters: $bvar")
	}
	if len(c.vals) < 2 {
		return c.arg(0), nil // mIRC gives back the name
	}
	b, _ := r.bin(c.arg(0))
	pos := c.arg(1)
	toEnd := strings.HasSuffix(pos, "-")
	n, _ := parseNum(strings.TrimSuffix(pos, "-"))
	if n == 0 && !toEnd {
		return itoa(len(b)), nil
	}
	from := int(n) - 1
	if from < 0 || from >= len(b) {
		return "", nil
	}
	switch c.prop {
	case "word", "nword", "long", "nlong":
		size := 2
		if strings.HasSuffix(c.prop, "long") {
			size = 4
		}
		w := make([]byte, size)
		copy(w, b[from:])
		var v uint64
		switch c.prop {
		case "word":
			v = uint64(binary.LittleEndian.Uint16(w))
		case "nword":
			v = uint64(binary.BigEndian.Uint16(w))
		case "long":
			v = uint64(binary.LittleEndian.Uint32(w))
		default:
			v = uint64(binary.BigEndian.Uint32(w))
		}
		return itoa(int(v)), nil
	}
	m := 1
	switch {
	case toEnd:
		m = len(b)
	case len(c.vals) > 2:
		m = int(c.num(2))
	}
	to := min(len(b), from+max(m, 0))
	part := b[from:to]
	if c.prop == "text" {
		if i := strings.IndexByte(string(part), 0); i >= 0 {
			part = part[:i]
		}
		return decodeText(part), nil
	}
	var s strings.Builder
	for i, v := range part {
		if i > 0 {
			s.WriteByte(' ')
		}
		s.WriteString(itoa(int(v)))
	}
	return s.String(), nil
}

// bfindIdent is $bfind(&binvar,N,M): the position of M from position N,
// or 0. M is byte values ("104 101"), or text (with .text, or when it
// isn't numbers), found ignoring case.
func bfindIdent(r *run, fr *frame, c *call) (string, error) {
	b, _ := r.bin(c.arg(0))
	n := max(int(c.num(1)), 1)
	what := c.arg(2)
	var needle []byte
	text := c.prop == "text"
	if !text {
		for w := range strings.FieldsSeq(what) {
			v, ok := parseNum(w)
			if !ok {
				text = true
				break
			}
			needle = append(needle, byte(int64(v)))
		}
	}
	if text {
		needle = []byte(what)
	}
	if len(needle) == 0 || n > len(b) {
		return "0", nil
	}
	for i := n - 1; i+len(needle) <= len(b); i++ {
		if text && asciiEqualFold(b[i:i+len(needle)], needle) || !text && string(b[i:i+len(needle)]) == string(needle) {
			return itoa(i + 1), nil
		}
	}
	return "0", nil
}

// asciiEqualFold compares bytes ignoring the case of ASCII letters.
func asciiEqualFold(a, b []byte) bool {
	for i := range a {
		x, y := a[i], b[i]
		if 'A' <= x && x <= 'Z' {
			x += 'a' - 'A'
		}
		if 'A' <= y && y <= 'Z' {
			y += 'a' - 'A'
		}
		if x != y {
			return false
		}
	}
	return true
}

var binIdents = map[string]builtin{
	"bvar":  bvarIdent,
	"bfind": bfindIdent,
}
