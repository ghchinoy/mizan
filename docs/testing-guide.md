# Mizan Testing Guide

This guide is for **exercising each built capability with real, copy-pasteable
commands** — it's a hands-on companion, not a replacement for the narrative
docs. For conceptual/narrative detail (what a flag means, how config
resolution works, full CRUD walkthroughs), see
[`docs/user-guide.md`](user-guide.md). For phase-by-phase roadmap detail and
work-item IDs (`WI-P1-*`), see
[`docs/implementation-plan.md`](implementation-plan.md).

Every command and every line of output below was run, in this pass, against a
binary built from current `main` (PRs #1–#11 merged — **Phase 1 is complete**:
all four metric kinds — pointwise, rubric, custom_schema, pairwise — and
multimodal (image/audio/video/music) are implemented and CLI-runnable
end-to-end). Nothing here is copied from another doc or invented; scores and
explanations are live autorater output and are expected to vary run-to-run.

## Minimal setup

```sh
go install github.com/ghchinoy/mizan/cmd/mizan@main
mizan config set project-id <your-project-id>
mizan config set location us-central1   # default; the "us" multi-region 404s
gcloud auth application-default login   # ADC — no API-key auth path exists
```

Use a scratch registry DB per test session so you don't collide with a real
registry:

```sh
export MIZAN_REGISTRY_DB=/tmp/mizan-testing.db
```

See [`docs/user-guide.md`](user-guide.md#prerequisites) for the full
prerequisites/install/config explanation (env-var overrides, `.env` file
location, the custom-endpoint safeguard, etc.) — it isn't repeated here.

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
`LLMBasedMetricSpec`/structured `rubric_groups` field to pass them as), and is
now fully CLI-authorable as of PR #11 (`WI-P1-6`): `registry create`/`update`
take a repeatable `--rubric-group "name=criterion one;criterion two"` flag, or
`--rubric-groups-file <path>` pointing at a JSON object of
`{"group": ["crit1", "crit2"], ...}`.

Creating a rubric template with **no** rubric flag now fails immediately at
create time (this is new, better UX from PR #11 — previously it saved and
only failed at `eval run`):

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

Same underlying path as rubric for CLI-authoring status: the engine
(`internal/eval/custom.go`'s `runCustomSchema`, calling
`genai.GenerateContent` with a `ResponseSchema` and exponential backoff) has
been implemented and integration-tested since PR #6, and PR #11 added the
CLI-authoring flags: `--response-schema '<json>'` (inline) or
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

Pairwise now has a full native engine path (`internal/eval/pairwise.go`) and
a dedicated `mizan eval pairwise` CLI command (added in PR #9), confirmed via
`--help`:

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
Score:        (none)
Choice:       BASELINE
Explanation:  The baseline response is more direct and concise, providing only the information specifically asked for in the question, which is generally preferred for simple factual queries.
```

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

### flip-enabled known P1 limitation

`registry create --kind pairwise` accepts a `--flip-enabled` flag (default
`true`), but **P1 always runs pairwise with flip enabled regardless of the
flag's value** — the registry's `FlipEnabled` field is a plain `bool`, which
can't represent an explicit "false" distinctly from "unset" (both are the Go
zero value), so P1 cannot honor an explicit opt-out
(source: `internal/eval/pairwise.go` comments, and
`docs/project-log/p1-wi4-multimodal-pairwise-mizan-p1-dev-5.md`). Passing
`--flip-enabled=false` at create time is accepted without error, but does
**not** actually disable flipping — treat that as an honest gap, not a
working toggle, until a tri-state field lands (tracked as P2 registry work).

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

## Not yet testable / roadmap

Phase 1 is complete, so this list now only covers P2/P3/P4 work — unrelated
to the P1 metric-kind work above, but re-verified absent from the built
binary in this pass rather than assumed unchanged. Kept consistent with
[`docs/user-guide.md`](user-guide.md#coming-soon--roadmap) — see that section
for the full picture.

- **Template packs and registry import/export** — `mizan pack` and
  `mizan registry import`/`export` do not exist:

  ```sh
  $ mizan pack
  Error: unknown command "pack" for "mizan"

  $ mizan registry --help
  Available Commands:
    create      Create a metric template
    delete      Delete a metric template
    get         Show a metric template
    list        List metric templates
    update      Update a metric template
  ```

  (No `import`/`export` subcommand is listed.) Roadmap phase P2.
- **Batch evaluation** (`EvaluateDataset` over GCS-hosted datasets) — no such
  command exists yet. Roadmap phase P3.
- **The Wails desktop app** (`cmd/mizan-desktop`) — design-stage scaffolding
  only; no built or runnable desktop app. Roadmap phase P4.

See [`docs/implementation-plan.md`](implementation-plan.md) for the
work-item breakdown and acceptance criteria behind each of these.
