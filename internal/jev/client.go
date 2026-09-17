// Package jev is a minimal client for the TypeSafe System One endpoint.
//
// Raw net/http on purpose: the benchmarks need control over concurrency,
// retries and timing, and the request shape is three fields.
package jev

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

const (
	APIURL       = "https://api.typesafe.ai/v1/systemone"
	PricePerMtok = 0.042 // USD per million input tokens, jev-1.13, output free
)

// Question is the wire shape of one typed question.
type Question map[string]any

func Noul(instructions any) Question {
	return Question{"type": "noul", "instructions": instructions}
}

func NoulWithCriteria(instructions any, yes, no string) Question {
	return Question{"type": "noul", "instructions": instructions, "criteria": map[string]string{"true": yes, "false": no}}
}

func Choice(instructions any, criteria map[string]any) Question {
	return Question{"type": "choice", "instructions": instructions, "criteria": criteria}
}

func Score(instructions any, levels []any) Question {
	return Question{"type": "score", "instructions": instructions, "criteria": levels}
}

type Answer struct {
	Type          string             `json:"type"`
	Noul          float64            `json:"noul"`
	Choice        string             `json:"choice"`
	Score         float64            `json:"score"`
	Probabilities map[string]float64 `json:"probabilities"`
	Confidence    float64            `json:"confidence"`
}

type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type Response struct {
	Model   string            `json:"model"`
	Answers map[string]Answer `json:"answers"`
	Usage   Usage             `json:"usage"`
}

type Result struct {
	Response
	Latency     time.Duration
	Attempts    int
	RateLimited int
}

func (r *Result) CostUSD() float64 { return float64(r.Usage.InputTokens) / 1e6 * PricePerMtok }

// APIError is a non-retryable HTTP failure with the response body attached.
type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string { return fmt.Sprintf("HTTP %d: %s", e.Status, e.Body) }

// Stats are process-wide counters, read them with Snapshot.
type Stats struct {
	Requests    atomic.Int64
	InputTokens atomic.Int64
	Status429   atomic.Int64
	Status5xx   atomic.Int64
	Errors      atomic.Int64
}

type Client struct {
	Model       string
	MaxAttempts int
	key         string
	http        *http.Client
	sem         chan struct{}
	Stats       Stats
}

type Option func(*Client)

func WithModel(m string) Option        { return func(c *Client) { c.Model = m } }
func WithMaxAttempts(n int) Option     { return func(c *Client) { c.MaxAttempts = n } }
func WithTimeout(d time.Duration) Option {
	return func(c *Client) { c.http.Timeout = d }
}

// New builds a client that allows at most maxConcurrency in-flight requests.
func New(maxConcurrency int, opts ...Option) *Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.MaxIdleConns = maxConcurrency + 16
	tr.MaxIdleConnsPerHost = maxConcurrency + 16
	tr.MaxConnsPerHost = 0
	c := &Client{
		Model:       "jev-latest",
		MaxAttempts: 8,
		key:         LoadAPIKey(),
		http:        &http.Client{Transport: tr, Timeout: 120 * time.Second},
		sem:         make(chan struct{}, maxConcurrency),
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

func LoadAPIKey() string {
	if k := os.Getenv("TYPESAFE_API_KEY"); k != "" {
		return k
	}
	for _, p := range []string{".env", filepath.Join(os.Getenv("HOME"), "Projects/jev-experiments/.env")} {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(line, "TYPESAFE_API_KEY=") {
				return strings.TrimSpace(strings.TrimPrefix(line, "TYPESAFE_API_KEY="))
			}
		}
	}
	panic("TYPESAFE_API_KEY not set")
}

// Ask evaluates the questions over state, retrying 429/529/5xx with backoff.
func (c *Client) Ask(ctx context.Context, state any, questions map[string]Question) (*Result, error) {
	body, err := json.Marshal(map[string]any{"state": state, "model": c.Model, "questions": questions})
	if err != nil {
		return nil, err
	}
	select {
	case c.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-c.sem }()

	start := time.Now()
	attempts, rateLimited := 0, 0
	for {
		attempts++
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, APIURL, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+c.key)
		req.Header.Set("Content-Type", "application/json")
		resp, err := c.http.Do(req)
		if err != nil {
			if attempts >= c.MaxAttempts || ctx.Err() != nil {
				c.Stats.Errors.Add(1)
				return nil, err
			}
			sleep(ctx, backoff(attempts))
			continue
		}
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		switch {
		case resp.StatusCode == http.StatusOK:
			var out Response
			if err := json.Unmarshal(respBody, &out); err != nil {
				return nil, fmt.Errorf("decode: %w (%s)", err, truncate(respBody))
			}
			c.Stats.Requests.Add(1)
			c.Stats.InputTokens.Add(int64(out.Usage.InputTokens))
			return &Result{Response: out, Latency: time.Since(start), Attempts: attempts, RateLimited: rateLimited}, nil
		case resp.StatusCode == 429 || resp.StatusCode == 529 || resp.StatusCode >= 500:
			if resp.StatusCode == 429 {
				rateLimited++
				c.Stats.Status429.Add(1)
			} else {
				c.Stats.Status5xx.Add(1)
			}
			if attempts >= c.MaxAttempts {
				c.Stats.Errors.Add(1)
				return nil, fmt.Errorf("gave up after %d attempts: %w", attempts, &APIError{resp.StatusCode, truncate(respBody)})
			}
			d := backoff(attempts)
			if ra := resp.Header.Get("Retry-After"); ra != "" {
				if secs, err := strconv.ParseFloat(ra, 64); err == nil {
					d = time.Duration(secs * float64(time.Second))
				}
			}
			sleep(ctx, d)
		default:
			c.Stats.Errors.Add(1)
			return nil, &APIError{resp.StatusCode, truncate(respBody)}
		}
	}
}

func backoff(attempt int) time.Duration {
	base := time.Duration(1<<uint(min(attempt, 4))) * time.Second
	return time.Duration(float64(base) * (0.5 + rand.Float64()*0.5))
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
	}
}

func truncate(b []byte) string {
	if len(b) > 300 {
		return string(b[:300]) + "..."
	}
	return string(b)
}

// IsMaxTokens reports whether err is the API's max_tokens_exceeded validation error.
func IsMaxTokens(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Status == 400 && strings.Contains(ae.Body, "max_tokens_exceeded")
}
