package script

import (
	"math"
	"strconv"
	"strings"
)

// parseNum reads a number the way mIRC's comparisons and math do.
func parseNum(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" || strings.ContainsAny(s, "xXeEpP_iInN") {
		// No hex, exponents, Inf or NaN: those are text to mIRC.
		return 0, false
	}
	f, err := strconv.ParseFloat(s, 64)
	return f, err == nil
}

// formatNum writes a number like mIRC: no decimals for whole numbers, and
// at most six otherwise, without trailing zeros.
func formatNum(f float64) string {
	if math.IsInf(f, 0) || math.IsNaN(f) {
		return "0"
	}
	if f == math.Trunc(f) && math.Abs(f) < 1e18 {
		return strconv.FormatInt(int64(f), 10)
	}
	s := strconv.FormatFloat(f, 'f', 6, 64)
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	if s == "-0" {
		return "0"
	}
	return s
}

// calcOp is one math operation. Dividing by zero gives 0, like mIRC.
func calcOp(op byte, a, b float64) float64 {
	switch op {
	case '+':
		return a + b
	case '-':
		return a - b
	case '*':
		return a * b
	case '/':
		if b == 0 {
			return 0
		}
		return a / b
	case '%':
		if b == 0 {
			return 0
		}
		return math.Mod(a, b)
	case '^':
		return math.Pow(a, b)
	}
	return 0
}

// calc evaluates $calc's expression: + - * / // % ^ and brackets, with ^
// first, then * / // %, then + -, each left to right. Anything it can't
// read counts as 0.
func calc(s string) float64 {
	c := &calcParser{s: s}
	return c.sum()
}

type calcParser struct {
	s string
	i int
}

func (c *calcParser) skip() {
	for c.i < len(c.s) && c.s[c.i] == ' ' {
		c.i++
	}
}

func (c *calcParser) peek() byte {
	c.skip()
	if c.i < len(c.s) {
		return c.s[c.i]
	}
	return 0
}

func (c *calcParser) sum() float64 {
	v := c.product()
	for {
		op := c.peek()
		if op != '+' && op != '-' {
			return v
		}
		c.i++
		v = calcOp(op, v, c.product())
	}
}

func (c *calcParser) product() float64 {
	v := c.power()
	for {
		op := c.peek()
		if op != '*' && op != '/' && op != '%' {
			return v
		}
		c.i++
		if op == '/' && c.peek() == '/' {
			// // divides and drops the fraction: $calc(7 // 2) is 3.
			c.i++
			v = math.Trunc(calcOp('/', v, c.power()))
			continue
		}
		v = calcOp(op, v, c.power())
	}
}

// power is a chain of ^, which mIRC does left to right: 2 ^ 3 ^ 2 is 64.
func (c *calcParser) power() float64 {
	v := c.unary()
	for c.peek() == '^' {
		c.i++
		v = calcOp('^', v, c.unary())
	}
	return v
}

func (c *calcParser) unary() float64 {
	switch c.peek() {
	case '-':
		c.i++
		return -c.unary()
	case '+':
		c.i++
		return c.unary()
	case '(':
		c.i++
		v := c.sum()
		if c.peek() == ')' {
			c.i++
		}
		return v
	}
	start := c.i
	for c.i < len(c.s) && (c.s[c.i] >= '0' && c.s[c.i] <= '9' || c.s[c.i] == '.') {
		c.i++
	}
	if c.i == start {
		// Not a number: skip the junk so parsing moves on.
		for c.i < len(c.s) && !strings.ContainsRune("+-*/%^() ", rune(c.s[c.i])) {
			c.i++
		}
		return 0
	}
	f, _ := strconv.ParseFloat(c.s[start:c.i], 64)
	return f
}
