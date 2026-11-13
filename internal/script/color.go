package script

import "strings"

// ColorSetter is the client changing its colors, for /color. A client
// without it ignores /color.
type ColorSetter interface {
	// SetColor makes color n (0-98) look like rgb (0xRRGGBB), or with
	// reset, as it was.
	SetColor(n int, rgb uint32, reset bool)
	// SetKindColor gives a kind of text (Colors.Index's: background,
	// normal, own, action...) palette color idx, or with reset, its own.
	SetKindColor(kind string, idx int, reset bool)
}

// cmdColor is /color [-r] <name|N> [index|rgb]: a kind of text's color
// number ("action text", or cut short like $color's), or what color N
// looks like as an $rgb number; -r puts it back. Like mIRC's (checked:
// /color 4 $rgb(1,2,3), /color -r 4, and colors 16-98 change too).
// -l and -s, mirc.ini's colors, are mIRC's own and do nothing.
func (r *run) cmdColor(line string) error {
	words := strings.Fields(line)
	reset := false
	for len(words) > 0 && strings.HasPrefix(words[0], "-") && len(words[0]) > 1 {
		reset = reset || strings.Contains(words[0], "r")
		words = words[1:]
	}
	if len(words) == 0 || !reset && len(words) < 2 {
		return errorf("/color: insufficient parameters")
	}
	name, value := strings.Join(words, " "), int64(0)
	if !reset {
		v, ok := parseNum(words[len(words)-1])
		if !ok || !isDigits(words[len(words)-1]) {
			return errorf("/color: invalid parameters")
		}
		name, value = strings.Join(words[:len(words)-1], " "), int64(v)
	}
	cs, _ := r.h.(ColorSetter)
	if isDigits(name) {
		n := atoi(name)
		if n > 98 {
			return errorf("/color: invalid parameters")
		}
		if cs != nil {
			cs.SetColor(n, colorref(uint32(value)&0xffffff), reset)
		}
		return nil
	}
	name = strings.ToLower(name)
	for _, exact := range []bool{true, false} {
		for _, it := range colorNames {
			if exact && it.name == name || !exact && strings.HasPrefix(it.name, name) {
				if it.kind != "" && cs != nil && value < 16 {
					cs.SetKindColor(it.kind, int(value), reset)
				}
				return nil
			}
		}
	}
	return errorf("/color: invalid parameters")
}
