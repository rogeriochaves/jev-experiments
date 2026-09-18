cli.mdx  (both rules, 10 sections, threshold 0.7)

## Asking a first question  [65 words]
  0.58  docs/artifact-as-subject  Rule 17: give the verbs to the reader

## Pricing a run before you ask  [69 words]
  0.65  docs/tool-seat-expectations  Rule 25: set expectations from the reader's seat, not the tool's
        > The estimate counts the rows, measures a sample of their texts, and prices the run from that.
  0.58  docs/statement-stack  Rule 28: prose, not a stack of statements

## Reading what came back  [66 words]
  0.51  docs/artifact-as-subject  Rule 17: give the verbs to the reader

## run  [709 words]
! 1.00  docs/artifact-verbs-literal  Rule 17: artifact as subject of a cognitive, transport or posture verb (literal shape)  (2 hits)
        > Without one, questions are called `q1`, `q2` and so on, and that name is the column the answer lands in and the name every judgement is filed under.
! 0.85  docs/artifact-as-subject  Rule 17: give the verbs to the reader
        > `--filter` speaks the trace explorer's language, and a target supports the fields the LangWatchQL trace view can answer: `traceId`, `traceName`, `service`, `origin`, `user`, `customer`, `conversation`, `scenarioRun`, `topic`, `subtopic`, `label`, `model`, `status:error`, `cost`, `duration`, `tokens`, `promptTokens`, `completionTokens`, `tokensPerSecond`, `ttft`, `ttlt`, `spans`, `tokensEstimated`,...
  0.69  docs/wall-of-prose  Rule 7: structure for scanning, not reading
        > Anything that reaches outside the trace row is refused by name: span fields (`spanId`, `spanName`, `spanType`, `spanStatus`, `rootSpanType`, `span.attribute.<key>`, `containsAi`), evaluation fields (`eval`, `evaluator*`, `guardrail`), events and annotations (`event`, `event.attribute.<key>`, `annotation`, `feedback`), scenario-run fields (`scenario`, `scenarioSet`, `scenarioBatch`, `scenarioStatus...

## status  [49 words]
  0.65  docs/wall-of-prose  Rule 7: structure for scanning, not reading
        > Reads one run: where it is, how many rows it found and judged, how many matched in total and per question, what the judge could not answer, and the tokens and price the judging came to.

## list  [35 words]
  clean

## results  [115 words]
! 0.84  docs/artifact-as-subject  Rule 17: give the verbs to the reader
        > `--cursor <cursor>`. The cursor the previous page answered with

## sample  [43 words]
  clean

## cancel  [35 words]
! 0.76  docs/statement-stack  Rule 28: prose, not a stack of statements
        > A run that already finished answers 409.
  0.68  docs/indefinite-event-opener  Rule 26: do not open a sentence with an indefinite event
        > A run that already finished answers 409.
  0.54  docs/artifact-as-subject  Rule 17: give the verbs to the reader

## Output  [50 words]
! 0.88  docs/artifact-as-subject  Rule 17: give the verbs to the reader
        > Every subcommand honours the shared output contract: `-o table|json|agents|yaml`, `--json <fields>` and `--jq <path>`.
  0.64  docs/wall-of-prose  Rule 7: structure for scanning, not reading
        > Every subcommand honours the shared output contract: `-o table|json|agents|yaml`, `--json <fields>` and `--jq <path>`.

cost: 86,023 input tokens over 16 requests = USD 0.0036 (0.042 USD per million)

