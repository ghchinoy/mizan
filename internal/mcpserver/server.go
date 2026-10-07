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

package mcpserver

// server.go is the ONLY file in this package that imports the go-sdk `mcp`
// package (design §2.1 containment rule): it constructs the shared *mcp.Server,
// registers the four tools, and provides the thin handler-signature adapters that
// delegate to the SDK-agnostic logic in handlers.go. Input/output JSON schemas
// are INFERRED by the SDK from the In/Out struct tags, so Tool.InputSchema /
// OutputSchema are left nil.

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ghchinoy/mizan/internal/config"
	"github.com/ghchinoy/mizan/internal/eval"
	"github.com/ghchinoy/mizan/internal/registry"
	"github.com/ghchinoy/mizan/internal/results"
	"github.com/ghchinoy/mizan/internal/version"
)

// Deps carries the service dependencies the MCP tools operate over. They are
// INJECTED (built once by the caller via wire.* in the cmd layer) rather than
// constructed here, so tests supply fakes and the package keeps cmd/mizan's
// dependency direction (depends only on the service façades + config).
type Deps struct {
	Registry *registry.Service
	Engine   *eval.Engine
	Results  *results.Service
	Config   *config.Config
}

// NewServer builds a single *mcp.Server with the four mizan tools registered on
// it. It is the one place that touches the SDK's server/tool API. The same server
// backs every transport (stdio / streamable HTTP), which the cmd layer mounts.
func NewServer(deps Deps) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{
		Name:    "mizan-mcp",
		Version: version.Get().Version,
	}, nil)

	h := &handlers{deps: deps}
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true}

	mcp.AddTool(s, &mcp.Tool{
		Name:        "mizan_list_metrics",
		Description: "List available mizan eval metrics (judges), optionally filtered by modality, kind, tags, or namespace.",
		Annotations: readOnly,
	}, h.listMetrics)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "mizan_get_metric",
		Description: "Get the full schema of one mizan metric template by id, including its declared input fields so a caller can build a valid eval request.",
		Annotations: readOnly,
	}, h.getMetric)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "mizan_eval_run",
		Description: "Run a mizan metric against an instance built from named fields and return the score/explanation. Persists the run by default and returns its runId.",
	}, h.evalRun)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "mizan_eval_pairwise",
		Description: "Run a mizan pairwise metric comparing a baseline against a candidate and return the winner (BASELINE|CANDIDATE|TIE). Persists the run by default and returns its runId.",
	}, h.evalPairwise)

	return s
}

// --- thin SDK adapters: delegate to the SDK-agnostic logic in handlers.go ------
// Returning a non-nil error makes the SDK emit a TOOL error (IsError=true); a nil
// *mcp.CallToolResult lets the SDK fill Content + StructuredContent from the Out.

func (h *handlers) listMetrics(ctx context.Context, _ *mcp.CallToolRequest, in ListMetricsIn) (*mcp.CallToolResult, ListMetricsOut, error) {
	out, err := h.list(ctx, in)
	return nil, out, err
}

func (h *handlers) getMetric(ctx context.Context, _ *mcp.CallToolRequest, in GetMetricIn) (*mcp.CallToolResult, GetMetricOut, error) {
	out, err := h.get(ctx, in)
	return nil, out, err
}

func (h *handlers) evalRun(ctx context.Context, _ *mcp.CallToolRequest, in EvalRunIn) (*mcp.CallToolResult, EvalRunOut, error) {
	out, err := h.run(ctx, in)
	return nil, out, err
}

func (h *handlers) evalPairwise(ctx context.Context, _ *mcp.CallToolRequest, in EvalPairwiseIn) (*mcp.CallToolResult, EvalPairwiseOut, error) {
	out, err := h.pairwise(ctx, in)
	return nil, out, err
}
