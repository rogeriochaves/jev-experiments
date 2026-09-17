# jev-experiments

**Results page: https://claude.ai/code/artifact/f00ee126-9554-4e2f-b2e7-1fc86c066aa9**

Benchmarks and toys built on TypeSafe's Jev (jev-1.13), a model that returns
typed judgments (Choice, Noul, Score) with probabilities instead of text.
The page above has the measured latency, throughput, limits, judge accuracy,
a cost calculator for judging 100k traces, and the caveman text and code
generation samples.

Everything is Go. `internal/jev` is a small client over `POST /v1/systemone`
with retries and counters. Put the key in `.env` as `TYPESAFE_API_KEY=...`.

## Benchmarks (`cmd/bench`)

```
go run ./cmd/bench latency        # latency vs state size x question count, sequential
go run ./cmd/bench concurrency    # throughput at 8..512 in flight, single and packed requests
go run ./cmd/bench sustained -conc 256 -duration 60s
go run ./cmd/bench limits         # max state, max questions, choice option cap, packing ceiling
go run ./cmd/bench gendata -n 75  # 300 labelled support conversations via gpt-5-mini
go run ./cmd/bench accuracy       # "is the customer annoyed" on the dataset, single vs packed vs gpt-5-mini
```

Results land in `results/*.json`. The dataset is `data/conversations.jsonl`:
four labels (annoyed_explicit, annoyed_subtle, neutral, satisfied), three
lengths, generated with a known label so accuracy can be measured.

## cavejev (`cmd/cavejev`)

Makes Jev generate text through a Choice over a caveman vocabulary.

```
go run ./cmd/cavejev -mode seq "Why is the sky blue?"        # flat 255-word Choice per step
go run ./cmd/cavejev -mode hier "Why is the sky blue?"       # word class + per-class Choice, one request
go run ./cmd/cavejev -mode parallel "Why is the sky blue?"   # mask-predict, all slots at once, 6 rounds
```

Sampling has temperature, top-k, top-p, HF style repetition penalty, frequency
and presence penalties, no-repeat n-gram blocking, and a ban on the same word
twice in a row. `-greedy` turns all of that off to show the degenerate loop.

## codejev (`cmd/codejev`)

Makes Jev write JavaScript through a grammar. The generator keeps the code so
far and a stack of pending grammar items. Each step asks Jev to pick among the
productions that are valid at the cursor (statement kinds, expression kinds,
operators, variables in scope, builtins), with speculative fan-out so one
request resolves a whole node. The output always parses; a node harness runs
the function against tests to see whether it is also correct.

```
go run ./cmd/codejev -n 6            # 16 tasks, 6 candidates each (candidate 0 greedy)
go run ./cmd/codejev -task total -v  # one task, print every candidate
```

Identifiers come from caveman nouns (`rock`, `fire`, `tribe`).
