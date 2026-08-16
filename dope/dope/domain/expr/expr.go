// Package expr is the little arithmetic language a scheme author writes
// scoring rules in (ADR-0008). It exists so a format with unfamiliar group
// scoring — «4 − место», 3/1/0, «0 очков за нулевую ничью» — is a line of
// YAML rather than a new Go rule, and so the metric a standings sorts on can be
// one the scheme invented.
//
// The language is deliberately small: numbers, named variables, arithmetic,
// comparisons, and ?:. There are no strings, no assignment and no loops, so an
// expression is always a pure float64 of its scope and can be evaluated in any
// order. Booleans are numbers — a comparison yields 1 or 0, and anything
// non-zero is true — which keeps `taken == 0 ? 0 : ...` readable without a
// second type.
//
// A pure leaf: it imports nothing of dope's and may be used from any layer.
package expr

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// Scope resolves a variable to its value. Missing names are an evaluation
// error, never a silent zero — a typo in a scoring rule would otherwise pay
// everyone nothing and look like a rules decision.
type Scope interface {
	Lookup(name string) (float64, bool)
}

// Vars is the ordinary map Scope.
type Vars map[string]float64

func (v Vars) Lookup(name string) (float64, bool) {
	value, ok := v[name]
	return value, ok
}

// Expr is a parsed expression, safe to evaluate concurrently and cheap to keep
// around — parse once when a scheme compiles, evaluate per participant.
type Expr struct {
	root node
	src  string
	vars []string
}

// String returns the source the expression was parsed from.
func (e *Expr) String() string { return e.src }

// Vars lists the variable names the expression reads, sorted. The compiler uses
// it to reject a rule naming a metric no Protocol declares, at compile time
// rather than mid-tournament.
func (e *Expr) Vars() []string { return e.vars }

// Eval computes the expression against a scope.
func (e *Expr) Eval(scope Scope) (float64, error) {
	if e == nil || e.root == nil {
		return 0, fmt.Errorf("expr: empty expression")
	}
	return e.root.eval(scope)
}

// Parse compiles an expression, reporting the first syntax error it meets.
func Parse(src string) (*Expr, error) {
	tokens, err := lex(src)
	if err != nil {
		return nil, err
	}
	p := &parser{tokens: tokens}
	root, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if p.peek().kind != tokEOF {
		return nil, fmt.Errorf("expr %q: unexpected %s", src, p.peek())
	}
	seen := map[string]bool{}
	root.collectVars(seen)
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return &Expr{root: root, src: src, vars: names}, nil
}

// --- tokens ---------------------------------------------------------------

type tokenKind int

const (
	tokEOF tokenKind = iota
	tokNumber
	tokIdent
	tokOp
)

type token struct {
	kind tokenKind
	text string
	num  float64
}

func (t token) String() string {
	if t.kind == tokEOF {
		return "end of expression"
	}
	return strconv.Quote(t.text)
}

var operators = []string{"&&", "||", "==", "!=", "<=", ">=", "+", "-", "*", "/", "%", "<", ">", "!", "(", ")", ",", "?", ":"}

func lex(src string) ([]token, error) {
	var tokens []token
	for i := 0; i < len(src); {
		c := rune(src[i])
		switch {
		case unicode.IsSpace(c):
			i++
		case c >= '0' && c <= '9' || c == '.':
			j := i
			for j < len(src) && (src[j] >= '0' && src[j] <= '9' || src[j] == '.') {
				j++
			}
			value, err := strconv.ParseFloat(src[i:j], 64)
			if err != nil {
				return nil, fmt.Errorf("expr: bad number %q", src[i:j])
			}
			tokens = append(tokens, token{kind: tokNumber, text: src[i:j], num: value})
			i = j
		case c == '_' || unicode.IsLetter(c):
			j := i
			for j < len(src) && (src[j] == '_' || src[j] >= '0' && src[j] <= '9' ||
				unicode.IsLetter(rune(src[j]))) {
				j++
			}
			tokens = append(tokens, token{kind: tokIdent, text: src[i:j]})
			i = j
		default:
			matched := ""
			for _, op := range operators {
				if strings.HasPrefix(src[i:], op) {
					matched = op
					break
				}
			}
			if matched == "" {
				return nil, fmt.Errorf("expr: unexpected character %q", string(c))
			}
			tokens = append(tokens, token{kind: tokOp, text: matched})
			i += len(matched)
		}
	}
	return append(tokens, token{kind: tokEOF}), nil
}

// --- nodes ----------------------------------------------------------------

type node interface {
	eval(Scope) (float64, error)
	collectVars(map[string]bool)
}

type numberNode float64

func (n numberNode) eval(Scope) (float64, error) { return float64(n), nil }
func (numberNode) collectVars(map[string]bool)   {}

type varNode string

func (n varNode) eval(scope Scope) (float64, error) {
	if scope == nil {
		return 0, fmt.Errorf("expr: no scope for %q", string(n))
	}
	value, ok := scope.Lookup(string(n))
	if !ok {
		return 0, fmt.Errorf("expr: unknown name %q", string(n))
	}
	return value, nil
}
func (n varNode) collectVars(seen map[string]bool) { seen[string(n)] = true }

type unaryNode struct {
	op    string
	inner node
}

func (n unaryNode) eval(scope Scope) (float64, error) {
	value, err := n.inner.eval(scope)
	if err != nil {
		return 0, err
	}
	if n.op == "!" {
		return boolValue(value == 0), nil
	}
	return -value, nil
}
func (n unaryNode) collectVars(seen map[string]bool) { n.inner.collectVars(seen) }

type binaryNode struct {
	op          string
	left, right node
}

func (n binaryNode) eval(scope Scope) (float64, error) {
	left, err := n.left.eval(scope)
	if err != nil {
		return 0, err
	}
	// && and || short-circuit, so `bouts > 0 && points / bouts > 1` is safe.
	switch n.op {
	case "&&":
		if left == 0 {
			return 0, nil
		}
	case "||":
		if left != 0 {
			return 1, nil
		}
	}
	right, err := n.right.eval(scope)
	if err != nil {
		return 0, err
	}
	switch n.op {
	case "&&", "||":
		return boolValue(right != 0), nil
	case "+":
		return left + right, nil
	case "-":
		return left - right, nil
	case "*":
		return left * right, nil
	case "/":
		if right == 0 {
			return 0, fmt.Errorf("expr: division by zero")
		}
		return left / right, nil
	case "%":
		if right == 0 {
			return 0, fmt.Errorf("expr: division by zero")
		}
		return math.Mod(left, right), nil
	case "==":
		return boolValue(left == right), nil
	case "!=":
		return boolValue(left != right), nil
	case "<":
		return boolValue(left < right), nil
	case "<=":
		return boolValue(left <= right), nil
	case ">":
		return boolValue(left > right), nil
	case ">=":
		return boolValue(left >= right), nil
	}
	return 0, fmt.Errorf("expr: unknown operator %q", n.op)
}
func (n binaryNode) collectVars(seen map[string]bool) {
	n.left.collectVars(seen)
	n.right.collectVars(seen)
}

type ternaryNode struct {
	cond, then, other node
}

func (n ternaryNode) eval(scope Scope) (float64, error) {
	cond, err := n.cond.eval(scope)
	if err != nil {
		return 0, err
	}
	if cond != 0 {
		return n.then.eval(scope)
	}
	return n.other.eval(scope)
}
func (n ternaryNode) collectVars(seen map[string]bool) {
	n.cond.collectVars(seen)
	n.then.collectVars(seen)
	n.other.collectVars(seen)
}

// calls are the few functions a ladder or a clamp needs; the language grows a
// function only when a real regulation cannot be written without it.
var calls = map[string]struct {
	arity int
	apply func([]float64) float64
}{
	"min": {2, func(a []float64) float64 { return math.Min(a[0], a[1]) }},
	"max": {2, func(a []float64) float64 { return math.Max(a[0], a[1]) }},
	"abs": {1, func(a []float64) float64 { return math.Abs(a[0]) }},
}

type callNode struct {
	name string
	args []node
}

func (n callNode) eval(scope Scope) (float64, error) {
	fn := calls[n.name]
	values := make([]float64, len(n.args))
	for i, arg := range n.args {
		value, err := arg.eval(scope)
		if err != nil {
			return 0, err
		}
		values[i] = value
	}
	return fn.apply(values), nil
}
func (n callNode) collectVars(seen map[string]bool) {
	for _, arg := range n.args {
		arg.collectVars(seen)
	}
}

func boolValue(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// --- parser ---------------------------------------------------------------

type parser struct {
	tokens []token
	pos    int
}

func (p *parser) peek() token { return p.tokens[p.pos] }

func (p *parser) next() token {
	t := p.tokens[p.pos]
	if p.pos < len(p.tokens)-1 {
		p.pos++
	}
	return t
}

func (p *parser) accept(op string) bool {
	if p.peek().kind == tokOp && p.peek().text == op {
		p.next()
		return true
	}
	return false
}

func (p *parser) expect(op string) error {
	if !p.accept(op) {
		return fmt.Errorf("expr: expected %q, got %s", op, p.peek())
	}
	return nil
}

// Precedence climbs from ?: (loosest) down to unary. Binary levels are a table
// so a new operator is one entry rather than a new function.
var binaryLevels = [][]string{
	{"||"},
	{"&&"},
	{"==", "!="},
	{"<", "<=", ">", ">="},
	{"+", "-"},
	{"*", "/", "%"},
}

func (p *parser) parseExpr() (node, error) {
	cond, err := p.parseBinary(0)
	if err != nil {
		return nil, err
	}
	if !p.accept("?") {
		return cond, nil
	}
	then, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if err := p.expect(":"); err != nil {
		return nil, err
	}
	other, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	return ternaryNode{cond: cond, then: then, other: other}, nil
}

func (p *parser) parseBinary(level int) (node, error) {
	if level >= len(binaryLevels) {
		return p.parseUnary()
	}
	left, err := p.parseBinary(level + 1)
	if err != nil {
		return nil, err
	}
	for {
		matched := ""
		for _, op := range binaryLevels[level] {
			if p.peek().kind == tokOp && p.peek().text == op {
				matched = op
				break
			}
		}
		if matched == "" {
			return left, nil
		}
		p.next()
		right, err := p.parseBinary(level + 1)
		if err != nil {
			return nil, err
		}
		left = binaryNode{op: matched, left: left, right: right}
	}
}

func (p *parser) parseUnary() (node, error) {
	for _, op := range []string{"-", "!"} {
		if p.accept(op) {
			inner, err := p.parseUnary()
			if err != nil {
				return nil, err
			}
			return unaryNode{op: op, inner: inner}, nil
		}
	}
	if p.accept("+") {
		return p.parseUnary()
	}
	return p.parsePrimary()
}

func (p *parser) parsePrimary() (node, error) {
	t := p.next()
	switch {
	case t.kind == tokNumber:
		return numberNode(t.num), nil
	case t.kind == tokIdent:
		fn, isCall := calls[t.text]
		if !isCall {
			return varNode(t.text), nil
		}
		if err := p.expect("("); err != nil {
			return nil, err
		}
		var args []node
		for {
			arg, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			args = append(args, arg)
			if !p.accept(",") {
				break
			}
		}
		if err := p.expect(")"); err != nil {
			return nil, err
		}
		if len(args) != fn.arity {
			return nil, fmt.Errorf("expr: %s takes %d argument(s), got %d", t.text, fn.arity, len(args))
		}
		return callNode{name: t.text, args: args}, nil
	case t.kind == tokOp && t.text == "(":
		inner, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if err := p.expect(")"); err != nil {
			return nil, err
		}
		return inner, nil
	}
	return nil, fmt.Errorf("expr: unexpected %s", t)
}
