package script

import "strings"

// stmt is one statement in an alias or event body.
type stmt interface{ line() int }

// cmdStmt is a command, like "msg $chan hi" or "%x = 1".
type cmdStmt struct {
	n    int
	text string
}

// ifStmt is an if with its elseif and else branches. cond is "" for else.
type ifStmt struct {
	n        int
	branches []branch
}

type branch struct {
	cond string
	body []stmt
}

type whileStmt struct {
	n    int
	cond string
	body []stmt
}

// labelStmt is a :label for /goto.
type labelStmt struct {
	n    int
	name string
}

func (s *cmdStmt) line() int   { return s.n }
func (s *ifStmt) line() int    { return s.n }
func (s *whileStmt) line() int { return s.n }
func (s *labelStmt) line() int { return s.n }

// token is a piece of a body: a command, or a { or } on its own.
type token struct {
	n    int
	text string // "{", "}" or a command
}

// tokens splits body lines into commands and braces. Like mIRC, | only
// separates commands and { } only open and close blocks when they're
// words of their own, surrounded by spaces, and not inside an
// identifier's brackets; and runs of spaces count as one.
func tokens(lines []srcLine) []token {
	var out []token
	for _, l := range lines {
		var cmd []string
		flush := func() {
			if len(cmd) > 0 {
				out = append(out, token{l.n, strings.Join(cmd, " ")})
				cmd = nil
			}
		}
		for _, w := range splitWords(l.text) {
			switch w {
			case "|":
				flush()
			case "{", "}":
				flush()
				out = append(out, token{l.n, w})
			default:
				cmd = append(cmd, w)
			}
		}
		flush()
	}
	return out
}

// stmts parses a body into statements, adding Problems for unbalanced
// blocks.
func (p *parser) stmts(lines []srcLine) []stmt {
	sp := &stmtParser{p: p, toks: tokens(lines)}
	body := sp.block(false)
	for sp.i < len(sp.toks) {
		// A stray }: report it and keep going.
		p.problem(sp.toks[sp.i].n, false, "this } has no matching {")
		sp.i++
		body = append(body, sp.block(false)...)
	}
	return body
}

type stmtParser struct {
	p    *parser
	toks []token
	i    int
}

// block parses statements up to a } (consumed when inner) or the end.
func (sp *stmtParser) block(inner bool) []stmt {
	var out []stmt
	for sp.i < len(sp.toks) {
		t := sp.toks[sp.i]
		switch t.text {
		case "}":
			if inner {
				sp.i++
			}
			return out
		case "{":
			// A bare block: run it in line.
			sp.i++
			out = append(out, sp.block(true)...)
			continue
		}
		sp.i++
		if s := sp.stmt(t.n, t.text); s != nil {
			out = append(out, s)
		}
	}
	if inner && len(sp.toks) > 0 {
		sp.p.problem(sp.toks[len(sp.toks)-1].n, false, "a { has no matching }")
	}
	return out
}

func (sp *stmtParser) stmt(n int, text string) stmt {
	kw, rest, _ := strings.Cut(text, " ")
	switch strings.ToLower(strings.TrimLeft(kw, "/")) {
	case "if":
		s := &ifStmt{n: n}
		cond, body := sp.condBody(n, rest)
		s.branches = append(s.branches, branch{cond, body})
		for sp.i < len(sp.toks) {
			t := sp.toks[sp.i]
			kw, rest, _ := strings.Cut(t.text, " ")
			switch strings.ToLower(kw) {
			case "elseif":
				sp.i++
				cond, body := sp.condBody(t.n, rest)
				s.branches = append(s.branches, branch{cond, body})
				continue
			case "else":
				sp.i++
				s.branches = append(s.branches, branch{"", sp.inlineOrBlock(t.n, rest)})
			}
			break
		}
		return s
	case "elseif", "else":
		sp.p.problem(n, false, kw+" without an if")
		return nil
	case "while":
		cond, body := sp.condBody(n, rest)
		return &whileStmt{n: n, cond: cond, body: body}
	}
	if name, ok := strings.CutPrefix(text, ":"); ok && !strings.Contains(name, " ") {
		return &labelStmt{n: n, name: strings.ToLower(name)}
	}
	return &cmdStmt{n: n, text: text}
}

// condBody splits "(cond) commands" or "(cond)" followed by a block.
// Without brackets, mIRC also takes "if %x == 1 echo yes": the condition
// is the first word, or three words when the second is an operator.
func (sp *stmtParser) condBody(n int, rest string) (string, []stmt) {
	rest = strings.TrimSpace(rest)
	var cond string
	if strings.HasPrefix(rest, "(") {
		// (a) || (b) is one condition too, like mIRC reads it.
		end := -1
		for {
			e := matchParen(rest, end+1)
			if e < 0 {
				sp.p.problem(n, false, "this ( has no matching )")
				end = len(rest) - 1
				break
			}
			end = e
			after := strings.TrimLeft(rest[end+1:], " ")
			if !strings.HasPrefix(after, "&&") && !strings.HasPrefix(after, "||") {
				break
			}
			next := strings.TrimLeft(after[2:], " ")
			if !strings.HasPrefix(next, "(") {
				break
			}
			end = len(rest) - len(next) - 1
		}
		cond, rest = "("+rest[:end+1]+")", strings.TrimSpace(rest[end+1:])
	} else {
		w := strings.Fields(rest)
		k := min(1, len(w))
		if len(w) >= 3 && isOperator(w[1]) {
			k = 3
		}
		cond, rest = strings.Join(w[:k], " "), strings.Join(w[k:], " ")
	}
	return cond, sp.inlineOrBlock(n, rest)
}

// inlineOrBlock is an if's or while's body: the rest of its command, or
// the { } block after it.
func (sp *stmtParser) inlineOrBlock(n int, rest string) []stmt {
	if rest != "" {
		if s := sp.stmt(n, rest); s != nil {
			return []stmt{s}
		}
		return nil
	}
	if sp.i < len(sp.toks) && sp.toks[sp.i].text == "{" {
		sp.i++
		return sp.block(true)
	}
	return nil
}

// matchParen is the index of the ) closing the ( at s[open], or -1.
func matchParen(s string, open int) int {
	depth := 0
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}
