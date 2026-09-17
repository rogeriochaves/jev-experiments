// Command codejev makes Jev write JavaScript through a grammar: at every step
// it only sees the productions that are valid at the cursor, so the output
// always parses. A node harness then runs the function against tests.
//
//	go run ./cmd/codejev -n 6 -out results/codejev.json
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rogeriochaves/jev-experiments/internal/jev"
)

const harness = `
const f = require(process.argv[2]);
const tests = JSON.parse(process.argv[3]);
let pass = 0;
for (const t of tests) {
  try {
    const out = f(...t.args);
    if (JSON.stringify(out) === JSON.stringify(t.expect)) pass++;
  } catch (e) {}
}
console.log(pass);
`

type candidate struct {
	Seed        uint64  `json:"seed"`
	Temperature float64 `json:"temperature"`
	Code        string  `json:"code"`
	Parses      bool    `json:"parses"`
	Passed      int     `json:"passed"`
	Requests    int     `json:"requests"`
	InputTokens int     `json:"input_tokens"`
	WallS       float64 `json:"wall_s"`
	Truncated   bool    `json:"truncated"`
	Error       string  `json:"error,omitempty"`
	Trace       []stepTrace `json:"trace,omitempty"`
}

type taskResult struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Tests       int         `json:"tests"`
	Candidates  []candidate `json:"candidates"`
	GreedyPass  bool        `json:"greedy_pass"`
	AnyPass     bool        `json:"any_pass"`
	BestCode    string      `json:"best_code"`
	WallS       float64     `json:"wall_s"`
}

func runTests(dir string, code string, tests []Test, id string) (parses bool, passed int, err error) {
	path := filepath.Join(dir, id+".js")
	if err := os.WriteFile(path, []byte(code+"\nmodule.exports = "+strings.Fields(code)[1][:strings.Index(strings.Fields(code)[1], "(")]+";\n"), 0o644); err != nil {
		return false, 0, err
	}
	if out, err := exec.Command("node", "--check", path).CombinedOutput(); err != nil {
		return false, 0, fmt.Errorf("syntax: %s", strings.TrimSpace(string(out)))
	}
	tb, _ := json.Marshal(tests)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "node", filepath.Join(dir, "harness.js"), path, string(tb)).Output()
	if err != nil {
		return true, 0, fmt.Errorf("run: %v", err)
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(out)))
	return true, n, nil
}

func main() {
	n := flag.Int("n", 6, "candidates per task (candidate 0 is greedy)")
	temp := flag.Float64("temp", 0.6, "sampling temperature for candidates 1..n-1")
	only := flag.String("task", "", "run only this task")
	out := flag.String("out", "results/codejev.json", "results file")
	verbose := flag.Bool("v", false, "print every candidate")
	flag.Parse()

	dir, _ := os.MkdirTemp("", "codejev")
	os.WriteFile(filepath.Join(dir, "harness.js"), []byte(harness), 0o644)
	client := jev.New(64)
	ctx := context.Background()

	var results []taskResult
	start := time.Now()
	for _, task := range tasks {
		if *only != "" && task.Name != *only {
			continue
		}
		tr := taskResult{Name: task.Name, Description: task.Description, Tests: len(task.Tests), Candidates: make([]candidate, *n)}
		ts := time.Now()
		var wg sync.WaitGroup
		for i := 0; i < *n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				t := *temp
				if i == 0 {
					t = 0
				}
				c := candidate{Seed: uint64(i), Temperature: t}
				g, err := generate(ctx, client, task, t, uint64(i)+1)
				if g != nil {
					c.Code, c.Requests, c.InputTokens, c.WallS, c.Truncated, c.Trace = g.Code, g.Requests, g.InputTokens, g.WallS, g.Truncated, g.Trace
				}
				if err != nil {
					c.Error = err.Error()
				} else {
					parses, passed, terr := runTests(dir, g.Code, task.Tests, fmt.Sprintf("%s_%d", task.Name, i))
					c.Parses, c.Passed = parses, passed
					if terr != nil {
						c.Error = terr.Error()
					}
				}
				tr.Candidates[i] = c
			}(i)
		}
		wg.Wait()
		tr.WallS = time.Since(ts).Seconds()
		tr.GreedyPass = tr.Candidates[0].Passed == len(task.Tests)
		best := 0
		for i, c := range tr.Candidates {
			if c.Passed == len(task.Tests) {
				tr.AnyPass = true
			}
			if c.Passed > tr.Candidates[best].Passed {
				best = i
			}
		}
		tr.BestCode = tr.Candidates[best].Code
		results = append(results, tr)
		passes := 0
		reqs, toks := 0, 0
		for _, c := range tr.Candidates {
			if c.Passed == len(task.Tests) {
				passes++
			}
			reqs += c.Requests
			toks += c.InputTokens
		}
		fmt.Printf("%-12s greedy %-4v  pass@%d %-4v  (%d/%d candidates pass)  %d req  %6d tok  %5.1fs\n",
			task.Name, tr.GreedyPass, *n, tr.AnyPass, passes, *n, reqs, toks, tr.WallS)
		fmt.Println(indent(tr.BestCode))
		if *verbose {
			for i, c := range tr.Candidates {
				fmt.Printf("  --- candidate %d (temp %.1f) passed %d/%d %s\n%s\n", i, c.Temperature, c.Passed, len(task.Tests), c.Error, indent(c.Code))
			}
		}
	}
	greedy, anyPass := 0, 0
	for _, r := range results {
		if r.GreedyPass {
			greedy++
		}
		if r.AnyPass {
			anyPass++
		}
	}
	fmt.Printf("\n%d tasks: greedy pass %d, pass@%d %d, %.0fs wall, %d input tokens, $%.4f\n",
		len(results), greedy, *n, anyPass, time.Since(start).Seconds(), client.Stats.InputTokens.Load(), float64(client.Stats.InputTokens.Load())/1e6*jev.PricePerMtok)
	b, _ := json.MarshalIndent(map[string]any{"n": *n, "temperature": *temp, "tasks": results, "greedy_pass": greedy, "any_pass": anyPass,
		"input_tokens": client.Stats.InputTokens.Load(), "wall_s": time.Since(start).Seconds(), "ran_at": time.Now()}, "", " ")
	os.WriteFile(*out, b, 0o644)
}

func indent(code string) string {
	return "    " + strings.ReplaceAll(code, "\n", "\n    ")
}
