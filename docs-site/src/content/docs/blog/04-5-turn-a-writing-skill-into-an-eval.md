---
title: "Turn a writing skill into an eval: grading this series with Mizan"
date: 2026-10-01
authors:
  - ghchinoy
excerpt: >
  The capstone closes the loop: take the editorial standard behind these posts
  and encode it as a Mizan rubric that grades the series itself.
tags: ["evals", "rubrics", "adaptive-rubrics"]
draft: true
series: "Build Evals with Mizan"
seriesOrder: 4.5
canonicalUrl: ""
---

Every hands-on piece in this series closes with a "graded by Mizan" sidebar. This
piece is about that sidebar. The
[previous piece](/mizan/blog/04-contribute-a-template-pack/) showed that a metric
template is data you can author, validate, and share without touching the core.
Here we point that machinery back at the series and answer a fair question: what
is the rubric in the sidebar, where did it come from, and what does its score
actually mean?

The short version is that the editorial standard these posts are written to is
itself a set of criteria, and criteria are exactly what a Mizan `rubric` metric
holds. So the standard can stop living in a reviewer's head and become a template
you run. This is the strongest form of dogfooding the series has: the product
grades the writing that documents the product.

## The standard is already a rubric

The posts are held to a small, named editorial standard. Five dimensions carry
most of it. **Directness**: state a claim plainly instead of announcing that you
are about to make it. **Rhythm**: vary sentence length instead of marching in a
metronome. **Density**: make every sentence load-bearing, with no cuttable filler.
**Authenticity**: read as someone who actually did the thing. **Trust**: state
facts and let the reader conclude, without hype.

Written out like that, the standard is a list of yes-or-no questions about a
draft. That is a rubric group. Encoding it is one `registry create`, exactly the
shape [piece four](/mizan/blog/04-contribute-a-template-pack/) used for a support
reply:

```sh
mizan registry create --id docs-quality/technical-explanation --kind rubric \
  --name "Technical explanation quality" \
  --description "Grades a technical blog draft on the series editorial standard." \
  --prompt 'Evaluate this technical blog draft against the editorial criteria. Draft: {{response}}' \
  --input 'response:text:true' \
  --rubric-group 'editorial=The draft states its claims plainly instead of announcing them.;Sentence lengths vary instead of marching in a metronome.;Every sentence is load-bearing, with no cuttable filler.;It reads as written by someone who did the thing.;It states facts and lets the reader conclude, without hype.' \
  --tag docs-quality --tag editorial \
  --license Apache-2.0 --author "Build Evals with Mizan" \
  --model gemini-2.5-flash
```

That is the whole metric. It is a prompt, one declared input, and five named
criteria. Nothing in it is executable, so anyone can read it, diff it, and reason
about what it scores before they run it.

## Let the model draft the criteria

Writing five criteria by hand is fine when you already know the standard. When you
are deriving a rubric from a longer guidance document, Mizan has an authoring aid:
`rubric generate` asks Gemini to draft candidate criteria aligned to a
representative prompt, then writes a draft YAML for you to review. It touches the
registry for nothing; it only proposes. You keep your non-negotiables by unioning
them in with `--add-criterion`:

```sh
mizan rubric generate \
  --sample 'A technical blog post that explains a Mizan feature to a Google Cloud developer, in direct, dense, plain-spoken prose.' \
  --id docs-quality/technical-explanation-draft \
  --out draft-rubric.yaml \
  --recipe text_quality_v1 --group-name editorial \
  --add-criterion 'It reads as written by someone who did the thing.' \
  --add-criterion 'It states facts and lets the reader conclude, without hype.'
```

When I ran that, Gemini drafted a dozen candidate criteria (directness, density,
plain-spokenness, relevance, accuracy, and so on) and printed them in a table
whose `ORIGIN` column marks each row `adaptive-generated`, followed by the two
`--add-criterion` rows marked `hand-authored`. That separation is the point: the
draft keeps the model's suggestions apart from your non-negotiables, and it records
a `rubricProvenance` block noting the generator model, the recipe, and a hash of
the sample prompt, so a reviewer can see later what came from the model and what
came from you. Adaptive generation is an authoring aid, not a source of truth:
Gemini drafts, you review and edit, and the moment you freeze the criteria the
template is an ordinary, reproducible static rubric. If you would rather generate
and freeze in one step, `eval adaptive` does the whole thing and, with `--save-as`,
writes the result straight into the registry:

```sh
mizan eval adaptive \
  --prompt 'A technical blog paragraph that explains a Mizan concept, in direct, dense prose.' \
  --response '<a paragraph to score>' \
  --recipe text_quality_v1 --group-name editorial \
  --save-as docs-quality/technical-explanation-adaptive
```

That single command makes one generation call and one eval call, prints the score
and a `mizan: froze generated rubric as docs-quality/technical-explanation-adaptive`
line, and leaves a reusable template behind. The
[adaptive rubrics section](/mizan/guides/user-guide/#adaptive-rubrics-authoring-aid)
of the user guide covers the recipes and the provenance record in full, and the
shipped [rubric-generate-from-brand-book](/mizan/guides/agent-skills/) skill drives
exactly this loop when you ask an agent to derive a rubric from a guidance doc.

## Grade a real draft

With the hand-authored metric frozen, run it against a real draft. Adding
`--rubric-detail` returns a verdict per criterion instead of one blended number.
The draft below is the actual opening of [piece one](/mizan/blog/01-why-evals/):

> You shipped a generated image, a marketing line, or a model response, and you
> asked yourself whether it was good enough. You answered by eye, once, and moved
> on. The next person on your team answered differently. Neither of you wrote down
> what "good enough" meant, so neither answer travels past the moment you made it.

Scoring it produced the following, pasted exactly as the CLI printed it:

```
$ mizan eval run --metric docs-quality/technical-explanation \
    --field response="You shipped a generated image, a marketing line, ..." --rubric-detail
mizan: autorater → project=ghchinoy-genai-sa (src=env-file) location=global (src=global-path) model=gemini-2.5-flash (path=genai)
mizan: samplingCount=4 is ignored on the genai/global structured path (it has no sampling-count concept); samplingCount applies only to the native evaluation path
mizan: flipEnabled is ignored on the genai/global structured path (it has no flip concept); flipEnabled applies only to the native pairwise evaluation path
Score:        5
Explanation:  This is an exceptionally strong opening for a technical blog post. It immediately engages the reader by presenting a highly relatable and common problem in a clear, concise, and authentic voice. The language is direct, every sentence serves a purpose, and it effectively sets the stage for further discussion without any unnecessary fluff or hype. The slight similarity in length of two sentences is a minor point in an otherwise excellent draft.
Per-criterion:
GROUP      CRITERION                                                        SCORE  RATIONALE
editorial  The draft states its claims plainly instead of announcing them.  5      The draft immediately presents a common scenario and problem without any introductory announcements or preambles.
editorial  Sentence lengths vary instead of marching in a metronome.        4      There is some variation in sentence length (23, 9, 9, 26 words), though two consecutive sentences are quite short and similar, slightly impacting flow.
editorial  Every sentence is load-bearing, with no cuttable filler.         5      Every sentence directly contributes to establishing the problem and its implications; there is no discernible filler or redundant phrasing.
editorial  It reads as written by someone who did the thing.                5      The scenario described is highly relatable and specific to real-world challenges in evaluating outputs, suggesting direct experience from the author.
editorial  It states facts and lets the reader conclude, without hype.      5      The draft presents a common problem in a straightforward, factual manner without any hype or exaggerated language, allowing the reader to infer the significance.
```

Two things about that run are worth noticing. The autorater line reports
`location=global` and `path=genai`: `--rubric-detail` uses the structured
generative path rather than the native sampling path, which is why the run also
notes that `samplingCount` and `flipEnabled` do not apply here. And the
per-criterion table is the reason to reach for the flag at all. A single "5" tells
you little. The Rhythm row scoring a 4, with the judge counting the sentence
lengths (23, 9, 9, 26 words) and flagging the two short middle sentences, is the
kind of specific, actionable note that makes the score useful as revision feedback
rather than a grade you file away.

Because Mizan persists every run by default, that feedback compounds. Score a
draft, take the Rhythm note, rewrite the two short sentences, and rerun the same
metric. `mizan results list` and `mizan results show <run-id>` let you look back at
what each revision scored and why, so you can see a dimension climb as you work
rather than guessing whether an edit helped. The standard is the same each time,
which is the whole reason to write it down: the check is repeatable, so the
comparison across drafts is honest.

## Ship it so others can run it

A rubric that only grades this series is a curiosity. The value is that anyone can
run the same standard on their own writing, and that is the contribution loop from
[piece four](/mizan/blog/04-contribute-a-template-pack/), applied to a rubric
instead of a support metric. Scaffold a pack, add the metric, and validate it with
no credentials:

```sh
mizan pack init packs/docs-quality --name docs-quality
mizan pack add packs/docs-quality --from docs-quality/technical-explanation
mizan pack validate packs/docs-quality
```

```
initialized pack "packs/docs-quality" (namespace "docs-quality")
1 written, 0 skipped (dest: packs/docs-quality)
  written: docs-quality/technical-explanation -> templates/technical-explanation.yaml
OK: no defects found.

0 error(s), 0 warning(s)
```

The validated pack is a directory of data you commit and open as a pull request,
the same way the [google-brand pack](https://github.com/ghchinoy/mizan-templates/tree/main/packs/google-brand)
travels. On the consuming side, importing it is one command, previewable with
`--dry-run` first:

```sh
mizan registry import packs/docs-quality
```

```
1 inserted, 0 updated, 0 skipped, 0 conflicted, 0 unchanged, 0 forked (source: packs/docs-quality)
  inserted: docs-quality/technical-explanation
```

Now the reader has the exact rubric the series is graded by, ready to point at
their own drafts. The editorial standard stopped being tribal knowledge and became
an artifact that travels with the work.

## What the score does not measure

Here is the honest part, and it is not a footnote. This rubric grades surface style
and clarity. It says nothing about whether the substance is correct.

A draft can score a clean five while claiming a command that does not exist, citing
a flag that was renamed, or building an argument that does not hold. The judge is
reading prose, not running the CLI and not checking the reasoning. The rubric would
happily award full marks to a fluent, confident, completely wrong paragraph. Style
conformity is necessary, not sufficient: it is real signal about readability and it
catches the obvious problems, but it is silent on the question that matters most,
which is whether the piece is right.

So the score sits alongside the checks that do test substance, and does not replace
them. Every command in this series was run live and its output pasted verbatim.
Every capability claim was verified against the shipped binary. A human read each
draft for whether the argument earns its conclusion. The rubric is one repeatable
check in that stack, not the top of it. Read a high score as "this reads cleanly,"
never as "this is correct." Overselling a rubric number as objective quality is the
one failure mode this piece exists to warn against.

## Read next

You have now seen the contribution loop turned on the series itself: a writing
standard encoded as five criteria, drafted with an authoring aid or hand-authored,
run live with a verdict per dimension, and shipped as a pack a reader can import.
That is as far as you can go without touching Mizan's code. The
[final piece](/mizan/blog/05-developing-for-mizan/) crosses that line: it is the
orientation for contributors who need to add a new metric kind or behavior to the
core, and shows the seams where that work attaches.

---

### Sidebar: graded by Mizan

Each hands-on piece in this series closes by grading itself with Mizan. This piece
is the one that explains the sidebar. The draft you just read was scored against
the `docs-quality/technical-explanation` rubric it describes, built with shipped
commands:

```sh
mizan registry create --id docs-quality/technical-explanation --kind rubric \
  --name "Technical explanation quality" \
  --rubric-group "editorial=The draft states its claims plainly instead of announcing them.;\
Sentence lengths vary instead of marching in a metronome.;\
Every sentence is load-bearing, with no cuttable filler.;\
It reads as written by someone who did the thing.;\
It states facts and lets the reader conclude, without hype." \
  --model gemini-2.5-flash

mizan eval run --metric docs-quality/technical-explanation \
  --field response="<draft of this article>" --rubric-detail
```

Be clear about what that score does and does not mean. The rubric grades surface
style and clarity. It does not verify that the substance is correct, that the
commands run as written, or that the honesty caveat above holds. A clean style
score sits alongside the live-command checks and human review that catch those
things; it does not replace them. Read the scorecard as a repeatable check that
catches the obvious problems, not as a measurement of whether the piece is right.

(Any scorecard numbers shown in this series are manual review estimates unless
labeled as measured; the per-criterion table above is a real, verbatim Vertex AI
run.)
