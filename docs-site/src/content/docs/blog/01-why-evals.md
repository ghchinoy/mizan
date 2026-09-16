---
title: "Why evals, and what is an LLM-as-a-judge, really?"
date: 2026-09-15
authors:
  - ghchinoy
excerpt: >
  You already judge generative output by eye. An eval turns that private
  judgment into something explicit, repeatable, and shareable.
tags: ["evals", "llm-as-a-judge", "getting-started"]
draft: false
series: "Build Evals with Mizan"
seriesOrder: 1
canonicalUrl: ""
---

## The judgment you already make

You shipped a generated image, a marketing line, or a model response, and you
asked yourself whether it was good enough. You answered by eye, once, and moved
on. The next person on your team answered differently. Neither of you wrote down
what "good enough" meant, so neither answer travels past the moment you made it.

That private judgment is already an evaluation. You held criteria in your head,
you applied them to one artifact, and you reached a verdict. The parts missing
were a written-down standard, a way to run it again next week, and a way to hand
it to a colleague and get the same answer back.

An eval supplies those missing parts. You state the criteria a response has to
meet, you hand a response and those criteria to a judge, and you get back a score
and an explanation you can store, rerun, and share. Mizan runs that judge on
Google's Vertex AI Gen AI Evaluation Service, across text, images, audio, video,
and music.

## The judge is a model

Automated scoring used to lean on string matching or reference answers. That
works when there is one correct output. Generative work rarely has one correct
output: a tagline can be strong in three different directions, and an image can
satisfy a brief without matching any reference pixel for pixel.

An LLM-as-a-judge scores the way a careful reviewer would. You give a model the
artifact and a written standard, and the model returns a judgment against that
standard, along with its reasoning. The model doing the judging is the same class
of model that produced the work, pointed at a different task: assessment instead
of generation.

Three properties make this worth adopting, and they are the same three you were
missing when you judged by eye. The judgment is explicit, because the standard
lives in a template you wrote rather than in your head. It is repeatable, because
running the same template over the same input gives you a comparable result each
time, which is what lets you track a draft improving across revisions. And it is
shareable: the template is a file, so you can commit it, hand it to a teammate,
or import one that someone else authored, and the standard travels with the
work instead of staying in one reviewer's head.

## Four kinds of judgment

Mizan gives you four metric kinds, and they differ by the shape of the question
you are asking.

A **pointwise** metric answers a single question about a single response: how
concise is this, how well does it follow the prompt, on a scale you define.
Reach for it when you want one number and a reason.

A **rubric** metric checks several named criteria at once and reports against
each. A brand rubric might ask, in one run, whether the copy uses the approved
voice, avoids competitor names, and carries the required legal line.

Two more cover the remaining shapes. A **custom_schema** metric returns
structured fields you specify, so a compliance check comes back as typed data
you can act on rather than prose you have to parse. A **pairwise** metric sets
two candidates side by side and picks the stronger one, for the times the honest
question is "which of these two."

You will meet all four across this series. The rest of this piece runs the first
one.

## Run one eval in ten minutes

You need a Google Cloud project with the Vertex AI API enabled and Application
Default Credentials on your machine. The
[user guide](/mizan/guides/user-guide/#install)
covers install and configuration in full; the short version follows.

Install the CLI (the install is cgo-free, so no C toolchain is required):

```sh
go install github.com/ghchinoy/mizan/cmd/mizan@v0.1.0
```

Point Mizan at your project:

```sh
mizan config set project-id <your-project-id>
mizan config show
```

Create a pointwise metric that scores conciseness, then run it against a sample
response:

```sh
mizan registry create --id demo/conciseness --name "Conciseness" \
    --description "Scores how concise a response is" \
    --kind pointwise \
    --prompt "Rate how concise this response is from 0 (verbose) to 1 (concise). Response: {{response}}" \
    --model gemini-2.5-flash

mizan eval run --metric demo/conciseness --field response="The cat sat on the mat."
```

The run returns a score and the judge's reasoning:

```
Score:        1
Explanation:  The response 'The cat sat on the mat.' is a very short, direct, and grammatically complete sentence that conveys its meaning with no superfluous words, making it maximally concise.
```

That output holds the whole idea. Your standard ("how concise, 0 to 1") now
lives in a template named `demo/conciseness`. Anyone with the template runs the
same check and gets a comparable score with a stated reason. Mizan persists each
run by default, so `mizan results list` and `mizan results show <run-id>` let you
look back at what you scored and why. Add `--no-store` when you want a run to
leave no record.

Swap the response for your own copy and rerun. Change the prompt to score a
dimension you care about (tone, factual grounding, adherence to a brief) and you
have authored a second metric. The mechanics hold steady as the criteria change.

## Read next

Everything above runs against shipped commands today. The README's
[Works today](https://github.com/ghchinoy/mizan/blob/main/README.md#works-today)
section is the honest inventory of what the CLI does now, and the
[LLM-as-a-Judge scenarios](/mizan/guides/llm-as-judge-scenarios/)
guide maps each kind of question you might ask onto the command that answers it.
Batch evaluation and the desktop app are not built yet, and the docs say so
rather than implying otherwise.

The next piece places all four Mizan personae on one spectrum, from "is this
on-brand?" at one end to "is this grounded and accurate?" at the other, and shows
that the same machinery answers both. For now, you have taken one judgment you
used to make by eye and turned it into a check you can rerun and hand to someone
else.

---

### Sidebar: graded by Mizan

Each hands-on piece in this series closes by grading itself with Mizan. The draft
you just read was scored against a `rubric` metric that encodes the editorial
standard for these posts. You can build the same rubric with shipped commands:

```sh
mizan registry create --id docs-quality/technical-explanation --kind rubric \
  --name "Technical explanation quality" \
  --rubric-group "quality=Is the explanation direct, stating claims not announcing them?;\
Is it dense with no cuttable filler?;Is it accurate and correctly scoped?;\
Does it read as written by someone who did the thing?" \
  --model gemini-2.5-flash

mizan eval run --metric docs-quality/technical-explanation \
  --field response="<draft of this article>" --rubric-detail
```

Be clear about what that score does and does not mean. The rubric grades surface
style and clarity. It does not verify that the substance is correct, that the
commands run as written, or that the argument holds. A clean style score sits
alongside the live-command checks and human review that catch those things; it
does not replace them. Read the scorecard as a repeatable check that catches the
obvious problems, not as a measurement of whether the piece is right.

(Any scorecard numbers shown in this series are manual review estimates unless
labeled as measured; the automated readability tooling was unavailable at the
time of writing.)
