---
title: "Developing for Mizan: extending the core"
date: 2026-11-11
authors:
  - ghchinoy
excerpt: >
  For contributors who do need to touch the core: an orientation to Mizan's
  architecture and where new metric kinds and behaviors plug in.
tags: ["architecture", "contributing", "adaptive-rubrics"]
draft: false
series: "Build Evals with Mizan"
seriesOrder: 5
canonicalUrl: ""
---

The [previous piece](/mizan/blog/04-contribute-a-template-pack/) made a promise:
you can extend Mizan without touching the core. A template pack is data, so
authoring one is a YAML file and a pull request against a separate repository, not
a change to the CLI. That covers most of what people want to add.

This piece is for the other case. Sometimes the thing you want to add is not a
new template but a new kind of check, a new place results can live, or a new
behavior in the engine that runs the judge. That is a code change to `mizan`
itself, and it is the deep end of the series. The good news is that the core was
built to be extended along a small number of named seams, and once you can see
them, the change you want to make is usually smaller than you expect.

## The shape of the codebase

Mizan is a single Go module. The entry points live under `cmd/mizan/`, the domain
logic lives under `internal/`, and one rule holds the whole thing together: the
command layer depends on a very small set of types and nothing else. Concretely,
`cmd/mizan` imports `registry.Service`, `eval.Engine`, and `config`, and never
imports the SQLite store, the sync code, the pack codec, or the Vertex AI protos
directly. The
[architecture reference](/mizan/reference/architecture-final/) calls this the
dependency direction, and it is enforced as an acceptance check rather than left
to good intentions.

The place where concrete backends get attached to those abstract types is a
single file, `internal/wire/wire.go`, the composition root. It is the one spot
that knows a `registry.Service` is backed by SQLite, that the eval engine talks to
a regional Vertex client and a separate global one, and that the results store is
a SQLite file. The comment at the top of the package states the payoff plainly:
swapping SQLite for a future store, or changing the eval transport, "is a one-line
change here and nowhere else." When you add something to the core, you are almost
always doing one of two things: implementing an interface, then wiring the
concrete type in `wire.go`, or adding a branch to a dispatch that already exists.

## The metric registry and its three seams

The registry is where metric templates are stored, authored, imported, and
exported. Everything the CLI and the eval engine touch goes through one facade,
`registry.Service`. Underneath it sit three separate interfaces, and the split is
the load-bearing design decision of the collaboration layer
([collaboration-design.md](https://github.com/ghchinoy/mizan/blob/main/docs/collaboration-design.md)
holds the rationale):

- `Store` is local runtime persistence, the thing the eval engine reads at eval
  time. Its methods are `Get`, `List`, `Put`, `Delete`, and a sync-friendly
  `ListChangedSince`. SQLite implements it today; a future Firestore store is a
  drop-in.
- `Codec` is serialization, `MetricTemplate` to and from bytes, with a single
  `Marshal` / `Unmarshal` / `Ext` shape. YAML implements it today.
- `SyncBackend` is the contribution channel, where shared templates come from and
  go to. Its methods are `Load`, `Save`, and `Describe`. The git pack backend
  implements it today.

These are not aspirational interfaces from a design doc. They are the exact
signatures shipping in `internal/registry/store.go`, `codec.go`, and `sync.go`.
The reason to keep them apart is that each answers a different question, and a new
contributor usually only needs to change one. If you want templates to persist
somewhere other than a local SQLite file, you implement `Store` and change one
constructor in `wire.OpenService`. If you want a new on-disk format, you implement
`Codec`. Nothing in the command layer or the eval engine has to change, because
neither of them names the concrete type.

## The evaluation engine and dispatch by kind

The engine turns a stored `MetricTemplate` into a real evaluation. It depends only
on the registry domain model and two narrow, mockable client seams:
`EvaluationClient` over the native Vertex AI `EvaluateInstances` RPC, and
`GenaiClient` over the single `GenerateContent` call the strict custom-schema path
uses. Because both are interfaces, the engine's spec building and result mapping
are unit-tested with fakes and no network at all.

The heart of the engine is one method, `Engine.dispatch`, and it is worth reading
in full because it is the seam every metric kind attaches to:

```go
func (e *Engine) dispatch(ctx context.Context, tmpl registry.MetricTemplate, inst Instance, model string, rc runConfig) (Result, error) {
	switch tmpl.Kind {
	case registry.KindPointwise:
		return e.runPointwise(ctx, tmpl, inst, model)
	case registry.KindRubric:
		// ... rubric-detail routing ...
		return e.runRubric(ctx, tmpl, inst, model)
	case registry.KindCustomSchema:
		return e.runCustomSchema(ctx, tmpl, inst, model)
	case registry.KindPairwise:
		return e.runPairwise(ctx, tmpl, inst, model)
	case registry.KindHeuristic:
		return runHeuristic(tmpl, inst)
	default:
		return Result{}, fmt.Errorf("eval: unknown metric kind %q", tmpl.Kind)
	}
}
```

A metric kind is a value of `registry.MetricKind` plus a branch in this switch.
The shared work, resolving which autorater model to use, timing the run,
validating that every supplied field maps to a placeholder, and recording the
applied autorater, all happens once in `Engine.Run` before dispatch. Each per-kind
runner only has to produce a `Result`. That is why adding a kind is a bounded
change: you are filling in one case, not rewriting the engine.

## A concrete walkthrough: how `kind: heuristic` was added

The newest metric kind is a good worked example, because it landed as a real
change against exactly the seams above. A heuristic is a deterministic,
credential-free check: does this text contain a string, match a regex, equal a
value, parse as JSON, or validate against a JSON Schema. There is no model and no
Vertex call. Here is everything it took to add it.

**The registry got a new kind and its config.** `internal/registry/model.go`
declares `KindHeuristic` alongside the four existing kinds, teaches `NormalizeKind`
to accept it, and adds a `HeuristicSpec` field to `MetricTemplate` that holds the
check type and its operand. The check type is a small closed enum
(`contains`, `regex`, `equals`, `json-valid`, `json-schema-valid`) validated at
the authoring boundary so a typo fails when you create the template, not when you
run it.

**The engine got a new case.** The `KindHeuristic` branch in `dispatch` calls
`runHeuristic`. One detail here is the most important interface decision in the
whole change: `runHeuristic` is a free function, not a method on `*Engine`. That
is deliberate. Because it is not a method, it structurally cannot reach
`e.client`, `e.globalClient`, or `e.genai`, so the "no network, no credentials"
guarantee is enforced by the compiler rather than by a comment. `Engine.Run` also
skips model resolution entirely for a heuristic, so a heuristic run stamps no
applied autorater and never validates a model it will not use.

**The command layer got authoring flags and an honest pre-flight.** `registry
create` gained `--heuristic-type`, `--heuristic-target`, `--heuristic-value`, and
the JSON-Schema operands, and `eval run` prints a pre-flight line that says no
autorater is involved.

That is the whole surface area: one enum value and a struct in the registry, one
switch case and a free function in the engine, and a handful of flags in the CLI.
Now watch it run, with no project and no credentials configured:

```
$ mizan registry create \
    --id checks/ends-with-next \
    --name "Ends with a Read next section" \
    --kind heuristic --modality text \
    --input "response:text:true" \
    --heuristic-type contains \
    --heuristic-target response \
    --heuristic-value "Read next"

$ mizan eval run --metric checks/ends-with-next \
    --field response="The mechanics hold steady. Read next"
mizan: heuristic: no autorater (deterministic contains check, no network)
Score:        1
Explanation:  matched: text contains "Read next"

$ mizan eval run --metric checks/ends-with-next \
    --field response="An abrupt stop with no pointer onward."
mizan: heuristic: no autorater (deterministic contains check, no network)
Score:        0
Explanation:  no match: text does not contain "Read next"
```

The verdict maps to a `Score` of `1.0` for pass and `0.0` for fail, on purpose.
By reusing the same `*float32` score every other kind returns, a heuristic member
flows unchanged through eval-set aggregation, gating thresholds, and the results
store. It is a first-class metric, not a special case bolted onto the side. If you
pass `--model` to a heuristic run, the CLI tells you it is ignored rather than
silently pretending to honor it:

```
$ mizan eval run --metric checks/ci --field response="all OK here" --model gemini-2.5-pro
mizan: warning: --model is ignored for kind:heuristic metrics (deterministic, no autorater)
```

The invariant that this path never touches the network is not a claim to take on
faith. The end-to-end test in `cmd/mizan/eval_heuristic_cli_test.go` drives the
real Cobra commands with no `PROJECT_ID` and no Application Default Credentials
set, for every check type, and asserts the exact score. If the path ever reached
Vertex, that test would fail:

```
$ go test ./cmd/mizan/ -run TestCLIHeuristic -count=1
ok  	github.com/ghchinoy/mizan/cmd/mizan	0.511s
```

## Where results live

Runs persist by default, and the results store follows the same seam pattern as
the registry. `wire.OpenResultService` returns a `results.Service` over a backend
selected by config. Today that is SQLite. A Firestore leg is a visible deferred
dependency, so an unknown backend returns a clear error rather than a silent stub.
The lesson for a contributor is that "add a place results can live" is the same
shape of change as "add a place templates can live": implement the store
interface, wire the one constructor, leave everything above it alone.

## The contribution gates

Before you open a pull request, run the checks CI runs. They all have Makefile
targets, and the
[CONTRIBUTING guide](https://github.com/ghchinoy/mizan/blob/main/CONTRIBUTING.md)
maps each CI gate to its local command:

```sh
make fmt-check   # gofmt cleanliness
make vet         # go vet ./...
make test        # unit tests
make lint        # golangci-lint (v2)
make vuln        # govulncheck
```

Two gates are worth knowing before they surprise you. The coverage floor is a soft
nudge: CI prints total coverage and warns if it dips, but never fails the build on
coverage alone. The doc-drift guard is not soft. If your pull request changes
`cmd/mizan/` or `internal/eval/` but updates none of the tracked docs, the job
fails. That is the guard doing its job: a change to the engine or the command
surface is expected to move a guide with it. If the docs genuinely do not apply,
you override it explicitly with a `docs: N/A` label or a `docs: N/A` line in the
pull request body. The build is CGO-free and the toolchain is pinned in `go.mod`,
so local and CI builds use the same compiler, and the author is never the
reviewer.

## Read next

You have now seen the whole arc: run one eval, place your role on the spectrum,
follow a per-persona playbook, contribute a pack without touching the core, and
now extend the core itself. The two references that go deeper are the
[architecture reference](/mizan/reference/architecture-final/), which lays out the
module layout and the engine dispatch, and
[CONTRIBUTING.md](https://github.com/ghchinoy/mizan/blob/main/CONTRIBUTING.md),
which is the contributor-ready account of the gates. The best way in is to read
`internal/eval/engine.go` and `internal/wire/wire.go` side by side: one shows the
dispatch you extend, the other shows the single place you wire what you add.

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
style and clarity. It does not verify that the substance is correct, that the code
paths described match the shipped source, or that the walkthrough compiles. A clean
style score sits alongside the live-command checks and human review that catch
those things; it does not replace them. Read the scorecard as a repeatable check
that catches the obvious problems, not as a measurement of whether the piece is
right.

(Any scorecard numbers shown in this series are manual review estimates unless
labeled as measured; the automated readability tooling was unavailable at the
time of writing.)
