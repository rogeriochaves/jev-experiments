// Package dataset loads and generates the labelled support-conversation dataset.
package dataset

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rogeriochaves/jev-experiments/internal/filler"
)

type Conversation struct {
	ID       string           `json:"id"`
	Label    string           `json:"label"`
	Annoyed  bool             `json:"annoyed"`
	Length   string           `json:"length"`
	Domain   string           `json:"domain"`
	Issue    string           `json:"issue"`
	Messages []filler.Message `json:"messages"`
}

func Load(path string) ([]Conversation, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Conversation
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		if len(bytes.TrimSpace(sc.Bytes())) == 0 {
			continue
		}
		var c Conversation
		if err := json.Unmarshal(sc.Bytes(), &c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, sc.Err()
}

var labels = map[string]string{
	"annoyed_explicit": "The customer is clearly annoyed and says so: complaints, exclamation marks, threats to cancel, sarcasm, capital letters.",
	"annoyed_subtle":   "The customer is annoyed but never says so directly: curt one-line replies, repeating themselves, 'as I said before', 'fine.', passive-aggressive politeness, asking to speak to a human. No exclamation marks, no explicit complaint words like angry, frustrated, ridiculous, unacceptable.",
	"neutral":          "The customer is calm and matter of fact. They ask questions, provide details, and the conversation proceeds normally. No strong emotion in either direction.",
	"satisfied":        "The customer is happy or grateful. The agent resolved things and the customer says thanks or expresses relief or praise.",
}

var labelOrder = []string{"annoyed_explicit", "annoyed_subtle", "neutral", "satisfied"}

var lengths = map[string]string{
	"short":  "2 to 4 messages in total",
	"medium": "6 to 10 messages in total",
	"long":   "14 to 24 messages in total, with a lot of back and forth, details, order numbers, troubleshooting steps",
}

var domains = []string{
	"a bank's mobile app", "an airline's booking system", "a SaaS analytics product", "an online furniture store",
	"a telecom provider", "a food delivery app", "a video game launcher", "an electricity utility",
	"a university enrollment portal", "a car rental company", "a payroll software", "a streaming service",
	"a smart thermostat maker", "a crypto exchange", "a hospital appointment system", "a bike sharing service",
}

var issues = []string{
	"a double charge on their card", "a password reset link that never arrives", "a delivery that is late",
	"a feature that disappeared after an update", "a refund that was promised but not received",
	"an account locked after travel", "a data export that comes back empty", "an invoice with the wrong company name",
	"a subscription they cannot cancel", "a coupon code that does not apply", "an app that crashes on launch",
	"a support ticket nobody replied to", "a wrong item shipped", "a plan upgrade that did not take effect",
	"a booking that shows the wrong date", "an API key that stopped working",
}

const prompt = `Write a realistic chat transcript between a customer and an AI support agent for %s. The topic is %s.

Customer emotional state: %s

Length: %s.

Rules:
- Alternate roles, first message from the customer.
- The agent is polite and competent. Whether the issue gets resolved is up to you.
- Make it feel real: names, order ids, dates, specific details. Vary the writing style of the customer (some type in lowercase, some are formal, some are terse).
- Return only JSON with this shape: {"messages": [{"role": "user", "content": "..."}, {"role": "assistant", "content": "..."}]}
`

// OpenAIKey reads the key from the environment or the langwatch .env.
func OpenAIKey() string {
	if k := os.Getenv("OPENAI_API_KEY"); k != "" {
		return k
	}
	b, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), "Projects/langwatch/platform/app/.env"))
	if err != nil {
		panic("OPENAI_API_KEY not set")
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "OPENAI_API_KEY=") {
			return strings.Trim(strings.TrimPrefix(line, "OPENAI_API_KEY="), "\" ")
		}
	}
	panic("OPENAI_API_KEY not found")
}

// ChatJSON calls OpenAI chat completions in JSON mode and returns the content.
func ChatJSON(ctx context.Context, key, model, userPrompt string) (string, time.Duration, int, error) {
	body, _ := json.Marshal(map[string]any{
		"model":           model,
		"messages":        []map[string]string{{"role": "user", "content": userPrompt}},
		"response_format": map[string]string{"type": "json_object"},
	})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.openai.com/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", 0, 0, err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", 0, 0, fmt.Errorf("openai HTTP %d: %s", resp.StatusCode, string(rb))
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(rb, &out); err != nil {
		return "", 0, 0, err
	}
	if len(out.Choices) == 0 {
		return "", 0, 0, fmt.Errorf("openai: no choices")
	}
	return out.Choices[0].Message.Content, time.Since(start), out.Usage.PromptTokens + out.Usage.CompletionTokens, nil
}

// Generate fills path up to nPerLabel conversations per label, resuming from existing rows.
func Generate(ctx context.Context, path string, nPerLabel int, concurrency int) error {
	existing := map[string]bool{}
	if rows, err := Load(path); err == nil {
		for _, r := range rows {
			existing[r.ID] = true
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	key := OpenAIKey()
	type job struct{ label, length string; i int }
	var jobs []job
	lens := []string{"short", "medium", "long"}
	for _, label := range labelOrder {
		for i := 0; i < nPerLabel; i++ {
			length := lens[i%3]
			if existing[fmt.Sprintf("%s-%s-%d", label, length, i)] {
				continue
			}
			jobs = append(jobs, job{label, length, i})
		}
	}
	fmt.Fprintf(os.Stderr, "generating %d conversations\n", len(jobs))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, concurrency)
	done := 0
	for _, j := range jobs {
		wg.Add(1)
		go func(j job) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			rng := rand.New(rand.NewPCG(uint64(j.i), uint64(len(j.label)*31+len(j.length))))
			domain := domains[rng.IntN(len(domains))]
			issue := issues[rng.IntN(len(issues))]
			p := fmt.Sprintf(prompt, domain, issue, labels[j.label], lengths[j.length])
			var content string
			var err error
			for attempt := 0; attempt < 3; attempt++ {
				content, _, _, err = ChatJSON(ctx, key, "gpt-5-mini", p)
				if err == nil {
					break
				}
				time.Sleep(2 * time.Second)
			}
			if err != nil {
				fmt.Fprintln(os.Stderr, "failed", j, err)
				return
			}
			var parsed struct {
				Messages []filler.Message `json:"messages"`
			}
			if err := json.Unmarshal([]byte(content), &parsed); err != nil || len(parsed.Messages) == 0 {
				fmt.Fprintln(os.Stderr, "bad json", j, err)
				return
			}
			row := Conversation{
				ID: fmt.Sprintf("%s-%s-%d", j.label, j.length, j.i), Label: j.label,
				Annoyed: strings.HasPrefix(j.label, "annoyed"), Length: j.length,
				Domain: domain, Issue: issue, Messages: parsed.Messages,
			}
			b, _ := json.Marshal(row)
			mu.Lock()
			f.Write(append(b, '\n'))
			done++
			if done%20 == 0 {
				fmt.Fprintf(os.Stderr, "%d/%d\n", done, len(jobs))
			}
			mu.Unlock()
		}(j)
	}
	wg.Wait()
	return nil
}
