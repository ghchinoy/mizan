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

// Package mcpserver exposes the mizan eval engine over the Model Context
// Protocol (MCP) as a THIN transport wrapper (issue ghchinoy/mizan#114). It
// registers four tools — mizan_list_metrics, mizan_get_metric, mizan_eval_run,
// and mizan_eval_pairwise — each mapping 1:1 onto an existing internal method
// the CLI already calls. No new eval logic lives here.
//
// CONTAINMENT RULE (design §2.1): only server.go imports the go-sdk `mcp`
// package (the constructor + the handler-signature wrappers). Everything else —
// the typed input/output structs in this file, the mapping helpers in
// mapping.go, and the handler logic in handlers.go — is SDK-agnostic, so the
// tool logic is unit-testable without the SDK and the package depends only on
// registry.Service / eval.Engine / results.Service / config, matching cmd/mizan's
// dependency direction.
package mcpserver

// --- mizan_list_metrics -------------------------------------------------------

// ListMetricsIn is the optional filter for mizan_list_metrics. A zero value
// lists every metric (maps onto a zero registry.ListFilter).
type ListMetricsIn struct {
	Modalities []string `json:"modalities,omitempty" jsonschema:"filter to metrics declaring any of these modalities (text|image|audio|video|music)"`
	Kinds      []string `json:"kinds,omitempty" jsonschema:"filter to metrics of any of these kinds (pointwise|pairwise|rubric|custom_schema|boul|choice|score|...)"`
	Tags       []string `json:"tags,omitempty" jsonschema:"filter to metrics carrying ALL of these tags"`
	Namespace  string   `json:"namespace,omitempty" jsonschema:"filter to metrics whose id namespace prefix equals this value"`
}

// MetricSummary is the per-metric projection returned by mizan_list_metrics.
type MetricSummary struct {
	ID          string   `json:"id"`
	Name        string   `json:"name,omitempty"`
	Kind        string   `json:"kind,omitempty"`
	Modalities  []string `json:"modalities,omitempty"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Version     string   `json:"version,omitempty"`
}

// ListMetricsOut is the mizan_list_metrics result.
type ListMetricsOut struct {
	Metrics []MetricSummary `json:"metrics"`
}

// --- mizan_get_metric ---------------------------------------------------------

// GetMetricIn is the input for mizan_get_metric.
type GetMetricIn struct {
	ID string `json:"id" jsonschema:"the metric template id to fetch (\"<namespace>/<slug>\")"`
}

// InputField is the declared per-placeholder schema an agent uses to build a
// valid mizan_eval_run / mizan_eval_pairwise call (projection of
// registry.InputSpec).
type InputField struct {
	Name     string `json:"name"`
	Modality string `json:"modality,omitempty"`
	Required bool   `json:"required"`
}

// GetMetricOut is the full template projection returned by mizan_get_metric.
type GetMetricOut struct {
	ID                   string       `json:"id"`
	Name                 string       `json:"name,omitempty"`
	Kind                 string       `json:"kind,omitempty"`
	Modalities           []string     `json:"modalities,omitempty"`
	Description          string       `json:"description,omitempty"`
	Version              string       `json:"version,omitempty"`
	Inputs               []InputField `json:"inputs,omitempty"`
	Choices              []string     `json:"choices,omitempty"`
	BaselineFieldName    string       `json:"baselineFieldName,omitempty"`
	CandidateFieldName   string       `json:"candidateFieldName,omitempty"`
	AutoraterModel       string       `json:"autoraterModel,omitempty"`
	SamplingCount        int32        `json:"samplingCount,omitempty"`
	FlipEnabled          bool         `json:"flipEnabled"`
	MetricPromptTemplate string       `json:"metricPromptTemplate,omitempty"`
	Tags                 []string     `json:"tags,omitempty"`
	// ResponseSchema is the raw JSON-Schema of the desired output shape for a
	// custom_schema metric (design §4). Empty for non-custom_schema metrics.
	ResponseSchema string `json:"responseSchema,omitempty"`
}

// --- shared field value -------------------------------------------------------

// FieldValue is one instance field value. EXACTLY ONE of Text, File, or GCS must
// be set, mirroring the CLI's --field / --file / --gcs flags (cmd buildInstance):
//   - Text: a literal text value          -> AssetRef{Modality: text, Text: v}
//   - File: a local file path             -> AssetRef{FilePath: v} (engine stages to GCS)
//   - GCS:  a pre-staged gs:// object URI -> AssetRef{GCSUri: v}
type FieldValue struct {
	Text string `json:"text,omitempty" jsonschema:"a literal text value for this field"`
	File string `json:"file,omitempty" jsonschema:"a local file path; the engine stages it to GCS (requires a staging bucket)"`
	GCS  string `json:"gcs,omitempty" jsonschema:"a pre-staged gs:// object URI"`
}

// --- mizan_eval_run -----------------------------------------------------------

// EvalRunIn is the input for mizan_eval_run.
type EvalRunIn struct {
	Metric   string                `json:"metric" jsonschema:"the metric template id to evaluate with"`
	Fields   map[string]FieldValue `json:"fields" jsonschema:"named instance fields keyed by the template's placeholder names; each value sets exactly one of text|file|gcs"`
	Model    string                `json:"model,omitempty" jsonschema:"autorater model override (highest precedence)"`
	Project  string                `json:"project,omitempty" jsonschema:"GCP project override for this call (applied by the cmd transport layer; see note in handlers.go)"`
	Location string                `json:"location,omitempty" jsonschema:"GCP location override for this call (applied by the cmd transport layer; see note in handlers.go)"`
	Store    *bool                 `json:"store,omitempty" jsonschema:"persist the run to the results store and return its runId (default true)"`
}

// EvalRunOut is the mizan_eval_run result (projection of eval.Result + RunID).
type EvalRunOut struct {
	Score          *float32       `json:"score,omitempty"`
	Passed         *bool          `json:"passed,omitempty"`
	Confidence     *float32       `json:"confidence,omitempty"`
	Choice         string         `json:"choice,omitempty"`
	PairwiseChoice string         `json:"pairwiseChoice,omitempty"`
	CustomOutput   map[string]any `json:"customOutput,omitempty"`
	Explanation    string         `json:"explanation,omitempty"`
	Warnings       []string       `json:"warnings,omitempty"`
	RunID          string         `json:"runId,omitempty"`
}

// --- mizan_eval_pairwise ------------------------------------------------------

// EvalPairwiseIn is the input for mizan_eval_pairwise. Baseline and Candidate
// are folded into the instance under the template's BaselineFieldName /
// CandidateFieldName.
type EvalPairwiseIn struct {
	Metric    string                `json:"metric" jsonschema:"the pairwise metric template id to evaluate with"`
	Baseline  FieldValue            `json:"baseline" jsonschema:"the baseline response (exactly one of text|file|gcs)"`
	Candidate FieldValue            `json:"candidate" jsonschema:"the candidate response (exactly one of text|file|gcs)"`
	Fields    map[string]FieldValue `json:"fields,omitempty" jsonschema:"any additional instance fields the metric prompt references"`
	Model     string                `json:"model,omitempty" jsonschema:"autorater model override (highest precedence)"`
	Project   string                `json:"project,omitempty" jsonschema:"GCP project override for this call (applied by the cmd transport layer; see note in handlers.go)"`
	Location  string                `json:"location,omitempty" jsonschema:"GCP location override for this call (applied by the cmd transport layer; see note in handlers.go)"`
	Store     *bool                 `json:"store,omitempty" jsonschema:"persist the run to the results store and return its runId (default true)"`
}

// EvalPairwiseOut is the mizan_eval_pairwise result. PairwiseChoice is one of
// BASELINE | CANDIDATE | TIE (or UNSPECIFIED on an unexpected enum).
type EvalPairwiseOut struct {
	PairwiseChoice string   `json:"pairwiseChoice"`
	Explanation    string   `json:"explanation,omitempty"`
	Warnings       []string `json:"warnings,omitempty"`
	RunID          string   `json:"runId,omitempty"`
}
