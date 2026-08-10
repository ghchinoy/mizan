package eval

// global_route_kinds_test.go extends the R-GLOBAL auto-routing coverage in
// global_route_test.go along two axes the original file left thin:
//
//   - ALL NATIVE KINDS, not just pointwise: rubric and pairwise also flow through
//     evaluateRouted, so the prefix fast-path and the self-correcting retry are
//     proven for every native EvaluateInstances kind (table-driven).
//   - custom_schema is GLOBAL BY DESIGN: it takes the genai path (location=global),
//     so the native regional/global routing seam is never touched — asserted so
//     "all kinds are handled" is unambiguous rather than an omission.
//   - the forced-global NOTICE is captured and asserted deterministically on both
//     the fast-path and the retry route (and proven ABSENT for a regional judge).
//
// Like global_route_test.go it injects TWO DISTINCT fakes (regional vs global) and
// asserts routing by WHICH fake is called plus the request Location — the fake
// hides the host, so this is how routing is observable.

import (
	"context"
	"strings"
	"testing"

	aiplatformpb "cloud.google.com/go/aiplatform/apiv1beta1/aiplatformpb"

	"github.com/ghchinoy/mizan/internal/eval/evaltest"
	"github.com/ghchinoy/mizan/internal/registry"
)

// nativeKind is one native EvaluateInstances metric kind that R-GLOBAL routing
// must cover. tmpl builds the kind's template with a given autorater model so the
// same case drives both the fast-path (a known global-only model) and the retry
// (a global-only model NOT in the prefix table). resp is the success payload the
// GLOBAL fake returns; verify asserts the mapped Result for that kind.
type nativeKind struct {
	name   string
	tmpl   func(model string) registry.MetricTemplate
	inst   Instance
	resp   func() *aiplatformpb.EvaluateInstancesResponse
	verify func(t *testing.T, res Result)
}

func nativeKinds() []nativeKind {
	return []nativeKind{
		{
			name: "pointwise",
			tmpl: func(m string) registry.MetricTemplate { t := pointwiseTemplate(); t.AutoraterModel = m; return t },
			inst: helpfulnessInstance(),
			resp: func() *aiplatformpb.EvaluateInstancesResponse { return evaltest.NewPointwiseResponse(5, "great") },
			verify: func(t *testing.T, res Result) {
				t.Helper()
				if res.Score == nil || *res.Score != 5 {
					t.Errorf("pointwise Score = %v, want 5", res.Score)
				}
			},
		},
		{
			name: "rubric",
			tmpl: func(m string) registry.MetricTemplate { t := rubricTemplate(); t.AutoraterModel = m; return t },
			inst: rubricInstance(),
			// The native rubric path reuses the pointwise result shape (score/explanation).
			resp: func() *aiplatformpb.EvaluateInstancesResponse { return evaltest.NewPointwiseResponse(3, "on brand") },
			verify: func(t *testing.T, res Result) {
				t.Helper()
				if res.Score == nil || *res.Score != 3 {
					t.Errorf("rubric Score = %v, want 3", res.Score)
				}
			},
		},
		{
			name: "pairwise",
			tmpl: func(m string) registry.MetricTemplate { t := pairwiseTemplate(); t.AutoraterModel = m; return t },
			inst: pairwiseInstance(),
			resp: func() *aiplatformpb.EvaluateInstancesResponse {
				return evaltest.NewPairwiseResponse(aiplatformpb.PairwiseChoice_BASELINE, "A is better")
			},
			verify: func(t *testing.T, res Result) {
				t.Helper()
				if res.PairwiseChoice != "BASELINE" {
					t.Errorf("pairwise PairwiseChoice = %q, want BASELINE", res.PairwiseChoice)
				}
			},
		},
	}
}

// 5a. prefix fast-path: for EVERY native kind, a KNOWN global-only judge routes
// straight to the GLOBAL fake (Location=.../locations/global, autorater expanded
// at global) and NEVER touches the regional fake.
func TestRoute_AllNativeKinds_FastPathRoutesGlobal(t *testing.T) {
	for _, k := range nativeKinds() {
		k := k
		t.Run(k.name, func(t *testing.T) {
			regional := &evaltest.FakeEvaluationClient{}
			global := (&evaltest.FakeEvaluationClient{}).PushResponse(k.resp())
			eng := newRoutingEngine("us-central1", regional, global)

			res, err := eng.Run(context.Background(), k.tmpl("gemini-3.5-flash"), k.inst)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			k.verify(t, res)

			if regional.Calls() != 0 {
				t.Errorf("regional client got %d calls, want 0 (fast-path must skip the regional host)", regional.Calls())
			}
			if global.Calls() != 1 {
				t.Fatalf("global client got %d calls, want 1", global.Calls())
			}
			if want := "projects/my-project/locations/global"; global.LastRequest().Location != want {
				t.Errorf("global request Location = %q, want %q", global.LastRequest().Location, want)
			}
			if got := global.LastRequest().GetAutoraterConfig().GetAutoraterModel(); !strings.Contains(got, "/locations/global/") {
				t.Errorf("autorater = %q, want it expanded at locations/global", got)
			}
		})
	}
}

// 5b. self-correcting retry: for EVERY native kind, a global-only judge NOT in the
// prefix table is attempted regionally, 404s with the SPECIFIC autorater-not-found
// error, and is transparently retried on the GLOBAL fake -> success. Regional gets
// exactly one attempt, global gets exactly one retry.
func TestRoute_AllNativeKinds_SelfCorrectingRetry(t *testing.T) {
	for _, k := range nativeKinds() {
		k := k
		t.Run(k.name, func(t *testing.T) {
			regional := (&evaltest.FakeEvaluationClient{}).PushError(
				autoraterNotFound("projects/my-project/locations/us-central1/publishers/google/models/gemini-4.0-preview"))
			global := (&evaltest.FakeEvaluationClient{}).PushResponse(k.resp())
			eng := newRoutingEngine("us-central1", regional, global)

			// gemini-4.0-preview: global-only but NOT in the prefix table -> retry path.
			res, err := eng.Run(context.Background(), k.tmpl("gemini-4.0-preview"), k.inst)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			k.verify(t, res)

			if regional.Calls() != 1 {
				t.Errorf("regional client got %d calls, want 1 (one regional attempt before the retry)", regional.Calls())
			}
			if global.Calls() != 1 {
				t.Fatalf("global client got %d calls, want 1 (the transparent retry)", global.Calls())
			}
			if want := "projects/my-project/locations/global"; global.LastRequest().Location != want {
				t.Errorf("retry request Location = %q, want %q", global.LastRequest().Location, want)
			}
			if got := global.LastRequest().GetAutoraterConfig().GetAutoraterModel(); !strings.Contains(got, "/locations/global/") {
				t.Errorf("retry autorater = %q, want it expanded at locations/global", got)
			}
		})
	}
}

// 5c. custom_schema is GLOBAL BY DESIGN: it takes the genai path (location=global),
// so even a global-only judge is served there and the native EvaluateInstances
// routing seam (the regional/global fakes) is NEVER touched. This is why the
// native regional->global fast-path/retry does not — and must not — apply to it.
func TestRoute_CustomSchema_IsGlobalByDesign_GenaiPath(t *testing.T) {
	regional := &evaltest.FakeEvaluationClient{}
	global := &evaltest.FakeEvaluationClient{}
	genai := (&evaltest.FakeGenaiClient{}).PushJSON(
		`{"overall_score":8.5,"compliant":true,"flagged_issues":[],"explanation":"On brand."}`, nil)
	eng := NewEngine(regional, "my-project", "us-central1",
		WithGlobalClient(global),
		WithGenaiClient(genai),
		WithNoticeWriter(&strings.Builder{}),
	)

	tmpl := customSchemaTemplate()
	tmpl.AutoraterModel = "gemini-3.5-flash" // even a KNOWN global-only judge...
	_, err := eng.Run(context.Background(), tmpl, Instance{
		Fields: map[string]AssetRef{"creative": {Modality: registry.ModalityText, Text: "A calm blue banner."}},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if genai.Calls() != 1 {
		t.Errorf("genai client got %d calls, want 1 (custom_schema uses the genai path)", genai.Calls())
	}
	if regional.Calls() != 0 || global.Calls() != 0 {
		t.Errorf("native routing seam touched: regional=%d global=%d, want 0/0 (custom_schema is genai-global-by-design)",
			regional.Calls(), global.Calls())
	}

	// The pre-flight echo agrees: genai path, location=global — no native routing needed.
	got := eng.Resolve(tmpl, "", false)
	if got.Path != "genai" || got.Location != GenaiLocation {
		t.Errorf("Resolve = {Path:%q Location:%q}, want {genai %q}", got.Path, got.Location, GenaiLocation)
	}
}

// newRoutingEngineCapture is newRoutingEngine but returns the notice writer so a
// test can assert the forced-global notice deterministically.
func newRoutingEngineCapture(location string, regional, global *evaltest.FakeEvaluationClient) (*Engine, *strings.Builder) {
	var w strings.Builder
	eng := NewEngine(regional, "my-project", location,
		WithGlobalClient(global),
		WithNoticeWriter(&w),
	)
	return eng, &w
}

// 7a. surfacing (fast-path): the forced-global notice is emitted to the writer on
// the prefix fast-path route, naming the model, the "known global-only judge"
// reason, and location=global.
func TestNotice_FastPath_EmittedToWriter(t *testing.T) {
	regional := &evaltest.FakeEvaluationClient{}
	global := (&evaltest.FakeEvaluationClient{}).PushResponse(evaltest.NewPointwiseResponse(5, "x"))
	eng, w := newRoutingEngineCapture("us-central1", regional, global)

	if _, err := eng.Run(context.Background(), globalOnlyTemplate(), helpfulnessInstance()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	notice := w.String()
	for _, want := range []string{"gemini-3.5-flash", "global-only", "known global-only judge", "location=global"} {
		if !strings.Contains(notice, want) {
			t.Errorf("fast-path notice %q missing %q", notice, want)
		}
	}
}

// 7b. surfacing (retry): the retry notice is emitted on the self-correcting retry
// route. Unlike the fast-path it does NOT assert the model is "global-only" (the
// retry also fires for a typo'd/unresolvable regional model) — it describes the
// ACTION: not found in the configured location, retrying on the global host.
func TestNotice_Retry_EmittedToWriter(t *testing.T) {
	regional := (&evaltest.FakeEvaluationClient{}).PushError(
		autoraterNotFound("projects/my-project/locations/us-central1/publishers/google/models/gemini-4.0-preview"))
	global := (&evaltest.FakeEvaluationClient{}).PushResponse(evaltest.NewPointwiseResponse(3, "recovered"))
	eng, w := newRoutingEngineCapture("us-central1", regional, global)

	tmpl := pointwiseTemplate()
	tmpl.AutoraterModel = "gemini-4.0-preview"
	if _, err := eng.Run(context.Background(), tmpl, helpfulnessInstance()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	notice := w.String()
	for _, want := range []string{"gemini-4.0-preview", "not found in location", "us-central1", "retrying", "aiplatform.googleapis.com", "location=global"} {
		if !strings.Contains(notice, want) {
			t.Errorf("retry notice %q missing %q", notice, want)
		}
	}
	// The retry notice must NOT overclaim the model as global-only.
	if strings.Contains(notice, "global-only") {
		t.Errorf("retry notice %q must not assert the model is global-only", notice)
	}
}

// 7c. surfacing (negative): a regional (non-global-only) judge emits NO
// forced-global notice — the writer stays empty.
func TestNotice_RegionalJudge_NoNotice(t *testing.T) {
	regional := (&evaltest.FakeEvaluationClient{}).PushResponse(evaltest.NewPointwiseResponse(4, "ok"))
	global := &evaltest.FakeEvaluationClient{}
	eng, w := newRoutingEngineCapture("us-central1", regional, global)

	if _, err := eng.Run(context.Background(), pointwiseTemplate(), helpfulnessInstance()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if w.Len() != 0 {
		t.Errorf("regional judge emitted an unexpected forced-global notice: %q", w.String())
	}
}
