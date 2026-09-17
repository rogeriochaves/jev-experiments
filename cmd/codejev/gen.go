package main

import (
	"context"
	"fmt"
	"math"
	"math/rand/v2"
	"sort"
	"strings"
	"time"

	"github.com/rogeriochaves/jev-experiments/internal/jev"
)

var cavemanNames = []string{
	"rock", "fire", "cave", "tribe", "hunt", "spear", "bone", "meat", "water", "sun", "moon", "wolf",
	"bear", "fish", "river", "stick", "mammoth", "sky", "tree", "berry", "drum", "song", "path", "hill",
}

type stepTrace struct {
	Role   string             `json:"role"`
	Chosen string             `json:"chosen"`
	Prob   float64            `json:"prob"`
	Probs  map[string]float64 `json:"probs"`
}

type genResult struct {
	Code        string      `json:"code"`
	Requests    int         `json:"requests"`
	InputTokens int         `json:"input_tokens"`
	WallS       float64     `json:"wall_s"`
	Trace       []stepTrace `json:"trace"`
	Truncated   bool        `json:"truncated"`
}

const (
	maxRequests = 60
	maxBlockDepth = 3
)

type picker struct {
	temp float64
	rng  *rand.Rand
}

func (p picker) pick(probs map[string]float64, allowed map[string]any) (string, float64) {
	type kv struct {
		k string
		v float64
	}
	var items []kv
	for k, v := range probs {
		if allowed != nil {
			if _, ok := allowed[k]; !ok {
				continue
			}
		}
		items = append(items, kv{k, v})
	}
	if len(items) == 0 {
		for k := range allowed {
			items = append(items, kv{k, 1})
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].v != items[j].v {
			return items[i].v > items[j].v
		}
		return items[i].k < items[j].k
	})
	if p.temp <= 0 {
		return items[0].k, items[0].v
	}
	weights := make([]float64, len(items))
	z := 0.0
	for i, it := range items {
		weights[i] = math.Exp(math.Log(math.Max(it.v, 1e-9)) / p.temp)
		z += weights[i]
	}
	r := p.rng.Float64() * z
	acc := 0.0
	for i, w := range weights {
		acc += w
		if acc >= r {
			return items[i].k, items[i].v
		}
	}
	return items[len(items)-1].k, items[len(items)-1].v
}

func (s *state) snapshot(task Task, role string) map[string]any {
	var vars []string
	for _, v := range s.inScope() {
		vars = append(vars, fmt.Sprintf("%s (%s)", v.name, v.kind))
	}
	var examples []map[string]any
	for _, t := range task.Tests {
		examples = append(examples, map[string]any{"call": task.callString(t), "returns": t.Expect})
	}
	return map[string]any{
		"task":               task.Description,
		"signature":          task.signature(),
		"examples":           examples,
		"code_so_far":        s.code.String() + " ▮",
		"cursor_is":          role,
		"still_open":         s.pendingSummary(),
		"variables_in_scope": vars,
	}
}

func instructions(role string) map[string]any {
	return map[string]any{
		"task": "Choose what to write at the cursor ▮ in `code_so_far`, so that the finished function does what `task` says and returns the values in `examples`.",
		"note": "The cursor is " + role + ". The code is JavaScript. Only the listed options are valid here.",
	}
}

func generate(ctx context.Context, client *jev.Client, task Task, temp float64, seed uint64) (*genResult, error) {
	s := &state{names: cavemanNames}
	pk := picker{temp: temp, rng: rand.New(rand.NewPCG(seed, seed^0xabcdef))}
	res := &genResult{}
	start := time.Now()

	s.emit(task.signature() + " {")
	s.scopes = append(s.scopes, append([]scopeVar(nil), task.Params...))
	s.indent = 1
	s.push(item{kind: stmtList, role: "the function body"})

	blockDepth := 0
	for len(s.stack) > 0 {
		it := s.pop()
		switch it.kind {
		case lit:
			s.emit(it.text)
		case indentIn:
			s.indent++
			s.scopes = append(s.scopes, nil)
			blockDepth++
		case popScope:
			s.scopes = s.scopes[:len(s.scopes)-1]
			blockDepth--
		case declare:
			mutable := it.role == "any"
			s.declare(scopeVar{name: it.text, mutable: mutable, kind: it.role})
		case newName:
			// handled inside statement productions
		case stmtList:
			if err := s.stepStatement(ctx, client, task, it, pk, res, &blockDepth); err != nil {
				return res, err
			}
		case expr:
			if err := s.stepExpr(ctx, client, task, it, pk, res); err != nil {
				return res, err
			}
		case elseOpt:
			if err := s.stepElse(ctx, client, task, it, pk, res); err != nil {
				return res, err
			}
		}
	}
	res.Code = s.code.String()
	res.WallS = time.Since(start).Seconds()
	return res, nil
}

func (s *state) ask(ctx context.Context, client *jev.Client, task Task, role string, qs map[string]jev.Question, res *genResult) (*jev.Result, error) {
	r, err := client.Ask(ctx, s.snapshot(task, role), qs)
	if err != nil {
		return nil, err
	}
	res.Requests++
	res.InputTokens += r.Usage.InputTokens
	return r, nil
}

func (s *state) stepStatement(ctx context.Context, client *jev.Client, task Task, it item, pk picker, res *genResult, blockDepth *int) error {
	crit := stmtProductions(s, it)
	if res.Requests >= maxRequests {
		res.Truncated = true
		crit = map[string]any{"return": crit["return"], "end_block": crit["end_block"]}
	}
	if *blockDepth >= maxBlockDepth {
		delete(crit, "if")
		delete(crit, "for_range")
		delete(crit, "for_of")
	}
	vars := s.inScope()
	fresh := s.freshName()
	qs := map[string]jev.Question{
		"stmt": jev.Choice(instructions("the start of a new statement in "+it.role), crit),
	}
	if len(fresh) > 0 {
		qs["new_name"] = jev.Choice("If the next statement declares a new variable, which name fits it best? Names are caveman words, pick one that reads well for the value it will hold.", listCriteria(fresh))
	}
	if len(vars) > 0 {
		qs["target_var"] = jev.Choice("If the next statement assigns to a variable, loops over an array variable, or calls a method on a variable, which variable?", varCriteria(vars))
		mcrit := map[string]any{}
		for k, v := range methods {
			if v.args >= 0 {
				mcrit[k] = v.desc
			}
		}
		qs["call_method"] = jev.Choice("If the next statement calls a method on a variable for its effect, which method?", mcrit)
	}
	r, err := s.ask(ctx, client, task, "the start of a new statement in "+it.role, qs, res)
	if err != nil {
		return err
	}
	kind, p := pk.pick(r.Answers["stmt"].Probabilities, crit)
	res.Trace = append(res.Trace, stepTrace{Role: "statement", Chosen: kind, Prob: p, Probs: r.Answers["stmt"].Probabilities})
	pickName := func() string {
		if len(fresh) == 0 {
			return "thing"
		}
		n, _ := pk.pick(r.Answers["new_name"].Probabilities, listCriteria(fresh))
		return n
	}
	pickVar := func(filter func(scopeVar) bool) scopeVar {
		allowed := map[string]any{}
		var list []scopeVar
		for _, v := range vars {
			if filter == nil || filter(v) {
				allowed[v.name] = nil
				list = append(list, v)
			}
		}
		if len(list) == 0 {
			return vars[0]
		}
		n, _ := pk.pick(r.Answers["target_var"].Probabilities, allowed)
		for _, v := range list {
			if v.name == n {
				return v
			}
		}
		return list[0]
	}

	switch kind {
	case "end_block":
		s.indent--
		s.newline()
		s.emit("}")
		return nil
	case "return":
		s.newline()
		s.emit("return ")
		s.push(item{kind: expr, role: "the value returned by the function", depth: 0}, item{kind: lit, text: ";"}, it)
	case "let":
		name := pickName()
		s.newline()
		s.emit("let " + name + " = ")
		s.push(item{kind: expr, role: "the initial value of `" + name + "`", depth: 0}, item{kind: lit, text: ";"},
			item{kind: declare, text: name, role: "any"}, it)
	case "assign":
		v := pickVar(func(v scopeVar) bool { return v.mutable })
		s.newline()
		s.emit(v.name + " = ")
		s.push(item{kind: expr, role: "the new value of `" + v.name + "`", depth: 0}, item{kind: lit, text: ";"}, it)
	case "if":
		s.newline()
		s.emit("if (")
		s.push(item{kind: expr, role: "the condition of the if", depth: 1}, item{kind: lit, text: ") {"}, item{kind: indentIn},
			item{kind: stmtList, role: "the body of the if"}, item{kind: popScope}, item{kind: elseOpt, role: "an optional else branch"}, it)
	case "for_range":
		name := pickName()
		s.newline()
		s.emit("for (let " + name + " = ")
		s.push(item{kind: expr, role: "the start number of the loop counter `" + name + "`", depth: 1},
			item{kind: lit, text: "; " + name + " < "},
			item{kind: expr, role: "the end number (not included) for the loop counter `" + name + "`", depth: 1},
			item{kind: lit, text: "; " + name + "++) {"}, item{kind: indentIn},
			item{kind: stmtList, role: "the body of the for loop over `" + name + "`"}, item{kind: popScope}, it)
		s.declare(scopeVar{name: name, mutable: false, kind: "number"})
	case "for_of":
		name := pickName()
		v := pickVar(func(v scopeVar) bool { return v.kind == "array" || v.kind == "any" || v.kind == "string" })
		s.newline()
		s.emit("for (const " + name + " of " + v.name + ") {")
		s.push(item{kind: indentIn}, item{kind: declare, text: name, role: "item"},
			item{kind: stmtList, role: "the body of the loop over each `" + name + "` in `" + v.name + "`"}, item{kind: popScope}, it)
	case "call":
		v := pickVar(nil)
		m, _ := pk.pick(r.Answers["call_method"].Probabilities, nil)
		s.newline()
		s.emit(v.name + "." + m)
		if methods[m].args == 1 {
			s.push(item{kind: expr, role: "the argument of " + v.name + "." + m + ")", depth: 1}, item{kind: lit, text: ");"}, it)
		} else {
			s.emit(";")
			s.push(it)
		}
	}
	return nil
}

func (s *state) stepElse(ctx context.Context, client *jev.Client, task Task, it item, pk picker, res *genResult) error {
	crit := map[string]any{"else": "add an else branch to the if that just closed", "no_else": "no else branch"}
	r, err := s.ask(ctx, client, task, "right after the closing brace of an if", map[string]jev.Question{"else": jev.Choice(instructions("right after an if block: add an else branch or not"), crit)}, res)
	if err != nil {
		return err
	}
	k, p := pk.pick(r.Answers["else"].Probabilities, crit)
	res.Trace = append(res.Trace, stepTrace{Role: "else", Chosen: k, Prob: p, Probs: r.Answers["else"].Probabilities})
	if k == "else" {
		s.emit(" else {")
		s.push(item{kind: indentIn}, item{kind: stmtList, role: "the body of the else"}, item{kind: popScope})
	}
	return nil
}

func (s *state) stepExpr(ctx context.Context, client *jev.Client, task Task, it item, pk picker, res *genResult) error {
	if res.Requests >= maxRequests {
		res.Truncated = true
		it.depth = maxExprDepth
	}
	crit := exprProductions(s, it)
	vars := s.inScope()
	qs := map[string]jev.Question{
		"kind":   jev.Choice(instructions(it.role), crit),
		"number": jev.Choice("If the expression is a number literal, which one?", listCriteria(numbers)),
		"string": jev.Choice("If the expression is a string literal, which one?", listCriteria(strings_)),
	}
	if len(vars) > 0 {
		qs["variable"] = jev.Choice("If the expression uses a variable, which one?", varCriteria(vars))
		qs["method"] = jev.Choice("If the expression is a property or method of the variable, which one?", methodCriteria())
	}
	if it.depth < maxExprDepth {
		qs["op"] = jev.Choice("If the expression combines two values with an operator, which operator?", opCriteria())
		qs["builtin"] = jev.Choice("If the expression calls a builtin function, which one?", builtinCriteria())
	}
	r, err := s.ask(ctx, client, task, it.role, qs, res)
	if err != nil {
		return err
	}
	kind, p := pk.pick(r.Answers["kind"].Probabilities, crit)
	res.Trace = append(res.Trace, stepTrace{Role: it.role, Chosen: kind, Prob: p, Probs: r.Answers["kind"].Probabilities})
	varName := func() string {
		n, _ := pk.pick(r.Answers["variable"].Probabilities, varCriteria(vars))
		return n
	}
	d := it.depth + 1
	switch kind {
	case "variable":
		s.emit(varName())
	case "number":
		n, _ := pk.pick(r.Answers["number"].Probabilities, listCriteria(numbers))
		s.emit(n)
	case "string":
		v, _ := pk.pick(r.Answers["string"].Probabilities, listCriteria(strings_))
		s.emit(v)
	case "true", "false":
		s.emit(kind)
	case "binary":
		op, _ := pk.pick(r.Answers["op"].Probabilities, opCriteria())
		s.emit("(")
		s.push(item{kind: expr, role: "the left side of `" + op + "` in " + it.role, depth: d},
			item{kind: lit, text: " " + op + " "},
			item{kind: expr, role: "the right side of `" + op + "` in " + it.role, depth: d},
			item{kind: lit, text: ")"})
	case "not":
		s.emit("!(")
		s.push(item{kind: expr, role: "the value being negated with ! in " + it.role, depth: d}, item{kind: lit, text: ")"})
	case "negate":
		s.emit("-(")
		s.push(item{kind: expr, role: "the number being negated in " + it.role, depth: d}, item{kind: lit, text: ")"})
	case "builtin":
		b, _ := pk.pick(r.Answers["builtin"].Probabilities, builtinCriteria())
		s.emit(b)
		var items []item
		for i := 0; i < builtins[b].args; i++ {
			if i > 0 {
				items = append(items, item{kind: lit, text: ", "})
			}
			items = append(items, item{kind: expr, role: fmt.Sprintf("argument %d of %s)", i+1, b), depth: d})
		}
		items = append(items, item{kind: lit, text: ")"})
		s.push(items...)
	case "index":
		v := varName()
		s.emit(v + "[")
		s.push(item{kind: expr, role: "the position inside `" + v + "[...]`", depth: d}, item{kind: lit, text: "]"})
	case "method":
		v := varName()
		m, _ := pk.pick(r.Answers["method"].Probabilities, methodCriteria())
		s.emit(v + "." + m)
		if methods[m].args == 1 {
			s.push(item{kind: expr, role: "the argument of `" + v + "." + m + ")`", depth: d}, item{kind: lit, text: ")"})
		}
	default:
		s.emit(strings.TrimSpace(kind))
	}
	return nil
}
