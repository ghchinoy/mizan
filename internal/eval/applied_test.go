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

// applied_test.go covers the additive autorater-as-applied recorded on every
// Result by Engine.Run (eval-results-store-design §4.3): the ModelSource
// precedence classifier (pure) and Run populating Result.Applied with the
// resolved model + effective host/location on the regional and global paths.

import (
	"context"
	"testing"

	"github.com/ghchinoy/mizan/internal/eval/evaltest"
	"github.com/ghchinoy/mizan/internal/registry"
)

// modelSource must mirror EXACTLY the resolveModel precedence
// (flag > template > config default-model > built-in); assert each branch.
func TestModelSource_AllBranches(t *testing.T) {
	tests := []struct {
		name          string
		override      string
		templateModel string
		configDefault string
		want          string
	}{
		{"flag override wins over all", "gemini-flag", "gemini-tmpl", "gemini-cfg", modelSourceFlag},
		{"template when no flag", "", "gemini-tmpl", "gemini-cfg", modelSourceTemplate},
		{"config default when no flag/template", "", "", "gemini-cfg", modelSourceConfigDefault},
		{"builtin when nothing else set", "", "", "", modelSourceBuiltin},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := modelSource(tt.override, tt.templateModel, tt.configDefault); got != tt.want {
				t.Errorf("modelSource(%q,%q,%q) = %q, want %q",
					tt.override, tt.templateModel, tt.configDefault, got, tt.want)
			}
		})
	}
}

// modelSource must classify from the SAME inputs resolveModel resolves the model
// from, so the label never disagrees with the model actually chosen.
func TestModelSource_MatchesResolveModel(t *testing.T) {
	cases := []struct {
		name       string
		defModel   string
		override   string
		tmplModel  string
		wantModel  string
		wantSource string
	}{
		{"builtin", "", "", "", BuiltinDefaultModel, modelSourceBuiltin},
		{"config-default", "gemini-cfg", "", "", "gemini-cfg", modelSourceConfigDefault},
		{"template", "gemini-cfg", "", "gemini-tmpl", "gemini-tmpl", modelSourceTemplate},
		{"flag", "gemini-cfg", "gemini-flag", "gemini-tmpl", "gemini-flag", modelSourceFlag},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := &Engine{defaultModel: tc.defModel}
			tmpl := registry.MetricTemplate{AutoraterModel: tc.tmplModel}
			if got := e.resolveModel(tmpl, tc.override); got != tc.wantModel {
				t.Errorf("resolveModel = %q, want %q", got, tc.wantModel)
			}
			if got := modelSource(tc.override, tmpl.AutoraterModel, e.defaultModel); got != tc.wantSource {
				t.Errorf("modelSource = %q, want %q", got, tc.wantSource)
			}
		})
	}
}

// A regional (default) pointwise run records the resolved model, template-sourced
// model, and a REGIONAL effective host at the engine's configured location.
func TestRun_PopulatesApplied_Regional(t *testing.T) {
	fc := (&evaltest.FakeEvaluationClient{}).PushResponse(evaltest.NewPointwiseResponse(4.5, "ok"))
	eng := NewEngine(fc, "my-project", "us-central1")

	res, err := eng.Run(context.Background(), pointwiseTemplate(), helpfulnessInstance())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if res.Applied == nil {
		t.Fatal("Run must populate Result.Applied (got nil)")
	}
	got := res.Applied
	if got.Model != "gemini-2.5-flash" {
		t.Errorf("Applied.Model = %q, want gemini-2.5-flash", got.Model)
	}
	if got.EffectiveHost != "regional" {
		t.Errorf("Applied.EffectiveHost = %q, want regional", got.EffectiveHost)
	}
	if got.Location != "us-central1" {
		t.Errorf("Applied.Location = %q, want us-central1", got.Location)
	}
	if got.ModelSource != modelSourceTemplate {
		t.Errorf("Applied.ModelSource = %q, want %q", got.ModelSource, modelSourceTemplate)
	}
	if got.SamplingCount != 4 {
		t.Errorf("Applied.SamplingCount = %d, want 4 (from tmpl)", got.SamplingCount)
	}
	if got.FlipEnabled {
		t.Errorf("Applied.FlipEnabled = true, want false (from tmpl)")
	}
}

// A --model flag override is recorded on Applied as the resolved model with
// ModelSource=flag, proving Applied reflects what actually ran, not the template.
func TestRun_PopulatesApplied_FlagOverride(t *testing.T) {
	fc := (&evaltest.FakeEvaluationClient{}).PushResponse(evaltest.NewPointwiseResponse(3, "ok"))
	eng := NewEngine(fc, "my-project", "us-central1")

	res, err := eng.Run(context.Background(), pointwiseTemplate(), helpfulnessInstance(),
		WithModel("gemini-2.0-flash"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Applied == nil {
		t.Fatal("Run must populate Result.Applied (got nil)")
	}
	if res.Applied.Model != "gemini-2.0-flash" {
		t.Errorf("Applied.Model = %q, want gemini-2.0-flash (flag override)", res.Applied.Model)
	}
	if res.Applied.ModelSource != modelSourceFlag {
		t.Errorf("Applied.ModelSource = %q, want %q", res.Applied.ModelSource, modelSourceFlag)
	}
}

// A global-only judge (gemini-3.5 prefix) is routed to the GLOBAL host; Applied
// must record EffectiveHost=global and Location=global — the routing actually
// taken, not the engine's configured region.
func TestRun_PopulatesApplied_GlobalOnly(t *testing.T) {
	regional := &evaltest.FakeEvaluationClient{}
	global := (&evaltest.FakeEvaluationClient{}).PushResponse(evaltest.NewPointwiseResponse(5, "great"))
	eng := newRoutingEngine("us-central1", regional, global)

	res, err := eng.Run(context.Background(), globalOnlyTemplate(), helpfulnessInstance())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if res.Applied == nil {
		t.Fatal("Run must populate Result.Applied (got nil)")
	}
	got := res.Applied
	if got.Model != "gemini-3.5-flash" {
		t.Errorf("Applied.Model = %q, want gemini-3.5-flash", got.Model)
	}
	if got.EffectiveHost != "global" {
		t.Errorf("Applied.EffectiveHost = %q, want global", got.EffectiveHost)
	}
	if got.Location != "global" {
		t.Errorf("Applied.Location = %q, want global", got.Location)
	}
	if got.ModelSource != modelSourceTemplate {
		t.Errorf("Applied.ModelSource = %q, want %q", got.ModelSource, modelSourceTemplate)
	}
}
