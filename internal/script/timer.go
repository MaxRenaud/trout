package script

import (
	"regexp"
	"slices"
	"strings"
	"time"
)

// Timers, from mIRC's /timer: a command run every interval, some number
// of times (0 for forever). The engine keeps them; the client wakes it at
// NextTimer and calls Tick.

type timer struct {
	name     string // lower-case, like mIRC's
	reps     int    // runs left, or 0 to run until it's stopped
	delay    string // the interval as given, in seconds or with -m milliseconds
	interval time.Duration
	next     time.Time
	command  string // already evaluated once, when /timer ran
	network  string
	window   string
	paused   bool
}

// NextTimer is when the next timer is due, or the next -uN variable or
// hash item is to be unset, if there's one.
func (e *Engine) NextTimer() (time.Time, bool) {
	var next time.Time
	soonest := func(t time.Time) {
		if !t.IsZero() && (next.IsZero() || t.Before(next)) {
			next = t
		}
	}
	for _, t := range e.timers {
		if !t.paused {
			soonest(t.next)
		}
	}
	for _, v := range e.vars {
		soonest(v.expires)
	}
	for _, t := range e.tables {
		for _, at := range t.expires {
			soonest(at)
		}
	}
	return next, !next.IsZero()
}

// expire unsets the -uN variables and hash items that are due.
func (e *Engine) expire(now time.Time) {
	for key, v := range e.vars {
		if !v.expires.IsZero() && !v.expires.After(now) {
			delete(e.vars, key)
			e.dirty = true
		}
	}
	for _, t := range e.tables {
		for key, at := range t.expires {
			if !at.After(now) {
				t.del(t.names[key])
			}
		}
	}
}

// Tick runs the timers that are due by h.Now(), each once.
func (e *Engine) Tick(h Host) []Action {
	r := e.newRun(h)
	now := h.Now()
	e.expire(now)
	for _, t := range slices.Clone(e.timers) {
		if t.paused || t.next.After(now) || !slices.Contains(e.timers, t) {
			continue
		}
		r.fire(t, now)
	}
	return r.finish()
}

// fire runs a timer's command, which is evaluated again now (so $!time is
// the time it fires), and schedules its next run. A timer on its last run
// is still there while its command runs, like mIRC's ($timer(0) counts
// it), and is removed after, unless the command started a new one by the
// same name.
func (r *run) fire(t *timer, now time.Time) {
	last := t.reps == 1
	if t.reps > 1 {
		t.reps--
	}
	t.next = now.Add(max(t.interval, time.Millisecond))
	r.e.ctimer = t.name
	r.steps = 0
	fr := &frame{network: t.network, window: t.window}
	r.report(fr, r.command(fr, t.command))
	if last {
		r.e.timers = slices.DeleteFunc(r.e.timers, func(x *timer) bool { return x == t })
	}
}

// timeOfDay is /timer's optional first parameter, like 14:30.
var timeOfDay = regexp.MustCompile(`^(\d{1,2}):(\d{2})$`)

// cmdTimer is /timer[name] [-switches] [time] <reps> <interval> <command>,
// /timer[name] off (with wildcards), /timers off, and /timer[name] -e, -p,
// -P or -r to run, pause or resume one.
func (r *run) cmdTimer(fr *frame, name, rest string) error {
	line, err := r.eval(fr, rest)
	if err != nil {
		return err
	}
	all := strings.EqualFold(name, "s") // /timers
	sw, line := switches(line)
	if all || strings.EqualFold(line, "off") || line == "" && strings.ContainsAny(sw, "epPr") {
		pattern := name
		if all || pattern == "" {
			pattern = "*"
		}
		for _, t := range slices.Clone(r.e.timers) {
			if !wildMatch(pattern, t.name, false) {
				continue
			}
			switch {
			case strings.EqualFold(line, "off"):
				r.e.timers = slices.DeleteFunc(r.e.timers, func(x *timer) bool { return x == t })
			case strings.Contains(sw, "e"):
				r.fire(t, r.h.Now())
			case strings.Contains(sw, "r"):
				t.paused = false
			case strings.ContainsAny(sw, "pP"):
				t.paused = true
			}
		}
		if line == "" && !strings.ContainsAny(sw, "epPr") {
			return errorf("/timers: listing timers isn't supported; use $timer")
		}
		return nil
	}

	words := strings.SplitN(line, " ", 4)
	now := r.h.Now()
	var first time.Time
	if m := timeOfDay.FindStringSubmatch(words[0]); m != nil && len(words) == 4 {
		// A time of day: the first run is then (today, or tomorrow).
		first = time.Date(now.Year(), now.Month(), now.Day(), atoi(m[1]), atoi(m[2]), 0, 0, now.Location())
		if !first.After(now) {
			first = first.AddDate(0, 0, 1)
		}
		words = strings.SplitN(strings.Join(words[1:], " "), " ", 3)
	} else {
		words = strings.SplitN(line, " ", 3)
	}
	if len(words) < 3 || !isDigits(words[0]) {
		return errorf("/timer: insufficient parameters")
	}
	n, ok := parseNum(words[1])
	if !ok || n < 0 {
		return errorf("/timer: invalid parameters")
	}
	unit := time.Second
	if strings.ContainsAny(sw, "mh") {
		unit = time.Millisecond
	}
	t := &timer{name: strings.ToLower(name), reps: atoi(words[0]), delay: words[1],
		interval: time.Duration(n * float64(unit)), command: words[2], network: fr.network, window: fr.window}
	if t.name == "" {
		// The first free number, like mIRC.
		for i := 1; ; i++ {
			if r.e.timerByName(itoa(i)) == nil {
				t.name = itoa(i)
				break
			}
		}
	}
	t.next = now.Add(t.interval)
	if !first.IsZero() {
		t.next = first
	}
	if old := r.e.timerByName(t.name); old != nil {
		r.e.timers = slices.DeleteFunc(r.e.timers, func(x *timer) bool { return x == old })
	}
	r.e.timers = append(r.e.timers, t)
	r.e.ltimer = t.name
	return nil
}

func (e *Engine) timerByName(name string) *timer {
	for _, t := range e.timers {
		if strings.EqualFold(t.name, name) {
			return t
		}
	}
	return nil
}

var timerIdents = map[string]builtin{
	// $timer(N/name) is the timer's name; .com its command, .reps the runs
	// left, .delay its interval and .secs the seconds to its next run.
	// $timer(0) is how many there are.
	"timer": func(r *run, fr *frame, c *call) (string, error) {
		var t *timer
		if n, ok := parseNum(c.arg(0)); ok && c.prop != "name" {
			if n == 0 {
				return itoa(len(r.e.timers)), nil
			}
			if i := int(n) - 1; i >= 0 && i < len(r.e.timers) {
				t = r.e.timers[i]
			}
		} else {
			t = r.e.timerByName(c.arg(0))
		}
		if t == nil {
			return "", nil
		}
		switch c.prop {
		case "com":
			return t.command, nil
		case "reps":
			return itoa(t.reps), nil
		case "delay":
			return t.delay, nil
		case "secs":
			return itoa(int(max(0, t.next.Sub(r.h.Now()).Seconds()) + 0.5)), nil
		case "pause":
			return boolStr(t.paused), nil
		}
		return t.name, nil
	},
	// $ctimer is the timer running now, $ltimer the last one started.
	"ctimer": func(r *run, fr *frame, c *call) (string, error) { return r.e.ctimer, nil },
	"ltimer": func(r *run, fr *frame, c *call) (string, error) { return r.e.ltimer, nil },
}
