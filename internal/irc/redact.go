package irc

import "strings"

const hidden = "<hidden>"

// Redact hides the secrets in a raw line before it reaches Config.Debug, so
// TROUT_DEBUG's log can be shared: SASL's credentials, PASS, OPER, the
// passwords in BouncerServ commands (-password, -pass, sasl set-plain) and
// NickServ's IDENTIFY and REGISTER. Everything else is left as is.
func Redact(line string) string {
	f := strings.Fields(line)
	i := 0
	if i < len(f) && strings.HasPrefix(f[i], "@") {
		i++
	}
	if i < len(f) && strings.HasPrefix(f[i], ":") {
		i++
	}
	if i >= len(f) {
		return line
	}
	cmd, args := strings.ToUpper(f[i]), f[i+1:]
	switch cmd {
	case "AUTHENTICATE":
		if len(args) == 1 && args[0] != "+" && args[0] != "*" && !isMech(args[0]) {
			args[0] = hidden
		}
	case "PASS":
		hideFrom(args, 0)
	case "OPER":
		hideFrom(args, 1)
	case "PRIVMSG", "NOTICE":
		if len(args) < 2 {
			return line
		}
		text := args[1:]
		text[0] = strings.TrimPrefix(text[0], ":")
		switch strings.ToLower(args[0]) {
		case "bouncerserv":
			hideServ(text)
		case "nickserv":
			hideNickServ(text)
		default:
			return line
		}
		text[0] = ":" + text[0]
	case "NICKSERV", "NS":
		if len(args) > 0 {
			args[0] = strings.TrimPrefix(args[0], ":")
			hideNickServ(args)
		}
	case "BOUNCERSERV":
		if len(args) > 0 {
			args[0] = strings.TrimPrefix(args[0], ":")
			hideServ(args)
		}
	default:
		return line
	}
	return strings.Join(f, " ")
}

// isMech reports a SASL mechanism's name (PLAIN, EXTERNAL, SCRAM-SHA-256),
// which AUTHENTICATE sends first; base64 credentials have lowercase letters,
// or end in "=".
func isMech(s string) bool {
	return strings.Trim(s, "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_") == "" && len(s) <= 20
}

func hideFrom(args []string, i int) {
	for ; i < len(args); i++ {
		args[i] = hidden
	}
}

// hideServ hides BouncerServ's password flags' values, and sasl set-plain's
// password (its last word).
func hideServ(words []string) {
	for i, w := range words {
		switch strings.ToLower(w) {
		case "-password", "-pass":
			if i+1 < len(words) {
				words[i+1] = hidden
			}
		case "set-plain":
			if i > 0 && strings.EqualFold(words[i-1], "sasl") && len(words) > i+2 {
				words[len(words)-1] = hidden
			}
		}
	}
}

// hideNickServ hides everything after IDENTIFY or REGISTER: the account
// name may come first, then the password, then (for REGISTER) an email.
func hideNickServ(words []string) {
	if len(words) > 0 {
		switch strings.ToUpper(words[0]) {
		case "IDENTIFY", "ID", "REGISTER", "GHOST", "RECOVER", "REGAIN", "SET":
			if strings.EqualFold(words[0], "SET") {
				if len(words) > 1 && strings.EqualFold(words[1], "PASSWORD") {
					hideFrom(words, 2)
				}
				return
			}
			hideFrom(words, 1)
		}
	}
}
