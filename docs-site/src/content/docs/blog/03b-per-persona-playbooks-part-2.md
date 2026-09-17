---
title: "Using Mizan in your role: per-persona playbooks, part 2 (Genmedia configurator, Brand Lab)"
date: 2026-10-01
authors:
  - ghchinoy
excerpt: >
  The second pair of playbooks: the genmedia configurator embedding an eval-set
  as a calling shape, and the Brand Lab user generating a rubric from a brand
  book and freezing it into a reusable metric.
tags: ["personae", "brand-alignment", "templates"]
draft: true
series: "Build Evals with Mizan"
seriesOrder: 3.2
canonicalUrl: ""
---

[Part one](/mizan/blog/03a-per-persona-playbooks-part-1/) handed the two roles
closest to the work a playbook each: the asset creator running the shortest path
from one eval to a scorecard, and the asset manager curating a set that keeps
every concern on its own line. This part picks up the other two personae, both a
step back from the raw output. The **genmedia configurator** does not grade one
asset by hand; they wire evaluation into an application so every asset it
produces is checked the same way. The **Brand Lab user** does not write criteria
by hand; they hold a brand book and want the criteria drafted from it. Different
jobs, the same machinery, and each role's standard ends up as a reusable Mizan
artifact.

## Genmedia configurator: the eval-set is the calling shape

You build or configure a generative-media application, and you want evaluation
available inside it rather than as a manual step someone runs afterwards. What
you need from Mizan is not a single metric but a named, reusable way to call a
*set* of metrics: point at one thing, get back one scorecard your application
can act on.

That thing is the eval-set manifest. Part one ran eval-sets from the creator's
and manager's side; here the manifest matters as the **calling shape**, the
stable unit your application embeds. It names its members by template id and says
how to aggregate them:

```yaml
spec:
  inputs:
    prompt: prompt
    response: response
  members:
    - metric: quickstart/response-helpfulness
      weight: 2
    - metric: quickstart/response-conciseness
      weight: 1
  aggregation:
    method: weighted-mean
    threshold: 3.0
    gate: false
```

Scope this honestly. Phase 1 is **library-first**: you embed the set by pointing
the CLI or the Go library at a manifest on disk. There is no hosted service and
no network API to call. The calling *shape* is fixed now, and it doubles as the
integration spec for a future API surface; that service is not built, and this
piece does not promise one.

Two properties make the manifest safe to depend on from inside an application.

First, it validates without credentials. Before your application commits to a
set, `pack validate` checks the manifest structure and every member with no
Vertex call, so it runs in CI as a gate:

```console
$ mizan pack validate docs/examples/evalset-quickstart
evalsets/answer-quality-badmember.yaml:
  [warn ] spec.members[0].metric "quickstart/does-not-exist" resolves to no template in this tree (ok if it lives in another pack)

0 error(s), 1 warning(s)
```

The one warning is deliberate: that example manifest references a member that
does not exist in the tree, and the validator flags it rather than failing,
because the member could live in another pack. A structural error, an empty
member list, or a malformed id would exit non-zero and block the change.

Second, running the set returns one scorecard, not a pile of loose scores.
Import the member templates once, then run the set:

```console
$ mizan registry import docs/examples/evalset-quickstart
2 inserted, 0 updated, 0 skipped, 0 conflicted, 0 unchanged, 0 forked (source: docs/examples/evalset-quickstart)
  inserted: quickstart/response-conciseness
  inserted: quickstart/response-helpfulness

$ mizan eval run --set docs/examples/evalset-quickstart/evalsets/answer-quality.yaml \
    --field prompt="What is the capital of France?" \
    --field response="The capital of France is Paris, a major European city on the Seine."
EvalSet: quickstart/answer-quality (v1.0.0)  asset-class: text-answer

MEMBER                           STATUS  WEIGHT  SCORE  NOTE
quickstart/response-helpfulness  ok      2       5.00
quickstart/response-conciseness  ok      1       3.00

Aggregate (weighted-mean over 2 scored): 4.33   threshold: 3   PASSED
```

For an application the table is the wrong surface, so add `--output json` and the
same run returns a structured object: a `Members` array where each entry carries
a flat `Status` and `Score` plus a nested `Result` object holding that member's
`Explanation`, and an `Aggregate` block with the method, the numeric score, the
threshold, and a boolean `Passed`, alongside a top-level `Verdict`. Your
application branches on `Passed`, logs each member's `Result.Explanation`, and
surfaces the aggregate to the user. That is the whole embedding contract: one
manifest in, one parseable verdict out.

Because the members are ordinary metric templates, the whole set travels as a
template pack. You curate the evaluation your application enforces once, ship it
in a pack, and every instance of the application imports and runs the same
standard. The [eval-set runner section of the user guide](/mizan/guides/user-guide/)
documents the manifest fields, the partial-failure behavior, and the gate exit
code in full.

## Brand Lab: draft the rubric from the brand book

You are a Brand Lab user. You hold a brand book, and you want evaluations
generated from it rather than authored by hand. Writing rubric criteria from
scratch for every brand rule is exactly the work you are trying to avoid.

The shipped piece of this is adaptive rubric generation: Gemini drafts rubric
criteria from a sample prompt, and you review and freeze them. This is the
suggest-first shape the Brand Lab journey asks for. The model proposes criteria
from your source material, and you decide what to keep. There are two entry
points, one for each moment you reach for it.

The first is the authoring aid. Feed `mizan rubric generate` a sample prompt that
encodes the brand book, and it drafts criteria for review, writing a draft YAML
and touching nothing in your registry:

```console
$ mizan rubric generate \
    --sample "Write a one-line product announcement that follows our brand book: warm and concise, never salesy, name the product exactly once, no superlatives, no competitor comparisons, plain language over jargon." \
    --id brand-lab/announcement-voice \
    --name "Announcement brand voice" \
    --recipe general_quality_v1 \
    --out brand-lab-draft.yaml
GROUP            CRITERION                                                               TYPE                                                   IMPORTANCE
general_quality  The response is in English.                                             LANGUAGE:PRIMARY_RESPONSE_LANGUAGE                     HIGH
general_quality  The response is a single line of text.                                  FORMAT_REQUIREMENT:SINGLE_LINE                         HIGH
general_quality  The response functions as a product announcement.                       CONTENT_REQUIREMENT:PURPOSE:PRODUCT_ANNOUNCEMENT       HIGH
general_quality  The announcement has a warm tone.                                       TONE_REQUIREMENT:WARM                                  MEDIUM
general_quality  The announcement is concise.                                            STYLE_REQUIREMENT:CONCISE                              MEDIUM
general_quality  The announcement avoids salesy language.                                TONE_REQUIREMENT:NON_SALESY                            HIGH
general_quality  The announcement mentions a product name exactly once.                  CONTENT_REQUIREMENT:PRODUCT_NAME_MENTION:EXACTLY_ONCE  HIGH
general_quality  The announcement contains no superlative adjectives or adverbs.         STYLE_REQUIREMENT:NO_SUPERLATIVES                      HIGH
general_quality  The announcement avoids direct or indirect comparisons to competitors.  CONTENT_REQUIREMENT:NO_COMPETITOR_COMPARISONS          HIGH
general_quality  The announcement uses plain language and avoids jargon.                 STYLE_REQUIREMENT:PLAIN_LANGUAGE_NO_JARGON             MEDIUM
```

Read the criteria: the brand book decomposed into checkable statements, each
tagged with a type and an importance. The draft is a file, not a registry entry.
You review and edit it, then wrap it in a pack and import it as an ordinary
template. From that moment it is a static, reproducible rubric, not a fresh
generation on every run.

The second entry point is for when you have an asset in front of you and want
feedback now. `mizan eval adaptive` generates the rubric from the prompt and
scores the response against it in one step, and `--save-as` freezes the rubric it
actually used into the registry so you do not lose it:

```console
$ mizan eval adaptive \
    --prompt "Write a one-line product announcement that follows our brand book: warm and concise, never salesy, name the product exactly once, no superlatives, no competitor comparisons, plain language over jargon." \
    --response "Meet Aria, the smart home hub that makes your mornings a little easier." \
    --recipe general_quality_v1 \
    --save-as brand-lab/announcement-voice
Score:        10
Explanation:  All ten criteria from the rubric were satisfied, as the announcement is in English, a single line, functions as an announcement, is warm and concise, avoids salesy language, names the product once, contains no superlatives or competitor comparisons, and uses plain language.
```

The on-brand line scores full marks. The value of `--save-as` shows up on the
next run: the rubric is now frozen as `brand-lab/announcement-voice`, an ordinary
rubric metric you rerun with `mizan eval run` against any candidate, grading each
one against the same criteria. Hand it an off-brand line, and the same frozen
template catches every violation:

```console
$ mizan eval run --metric brand-lab/announcement-voice \
    --field prompt="Write a one-line product announcement that follows our brand book: warm and concise, never salesy, name the product exactly once, no superlatives, no competitor comparisons, plain language over jargon." \
    --field response="Introducing Aria, the world's #1 smartest home hub, way better than anything else, buy now!"
Score:        5
Explanation:  The response is in English, a single line, functions as a product announcement, names the product once, and uses plain language. However, it fails on being warm and concise, contains salesy language and a call to action, uses a superlative ('#1 smartest'), and makes a comparison ('way better than anything else'), missing 5 out of 10 criteria.
```

Same template, same criteria, a different asset, and the score drops from 10 to 5
with a reason that names the superlative, the competitor comparison, and the
sales pitch. That is the Brand Lab standard encoded as a Mizan metric: generated
once from the brand book, frozen, and reusable. Curate a few of these and the
brand book itself travels as importable templates. The [adaptive rubrics section
of the user guide](/mizan/guides/user-guide/) covers the recipes, `--save-as`,
and the generation provenance Mizan records. Keep one caveat in view: generation
is a drafting aid, so review the criteria before you freeze them, exactly as you
would review any generated draft.

## Next

That closes the per-persona playbooks. Across both parts the four roles ran the
same machinery and differed only in what they wrote down: the creator ran one
eval and then a set, the manager curated a set that keeps concerns apart, the
configurator embedded a set as a calling shape, and the Brand Lab user generated
a rubric from a brand book and froze it. Each standard ended up as the same kind
of artifact, a metric template or an eval-set, and each of those travels as a
template pack. Piece four is about that last step: authoring a pack, validating
it, and sharing it so other people import your standard without touching the
core.

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
commands run as written against your project, or that the two playbooks fit your
role. A clean style score sits alongside the live-command checks and human review
that catch those things; it does not replace them. Read the scorecard as a
repeatable check that catches the obvious problems, not as a measurement of
whether the piece is right.

(Any scorecard numbers shown in this series are manual review estimates unless
labeled as measured; the automated readability tooling was unavailable at the
time of writing.)
