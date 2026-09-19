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

package diffusion

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client is the mockable interface for interacting with DiffusionGemma.
type Client interface {
	Complete(ctx context.Context, req ChatCompletionRequest) (*ChatCompletionResponse, *RequestStats, error)
	Decide(ctx context.Context, schemaContent, userStateContent string, images ...string) (*StructuredDecisionResponse, *RequestStats, error)
}

// HTTPClient communicates with the local or remote diffgemma server over HTTP.
type HTTPClient struct {
	BaseURL   string
	HTTP      *http.Client
	Model     string
	AuthToken string
}

// NewClient creates a new HTTPClient targeting the given endpoint.
func NewClient(baseURL, defaultModel string, timeout time.Duration) *HTTPClient {
	if baseURL == "" {
		baseURL = "http://127.0.0.1:8080/v1"
	}
	baseURL = strings.TrimRight(baseURL, "/")

	if timeout <= 0 {
		timeout = 120 * time.Second
	}

	return &HTTPClient{
		BaseURL: baseURL,
		HTTP:    &http.Client{Timeout: timeout},
		Model:   defaultModel,
	}
}

// WithAuthToken sets the optional Authorization Bearer / IAM token.
func (c *HTTPClient) WithAuthToken(token string) *HTTPClient {
	c.AuthToken = strings.TrimSpace(token)
	return c
}

// Complete executes an OpenAI-compatible chat completion.
func (c *HTTPClient) Complete(ctx context.Context, req ChatCompletionRequest) (*ChatCompletionResponse, *RequestStats, error) {
	if req.Model == "" {
		req.Model = c.Model
	}

	endpoint := fmt.Sprintf("%s/chat/completions", c.BaseURL)
	payloadBytes, err := json.Marshal(req)
	if err != nil {
		return nil, nil, fmt.Errorf("diffusion: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payloadBytes))
	if err != nil {
		return nil, nil, fmt.Errorf("diffusion: create http request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	if c.AuthToken != "" {
		token := c.AuthToken
		if !strings.HasPrefix(strings.ToLower(token), "bearer ") {
			token = "Bearer " + token
		}
		httpReq.Header.Set("Authorization", token)
	}

	start := time.Now()
	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return nil, nil, fmt.Errorf("diffusion: http request failed to %s: %w", endpoint, err)
	}
	defer resp.Body.Close()
	wallTime := time.Since(start)

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("diffusion: read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, nil, fmt.Errorf("diffusion: server returned HTTP %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var chatResp ChatCompletionResponse
	if err := json.Unmarshal(bodyBytes, &chatResp); err != nil {
		return nil, nil, fmt.Errorf("diffusion: unmarshal response: %w", err)
	}

	stats := &RequestStats{
		Model:        chatResp.Model,
		Endpoint:     endpoint,
		WallTime:     wallTime,
		PromptTokens: chatResp.Usage.PromptTokens,
		OutputTokens: chatResp.Usage.CompletionTokens,
		TotalTokens:  chatResp.Usage.TotalTokens,
	}

	if len(chatResp.Choices) > 0 {
		content := chatResp.Choices[0].Message.RawContent()
		if structured, err := ParseStructuredContent(content); err == nil && structured.Diagnostics.Steps > 0 {
			stats.Diagnostics = &structured.Diagnostics
			stats.PrefillMs = structured.Diagnostics.Timing.PrefillMs
			stats.DenoiseMs = structured.Diagnostics.Timing.DenoiseMs
			stats.ReusedTokens = structured.Diagnostics.Timing.ReusedTokens
			stats.DenoiseSteps = structured.Diagnostics.Timing.StepsRun
			stats.SamplesN = structured.Diagnostics.Samples.N
			stats.Extended = structured.Diagnostics.Samples.Policy.Extended
		}
	}

	return &chatResp, stats, nil
}

// Decide executes a discrete diffusion slot readout decision query with optional multimodal images.
func (c *HTTPClient) Decide(ctx context.Context, schemaContent, userStateContent string, images ...string) (*StructuredDecisionResponse, *RequestStats, error) {
	userPayload, err := BuildMultimodalContent(userStateContent, images)
	if err != nil {
		return nil, nil, fmt.Errorf("diffusion: build multimodal payload: %w", err)
	}

	req := ChatCompletionRequest{
		Messages: []ChatMessage{
			{Role: "system", Content: schemaContent},
			{Role: "user", Content: userPayload},
		},
	}

	chatResp, stats, err := c.Complete(ctx, req)
	if err != nil {
		return nil, nil, err
	}

	if len(chatResp.Choices) == 0 {
		return nil, stats, fmt.Errorf("diffusion: no response choices returned by model")
	}

	rawText := chatResp.Choices[0].Message.RawContent()
	structured, err := ParseStructuredContent(rawText)
	if err != nil {
		return nil, stats, fmt.Errorf("diffusion: parse decision output: %w (raw: %s)", err, rawText)
	}

	return structured, stats, nil
}

// ParseStructuredContent unmarshals JSON or fenced JSON decision content.
func ParseStructuredContent(raw string) (*StructuredDecisionResponse, error) {
	clean := strings.TrimSpace(raw)
	if strings.HasPrefix(clean, "```") {
		firstLine := strings.Index(clean, "\n")
		if firstLine != -1 {
			clean = clean[firstLine+1:]
		}
		clean = strings.TrimSuffix(clean, "```")
		clean = strings.TrimSpace(clean)
	}

	var resp StructuredDecisionResponse
	if err := json.Unmarshal([]byte(clean), &resp); err != nil {
		return nil, fmt.Errorf("failed to parse JSON into StructuredDecisionResponse: %w", err)
	}

	return &resp, nil
}
