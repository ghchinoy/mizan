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

// mcp_smoke_more_test.go strengthens the transport smoke suite (test-engineer
// pass, issue #114) by exercising the PAIRWISE eval tool and the TOOL-ERROR path
// end-to-end through the SDK transport: a handler-returned error must surface as
// a CallToolResult with IsError=true (not a Go transport error), per go-sdk
// semantics. Reuses the network-free smoke harness (fake eval client + temp-file
// SQLite). No network, no ADC.

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	aiplatformpb "cloud.google.com/go/aiplatform/apiv1beta1/aiplatformpb"

	"github.com/ghchinoy/mizan/internal/config"
	"github.com/ghchinoy/mizan/internal/eval"
	"github.com/ghchinoy/mizan/internal/eval/evaltest"
	"github.com/ghchinoy/mizan/internal/mcpserver"
	"github.com/ghchinoy/mizan/internal/registry"
	registrysqlite "github.com/ghchinoy/mizan/internal/registry/sqlite"
	"github.com/ghchinoy/mizan/internal/results"
	resultssqlite "github.com/ghchinoy/mizan/internal/results/sqlite"
)

const smokePairwiseID = "smoke/preference"

// newSmokeDepsPairwise assembles network-free deps seeded with BOTH a pointwise
// and a pairwise template, so the pairwise transport path has a metric to use.
func newSmokeDepsPairwise(t *testing.T, fake *evaltest.FakeEvaluationClient) mcpserver.Deps {
	t.Helper()
	ctx := context.Background()

	regStore, err := registrysqlite.Open(t.TempDir() + "/registry.db")
	if err != nil {
		t.Fatalf("open registry store: %v", err)
	}
	t.Cleanup(func() { _ = regStore.Close() })
	tmpls := []registry.MetricTemplate{
		smokeTemplate(),
		{
			ID:                   smokePairwiseID,
			Name:                 "Preference",
			Description:          "Which response is better.",
			Version:              "1.0.0",
			Kind:                 registry.KindPairwise,
			Modalities:           []registry.Modality{registry.ModalityText},
			MetricPromptTemplate: "Which is better, {{baseline}} or {{candidate}}?",
			BaselineFieldName:    "baseline",
			CandidateFieldName:   "candidate",
			AutoraterModel:       "gemini-2.5-flash",
			SamplingCount:        4,
		},
	}
	for _, tmpl := range tmpls {
		tmpl := tmpl
		if err := regStore.Put(ctx, &tmpl); err != nil {
			t.Fatalf("seed template %s: %v", tmpl.ID, err)
		}
	}

	resStore, err := resultssqlite.Open(t.TempDir() + "/results.db")
	if err != nil {
		t.Fatalf("open results store: %v", err)
	}
	t.Cleanup(func() { _ = resStore.Close() })

	return mcpserver.Deps{
		Registry: registry.NewService(regStore),
		Engine:   eval.NewEngine(fake, "proj", "us-central1"),
		Results:  results.NewService(resStore),
		Config:   &config.Config{ProjectID: "proj", Location: "us-central1"},
	}
}

// TestMcpEvalPairwiseThroughTransport exercises the pairwise eval tool end-to-end
// through the SDK transport, asserting the mapped BASELINE|CANDIDATE|TIE choice.
func TestMcpEvalPairwiseThroughTransport(t *testing.T) {
	fake := (&evaltest.FakeEvaluationClient{}).PushResponse(
		evaltest.NewPairwiseResponse(aiplatformpb.PairwiseChoice_CANDIDATE, "B is sharper."))
	cs := connectSmoke(t, newSmokeDepsPairwise(t, fake))

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "mizan_eval_pairwise",
		Arguments: mcpserver.EvalPairwiseIn{
			Metric:    smokePairwiseID,
			Baseline:  mcpserver.FieldValue{Text: "answer A"},
			Candidate: mcpserver.FieldValue{Text: "answer B"},
		},
	})
	if err != nil {
		t.Fatalf("call mizan_eval_pairwise: %v", err)
	}
	var out mcpserver.EvalPairwiseOut
	decodeStructured(t, res, &out)
	if out.PairwiseChoice != "CANDIDATE" {
		t.Errorf("pairwiseChoice = %q, want CANDIDATE", out.PairwiseChoice)
	}
	if out.Explanation != "B is sharper." {
		t.Errorf("explanation = %q", out.Explanation)
	}
	if out.RunID == "" {
		t.Error("expected a non-empty runId (store default-on) through the transport")
	}
}

// TestMcpToolErrorThroughTransport asserts that a handler-returned error (here,
// an unknown metric id) surfaces as a TOOL error — CallTool returns no Go error,
// but the result has IsError=true — rather than a transport/protocol failure.
func TestMcpToolErrorThroughTransport(t *testing.T) {
	cs := connectSmoke(t, newSmokeDeps(t, &evaltest.FakeEvaluationClient{}))

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "mizan_eval_run",
		Arguments: mcpserver.EvalRunIn{
			Metric: "smoke/does-not-exist",
			Fields: map[string]mcpserver.FieldValue{"response": {Text: "x"}},
		},
	})
	if err != nil {
		t.Fatalf("CallTool returned a Go/transport error; a tool error should come back in the result: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError=true for an unknown metric, got result %+v", res)
	}
}
