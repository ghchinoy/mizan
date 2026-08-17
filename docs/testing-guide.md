# Mizan Testing Guide

This guide is for **exercising each built capability with real, copy-pasteable
commands** — it's a hands-on companion, not a replacement for the narrative
docs. For conceptual/narrative detail (what a flag means, how config
resolution works, full CRUD walkthroughs), see
[`docs/user-guide.md`](user-guide.md) and
[`docs/llm-as-judge-scenarios.md`](llm-as-judge-scenarios.md).

The guide covers the CLI's capabilities with copy-pasteable checks: the four
metric kinds (pointwise, rubric, custom_schema, pairwise), multimodal
(image/audio/video/music), the `mizan version` command, per-criterion rubric
detail with strict reconciliation, and global-only judge auto-routing. Scores
and explanations are live autorater output and are expected to vary
run-to-run.

The metric-kind recipes below (pointwise, rubric, custom_schema, pairwise,
multimodal) were run live against a binary built from `main`, with output
captured verbatim — all four metric kinds and multimodal are CLI-runnable
end-to-end.

**How these checks were verified.** Every Vertex-hitting recipe below was run
against live Vertex (project `ghchinoy-genai-sa`, autorater output captured
verbatim) — including the two that previously were not: per-criterion rubric
`--rubric-detail` and global-only judge auto-routing. The only outputs still
transcribed from the shipped source are the ones that depend on
non-deterministic judge behavior that cannot be forced on demand: the rubric
**reconciliation error cases** (a missing / duplicated / extra criterion) and
the **self-correcting retry** for a global-only judge that is not on the
known-prefix list. Each of those is marked inline where it appears. The
`mizan version` section and the `make` targets under "Dev & CI setup" are
pure-local checks, also run against the built binary. Nothing here implies a
live Vertex run that did not happen.

## Minimal setup

```sh
go install github.com/ghchinoy/mizan/cmd/mizan@main
mizan config set project-id <your-project-id>
mizan config set location us-central1   # default; the "us" multi-region 404s
gcloud auth application-default login   # ADC — no API-key auth path exists
```

Confirm your project and location resolved from where you expect with
`mizan config show` (alias: `mizan config list`) — it prints a `KEY`/`VALUE`/`SOURCE`
table whose `SOURCE` matches the `src=` hints in the `eval` pre-flight line. A
mistyped `MIZAN_*` variable is ignored with a `did you mean …?` warning on stderr
rather than silently applied. See
[`docs/user-guide.md`](user-guide.md#configure) for the full table.

Use a scratch registry DB per test session so you don't collide with a real
registry:

```sh
export MIZAN_REGISTRY_DB=/tmp/mizan-testing.db
```

See [`docs/user-guide.md`](user-guide.md#prerequisites) for the full
prerequisites/install/config explanation (env-var overrides, `.env` file
location, the custom-endpoint safeguard, etc.) — it isn't repeated here.

## Version and build metadata

`mizan version` is a pure-local check — it needs **no** project and **no** ADC,
so it is the one recipe you can run anywhere. It prints the version, git
commit, and build date, and honors the global `-o/--output json|table` flag
(default is a plain one-line form; `table` prints that same plain line, `json`
an object). The three values are injected at build time via `-ldflags`; for the
release/tag workflow that stamps them, see
[`docs/user-guide.md`](user-guide.md#version-and-releases) — this section does
not repeat it.

There are three build flavors and the version string differs in each. Flavors 1
and 2 below were run against the built binary in this pass.

**1. `make build` in an untagged checkout (the common dev case — verified this
pass).** `make build` derives the version from `git describe --tags --always
--dirty`. With no reachable `vX.Y.Z` tag, `git describe` falls back to the short
commit, so the **version equals the commit** — this surprises people, so call it
out:

```sh
$ ./bin/mizan version
mizan 7da080f (commit 7da080f, built 2026-08-10T14:58:30Z)

$ ./bin/mizan version -o json
{
  "version": "7da080f",
  "commit": "7da080f",
  "date": "2026-08-10T14:58:30Z"
}
```

(`version == commit` here is the `git describe` fallback, not a bug. Your own
commit hash and timestamp will differ. This equality holds on a **clean** tree;
uncommitted local edits make `git describe --tags --always --dirty` append
`-dirty`, e.g. `mizan 7da080f-dirty (commit 7da080f, …)`, so the version and
commit then differ by that suffix.)

**2. Plain `go build ./cmd/mizan` (no ldflags — verified this pass).** With no
`-ldflags` at all, the three values keep their in-source placeholders
`dev`/`none`/`unknown`:

```sh
$ go build -o /tmp/mizan-plain ./cmd/mizan && /tmp/mizan-plain version
mizan dev (commit none, built unknown)

$ /tmp/mizan-plain version -o json
{
  "version": "dev",
  "commit": "none",
  "date": "unknown"
}
```

**3. A released / tagged build** (`make build` or `make install` from a checkout
that has a reachable `vX.Y.Z` tag, or a downloaded release binary). `git
describe` resolves to the tag, so the version *is* the tag:

```sh
$ mizan version
mizan v1.2.3 (commit a1b2c3d, built 2026-08-10T00:00:00Z)

$ mizan version -o json
{
  "version": "v1.2.3",
  "commit": "a1b2c3d",
  "date": "2026-08-10T00:00:00Z"
}
```

(This tagged example is illustrative of the *shape* — the real `version`,
`commit`, and `date` come from the tag, commit, and build timestamp of the build
you are running. It was not produced from a tagged checkout in this pass;
flavors 1 and 2 are the ones captured live here.)

## Text pointwise

The original, simplest path — fully implemented and wired end-to-end:
CLI → `registry.Service` → `eval.Engine` → live Vertex AI
`EvaluateInstances`. (All four metric kinds share this same live-call shape;
see the sections below for rubric, custom_schema, pairwise, and multimodal.)

Create the template:

```sh
$ mizan registry create --id demo/conciseness --name "Conciseness" \
    --description "Scores how concise a response is" \
    --kind pointwise \
    --prompt "Rate how concise this response is from 0 (verbose) to 1 (concise). Response: {{response}}" \
    --model gemini-2.5-flash
ID:             demo/conciseness
Name:           Conciseness
Kind:           pointwise
Modalities:     text
Model:          gemini-2.5-flash
SamplingCount:  4
Description:    Scores how concise a response is
Prompt:         Rate how concise this response is from 0 (verbose) to 1 (concise). Response: {{response}}
```

Run it (live call to Vertex AI, captured on 2026-08-09 against
`gemini-2.5-flash` in `us-central1`):

```sh
$ mizan eval run --metric demo/conciseness --field response="The cat sat on the mat."
Score:        0.95
Explanation:  The sentence is extremely concise, using the minimum number of words necessary to convey a complete and clear thought without any redundancy or filler.
```

Re-running this against a live model is a real, non-deterministic autorater
call — don't expect the score/explanation text to be byte-identical on a
re-run; the shape (a `Score` float and an `Explanation` string) is what's
guaranteed.

### What `Score`/`Explanation` mean

- **`Score`** is a float whose scale is whatever your prompt asked for — Mizan
  doesn't impose one. Here the prompt asked for 0 (verbose) to 1 (concise), so
  `0.95` means "very concise."
- **`Explanation`** is free-text rationale from the autorater model. Treat it
  as a qualitative aid, not a machine-parseable field.

## Rubric

The native rubric path is implemented in `internal/eval/native.go`
(`runRubric`, which renders the inline rubric criteria as text into the same
`PointwiseMetricSpec`/`EvaluateInstances` judge-prompt mechanism `pointwise`
uses, via `renderRubricGroups` — the synchronous API has no
`LLMBasedMetricSpec`/structured `rubric_groups` field to pass them as). Rubric
templates are CLI-authorable via `registry create`/`update`: pass a repeatable
`--rubric-group "name=criterion one;criterion two"` flag, or
`--rubric-groups-file <path>` pointing at a JSON object of
`{"group": ["crit1", "crit2"], ...}`.

Creating a rubric template with **no** rubric flag fails immediately at create
time:

```sh
$ mizan registry create --id demo/rubric-nogroup --name "no group" --kind rubric \
    --prompt "Evaluate: {{response}}"
Error: kind "rubric" requires rubric groups; pass --rubric-group "name=crit1;crit2" (repeatable) or --rubric-groups-file <path>
```

Create and run a real rubric template (live, captured in this pass):

```sh
$ mizan registry create --id demo/rubric-quality --name "Response Quality Rubric" --kind rubric \
    --prompt "Evaluate this response: {{response}}" \
    --rubric-group "clarity=Is the response clear;Is it free of jargon" \
    --rubric-group "correctness=Is the factual content accurate"
ID:                        demo/rubric-quality
Name:                      Response Quality Rubric
Kind:                      rubric
Modalities:                text
Model:                     gemini-2.5-flash
SamplingCount:             4
Prompt:                    Evaluate this response: {{response}}
RubricGroup[clarity]:      Is the response clear; Is it free of jargon
RubricGroup[correctness]:  Is the factual content accurate

$ mizan eval run --metric demo/rubric-quality --field response="The Eiffel Tower is located in Paris, France, and was completed in 1889."
Score:        5
Explanation:  The response is perfectly clear, free of jargon, and all factual content (location and completion date of the Eiffel Tower) is accurate.
```

(Same non-determinism caveat as pointwise: score/explanation text will vary
run to run — the `Score`/`Explanation` shape is what's guaranteed.)

## Custom schema

Same underlying path as rubric for CLI-authoring: the engine
(`internal/eval/custom.go`'s `runCustomSchema`, calling
`genai.GenerateContent` with a `ResponseSchema` and exponential backoff) is
authored with `--response-schema '<json>'` (inline) or
`--response-schema-file <path>` pointing at a JSON-Schema object.

Same create-time validation as rubric — no schema flag fails immediately:

```sh
$ mizan registry create --id demo/custom-noschema --name "no schema" --kind custom_schema \
    --prompt "Evaluate: {{response}}"
Error: kind "custom_schema" requires a response schema; pass --response-schema '<json>' or --response-schema-file <path>
```

The schema file is a JSON-Schema object with `overall_score`, `compliant`,
`flagged_issues`, `explanation` properties, and is shipped in this repo at
[`docs/examples/compliance-schema.json`](examples/compliance-schema.json) —
`--response-schema-file` accepts standard (lowercase) JSON-Schema `type`
values like `"object"`/`"integer"`/`"array"`/`"string"` directly; genai's own
uppercase convention (`"OBJECT"`, etc.) also works, since
`internal/eval/custom.go`'s `toGenaiSchema`/`normalizeSchemaTypes` uppercase
whatever you give them, but plain JSON-Schema is more idiomatic/portable and
is what's shown here:

```json
{
  "type": "object",
  "properties": {
    "overall_score": {
      "type": "integer",
      "description": "A 0-10 compliance score for the response (10 = fully compliant)."
    },
    "compliant": {
      "type": "boolean",
      "description": "Whether the response complies with the policy overall."
    },
    "flagged_issues": {
      "type": "array",
      "items": { "type": "string" },
      "description": "Short labels for any policy issues found (empty if none)."
    },
    "explanation": {
      "type": "string",
      "description": "Free-text rationale for the score and compliant verdict."
    }
  },
  "required": ["overall_score", "compliant", "explanation"]
}
```

If you have a clone of this repo, point `--response-schema-file` straight at
`docs/examples/compliance-schema.json` (this is a relative path, so run the
command from the repository root, or adjust the path / use an absolute path
if you're elsewhere). Otherwise, create the file yourself first:

```sh
cat > /tmp/compliance-schema.json <<'EOF'
{
  "type": "object",
  "properties": {
    "overall_score": {
      "type": "integer",
      "description": "A 0-10 compliance score for the response (10 = fully compliant)."
    },
    "compliant": {
      "type": "boolean",
      "description": "Whether the response complies with the policy overall."
    },
    "flagged_issues": {
      "type": "array",
      "items": { "type": "string" },
      "description": "Short labels for any policy issues found (empty if none)."
    },
    "explanation": {
      "type": "string",
      "description": "Free-text rationale for the score and compliant verdict."
    }
  },
  "required": ["overall_score", "compliant", "explanation"]
}
EOF
```

Create and run a real custom_schema template (live, captured in this pass,
using the shipped `docs/examples/compliance-schema.json` — swap in
`/tmp/compliance-schema.json` if you used the heredoc above instead):

```sh
$ mizan registry create --id demo/custom-compliance --name "Compliance Check" --kind custom_schema \
    --prompt "Check if this response follows the policy: no medical advice. Response: {{response}}" \
    --response-schema-file docs/examples/compliance-schema.json
ID:              demo/custom-compliance
Name:            Compliance Check
Kind:            custom_schema
Modalities:      text
Model:           gemini-2.5-flash
SamplingCount:   4
Prompt:          Check if this response follows the policy: no medical advice. Response: {{response}}
ResponseSchema:  { …

$ mizan eval run --metric demo/custom-compliance --field response="Drink plenty of water and rest."
Score:                         (none)
Explanation:                   
CustomOutput[compliant]:       true
CustomOutput[explanation]:     The response provides very general health recommendations (drink plenty of water and rest). While these are health-related, they are not specific medical advice for a particular condition, diagnosis, or treatment plan, and are often considered common sense. Therefore, it largely complies with the 'no medical advice' policy, though it touches on health-related topics.
CustomOutput[flagged_issues]:  []
CustomOutput[overall_score]:   9
```

(Non-determinism caveat as before: this is a live autorater call — a re-run
may return a different `compliant`/`overall_score`/`explanation` verdict for
the same input, e.g. flagging the response instead of passing it. The shape —
one `CustomOutput[field]` line per schema property — is what's guaranteed.)

Note the shape: `custom_schema` results have no `Score`/`Explanation` (both
print empty/`(none)`) — the structured `CustomOutput[...]` fields carry the
result instead, one line per property in your `ResponseSchema`.

## Pairwise

Pairwise has a full native engine path (`internal/eval/pairwise.go`) and
a dedicated `mizan eval pairwise` CLI command, confirmed via `--help`:

```
mizan eval pairwise --metric <id> --baseline key=… --candidate key=… [--field/--file/--gcs …]
```

Create a pairwise template (the `--candidate-field`/`--baseline-field` flags
name the placeholders that `eval pairwise --baseline`/`--candidate` fill) and
run it (live, captured in this pass):

```sh
$ mizan registry create --id demo/pairwise-quality --name "Pairwise Quality" --kind pairwise \
    --prompt "Which response better answers the question 'What is the capital of France?' Baseline: {{baseline_response}} Candidate: {{candidate_response}}" \
    --candidate-field candidate_response --baseline-field baseline_response
ID:             demo/pairwise-quality
Name:           Pairwise Quality
Kind:           pairwise
Modalities:     text
Model:          gemini-2.5-flash
SamplingCount:  4
Prompt:         Which response better answers the question 'What is the capital of France?' Baseline: {{baseline_response}} Candidate: {{candidate_response}}

$ mizan eval pairwise --metric demo/pairwise-quality \
    --baseline baseline_response="Paris." \
    --candidate candidate_response="The capital of France is Paris, a city renowned for the Eiffel Tower and its rich cultural history."
Choice:       BASELINE
Explanation:  The baseline response is more direct and concise, providing only the information specifically asked for in the question, which is generally preferred for simple factual queries.
```

Because flip is on by default, `mizan eval pairwise` also prints a flip
warning to stderr — see "Pairwise flip and why the Choice is authoritative"
below for what it means.

`Choice` is non-deterministic across runs like `Score`/`Explanation` — a
re-run of the exact same command may return `CANDIDATE` instead of
`BASELINE`; both are valid live autorater outcomes.

### Placeholder contract (real, verified)

The metric prompt template **must** reference the baseline and candidate
field names as `{{name}}` placeholders matching
`--baseline-field`/`--candidate-field`. This isn't a suggestion — it's
enforced, because the underlying API rejects instance keys that aren't
present in the template. Reproduced live:

```sh
$ mizan registry create --id demo/pairwise-badprompt --name "Pairwise Bad Prompt" --kind pairwise \
    --prompt "Which response is better?" \
    --candidate-field candidate_response --baseline-field baseline_response
ID:             demo/pairwise-badprompt
Name:           Pairwise Bad Prompt
Kind:           pairwise
Modalities:     text
Model:          gemini-2.5-flash
SamplingCount:  4
Prompt:         Which response is better?

$ mizan eval pairwise --metric demo/pairwise-badprompt \
    --baseline baseline_response="Paris." \
    --candidate candidate_response="The capital of France is Paris."
Error: eval: pairwise template "demo/pairwise-badprompt" metric prompt must reference the baseline {{baseline_response}} and candidate {{candidate_response}} placeholder(s); the API rejects instance keys not present in the template
```

### Pairwise flip and why the Choice is authoritative

`registry create --kind pairwise` accepts a `--flip-enabled` flag (default
`true`), and pairwise evaluation honors it. With flip on, the autorater runs
both position orderings across samples to mitigate position bias, and the
returned `Choice` is the de-biased, authoritative verdict — trust it.

The catch is the `Explanation`: under flip it is a single sampled artifact
whose `baseline`/`candidate` wording may reflect a flipped ordering and may
not match the order you presented. It can read as praising the other response
even when the `Choice` is correct. When flip is in effect, `mizan eval
pairwise` emits this warning on stderr (and, with `--output json`, under the
`warnings` array):

```text
# stderr
pairwise flip is enabled: the Choice is the de-biased, authoritative verdict; the explanation is a sampled artifact whose 'baseline'/'candidate' wording may reflect a flipped ordering and may not match your input. Disable flip (--flip-enabled=false on the template) to keep the explanation's wording aligned with the presented order, at the cost of position-bias mitigation.
```

To keep the explanation's wording aligned with the presented order, create the
template with `--flip-enabled=false` (the trade-off: you lose position-bias
mitigation). Set the flag explicitly at create time to opt out.

For the same guidance framed for end users, see "Pairwise flip and the
`Choice` is authoritative" in `docs/user-guide.md`, and
`docs/llm-as-judge-scenarios.md#scenario-2-compare-two-responses-pairwise`.

### Compare two media assets (pairwise)

The compare recipes above pit two **text** responses against each other. The
same command compares two **media** assets — two images, two videos, two audio
clips — by filling the baseline and candidate slots from `--gcs` (a pre-staged
`gs://` URI) or `--file` (a local asset the engine stages for you) instead of
`--baseline`/`--candidate`. Everything else is the pairwise path you already
know (the CLI keeps `mizan eval pairwise` as its spelling); the difference is
only where the two slots get their values, so the judge actually sees both
assets and grounds its verdict in their content.

Create a compare template whose baseline/candidate fields are media — declare
the asset modality alongside `text`, and point the prompt's
`{{baseline_…}}`/`{{candidate_…}}` placeholders at those fields:

```sh
$ mizan registry create --id demo/pairwise-image --name "Pairwise Image Compare" --kind pairwise \
    --modality image --modality text --model gemini-2.5-flash \
    --prompt "You are comparing two images as candidate pet photographs. Describe what each image shows, then choose which is the better, clearer photo of a domestic pet. Baseline: {{baseline_image}} Candidate: {{candidate_image}}" \
    --baseline-field baseline_image --candidate-field candidate_image
ID:             demo/pairwise-image
Name:           Pairwise Image Compare
Kind:           pairwise
Modalities:     image,text
Model:          gemini-2.5-flash
SamplingCount:  4
Prompt:         You are comparing two images as candidate pet photographs. Describe what each image shows, then choose which is the better, clearer photo of a domestic pet. Baseline: {{baseline_image}} Candidate: {{candidate_image}}
```

Run the compare with each media slot supplied via `--gcs` (live, captured in
this pass against two public sample images:
`gs://cloud-samples-data/generative-ai/image/320px-Felis_catus-cat_on_snow.jpg`
as baseline, `.../a-man-and-a-dog.png` as candidate):

```sh
$ mizan eval pairwise --metric demo/pairwise-image \
    --gcs baseline_image=gs://cloud-samples-data/generative-ai/image/320px-Felis_catus-cat_on_snow.jpg \
    --gcs candidate_image=gs://cloud-samples-data/generative-ai/image/a-man-and-a-dog.png
Choice:       BASELINE
Explanation:  Image (A) shows a tabby cat standing in the snow, clearly and sharply focused as the sole subject. Image (B) shows a man and a dog taking a selfie together in a living room, where the dog shares prominence with the human. Image (A) is a clearer and better photo *of a domestic pet* because the pet is the singular and primary focus.
```

The `Explanation` describes what is **actually in each image** — a cat in the
snow vs. a man-and-dog selfie — which is the proof that the judge saw the media
rather than a literal URI string. As with every compare run, the pre-flight echo
and (because flip is on by default) the flip warning go to **stderr**:

```
mizan: autorater → project=ghchinoy-genai-sa (src=env) location=us-central1 (src=env-file) model=gemini-2.5-flash (path=native)
pairwise flip is enabled: the Choice is the de-biased, authoritative verdict; the explanation is a sampled artifact whose 'baseline'/'candidate' wording may reflect a flipped ordering and may not match your input. Disable flip (--flip-enabled=false on the template) to keep the explanation's wording aligned with the presented order, at the cost of position-bias mitigation.
```

`Choice` (and the wording of the `Explanation`) is live autorater output and
varies run to run — see [Pairwise flip and why the Choice is
authoritative](#pairwise-flip-and-why-the-choice-is-authoritative) above. Videos
and audio compare the same way: declare `--modality video`/`--modality audio` on
the template and pass `.mp4`/audio URIs to the same two slots.

#### Media belongs in `--gcs`/`--file`, never a text slot

A media reference only reaches the judge *as media* through `--gcs`/`--file`.
Passing a `gs://` URI to a **text** slot
(`--baseline`/`--candidate`/`--field`) is a hard error with a non-zero exit —
the guard stops you before the URI is sent verbatim to the judge (which would
never see the media, and would confabulate about a literal string):

```sh
$ mizan eval pairwise --metric demo/pairwise-image \
    --baseline baseline_image=gs://cloud-samples-data/generative-ai/image/320px-Felis_catus-cat_on_snow.jpg \
    --candidate candidate_image=gs://cloud-samples-data/generative-ai/image/a-man-and-a-dog.png
error: --baseline baseline_image=<value> looks like a gs:// media URI passed as TEXT; a gs:// URI in a text slot is sent verbatim to the judge (which never sees the media). Use --gcs baseline_image=gs://cloud-samples-data/generative-ai/image/320px-Felis_catus-cat_on_snow.jpg to evaluate it as a media asset

$ echo $?
1
```

The error names the offending slot and hands you the exact `--gcs` form to use
instead (a local media path in a text slot is caught the same way, pointing you
at `--file`). The fix is the working run above: put each media asset in `--gcs`
(pre-staged) or `--file` (local, auto-staged), never a text slot.

## Multimodal

Native multimodal pointwise (and pairwise) evaluation is implemented: local
files passed with `--file key=/path` are auto-staged to your configured
`StagingBucket` (`internal/asset`: MIME detection + content-addressed upload
to `gs://.../mizan-staging/<sha256>.<ext>`), and pre-staged assets can be
passed directly with `--gcs key=gs://...`.

Generate a tiny test image (any real image/audio/video/music file works —
this is just the smallest thing to synthesize without external
dependencies):

```sh
python3 -c "
import struct, zlib
def chunk(tag, data):
    return struct.pack('>I', len(data)) + tag + data + struct.pack('>I', zlib.crc32(tag+data) & 0xffffffff)
w, h = 4, 4
raw = b''.join(b'\x00' + bytes([255,0,0]*w) for _ in range(h))
png = b'\x89PNG\r\n\x1a\n' + chunk(b'IHDR', struct.pack('>IIBBBBB', w,h,8,2,0,0,0)) + chunk(b'IDAT', zlib.compress(raw)) + chunk(b'IEND', b'')
open('/tmp/test-image.png', 'wb').write(png)
"
```

Create an image metric and run it against a local file (live, captured in
this pass against a 4x4 solid-red synthetic PNG at `/tmp/test-image.png`):

```sh
$ mizan registry create --id demo/image-description --name "Image Color Check" --kind pointwise \
    --prompt "What is the dominant color in this image? {{photo}}" --modality image --modality text
ID:             demo/image-description
Name:           Image Color Check
Kind:           pointwise
Modalities:     image,text
Model:          gemini-2.5-flash
SamplingCount:  4
Prompt:         What is the dominant color in this image? {{photo}}

$ mizan eval run --metric demo/image-description --file photo=/tmp/test-image.png
Score:        5
Explanation:  The image is a solid color, and that color is unmistakably red, making it the dominant color.
```

Staging genuinely happens and is content-addressed by SHA-256 — the local
file's hash matches the object name Mizan uploads:

```sh
$ sha256sum /tmp/test-image.png
3fd6e6be528c182d768563a63b65ac5a70d022149a01eeeaaa30396d75f426e0  /tmp/test-image.png

$ gcloud storage ls gs://ghchinoy-genai-sa-mizan-staging/mizan-staging/
gs://ghchinoy-genai-sa-mizan-staging/mizan-staging/3fd6e6be528c182d768563a63b65ac5a70d022149a01eeeaaa30396d75f426e0.png
gs://ghchinoy-genai-sa-mizan-staging/mizan-staging/de95a16efc9d3dd418391f23f03d1b34358fe64e1187e13aa9cc096a79926011.png
```

(The second object is a leftover from an earlier verification pass against
the same bucket — the content-addressed naming means re-staging identical
bytes is idempotent, it doesn't create a duplicate.)

`--gcs` reuses a pre-staged URI directly, without re-uploading (live,
captured in this pass, reusing the URI just staged above):

```sh
$ mizan eval run --metric demo/image-description --gcs photo=gs://ghchinoy-genai-sa-mizan-staging/mizan-staging/3fd6e6be528c182d768563a63b65ac5a70d022149a01eeeaaa30396d75f426e0.png
Score:        5
Explanation:  The image is a solid block of color, and that color is red, making it the unequivocally dominant color.
```

Audio/video/music follow the same `--modality`/`--file`/`--gcs` shape; image
is shown here because it's the easiest asset to synthesize for a
reproducible test.

## Template packs: author, validate, export & import

Packs are how metric templates travel between registries: a pack directory is a
`mizan-pack.yaml` manifest plus a `templates/` tree (and an empty `evalsets/`
carriage hook). The whole author → export → validate → import loop is
**pure-local** — no project, no ADC, no Vertex call — so, like `mizan version`
above, it's a recipe you can run anywhere. This is the hands-on companion to the
user-guide's fuller [Registry walkthrough](user-guide.md#registry-walkthrough);
see there for the narrative and for the full `--strategy` / `--dry-run`
reconciliation semantics, which this recipe deliberately does not re-derive.

The output below was captured live against the built binary in this pass, using
a scratch `MIZAN_REGISTRY_DB` seeded with three throwaway pointwise templates in
an `acme` namespace. The `mizan: loaded env file …` notice each command writes to
stderr is omitted for copy-pasteability.

Scaffold an empty pack and confirm its shape:

```sh
$ mizan pack init packs/acme --name acme
initialized pack "packs/acme" (namespace "acme")

$ ls packs/acme
evalsets
mizan-pack.yaml
templates
```

`pack add` writes a single template into the pack straight from the registry (a
thin convenience over `registry export --id`) — a handy way to build a pack up
one template at a time. Start with just `acme/clarity`:

```sh
$ mizan pack add packs/acme --from acme/clarity
1 written, 0 skipped (dest: packs/acme)
  written: acme/clarity -> templates/clarity.yaml
```

Export pulls a whole namespace out of the registry into the pack (use `--all` for
every template, or `--id <id>` for a single one) — one file per template lands
under `templates/`. Export always writes the entire selection, so here it adds
`helpfulness` and `tone` and re-writes the `clarity.yaml` that `pack add` just
placed — the two commands are complementary, not competing:

```sh
$ mizan registry export --out packs/acme --namespace acme
3 written, 0 skipped (dest: packs/acme)
  written: acme/clarity -> templates/clarity.yaml
  written: acme/helpfulness -> templates/helpfulness.yaml
  written: acme/tone -> templates/tone.yaml
```

Validate the pack — this is the creds-free PR gate (structural schema, identity,
kind semantics, placeholder consistency, and lint). It exits non-zero only when
there are `error(s)`; `warning(s)` are advisory and never fail:

```sh
$ mizan pack validate packs/acme
templates/clarity.yaml:
  [warn ] lint: missing metadata.description
  [warn ] lint: no autorater.model set (eval-time default will be used)
templates/helpfulness.yaml:
  [warn ] lint: missing metadata.description
  [warn ] lint: no autorater.model set (eval-time default will be used)
templates/tone.yaml:
  [warn ] lint: missing metadata.description
  [warn ] lint: no autorater.model set (eval-time default will be used)

0 error(s), 6 warning(s)
```

Import the pack into a **fresh** `MIZAN_REGISTRY_DB`. The summary is the same
six-field report used by the import examples elsewhere in this guide:

```sh
$ mizan registry import packs/acme
3 inserted, 0 updated, 0 skipped, 0 conflicted, 0 unchanged, 0 forked (source: packs/acme)
  inserted: acme/clarity
  inserted: acme/helpfulness
  inserted: acme/tone
```

Re-importing the same pack is a no-op — nothing changed on disk, so every
template lands in the `unchanged` column instead of `inserted`:

```sh
$ mizan registry import packs/acme
0 inserted, 0 updated, 0 skipped, 0 conflicted, 3 unchanged, 0 forked (source: packs/acme)
  unchanged: acme/clarity (unchanged (same content))
  unchanged: acme/helpfulness (unchanged (same content))
  unchanged: acme/tone (unchanged (same content))
```

## Per-criterion rubric detail and reconciliation (`--rubric-detail`)

> **ADC-required — captured live.** Every command in this section makes a live
> Vertex AI call (via the genai structured-output path at `location=global`) and
> needs ADC + a configured project. The happy-path scorecard below was **run
> against live Vertex** (project `ghchinoy-genai-sa`) and captured verbatim; the
> per-criterion scores are live and non-deterministic, so your exact numbers and
> rationales will differ. The **reconciliation error cases** (missing / duplicated
> / extra criterion) require the judge to return a non-conforming criterion set,
> which cannot be forced on demand — those specific outputs are transcribed from
> the shipped source (`internal/eval/rubric_structured.go`) and flagged inline.
> For the narrative and output shape see
> [`docs/llm-as-judge-scenarios.md`](llm-as-judge-scenarios.md#scenario-4-explainable-per-criterion-rubric-scoring---rubric-detail)
> (Scenario 4) and the reconciliation contract in
> [`docs/user-guide.md`](user-guide.md#per-criterion-rubric-detail---rubric-detail).

Add `--rubric-detail` to `eval run` on a `rubric` template to get a score and
rationale per authored criterion plus an overall roll-up. Reuse
`demo/rubric-quality` from the [Rubric](#rubric) recipe above — it authors three
criteria: `clarity` = "Is the response clear" / "Is it free of jargon", and
`correctness` = "Is the factual content accurate".

The judge's returned criteria are **strictly reconciled** against your authored
set, matched by the exact **(group, criterion)** pair. There are three cases.

**(a) Happy path — all authored criteria returned.** You get the full
per-criterion scorecard: an overall `Score` + `Explanation`, then the
`Per-criterion` table (one row per authored `(group, criterion)`). Captured live
below (scores are live autorater output and vary run to run):

```sh
$ mizan eval run --metric demo/rubric-quality --rubric-detail --rubric-scale 1-5 \
    --field response="The Eiffel Tower is in Paris, France, completed in 1889."
Score:        5
Explanation:  The response is perfectly clear, concise, and factually accurate. It provides correct information in a straightforward manner, free of any ambiguity or complex language.
Per-criterion:
GROUP        CRITERION                        SCORE  RATIONALE
clarity      Is the response clear            5      The response is exceptionally clear and easy to understand.
clarity      Is it free of jargon             5      The response uses simple, common language with no jargon.
correctness  Is the factual content accurate  5      All factual statements about the Eiffel Tower's location and completion year are accurate.
```

Because `--rubric-detail` uses the genai structured-output path, the pre-flight
echo goes to the global host — captured on stderr from the same run:

```
mizan: autorater → project=ghchinoy-genai-sa (src=env-file) location=global (src=global-path) model=gemini-2.5-flash (path=genai)
```

Set the Likert range with `--rubric-scale "<min>-<max>"` (default `1-5`;
non-negative integers, `min < max`).

**(b) A missing or duplicated authored criterion → hard error, non-zero exit.**
If the judge omits an authored `(group, criterion)` pair, or returns the same
pair more than once, the run **fails** with an `eval:` error naming the
offending pair(s) — no partial scorecard is surfaced. Exact format from source
(pairs are rendered inside `[...]` by `formatPairs`):

```
Error: eval: rubric reconciliation failed: missing authored criterion(s): [group="clarity" criterion="Is it free of jargon"]
```

```
Error: eval: rubric reconciliation failed: duplicated authored criterion(s): [group="clarity" criterion="Is the response clear"]
```

If both categories occur in one run they are reported together, joined with
`;`:

```
Error: eval: rubric reconciliation failed: missing authored criterion(s): [group="clarity" criterion="Is it free of jargon"]; duplicated authored criterion(s): [group="clarity" criterion="Is the response clear"]
```

Missing/duplicate are **judge-behavior-dependent** and hard to force
deterministically — treat these as "what you'll see *if* the judge returns a
missing or duplicated pair," not a reproducible command. Unlike the happy path
above (captured live), these two error strings are still transcribed from
source. The contract they enforce is documented in
[`docs/user-guide.md`](user-guide.md#per-criterion-rubric-detail---rubric-detail)
and Scenario 4.

**(c) An extra (unauthored) criterion → kept in output + a warning.** If the
judge returns a `(group, criterion)` pair you did not author, it is **kept** in
the output and a warning is emitted — extras are informative, not corrupting, so
they do **not** fail the run. The warning shows up on two surfaces:

- **Text mode** — one warning line per extra, on **stderr** (exact from
  source):

  ```
  mizan: rubric reconciliation warning: judge returned unauthored criterion (group="extra_group", criterion="some unauthored criterion"); kept in output
  ```

- **`--output json` mode** — the same message(s) also serialize into the result
  body under the `warnings` array (`Result` carries a `warnings,omitempty` JSON
  tag), alongside the kept criterion, so machine consumers see them too:

  ```json
  {
    "Score": 4.5,
    "CustomOutput": {
      "per_criterion": [
        { "group": "extra_group", "criterion": "some unauthored criterion", "score": 4, "rationale": "…" }
      ]
    },
    "warnings": [
      "mizan: rubric reconciliation warning: judge returned unauthored criterion (group=\"extra_group\", criterion=\"some unauthored criterion\"); kept in output"
    ]
  }
  ```

  (JSON keys/shape illustrate the `warnings` field next to the kept criterion;
  the exact surrounding fields depend on your template and the live response.)

## Adaptive rubrics: draft a template, or generate-and-score

> **ADC-required — captured live.** Every command in this section makes a live
> Vertex AI call (Gemini adaptive rubric generation, plus the eval call itself)
> and needs ADC + a configured project. The output below was **run against live
> Vertex** (project `ghchinoy-genai-sa`, `us-central1`) and captured verbatim;
> the generated criteria, scores, and rationales are live autorater output and
> are **non-deterministic** — your criteria set and numbers will differ run to
> run. The `mizan: loaded env file …` notice each command writes to stderr is
> omitted for copy-pasteability. For the narrative and the framing (adaptive
> generation is an authoring aid, not a new metric kind) see
> [`docs/user-guide.md`](user-guide.md#adaptive-rubrics-authoring-aid).

Two entry points share the same generation primitive; they differ only in where
the generated rubric goes.

### Draft a reusable template (`mizan rubric generate`)

Generate criteria from a sample prompt and write a **draft** template YAML for
review. This writes nothing to the registry. Create the `--out` directory first —
`rubric generate` does not create it and fails with `write draft "…": … no such
file or directory` if it is missing:

```sh
$ mkdir -p drafts
$ mizan rubric generate \
    --sample "Write a concise product description for a wireless mouse." \
    --id acme/product-copy --out drafts/product-copy.yaml
GROUP            CRITERION                                                                                                                                                   TYPE                                        IMPORTANCE
general_quality  The response is in English.                                                                                                                                 LANGUAGE:PRIMARY_RESPONSE_LANGUAGE          HIGH
general_quality  The response is formatted as a product description.                                                                                                         FORMAT_REQUIREMENT:PRODUCT_DESCRIPTION      HIGH
general_quality  The product being described is a wireless mouse.                                                                                                            CONTENT_REQUIREMENT:PRODUCT_IDENTIFICATION  HIGH
general_quality  The product description is concise.                                                                                                                         FORMAT_REQUIREMENT:CONCISENESS              HIGH
general_quality  The description highlights key benefits or selling points of a wireless mouse.                                                                              CONTENT_REQUIREMENT:BENEFITS                HIGH
general_quality  The description mentions relevant technical features or characteristics typical of a wireless mouse (e.g., connectivity, battery life, design, precision).  CONTENT_REQUIREMENT:FEATURES                MEDIUM
general_quality  The language used is persuasive and engaging, aimed at attracting potential buyers.                                                                         STYLE_REQUIREMENT:PERSUASIVE_TONE           MEDIUM
mizan: wrote draft template acme/product-copy to drafts/product-copy.yaml — review/edit, then `mizan registry import`/`create` it and `mizan eval run --metric acme/product-copy`
```

The draft is a plain template YAML on disk, not a registry entry. `registry
import` reads a **pack tree**, not a loose file, so to load the reviewed draft
wrap it in a pack and import the pack — copy it under the pack's `templates/`
directory and import the pack dir:

```sh
$ mizan pack init packs/acme --name acme
initialized pack "packs/acme" (namespace "acme")

$ cp drafts/product-copy.yaml packs/acme/templates/product-copy.yaml   # after review/edit
$ mizan registry import packs/acme
1 inserted, 0 updated, 0 skipped, 0 conflicted, 0 unchanged, 0 forked (source: packs/acme)
  inserted: acme/product-copy
```

See [Template packs](#template-packs-author-validate-export--import) above for the
full pack author → validate → import loop. (Importing the loose file, `mizan
registry import drafts/product-copy.yaml`, instead errors with `source "…" is not
a directory`.)

### Generate-and-score in one step (`mizan eval adaptive`)

Generate criteria from a prompt and immediately score a response against them.
The generated rubric is held in memory and never persisted unless you pass
`--save-as`. The proposed criteria and the pre-flight target echo go to
**stderr** (so `-o json` on stdout stays a single clean object); `Score` /
`Explanation` go to stdout:

```sh
$ mizan eval adaptive \
    --prompt "Write a concise product description for a wireless mouse." \
    --response "The Acme M1 is a compact wireless mouse with a 12-month battery, silent clicks, and a precise optical sensor."
mizan: generated rubric criteria:
GROUP            CRITERION                                                                                                                           TYPE                                        IMPORTANCE
general_quality  The response is in English.                                                                                                         LANGUAGE:PRIMARY_RESPONSE_LANGUAGE          HIGH
general_quality  The response is a product description.                                                                                              FORMAT_REQUIREMENT:PRODUCT_DESCRIPTION      MEDIUM
general_quality  The product being described is a wireless mouse.                                                                                    CONTENT_REQUIREMENT:PRODUCT_IDENTIFICATION  HIGH
general_quality  The product description is concise.                                                                                                 STYLE_REQUIREMENT:CONCISENESS               HIGH
general_quality  The description highlights key features of a wireless mouse (e.g., connectivity type, design, battery life, precision).             CONTENT_REQUIREMENT:PRODUCT_FEATURES        HIGH
general_quality  The description communicates benefits of using the wireless mouse (e.g., freedom of movement, comfort, productivity, portability).  CONTENT_REQUIREMENT:PRODUCT_BENEFITS        HIGH
general_quality  The description adopts an engaging and persuasive tone suitable for marketing.                                                      STYLE_REQUIREMENT:TONE_MARKETING            MEDIUM
mizan: autorater → project=ghchinoy-genai-sa (src=env) location=us-central1 (src=env) model=gemini-2.5-flash (path=native)
Score:        5
Explanation:  The response perfectly meets all criteria: it is in English, is a concise product description for a wireless mouse, highlights key features (wireless, compact, 12-month battery, silent clicks, precise optical sensor), implicitly communicates benefits (portability, comfort, productivity, freedom), and adopts an engaging, persuasive tone by presenting desirable attributes.
```

Add `--save-as <ns>/<slug>` to **freeze** the generated rubric into the registry
as an ordinary static template. This is the no-draft-file path — it needs no
`rubric generate`, no pack, no import. Freezing fails if the id already exists
(`error: registry: template "<id>" already exists`), so it never silently
overwrites:

```sh
$ mizan eval adaptive \
    --prompt "Write a concise product description for a wireless mouse." \
    --response "The Acme M1 is a compact wireless mouse with a 12-month battery, silent clicks, and a precise optical sensor." \
    --save-as acme/product-copy
# … generated-criteria table + Score/Explanation on stderr/stdout as above …
mizan: froze generated rubric as acme/product-copy — rerun it with `mizan eval run --metric acme/product-copy`
```

The frozen template declares `prompt` and `response` inputs, so rerun it like any
other rubric template — the same deterministic eval path, reproducible from the
registry:

```sh
$ mizan eval run --metric acme/product-copy \
    --field prompt="Write a concise product description for a wireless mouse." \
    --field response="The Acme M1 is a compact wireless mouse with a 12-month battery, silent clicks, and a precise optical sensor."
mizan: autorater → project=ghchinoy-genai-sa (src=env) location=us-central1 (src=env) model=gemini-2.5-flash (path=native)
Score:        4.5
Explanation:  The response effectively functions as a concise product description, clearly identifying the product and highlighting key features in clear English; however, it could more explicitly emphasize benefits and use slightly stronger persuasive language.
```

### Verify generation provenance and content-hash auditability

> **No live call needed here.** The checks below inspect the draft file and the
> local registry produced by the steps above (`registry get`/`import` are local,
> ADC-free). They confirm the two auditability guarantees: every AI-drafted rubric
> carries `rubricProvenance`, and its `contentHash` moves when you edit it. For the
> field reference see [`docs/user-guide.md`](user-guide.md#generation-provenance-rubricprovenance)
> and RFC-0001 §13.

**1. The draft carries `rubricProvenance` (CUJ 7).** After `rubric generate`
writes `drafts/product-copy.yaml`, the block is right there in the file:

```sh
$ grep -A9 'rubricProvenance:' drafts/product-copy.yaml
  rubricProvenance:
    method: adaptive-generated
    generatorModel: gemini-2.5-flash
    recipe: general_quality_v1
    sampleInputRef: 'inline:"Write a concise product description for a wireless mouse." sha256:…'
    generatedAt: 2026-08-17T00:00:00Z
    apiVersion: v1beta1:generateInstanceRubrics
    rubricMeta:
      - {group: general_quality, criterion: The response is in English., type: 'LANGUAGE:PRIMARY_RESPONSE_LANGUAGE', importance: HIGH}
```

**2. Provenance round-trips into the registry — both paths.** Whether you imported
a reviewed pack (CUJ 7) or froze with `eval adaptive --save-as` (CUJ 8), the stored
template returns its provenance and the frozen rubric's `ContentHash` from
`registry get -o json`:

```sh
$ mizan registry get acme/product-copy -o json \
    | jq '{method: .rubricProvenance.method, model: .rubricProvenance.generatorModel, recipe: .rubricProvenance.recipe, hash: .ContentHash}'
{
  "method": "adaptive-generated",
  "model": "gemini-2.5-flash",
  "recipe": "general_quality_v1",
  "hash": "sha256:…"
}
```

A hand-authored template has no such block — the same query returns
`"method": null` — so you can always tell an AI-drafted rubric from a hand-authored
one straight out of the registry.

**3. Editing the rubric changes its `contentHash` (auditability).** `rubricProvenance`
and the criteria are both part of the hash, so any edit is detectable. Capture the
hash, reword one criterion in the pack template and bump `metadata.version`,
re-import, and compare:

```sh
$ mizan registry get acme/product-copy -o json | jq -r .ContentHash
sha256:1f3a…                       # before

# edit packs/acme/templates/product-copy.yaml: reword a criterion, bump metadata.version
$ mizan registry import packs/acme
0 inserted, 1 updated, 0 skipped, 0 conflicted, 0 unchanged, 0 forked (source: packs/acme)
  updated: acme/product-copy

$ mizan registry get acme/product-copy -o json | jq -r .ContentHash
sha256:9c72…                       # after — different: the edit is captured in the hash
```

(An edit *without* a version bump is reported as `1 conflicted` under the default
`newer` strategy, not silently applied — bump the version to record the change.)

## Global-only judge auto-routing

> **ADC-required — captured live.** Every command in this section makes a live
> Vertex AI call and needs ADC + a configured project. The known-global-only
> fast-path (running `gemini-3.5-flash` from a regional config) and the regional
> contrast case below were **run against live Vertex** (project
> `ghchinoy-genai-sa`); their pre-flight echoes, routing notice, and results are
> captured verbatim. The only output still transcribed from source is the
> **self-correcting retry** notice — it fires only for a global-only judge that
> is *not* on the known-prefix list, so it cannot be triggered with a documented
> model — and it is flagged inline. For the full narrative see Scenario 7 in
> [`docs/llm-as-judge-scenarios.md`](llm-as-judge-scenarios.md#scenario-7-choose-the-judge-model),
> which cites the `design/spike-eval-region-autorater.md` spike; the deciding
> factor is the eval endpoint **host**, not the autorater's location path.

Some newer judges — the `gemini-3.5` family (`gemini-3.5-flash` /
`-flash-lite`) — are **global-only**: they resolve only on Vertex's global eval
host and 404 on a regional native endpoint. When the resolved autorater is
global-only, Mizan forces the whole native `EvaluateInstances` call onto the
global host, regardless of your `--location`. You don't set `--location global`
by hand. (The genai paths — `custom_schema` and rubric `--rubric-detail` —
already run at `location=global` by design, so this routing only concerns the
native `pointwise` / `rubric` / `pairwise` paths.)

**Run a global-only judge from a regional config and watch it route.** Set a
regional location (e.g. `us-central1`) and select a known global-only judge with
`--model`:

```sh
$ mizan eval run --metric demo/conciseness --model gemini-3.5-flash \
    --field response="The cat sat on the mat."
```

All of the following go to **stderr** (so they never pollute `--output json` on
stdout):

1. The pre-flight echo shows `location=global` **up front** for a known
   global-only judge (the fast-path classifies it before the call), with
   `path=native`:

   ```
   mizan: autorater → project=ghchinoy-genai-sa (src=env-file) location=global (src=global-route) model=gemini-3.5-flash (path=native)
   ```

   The `(src=…)` hints report where each value resolved — `env` / `env-file` /
   `default` for a configured value, `flag` for a `--project` override, `model`
   for a fully-qualified model resource, `global-path` for the always-global
   genai path, and `global-route` for this native auto-routing — mirroring the
   `SOURCE` column in `mizan config show` (see
   [`docs/user-guide.md`](user-guide.md#configure)). The forced-global `location`
   reads `src=global-route` because the global-only **routing** (not a
   fully-qualified model resource) moved this native call to the global host; a
   resource explicitly pinned to global would instead read `src=model`.

2. A forced-global notice (the known-prefix fast-path), captured verbatim:

   ```
   mizan: autorater gemini-3.5-flash is global-only (gemini-3.5-flash is a known global-only judge); routing this eval to the GLOBAL host (location=global). Your configured --location is kept for labeling only.
   ```

3. Then the ordinary successful result of the underlying kind — here a pointwise
   `Score` + `Explanation`, captured live:

   ```
   Score:        1
   Explanation:  The response is extremely brief, conveying the complete thought with absolutely no redundant words or fluff.
   ```

**The self-correcting retry (safety net).** For a global-only model *not* in the
known-prefix list, the first regional attempt fails with a narrow gRPC
`NotFound` + "autorater model not found", and Mizan transparently retries the
same eval on the global host, printing a sibling notice at run time. This path
cannot be triggered with a documented model (every known global-only judge takes
the fast-path above), so this notice is **still transcribed from source**, not
captured live:

```
mizan: autorater <model> not found in location <loc>; retrying this eval on the global host (<host>, location=global). Your configured --location is kept for labeling only.
```

(A non-autorater `NOT_FOUND`, e.g. a missing template, is **not** retried.)

**Contrast case — a regional judge stays regional.** Run the same metric with
the built-in-default-class regional judge `gemini-2.5-flash` from `us-central1`:

```sh
$ mizan eval run --metric demo/conciseness --model gemini-2.5-flash \
    --field response="The cat sat on the mat."
```

The pre-flight echo shows **your region**, there is **no** routing notice, and
the call stays regional (captured live; here `location` resolved from the
built-in default, hence `src=default`):

```
mizan: autorater → project=ghchinoy-genai-sa (src=env-file) location=us-central1 (src=default) model=gemini-2.5-flash (path=native)
```

For a global-only judge, `--location` / `MIZAN_LOCATION` is kept for output
**labeling only** — it is not honored as a residency region for that run,
because the judge cannot run in your region.

## Per-run stats (`--stats`)

> **ADC-required — captured live.** The commands below make live Vertex AI eval
> calls and need ADC + a configured project. Captured verbatim against live
> Vertex (project `ghchinoy-genai-sa`, `us-central1`) with a scratch
> `MIZAN_REGISTRY_DB` holding `demo/conciseness` (pointwise, from the
> [Text pointwise](#text-pointwise) recipe) and `demo/rubric-quality` (rubric,
> from the [Rubric](#rubric) recipe). The pre-flight echo (stderr) is elided here
> to isolate the footer; `Duration` is wall-clock and varies run to run.

`--stats` adds a footer to `eval run` with the run's **duration** (always
measured) and, on the genai path only, **token usage**.

**Native path** (pointwise/rubric/pairwise via `EvaluateInstances`) reports
duration but no token usage — the native API returns no usage metadata, so the
footer says so explicitly instead of showing zeros:

```sh
$ mizan eval run --metric demo/conciseness --field response="The cat sat on the mat." --stats
Score:        1
Explanation:  The response is a very short, direct, and common example sentence that conveys its meaning without any unnecessary words, making it highly concise.
Duration:     1.703s
Tokens:       token usage not available on this path (native EvaluateInstances returns no usage)
```

**Genai path** (a `custom_schema` template, or any run with `--rubric-detail`)
reports both duration and `prompt` / `candidates` / `total` token counts:

```sh
$ mizan eval run --metric demo/rubric-quality --rubric-detail --rubric-scale 1-5 \
    --field response="The Eiffel Tower is in Paris, France, completed in 1889." --stats
Score:        5
Explanation:  The response is perfectly clear, free of jargon, and entirely accurate in its factual content. It provides a concise and correct piece of information.
Per-criterion:
GROUP        CRITERION                        SCORE  RATIONALE
clarity      Is the response clear            5      The response is very clear and easy to understand.
clarity      Is it free of jargon             5      The response uses simple, common language with no jargon.
correctness  Is the factual content accurate  5      The statement 'The Eiffel Tower is in Paris, France, completed in 1889' is factually correct.
Duration:  2.68s
Tokens:    prompt=168 candidates=229 total=595
```

In `-o json` the footer becomes a `"Stats"` object — always `"duration_ns"`, plus
a nested `"token_usage"` (`prompt_tokens` / `candidates_tokens` / `total_tokens`)
on the genai path. Native path (no `token_usage` key):

```sh
$ mizan eval run --metric demo/conciseness --field response="The cat sat on the mat." --stats -o json
{
  "Score": 1,
  "PairwiseChoice": "",
  "Explanation": "The sentence 'The cat sat on the mat' is a classic, simple, and direct statement that conveys its meaning with no superfluous words, making it highly concise.",
  "RawOutput": null,
  "CustomOutput": null,
  "Stats": {
    "duration_ns": 2128522658
  }
}
```

On the genai path the same `"Stats"` object additionally carries
`"token_usage": { "prompt_tokens": …, "candidates_tokens": …, "total_tokens": … }`.

See [`docs/llm-as-judge-scenarios.md`](llm-as-judge-scenarios.md#scenario-8-see-what-a-run-cost-and-where-it-went)
(Scenario 8) for the fuller narrative, including how `--stats` pairs with the
always-on pre-flight target echo.

## What each result means

- **`Score` + `Explanation`** (pointwise and rubric) — demonstrated live
  above for both. A float score plus free-text rationale.
- **`CustomOutput`** (`custom_schema`) — a typed JSON object matching the
  template's `ResponseSchema`, surfaced as `map[string]any` on
  `eval.Result.CustomOutput` (source: `internal/eval/engine.go`'s `Result`
  struct). **Demonstrated live above** (the `demo/custom-compliance` run) —
  `Score`/`Explanation` print empty, and one `CustomOutput[field]` line is
  printed per property in your `ResponseSchema`.
- **`PairwiseChoice`** (pairwise) — an enum-like string result:
  `BASELINE=1`, `CANDIDATE=2`, `TIE=3` (source:
  [`docs/architecture-final.md`](architecture-final.md) §6). **Demonstrated
  live above** as the `Choice:` line in the `eval pairwise` output — expect
  it to vary run to run since it's a live autorater judgment.

## Dev & CI setup

Contributor-facing quality gates, all runnable locally with no project/ADC. The
`make` targets below were run against this checkout (output captured verbatim).

- **`make test`** — runs the full test suite (unit, creds-free end-to-end smoke
  tests across all metric kinds, and golden-output tests).
- **`make lint`** — runs `golangci-lint` (v2.12.2; curated set in
  `.golangci.yml`) over the module. Part of CI. Verified clean (`0 issues.`).
- **`make cover`** — one CGO-free `go test -coverprofile` run; writes
  `coverage.out` and prints the total. CI enforces a **soft, non-blocking**
  coverage floor — it warns below the floor but never fails the build, so don't
  treat it as a gate.
- **CI runs on PRs** (`.github/workflows/ci.yml`): build, `go vet`, `gofmt`
  check, `go test`, `govulncheck`, and coverage, plus `golangci-lint` and a
  `doc-drift` guard as separate jobs.

**Docs Definition of Done (PR template).** The
[`pull_request_template.md`](../.github/pull_request_template.md) checklist
requires each PR to update the user/reference docs, the scenarios doc + decision
matrix, and add or verify a `testing-guide.md` recipe for the change — or mark
each item **`docs: N/A`** with a reason (the `doc-drift` CI job enforces this).

## Not yet available

The following are **not built yet** — do not expect them to work, and don't
write test recipes against them. Each was re-verified absent from the built
binary rather than assumed. See
[`docs/user-guide.md`](user-guide.md#coming-soon--roadmap) for the roadmap.

- **Batch evaluation** (`EvaluateDataset` over GCS-hosted datasets) — no such
  command exists yet.
- **The Wails desktop app** (`cmd/mizan-desktop`) — design-stage scaffolding
  only; no built or runnable desktop app.
