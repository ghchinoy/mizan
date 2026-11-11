// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package mizan

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"google.golang.org/genai"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type fakeGenerator struct {
	resp        *genai.GenerateContentResponse
	resps       []*genai.GenerateContentResponse
	err         error
	calls       int
	gotModel    string
	gotContents []*genai.Content
	gotCfg      *genai.GenerateContentConfig
}

func (f *fakeGenerator) GenerateContent(_ context.Context, model string, contents []*genai.Content, cfg *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
	idx := f.calls
	f.calls++
	f.gotModel = model
	f.gotContents = contents
	f.gotCfg = cfg
	if f.err != nil {
		return nil, f.err
	}
	if idx < len(f.resps) {
		return f.resps[idx], nil
	}
	return f.resp, nil
}

var jpegHeader = []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00}

func TestNewEvaluator_BackendEnterpriseAndRetrySuppression(t *testing.T) {
	var httpCalls int
	var gotPath string
	httpClient := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			httpCalls++
			gotPath = req.URL.Path
			body := `{
				"responseId": "ent-1",
				"modelVersion": "gemini-4-argon-001",
				"candidates": [{
					"finishReason": "STOP",
					"content": {"role": "model", "parts": [{"text": "{\"selection\":\"take_2\",\"explanation\":\"best framing\"}"}]}
				}],
				"usageMetadata": {"promptTokenCount": 120, "candidatesTokenCount": 18, "thoughtsTokenCount": 10, "totalTokenCount": 148, "cachedContentTokenCount": 40}
			}`
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(body)),
			}, nil
		}),
	}

	attempts := int32(1)
	client, err := genai.NewClient(t.Context(), &genai.ClientConfig{
		Backend:    genai.BackendEnterprise,
		Project:    "studio-enterprise",
		Location:   "global",
		HTTPClient: httpClient,
		HTTPOptions: genai.HTTPOptions{
			RetryOptions: &genai.HTTPRetryOptions{Attempts: &attempts},
		},
	})
	if err != nil {
		t.Fatalf("genai.NewClient(BackendEnterprise) error = %v", err)
	}

	// Capture os.Stderr to guarantee zero CLI stderr noise leaks during SDK calls.
	origStderr := os.Stderr
	rPipe, wPipe, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error = %v", err)
	}
	os.Stderr = wPipe
	defer func() { os.Stderr = origStderr }()

	ev, err := NewEvaluator(client,
		WithBackendLabel("enterprise"),
		WithModelOverride("gemini-4-argon"),
	)
	if err != nil {
		t.Fatalf("NewEvaluator() error = %v", err)
	}

	tmpl, err := ParseTemplate([]byte(`
apiVersion: mizan.dev/v1alpha1
kind: MetricTemplate
metadata:
  id: editingbay/best-take
  name: Best Video Take
  version: 1.0.0
spec:
  kind: choice
  modalities: [video, text]
  inputs:
    - name: brief
      modality: text
      required: true
    - name: variations
      modality: video
      required: true
  choices: [variation_1, variation_2]
  autorater:
    model: gemini-3.5-flash-lite
  metricPromptTemplate: |
    Creative brief: {{brief}}
    Select the strongest take from {{variations}}.
`))
	if err != nil {
		t.Fatalf("ParseTemplate() error = %v", err)
	}
	origHash := tmpl.ContentHash()

	res, err := ev.Run(t.Context(), tmpl, Instance{
		"brief": Text("Dramatic slow-motion reveal"),
		"variations": Variations(
			Variation{ID: "take_1", Description: "Wide angle", Asset: URI("gs://renders/take_1.mp4", "video/mp4")},
			Variation{ID: "take_2", Description: "Macro lens", Asset: URI("gs://renders/take_2.mp4", "video/mp4")},
			Variation{ID: "take_3", Asset: URI("gs://renders/take_3.mp4", "video/mp4")},
		),
	}, WithChoicesFromVariations("none"), WithMediaResolution("low"))

	_ = wPipe.Close()
	stderrBytes, _ := io.ReadAll(rPipe)
	_ = rPipe.Close()

	if err != nil {
		t.Fatalf("ev.Run() error = %v", err)
	}
	if len(stderrBytes) != 0 {
		t.Errorf("SDK leaked %d bytes to os.Stderr: %q", len(stderrBytes), string(stderrBytes))
	}
	if httpCalls != 1 {
		t.Errorf("httpCalls = %d, want 1", httpCalls)
	}
	if !strings.Contains(gotPath, "gemini-4-argon") {
		t.Errorf("request URL path = %q, want gemini-4-argon override instead of template gemini-3.5-flash-lite", gotPath)
	}
	if tmpl.ContentHash() != origHash || !slices.Equal(tmpl.Choices(), []string{"variation_1", "variation_2"}) {
		t.Errorf("Template mutated by runtime choices: hash=%q (want %q), choices=%v", tmpl.ContentHash(), origHash, tmpl.Choices())
	}
	if res.ChoiceSelection != "take_2" {
		t.Errorf("ChoiceSelection = %q, want take_2", res.ChoiceSelection)
	}
	if res.Applied == nil {
		t.Fatal("Result.Applied = nil, want populated AppliedJudge")
	}
	if res.Applied.Backend != "enterprise" || res.Applied.Model != "gemini-4-argon" || res.Applied.ModelSource != "engine-override" {
		t.Errorf("Result.Applied = %+v, want Backend=enterprise Model=gemini-4-argon ModelSource=engine-override", res.Applied)
	}
	if !slices.Equal(res.Applied.EffectiveChoices, []string{"take_1", "take_2", "take_3", "none"}) {
		t.Errorf("EffectiveChoices = %v, want [take_1 take_2 take_3 none]", res.Applied.EffectiveChoices)
	}
	if res.Stats.CachedTokens != 40 || res.Stats.TokenUsage == nil || res.Stats.TokenUsage.TotalTokens != 148 {
		t.Errorf("Stats = %+v, want CachedTokens=40 and TotalTokens=148", res.Stats)
	}

	// Applied MUST serialize in pkg/mizan.Result JSON (unlike internal/eval.Result).
	rawJSON, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("json.Marshal(Result) error = %v", err)
	}
	if !bytes.Contains(rawJSON, []byte(`"applied"`)) || !bytes.Contains(rawJSON, []byte(`"enterprise"`)) {
		t.Errorf("marshaled Result JSON = %s, want applied.backend=enterprise", rawJSON)
	}

	// Verify nested retry suppression when HTTPRetryOptions has default (nil -> 5) attempts.
	var errCalls int
	errClient, err := genai.NewClient(t.Context(), &genai.ClientConfig{
		Backend:  genai.BackendEnterprise,
		Project:  "studio-enterprise",
		Location: "global",
		HTTPClient: &http.Client{
			Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				errCalls++
				return &http.Response{
					StatusCode: http.StatusTooManyRequests,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"error":{"code":429,"status":"RESOURCE_EXHAUSTED","message":"quota"}}`)),
				}, nil
			}),
		},
		HTTPOptions: genai.HTTPOptions{
			// Attempts=2 (>1) means inner client retries; Mizan outer retries must default to 1.
			RetryOptions: &genai.HTTPRetryOptions{
				Attempts:     genai.Ptr[int32](2),
				InitialDelay: genai.Ptr(0.001),
				MaxDelay:     genai.Ptr(0.002),
			},
		},
	})
	if err != nil {
		t.Fatalf("genai.NewClient(retry check) error = %v", err)
	}
	evRetry, err := NewEvaluator(errClient)
	if err != nil {
		t.Fatalf("NewEvaluator(retry check) error = %v", err)
	}
	_, err = evRetry.Run(t.Context(), tmpl, Instance{
		"brief":      Text("x"),
		"variations": URI("gs://renders/v.mp4", "video/mp4"),
	}, WithChoices("a", "b"))
	if err == nil {
		t.Fatal("Run(429) = nil, want error")
	}
	if errCalls != 2 {
		t.Errorf("HTTP calls under inner RetryOptions(Attempts=2) = %d, want 2 (1 outer x 2 inner, not 5x2=10)", errCalls)
	}
}

func TestFromGenaiPart_MediaPreambleAndSecurityGuards(t *testing.T) {
	fps := 4.0
	numTok := int32(64)
	part := &genai.Part{
		FileData: &genai.FileData{
			FileURI:  "gs://studio/master.mp4",
			MIMEType: "video/mp4",
		},
		VideoMetadata: &genai.VideoMetadata{
			StartOffset: 2 * time.Second,
			EndOffset:   8 * time.Second,
			FPS:         &fps,
		},
		MediaResolution: &genai.PartMediaResolution{
			Level:     genai.PartMediaResolutionLevelMediaResolutionLow,
			NumTokens: &numTok,
		},
	}
	vidAsset, err := FromGenaiPart(part)
	if err != nil {
		t.Fatalf("FromGenaiPart(valid video) error = %v", err)
	}

	fg := &fakeGenerator{
		resp: &genai.GenerateContentResponse{
			Candidates: []*genai.Candidate{{
				FinishReason: genai.FinishReasonStop,
				Content:      &genai.Content{Parts: []*genai.Part{{Text: `{"passed":true,"confidence":0.95,"explanation":"clean cut"}`}}},
			}},
		},
	}
	ev, err := NewEvaluatorFromGenerator(fg, WithModelOverride("models/gemini-4-argon"))
	if err != nil {
		t.Fatalf("NewEvaluatorFromGenerator() error = %v", err)
	}
	tmpl, err := NewTemplate(TemplateSpec{
		ID:                   "editingbay/cut-check",
		Kind:                 KindBoul,
		MetricPromptTemplate: "Rubric context before placeholder.\nInspect {{clip}} and confirm {{clip}} has no jump cuts.",
	})
	if err != nil {
		t.Fatalf("NewTemplate() error = %v", err)
	}

	res, err := ev.Run(t.Context(), tmpl, Instance{"clip": vidAsset})
	if err != nil {
		t.Fatalf("ev.Run() error = %v", err)
	}
	if res.Passed == nil || !*res.Passed {
		t.Errorf("Result.Passed = %v, want true", res.Passed)
	}

	// Media-Preamble-First: [Label, VideoPart, SingleTrailingPrompt] -> 3 parts.
	parts := fg.gotContents[0].Parts
	if len(parts) != 3 {
		t.Fatalf("len(parts) = %d, want 3", len(parts))
	}
	if parts[0].Text != `[video "clip"]:` {
		t.Errorf("parts[0].Text = %q, want [video \"clip\"]:", parts[0].Text)
	}
	if parts[1].VideoMetadata == nil || parts[1].MediaResolution == nil || parts[1].MediaResolution.Level != genai.PartMediaResolutionLevelMediaResolutionLow {
		t.Errorf("parts[1] lost VideoMetadata or MediaResolution: %+v", parts[1])
	}

	// Text-only run emits len(Parts) == 1.
	txtTmpl, err := NewTemplate(TemplateSpec{
		ID:                   "editingbay/text-check",
		Kind:                 KindBoul,
		MetricPromptTemplate: "Check {{copy}}",
	})
	if err != nil {
		t.Fatalf("NewTemplate(text) error = %v", err)
	}
	if _, err := ev.Run(t.Context(), txtTmpl, Instance{"copy": Text("hello")}); err != nil {
		t.Fatalf("ev.Run(text) error = %v", err)
	}
	if len(fg.gotContents[0].Parts) != 1 {
		t.Errorf("text-only len(Parts) = %d, want 1", len(fg.gotContents[0].Parts))
	}

	// Sub-second rounding collapse on FromGenaiPart fails fast.
	if _, err := FromGenaiPart(&genai.Part{
		FileData:      &genai.FileData{FileURI: "gs://b/v.mp4", MIMEType: "video/mp4"},
		VideoMetadata: &genai.VideoMetadata{StartOffset: 10200 * time.Millisecond, EndOffset: 10400 * time.Millisecond},
	}); err == nil {
		t.Error("FromGenaiPart(10.2s..10.4s) = nil, want rounding-collapse error")
	}

	// SSRF mapped-IPv4 literal fails fast.
	if _, err := FromGenaiPart(&genai.Part{
		FileData: &genai.FileData{FileURI: "https://[::ffff:127.0.0.1]/v.mp4", MIMEType: "video/mp4"},
	}); err == nil {
		t.Error("FromGenaiPart(::ffff:127.0.0.1) = nil, want SSRF error")
	}

	// File() rejected by default, accepted with WithLocalFileAccess(true).
	fp := filepath.Join(t.TempDir(), "frame.jpg")
	if err := os.WriteFile(fp, jpegHeader, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ev.Run(t.Context(), tmpl, Instance{"clip": File(fp)}); err == nil {
		t.Error("ev.Run(File without WithLocalFileAccess) = nil, want rejection")
	}
	evLocal, err := NewEvaluatorFromGenerator(fg, WithLocalFileAccess(true))
	if err != nil {
		t.Fatalf("NewEvaluatorFromGenerator(local) error = %v", err)
	}
	if _, err := evLocal.Run(t.Context(), tmpl, Instance{"clip": File(fp)}); err != nil {
		t.Errorf("evLocal.Run(File with WithLocalFileAccess(true)) error = %v", err)
	}
}

func TestCatalogFS_StrictYAML_EvalSetAndLegacyKinds(t *testing.T) {
	// Strict YAML rejects snake_case typos like system_instruction:.
	if _, err := ParseTemplate([]byte(`
apiVersion: mizan.dev/v1alpha1
kind: MetricTemplate
metadata:
  id: bad/snake
  version: 1.0.0
spec:
  kind: boul
  system_instruction: typo
  metricPromptTemplate: "Check {{x}}"
`)); err == nil {
		t.Fatal("ParseTemplate(system_instruction:) = nil, want strict schema error")
	}

	fsys := fstest.MapFS{
		"packs/video/mizan-pack.yaml": &fstest.MapFile{Data: []byte("apiVersion: mizan.dev/v1alpha1\nkind: Pack\nmetadata:\n  name: video\n")},
		"packs/video/templates/rubric.yaml": &fstest.MapFile{Data: []byte(`
apiVersion: mizan.dev/v1alpha1
kind: MetricTemplate
metadata:
  id: video/rubric-quality
  name: Rubric Quality
  version: 1.0.0
spec:
  kind: rubric
  modalities: [video, text]
  inputs:
    - name: clip
      modality: video
      required: true
    - name: metadata
      modality: text
      required: true
  rubricGroups:
    craft:
      - Smooth camera motion
  autorater:
    samplingCount: 4
  metricPromptTemplate: "Assess {{clip}} with metadata {{metadata}}"
`)},
		"packs/video/templates/pointwise.yaml": &fstest.MapFile{Data: []byte(`
apiVersion: mizan.dev/v1alpha1
kind: MetricTemplate
metadata:
  id: video/brief-fit
  name: Brief Fit
  version: 1.0.0
spec:
  kind: pointwise
  modalities: [video, text]
  inputs:
    - name: brief
      modality: text
      required: true
    - name: clip
      modality: video
      required: true
  metricPromptTemplate: "Score {{clip}} for brief {{brief}}"
`)},
		"packs/video/templates/check.yaml": &fstest.MapFile{Data: []byte(`
apiVersion: mizan.dev/v1alpha1
kind: MetricTemplate
metadata:
  id: video/brief-nonempty
  name: Brief Nonempty
  version: 1.0.0
spec:
  kind: heuristic
  modalities: [text]
  inputs:
    - name: brief
      modality: text
      required: true
  heuristic:
    type: contains
    target: brief
    value: product
`)},
		"packs/video/evalsets/gate.yaml": &fstest.MapFile{Data: []byte(`
apiVersion: mizan.dev/v1alpha1
kind: EvalSet
metadata:
  id: video/release-gate
  name: Release Gate
  version: 1.0.0
  assetClass: video
spec:
  members:
    - metric: video/rubric-quality
      required: true
      bind:
        clip_meta: metadata
    - metric: video/brief-fit
      required: true
    - metric: video/brief-nonempty
      required: true
  aggregation:
    method: mean
    threshold: 1.0
    gate: true
`)},
	}

	cat, err := LoadCatalogFS(fsys)
	if err != nil {
		t.Fatalf("LoadCatalogFS() error = %v", err)
	}
	set, ok := cat.EvalSet("video/release-gate")
	if !ok {
		t.Fatal("cat.EvalSet(video/release-gate) not found")
	}

	fg := &fakeGenerator{
		resps: []*genai.GenerateContentResponse{
			{
				Candidates: []*genai.Candidate{{
					FinishReason: genai.FinishReasonStop,
					Content: &genai.Content{Parts: []*genai.Part{{Text: `{
						"per_criterion": [{"group":"craft","criterion":"Smooth camera motion","score":5,"rationale":"steady gimbal"}],
						"overall_score": 5,
						"explanation": "Smooth motion throughout"
					}`}}},
				}},
				UsageMetadata: &genai.GenerateContentResponseUsageMetadata{PromptTokenCount: 100, CandidatesTokenCount: 30, TotalTokenCount: 130},
			},
			{
				Candidates: []*genai.Candidate{{
					FinishReason: genai.FinishReasonStop,
					Content:      &genai.Content{Parts: []*genai.Part{{Text: `{"score":4,"explanation":"matches brief"}`}}},
				}},
				UsageMetadata: &genai.GenerateContentResponseUsageMetadata{PromptTokenCount: 80, CandidatesTokenCount: 20, TotalTokenCount: 100},
			},
		},
	}
	ev, err := NewEvaluatorFromGenerator(fg)
	if err != nil {
		t.Fatalf("NewEvaluatorFromGenerator() error = %v", err)
	}

	setRes, err := ev.RunSet(t.Context(), set, cat, SetRunOptions{
		Inputs: Instance{
			"clip":      URI("gs://studio/cut.mp4", "video/mp4"),
			"clip_meta": Text("24fps 1080p"),
			"brief":     Text("Launch product teaser"),
		},
	})
	if err != nil {
		t.Fatalf("ev.RunSet() error = %v", err)
	}
	if setRes.Verdict != VerdictPassed {
		t.Errorf("Verdict = %q, want PASSED (members=%+v)", setRes.Verdict, setRes.Members)
	}
	if setRes.TokenUsage == nil || setRes.TokenUsage.TotalTokens != 230 {
		t.Errorf("EvalSetResult.TokenUsage = %+v, want summed TotalTokens=230", setRes.TokenUsage)
	}
	if len(setRes.Members[0].Result.Criteria) != 1 || setRes.Members[0].Result.Criteria[0].Score != 5 {
		t.Errorf("rubric Criteria = %+v, want 1 criterion scored 5", setRes.Members[0].Result.Criteria)
	}
	if len(setRes.Members[0].Result.Warnings) == 0 {
		t.Error("expected samplingCount=4 warning on genai-routed rubric member")
	}

	// Unreferenced suite input fails pre-flight before invoking generator.
	fg.calls = 0
	if _, err := ev.RunSet(t.Context(), set, cat, SetRunOptions{
		Inputs: Instance{
			"clip":      URI("gs://studio/cut.mp4", "video/mp4"),
			"clip_meta": Text("24fps"),
			"brief":     Text("product"),
			"typo_key":  Text("unused"),
		},
	}); err == nil || fg.calls != 0 {
		t.Errorf("RunSet(unreferenced key) err=%v calls=%d, want pre-flight error with 0 calls", err, fg.calls)
	}
}

func TestDecodeCustom_FinishError_PrebuiltAndOfflineEvaluator(t *testing.T) {
	// DecodeCustom[T] generic helper.
	type customVerdict struct {
		Compliant bool   `json:"compliant"`
		Notes     string `json:"notes"`
	}
	fg := &fakeGenerator{
		resp: &genai.GenerateContentResponse{
			Candidates: []*genai.Candidate{{
				FinishReason: genai.FinishReasonStop,
				Content:      &genai.Content{Parts: []*genai.Part{{Text: `{"compliant":true,"notes":"approved"}`}}},
			}},
		},
	}
	ev, err := NewEvaluatorFromGenerator(fg)
	if err != nil {
		t.Fatalf("NewEvaluatorFromGenerator() error = %v", err)
	}
	csTmpl, err := NewTemplate(TemplateSpec{
		ID:                   "brand/custom",
		Kind:                 KindCustomSchema,
		MetricPromptTemplate: "Check {{ad}}",
		ResponseSchemaJSON:   `{"type":"object","properties":{"compliant":{"type":"boolean"},"notes":{"type":"string"}},"required":["compliant","notes"]}`,
	})
	if err != nil {
		t.Fatalf("NewTemplate(custom_schema) error = %v", err)
	}
	csRes, err := ev.Run(t.Context(), csTmpl, Instance{"ad": Text("Clean headline")})
	if err != nil {
		t.Fatalf("Run(custom_schema) error = %v", err)
	}
	decoded, err := DecodeCustom[customVerdict](csRes)
	if err != nil || !decoded.Compliant || decoded.Notes != "approved" {
		t.Errorf("DecodeCustom() = (%+v, %v), want compliant=true notes=approved", decoded, err)
	}

	// FinishError surfaces typed *mizan.FinishError.
	fg.resp = &genai.GenerateContentResponse{
		ResponseID: "r-block",
		Candidates: []*genai.Candidate{{FinishReason: genai.FinishReasonSafety}},
	}
	_, err = ev.Run(t.Context(), csTmpl, Instance{"ad": Text("probe")})
	var finErr *FinishError
	if !errors.As(err, &finErr) || finErr.FinishReason != string(genai.FinishReasonSafety) {
		t.Errorf("Run(SAFETY) error = %v, want *mizan.FinishError with SAFETY", err)
	}

	// Prebuilt fails fast on genai-only Evaluator.
	pbTmpl, err := NewTemplate(TemplateSpec{
		ID:     "vertex/fluency",
		Kind:   KindPrebuilt,
		Inputs: []InputSpec{{Name: "response", Modality: ModalityText, Required: true}},
		Native: &NativeMetricSpec{Metric: "fluency"},
	})
	if err != nil {
		t.Fatalf("NewTemplate(prebuilt) error = %v", err)
	}
	if _, err := ev.Run(t.Context(), pbTmpl, Instance{"response": Text("hello")}); err == nil || !strings.Contains(err.Error(), "EvaluateInstances") {
		t.Errorf("Run(prebuilt) error = %v, want EvaluateInstances fast-fail", err)
	}

	// Offline evaluator runs computation metrics with zero credentials.
	off := NewOfflineEvaluator()
	compTmpl, err := NewTemplate(TemplateSpec{
		ID:   "local/exact",
		Kind: KindComputation,
		Inputs: []InputSpec{
			{Name: "response", Modality: ModalityText, Required: true},
			{Name: "reference", Modality: ModalityText, Required: true},
		},
		Native: &NativeMetricSpec{Metric: "exact_match"},
	})
	if err != nil {
		t.Fatalf("NewTemplate(computation) error = %v", err)
	}
	compRes, err := off.Run(t.Context(), compTmpl, Instance{
		"response":  Text("same"),
		"reference": Text("same"),
	})
	if err != nil || compRes.Score == nil || *compRes.Score != 1.0 {
		t.Errorf("OfflineEvaluator.Run(exact_match) = (%+v, %v), want Score=1.0", compRes, err)
	}
}
