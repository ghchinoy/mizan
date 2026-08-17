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

package eval

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/genai"

	"github.com/ghchinoy/mizan/internal/registry"
)

// fakeGenai is a network-free GenaiClient. It records the last call, can fail
// the first failN invocations with a synthetic RESOURCE_EXHAUSTED (to exercise
// the backoff path), and otherwise returns respText as the model's JSON output.
type fakeGenai struct {
	calls    int
	failN    int    // return 429 for the first failN calls
	respText string // returned as the single text part
	hardErr  error  // if set (after failN), returned as a non-retryable error

	gotModel    string
	gotContents []*genai.Content
	gotCfg      *genai.GenerateContentConfig
}

func (f *fakeGenai) GenerateContent(_ context.Context, model string, contents []*genai.Content, cfg *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
	f.calls++
	f.gotModel = model
	f.gotContents = contents
	f.gotCfg = cfg
	if f.calls <= f.failN {
		return nil, genai.APIError{Code: 429, Status: "RESOURCE_EXHAUSTED", Message: "quota"}
	}
	if f.hardErr != nil {
		return nil, f.hardErr
	}
	return &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{{
			Content: &genai.Content{Parts: []*genai.Part{{Text: f.respText}}},
		}},
	}, nil
}

func customSchemaTemplate() registry.MetricTemplate {
	return registry.MetricTemplate{
		ID:                   "test/brand-check",
		Name:                 "Brand Check",
		Kind:                 registry.KindCustomSchema,
		MetricPromptTemplate: "Assess this creative against the brand guideline: {{creative}}",
		SystemInstruction:    "You are a strict brand auditor.",
		AutoraterModel:       "gemini-2.5-flash",
		ResponseSchema: &registry.Schema{JSON: `{
			"type": "object",
			"properties": {
				"overall_score": {"type": "number"},
				"compliant": {"type": "boolean"},
				"flagged_issues": {"type": "array", "items": {"type": "string"}},
				"explanation": {"type": "string"}
			},
			"required": ["overall_score", "compliant", "flagged_issues", "explanation"]
		}`},
	}
}

// fastRetry shrinks the backoff so the exhaust/recover paths run instantly.
func fastRetry(e *Engine) {
	e.retry = retryPolicy{maxAttempts: 4, baseDelay: time.Millisecond, maxDelay: 2 * time.Millisecond}
}

func TestRunCustomSchemaSuccess(t *testing.T) {
	fg := &fakeGenai{respText: `{"overall_score":8.5,"compliant":true,"flagged_issues":[],"explanation":"On brand."}`}
	eng := NewEngine(&fakeClient{}, "p", "us-central1", WithGenaiClient(fg))

	res, err := eng.Run(context.Background(), customSchemaTemplate(), Instance{
		Fields: map[string]AssetRef{
			"creative": {Modality: registry.ModalityText, Text: "A calm blue banner."},
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Output parsing.
	if res.CustomOutput["compliant"] != true {
		t.Errorf("compliant = %v, want true", res.CustomOutput["compliant"])
	}
	if got := res.CustomOutput["overall_score"]; got != 8.5 {
		t.Errorf("overall_score = %v, want 8.5", got)
	}
	if len(res.RawOutput) != 1 || !strings.Contains(res.RawOutput[0], "On brand") {
		t.Errorf("RawOutput = %v", res.RawOutput)
	}

	// Model id is the bare publisher-relative id (genai does not need the full
	// resource name).
	if fg.gotModel != "gemini-2.5-flash" {
		t.Errorf("model = %q, want gemini-2.5-flash", fg.gotModel)
	}
	// Strict JSON config materialization.
	if fg.gotCfg.ResponseMIMEType != "application/json" {
		t.Errorf("ResponseMIMEType = %q", fg.gotCfg.ResponseMIMEType)
	}
	if fg.gotCfg.ResponseSchema == nil || fg.gotCfg.ResponseSchema.Type != genai.TypeObject {
		t.Fatalf("ResponseSchema not mapped to a top-level OBJECT: %+v", fg.gotCfg.ResponseSchema)
	}
	if fg.gotCfg.SystemInstruction == nil {
		t.Error("SystemInstruction not set")
	}
	// The single user content's first part is the rendered prompt with the text
	// placeholder substituted inline.
	if len(fg.gotContents) != 1 || len(fg.gotContents[0].Parts) == 0 {
		t.Fatalf("unexpected contents shape: %+v", fg.gotContents)
	}
	if txt := fg.gotContents[0].Parts[0].Text; !strings.Contains(txt, "A calm blue banner") {
		t.Errorf("prompt = %q, missing substituted value", txt)
	}
}

func TestRunCustomSchemaBackoffRecovers(t *testing.T) {
	fg := &fakeGenai{failN: 2, respText: `{"overall_score":1,"compliant":false,"flagged_issues":["x"],"explanation":"nope"}`}
	eng := NewEngine(&fakeClient{}, "p", "us-central1", WithGenaiClient(fg))
	fastRetry(eng)

	res, err := eng.Run(context.Background(), customSchemaTemplate(), Instance{
		Fields: map[string]AssetRef{"creative": {Text: "x"}},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if fg.calls != 3 {
		t.Errorf("calls = %d, want 3 (2 x 429 then success)", fg.calls)
	}
	if res.CustomOutput["compliant"] != false {
		t.Errorf("compliant = %v, want false", res.CustomOutput["compliant"])
	}
}

func TestRunCustomSchemaBackoffExhausts(t *testing.T) {
	fg := &fakeGenai{failN: 99} // always 429
	eng := NewEngine(&fakeClient{}, "p", "us-central1", WithGenaiClient(fg))
	fastRetry(eng)

	_, err := eng.Run(context.Background(), customSchemaTemplate(), Instance{
		Fields: map[string]AssetRef{"creative": {Text: "x"}},
	})
	if err == nil {
		t.Fatal("expected exhausted-attempts error")
	}
	if !strings.Contains(err.Error(), "exhausted") {
		t.Errorf("error = %v, want 'exhausted'", err)
	}
	if fg.calls != 4 {
		t.Errorf("calls = %d, want 4 (maxAttempts)", fg.calls)
	}
}

func TestRunCustomSchemaFailsFastOnOtherError(t *testing.T) {
	fg := &fakeGenai{hardErr: genai.APIError{Code: 400, Status: "INVALID_ARGUMENT"}}
	eng := NewEngine(&fakeClient{}, "p", "us-central1", WithGenaiClient(fg))
	fastRetry(eng)

	_, err := eng.Run(context.Background(), customSchemaTemplate(), Instance{
		Fields: map[string]AssetRef{"creative": {Text: "x"}},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "non-retryable") {
		t.Errorf("error = %v, want 'non-retryable'", err)
	}
	if fg.calls != 1 {
		t.Errorf("calls = %d, want 1 (no retry on non-429)", fg.calls)
	}
}

func TestRunCustomSchemaMissingClient(t *testing.T) {
	eng := NewEngine(&fakeClient{}, "p", "us-central1") // no WithGenaiClient
	_, err := eng.Run(context.Background(), customSchemaTemplate(), Instance{
		Fields: map[string]AssetRef{"creative": {Text: "x"}},
	})
	if err == nil || !strings.Contains(err.Error(), "genai client") {
		t.Fatalf("want genai-client error, got %v", err)
	}
}

func TestRunCustomSchemaMissingVariable(t *testing.T) {
	fg := &fakeGenai{respText: "{}"}
	eng := NewEngine(&fakeClient{}, "p", "us-central1", WithGenaiClient(fg))
	_, err := eng.Run(context.Background(), customSchemaTemplate(), Instance{Fields: map[string]AssetRef{}})
	if err == nil || !strings.Contains(err.Error(), "creative") {
		t.Fatalf("want missing-variable error naming 'creative', got %v", err)
	}
	if fg.calls != 0 {
		t.Error("client should not be called when validation fails")
	}
}

func TestToGenaiSchema(t *testing.T) {
	// Lowercase (standard JSON-schema) types normalize to genai uppercase.
	s := &registry.Schema{JSON: `{"type":"object","properties":{"n":{"type":"number"},"tags":{"type":"array","items":{"type":"string"}}}}`}
	gs, err := toGenaiSchema(s)
	if err != nil {
		t.Fatalf("toGenaiSchema: %v", err)
	}
	if gs.Type != genai.TypeObject {
		t.Errorf("top type = %q, want OBJECT", gs.Type)
	}
	if gs.Properties["n"].Type != genai.TypeNumber {
		t.Errorf("n type = %q, want NUMBER", gs.Properties["n"].Type)
	}
	if gs.Properties["tags"].Type != genai.TypeArray || gs.Properties["tags"].Items.Type != genai.TypeString {
		t.Errorf("tags/items types not mapped: %+v", gs.Properties["tags"])
	}

	// Empty / missing schema errors.
	if _, err := toGenaiSchema(&registry.Schema{JSON: ""}); err == nil {
		t.Error("empty JSON should error")
	}
	if _, err := toGenaiSchema(&registry.Schema{JSON: `{"properties":{}}`}); err == nil {
		t.Error("missing top-level type should error")
	}
}

func TestParseCustomOutputFenceStrip(t *testing.T) {
	out, err := parseCustomOutput("```json\n{\"a\":1}\n```")
	if err != nil {
		t.Fatalf("parseCustomOutput: %v", err)
	}
	if out["a"] != float64(1) {
		t.Errorf("a = %v, want 1", out["a"])
	}
	if _, err := parseCustomOutput("not json"); err == nil {
		t.Error("invalid JSON should error")
	}
}

func TestGenaiModelID(t *testing.T) {
	// genaiModelID only reduces to the bare id now; the empty-string fallback
	// branch was removed (the model is resolved+validated in Engine.Run before it
	// reaches here), so "" is no longer special-cased.
	cases := map[string]string{
		"gemini-2.5-flash":                                              "gemini-2.5-flash",
		"publishers/google/models/gemini-2.5-pro":                       "gemini-2.5-pro",
		"projects/x/locations/global/publishers/google/models/gemini-z": "gemini-z",
	}
	for in, want := range cases {
		if got := genaiModelID(in); got != want {
			t.Errorf("genaiModelID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestToGenaiInlinePart(t *testing.T) {
	// Text.
	p, err := toGenaiInlinePart(AssetRef{Modality: registry.ModalityText, Text: "hi"})
	if err != nil {
		t.Fatalf("text: %v", err)
	}
	if p.Text != "hi" {
		t.Errorf("text part = %q", p.Text)
	}

	// Local file -> inline bytes with detected MIME.
	dir := t.TempDir()
	fp := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(fp, []byte("hello bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err = toGenaiInlinePart(AssetRef{Modality: registry.ModalityImage, FilePath: fp})
	if err != nil {
		t.Fatalf("file: %v", err)
	}
	if p.InlineData == nil || string(p.InlineData.Data) != "hello bytes" {
		t.Errorf("inline data not read: %+v", p.InlineData)
	}
	if p.InlineData.MIMEType == "" {
		t.Error("MIME type not detected")
	}

	// Explicit MIME is honored.
	p, err = toGenaiInlinePart(AssetRef{Modality: registry.ModalityImage, FilePath: fp, MimeType: "image/png"})
	if err != nil {
		t.Fatalf("file+mime: %v", err)
	}
	if p.InlineData.MIMEType != "image/png" {
		t.Errorf("MIME = %q, want image/png", p.InlineData.MIMEType)
	}

	// gs:// -> FileData (requires MIME).
	p, err = toGenaiInlinePart(AssetRef{Modality: registry.ModalityImage, GCSUri: "gs://b/x.png", MimeType: "image/png"})
	if err != nil {
		t.Fatalf("gs: %v", err)
	}
	if p.FileData == nil || p.FileData.FileURI != "gs://b/x.png" {
		t.Errorf("FileData not set: %+v", p.FileData)
	}
	// gs:// without an explicit MIME now resolves it from the object extension
	// (Fix B / Defect A) — mirroring the native path — instead of erroring. This
	// is what lets a bare CLI --gcs asset reach the judge.
	p, err = toGenaiInlinePart(AssetRef{Modality: registry.ModalityImage, GCSUri: "gs://b/x.png"})
	if err != nil {
		t.Fatalf("gs:// with resolvable extension should not error: %v", err)
	}
	if p.FileData == nil || p.FileData.MIMEType != "image/png" {
		t.Errorf("FileData MIME = %+v, want image/png resolved from extension", p.FileData)
	}
	// A gs:// object whose MIME cannot be resolved (unknown extension) is a hard
	// error — never a droppable octet-stream.
	if _, err := toGenaiInlinePart(AssetRef{Modality: registry.ModalityImage, GCSUri: "gs://b/object-without-extension"}); err == nil {
		t.Error("gs:// with unresolvable MIME should error")
	}
}
