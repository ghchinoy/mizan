// Package evaltest provides REUSABLE, network-free test doubles for the eval
// engine's two client seams: FakeEvaluationClient (the native EvaluateInstances
// seam) and FakeGenaiClient (the genai GenerateContent seam), plus the response
// and error builders used to script them.
//
// These live in their OWN package (not a _test.go file in package eval) so they
// can be imported ACROSS package boundaries — internal/eval tests, internal/wire
// tests, and cmd/mizan tests can all share one set of fakes. They were extracted
// here from internal/eval/fakeclient_test.go (R-SMOKE) when internal/wire needed
// them cross-package (R-GAPS); the same extraction normalized the capture-field
// naming (both fakes now expose a consistently-named CallsLog).
//
// Because this is a normal (non-test) package, nothing links it into the mizan
// binary unless a production package imports it — and none do; only *_test.go
// files import evaltest, so it stays out of the shipped binary.
//
// Surface (deliberately small and ergonomic):
//   - script responses:  PushResponse / PushError (per-call queue) or the sticky
//     Resp / Err fallback used once the queue drains.
//   - capture calls:      CallsLog (in call order) + LastRequest / LastCall /
//     Calls, so routing and spec assertions (e.g. Location) are possible.
//   - drive errors:       PushError + the NewResourceExhausted helper, so the
//     genai retry/backoff and native error paths can be exercised.
package evaltest

import (
	"context"

	aiplatformpb "cloud.google.com/go/aiplatform/apiv1beta1/aiplatformpb"
	gax "github.com/googleapis/gax-go/v2"
	"google.golang.org/genai"
	"google.golang.org/protobuf/proto"
)

// --- native EvaluateInstances seam --------------------------------------------

// evalTurn is one scripted (response, error) outcome for FakeEvaluationClient.
type evalTurn struct {
	resp *aiplatformpb.EvaluateInstancesResponse
	err  error
}

// FakeEvaluationClient is a scriptable, network-free eval.EvaluationClient. It
// records every request it receives and returns scripted outcomes, so the
// engine's spec materialization, result mapping, routing and error handling are
// all testable without a live API call or credentials.
//
// Outcome selection: if any turns were scripted via PushResponse/PushError they
// are consumed in order, one per call; once the queue is empty the sticky Resp /
// Err are returned for every subsequent call. A zero-value FakeEvaluationClient
// therefore returns (nil, nil) — set Resp (or push a turn) for a useful default.
//
// It also implements Close so it satisfies the composition root's closable
// native-client seam (internal/wire), which lets wire tests assert both native
// clients are closed.
type FakeEvaluationClient struct {
	// CallsLog captures every EvaluateInstancesRequest received, in call order.
	// (Named to match FakeGenaiClient.CallsLog — the R-SMOKE naming nit.)
	CallsLog []*aiplatformpb.EvaluateInstancesRequest
	// CallOpts captures the gax.CallOption slice passed to each call, in order.
	CallOpts [][]gax.CallOption

	// Resp / Err are the sticky fallback returned once the scripted queue drains.
	Resp *aiplatformpb.EvaluateInstancesResponse
	Err  error

	// Closed counts Close() invocations (for wire close-path assertions).
	Closed int

	turns []evalTurn
}

// PushResponse queues one successful response for the next call. Returns the
// receiver for chaining.
func (f *FakeEvaluationClient) PushResponse(resp *aiplatformpb.EvaluateInstancesResponse) *FakeEvaluationClient {
	f.turns = append(f.turns, evalTurn{resp: resp})
	return f
}

// PushError queues one error for the next call (drives the native error path).
// Returns the receiver for chaining.
func (f *FakeEvaluationClient) PushError(err error) *FakeEvaluationClient {
	f.turns = append(f.turns, evalTurn{err: err})
	return f
}

// EvaluateInstances records the request and returns the next scripted outcome
// (or the sticky Resp/Err fallback). It satisfies the eval.EvaluationClient seam.
func (f *FakeEvaluationClient) EvaluateInstances(_ context.Context, req *aiplatformpb.EvaluateInstancesRequest, opts ...gax.CallOption) (*aiplatformpb.EvaluateInstancesResponse, error) {
	f.CallsLog = append(f.CallsLog, req)
	f.CallOpts = append(f.CallOpts, opts)
	if len(f.turns) > 0 {
		t := f.turns[0]
		f.turns = f.turns[1:]
		return t.resp, t.err
	}
	return f.Resp, f.Err
}

// Close records the invocation and returns nil. It lets the fake satisfy the
// closable-client seam the composition root (internal/wire) closes on shutdown.
func (f *FakeEvaluationClient) Close() error {
	f.Closed++
	return nil
}

// Calls returns the number of EvaluateInstances calls received.
func (f *FakeEvaluationClient) Calls() int { return len(f.CallsLog) }

// LastRequest returns the most recent request received, or nil if none.
func (f *FakeEvaluationClient) LastRequest() *aiplatformpb.EvaluateInstancesRequest {
	if len(f.CallsLog) == 0 {
		return nil
	}
	return f.CallsLog[len(f.CallsLog)-1]
}

// NewPointwiseResponse builds an EvaluateInstancesResponse carrying a pointwise
// metric result (used by the pointwise and native-rubric paths).
func NewPointwiseResponse(score float32, explanation string) *aiplatformpb.EvaluateInstancesResponse {
	return &aiplatformpb.EvaluateInstancesResponse{
		EvaluationResults: &aiplatformpb.EvaluateInstancesResponse_PointwiseMetricResult{
			PointwiseMetricResult: &aiplatformpb.PointwiseMetricResult{
				Score:       proto.Float32(score),
				Explanation: explanation,
			},
		},
	}
}

// NewPairwiseResponse builds an EvaluateInstancesResponse carrying a pairwise
// metric result (choice enum + explanation).
func NewPairwiseResponse(choice aiplatformpb.PairwiseChoice, explanation string) *aiplatformpb.EvaluateInstancesResponse {
	return &aiplatformpb.EvaluateInstancesResponse{
		EvaluationResults: &aiplatformpb.EvaluateInstancesResponse_PairwiseMetricResult{
			PairwiseMetricResult: &aiplatformpb.PairwiseMetricResult{
				PairwiseChoice: choice,
				Explanation:    explanation,
			},
		},
	}
}

// --- genai GenerateContent seam -----------------------------------------------

// GenaiCall captures the arguments of one GenerateContent invocation, so routing
// and config assertions (model id, response schema, system instruction) are
// possible.
type GenaiCall struct {
	Model    string
	Contents []*genai.Content
	Config   *genai.GenerateContentConfig
}

// genaiTurn is one scripted (response, error) outcome for FakeGenaiClient.
type genaiTurn struct {
	resp *genai.GenerateContentResponse
	err  error
}

// FakeGenaiClient is a scriptable, network-free eval.GenaiClient. It records
// every call and returns scripted outcomes, so the genai structured-output path
// (custom_schema and rubric-detail), its JSON parsing, token-usage capture and
// retry/backoff behavior are testable without credentials.
//
// Outcome selection mirrors FakeEvaluationClient: scripted turns are consumed in
// order (one per call), then the sticky Resp / Err are returned. Scripting a
// RESOURCE_EXHAUSTED error (see NewResourceExhausted) before a success drives the
// engine's retry loop.
type FakeGenaiClient struct {
	// CallsLog captures every GenerateContent invocation's arguments, in order.
	CallsLog []GenaiCall

	// Resp / Err are the sticky fallback returned once the scripted queue drains.
	Resp *genai.GenerateContentResponse
	Err  error

	turns []genaiTurn
}

// PushResponse queues one successful response for the next call.
func (f *FakeGenaiClient) PushResponse(resp *genai.GenerateContentResponse) *FakeGenaiClient {
	f.turns = append(f.turns, genaiTurn{resp: resp})
	return f
}

// PushJSON queues one successful response whose single text part is jsonText and
// whose UsageMetadata is usage (nil for none). This is the common case for the
// structured-output path.
func (f *FakeGenaiClient) PushJSON(jsonText string, usage *genai.GenerateContentResponseUsageMetadata) *FakeGenaiClient {
	return f.PushResponse(NewGenaiJSONResponse(jsonText, usage))
}

// PushError queues one error for the next call (e.g. NewResourceExhausted to
// drive retry, or any other error to drive the non-retryable failure path).
func (f *FakeGenaiClient) PushError(err error) *FakeGenaiClient {
	f.turns = append(f.turns, genaiTurn{err: err})
	return f
}

// GenerateContent records the call and returns the next scripted outcome (or the
// sticky Resp/Err fallback). It satisfies the eval.GenaiClient seam.
func (f *FakeGenaiClient) GenerateContent(_ context.Context, model string, contents []*genai.Content, config *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
	f.CallsLog = append(f.CallsLog, GenaiCall{Model: model, Contents: contents, Config: config})
	if len(f.turns) > 0 {
		t := f.turns[0]
		f.turns = f.turns[1:]
		return t.resp, t.err
	}
	return f.Resp, f.Err
}

// Calls returns the number of GenerateContent calls received.
func (f *FakeGenaiClient) Calls() int { return len(f.CallsLog) }

// LastCall returns the most recent recorded call, or nil if none.
func (f *FakeGenaiClient) LastCall() *GenaiCall {
	if len(f.CallsLog) == 0 {
		return nil
	}
	return &f.CallsLog[len(f.CallsLog)-1]
}

// NewGenaiJSONResponse builds a GenerateContentResponse with a single text part
// (jsonText) and the given UsageMetadata (nil for none).
func NewGenaiJSONResponse(jsonText string, usage *genai.GenerateContentResponseUsageMetadata) *genai.GenerateContentResponse {
	return &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{{
			Content: &genai.Content{Parts: []*genai.Part{{Text: jsonText}}},
		}},
		UsageMetadata: usage,
	}
}

// NewTokenUsage builds a genai UsageMetadata for token-usage capture assertions.
func NewTokenUsage(prompt, candidates, total int32) *genai.GenerateContentResponseUsageMetadata {
	return &genai.GenerateContentResponseUsageMetadata{
		PromptTokenCount:     prompt,
		CandidatesTokenCount: candidates,
		TotalTokenCount:      total,
	}
}

// NewResourceExhausted returns a genai RESOURCE_EXHAUSTED (HTTP 429) error, the
// only error class the genai path retries. genai.APIError is a value type, so it
// is returned by value (matching isResourceExhausted's errors.As target).
func NewResourceExhausted() error {
	return genai.APIError{Code: 429, Status: "RESOURCE_EXHAUSTED", Message: "quota"}
}
