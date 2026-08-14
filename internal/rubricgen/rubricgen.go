// Package rubricgen is a small, hand-rolled REST client for the synchronous
// Vertex AI `:generateInstanceRubrics` RPC (v1beta1). That RPC — and the Rubric
// type it returns — are NOT code-generated in the Go aiplatform module
// (aiplatform@v1.126.0: zero occurrences; the REST/v1beta1 surface is ahead of
// the Go proto), so Mizan speaks JSON to the one endpoint directly. See
// design/adaptive-rubrics-support-plan.md §4.2 and
// research/adaptive-rubrics-live-probe-2026-08-14.md (Evidence 5/6).
//
// This is Stage 1 (GENERATE) of adaptive rubrics: given a sample prompt it
// returns a list of suggested Rubric criteria that a human reviews/edits/freezes
// into an ordinary mizan KindRubric template (Stage 2 = the EXISTING deterministic
// eval path, untouched). The package is deliberately tiny: one endpoint, the
// predefined-recipe path, synchronous, single-instance, GCS-free.
package rubricgen

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"

	"google.golang.org/api/option"
	htransport "google.golang.org/api/transport/http"

	"github.com/ghchinoy/mizan/internal/config"
)

// cloudPlatformScope is the OAuth scope the ADC bearer token is minted for — the
// SAME scope the existing native/genai clients use. No new auth is introduced.
const cloudPlatformScope = "https://www.googleapis.com/auth/cloud-platform"

// maxResponseBytes bounds the response body read so a hostile or degenerate API
// response cannot make the client allocate without limit (defensive parsing).
const maxResponseBytes = 8 << 20 // 8 MiB

// maxRubrics caps how many generated rubrics are accepted from one response, so a
// pathological response cannot flood the draft template / registry.
const maxRubrics = 1024

// Rubric mirrors GoogleCloudAiplatformV1beta1Rubric (v1beta1 REST; absent from the
// Go proto). Only the fields Mizan consumes are modeled; unknown fields are
// ignored on decode. The testable criterion string is Content.Property.Description.
type Rubric struct {
	RubricID   string  `json:"rubricId,omitempty"`
	Type       string  `json:"type,omitempty"`       // e.g. "FORMAT_REQUIREMENT:SENTENCE_COUNT"
	Importance string  `json:"importance,omitempty"` // HIGH | MEDIUM | LOW
	Content    Content `json:"content"`
}

// Content wraps a rubric's property (the shape the API returns).
type Content struct {
	Property Property `json:"property"`
}

// Property carries the single testable criterion description.
type Property struct {
	Description string `json:"description"`
}

// InstanceContent is one request `contents[]` entry: a bag of parts.
type InstanceContent struct {
	Parts []Part `json:"parts"`
}

// Part is one content part. Phase 1 sends text only.
type Part struct {
	Text string `json:"text"`
}

// TextContents builds the single-text-part contents slice from a sample prompt —
// the common Phase 1 case.
func TextContents(sample string) []InstanceContent {
	return []InstanceContent{{Parts: []Part{{Text: sample}}}}
}

// Spec selects the generation recipe. Phase 1 supports the predefined-recipe path
// only (the custom rubricGenerationSpec path is a later phase); PredefinedMetric
// is required.
type Spec struct {
	// PredefinedMetric is the pinned recipe version, e.g. "general_quality_v1".
	// (Live probe: general_quality_v2 returned 400 in-project; pin a version.)
	PredefinedMetric string
}

// Client is the narrow seam over the generation RPC. The concrete REST client
// satisfies it in production; a scriptable fake satisfies it in tests (see
// rubricgentest), so callers are testable without a live call or credentials.
type Client interface {
	Generate(ctx context.Context, contents []InstanceContent, spec Spec) ([]Rubric, error)
}

// --- REST wire structs -------------------------------------------------------

type generateRequest struct {
	Contents                       []InstanceContent `json:"contents"`
	PredefinedRubricGenerationSpec *predefinedSpec   `json:"predefinedRubricGenerationSpec,omitempty"`
}

type predefinedSpec struct {
	MetricSpecName string `json:"metric_spec_name"`
}

type generateResponse struct {
	GeneratedRubrics []Rubric `json:"generatedRubrics"`
}

// vertexError models the Vertex JSON error envelope so error.message can be
// surfaced VERBATIM (INVALID_ARGUMENT etc.) rather than a generic wrapper.
type vertexError struct {
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
	} `json:"error"`
}

// RESTClient is the concrete hand-rolled client for :generateInstanceRubrics.
type RESTClient struct {
	httpClient *http.Client
	baseURL    string // e.g. https://us-central1-aiplatform.googleapis.com
	projectID  string
	location   string
}

// NewRESTClient builds an ADC-authenticated REST client for the regional
// generation endpoint.
//
// SECURITY: an apiEndpoint override is validated through the EXISTING
// *.googleapis.com allow-list (config.ValidateEndpoint) BEFORE the authenticated
// (ADC bearer-token) client is constructed, so no attacker-controlled string can
// redirect the token to a non-Google host. The credential chain is the SAME one
// the native/genai clients use (transport/http.NewClient with the cloud-platform
// scope) — no new or fake credentials.
func NewRESTClient(ctx context.Context, projectID, location, apiEndpoint string) (*RESTClient, error) {
	if projectID == "" {
		return nil, fmt.Errorf("rubricgen: project id is required")
	}
	base, err := restBaseURL(location, apiEndpoint)
	if err != nil {
		return nil, err
	}
	// Build the ADC transport AFTER the endpoint is validated. option.WithEndpoint
	// pins the host the transport dials to the validated base URL.
	hc, _, err := htransport.NewClient(ctx,
		option.WithScopes(cloudPlatformScope),
		option.WithEndpoint(base),
	)
	if err != nil {
		return nil, fmt.Errorf("rubricgen: build authenticated client: %w", err)
	}
	return &RESTClient{
		httpClient: hc,
		baseURL:    base,
		projectID:  projectID,
		location:   locationOrDefault(location),
	}, nil
}

// restBaseURL computes the https base URL for the generation endpoint and gates
// any override through the same allow-list the native/genai paths use.
//
//   - apiEndpoint override: validated via config.ValidateEndpoint (rejects a host
//     that is not *.googleapis.com unless MIZAN_ALLOW_CUSTOM_ENDPOINT=1), then its
//     bare host is used over https. Never send the ADC token to an unvalidated host.
//   - otherwise: the regional host {loc}-aiplatform.googleapis.com; "" and
//     "global" map to the bare global host aiplatform.googleapis.com (mirroring
//     eval.endpointFor).
func restBaseURL(location, apiEndpoint string) (string, error) {
	if apiEndpoint != "" {
		if err := config.ValidateEndpoint(apiEndpoint); err != nil {
			return "", err
		}
		host := apiEndpoint
		host = strings.TrimPrefix(host, "https://")
		host = strings.TrimPrefix(host, "http://")
		host = strings.TrimSuffix(host, "/")
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		return "https://" + host, nil
	}
	loc := locationOrDefault(location)
	if loc == "global" {
		return "https://aiplatform.googleapis.com", nil
	}
	return "https://" + loc + "-aiplatform.googleapis.com", nil
}

// locationOrDefault normalizes an empty location to "global" (the bare-host case),
// matching eval.endpointFor's treatment of "".
func locationOrDefault(location string) string {
	if location == "" {
		return "global"
	}
	return location
}

// Generate performs the synchronous generation call and returns the criteria in
// DECLARED order. Errors surface the Vertex error.message verbatim when present.
func (c *RESTClient) Generate(ctx context.Context, contents []InstanceContent, spec Spec) ([]Rubric, error) {
	if spec.PredefinedMetric == "" {
		return nil, fmt.Errorf("rubricgen: predefined recipe (metric_spec_name) is required")
	}
	if len(contents) == 0 {
		return nil, fmt.Errorf("rubricgen: at least one content is required")
	}

	body, err := json.Marshal(generateRequest{
		Contents:                       contents,
		PredefinedRubricGenerationSpec: &predefinedSpec{MetricSpecName: spec.PredefinedMetric},
	})
	if err != nil {
		return nil, fmt.Errorf("rubricgen: marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/v1beta1/projects/%s/locations/%s:generateInstanceRubrics",
		c.baseURL, c.projectID, c.location)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("rubricgen: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("rubricgen: call %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("rubricgen: read response: %w", err)
	}
	if len(raw) > maxResponseBytes {
		return nil, fmt.Errorf("rubricgen: response exceeds %d-byte cap", maxResponseBytes)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, decodeError(resp.StatusCode, raw)
	}

	var parsed generateResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("rubricgen: parse response (HTTP %d): %w", resp.StatusCode, err)
	}
	if len(parsed.GeneratedRubrics) == 0 {
		return nil, fmt.Errorf("rubricgen: API returned no rubrics for the given prompt")
	}
	if len(parsed.GeneratedRubrics) > maxRubrics {
		return nil, fmt.Errorf("rubricgen: API returned %d rubrics, exceeds the %d cap",
			len(parsed.GeneratedRubrics), maxRubrics)
	}
	return parsed.GeneratedRubrics, nil
}

// decodeError turns a non-200 response into an error, surfacing the Vertex
// error.message VERBATIM when the body parses as the standard error envelope, and
// falling back to a bounded snippet otherwise. It never panics on hostile input.
func decodeError(status int, raw []byte) error {
	var ve vertexError
	if err := json.Unmarshal(raw, &ve); err == nil && ve.Error.Message != "" {
		if ve.Error.Status != "" {
			return fmt.Errorf("rubricgen: generation failed (HTTP %d %s): %s",
				status, ve.Error.Status, ve.Error.Message)
		}
		return fmt.Errorf("rubricgen: generation failed (HTTP %d): %s", status, ve.Error.Message)
	}
	snippet := strings.TrimSpace(string(raw))
	const maxSnippet = 512
	if len(snippet) > maxSnippet {
		snippet = snippet[:maxSnippet] + "…"
	}
	if snippet == "" {
		return fmt.Errorf("rubricgen: generation failed (HTTP %d) with empty body", status)
	}
	return fmt.Errorf("rubricgen: generation failed (HTTP %d): %s", status, snippet)
}

// ToRubricGroups converts generated rubrics into a mizan RubricGroups map: a
// SINGLE group keyed by groupName whose value is the criteria descriptions in
// DECLARED order. It is pure and deterministic — order is a tested invariant.
// Empty descriptions are skipped (defensive against a partial response). Returns
// an empty map when there are no usable criteria; the caller decides whether that
// is an error.
func ToRubricGroups(rubrics []Rubric, groupName string) map[string][]string {
	criteria := make([]string, 0, len(rubrics))
	for _, r := range rubrics {
		desc := strings.TrimSpace(r.Content.Property.Description)
		if desc == "" {
			continue
		}
		criteria = append(criteria, desc)
	}
	if len(criteria) == 0 {
		return map[string][]string{}
	}
	return map[string][]string{groupName: criteria}
}
