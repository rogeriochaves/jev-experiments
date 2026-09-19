# prose-lint

A linter for docs pages and posts that uses TypeSafe Jev as the judge. Every rule from the two writing guides is one entry in `rules/`: the ones a regex catches better (em dashes, banned words, literal phrases) run locally; the shape-based ones (negation reframe, staccato, preamble opener, artifact as subject) are one boolean Jev question each, asked per section. A second Jev request per section asks which sentence is the offending one for every rule that fired.

No dependencies. Node 20 or newer.

## Run

```bash
node lint.mjs docs/features/instant-evals/overview.mdx --rules docs
node lint.mjs post.md --rules writing
node lint.mjs page.mdx --rules both --json
node lint.mjs page.mdx --rules both --section-level 3 --threshold 0.8
```

| Flag | Default | What it does |
|---|---|---|
| `--rules docs\|writing\|both` | `docs` | Which rule file(s) to load. `both` runs the docs rules and the writing rules on every section |
| `--rules landing` | | `rules/landing.json`, the landing-page-writing rules, plus the writing rules, since every writing rule binds on a landing page |
| `--context` | | Give the judge every section above the one it reads as `above`. Rules marked `"context": true` (landing rule 2, understandable from the page above; rule 9, product name defined) only run with it, and are skipped with a note without it. Write the page copy as one file, one `##` per block, in page order |
| `--section-level N` | `2` | Split the file at headings of level N and above. Frontmatter is stripped, MDX components stay in the text |
| `--threshold P` | `0.7` | Exit code 1 when any rule fires at or above P |
| `--min P` | `0.5` | Only report rules at or above P |
| `--locate P` | `0.6` | Ask Jev which sentence for judge rules at or above P; `--no-locate` skips that request |
| `--only a,b` / `--skip a,b` | | Run or skip rule ids |
| `--json` | | Machine output: sections, findings, usage |
| `--concurrency N` | `4` | Sections judged in parallel |

The key comes from `JEV_API_KEY` or `TYPESAFE_API_KEY` in the environment, then from `prose-lint/.env`, `../.env` (the jev-experiments repo), then `~/Projects/langwatch/platform/app/.env`.

## Output

```
overview.mdx  (both rules, 7 sections, threshold 0.7)

## Getting started  [64 words]
! 1.00  docs/release-flag  Rule 11: do not document a feature flag or rollout gate
        > Instant Evals are behind a release flag.
! 0.96  docs/rollout-gate  Rule 11: do not document a rollout gate, describe the feature as available
        > Ask LangWatch to turn them on for your project.
  0.62  docs/paraphrased-artifact  Rule 2: show the real artifact, always

cost: 59,244 input tokens over 13 requests = USD 0.0025 (0.042 USD per million)
```

One block per section. Each line is the probability that the rule is broken in that section, the rule id (`set/id`) and its name, then the sentence Jev picked as the clearest instance. `!` marks findings at or above the threshold; they make the exit code 1. Regex and local rules report probability 1.00 and the first matching sentence, with a hit count when there are several. Findings between `--min` and the threshold are shown as a softer signal and do not fail the run.

Read a finding as "the judge thinks this section breaks rule X with probability P". A 0.9 on a rule with a literal example in the guide (release flag, "rides", em dash) is close to certain. A 0.7 on a shape rule (artifact as subject, filler sentence, statement stack) is worth a look, and the located sentence tells you whether the judge understood the rule the way the guide meant it. The judge sees only the section, so rules about the whole page (title, description, opener) run only on the first section with content and receive the frontmatter.

## Rules

`rules/docs.json` holds the 29 numbered rules from docs-writing-rules plus the "less bs" and changelog rules; `rules/writing.json` holds the voice rules, the banned constructions, the technical-audience rules and the founder word bans from writing-rules. Each entry has an `id`, the rule's `name`, and one of three `kind`s:

- `regex`: `pattern` and `flags`, run per sentence on prose, list items, table cells and headings; code blocks, inline code and paragraphs carrying a `Founder decision` MDX comment are skipped. `documentMax` makes a rule fire only past a count over the whole file (exclamation marks). `target: "headings"` or `"raw"` changes what the pattern runs over.
- `local`: a structural check in `lint.mjs` (`paragraph-words` for the 80-word limit, `one-sentence-paragraph-run` for the barrage, `title-repeated-as-heading`, `title-how-to`).
- `judge`: `instruction` written as a question about `text`, and `yes` / `no` criteria taken from the rule's own examples. `scope: "first-section"` limits it to the page opener. `locate: "heading"` or `"first-sentence"` reports that instead of asking Jev which sentence.

The instruction wording is what the calibration tuned; the criteria matter as much as the question. When a rule misfires, rewrite the instruction with the guide's own example on each side and re-run `calibrate.mjs` before changing anything else. Do not quote sentences from the page you are about to lint as examples in a rule; the judge will match them and the report stops meaning anything.

## Landing pages

`rules/landing.json` holds the twelve checkable rules from landing-page-writing (rule 13 is the process itself). Write the page as markdown, one `##` per block in the order a visitor reads them (hero, each section with its subtitle, each card, the FAQ, the closing band), with buttons as markdown links, and run:

```bash
node lint.mjs page-copy.md --rules landing --context --threshold 0.6
```

`--context` is what makes rule 2 mean anything: each block is judged together with everything above it, the way a first-time visitor meets it.

Label what is not prose so the judge reads the page the way a visitor sees it: `Search box:`, `Counters:`, `Chart:`, `Footnote:`, `Tiles:`, `Note:`, `Form:`, and `Q:` / `A:` for a FAQ. The landing rules exempt product views and footnotes from the rules meant for titles and body copy. A line the founder supplied verbatim ends with `[founder]`: a judge finding located on it prints with an `f` flag and never fails the run (rule 13 of the guide); the word bans still apply to it. `landing.json` also carries an `amend` map that appends a landing-aware clause to two writing rules (keynote-closer, round-number-flex), because a button at the end of a card is the card's action and a counter under a search box is the product's output.

## Cost

Jev charges 0.042 USD per million input tokens and nothing for output. Every judge question costs roughly 100 tokens of question text on top of the section text, and the sentence locator sends the section again with the fired rules as choice questions.

| Page | Sections | Requests | Input tokens | USD |
|---|---|---|---|---|
| instant-evals/overview.mdx (`--rules both`) | 7 | 13 | 59,244 | 0.0025 |
| instant-evals/cli.mdx (`--rules both`) | 10 | 16 | 86,023 | 0.0036 |
| instant-evals/shorthand.mdx (`--rules both`) | 6 | 10 | 52,834 | 0.0022 |

So a docs page costs a quarter to a third of a cent with both rule sets, and about half that with `--rules docs`. The full calibration run over 15 fixtures is 0.006 USD. The request cap is 32k tokens for text and questions together; the tool keeps a 30k budget and splits the questions across requests when a section is long. It never splits the text: a section over the budget is reported as an error, and `--section-level 3` is the way to make sections smaller.

## Calibrate

```bash
node calibrate.mjs          # threshold 0.7
node calibrate.mjs 0.8
```

Runs every fixture in `fixtures/` (`.mdx` with both rule sets, `.md` with the writing rules), compares against `fixtures/expected.json`, and prints precision and recall per rule. `CALIBRATION.md` has the current table and the changes it drove. The raw per-fixture probabilities land in `fixtures/results.json`.
