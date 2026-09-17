// Command cavejev makes Jev, a model that cannot generate text, generate text
// anyway by turning its Choice distributions over a caveman vocabulary into a
// sampling loop.
//
// Modes:
//
//	seq      one request per word, Choice over a flat 255-word vocabulary
//	hier     one request per word, speculative fan-out: "which word class" plus
//	         one Choice per class, multiplied in code (effective vocabulary ~500)
//	parallel mask-predict: every position predicted at once, low-confidence
//	         positions re-masked and re-predicted for a few rounds
//
//	go run ./cmd/cavejev -mode seq "Why is the sky blue?"
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rogeriochaves/jev-experiments/internal/jev"
)

var style = map[string]any{
	"who":   "A smart caveman answering `human_says`. Terse. All substance stays, only fluff dies.",
	"rules": "Drop articles (a, an, the), filler, pleasantries, hedging. Fragments OK. Short words. Say 'me' not 'I'. Present tense. One idea per sentence, sentences short. Pattern: [thing] [action] [reason]. [next step].",
	"examples": []string{
		"Human: why React component re-render? Caveman: New thing each time. Same name, new thing, so change again. Fix: remember thing.",
		"Human: explain database pooling. Caveman: Pool keep door open. No new door each time. Fast.",
		"Human: how are you? Caveman: Me good. Fire warm. Belly full. You?",
	},
}

func seqInstructions() map[string]any {
	return map[string]any{
		"task":  "Predict the single next word of `caveman_reply_so_far`.",
		"style": style,
		"rules": []string{
			"Pick the word that continues the reply grammatically and actually answers `human_says`.",
			"Pick punctuation when the current sentence is complete.",
			"Pick <END> only when the whole reply is complete.",
		},
	}
}

type step struct {
	Word       string             `json:"word"`
	Prob       float64            `json:"prob"`
	Confidence float64            `json:"confidence"`
	Top5       []wordProb         `json:"top5"`
	LatencyMs  float64            `json:"latency_ms"`
	Tokens     int                `json:"input_tokens"`
	Class      map[string]float64 `json:"class_probs,omitempty"`
}

type wordProb struct {
	Word string  `json:"word"`
	Prob float64 `json:"prob"`
}

type generation struct {
	Prompt      string  `json:"prompt"`
	Mode        string  `json:"mode"`
	Text        string  `json:"text"`
	Words       []string `json:"words"`
	Steps       []step  `json:"steps,omitempty"`
	Rounds      []round `json:"rounds,omitempty"`
	Requests    int     `json:"requests"`
	InputTokens int     `json:"input_tokens"`
	WallS       float64 `json:"wall_s"`
}

type round struct {
	Round    int      `json:"round"`
	Words    []string `json:"words"`
	Masked   int      `json:"masked"`
	Requests int      `json:"requests"`
	WallMs   float64  `json:"wall_ms"`
}

func top5(probs map[string]float64) []wordProb {
	var all []wordProb
	for w, p := range probs {
		all = append(all, wordProb{w, p})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Prob > all[j].Prob })
	if len(all) > 5 {
		all = all[:5]
	}
	return all
}

// ---------- sequential ----------

func generateSeq(ctx context.Context, client *jev.Client, prompt string, cfg SamplingConfig, seed uint64, hier bool) (*generation, error) {
	g := &generation{Prompt: prompt, Mode: "seq"}
	if hier {
		g.Mode = "hier"
	}
	s := NewSampler(cfg, seed)
	flat, full := Flat(), Full()
	start := time.Now()
	for len(g.Words) < cfg.MaxWords {
		state := map[string]any{"human_says": prompt, "caveman_reply_so_far": detokenize(g.Words)}
		if len(g.Words) == 0 {
			state["caveman_reply_so_far"] = "(nothing yet, this is the first word)"
		}
		var probs map[string]float64
		var st step
		var r *jev.Result
		var err error
		if !hier {
			r, err = client.Ask(ctx, state, map[string]jev.Question{"next_word": jev.Choice(seqInstructions(), criteriaFor(flat.Words))})
			if err != nil {
				return g, err
			}
			a := r.Answers["next_word"]
			probs = a.Probabilities
			st.Confidence = a.Confidence
		} else {
			qs := map[string]jev.Question{}
			classCrit := map[string]any{}
			for _, c := range categories {
				classCrit[c.Name] = c.Desc
				inst := seqInstructions()
				inst["task"] = fmt.Sprintf("Assume the next word of `caveman_reply_so_far` is a %s (%s). Which one?", c.Name, c.Desc)
				qs["word_"+c.Name] = jev.Choice(inst, criteriaFor(full.ByCategory[c.Name]))
			}
			inst := seqInstructions()
			inst["task"] = "Which class of word comes next in `caveman_reply_so_far`?"
			qs["class"] = jev.Choice(inst, classCrit)
			r, err = client.Ask(ctx, state, qs)
			if err != nil {
				return g, err
			}
			class := r.Answers["class"]
			st.Class = class.Probabilities
			st.Confidence = class.Confidence
			probs = map[string]float64{}
			for _, c := range categories {
				pc := class.Probabilities[c.Name]
				for w, pw := range r.Answers["word_"+c.Name].Probabilities {
					probs[w] = pc * pw
				}
			}
		}
		word, p := s.Next(probs, g.Words)
		st.Word, st.Prob, st.Top5, st.LatencyMs, st.Tokens = word, p, top5(probs), float64(r.Latency.Milliseconds()), r.Usage.InputTokens
		g.Steps = append(g.Steps, st)
		g.Requests++
		g.InputTokens += r.Usage.InputTokens
		if word == END {
			break
		}
		g.Words = append(g.Words, word)
	}
	g.WallS = time.Since(start).Seconds()
	g.Text = detokenize(g.Words)
	return g, nil
}

// ---------- parallel (mask-predict) ----------

const positionsPerRequest = 28 // 255-option choices are ~1.7k tokens each, 64k cap per request

func generateParallel(ctx context.Context, client *jev.Client, prompt string, cfg SamplingConfig, seed uint64, length, rounds int) (*generation, error) {
	g := &generation{Prompt: prompt, Mode: "parallel"}
	s := NewSampler(cfg, seed)
	flat := Flat()
	start := time.Now()

	if length <= 0 {
		r, err := client.Ask(ctx, map[string]any{"human_says": prompt, "style": style}, map[string]jev.Question{
			"length": jev.Choice("How many words does a good caveman reply to `human_says` need?", map[string]any{
				"8": "one short sentence", "16": "two short sentences", "24": "three short sentences", "40": "a short story or a list of steps",
			}),
		})
		if err != nil {
			return g, err
		}
		fmt.Sscanf(r.Answers["length"].Choice, "%d", &length)
		g.Requests++
		g.InputTokens += r.Usage.InputTokens
	}

	words := make([]string, length)
	probs := make([]float64, length)
	masked := make([]bool, length)
	for i := range masked {
		masked[i] = true
	}
	for t := 0; t < rounds; t++ {
		roundStart := time.Now()
		var idx []int
		for i, m := range masked {
			if m {
				idx = append(idx, i)
			}
		}
		if len(idx) == 0 {
			break
		}
		shown := make([]string, length)
		for i := range words {
			if masked[i] {
				shown[i] = "?"
			} else {
				shown[i] = words[i]
			}
		}
		state := map[string]any{
			"human_says":   prompt,
			"reply_words":  shown,
			"note":         "`reply_words` is the caveman reply as a list of words, one per slot. '?' marks a slot that is still empty.",
			"style":        style,
		}
		type ans struct {
			i     int
			probs map[string]float64
		}
		results := make(chan ans, len(idx))
		var wg sync.WaitGroup
		var firstErr error
		var mu sync.Mutex
		reqs := 0
		for b := 0; b < len(idx); b += positionsPerRequest {
			chunk := idx[b:min(b+positionsPerRequest, len(idx))]
			reqs++
			wg.Add(1)
			go func(chunk []int) {
				defer wg.Done()
				qs := map[string]jev.Question{}
				for _, i := range chunk {
					qs[fmt.Sprintf("slot_%d", i)] = jev.Choice(map[string]any{
						"task":  fmt.Sprintf("Which word belongs in `reply_words[%d]` so that the whole reply reads as a good caveman answer to `human_says`?", i),
						"style": style,
						"rules": []string{"Punctuation options end a sentence.", "<END> means the reply already ended before this slot, so this slot and everything after it stay empty."},
					}, criteriaFor(flat.Words))
				}
				r, err := client.Ask(ctx, state, qs)
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					if firstErr == nil {
						firstErr = err
					}
					return
				}
				g.InputTokens += r.Usage.InputTokens
				for _, i := range chunk {
					results <- ans{i, r.Answers[fmt.Sprintf("slot_%d", i)].Probabilities}
				}
			}(chunk)
		}
		wg.Wait()
		close(results)
		if firstErr != nil {
			return g, firstErr
		}
		g.Requests += reqs
		// fill masked slots, left to right, so repetition control sees the words chosen so far this round
		byIdx := map[int]map[string]float64{}
		for a := range results {
			byIdx[a.i] = a.probs
		}
		for _, i := range idx {
			var history []string
			for j := 0; j < i; j++ {
				if words[j] != "" {
					history = append(history, words[j])
				}
			}
			w, p := s.Next(byIdx[i], history)
			words[i], probs[i], masked[i] = w, p, false
		}
		g.Rounds = append(g.Rounds, round{Round: t, Words: append([]string(nil), words...), Masked: len(idx), Requests: reqs, WallMs: float64(time.Since(roundStart).Milliseconds())})
		// re-mask the least confident positions for the next round
		n := length * (rounds - t - 1) / rounds
		if n == 0 {
			break
		}
		order := make([]int, length)
		for i := range order {
			order[i] = i
		}
		sort.Slice(order, func(a, b int) bool { return probs[order[a]] < probs[order[b]] })
		for _, i := range order[:n] {
			masked[i] = true
		}
	}
	// cut at the first <END>
	for i, w := range words {
		if w == END {
			words = words[:i]
			break
		}
	}
	g.Words = words
	g.WallS = time.Since(start).Seconds()
	g.Text = detokenize(words)
	return g, nil
}

func main() {
	mode := flag.String("mode", "seq", "seq | hier | parallel")
	greedy := flag.Bool("greedy", false, "argmax decoding with no repetition control")
	maxWords := flag.Int("max-words", 64, "maximum words (seq/hier)")
	length := flag.Int("len", 0, "parallel: fixed length, 0 = let Jev pick")
	rounds := flag.Int("rounds", 6, "parallel: refinement rounds")
	seed := flag.Uint64("seed", 1, "sampling seed")
	out := flag.String("out", "", "write JSON results here")
	verbose := flag.Bool("v", false, "print top-5 per step")
	flag.Parse()
	prompts := flag.Args()
	if len(prompts) == 0 {
		prompts = []string{
			"Why is the sky blue?",
			"My code has a bug and I can't find it. What should I do?",
			"Tell me a story about a mammoth hunt.",
			"What do you think about money?",
		}
	}
	cfg := Default(*maxWords)
	if *greedy {
		cfg = Greedy(*maxWords)
	}
	client := jev.New(64)
	ctx := context.Background()
	gens := make([]*generation, len(prompts))
	start := time.Now()
	var wg sync.WaitGroup
	for i, p := range prompts {
		wg.Add(1)
		go func(i int, p string) {
			defer wg.Done()
			var g *generation
			var err error
			switch *mode {
			case "seq":
				g, err = generateSeq(ctx, client, p, cfg, *seed+uint64(i), false)
			case "hier":
				g, err = generateSeq(ctx, client, p, cfg, *seed+uint64(i), true)
			case "parallel":
				g, err = generateParallel(ctx, client, p, cfg, *seed+uint64(i), *length, *rounds)
			default:
				err = fmt.Errorf("unknown mode %q", *mode)
			}
			if err != nil {
				fmt.Fprintln(os.Stderr, "error:", err)
			}
			gens[i] = g
		}(i, p)
	}
	wg.Wait()
	wall := time.Since(start)
	totalWords := 0
	for _, g := range gens {
		if g == nil {
			continue
		}
		totalWords += len(g.Words)
		fmt.Printf("\n> %s\n%s\n", g.Prompt, g.Text)
		fmt.Printf("  [%s: %d words, %d requests, %.1fs, %.1f words/s, %d input tokens, $%.4f]\n",
			g.Mode, len(g.Words), g.Requests, g.WallS, float64(len(g.Words))/g.WallS, g.InputTokens, float64(g.InputTokens)/1e6*jev.PricePerMtok)
		if *verbose {
			for _, st := range g.Steps {
				var parts []string
				for _, wp := range st.Top5 {
					parts = append(parts, fmt.Sprintf("%s %.2f", wp.Word, wp.Prob))
				}
				fmt.Printf("    %-10s p=%.2f conf=%.2f  [%s]\n", st.Word, st.Prob, st.Confidence, strings.Join(parts, ", "))
			}
			for _, r := range g.Rounds {
				fmt.Printf("    round %d (%d masked, %d req, %.0fms): %s\n", r.Round, r.Masked, r.Requests, r.WallMs, strings.Join(r.Words, " "))
			}
		}
	}
	fmt.Printf("\n%d prompts in parallel: %.1fs wall, %.1f words/s aggregate, %d input tokens total\n",
		len(prompts), wall.Seconds(), float64(totalWords)/wall.Seconds(), client.Stats.InputTokens.Load())
	if *out != "" {
		b, _ := json.MarshalIndent(map[string]any{"mode": *mode, "config": cfg, "generations": gens, "wall_s": wall.Seconds()}, "", " ")
		os.WriteFile(*out, b, 0o644)
	}
}
