package main

import (
	"math"
	"math/rand/v2"
	"sort"
)

// SamplingConfig mirrors the knobs of a normal LM decoder.
type SamplingConfig struct {
	Temperature       float64 // 0 = greedy
	TopK              int
	TopP              float64
	RepetitionPenalty float64 // HF style: logits of seen words are multiplied (they are negative log-probs)
	FrequencyPenalty  float64 // subtracted per prior occurrence
	PresencePenalty   float64 // subtracted once if the word appeared at all
	NoRepeatNgram     int     // block a word that would complete a repeated n-gram
	MinWords          int
	MaxWords          int
}

func Greedy(maxWords int) SamplingConfig {
	return SamplingConfig{Temperature: 0, RepetitionPenalty: 1, MinWords: 4, MaxWords: maxWords}
}

func Default(maxWords int) SamplingConfig {
	return SamplingConfig{Temperature: 0.5, TopK: 10, TopP: 0.9, RepetitionPenalty: 1.3,
		FrequencyPenalty: 0.6, PresencePenalty: 0.4, NoRepeatNgram: 3, MinWords: 6, MaxWords: maxWords}
}

type Sampler struct {
	cfg SamplingConfig
	rng *rand.Rand
}

func NewSampler(cfg SamplingConfig, seed uint64) *Sampler {
	return &Sampler{cfg: cfg, rng: rand.New(rand.NewPCG(seed, seed*7919+1))}
}

// banned returns the words that cannot follow history under the grammar rules.
func banned(history []string, cfg SamplingConfig) map[string]bool {
	b := map[string]bool{}
	last := ""
	if len(history) > 0 {
		last = history[len(history)-1]
	}
	if last == "" || punctuation[last] {
		for p := range punctuation {
			b[p] = true
		}
	}
	if len(history) < cfg.MinWords || !punctuation[last] || last == "," {
		b[END] = true
	}
	if last != "" && !punctuation[last] {
		b[last] = true // never the same word twice in a row
	}
	n := cfg.NoRepeatNgram
	if n > 1 && len(history) >= n-1 {
		prefix := history[len(history)-(n-1):]
		for i := 0; i+n-1 < len(history); i++ {
			match := true
			for j := 0; j < n-1; j++ {
				if history[i+j] != prefix[j] {
					match = false
					break
				}
			}
			if match {
				b[history[i+n-1]] = true
			}
		}
	}
	return b
}

// Next picks a word from Jev's probabilities given the words so far.
func (s *Sampler) Next(probs map[string]float64, history []string) (string, float64) {
	cfg := s.cfg
	ban := banned(history, cfg)
	counts := map[string]int{}
	for _, w := range history {
		counts[w]++
	}
	type cand struct {
		w     string
		logit float64
	}
	var cands []cand
	for w, p := range probs {
		if ban[w] {
			continue
		}
		l := math.Log(math.Max(p, 1e-9))
		if c := counts[w]; c > 0 && !punctuation[w] {
			if cfg.RepetitionPenalty > 0 && cfg.RepetitionPenalty != 1 {
				l *= cfg.RepetitionPenalty // log-probs are negative, so this pushes them down
			}
			l -= cfg.FrequencyPenalty*float64(c) + cfg.PresencePenalty
		}
		cands = append(cands, cand{w, l})
	}
	if len(cands) == 0 {
		return END, 0
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].logit != cands[j].logit {
			return cands[i].logit > cands[j].logit
		}
		return cands[i].w < cands[j].w
	})
	if cfg.Temperature <= 0 {
		return cands[0].w, probs[cands[0].w]
	}
	if cfg.TopK > 0 && len(cands) > cfg.TopK {
		cands = cands[:cfg.TopK]
	}
	maxL := cands[0].logit / cfg.Temperature
	weights := make([]float64, len(cands))
	z := 0.0
	for i, c := range cands {
		weights[i] = math.Exp(c.logit/cfg.Temperature - maxL)
		z += weights[i]
	}
	cum := 0.0
	kept := len(cands)
	for i := range weights {
		weights[i] /= z
		cum += weights[i]
		if cfg.TopP > 0 && cum >= cfg.TopP {
			kept = i + 1
			break
		}
	}
	total := 0.0
	for _, w := range weights[:kept] {
		total += w
	}
	r := s.rng.Float64() * total
	acc := 0.0
	for i := 0; i < kept; i++ {
		acc += weights[i]
		if acc >= r {
			return cands[i].w, probs[cands[i].w]
		}
	}
	return cands[kept-1].w, probs[cands[kept-1].w]
}
