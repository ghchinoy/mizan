---
title: "Contribute a template pack: extend Mizan without touching the core"
date: 2026-10-14
authors:
  - ghchinoy
excerpt: >
  You do not have to fork Mizan to add value to it. This piece walks authoring,
  validating, and sharing a template pack that others can import.
tags: ["templates", "contributing", "eval-sets"]
draft: false
series: "Build Evals with Mizan"
seriesOrder: 4
canonicalUrl: ""
---

The [asset manager playbook](/mizan/blog/03a-per-persona-playbooks-part-1/) ended
on a promise. The checks a manager curates for a class of assets are just metric
templates, so the presets a team settles on can travel as a template pack that
creators import once and reuse. This piece keeps that promise. It walks the whole
loop: author a pack, validate it, and share it so anyone can import it, without
changing a line of Mizan's code.

That last part is the point. A metric template is data, not code. It is a YAML
file that states a prompt, the inputs it expects, and the criteria a judge should
weigh. A pack is a directory of those files plus a small manifest. Contributing
one means writing data and opening a pull request, which is the lowest-barrier
way to extend Mizan. You are not compiling anything, and you are not touching the
engine that runs your template.

## A pack is a directory of data

Start by scaffolding the pack. `mizan pack init` writes the layout for you:

```sh
mizan pack init packs/acme-support --name acme-support
```

```
initialized pack "packs/acme-support" (namespace "acme-support")
```

That creates three things: a `mizan-pack.yaml` manifest, an empty `templates/`
directory, and an empty `evalsets/` directory. The `templates/` directory holds
one YAML file per metric. The `evalsets/` directory holds eval-set manifests, the
grouped, runnable sets [part one](/mizan/blog/03a-per-persona-playbooks-part-1/)
introduced; a pack can carry templates, eval-sets, or both. One file per template
is deliberate: it keeps merge conflicts small when several contributors add to the
same pack at once.

The manifest itself is short. Its `metadata.name` is the namespace, and every
template id in the pack begins with it:

```
apiVersion: mizan.dev/v1alpha1
kind: Pack
metadata:
    name: acme-support
    version: 0.1.0
spec:
    requiresApiVersion: mizan.dev/v1alpha1
```

## Author a template into the pack

Author the metric in your local registry first, exactly as the earlier pieces
did, then write it into the pack. Here is a `rubric` metric that scores a
customer-support reply on three separate concerns:

```sh
mizan registry create --id acme-support/reply-quality --kind rubric \
  --name "Support reply quality" \
  --description "Scores a customer-support reply on clarity, tone, and whether it resolves the issue." \
  --prompt 'Evaluate this support reply against the criteria. Customer message: {{customer_message}} Reply: {{reply}}' \
  --input 'customer_message:text:true' \
  --input 'reply:text:true' \
  --rubric-group 'clarity=Is the reply easy to follow?;Does it avoid jargon the customer would not know?' \
  --rubric-group 'tone=Is the reply courteous and free of blame?' \
  --rubric-group 'resolution=Does the reply actually resolve or advance the issue?' \
  --tag support --tag customer-comms \
  --license Apache-2.0 --author "ACME Support Guild" \
  --model gemini-2.5-flash
```

Now add it to the pack. `pack add` writes one schema-valid file under
`templates/`:

```sh
mizan pack add packs/acme-support --from acme-support/reply-quality
```

```
1 written, 0 skipped (dest: packs/acme-support)
  written: acme-support/reply-quality -> templates/reply-quality.yaml
```

The file it wrote is the whole metric, and it is readable enough to review in a
pull request:

```yaml
apiVersion: mizan.dev/v1alpha1
kind: MetricTemplate
metadata:
    id: acme-support/reply-quality
    name: Support reply quality
    description: Scores a customer-support reply on clarity, tone, and whether it resolves the issue.
    version: 0.1.0
    authors:
        - name: ACME Support Guild
    license: Apache-2.0
    tags:
        - support
        - customer-comms
spec:
    kind: rubric
    modalities:
        - text
    inputs:
        - name: customer_message
          modality: text
          required: true
        - name: reply
          modality: text
          required: true
    metricPromptTemplate: 'Evaluate this support reply against the criteria. Customer message: {{customer_message}} Reply: {{reply}}'
    rubricGroups:
        clarity:
            - Is the reply easy to follow?
            - Does it avoid jargon the customer would not know?
        resolution:
            - Does the reply actually resolve or advance the issue?
        tone:
            - Is the reply courteous and free of blame?
    autorater:
        model: gemini-2.5-flash
        samplingCount: 4
        flipEnabled: true
```

Nothing here is executable. It is a prompt, a list of declared inputs, and named
rubric groups. Anyone can read it, diff it, and reason about what it scores before
they ever run it. That is what "templates are data" buys you.

## The validate gate

Before you share a pack, you check it. `mizan pack validate` runs a set of
structural, identity, semantic, placeholder, and lint checks over every manifest
in the pack, and it does so with no credentials. It never calls Vertex AI, so it
costs nothing and needs no project:

```sh
mizan pack validate packs/acme-support
```

```
OK: no defects found.

0 error(s), 0 warning(s)
```

The reason this matters is that it is the same gate a shared templates repository
runs in continuous integration on every pull request. Because the checks are
credential-free, they run identically on your laptop and in CI. You catch the
problem before you publish, not after a reviewer's build turns red.

Here is the gate earning its place. Say you edit the prompt to reference an
account tier but forget to declare it as an input:

```sh
mizan pack validate packs/acme-support
```

```
templates/reply-quality.yaml:
  [ERROR] prompt references undeclared placeholder {{account_tier}} (add it to spec.inputs)

1 error(s), 0 warning(s)
error: pack validate: 1 error(s) found
```

The command exits non-zero and names the file, the defect, and the fix. Declare
the input (or drop the placeholder) and it passes again. Warnings behave
differently from errors: a missing license or a missing description is reported as
a warning and never fails the gate, so a pack can be accepted with advisories you
choose to act on later. Branch on the exit code for the accept-or-reject decision,
and read the trailing count line for the summary.

This is exactly the loop the shipped
[author-and-validate-a-template-pack](/mizan/guides/agent-skills/) skill drives
when you ask an agent to package or check a pack: it scaffolds, authors, and runs
`pack validate`, reading the exit code and the text report rather than parsing
JSON. The [user guide](/mizan/guides/user-guide/#validating-a-pack-pack-validate)
documents each check the gate applies.

## Share it, then import it

A validated pack directory is ready to share. `registry export` writes your local
templates into a pack directory (`pack add` is a thin convenience over it), and
from there the workflow is ordinary git. You commit the pack and open a pull
request against a templates repository; Mizan writes files but never pushes:

```sh
mizan registry export --out packs/acme-support --namespace acme-support
```

```
1 written, 0 skipped (dest: packs/acme-support)
  written: acme-support/reply-quality -> templates/reply-quality.yaml
```

The community repository, `github.com/ghchinoy/mizan-templates`, is the default
source, and the
[`google-brand` pack](https://github.com/ghchinoy/mizan-templates/tree/main/packs/google-brand)
that [piece two](/mizan/blog/02-one-spectrum-four-users/) imported is the worked
model to copy: a manifest, a `templates/` directory, and one file per metric.

On the consuming side, someone imports your pack. Preview first with `--dry-run`,
which computes the reconciliation without writing anything:

```sh
mizan registry import packs/acme-support --dry-run
```

```
dry run (no changes written): 1 inserted, 0 updated, 0 skipped, 0 conflicted, 0 unchanged, 0 forked (source: packs/acme-support)
  inserted: acme-support/reply-quality
```

Drop the flag to commit it:

```sh
mizan registry import packs/acme-support
```

```
1 inserted, 0 updated, 0 skipped, 0 conflicted, 0 unchanged, 0 forked (source: packs/acme-support)
  inserted: acme-support/reply-quality
```

Import reconciles by template id, so re-importing an unchanged pack does nothing:

```
0 inserted, 0 updated, 0 skipped, 0 conflicted, 1 unchanged, 0 forked (source: packs/acme-support)
  unchanged: acme-support/reply-quality (unchanged (same content))
```

The imported template records where it came from, so a consumer always knows the
provenance of a metric in their registry:

```sh
mizan registry get acme-support/reply-quality
```

```
ID:                       acme-support/reply-quality
...
Version:                  0.1.0
Kind:                     rubric
...
Source:                   pack:acme-support@packs/acme-support
...
```

By default, import takes the higher version and never clobbers a template you have
edited locally; `--strategy` lets you choose `skip`, `overwrite`, or `fork` when
you want different behavior. The shipped
[discover-and-import-templates](/mizan/guides/agent-skills/) skill wraps this half
of the loop, previewing, reconciling, and browsing what landed, and the
[user guide](/mizan/guides/user-guide/#import-from-a-local-pack-tree) covers the
import strategies in full. A source can be a local directory, a single pack, or a
git URL, so importing from your teammate's checkout and importing from the
community repository are the same command.

## Next

You have now extended Mizan the easy way: you wrote data, validated it for free,
and shared it through a pull request, with the engine untouched. The next piece
turns this contribution loop on the series itself. It builds a `docs-quality` pack
(the rubric that grades every entry, including this one, in the sidebar below) and
is honest about what such a rubric does and does not measure. After that, the
final piece moves from contributing data to extending the code, and shows the
seams where a new metric kind or behavior attaches to the core.

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
commands run as written against your project, or that the pack you author is the
right one for your team. A clean style score sits alongside the live-command
checks and human review that catch those things; it does not replace them. Read
the scorecard as a repeatable check that catches the obvious problems, not as a
measurement of whether the piece is right.

(Any scorecard numbers shown in this series are manual review estimates unless
labeled as measured; the automated readability tooling was unavailable at the
time of writing.)
