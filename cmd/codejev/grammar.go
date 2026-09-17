package main

import (
	"fmt"
	"sort"
	"strings"
)

// The generator keeps the code emitted so far plus a stack of pending grammar
// items. Each step pops the top item: a literal is emitted as is, a nonterminal
// is decided by Jev among the productions that are valid at that point. The
// output therefore always parses; only its meaning is up to the model.

type itemKind int

const (
	lit      itemKind = iota // emit text
	stmtList                 // zero or more statements then a closing brace
	expr                     // an expression
	elseOpt                  // optional else branch after an if block
	newName                  // declare a fresh identifier
	popScope                 // leave a block scope
	indentIn                 // enter a block: indent and push a scope
	declare                  // declare text as a variable of kind role in the current scope
)

type item struct {
	kind  itemKind
	text  string // for lit
	role  string // shown to the model: what this item is for
	depth int    // expression nesting depth, to force terminals eventually
	bind  string // for newName: which stack items receive the chosen name, via placeholder
}

type scopeVar struct {
	name    string
	mutable bool
	kind    string // "number", "string", "array", "boolean", "any"
}

type state struct {
	code   strings.Builder
	stack  []item
	scopes [][]scopeVar
	indent int
	names  []string // fresh identifier pool, caveman nouns
}

func (s *state) push(items ...item) {
	// push in reverse so items[0] is on top
	for i := len(items) - 1; i >= 0; i-- {
		s.stack = append(s.stack, items[i])
	}
}

func (s *state) pop() item {
	it := s.stack[len(s.stack)-1]
	s.stack = s.stack[:len(s.stack)-1]
	return it
}

func (s *state) emit(text string) {
	s.code.WriteString(text)
}

func (s *state) newline() {
	s.code.WriteString("\n" + strings.Repeat("  ", s.indent))
}

func (s *state) inScope() []scopeVar {
	var out []scopeVar
	for _, sc := range s.scopes {
		out = append(out, sc...)
	}
	return out
}

func (s *state) declare(v scopeVar) {
	s.scopes[len(s.scopes)-1] = append(s.scopes[len(s.scopes)-1], v)
}

func (s *state) freshName() []string {
	used := map[string]bool{}
	for _, v := range s.inScope() {
		used[v.name] = true
	}
	var out []string
	for _, n := range s.names {
		if !used[n] {
			out = append(out, n)
		}
		if len(out) == 24 {
			break
		}
	}
	return out
}

// pendingSummary tells the model what is still open, innermost first.
func (s *state) pendingSummary() []string {
	var out []string
	for i := len(s.stack) - 1; i >= 0 && len(out) < 6; i-- {
		it := s.stack[i]
		switch it.kind {
		case expr, stmtList, elseOpt, newName:
			out = append(out, it.role)
		}
	}
	return out
}

const maxExprDepth = 3

var binaryOps = map[string]string{
	"+":   "add numbers or join strings",
	"-":   "subtract",
	"*":   "multiply",
	"/":   "divide",
	"%":   "remainder after division",
	"===": "equal",
	"!==": "not equal",
	"<":   "less than",
	"<=":  "less or equal",
	">":   "greater than",
	">=":  "greater or equal",
	"&&":  "both true",
	"||":  "either true",
}

var numbers = []string{"0", "1", "2", "3", "10", "100", "-1"}

var strings_ = []string{`""`, `" "`, `"rock"`, `"fire"`, `"yes"`, `"no"`}

// method calls on a value, with how many argument expressions they take
var methods = map[string]struct {
	desc string
	args int
}{
	"length":        {"number of characters in a string or items in an array (no call, a property)", -1},
	"toUpperCase()": {"string in capital letters", 0},
	"toLowerCase()": {"string in small letters", 0},
	"trim()":        {"string without spaces at the ends", 0},
	"reverse()":     {"array reversed in place", 0},
	"sort()":        {"array sorted in place as strings", 0},
	"join(\"\")":    {"array items joined into one string", 0},
	"split(\"\")":   {"string split into an array of characters", 0},
	"includes(":     {"true when the array or string contains the argument", 1},
	"indexOf(":      {"position of the argument, -1 if absent", 1},
	"push(":         {"append the argument to the array", 1},
	"slice(":        {"copy from the argument index to the end", 1},
	"charAt(":       {"character at the argument index", 1},
}

var builtins = map[string]struct {
	desc string
	args int
}{
	"Math.max(":   {"largest of two numbers", 2},
	"Math.min(":   {"smallest of two numbers", 2},
	"Math.abs(":   {"number without its sign", 1},
	"Math.floor(": {"round a number down", 1},
	"Math.sqrt(":  {"square root", 1},
	"String(":     {"convert to string", 1},
	"Number(":     {"convert to number", 1},
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func exprProductions(s *state, it item) map[string]any {
	crit := map[string]any{}
	vars := s.inScope()
	if len(vars) > 0 {
		crit["variable"] = "a variable that is in scope"
		crit["index"] = "an item of an array variable by position, like tribe[0]"
		crit["method"] = "a property or method of a variable, like word.length or word.toUpperCase()"
	}
	crit["number"] = "a number literal"
	crit["string"] = "a string literal"
	crit["true"] = "the boolean true"
	crit["false"] = "the boolean false"
	if it.depth < maxExprDepth {
		crit["binary"] = "two expressions combined with an operator, like (a + b) or (a < b)"
		crit["not"] = "logical not of an expression, like !(a === b)"
		crit["negate"] = "negative of an expression, like -(a)"
		crit["builtin"] = "a call to a builtin function like Math.max(a, b)"
	}
	return crit
}

func stmtProductions(s *state, it item) map[string]any {
	crit := map[string]any{
		"return":    "return a value from the function",
		"let":       "declare a new variable with a value",
		"if":        "branch on a condition",
		"for_range": "count with an index from a start number up to (not including) an end number",
		"for_of":    "loop over every item of an array",
		"call":      "call a method for its effect, like tribe.push(x)",
		"end_block": "no more statements in this block, close it",
	}
	if len(s.mutableVars()) > 0 {
		crit["assign"] = "assign a new value to an existing variable"
	}
	if s.indent == 1 && strings.TrimSpace(strings.TrimPrefix(s.code.String(), "")) != "" && !strings.Contains(s.code.String(), "return") {
		// keep end_block available but the model can still return first
	}
	return crit
}

func (s *state) mutableVars() []scopeVar {
	var out []scopeVar
	for _, v := range s.inScope() {
		if v.mutable {
			out = append(out, v)
		}
	}
	return out
}

func varCriteria(vars []scopeVar) map[string]any {
	crit := map[string]any{}
	for _, v := range vars {
		crit[v.name] = fmt.Sprintf("%s variable", v.kind)
	}
	return crit
}

func listCriteria(items []string) map[string]any {
	crit := map[string]any{}
	for _, it := range items {
		crit[it] = nil
	}
	return crit
}

func methodCriteria() map[string]any {
	crit := map[string]any{}
	for k, v := range methods {
		crit[k] = v.desc
	}
	return crit
}

func builtinCriteria() map[string]any {
	crit := map[string]any{}
	for k, v := range builtins {
		crit[k] = v.desc
	}
	return crit
}

func opCriteria() map[string]any {
	crit := map[string]any{}
	for k, v := range binaryOps {
		crit[k] = v
	}
	return crit
}
