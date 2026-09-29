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

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	aiplatformpb "cloud.google.com/go/aiplatform/apiv1beta1/aiplatformpb"
	"google.golang.org/protobuf/proto"

	"github.com/ghchinoy/mizan/internal/config"
	"github.com/ghchinoy/mizan/internal/eval"
	"github.com/ghchinoy/mizan/internal/eval/evaltest"
)

// exactMatchPack is a one-template pack holding a kind:computation template.
const exactMatchPack = `apiVersion: mizan.dev/v1alpha1
kind: MetricTemplate
metadata:
  id: nlg/exact
  name: Exact match
  description: exact match
  version: 1.0.0
  license: Apache-2.0
spec:
  kind: computation
  modalities: [text]
  inputs:
    - name: response
      modality: text
      required: true
    - name: reference
      modality: text
      required: true
  native:
    metric: exact_match
`

// importComputationPack writes the pack to a temp dir and imports it into the
// throwaway registry configured by setupHeuristicCLIEnv.
func importComputationPack(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "templates", "exact.yaml"), []byte(exactMatchPack), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := executeRoot(t, "registry", "import", dir); err != nil {
		t.Fatalf("registry import: %v (out=%q)", err, out)
	}
}

// TestCLIComputationRunCredentialFree: `eval run` on a kind:computation template
// with no --engine runs locally with NO project, NO ADC and NO network — and
// never calls openEngine (which would build live clients).
func TestCLIComputationRunCredentialFree(t *testing.T) {
	setupHeuristicCLIEnv(t)
	importComputationPack(t)
	prev := openEngine
	openEngine = func(context.Context, *config.Config) (*eval.Engine, func() error, error) {
		t.Fatal("the local computation path must not open the live engine")
		return nil, nil, nil
	}
	t.Cleanup(func() { openEngine = prev })

	out, err := executeRoot(t, "--output", "json", "eval", "run", "--metric", "nlg/exact",
		"--field", "response= Paris ", "--field", "reference=Paris", "--model", "ignored-model")
	if err != nil {
		t.Fatalf("eval run: %v (out=%q)", err, out)
	}
	res := decodeResultJSON(t, out)
	if res.Score == nil || *res.Score != 1 {
		t.Errorf("Score = %v, want 1 (out=%q)", res.Score, out)
	}
	for _, want := range []string{"computation: no autorater (local exact_match, no network)", "--model is ignored for kind:computation"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

// TestCLIComputationRunVertexEngine: --engine vertex opens the live engine and
// the metric is computed by EvaluateInstances (here a fake client).
func TestCLIComputationRunVertexEngine(t *testing.T) {
	setupHeuristicCLIEnv(t)
	t.Setenv("MIZAN_PROJECT_ID", "demo-project")
	importComputationPack(t)
	fc := &evaltest.FakeEvaluationClient{Resp: &aiplatformpb.EvaluateInstancesResponse{
		EvaluationResults: &aiplatformpb.EvaluateInstancesResponse_ExactMatchResults{ExactMatchResults: &aiplatformpb.ExactMatchResults{
			ExactMatchMetricValues: []*aiplatformpb.ExactMatchMetricValue{{Score: proto.Float32(0)}}}}}}
	prev := openEngine
	openEngine = func(_ context.Context, cfg *config.Config) (*eval.Engine, func() error, error) {
		return eval.NewEngine(fc, cfg.ProjectID, "us-central1"), func() error { return nil }, nil
	}
	t.Cleanup(func() { openEngine = prev })

	out, err := executeRoot(t, "--output", "json", "eval", "run", "--metric", "nlg/exact",
		"--field", "response=Paris", "--field", "reference=paris", "--engine", "vertex")
	if err != nil {
		t.Fatalf("eval run: %v (out=%q)", err, out)
	}
	if fc.Calls() != 1 || fc.LastRequest().GetExactMatchInput() == nil {
		t.Fatalf("calls = %d, want one ExactMatchInput request", fc.Calls())
	}
	if res := decodeResultJSON(t, out); res.Score == nil || *res.Score != 0 {
		t.Errorf("Score = %v, want 0", res.Score)
	}
	if !strings.Contains(out, "via Vertex EvaluateInstances") {
		t.Errorf("pre-flight missing:\n%s", out)
	}
}

// TestCLIRegistryCreateComputationNeedsPack: `registry create --kind
// computation` has no spec.native flags, so it points the author at pack import.
func TestCLIRegistryCreateComputationNeedsPack(t *testing.T) {
	setupHeuristicCLIEnv(t)
	out, err := executeRoot(t, "registry", "create", "--id", "nlg/bleu", "--name", "BLEU", "--kind", "computation", "--input", "response:text:true")
	if err == nil || !strings.Contains(out+err.Error(), "spec.native") {
		t.Errorf("err = %v out = %q, want a spec.native / pack-import error", err, out)
	}
}
