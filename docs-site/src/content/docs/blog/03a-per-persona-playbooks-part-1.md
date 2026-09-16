---
title: "Using Mizan in your role: per-persona playbooks, part 1 (Asset creator, Asset manager)"
date: 2026-09-16
authors:
  - ghchinoy
excerpt: >
  Two roles, two playbooks. How an asset creator finds the shortest path from one
  eval to a scorecard, and how an asset manager curates a set that reports each
  concern on its own.
tags: ["personae", "eval-sets", "getting-started"]
draft: false
series: "Build Evals with Mizan"
seriesOrder: 3.1
canonicalUrl: ""
---

The [first piece](/mizan/blog/01-why-evals/) turned one by-eye judgment into a
check you can rerun. The [second](/mizan/blog/02-one-spectrum-four-users/) placed
Mizan's four users on a single spectrum and showed that the same machinery serves
all of them. This piece gets practical: it hands two of those users a playbook for
their actual job. Pick your role, and here is the shortest path to value.

Mizan has four canonical personae. This part covers the two closest to the work
itself. The **asset creator** produces candidate output; the **asset manager**
decides what a class of assets must clear before it ships. Part 2 picks up the
other two.

## Asset creator: one eval, then a set, then a scorecard

You make assets and you want to know they align with guidance before you send
them on. Your path has three steps, each shipped today.

**Run one eval.** Piece one already walked this: author a metric, hand it a
response, read back a score and a reason. That is the atom, and it covers the
day you have exactly one question about one asset.

**Run a set.** Your work rarely raises one question. A tagline needs to be
concise *and* on-voice *and* free of competitor names. Rather than run three
commands and reconcile three outputs by hand, bundle the metrics into an
**eval-set** and score the asset against all of them in a single run. In Phase 1
the runner is path-based: `eval run --set` takes a filesystem path to an EvalSet
manifest and scores its members together. The set's members reference metric
template ids, so import their templates first, then run the set:

```console
$ mizan registry import docs/examples/evalset-quickstart
2 inserted, 0 updated, 0 skipped, 0 conflicted, 0 unchanged, 0 forked (source: docs/examples/evalset-quickstart)
  inserted: quickstart/response-conciseness
  inserted: quickstart/response-helpfulness

$ mizan eval run --set docs/examples/evalset-quickstart/evalsets/answer-quality.yaml \
    --field prompt="What is the capital of France?" \
    --field response="The capital of France is Paris, a major European city on the Seine."
```

**Read the scorecard.** The run prints one table, a row per member, then an
aggregate line with the method, the count of scored members, the threshold, and
the always-computed verdict:

```console
EvalSet: quickstart/answer-quality (v1.0.0)  asset-class: text-answer

MEMBER                           STATUS  WEIGHT  SCORE  NOTE
quickstart/response-helpfulness  ok      2       5.00
quickstart/response-conciseness  ok      1       4.00

Aggregate (weighted-mean over 2 scored): 4.67   threshold: 3   PASSED
```

Read the member rows first, since they tell you *which* criteria the asset met,
then read the aggregate line for the single go/no-go. A member that can't resolve is
reported and the run continues, so a broken template never silently drops a
concern; add `--fail-fast` when you would rather stop at the first problem. Every
run persists by default, so `mizan results list` and `mizan results show
<run-id>` let you look back at what you scored and why. The
[eval-set runner walkthrough](/mizan/guides/user-guide/) in the user guide covers
the manifest fields, `--output json`, and partial failures in full.

## Asset manager: curate a set that keeps concerns apart

You do not run assets one at a time. You decide what a *class* of assets must
clear, curate the checks once, and hand creators something they can reuse. The
eval-set is that artifact. Its most valuable property for you is that it reports
each concern on its own line rather than blending everything into one opaque
number.

Say a campaign asset has to satisfy five separate concerns: it aligns with the
prompt, adheres to brand, follows campaign guidelines, meets the advertising
channel's requirements, and passes safety. Curate each as its own member so each
gets its own verdict, and aggregate with `min` so the set only passes when the
weakest concern does:

```yaml
apiVersion: mizan.dev/v1alpha1
kind: EvalSet
metadata:
  id: campaign/spring-launch-readiness
  version: 1.0.0
  assetClass: display-ad
spec:
  inputs:
    prompt: prompt
    response: response
  members:
    - metric: campaign/prompt-alignment
    - metric: brand/adherence
    - metric: campaign/guidelines
    - metric: channel/display-requirements
    - metric: safety/content
  aggregation:
    method: min            # mean | weighted-mean | min
    threshold: 3.0
    gate: true             # non-zero exit only when the verdict is FAILED
```

Run it and the scorecard names each concern independently, so a stakeholder can
see exactly which dimension an asset fails, not just that it failed somewhere:

```console
MEMBER                        STATUS  WEIGHT  SCORE  NOTE
campaign/prompt-alignment     ok      1       5.00
brand/adherence               ok      1       4.00
channel/display-requirements  ok      1       4.00
campaign/guidelines           ok      1       2.00
safety/content                ok      1       5.00

Aggregate (min over 5 scored): 2.00   threshold: 3   FAILED
```

Two things make this a manager's tool rather than a creator's. First, because
each member persists as its own result, you can track a *single* concern's trend
over time. `mizan results list --metric campaign/guidelines --since 2026-10-01`
shows only that dimension, run after run, so a slipping category is visible
before it becomes a pattern. Second, `gate: true` makes a failing verdict exit
non-zero, which is exactly what wires the set into a CI check that blocks a
release. The members themselves are just metric templates, so the industry
presets you curate travel as a template pack that creators import once and reuse,
the contribution loop piece four is built around. The
[testing guide](/mizan/guides/testing-guide/) has the eval-set recipe end to end,
including the gate exit-code behavior.

Keep the runner's scope in mind: Phase 1 is a **library-first**, path-based
surface. There is no hosted service or API to call. You point the CLI (or the Go
library) at a manifest on disk. Store-backed resolution by set id is a documented
fast-follow, not something to build a pipeline around yet.

## Next

That is the operational end of the spectrum: the creator running the shortest
path from one eval to a scorecard, and the manager curating a set that keeps
every concern on its own line. Part 2 picks up the other two personae: the
genmedia application configurator embedding an eval-set's calling shape, and the
Brand Lab user auto-generating a rubric from a brand book.

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
