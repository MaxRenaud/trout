package script

import (
	"cmp"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"hash"
	"hash/crc32"
	"maps"
	"math"
	"math/big"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

// More of mIRC's identifiers that don't need the client: text, numbers,
// hashes and encodings, from its Text and Number, File and Other
// Identifiers sections.
var miscIdents = map[string]builtin{
	// $remote is which events are listened to, as bits: 1 ctcp events, 2
	// named events, 4 raw events (/ctcps, /events, /raw).
	"remote": func(r *run, _ *frame, _ *call) (string, error) { return itoa(7 &^ r.e.deaf), nil },
	// $var(%name, N) is the Nth variable matching name (a wildcard), local
	// ones first; N = 0 is how many. .value is its value, .local whether
	// it's local, .secs the seconds until -uN unsets it.
	"var": func(r *run, fr *frame, c *call) (string, error) {
		// The name is read as written, not evaluated: $var(%x,1) is %x.
		if len(c.args) == 0 {
			return "", nil
		}
		pattern := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(c.args[0]), "%"))
		rest := &call{args: c.args[1:], hasArgs: true}
		if err := r.evalArgs(fr, rest); err != nil {
			return "", err
		}
		c.vals = append([]string{c.args[0]}, rest.vals...)
		type match struct {
			name, value string
			local       bool
			v           *variable
		}
		var ms []match
		for _, k := range slices.Sorted(maps.Keys(fr.locals)) {
			if wildMatch(pattern, k, false) {
				ms = append(ms, match{name: k, value: fr.locals[k], local: true})
			}
		}
		for _, k := range slices.Sorted(maps.Keys(r.e.vars)) {
			if _, shadowed := fr.locals[k]; !shadowed && wildMatch(pattern, k, false) {
				v := r.e.vars[k]
				ms = append(ms, match{name: v.name, value: v.value, v: v})
			}
		}
		n := 1
		if len(c.vals) > 1 {
			n = int(c.num(1))
		}
		if n == 0 {
			return itoa(len(ms)), nil
		}
		if n < 1 || n > len(ms) {
			return "", nil
		}
		m := ms[n-1]
		switch c.prop {
		case "value":
			return m.value, nil
		case "local":
			return boolStr(m.local), nil
		case "secs":
			if m.v == nil || m.v.expires.IsZero() {
				return "0", nil
			}
			return itoa(int(math.Ceil(m.v.expires.Sub(r.h.Now()).Seconds()))), nil
		}
		return "%" + m.name, nil
	},
	// $qt adds outer quotes and $noqt takes them off.
	"qt": func(r *run, fr *frame, c *call) (string, error) {
		s := c.arg(0)
		if !strings.HasPrefix(s, `"`) {
			s = `"` + s
		}
		if !strings.HasSuffix(s, `"`) || len(s) == 1 {
			s += `"`
		}
		return s, nil
	},
	"noqt": func(r *run, fr *frame, c *call) (string, error) {
		s := c.arg(0)
		s = strings.TrimPrefix(s, `"`)
		return strings.TrimSuffix(s, `"`), nil
	},
	// $ord(N) is N with st, nd, rd or th.
	"ord": func(r *run, fr *frame, c *call) (string, error) {
		n := int(c.num(0))
		return itoa(n) + ordinal(abs(n)), nil
	},
	"replacex":   replacexIdent(false),
	"replacexcs": replacexIdent(true),
	// $show is $false in an alias called quietly, with a . in front.
	"show": func(r *run, fr *frame, c *call) (string, error) {
		return boolStr(!fr.quiet), nil
	},
	"nopath": func(r *run, fr *frame, c *call) (string, error) {
		s := c.arg(0)
		return s[strings.LastIndexAny(s, `\/`)+1:], nil
	},
	"nofile": func(r *run, fr *frame, c *call) (string, error) {
		s := c.arg(0)
		return s[:strings.LastIndexAny(s, `\/`)+1], nil
	},
	// The folders scripts might build paths from are all the scripts
	// folder, which file names are already relative to: "" works as a
	// prefix, as in $scriptdir $+ seen.hsh.
	"mircdir":   str(""),
	"scriptdir": str(""),
	"logdir":    str(""),
	"getdir":    str(""),
	"pi":        str("3.14159265358979323846"),
	"bytes":     bytesIdent,
	"base":      baseIdent,
	"encode":    func(r *run, fr *frame, c *call) (string, error) { return codec(c, true) },
	"decode":    func(r *run, fr *frame, c *call) (string, error) { return codec(c, false) },
	"md5":       hashIdent(md5.New),
	"sha1":      hashIdent(sha1.New),
	"sha256":    hashIdent(sha256.New),
	"sha384":    hashIdent(sha512.New384),
	"sha512":    hashIdent(sha512.New),
	// $crc(text, N) is a CRC-32: of text with N = 0, of a file (the
	// default, N = 2).
	"crc": func(r *run, fr *frame, c *call) (string, error) {
		data, ok := hashInput(r, c, 2)
		if !ok {
			return "", nil
		}
		return fmt.Sprintf("%08X", crc32.ChecksumIEEE([]byte(data))), nil
	},
	"utfencode": func(r *run, fr *frame, c *call) (string, error) { return c.arg(0), nil },
	"utfdecode": func(r *run, fr *frame, c *call) (string, error) { return c.arg(0), nil },
	// Bits: $and, $or, $xor, $not, $biton, $bitoff and $isbit, on 32-bit
	// numbers like mIRC's.
	"and":    bits(func(a, b uint32) uint32 { return a & b }),
	"or":     bits(func(a, b uint32) uint32 { return a | b }),
	"xor":    bits(func(a, b uint32) uint32 { return a ^ b }),
	"biton":  bits(func(a, n uint32) uint32 { return a | 1<<(n-1) }),
	"bitoff": bits(func(a, n uint32) uint32 { return a &^ (1 << (n - 1)) }),
	"not": func(r *run, fr *frame, c *call) (string, error) {
		return itoa(int(^uint32(int64(c.num(0))))), nil
	},
	// $isbit(A,N) is 1 or 0 (checked in mIRC).
	"isbit": func(r *run, fr *frame, c *call) (string, error) {
		a, n := uint32(int64(c.num(0))), uint32(c.num(1))
		return iff(n >= 1 && n <= 32 && a&(1<<(n-1)) != 0, "1", "0"), nil
	},
	// Math: trigonometry takes radians, or degrees with .deg.
	"sin":   trig(math.Sin, false),
	"cos":   trig(math.Cos, false),
	"tan":   trig(math.Tan, false),
	"asin":  trig(math.Asin, true),
	"acos":  trig(math.Acos, true),
	"atan":  trig(math.Atan, true),
	"sinh":  trig(math.Sinh, false),
	"cosh":  trig(math.Cosh, false),
	"tanh":  trig(math.Tanh, false),
	"log":   num1(math.Log),
	"log2":  num1(math.Log2),
	"log10": num1(math.Log10),
	"cbrt":  num1(math.Cbrt),
	"hypot": func(r *run, fr *frame, c *call) (string, error) {
		return formatNum(math.Hypot(c.num(0), c.num(1))), nil
	},
	"gcd": func(r *run, fr *frame, c *call) (string, error) {
		g := 0
		for i := range c.vals {
			g = gcd(g, abs(int(c.num(i))))
		}
		return itoa(g), nil
	},
	"lcm": func(r *run, fr *frame, c *call) (string, error) {
		l := 1
		for i := range c.vals {
			n := abs(int(c.num(i)))
			if n == 0 {
				return "0", nil
			}
			l = l / gcd(l, n) * n
		}
		return itoa(l), nil
	},
}

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

func num1(f func(float64) float64) builtin {
	return func(r *run, fr *frame, c *call) (string, error) {
		return formatNum(f(c.num(0))), nil
	}
}

// trig is a trigonometry identifier: .deg means degrees, for the angle it
// takes or (inverse) returns.
func trig(f func(float64) float64, inverse bool) builtin {
	return func(r *run, fr *frame, c *call) (string, error) {
		x := c.num(0)
		if c.prop == "deg" && !inverse {
			x *= math.Pi / 180
		}
		v := f(x)
		if c.prop == "deg" && inverse {
			v *= 180 / math.Pi
		}
		return formatNum(v), nil
	}
}

func bits(f func(a, b uint32) uint32) builtin {
	return func(r *run, fr *frame, c *call) (string, error) {
		return itoa(int(f(uint32(int64(c.num(0))), uint32(int64(c.num(1)))))), nil
	}
}

// replacexIdent is $replacex(text,a,A,b,B...): each place in text is
// replaced at most once, so a replacement isn't replaced again.
func replacexIdent(cs bool) builtin {
	return func(r *run, fr *frame, c *call) (string, error) {
		s := c.arg(0)
		var b strings.Builder
	next:
		for i := 0; i < len(s); {
			for k := 1; k+1 < len(c.vals); k += 2 {
				sub := c.vals[k]
				if sub == "" || i+len(sub) > len(s) {
					continue
				}
				if cs && s[i:i+len(sub)] == sub || !cs && strings.EqualFold(s[i:i+len(sub)], sub) {
					b.WriteString(c.vals[k+1])
					i += len(sub)
					continue next
				}
			}
			b.WriteByte(s[i])
			i++
		}
		return b.String(), nil
	}
}

// bytesIdent is $bytes(N,options), as mIRC does it (checked): N in the
// largest unit it's at least 1 of, or with b, k, m, g or t in that unit,
// with thousands commas and up to 5 decimals (6 with d). 3 is three
// significant digits instead. .suf adds the unit.
func bytesIdent(r *run, fr *frame, c *call) (string, error) {
	n, opts := c.num(0), c.arg(1)
	units := []string{"B", "KB", "MB", "GB", "TB"}
	unit := -1
	for i, u := range "bkmgt" {
		if strings.ContainsRune(opts, u) {
			unit = i
		}
	}
	if unit < 0 {
		unit = 0
		for unit < len(units)-1 && math.Abs(n)/math.Pow(1024, float64(unit+1)) >= 1 {
			unit++
		}
	}
	v := n / math.Pow(1024, float64(unit))
	var s string
	if strings.Contains(opts, "3") {
		digits := 3 - len(strconv.Itoa(int(math.Abs(v))))
		s = strconv.FormatFloat(math.Trunc(v*math.Pow(10, float64(max(digits, 0))))/math.Pow(10, float64(max(digits, 0))), 'f', -1, 64)
	} else {
		prec := 5
		if strings.Contains(opts, "d") {
			prec = 6
		}
		s = strconv.FormatFloat(v, 'f', prec, 64)
		if strings.Contains(s, ".") {
			s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
		}
		s = commas(s)
	}
	if c.prop == "suf" {
		s += units[unit]
	}
	return s, nil
}

// commas puts thousands separators in a number's whole part.
func commas(s string) string {
	whole, frac, _ := strings.Cut(s, ".")
	neg := strings.HasPrefix(whole, "-")
	whole = strings.TrimPrefix(whole, "-")
	var b strings.Builder
	for i, ch := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(ch)
	}
	out := b.String()
	if neg {
		out = "-" + out
	}
	if frac != "" {
		out += "." + frac
	}
	return out
}

// baseIdent is $base(N, inbase, outbase, zeropad, precision).
func baseIdent(r *run, fr *frame, c *call) (string, error) {
	in, out := int(c.num(1)), int(c.num(2))
	if in < 2 || in > 36 || out < 2 || out > 36 {
		return "", errorf("$base: invalid base")
	}
	s := strings.ToLower(strings.TrimSpace(c.arg(0)))
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	whole, frac, _ := strings.Cut(s, ".")
	w, ok := new(big.Int).SetString(cmp.Or(whole, "0"), in)
	if !ok {
		return "0", nil
	}
	f := 0.0
	for i, d := range frac {
		v := strings.IndexRune("0123456789abcdefghijklmnopqrstuvwxyz", d)
		if v < 0 || v >= in {
			break
		}
		f += float64(v) / math.Pow(float64(in), float64(i+1))
	}
	res := strings.ToUpper(w.Text(out))
	if pad := int(c.num(3)); len(res) < pad {
		res = strings.Repeat("0", pad-len(res)) + res
	}
	if f > 0 {
		prec := 6
		if len(c.vals) > 4 {
			prec = int(c.num(4))
		}
		var digits strings.Builder
		for range prec {
			f *= float64(out)
			d := int(f)
			digits.WriteByte("0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ"[d])
			f -= float64(d)
			if f <= 1e-12 {
				break
			}
		}
		if ds := strings.TrimRight(digits.String(), "0"); ds != "" {
			res += "." + ds
		}
	}
	if neg {
		res = "-" + res
	}
	return res, nil
}

// codec is $encode and $decode with m (base64), a (base32), x (percent
// encoding) or u (uuencode, the default).
func codec(c *call, encode bool) (string, error) {
	opts := c.arg(1)
	s := c.arg(0)
	switch {
	case strings.ContainsAny(opts, "bcelv"):
		return "", errorf("$encode/$decode with ", opts, " isn't supported")
	case strings.Contains(opts, "m"):
		if encode {
			return base64.StdEncoding.EncodeToString([]byte(s)), nil
		}
		b, _ := base64.StdEncoding.DecodeString(s)
		return string(b), nil
	case strings.Contains(opts, "a"):
		if encode {
			return base32.StdEncoding.EncodeToString([]byte(s)), nil
		}
		b, _ := base32.StdEncoding.DecodeString(s)
		return string(b), nil
	case strings.Contains(opts, "x"):
		if encode {
			return strings.ReplaceAll(url.QueryEscape(s), "+", "%20"), nil
		}
		v, _ := url.PathUnescape(s)
		return v, nil
	}
	if encode {
		return uuencode([]byte(s)), nil
	}
	return string(uudecode(s)), nil
}

// uuencode is one uuencoded line per 45 bytes, joined.
func uuencode(b []byte) string {
	enc := func(v byte) byte {
		if v == 0 {
			return '`'
		}
		return v + 32
	}
	var out strings.Builder
	for len(b) > 0 {
		n := min(len(b), 45)
		chunk := b[:n]
		b = b[n:]
		out.WriteByte(enc(byte(n)))
		for i := 0; i < n; i += 3 {
			var t [3]byte
			copy(t[:], chunk[i:min(i+3, n)])
			out.WriteByte(enc(t[0] >> 2))
			out.WriteByte(enc((t[0]<<4 | t[1]>>4) & 63))
			out.WriteByte(enc((t[1]<<2 | t[2]>>6) & 63))
			out.WriteByte(enc(t[2] & 63))
		}
	}
	return out.String()
}

func uudecode(s string) []byte {
	dec := func(c byte) byte { return (c - 32) & 63 }
	var out []byte
	for len(s) > 0 {
		n := int(dec(s[0]))
		s = s[1:]
		groups := (n + 2) / 3
		var line []byte
		for g := 0; g < groups && len(s) >= 4; g++ {
			a, b, c, d := dec(s[0]), dec(s[1]), dec(s[2]), dec(s[3])
			line = append(line, a<<2|b>>4, b<<4|c>>2, c<<6|d)
			s = s[4:]
		}
		out = append(out, line[:min(n, len(line))]...)
		if n == 0 {
			break
		}
	}
	return out
}

// hashInput is what a hash identifier hashes: text (N = 0) or a file's
// contents (N = 2); def is N's default.
func hashInput(r *run, c *call, def int) (string, bool) {
	n := def
	if len(c.vals) > 1 {
		n = int(c.num(1))
	}
	if n == 2 {
		return r.readFile(c.arg(0))
	}
	return c.arg(0), true
}

// hashIdent is $md5, $sha1, $sha256...(text|file, N): a hex digest of
// text, or with N = 2 of a file.
func hashIdent(h func() hash.Hash) builtin {
	return func(r *run, fr *frame, c *call) (string, error) {
		data, ok := hashInput(r, c, 0)
		if !ok {
			return "", nil
		}
		d := h()
		d.Write([]byte(data))
		return hex.EncodeToString(d.Sum(nil)), nil
	}
}
