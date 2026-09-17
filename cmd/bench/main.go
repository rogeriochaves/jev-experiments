// Command bench measures Jev: latency vs input size, throughput under
// concurrency, hard limits, and classifier accuracy on the labelled dataset.
//
//	go run ./cmd/bench latency
//	go run ./cmd/bench concurrency
//	go run ./cmd/bench sustained -conc 128 -duration 60s
//	go run ./cmd/bench limits
//	go run ./cmd/bench accuracy
//	go run ./cmd/bench gendata -n 75
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/rogeriochaves/jev-experiments/internal/dataset"
	"github.com/rogeriochaves/jev-experiments/internal/filler"
	"github.com/rogeriochaves/jev-experiments/internal/jev"
)

const charsPerToken = 4.2

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: bench <latency|concurrency|sustained|limits|accuracy|gendata> [flags]")
		os.Exit(2)
	}
	sub, args := os.Args[1], os.Args[2:]
	ctx := context.Background()
	var err error
	switch sub {
	case "latency":
		err = runLatency(ctx)
	case "concurrency":
		err = runConcurrency(ctx, args)
	case "sustained":
		err = runSustained(ctx, args)
	case "limits":
		err = runLimits(ctx)
	case "accuracy":
		err = runAccuracy(ctx, args)
	case "gendata":
		fs := flag.NewFlagSet("gendata", flag.ExitOnError)
		n := fs.Int("n", 75, "conversations per label")
		conc := fs.Int("conc", 16, "OpenAI concurrency")
		fs.Parse(args)
		err = dataset.Generate(ctx, "data/conversations.jsonl", *n, *conc)
	default:
		err = fmt.Errorf("unknown subcommand %q", sub)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func writeJSON(path string, v any) {
	b, _ := json.MarshalIndent(v, "", " ")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		panic(err)
	}
	fmt.Println("wrote", path)
}

func percentile(ds []time.Duration, p float64) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	s := append([]time.Duration(nil), ds...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	i := int(float64(len(s)-1) * p)
	return s[i]
}

func ms(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

// ---------- latency vs size ----------

var noulTemplates = []string{
	"Is the customer annoyed at any point in the conversation?",
	"Did the agent resolve the customer's problem?",
	"Does the customer ask to talk to a human?",
	"Does the customer mention a refund?",
	"Is the conversation about a billing issue?",
	"Does the agent apologize?",
	"Does the customer repeat a request they already made?",
	"Does the customer provide an order or invoice number?",
	"Is the customer's problem about a mobile app?",
	"Does the agent escalate the issue to another team?",
}

func nouls(n int) map[string]jev.Question {
	qs := map[string]jev.Question{}
	for i := 0; i < n; i++ {
		t := noulTemplates[i%len(noulTemplates)]
		if i >= len(noulTemplates) {
			t = fmt.Sprintf("%s (variant %d)", t, i)
		}
		qs[fmt.Sprintf("q%d", i)] = jev.Noul(t)
	}
	return qs
}

type latencyRow struct {
	TargetTokens int       `json:"target_tokens"`
	Questions    int       `json:"questions"`
	InputTokens  int       `json:"input_tokens"`
	Chars        int       `json:"chars"`
	P50Ms        float64   `json:"latency_p50_ms"`
	MinMs        float64   `json:"latency_min_ms"`
	MaxMs        float64   `json:"latency_max_ms"`
	LatenciesMs  []float64 `json:"latencies_ms"`
}

func runLatency(ctx context.Context) error {
	client := jev.New(1)
	targets := []int{100, 500, 1000, 2000, 4000, 8000, 16000, 30000}
	counts := []int{1, 10, 50}
	const repeats = 5
	if _, err := client.Ask(ctx, "hello", map[string]jev.Question{"q": jev.Noul("Is this a greeting?")}); err != nil {
		return err
	}
	var rows []latencyRow
	for _, target := range targets {
		text := filler.Text(int(float64(target)*charsPerToken), 1)
		for _, nq := range counts {
			qs := nouls(nq)
			var lats []time.Duration
			toks := 0
			for i := 0; i < repeats; i++ {
				r, err := client.Ask(ctx, text, qs)
				if err != nil {
					return err
				}
				lats = append(lats, r.Latency)
				toks = r.Usage.InputTokens
			}
			row := latencyRow{TargetTokens: target, Questions: nq, InputTokens: toks, Chars: len(text),
				P50Ms: ms(percentile(lats, 0.5)), MinMs: ms(percentile(lats, 0)), MaxMs: ms(percentile(lats, 1))}
			for _, l := range lats {
				row.LatenciesMs = append(row.LatenciesMs, ms(l))
			}
			rows = append(rows, row)
			fmt.Printf("%6d target %6d tok %3d q  p50 %6.0f ms  min %6.0f  max %6.0f\n", target, toks, nq, row.P50Ms, row.MinMs, row.MaxMs)
		}
	}
	writeJSON("results/latency_vs_size.json", map[string]any{"rows": rows, "ran_at": time.Now()})
	return nil
}

// ---------- concurrency ----------

const traceChars = 8000 // ~1.9k tokens, a mid-sized support conversation

var traceQuestions = map[string]string{
	"annoyed":     "Is the customer annoyed at any point in the conversation?",
	"resolved":    "Did the agent resolve the customer's problem by the end of the conversation?",
	"wants_human": "Does the customer ask to talk to a human?",
}

func singlePayload(seed uint64) (any, map[string]jev.Question) {
	qs := map[string]jev.Question{}
	for k, v := range traceQuestions {
		qs[k] = jev.Noul(v)
	}
	return map[string]any{"conversation": filler.Transcript(traceChars, seed)}, qs
}

func packedPayload(seed uint64, n int) (any, map[string]jev.Question) {
	convs := make([][]filler.Message, n)
	qs := map[string]jev.Question{}
	for i := range convs {
		convs[i] = filler.Transcript(traceChars, seed*1000+uint64(i))
		for k, v := range traceQuestions {
			qs[fmt.Sprintf("%s_%d", k, i)] = jev.Noul(fmt.Sprintf("About `conversations[%d]` only: %s", i, v))
		}
	}
	return map[string]any{"conversations": convs}, qs
}

type levelRow struct {
	Label            string  `json:"label"`
	Concurrency      int     `json:"concurrency"`
	Requests         int     `json:"requests"`
	OK               int     `json:"ok"`
	Errors           int     `json:"errors"`
	WallS            float64 `json:"wall_s"`
	ReqPerS          float64 `json:"req_per_s"`
	TracesPerS       float64 `json:"traces_per_s"`
	TokensPerS       float64 `json:"tokens_per_s"`
	InputTokens      int     `json:"input_tokens"`
	TokensPerRequest float64 `json:"tokens_per_request"`
	P50Ms            float64 `json:"latency_p50_ms"`
	P95Ms            float64 `json:"latency_p95_ms"`
	MaxMs            float64 `json:"latency_max_ms"`
	RateLimited429   int64   `json:"rate_limited_429"`
	Retries          int     `json:"retries"`
	FirstError       string  `json:"first_error,omitempty"`
}

func runLevel(ctx context.Context, label string, conc, n, tracesPerReq int, payload func(uint64) (any, map[string]jev.Question)) levelRow {
	client := jev.New(conc)
	var mu sync.Mutex
	var lats []time.Duration
	toks, errs, retries := 0, 0, 0
	firstErr := ""
	start := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			state, qs := payload(uint64(i))
			r, err := client.Ask(ctx, state, qs)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs++
				if firstErr == "" {
					firstErr = err.Error()
				}
				return
			}
			lats = append(lats, r.Latency)
			toks += r.Usage.InputTokens
			retries += r.Attempts - 1
		}(i)
	}
	wg.Wait()
	wall := time.Since(start).Seconds()
	ok := len(lats)
	row := levelRow{Label: label, Concurrency: conc, Requests: n, OK: ok, Errors: errs, WallS: wall,
		ReqPerS: float64(ok) / wall, TracesPerS: float64(ok*tracesPerReq) / wall, TokensPerS: float64(toks) / wall,
		InputTokens: toks, TokensPerRequest: float64(toks) / float64(max(ok, 1)),
		P50Ms: ms(percentile(lats, 0.5)), P95Ms: ms(percentile(lats, 0.95)), MaxMs: ms(percentile(lats, 1)),
		RateLimited429: client.Stats.Status429.Load(), Retries: retries, FirstError: firstErr}
	fmt.Printf("%-9s conc %4d  %4d/%-4d ok  wall %6.1fs  %6.1f req/s  %7.1f traces/s  %7.1fk tok/s  p50 %6.0fms  p95 %6.0fms  429s %d  retries %d\n",
		label, conc, ok, n, wall, row.ReqPerS, row.TracesPerS, row.TokensPerS/1000, row.P50Ms, row.P95Ms, row.RateLimited429, retries)
	if firstErr != "" {
		fmt.Println("   first error:", firstErr)
	}
	return row
}

func runConcurrency(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("concurrency", flag.ExitOnError)
	packN := fs.Int("pack", 10, "traces per packed request")
	fs.Parse(args)
	type level struct {
		label string
		conc  int
		n     int
	}
	var rows []levelRow
	for _, l := range []level{{"single", 8, 64}, {"single", 32, 192}, {"single", 64, 256}, {"single", 128, 384}, {"single", 256, 512}, {"single", 512, 1024}} {
		rows = append(rows, runLevel(ctx, l.label, l.conc, l.n, 1, singlePayload))
		time.Sleep(3 * time.Second)
	}
	label := fmt.Sprintf("packed%d", *packN)
	for _, l := range []level{{label, 8, 32}, {label, 16, 48}, {label, 32, 64}, {label, 64, 128}, {label, 128, 256}} {
		rows = append(rows, runLevel(ctx, l.label, l.conc, l.n, *packN, func(s uint64) (any, map[string]jev.Question) { return packedPayload(s, *packN) }))
		time.Sleep(3 * time.Second)
	}
	writeJSON("results/concurrency.json", map[string]any{"rows": rows, "ran_at": time.Now()})
	return nil
}

// ---------- sustained ----------

type bucket struct {
	SecondFrom int     `json:"second_from"`
	Requests   int     `json:"requests"`
	Tokens     int     `json:"tokens"`
	Status429  int     `json:"status_429"`
	P50Ms      float64 `json:"latency_p50_ms"`
	P95Ms      float64 `json:"latency_p95_ms"`
}

func runSustained(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("sustained", flag.ExitOnError)
	conc := fs.Int("conc", 128, "in-flight requests")
	dur := fs.Duration("duration", 60*time.Second, "how long to hammer")
	packN := fs.Int("pack", 1, "traces per request (1 = single)")
	fs.Parse(args)
	client := jev.New(*conc)
	deadline := time.Now().Add(*dur)
	start := time.Now()
	var mu sync.Mutex
	type sample struct {
		at  time.Duration
		lat time.Duration
		tok int
		rl  int
	}
	var samples []sample
	var wg sync.WaitGroup
	var seed uint64
	for w := 0; w < *conc; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for time.Now().Before(deadline) {
				mu.Lock()
				seed++
				s := seed
				mu.Unlock()
				var state any
				var qs map[string]jev.Question
				if *packN > 1 {
					state, qs = packedPayload(s, *packN)
				} else {
					state, qs = singlePayload(s)
				}
				r, err := client.Ask(ctx, state, qs)
				if err != nil {
					fmt.Fprintln(os.Stderr, "error:", err)
					continue
				}
				mu.Lock()
				samples = append(samples, sample{time.Since(start), r.Latency, r.Usage.InputTokens, r.RateLimited})
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	wall := time.Since(start)
	var buckets []bucket
	for from := 0; from < int(wall.Seconds()); from += 10 {
		b := bucket{SecondFrom: from}
		var lats []time.Duration
		for _, s := range samples {
			sec := int(s.at.Seconds())
			if sec >= from && sec < from+10 {
				b.Requests++
				b.Tokens += s.tok
				b.Status429 += s.rl
				lats = append(lats, s.lat)
			}
		}
		b.P50Ms, b.P95Ms = ms(percentile(lats, 0.5)), ms(percentile(lats, 0.95))
		buckets = append(buckets, b)
		fmt.Printf("t=%3ds  %5.1f req/s  %7.1fk tok/s  429s %3d  p50 %6.0fms  p95 %6.0fms\n",
			from, float64(b.Requests)/10, float64(b.Tokens)/10000, b.Status429, b.P50Ms, b.P95Ms)
	}
	totalTok := 0
	for _, s := range samples {
		totalTok += s.tok
	}
	summary := map[string]any{
		"concurrency": *conc, "pack": *packN, "duration_s": wall.Seconds(), "requests": len(samples),
		"req_per_s": float64(len(samples)) / wall.Seconds(), "tokens_per_s": float64(totalTok) / wall.Seconds(),
		"traces_per_s": float64(len(samples)*(*packN)) / wall.Seconds(),
		"status_429": client.Stats.Status429.Load(), "errors": client.Stats.Errors.Load(),
		"input_tokens": totalTok, "cost_usd": float64(totalTok) / 1e6 * jev.PricePerMtok, "buckets": buckets, "ran_at": time.Now(),
	}
	fmt.Printf("total: %d req in %.0fs, %.1f req/s, %.1fk tok/s, %.1f traces/s, %d 429s, $%.3f\n",
		len(samples), wall.Seconds(), summary["req_per_s"], summary["tokens_per_s"].(float64)/1000, summary["traces_per_s"], summary["status_429"], summary["cost_usd"])
	writeJSON(fmt.Sprintf("results/sustained_c%d_p%d.json", *conc, *packN), summary)
	return nil
}

// ---------- limits ----------

func runLimits(ctx context.Context) error {
	client := jev.New(4)
	out := map[string]any{}

	// 1. largest state with a single question
	lo, hi := 20_000, 400_000 // chars
	var best *jev.Result
	bestChars := 0
	for hi-lo > 2000 {
		mid := (lo + hi) / 2
		r, err := client.Ask(ctx, filler.Text(mid, 1), map[string]jev.Question{"q": jev.Noul(noulTemplates[0])})
		if err != nil {
			if jev.IsMaxTokens(err) {
				hi = mid
				continue
			}
			return err
		}
		best, bestChars, lo = r, mid, mid
	}
	fmt.Printf("max state: ~%d chars = %d input tokens (single noul)\n", bestChars, best.Usage.InputTokens)
	out["max_state_chars"] = bestChars
	out["max_state_tokens"] = best.Usage.InputTokens

	// 2. how many nouls fit alongside a 1k-token state
	text := filler.Text(int(1000*charsPerToken), 2)
	var maxQ int
	var maxQTokens int
	for _, n := range []int{50, 100, 200, 400, 800, 1000, 1500, 2000, 3000} {
		r, err := client.Ask(ctx, text, nouls(n))
		if err != nil {
			fmt.Printf("%d nouls: %v\n", n, err)
			break
		}
		maxQ, maxQTokens = n, r.Usage.InputTokens
		fmt.Printf("%d nouls ok: %d input tokens, %.0f ms\n", n, r.Usage.InputTokens, ms(r.Latency))
	}
	out["max_nouls_with_1k_state"] = maxQ
	out["max_nouls_tokens"] = maxQTokens

	// 3. choice option cap
	crit := map[string]any{}
	for i := 0; i < 256; i++ {
		crit[fmt.Sprintf("opt%d", i)] = nil
	}
	_, err := client.Ask(ctx, "pick one", map[string]jev.Question{"q": jev.Choice("Pick any option.", crit)})
	out["choice_256_options_error"] = fmt.Sprint(err)
	fmt.Println("256 options:", err)
	delete(crit, "opt255")
	r, err := client.Ask(ctx, "pick one", map[string]jev.Question{"q": jev.Choice("Pick any option.", crit)})
	if err != nil {
		return err
	}
	out["choice_255_options_tokens"] = r.Usage.InputTokens
	fmt.Printf("255 options ok: %d input tokens, %.0f ms\n", r.Usage.InputTokens, ms(r.Latency))

	// 4. packed traces of ~1.9k tokens: how many fit
	var maxPack int
	for _, n := range []int{8, 10, 12, 14, 16} {
		state, qs := packedPayload(7, n)
		r, err := client.Ask(ctx, state, qs)
		if err != nil {
			fmt.Printf("pack %d: %v\n", n, err)
			break
		}
		maxPack = n
		fmt.Printf("pack %d ok: %d input tokens, %.0f ms\n", n, r.Usage.InputTokens, ms(r.Latency))
	}
	out["max_pack_of_1900_token_traces"] = maxPack
	writeJSON("results/limits.json", out)
	return nil
}

// ---------- accuracy ----------

const annoyedInstructions = "Is the customer annoyed at any point in the conversation? Annoyed includes explicit complaints and also subtle signs: curt replies, repeating themselves, passive-aggressive politeness, asking for a human."

type accRow struct {
	ID          string  `json:"id"`
	Label       string  `json:"label"`
	Length      string  `json:"length"`
	Annoyed     bool    `json:"annoyed"`
	Single      float64 `json:"single"`
	Packed      float64 `json:"packed"`
	Choice      string  `json:"choice"`
	ChoiceProbs map[string]float64 `json:"choice_probs"`
	GPT         *bool   `json:"gpt,omitempty"`
	Tokens      int     `json:"tokens_single"`
}

func runAccuracy(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("accuracy", flag.ExitOnError)
	packN := fs.Int("pack", 8, "conversations per packed request")
	withGPT := fs.Bool("gpt", true, "also run a gpt-5-mini baseline")
	fs.Parse(args)
	convs, err := dataset.Load("data/conversations.jsonl")
	if err != nil {
		return err
	}
	fmt.Printf("%d conversations\n", len(convs))
	rows := make([]accRow, len(convs))
	for i, c := range convs {
		rows[i] = accRow{ID: c.ID, Label: c.Label, Length: c.Length, Annoyed: c.Annoyed}
	}
	client := jev.New(64)

	// single: one conversation per request, noul + 4-way choice
	start := time.Now()
	var wg sync.WaitGroup
	for i := range convs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r, err := client.Ask(ctx, map[string]any{"conversation": convs[i].Messages}, map[string]jev.Question{
				"annoyed": jev.NoulWithCriteria(annoyedInstructions, "the customer shows annoyance, explicit or subtle", "the customer stays calm, neutral or satisfied throughout"),
				"mood": jev.Choice("What is the customer's overall mood in `conversation`?", map[string]any{
					"annoyed_explicit": "openly annoyed: complaints, exclamation marks, threats to cancel, sarcasm",
					"annoyed_subtle":   "annoyed without saying so: curt replies, repeating themselves, 'as I said', passive-aggressive politeness, asks for a human",
					"neutral":          "calm and matter of fact, no strong emotion",
					"satisfied":        "happy or grateful, thanks the agent",
				}),
			})
			if err != nil {
				fmt.Fprintln(os.Stderr, "single error:", err)
				return
			}
			rows[i].Single = r.Answers["annoyed"].Noul
			rows[i].Choice = r.Answers["mood"].Choice
			rows[i].ChoiceProbs = r.Answers["mood"].Probabilities
			rows[i].Tokens = r.Usage.InputTokens
		}(i)
	}
	wg.Wait()
	singleWall := time.Since(start)
	singleTok := client.Stats.InputTokens.Load()
	fmt.Printf("single: %d requests in %.1fs, %d tokens, $%.4f\n", len(convs), singleWall.Seconds(), singleTok, float64(singleTok)/1e6*jev.PricePerMtok)

	// packed: N conversations per request
	start = time.Now()
	before := client.Stats.InputTokens.Load()
	for g := 0; g < len(convs); g += *packN {
		end := min(g+*packN, len(convs))
		wg.Add(1)
		go func(g, end int) {
			defer wg.Done()
			batch := make([][]filler.Message, 0, end-g)
			qs := map[string]jev.Question{}
			for i := g; i < end; i++ {
				batch = append(batch, convs[i].Messages)
				qs[fmt.Sprintf("annoyed_%d", i-g)] = jev.NoulWithCriteria(
					fmt.Sprintf("Considering only `conversations[%d]`: %s", i-g, annoyedInstructions),
					"the customer shows annoyance, explicit or subtle", "the customer stays calm, neutral or satisfied throughout")
			}
			r, err := client.Ask(ctx, map[string]any{"conversations": batch}, qs)
			if err != nil {
				fmt.Fprintln(os.Stderr, "packed error:", err)
				return
			}
			for i := g; i < end; i++ {
				rows[i].Packed = r.Answers[fmt.Sprintf("annoyed_%d", i-g)].Noul
			}
		}(g, end)
	}
	wg.Wait()
	packedWall := time.Since(start)
	packedTok := client.Stats.InputTokens.Load() - before
	fmt.Printf("packed%d: %d requests in %.1fs, %d tokens, $%.4f\n", *packN, (len(convs)+*packN-1)/(*packN), packedWall.Seconds(), packedTok, float64(packedTok)/1e6*jev.PricePerMtok)

	// gpt-5-mini baseline
	var gptWall time.Duration
	gptTok := 0
	if *withGPT {
		key := dataset.OpenAIKey()
		sem := make(chan struct{}, 16)
		var mu sync.Mutex
		start = time.Now()
		for i := range convs {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				b, _ := json.Marshal(convs[i].Messages)
				p := fmt.Sprintf("%s\n\nConversation:\n%s\n\nAnswer with JSON {\"annoyed\": true|false}.", annoyedInstructions, string(b))
				var content string
				var tok int
				var err error
				for attempt := 0; attempt < 3; attempt++ {
					content, _, tok, err = dataset.ChatJSON(ctx, key, "gpt-5-mini", p)
					if err == nil {
						break
					}
					time.Sleep(2 * time.Second)
				}
				if err != nil {
					fmt.Fprintln(os.Stderr, "gpt error:", err)
					return
				}
				var parsed struct {
					Annoyed bool `json:"annoyed"`
				}
				if json.Unmarshal([]byte(content), &parsed) != nil {
					return
				}
				mu.Lock()
				v := parsed.Annoyed
				rows[i].GPT = &v
				gptTok += tok
				mu.Unlock()
			}(i)
		}
		wg.Wait()
		gptWall = time.Since(start)
		fmt.Printf("gpt-5-mini: %d requests in %.1fs, %d tokens\n", len(convs), gptWall.Seconds(), gptTok)
	}

	// metrics
	metric := func(name string, pred func(accRow) bool) map[string]any {
		tp, fp, tn, fn := 0, 0, 0, 0
		perLabel := map[string][2]int{}
		for _, r := range rows {
			p := pred(r)
			pl := perLabel[r.Label]
			pl[1]++
			if p == r.Annoyed {
				pl[0]++
			}
			perLabel[r.Label] = pl
			switch {
			case p && r.Annoyed:
				tp++
			case p && !r.Annoyed:
				fp++
			case !p && !r.Annoyed:
				tn++
			default:
				fn++
			}
		}
		acc := float64(tp+tn) / float64(len(rows))
		prec := float64(tp) / float64(max(tp+fp, 1))
		rec := float64(tp) / float64(max(tp+fn, 1))
		pl := map[string]float64{}
		for k, v := range perLabel {
			pl[k] = float64(v[0]) / float64(v[1])
		}
		fmt.Printf("%-12s acc %.3f  precision %.3f  recall %.3f  per-label %v\n", name, acc, prec, rec, pl)
		return map[string]any{"accuracy": acc, "precision": prec, "recall": rec, "tp": tp, "fp": fp, "tn": tn, "fn": fn, "per_label_accuracy": pl}
	}
	metrics := map[string]any{
		"single_noul_0.5": metric("single@0.5", func(r accRow) bool { return r.Single >= 0.5 }),
		"packed_noul_0.5": metric("packed@0.5", func(r accRow) bool { return r.Packed >= 0.5 }),
		"single_choice":   metric("choice", func(r accRow) bool { return r.Choice == "annoyed_explicit" || r.Choice == "annoyed_subtle" }),
	}
	if *withGPT {
		metrics["gpt_5_mini"] = metric("gpt-5-mini", func(r accRow) bool { return r.GPT != nil && *r.GPT })
	}
	// best threshold sweep for single
	bestT, bestAcc := 0.5, 0.0
	for t := 0.05; t < 0.96; t += 0.05 {
		c := 0
		for _, r := range rows {
			if (r.Single >= t) == r.Annoyed {
				c++
			}
		}
		if a := float64(c) / float64(len(rows)); a > bestAcc {
			bestAcc, bestT = a, t
		}
	}
	fmt.Printf("best single threshold %.2f -> acc %.3f\n", bestT, bestAcc)
	// agreement single vs packed
	agree, absDiff := 0, 0.0
	for _, r := range rows {
		if (r.Single >= 0.5) == (r.Packed >= 0.5) {
			agree++
		}
		absDiff += absf(r.Single - r.Packed)
	}
	fmt.Printf("single vs packed: label agreement %.3f, mean |diff| %.3f\n", float64(agree)/float64(len(rows)), absDiff/float64(len(rows)))
	// 4-way choice confusion
	confusion := map[string]map[string]int{}
	for _, r := range rows {
		if confusion[r.Label] == nil {
			confusion[r.Label] = map[string]int{}
		}
		confusion[r.Label][r.Choice]++
	}
	fmt.Println("4-way confusion (true -> predicted):", confusion)

	writeJSON("results/accuracy.json", map[string]any{
		"n": len(convs), "pack": *packN, "metrics": metrics,
		"best_single_threshold": bestT, "best_single_accuracy": bestAcc,
		"single_vs_packed_agreement": float64(agree) / float64(len(rows)), "single_vs_packed_mean_abs_diff": absDiff / float64(len(rows)),
		"confusion_4way": confusion,
		"single_wall_s": singleWall.Seconds(), "single_tokens": singleTok, "single_cost_usd": float64(singleTok) / 1e6 * jev.PricePerMtok,
		"packed_wall_s": packedWall.Seconds(), "packed_tokens": packedTok, "packed_cost_usd": float64(packedTok) / 1e6 * jev.PricePerMtok,
		"gpt_wall_s": gptWall.Seconds(), "gpt_tokens": gptTok,
		"rows": rows, "ran_at": time.Now(),
	})
	return nil
}

func absf(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
