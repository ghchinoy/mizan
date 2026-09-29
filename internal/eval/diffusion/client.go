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
	"math"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Client is the mockable interface for interacting with DiffusionGemma.
type Client interface {
	Complete(ctx context.Context, req ChatCompletionRequest) (*ChatCompletionResponse, *RequestStats, error)
	Decide(ctx context.Context, schemaContent, userStateContent string, images ...string) (*StructuredDecisionResponse, *RequestStats, error)
}

// HTTPClient communicates with a DiffusionGemma structured-readout server over
// HTTP. BaseURL may be:
//   - a local or Cloud Run structured server (".../v1" -> POST .../v1/chat/completions),
//   - a dgem gateway (same path; Backend selects vertex | cloudrun | vertex_first),
//   - a Vertex AI dedicated endpoint (host *.prediction.vertexai.goog or a path
//     containing /invoke), normalized to .../invoke/v1/chat/completions.
type HTTPClient struct {
	BaseURL   string
	HTTP      *http.Client
	Model     string
	AuthToken string
	// Tokens, when set, supplies a fresh bearer token per request (ADC access or
	// identity token). It takes precedence over AuthToken.
	Tokens TokenSource
	// Backend, when set, is sent as the X-DGem-Backend header so a dgem gateway
	// pins the request to one backend instead of failing over.
	Backend string
	// MaxRetries bounds retries on HTTP 429/503 (exponential backoff with jitter).
	MaxRetries int
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
		BaseURL:    baseURL,
		HTTP:       &http.Client{Timeout: timeout},
		Model:      defaultModel,
		MaxRetries: 3,
	}
}

// WithAuthToken sets the optional Authorization Bearer / IAM token.
func (c *HTTPClient) WithAuthToken(token string) *HTTPClient {
	c.AuthToken = strings.TrimSpace(token)
	return c
}

// WithTokenSource sets a per-request bearer token source.
func (c *HTTPClient) WithTokenSource(ts TokenSource) *HTTPClient {
	c.Tokens = ts
	return c
}

// WithBackend sets the X-DGem-Backend header value (vertex | cloudrun | vertex_first).
func (c *HTTPClient) WithBackend(backend string) *HTTPClient {
	c.Backend = strings.TrimSpace(backend)
	return c
}

// IsVertexEndpointURL reports whether u targets a Vertex AI dedicated endpoint.
func IsVertexEndpointURL(u string) bool {
	l := strings.ToLower(u)
	return strings.Contains(l, ".prediction.vertexai.goog") || strings.Contains(l, "/invoke")
}

// ChatCompletionsURL resolves the chat-completions URL for BaseURL.
func (c *HTTPClient) ChatCompletionsURL() string {
	u := strings.TrimRight(c.BaseURL, "/")
	if IsVertexEndpointURL(u) {
		if strings.HasSuffix(u, "/invoke/v1/chat/completions") {
			return u
		}
		u = strings.TrimSuffix(u, "/chat/completions")
		u = strings.TrimSuffix(u, "/v1")
		if !strings.HasSuffix(u, "/invoke") {
			u += "/invoke"
		}
		return u + "/v1/chat/completions"
	}
	if strings.HasSuffix(u, "/chat/completions") {
		return u
	}
	return u + "/chat/completions"
}

func (c *HTTPClient) bearer(ctx context.Context) (string, error) {
	if c.Tokens != nil {
		tok, err := c.Tokens.Token(ctx)
		if err != nil {
			return "", fmt.Errorf("diffusion: mint auth token: %w", err)
		}
		return tok, nil
	}
	return c.AuthToken, nil
}

// Complete executes an OpenAI-compatible chat completion.
func (c *HTTPClient) Complete(ctx context.Context, req ChatCompletionRequest) (*ChatCompletionResponse, *RequestStats, error) {
	if req.Model == "" {
		req.Model = c.Model
	}

	endpoint := c.ChatCompletionsURL()
	payloadBytes, err := json.Marshal(req)
	if err != nil {
		return nil, nil, fmt.Errorf("diffusion: marshal request: %w", err)
	}

	token, err := c.bearer(ctx)
	if err != nil {
		return nil, nil, err
	}

	var (
		resp      *http.Response
		bodyBytes []byte
		wallTime  time.Duration
		retries   int
	)
	for attempt := 0; ; attempt++ {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payloadBytes))
		if err != nil {
			return nil, nil, fmt.Errorf("diffusion: create http request: %w", err)
		}
		httpReq.Header.Set("Content-Type", "application/json")
		if c.Backend != "" {
			httpReq.Header.Set("X-DGem-Backend", c.Backend)
		}
		if token != "" {
			if !strings.HasPrefix(strings.ToLower(token), "bearer ") {
				token = "Bearer " + token
			}
			httpReq.Header.Set("Authorization", token)
		}

		start := time.Now()
		resp, err = c.HTTP.Do(httpReq)
		if err != nil {
			return nil, nil, fmt.Errorf("diffusion: http request failed to %s: %w", endpoint, err)
		}
		bodyBytes, err = io.ReadAll(resp.Body)
		resp.Body.Close()
		wallTime = time.Since(start)
		if err != nil {
			return nil, nil, fmt.Errorf("diffusion: read response body: %w", err)
		}
		if (resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable) && attempt < c.MaxRetries {
			retries++
			backoff := time.Duration(250*(1<<attempt)) * time.Millisecond
			backoff += time.Duration(rand.Int63n(int64(backoff / 2)))
			select {
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			case <-time.After(backoff):
			}
			continue
		}
		break
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, nil, &HTTPError{StatusCode: resp.StatusCode, Body: truncate(string(bodyBytes), 600), Endpoint: endpoint, Retries: retries}
	}

	var chatResp ChatCompletionResponse
	if err := json.Unmarshal(bodyBytes, &chatResp); err != nil {
		return nil, nil, fmt.Errorf("diffusion: unmarshal response: %w", err)
	}

	stats := &RequestStats{
		Model:        chatResp.Model,
		Endpoint:     endpoint,
		BackendUsed:  resp.Header.Get("X-DGem-Backend-Used"),
		TraceID:      resp.Header.Get("X-DGem-Trace-Id"),
		WallTime:     wallTime,
		Retries:      retries,
		PromptTokens: chatResp.Usage.PromptTokens,
		OutputTokens: chatResp.Usage.CompletionTokens,
		TotalTokens:  chatResp.Usage.TotalTokens,
	}

	if len(chatResp.Choices) > 0 {
		content := chatResp.Choices[0].Message.RawContent()
		if structured, err := ParseStructuredContentWithLogprobs(content, chatResp.Choices[0].Logprobs); err == nil {
			stats.ReadoutMode = structured.ReadoutMode
			if structured.ReadoutMode == ReadoutEnvelope {
				d := structured.Diagnostics
				stats.Diagnostics = &d
				stats.PrefillMs = d.Timing.PrefillMs
				stats.DenoiseMs = d.Timing.DenoiseMs
				stats.ServerMs = d.Timing.TotalMs
				stats.ReusedTokens = d.Timing.ReusedTokens
				stats.DenoiseSteps = d.Timing.StepsRun
				if stats.DenoiseSteps == 0 {
					stats.DenoiseSteps = d.Steps
				}
				stats.SamplesN = d.Samples.N
				if d.Samples.Policy.Extended != nil {
					stats.Extended = *d.Samples.Policy.Extended
				}
			}
		}
	}

	return &chatResp, stats, nil
}

// HTTPError is returned for non-2xx responses so callers can record the status.
type HTTPError struct {
	StatusCode int
	Body       string
	Endpoint   string
	Retries    int
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("diffusion: server returned HTTP %d from %s (after %d retries): %s", e.StatusCode, e.Endpoint, e.Retries, e.Body)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
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
		Logprobs:    true,
		TopLogprobs: 5,
	}

	chatResp, stats, err := c.Complete(ctx, req)
	if err != nil {
		return nil, nil, err
	}

	if len(chatResp.Choices) == 0 {
		return nil, stats, fmt.Errorf("diffusion: no response choices returned by model")
	}

	rawText := chatResp.Choices[0].Message.RawContent()
	structured, err := ParseStructuredContentWithLogprobs(rawText, chatResp.Choices[0].Logprobs)
	if err != nil {
		return nil, stats, fmt.Errorf("diffusion: parse decision output: %w (raw: %s)", err, truncate(rawText, 600))
	}

	return structured, stats, nil
}

// ParseStructuredContent unmarshals JSON or fenced JSON decision content.
func ParseStructuredContent(raw string) (*StructuredDecisionResponse, error) {
	return ParseStructuredContentWithLogprobs(raw, nil)
}

// ParseStructuredContentWithLogprobs unmarshals either native Metal StructuredDecisionResponse
// or vLLM flat JSON key-value decision content, enriching confidence, Shannon entropy,
// and probabilities using token logprobs when present.
func ParseStructuredContentWithLogprobs(raw string, logprobs *ChoiceLogprobs) (*StructuredDecisionResponse, error) {
	var resp StructuredDecisionResponse
	if err := json.Unmarshal([]byte(raw), &resp); err == nil && len(resp.Answers) > 0 {
		return finishEnvelope(&resp), nil
	}

	clean := cleanJSON(raw)
	resp = StructuredDecisionResponse{}
	if err := json.Unmarshal([]byte(clean), &resp); err == nil && len(resp.Answers) > 0 {
		return finishEnvelope(&resp), nil
	}
	resp = StructuredDecisionResponse{}

	var rawMap map[string]interface{}
	if err := json.Unmarshal([]byte(clean), &rawMap); err == nil && len(rawMap) > 0 {
		resp.Answers = make(map[string]QuestionAnswer)
		if resp.Diagnostics.Questions == nil {
			resp.Diagnostics.Questions = make(map[string]QuestionDiagnostic)
		}
		for k, v := range rawMap {
			label := fmt.Sprintf("%v", v)
			conf, lp, ent, probs := extractKeySlotLogprobTelemetry(k, label, logprobs)

			var scoreVal float64
			if f, err := strconv.ParseFloat(strings.TrimSpace(label), 64); err == nil {
				scoreVal = f
			}
			// If top_logprobs contains numeric levels, compute probability-weighted expected score
			var weightedSum, probSum float64
			var numericLevels int
			for tok, p := range probs {
				if lvl, err := strconv.ParseFloat(strings.TrimSpace(tok), 64); err == nil && lvl >= 0 && lvl <= 10 {
					weightedSum += lvl * p
					probSum += p
					numericLevels++
				}
			}
			if numericLevels >= 2 && probSum > 0 {
				scoreVal = weightedSum / probSum
			}

			normLbl := strings.ToLower(strings.TrimSpace(label))
			var noul float64
			if normLbl == "yes" || normLbl == "true" {
				noul = conf
			} else if normLbl == "no" || normLbl == "false" {
				noul = 1.0 - conf
			}

			resp.Answers[k] = QuestionAnswer{
				Type:          "auto",
				Label:         label,
				Choice:        label,
				Score:         scoreVal,
				Noul:          noul,
				Confidence:    conf,
				Logprob:       lp,
				Entropy:       ent,
				Probabilities: probs,
			}
			resp.Diagnostics.Questions[k] = QuestionDiagnostic{
				Argmax:       label,
				Entropy:      ent,
				LabelMass:    conf,
				PrimaryToken: label,
			}
		}
		resp.ReadoutMode = ReadoutFallback
		return &resp, nil
	}

	return nil, fmt.Errorf("failed to parse JSON into StructuredDecisionResponse: %s", raw)
}

// finishEnvelope marks an envelope response and fills per-answer entropy from
// the restricted-softmax probabilities (or the per-question diagnostics) when
// the server did not report it on the answer itself.
func finishEnvelope(resp *StructuredDecisionResponse) *StructuredDecisionResponse {
	resp.ReadoutMode = ReadoutEnvelope
	for id, a := range resp.Answers {
		if a.Entropy == 0 {
			if len(a.Probabilities) > 0 {
				a.Entropy = ShannonEntropy(a.Probabilities)
			} else if d, ok := resp.Diagnostics.Questions[id]; ok {
				a.Entropy = d.Entropy
			}
			resp.Answers[id] = a
		}
	}
	return resp
}

// ShannonEntropy returns H = -sum p ln p (nats) over a probability map.
func ShannonEntropy(probs map[string]float64) float64 {
	var h float64
	for _, p := range probs {
		if p > 0 {
			h -= p * math.Log(p)
		}
	}
	return h
}

func extractKeySlotLogprobTelemetry(key, label string, lp *ChoiceLogprobs) (confidence float64, logprob float64, entropy float64, probs map[string]float64) {
	confidence = 1.0
	if lp == nil || len(lp.Content) == 0 {
		return confidence, 0, 0, nil
	}

	normLabel := strings.ToLower(strings.TrimSpace(label))
	if normLabel == "" {
		return 1.0, 0, 0, nil
	}

	normKey := strings.ToLower(strings.TrimSpace(key))
	colonIdx := -1
	if normKey != "" {
		keyIdx := -1
		for i := 0; i < len(lp.Content); i++ {
			tokClean := strings.ToLower(strings.Trim(lp.Content[i].Token, " \t\n\r\"',:{}[]"))
			if tokClean != "" && (strings.Contains(normKey, tokClean) || strings.Contains(tokClean, normKey)) {
				keyIdx = i
			}
			if keyIdx != -1 && i >= keyIdx && strings.Contains(lp.Content[i].Token, ":") {
				colonIdx = i
				break
			}
		}
	}

	if colonIdx == -1 {
		for i := len(lp.Content) - 1; i >= 0; i-- {
			if strings.Contains(lp.Content[i].Token, ":") {
				colonIdx = i
				break
			}
		}
	}

	startSearch := 0
	if colonIdx != -1 && colonIdx+1 < len(lp.Content) {
		startSearch = colonIdx + 1
	}

	var matchedTokens []*TokenLogprob
	for i := startSearch; i < len(lp.Content); i++ {
		if i > startSearch && strings.Contains(lp.Content[i].Token, ",") {
			break
		}
		tokClean := strings.ToLower(strings.Trim(lp.Content[i].Token, " \t\n\r\"',:{}[]"))
		if tokClean == "" {
			continue
		}
		if strings.Contains(normLabel, tokClean) || strings.HasPrefix(tokClean, normLabel) {
			matchedTokens = append(matchedTokens, &lp.Content[i])
		}
	}

	if len(matchedTokens) == 0 {
		for i := len(lp.Content) - 1; i >= 0; i-- {
			tokClean := strings.Trim(lp.Content[i].Token, " \t\n\r\"',:{}[]")
			if len(tokClean) > 0 {
				matchedTokens = append(matchedTokens, &lp.Content[i])
				break
			}
		}
	}
	if len(matchedTokens) == 0 {
		return 1.0, 0, 0, nil
	}

	minLogprob := 0.0
	maxEntropy := 0.0
	probs = make(map[string]float64)

	for idx, mt := range matchedTokens {
		if idx == 0 || mt.Logprob < minLogprob {
			minLogprob = mt.Logprob
		}
		var h float64
		for _, item := range mt.TopLogprobs {
			p := math.Exp(item.Logprob)
			tKey := strings.Trim(item.Token, " \t\n\r\"',:{}[]")
			if tKey != "" {
				if existing, ok := probs[tKey]; !ok || p > existing {
					probs[tKey] = p
				}
			}
			if p > 0 {
				h -= p * math.Log(p)
			}
		}
		if h > maxEntropy {
			maxEntropy = h
		}
	}

	logprob = minLogprob
	confidence = math.Exp(minLogprob)
	if confidence > 1.0 {
		confidence = 1.0
	}
	entropy = maxEntropy

	return confidence, logprob, entropy, probs
}

func cleanJSON(content string) string {
	content = strings.TrimSpace(content)
	if idx := strings.Index(content, "```json"); idx != -1 {
		content = content[idx+7:]
		if end := strings.Index(content, "```"); end != -1 {
			content = content[:end]
		}
	} else if idx := strings.Index(content, "```"); idx != -1 {
		content = content[idx+3:]
		if end := strings.Index(content, "```"); end != -1 {
			content = content[:end]
		}
	} else if idx := strings.Index(content, "{"); idx != -1 {
		if end := strings.LastIndex(content, "}"); end != -1 && end > idx {
			content = content[idx : end+1]
		}
	}
	return strings.TrimSpace(content)
}
