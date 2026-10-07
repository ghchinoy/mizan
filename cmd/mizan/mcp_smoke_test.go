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

// mcp_smoke_test.go proves the four MCP tools work END-TO-END through the SDK
// transport layer (design §8 "transport smoke"), not just as direct handler
// calls. It builds a server via mcpserver.NewServer with FAKE deps (a fake eval
// client + temp-file SQLite registry/results, the same network-free approach as
// the Phase A handler tests), connects the SERVER first over the SDK's in-memory
// transport, then a client, and exercises one read tool (mizan_list_metrics) and
// one eval tool (mizan_eval_run) via cs.CallTool. No network, no ADC.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ghchinoy/mizan/internal/config"
	"github.com/ghchinoy/mizan/internal/eval"
	"github.com/ghchinoy/mizan/internal/eval/evaltest"
	"github.com/ghchinoy/mizan/internal/mcpserver"
	"github.com/ghchinoy/mizan/internal/registry"
	registrysqlite "github.com/ghchinoy/mizan/internal/registry/sqlite"
	"github.com/ghchinoy/mizan/internal/results"
	resultssqlite "github.com/ghchinoy/mizan/internal/results/sqlite"
)

const smokePointwiseID = "smoke/helpfulness"

// smokeTemplate is a minimal pointwise template seeded into the smoke-test
// registry so mizan_list_metrics and mizan_eval_run have something to operate on.
func smokeTemplate() registry.MetricTemplate {
	return registry.MetricTemplate{
		ID:                   smokePointwiseID,
		Name:                 "Helpfulness",
		Description:          "Rate response helpfulness.",
		Version:              "1.0.0",
		Kind:                 registry.KindPointwise,
		Modalities:           []registry.Modality{registry.ModalityText},
		Inputs:               []registry.InputSpec{{Name: "response", Modality: registry.ModalityText, Required: true}},
		MetricPromptTemplate: "Rate the helpfulness of this response: {{response}}",
		AutoraterModel:       "gemini-2.5-flash",
		SamplingCount:        4,
		Tags:                 []string{"quality"},
	}
}

// newSmokeDeps assembles network-free mcpserver.Deps: a registry.Service over a
// temp SQLite store seeded with the pointwise template, an eval.Engine over the
// scripted fake evaluation client, and a results.Service over a temp SQLite store.
func newSmokeDeps(t *testing.T, fake *evaltest.FakeEvaluationClient) mcpserver.Deps {
	t.Helper()
	ctx := context.Background()

	regStore, err := registrysqlite.Open(t.TempDir() + "/registry.db")
	if err != nil {
		t.Fatalf("open registry store: %v", err)
	}
	t.Cleanup(func() { _ = regStore.Close() })
	tmpl := smokeTemplate()
	if err := regStore.Put(ctx, &tmpl); err != nil {
		t.Fatalf("seed template: %v", err)
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

// connectSmoke builds the server from deps, connects it over the in-memory
// transport (SERVER FIRST, per the go-sdk contract), then connects a client and
// returns the client session.
func connectSmoke(t *testing.T, deps mcpserver.Deps) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()

	srv := mcpserver.NewServer(deps)
	serverT, clientT := mcp.NewInMemoryTransports()
	if _, err := srv.Connect(ctx, serverT, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "mizan-mcp-smoke", Version: "v0.0.1"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// decodeStructured re-marshals the untyped StructuredContent then unmarshals it
// into out — the confirmed pattern for reading typed tool output client-side
// (the SDK exposes StructuredContent as `any`).
func decodeStructured(t *testing.T, res *mcp.CallToolResult, out any) {
	t.Helper()
	if res.IsError {
		t.Fatalf("tool returned an error result: %+v", res.Content)
	}
	b, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	if err := json.Unmarshal(b, out); err != nil {
		t.Fatalf("unmarshal into %T: %v", out, err)
	}
}

// TestMcpListMetricsThroughTransport exercises a READ tool end-to-end through the
// SDK transport layer.
func TestMcpListMetricsThroughTransport(t *testing.T) {
	cs := connectSmoke(t, newSmokeDeps(t, &evaltest.FakeEvaluationClient{}))

	// Tool discovery works through the transport.
	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	names := map[string]bool{}
	for _, tl := range tools.Tools {
		names[tl.Name] = true
	}
	for _, want := range []string{"mizan_list_metrics", "mizan_get_metric", "mizan_eval_run", "mizan_eval_pairwise"} {
		if !names[want] {
			t.Errorf("tool %q not advertised over the transport (got %v)", want, names)
		}
	}

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "mizan_list_metrics",
		Arguments: mcpserver.ListMetricsIn{},
	})
	if err != nil {
		t.Fatalf("call mizan_list_metrics: %v", err)
	}
	var out mcpserver.ListMetricsOut
	decodeStructured(t, res, &out)
	if len(out.Metrics) != 1 || out.Metrics[0].ID != smokePointwiseID {
		t.Fatalf("list metrics = %+v, want one %q", out.Metrics, smokePointwiseID)
	}
}

// TestMcpEvalRunThroughTransport exercises an EVAL tool end-to-end through the SDK
// transport layer, asserting the mapped structured result (score/explanation/runId).
func TestMcpEvalRunThroughTransport(t *testing.T) {
	fake := (&evaltest.FakeEvaluationClient{}).PushResponse(
		evaltest.NewPointwiseResponse(4.5, "Clear and correct."))
	cs := connectSmoke(t, newSmokeDeps(t, fake))

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "mizan_eval_run",
		Arguments: mcpserver.EvalRunIn{
			Metric: smokePointwiseID,
			Fields: map[string]mcpserver.FieldValue{"response": {Text: "Click the reset link."}},
		},
	})
	if err != nil {
		t.Fatalf("call mizan_eval_run: %v", err)
	}
	var out mcpserver.EvalRunOut
	decodeStructured(t, res, &out)
	if out.Score == nil || *out.Score != 4.5 {
		t.Errorf("score = %v, want 4.5", out.Score)
	}
	if out.Explanation != "Clear and correct." {
		t.Errorf("explanation = %q, want %q", out.Explanation, "Clear and correct.")
	}
	if out.RunID == "" {
		t.Error("expected a non-empty runId (store default-on) through the transport")
	}
}
