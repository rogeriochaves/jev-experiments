overview.mdx  (both rules, 7 sections, threshold 0.7)

## What an Instant Eval is  [76 words]
! 0.80  docs/comparison-to-other-area  Rule 16: never explain a feature by comparison to another product area
        > An Instant Eval is the third kind: a question you thought of today, answered across six months of production.
  0.61  docs/statement-stack  Rule 28: prose, not a stack of statements
        > Offline evals score a dataset before you ship.
  0.60  docs/heading-indirect-form  Rule 29: headings are the question the reader types
        > ## What an Instant Eval is
  0.60  writing/teach-list-as-list  Teach a list as a list
        > An Instant Eval is the third kind: a question you thought of today, answered across six months of production.
  0.52  docs/vague-boundary  Rule 5: state the limits and the boundaries, exactly

## What it costs  [96 words]
! 0.83  docs/artifact-as-subject  Rule 17: give the verbs to the reader
        > Judging is done by a small classifier that answers typed questions over text, so a run is cheap and fast rather than a model call per row.

## The loop  [102 words]
! 0.70  docs/filler-sentence  What "less bs" means: no honest-but-empty sentences
        > The feature is built around one loop, and the loop is what makes the answers good:
  0.61  docs/artifact-as-subject  Rule 17: give the verbs to the reader
        > A question that reads well to you often reads differently to a judge.
  0.59  writing/narrator-sentence  Narrator sentences

## What one row is  [74 words]
! 0.90  docs/artifact-as-subject  Rule 17: give the verbs to the reader
        > A target settles what gets judged:
  0.61  docs/heading-unanswered  Rule 15: the first sentence answers the heading
        > A target settles what gets judged:

## What a question can ask  [101 words]
! 0.74  docs/heading-indirect-form  Rule 29: headings are the question the reader types
        > ## What a question can ask
  0.61  docs/artifact-as-subject  Rule 17: give the verbs to the reader
        > Asking several questions of the same text costs about what asking one does, because one classification answers them all.
  0.52  docs/heading-unanswered  Rule 15: the first sentence answers the heading

## Where the answers go  [46 words]
  clean

## Getting started  [64 words]
! 1.00  docs/release-flag  Rule 11: do not document a feature flag or rollout gate
        > Instant Evals are behind a release flag.
! 0.97  docs/rollout-gate  Rule 11: do not document a rollout gate, describe the feature as available
        > Ask LangWatch to turn them on for your project.
  0.62  docs/paraphrased-artifact  Rule 2: show the real artifact, always
        > Instant Evals are behind a release flag.
  0.54  docs/artifact-as-subject  Rule 17: give the verbs to the reader

cost: 59,244 input tokens over 13 requests = USD 0.0025 (0.042 USD per million)

