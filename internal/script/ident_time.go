package script

import (
	"math"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Number identifiers.
var numIdents = map[string]builtin{
	"calc": func(r *run, fr *frame, c *call) (string, error) {
		if len(c.vals) > 1 {
			return "", errorf("$calc: invalid format")
		}
		return formatNum(calc(c.arg(0))), nil
	},
	"int": func(r *run, fr *frame, c *call) (string, error) {
		return formatNum(math.Trunc(c.num(0))), nil
	},
	"abs": func(r *run, fr *frame, c *call) (string, error) {
		return formatNum(math.Abs(c.num(0))), nil
	},
	"ceil": func(r *run, fr *frame, c *call) (string, error) {
		return formatNum(math.Ceil(c.num(0))), nil
	},
	"floor": func(r *run, fr *frame, c *call) (string, error) {
		return formatNum(math.Floor(c.num(0))), nil
	},
	"sqrt": func(r *run, fr *frame, c *call) (string, error) {
		return formatNum(math.Sqrt(c.num(0))), nil
	},
	// $round(N,D) rounds to D decimals.
	"round": func(r *run, fr *frame, c *call) (string, error) {
		p := math.Pow(10, float64(clamp(int(c.num(1)), 0, 6)))
		return formatNum(math.Round(c.num(0)*p) / p), nil
	},
	"min":  func(r *run, fr *frame, c *call) (string, error) { return minMax(c, -1), nil },
	"max":  func(r *run, fr *frame, c *call) (string, error) { return minMax(c, 1), nil },
	"rand": randIdent,
	"r":    randIdent, // mIRC's old short name, which beats an alias called r
}

// randIdent is $rand(v1,v2): a number between v1 and v2, or a letter
// between two letters.
func randIdent(r *run, fr *frame, c *call) (string, error) {
	if len(c.vals) < 2 {
		return "", errorf("$rand: invalid parameters")
	}
	ra, rb := []rune(c.arg(0)), []rune(c.arg(1))
	if len(ra) == 1 && len(rb) == 1 && unicode.IsLetter(ra[0]) && unicode.IsLetter(rb[0]) {
		lo, hi := min(ra[0], rb[0]), max(ra[0], rb[0])
		return string(lo + rune(r.e.opts.Rand.IntN(int(hi-lo)+1))), nil
	}
	// Numbers past ±2^52 lose precision as floats anyway; keeping them in
	// that range keeps hi-lo+1 from overflowing (which made IntN panic).
	const limit = 1 << 52
	lo := int64(max(-limit, min(c.num(0), limit)))
	hi := int64(max(-limit, min(c.num(1), limit)))
	lo, hi = min(lo, hi), max(lo, hi)
	return strconv.FormatInt(lo+r.e.opts.Rand.Int64N(hi-lo+1), 10), nil
}

func minMax(c *call, sign float64) string {
	best, found := 0.0, false
	for _, v := range c.vals {
		n, ok := parseNum(v)
		if ok && (!found || (n-best)*sign > 0) {
			best, found = n, true
		}
	}
	if !found {
		return ""
	}
	return formatNum(best)
}

// Time and date identifiers. The time comes from the Host.
var timeIdents = map[string]builtin{
	"ctime": func(r *run, fr *frame, c *call) (string, error) {
		if c.hasArgs {
			return "", errorf("$ctime(date) isn't supported yet")
		}
		return itoa(int(r.h.Now().Unix())), nil
	},
	"gmt": func(r *run, fr *frame, c *call) (string, error) {
		if c.hasArgs {
			return asctime(r.h.Now().UTC(), c.arg(0)), nil
		}
		return itoa(int(r.h.Now().Unix())), nil
	},
	"ticks": func(r *run, fr *frame, c *call) (string, error) {
		return itoa(int(r.h.Now().UnixMilli())), nil
	},
	"time": timeFormat("HH:nn:ss"),
	"date": timeFormat("dd/mm/yyyy"),
	// $adate is the date in US order.
	"adate":     timeFormat("mm/dd/yyyy"),
	"fulldate":  timeFormat("ddd mmm dd HH:nn:ss yyyy"),
	"day":       timeFormat("dddd"),
	"timestamp": timeFormat("[HH:nn]"),
	// $asctime(N,format), $asctime(N) or $asctime(format).
	"asctime": func(r *run, fr *frame, c *call) (string, error) {
		t, format := r.h.Now(), "ddd mmm dd HH:nn:ss yyyy"
		switch {
		case len(c.vals) >= 2:
			t, format = time.Unix(int64(c.num(0)), 0).In(t.Location()), c.arg(1)
		case len(c.vals) == 1:
			if n, ok := parseNum(c.arg(0)); ok {
				t = time.Unix(int64(n), 0).In(t.Location())
			} else {
				format = c.arg(0)
			}
		}
		return asctime(t, format), nil
	},
	// $duration(seconds,N) is "1wk 2days 3hrs 4mins 5secs"; N = 2 leaves
	// out the seconds, N = 3 is hh:nn:ss. Given its own output, it turns it
	// back into seconds.
	"duration": func(r *run, fr *frame, c *call) (string, error) {
		if _, ok := parseNum(c.arg(0)); !ok {
			if secs, ok := parseDuration(c.arg(0)); ok {
				return itoa(secs), nil
			}
		}
		return duration(int(c.num(0)), int(c.num(1))), nil
	},
}

// timeFormat is an identifier like $time: the current time in a default
// format, or in the one given, as with $asctime.
func timeFormat(def string) builtin {
	return func(r *run, fr *frame, c *call) (string, error) {
		format := def
		if c.hasArgs && c.arg(0) != "" {
			format = c.arg(0)
		}
		return asctime(r.h.Now(), format), nil
	}
}

// asctime formats t with mIRC's format codes: yyyy yy, mmmm mmm mm m,
// dddd ddd dd d, HH H (24-hour), hh h (12-hour), nn n, ss s, TT T tt t
// (AM/PM), oo (1st, 2nd) and zzz zz z (time zone). Anything else is text.
func asctime(t time.Time, format string) string {
	codes := []struct {
		code string
		f    func() string
	}{
		{"yyyy", func() string { return t.Format("2006") }},
		{"yy", func() string { return t.Format("06") }},
		{"mmmm", func() string { return t.Format("January") }},
		{"mmm", func() string { return t.Format("Jan") }},
		{"mm", func() string { return t.Format("01") }},
		{"m", func() string { return t.Format("1") }},
		{"dddd", func() string { return t.Format("Monday") }},
		{"ddd", func() string { return t.Format("Mon") }},
		{"dd", func() string { return t.Format("02") }},
		{"d", func() string { return t.Format("2") }},
		{"HH", func() string { return t.Format("15") }},
		{"H", func() string { return itoa(t.Hour()) }},
		{"hh", func() string { return t.Format("03") }},
		{"h", func() string { return t.Format("3") }},
		{"nn", func() string { return t.Format("04") }},
		{"n", func() string { return t.Format("4") }},
		{"ss", func() string { return t.Format("05") }},
		{"s", func() string { return t.Format("5") }},
		{"TT", func() string { return t.Format("PM") }},
		{"T", func() string { return t.Format("PM")[:1] }},
		{"tt", func() string { return t.Format("pm") }},
		{"t", func() string { return t.Format("pm")[:1] }},
		{"oo", func() string { return ordinal(t.Day()) }},
		{"zzz", func() string { return t.Format("-0700 MST") }},
		{"zz", func() string { return t.Format("-0700") }},
		{"z", func() string {
			_, off := t.Zone()
			return t.Format("-07")[:1] + itoa(abs(off)/3600)
		}},
	}
	var b strings.Builder
next:
	for format != "" {
		for _, c := range codes {
			if strings.HasPrefix(format, c.code) {
				b.WriteString(c.f())
				format = format[len(c.code):]
				continue next
			}
		}
		b.WriteByte(format[0])
		format = format[1:]
	}
	return b.String()
}

func abs(n int) int { return max(n, -n) }

func ordinal(n int) string {
	switch {
	case n%100 >= 11 && n%100 <= 13:
		return "th"
	case n%10 == 1:
		return "st"
	case n%10 == 2:
		return "nd"
	case n%10 == 3:
		return "rd"
	}
	return "th"
}

func duration(secs, mode int) string {
	if mode == 3 {
		return twoDigits(secs/3600) + ":" + twoDigits(secs/60%60) + ":" + twoDigits(secs%60)
	}
	units := []struct {
		name string
		size int
	}{{"wk", 604800}, {"day", 86400}, {"hr", 3600}, {"min", 60}, {"sec", 1}}
	if mode == 2 {
		units = units[:4]
	}
	var parts []string
	for _, u := range units {
		n := secs / u.size
		secs %= u.size
		if n == 0 {
			continue
		}
		p := itoa(n) + u.name
		if n != 1 {
			p += "s"
		}
		parts = append(parts, p)
	}
	if len(parts) == 0 {
		return "0" + units[len(units)-1].name + "s"
	}
	return strings.Join(parts, " ")
}

func twoDigits(n int) string {
	if n < 10 {
		return "0" + itoa(n)
	}
	return itoa(n)
}

// parseDuration reads $duration's output back: "1hr 1min 40secs" or
// "01:01:40".
func parseDuration(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if parts := strings.Split(s, ":"); len(parts) == 3 {
		total := 0
		for _, p := range parts {
			if !isDigits(p) {
				return 0, false
			}
			total = total*60 + atoi(p)
		}
		return total, true
	}
	sizes := map[string]int{"wk": 604800, "day": 86400, "hr": 3600, "min": 60, "sec": 1}
	total, num := 0, -1
	for w := range strings.FieldsSeq(s) {
		// "5secs", or "5 secs" like mIRC also reads.
		n := digitsAt(w, 0, len(w))
		if n == len(w) && num < 0 {
			num = atoi(w)
			continue
		}
		unit := strings.TrimSuffix(w[n:], "s")
		if n > 0 {
			num = atoi(w[:n])
		}
		if num < 0 || sizes[unit] == 0 {
			return 0, false
		}
		total += num * sizes[unit]
		num = -1
	}
	return total, s != "" && num < 0
}
