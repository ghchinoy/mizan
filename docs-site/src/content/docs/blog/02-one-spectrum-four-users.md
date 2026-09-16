---
title: "One spectrum, four users: from brand alignment to technical metrics"
date: 2026-09-15
authors:
  - ghchinoy
excerpt: >
  Four Mizan users sit on one spectrum, from "is this on-brand?" to "is this
  grounded and accurate?" The same machinery answers both. Only the criteria
  change.
tags: ["evals", "llm-as-a-judge", "personae", "brand-alignment"]
draft: false
series: "Build Evals with Mizan"
seriesOrder: 2
canonicalUrl: ""
---

The [first piece](/mizan/blog/01-why-evals/) took one judgment you used to make
by eye and turned it into a check you can rerun and hand to a colleague. That
piece stayed narrow by design: one pointwise metric, one response, one score.
This piece widens the frame to the people doing the judging, because who you are
changes what you ask a judge to check, and it is easy to assume that a different
question needs a different tool.

It does not. The question a brand reviewer asks and the question a platform
engineer asks land on the same command. The criteria they write down separate
them, not the machinery that runs them.

## Four users, one spectrum

Mizan has four canonical users. Line them up by the kind of question each one
brings to a generated asset, and they form a spectrum rather than four separate
worlds.

At one end sits the **Brand Lab user**. They hold a brand book and a set of
assets, and they want evaluations that check alignment: does this ad carry the
approved voice, show the logo where the guideline requires, stay off competitor
colors. Their criteria are qualitative and human-authored, and a good answer
reads like a careful reviewer's note.

At the other end sit the **asset manager** and the **genmedia application
creator or configurator**. The asset manager curates groups of evaluations for a
class of assets so that creators can reuse them, and wants each concern reported
as its own verdict rather than blended into one number. The genmedia configurator
wires evaluation into a generative-media application and wants a result the
application can parse and act on. Both live at the technical, metric end: they
want structured, gateable output that a pipeline can read.

The **asset creator** spans the middle. They run an eval against an asset to
check it against guidance, and they run sets of evals as their work matures. Some
days their question is a brand question; other days it is an accuracy question.
They move along the spectrum depending on the asset in front of them.

Read left to right, the spectrum runs from "is this on-brand?" to "is this
grounded and accurate?" The claim of this piece is that a single toolset covers
the whole line.

## The tool is fixed; the criteria move

Recall the four metric kinds from piece one. A **pointwise** metric scores one
response against one question. A **rubric** metric scores several named criteria
at once. A **custom_schema** metric returns typed fields you define. A
**pairwise** metric picks the stronger of two candidates.

None of those kinds is a "brand" tool or a "technical" tool. A pointwise metric
does not know whether the number it produces measures brand voice or factual
grounding. You decide that when you write the prompt and the criteria. The
workflow is identical at both ends of the spectrum: you author a template, you
hand it a response, and you read back a verdict with the judge's reasoning. The
brand reviewer and the platform engineer follow the same three steps. They differ
only in what they put in the template and what shape of answer they ask for.

The two worked examples below prove it. The first sits at the brand end and uses
a shared template pack. The second sits at the technical end and returns a typed
verdict. Both are the same author-and-run loop.

## Brand end: import a pack and score alignment

The Brand Lab user rarely writes brand criteria from scratch. Brand rules are
shared property, so they travel as a template pack: a versioned folder of metric
templates that anyone can import. The community pack repository ships a worked
example, the [`google-brand` pack](https://github.com/ghchinoy/mizan-templates/tree/main/packs/google-brand),
which contains a `video-brand-alignment` template.

Import it into your local registry. With no argument, `registry import` pulls
from the configured default source, `github.com/ghchinoy/mizan-templates`:

```sh
mizan registry import --namespace google-brand
```

```
1 inserted, 0 updated, 0 skipped, 0 conflicted, 0 unchanged, 0 forked (source: github.com/ghchinoy/mizan-templates)
```

The template is now a first-class metric in your registry, addressable by its
stable id:

```sh
mizan registry get google-brand/video-brand-alignment
```

Under the hood it is a pointwise metric: it scores one asset on a 1-to-5 scale
against a supplied brand guideline. Its prompt asks the judge to weigh tone,
visual identity, and messaging consistency, and it declares two inputs, the
`response` (the video under evaluation) and a `brand_guideline` (the reference
text). Run it by supplying both fields. Multimodal assets are passed as `gs://`
URIs, because the native Eval Service reads them from Cloud Storage rather than
inline:

```sh
mizan eval run --metric google-brand/video-brand-alignment \
  --field response=gs://example-bucket/ads/spring-sale-15s.mp4 \
  --field brand_guideline="Our brand voice is warm, concise, and never salesy. \
Always show the logo in the final 3 seconds. Primary color is #1A73E8; avoid competitor colors."
```

The result comes back as a pointwise score in the declared 1-to-5 range with a
free-text rationale that names what aligned and what did not. That is the whole
brand end: someone authored the criteria once, shared them as a pack, and now any
reviewer runs the same standard against their own footage and gets a comparable
answer. Nobody re-litigated what "on-brand" means for this run.

## Technical end: a typed verdict a pipeline can gate on

The asset manager and the genmedia configurator want the same author-and-run
loop, but their downstream needs a machine to read the result, not a person. When
the honest question is "does this response stay grounded and inside policy," the
`custom_schema` kind returns a typed JSON verdict instead of prose.

The repository ships an example schema,
[`docs/examples/compliance-schema.json`](https://github.com/ghchinoy/mizan/blob/main/docs/examples/compliance-schema.json),
that returns four fields: an `overall_score`, a boolean `compliant`, an array of
`flagged_issues`, and an `explanation`. Author a metric against it and run it:

```sh
mizan registry create --id demo/custom-compliance --name "Compliance Check" --kind custom_schema \
  --prompt "Check if this response follows the policy: no medical advice. Response: {{response}}" \
  --response-schema-file docs/examples/compliance-schema.json

mizan eval run --metric demo/custom-compliance --field response="Drink plenty of water and rest."
```

```
Score:                         (none)
Explanation:
CustomOutput[compliant]:       true
CustomOutput[explanation]:     General wellness suggestions, not specific medical advice; complies with the policy.
CustomOutput[flagged_issues]:  []
CustomOutput[overall_score]:   9
```

Read that output next to the brand run. The brand reviewer got a score and a
sentence for a human to weigh. Here the genmedia configurator gets a `compliant`
boolean their application can branch on, an `overall_score` they can threshold,
and a `flagged_issues` array they can log or surface, all as typed data returned
with `--output json`. Same command, `mizan eval run`; the difference is that the
criteria live in a schema the judge fills in rather than in a scale it reasons to.

The grounding and accuracy question follows the identical shape. Swap the prompt
for one that asks whether every claim in the response is supported by a supplied
source, add a `source` field to feed the reference text, and the same
`custom_schema` template returns a typed verdict on factual grounding. You did
not change tools to move from a compliance concern to a grounding concern. You
changed the sentence in the prompt.

## Where you sit changes what you write, not what you run

Put the two examples side by side and the spectrum collapses into one workflow.
The Brand Lab user imported a pack of qualitative criteria and read a scored,
explained verdict. The genmedia configurator authored a schema of structured
criteria and read a typed, parseable verdict. The asset creator in the middle
does both on different days. The asset manager's job is to curate these templates
into reusable sets and report each concern independently, which is the same loop
run over a group; Mizan ships a path-based eval-set runner (`eval run --set`) for
exactly that, and the next piece walks it end to end per role.

The [LLM-as-a-Judge scenarios guide](/mizan/guides/llm-as-judge-scenarios/) is
the map from "here is the question I am asking" to "here is the command that
answers it," across all four kinds and both ends of this spectrum; the
[custom_schema scenario](/mizan/guides/llm-as-judge-scenarios/#scenario-5-structured--compliance-verdicts-custom_schema)
covers the typed-verdict path in full, and the
[user guide](/mizan/guides/user-guide/) documents importing and inspecting packs.
Batch evaluation and the desktop app are not built yet, and the docs say so
rather than implying otherwise.

The lesson to carry into the per-persona playbooks in piece three: pick your
place on the spectrum, and the only decision left is what to write down. The
machinery is already the same.

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
style and clarity. It does not verify that the substance is correct, that the two
worked examples run as written against your project, or that the spectrum
argument holds. A clean style score sits alongside the live-command checks and
human review that catch those things; it does not replace them. Read the
scorecard as a repeatable check that catches the obvious problems, not as a
measurement of whether the piece is right.

(Any scorecard numbers shown in this series are manual review estimates unless
labeled as measured; the automated readability tooling was unavailable at the
time of writing.)
