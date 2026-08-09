package eval

import (
	"context"
	"strings"
	"testing"

	aiplatformpb "cloud.google.com/go/aiplatform/apiv1beta1/aiplatformpb"
	"google.golang.org/genai"
	"google.golang.org/protobuf/proto"

	"github.com/ghchinoy/mizan/internal/registry"
)

// TestResolveModelPrecedence exercises the WI-F3 precedence chain
// (flag > template > config default > built-in) directly on the resolver.
func TestResolveModelPrecedence(t *testing.T) {
	cases := []struct {
		name        string
		override    string // eval-time --model
		tmplModel   string // template.AutoraterModel
		configModel string // config default-model (engine.defaultModel)
		want        string
	}{
		{"flag wins over all", "flag-model", "tmpl-model", "cfg-model", "flag-model"},
		{"template when no flag", "", "tmpl-model", "cfg-model", "tmpl-model"},
		{"config when no flag/template", "", "", "cfg-model", "cfg-model"},
		{"built-in when nothing set", "", "", "", BuiltinDefaultModel},
		{"flag over template only", "flag-model", "tmpl-model", "", "flag-model"},
		{"config over built-in", "", "", "cfg-model", "cfg-model"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			eng := NewEngine(&fakeClient{}, "p", "us-central1", WithDefaultModel(tc.configModel))
			tmpl := registry.MetricTemplate{AutoraterModel: tc.tmplModel}
			if got := eng.resolveModel(tmpl, tc.override); got != tc.want {
				t.Errorf("resolveModel = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestResolveModelNativePathExpanded verifies the resolved model reaches the
// native EvaluateInstances request expanded to the full resource name, honoring
// precedence end-to-end (flag > template > config > built-in).
func TestResolveModelNativePathExpanded(t *testing.T) {
	cases := []struct {
		name        string
		override    string
		tmplModel   string
		configModel string
		wantBare    string
	}{
		{"flag override", "gemini-flag", "gemini-tmpl", "gemini-cfg", "gemini-flag"},
		{"template", "", "gemini-tmpl", "gemini-cfg", "gemini-tmpl"},
		{"config default", "", "", "gemini-cfg", "gemini-cfg"},
		{"built-in fallback", "", "", "", BuiltinDefaultModel},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fc := &fakeClient{
				resp: &aiplatformpb.EvaluateInstancesResponse{
					EvaluationResults: &aiplatformpb.EvaluateInstancesResponse_PointwiseMetricResult{
						PointwiseMetricResult: &aiplatformpb.PointwiseMetricResult{
							Score: proto.Float32(1), Explanation: "ok",
						},
					},
				},
			}
			eng := NewEngine(fc, "proj", "us-central1", WithDefaultModel(tc.configModel))
			tmpl := pointwiseTemplate()
			tmpl.AutoraterModel = tc.tmplModel

			_, err := eng.Run(context.Background(), tmpl, Instance{
				Fields: map[string]AssetRef{"response": {Text: "hi"}},
			}, WithModel(tc.override))
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			want := "projects/proj/locations/us-central1/publishers/google/models/" + tc.wantBare
			if got := fc.gotReq.GetAutoraterConfig().GetAutoraterModel(); got != want {
				t.Errorf("AutoraterModel = %q, want %q", got, want)
			}
		})
	}
}

// TestResolveModelGenaiPath verifies the resolved model reaches the genai
// (custom_schema) path as the bare id, honoring the same precedence chain — in
// particular that an empty template model inherits the config default (and, with
// no config, the built-in) instead of erroring.
func TestResolveModelGenaiPath(t *testing.T) {
	cases := []struct {
		name        string
		override    string
		tmplModel   string
		configModel string
		want        string
	}{
		{"flag override", "gemini-flag", "gemini-tmpl", "gemini-cfg", "gemini-flag"},
		{"template", "", "gemini-tmpl", "gemini-cfg", "gemini-tmpl"},
		{"config default (empty template inherits)", "", "", "gemini-cfg", "gemini-cfg"},
		{"built-in fallback", "", "", "", BuiltinDefaultModel},
		{"publisher-relative reduced to bare", "publishers/google/models/gemini-x", "", "", "gemini-x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fg := &fakeGenai{respText: `{"overall_score":1,"compliant":true,"flagged_issues":[],"explanation":"x"}`}
			eng := NewEngine(&fakeClient{}, "p", "us-central1",
				WithGenaiClient(fg), WithDefaultModel(tc.configModel))
			tmpl := customSchemaTemplate()
			tmpl.AutoraterModel = tc.tmplModel

			_, err := eng.Run(context.Background(), tmpl, Instance{
				Fields: map[string]AssetRef{"creative": {Text: "x"}},
			}, WithModel(tc.override))
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if fg.gotModel != tc.want {
				t.Errorf("genai model = %q, want %q", fg.gotModel, tc.want)
			}
		})
	}
}

// TestResolveReflectsPerPathLocation checks the pre-flight ResolvedTarget (WI-F7)
// reports the regional location for native kinds and global for custom_schema,
// with the model run through the precedence chain.
func TestResolveReflectsPerPathLocation(t *testing.T) {
	eng := NewEngine(&fakeClient{}, "proj", "us-central1", WithDefaultModel("cfg-model"))

	native := eng.Resolve(pointwiseTemplate(), "")
	if native.Path != "native" || native.Location != "us-central1" {
		t.Errorf("native target = %+v, want path=native location=us-central1", native)
	}
	if native.Model != "gemini-2.5-flash" { // template pins this
		t.Errorf("native model = %q, want template value", native.Model)
	}
	if native.Project != "proj" {
		t.Errorf("project = %q, want proj", native.Project)
	}

	genai := eng.Resolve(customSchemaTemplate(), "flag-model")
	if genai.Path != "genai" || genai.Location != GenaiLocation {
		t.Errorf("genai target = %+v, want path=genai location=%s", genai, GenaiLocation)
	}
	if genai.Model != "flag-model" { // override wins
		t.Errorf("genai model = %q, want flag-model", genai.Model)
	}

	// Empty template model on the genai path inherits the config default.
	tmpl := customSchemaTemplate()
	tmpl.AutoraterModel = ""
	if got := eng.Resolve(tmpl, "").Model; got != "cfg-model" {
		t.Errorf("empty-template genai model = %q, want cfg-model", got)
	}
}

// sanity: ensure Resolve's bare-id reduction matches genaiModelID for a
// fully-qualified resource name.
func TestResolveBareModelID(t *testing.T) {
	eng := NewEngine(&fakeClient{}, "p", "us-central1")
	tmpl := pointwiseTemplate()
	tmpl.AutoraterModel = "projects/x/locations/global/publishers/google/models/gemini-z"
	if got := eng.Resolve(tmpl, "").Model; got != "gemini-z" {
		t.Errorf("bare model = %q, want gemini-z", got)
	}
	if !strings.HasPrefix(BuiltinDefaultModel, "gemini-") {
		t.Errorf("BuiltinDefaultModel = %q looks wrong", BuiltinDefaultModel)
	}
}

// TestNativeEmptyModelInheritsBuiltin proves the native path no longer
// hard-errors on an empty template model: it inherits the built-in via the
// resolution chain and produces a valid expanded resource name (WI-F3 item 3).
func TestNativeEmptyModelInheritsBuiltin(t *testing.T) {
	fc := &fakeClient{
		resp: &aiplatformpb.EvaluateInstancesResponse{
			EvaluationResults: &aiplatformpb.EvaluateInstancesResponse_PointwiseMetricResult{
				PointwiseMetricResult: &aiplatformpb.PointwiseMetricResult{
					Score: proto.Float32(2), Explanation: "ok",
				},
			},
		},
	}
	eng := NewEngine(fc, "proj", "us-central1") // no config default
	tmpl := pointwiseTemplate()
	tmpl.AutoraterModel = "" // previously an error on the native path

	_, err := eng.Run(context.Background(), tmpl, Instance{
		Fields: map[string]AssetRef{"response": {Text: "hi"}},
	})
	if err != nil {
		t.Fatalf("Run with empty template model should inherit built-in, got: %v", err)
	}
	want := "projects/proj/locations/us-central1/publishers/google/models/" + BuiltinDefaultModel
	if got := fc.gotReq.GetAutoraterConfig().GetAutoraterModel(); got != want {
		t.Errorf("AutoraterModel = %q, want %q", got, want)
	}
}

// TestBuiltinDefaultModelValue LOCKS the literal value of the single built-in
// autorater id (WI-F3). The other precedence tests compare against the
// BuiltinDefaultModel symbol, so they would still pass if the constant were
// changed; this test pins the actual string so an accidental (or unreviewed)
// change to the coordinator-locked GA id is caught. The rationale (2.5-flash is
// served on BOTH regional native and location=global, unlike the global-only
// 3.5 ids) lives in model.go — changing this value is a deliberate act.
func TestBuiltinDefaultModelValue(t *testing.T) {
	if BuiltinDefaultModel != "gemini-2.5-flash" {
		t.Errorf("BuiltinDefaultModel = %q, want %q (coordinator-locked GA id)", BuiltinDefaultModel, "gemini-2.5-flash")
	}
	// The genai (custom_schema) path is global by design; the constant backing
	// the pre-flight echo and the wire composition root must agree on that.
	if GenaiLocation != "global" {
		t.Errorf("GenaiLocation = %q, want %q", GenaiLocation, "global")
	}
}

// --- WI-F4 stats plumbing ---

// TestStatsDurationAlwaysSet asserts Duration is populated on every path and
// TokenUsage is nil on the native path (no usage metadata available).
func TestStatsDurationAlwaysSet(t *testing.T) {
	fc := &fakeClient{
		resp: &aiplatformpb.EvaluateInstancesResponse{
			EvaluationResults: &aiplatformpb.EvaluateInstancesResponse_PointwiseMetricResult{
				PointwiseMetricResult: &aiplatformpb.PointwiseMetricResult{
					Score: proto.Float32(3), Explanation: "ok",
				},
			},
		},
	}
	eng := NewEngine(fc, "p", "us-central1")
	res, err := eng.Run(context.Background(), pointwiseTemplate(), Instance{
		Fields: map[string]AssetRef{"response": {Text: "hi"}},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Stats.Duration <= 0 {
		t.Errorf("Duration = %v, want > 0", res.Stats.Duration)
	}
	if res.Stats.TokenUsage != nil {
		t.Errorf("native TokenUsage = %+v, want nil", res.Stats.TokenUsage)
	}
}

// TestStatsTokenUsageGenaiPath asserts TokenUsage is populated from the genai
// response's UsageMetadata, and Duration is set.
func TestStatsTokenUsageGenaiPath(t *testing.T) {
	fg := &fakeGenaiWithUsage{
		respText: `{"overall_score":1,"compliant":true,"flagged_issues":[],"explanation":"x"}`,
		usage:    &genai.GenerateContentResponseUsageMetadata{PromptTokenCount: 12, CandidatesTokenCount: 34, TotalTokenCount: 46},
	}
	eng := NewEngine(&fakeClient{}, "p", "us-central1", WithGenaiClient(fg))
	res, err := eng.Run(context.Background(), customSchemaTemplate(), Instance{
		Fields: map[string]AssetRef{"creative": {Text: "x"}},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Stats.Duration <= 0 {
		t.Errorf("Duration = %v, want > 0", res.Stats.Duration)
	}
	tu := res.Stats.TokenUsage
	if tu == nil {
		t.Fatal("genai TokenUsage = nil, want populated")
	}
	if tu.PromptTokens != 12 || tu.CandidatesTokens != 34 || tu.TotalTokens != 46 {
		t.Errorf("TokenUsage = %+v, want {12,34,46}", tu)
	}
}

// TestStatsTokenUsageNilWhenNoMetadata asserts a genai response WITHOUT usage
// metadata leaves TokenUsage nil (defensive: the API may omit it).
func TestStatsTokenUsageNilWhenNoMetadata(t *testing.T) {
	fg := &fakeGenai{respText: `{"overall_score":1,"compliant":true,"flagged_issues":[],"explanation":"x"}`}
	eng := NewEngine(&fakeClient{}, "p", "us-central1", WithGenaiClient(fg))
	res, err := eng.Run(context.Background(), customSchemaTemplate(), Instance{
		Fields: map[string]AssetRef{"creative": {Text: "x"}},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Stats.TokenUsage != nil {
		t.Errorf("TokenUsage = %+v, want nil when response has no UsageMetadata", res.Stats.TokenUsage)
	}
}

// fakeGenaiWithUsage returns a canned response that carries UsageMetadata so the
// stats-capture path (custom.go) is exercised.
type fakeGenaiWithUsage struct {
	respText string
	usage    *genai.GenerateContentResponseUsageMetadata
	gotModel string
}

func (f *fakeGenaiWithUsage) GenerateContent(_ context.Context, model string, _ []*genai.Content, _ *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
	f.gotModel = model
	return &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{{
			Content: &genai.Content{Parts: []*genai.Part{{Text: f.respText}}},
		}},
		UsageMetadata: f.usage,
	}, nil
}

// --- WI-F3 model-id validation (review item 1, [Security Low]) ---

// TestValidateModel covers the local allowlist that guards the flag/template/
// config model id before it is echoed to stderr or composed into a Vertex
// resource name. A clearly-invalid id (newline/control-char/slash-bearing) must
// be rejected LOCALLY; "" (unset) and a fully-qualified resource name pass.
func TestValidateModel(t *testing.T) {
	ok := []string{
		"",                 // unset: falls through the precedence chain
		"gemini-2.5-flash", // bare
		"gemini-2.5-pro",
		"publishers/google/models/gemini-2.5-flash",                   // publisher-relative
		"projects/x/locations/global/publishers/google/models/g-z",    // fully-qualified passthrough
		"projects/p/locations/us-central1/publishers/google/models/m", // fully-qualified passthrough
	}
	for _, in := range ok {
		if err := ValidateModel(in); err != nil {
			t.Errorf("ValidateModel(%q) = %v, want nil", in, err)
		}
	}

	bad := []string{
		"gemini-2.5-flash\n",              // trailing newline (log/arg injection)
		"gemini\n2.5-flash",               // embedded newline
		"gemini-2.5-flash\r",              // carriage return
		"gemini\x00flash",                 // NUL / control char
		"gemini 2.5 flash",                // whitespace
		"bad/id",                          // bare form must not contain a slash
		"publishers/google/models/bad id", // reduces to a bare id with a space
		"../../etc/passwd",                // path traversal shape
		"-leading-dash",                   // must start alphanumeric
		"モデル",                             // non-ASCII
	}
	for _, in := range bad {
		if err := ValidateModel(in); err == nil {
			t.Errorf("ValidateModel(%q) = nil, want a local rejection error", in)
		}
	}
}

// TestRunRejectsInvalidModelLocally proves an invalid --model override fails at
// Engine.Run with a LOCAL error, before any client call (the fakeClient would
// error/panic differently) — the primary security fix for both paths.
func TestRunRejectsInvalidModelLocally(t *testing.T) {
	eng := NewEngine(&fakeClient{}, "p", "us-central1")
	_, err := eng.Run(context.Background(), pointwiseTemplate(), Instance{
		Fields: map[string]AssetRef{"response": {Text: "hi"}},
	}, WithModel("gemini-2.5-flash\nrm -rf"))
	if err == nil {
		t.Fatal("Run with newline-bearing --model = nil error, want local rejection")
	}
	if !strings.Contains(err.Error(), "invalid autorater model id") {
		t.Errorf("error = %v, want an 'invalid autorater model id' local rejection", err)
	}
}

// TestExpandAutoraterModelRejectsInvalid asserts the native resource-name
// composer rejects a malformed bare id LOCALLY rather than interpolating it.
func TestExpandAutoraterModelRejectsInvalid(t *testing.T) {
	if _, err := expandAutoraterModel("gemini-2.5-flash\n", "p", "us-central1"); err == nil {
		t.Fatal("expandAutoraterModel with newline = nil error, want local rejection")
	}
	// Fully-qualified names remain a trusted passthrough (unchanged behavior).
	full := "projects/x/locations/global/publishers/google/models/gemini-z"
	got, err := expandAutoraterModel(full, "p", "us-central1")
	if err != nil || got != full {
		t.Errorf("expandAutoraterModel(full) = %q,%v; want passthrough", got, err)
	}
}

// --- WI-F7 pre-flight location from a fully-qualified model (review item 4/n3) ---

// TestParseFullModelResource covers extraction of the embedded project/location
// from a fully-qualified model resource name.
func TestParseFullModelResource(t *testing.T) {
	p, l, ok := parseFullModelResource("projects/other/locations/europe-west1/publishers/google/models/gemini-z")
	if !ok || p != "other" || l != "europe-west1" {
		t.Errorf("parseFullModelResource full = (%q,%q,%v), want (other,europe-west1,true)", p, l, ok)
	}
	for _, in := range []string{"gemini-2.5-flash", "publishers/google/models/gemini-2.5-flash", "projects/onlyproj"} {
		if _, _, ok := parseFullModelResource(in); ok {
			t.Errorf("parseFullModelResource(%q) ok=true, want false", in)
		}
	}
}

// TestResolveNativeFQModelReflectsEmbeddedLocation proves the pre-flight echo on
// the NATIVE path shows the project/location embedded in a fully-qualified
// template/flag model (which the native path forwards verbatim), not the
// engine's configured defaults.
func TestResolveNativeFQModelReflectsEmbeddedLocation(t *testing.T) {
	eng := NewEngine(&fakeClient{}, "cfg-proj", "us-central1")
	tmpl := pointwiseTemplate()
	tmpl.AutoraterModel = "projects/fq-proj/locations/europe-west4/publishers/google/models/gemini-z"
	got := eng.Resolve(tmpl, "")
	if got.Project != "fq-proj" || got.Location != "europe-west4" {
		t.Errorf("native FQ target = %+v, want project=fq-proj location=europe-west4", got)
	}
	if got.Model != "gemini-z" || got.Path != "native" {
		t.Errorf("native FQ target = %+v, want model=gemini-z path=native", got)
	}
}
