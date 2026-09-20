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
		if structured, err := ParseStructuredContentWithLogprobs(content, chatResp.Choices[0].Logprobs); err == nil && structured.Diagnostics.Steps > 0 {
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
		return nil, stats, fmt.Errorf("diffusion: parse decision output: %w (raw: %s)", err, rawText)
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
		return &resp, nil
	}

	clean := cleanJSON(raw)
	if err := json.Unmarshal([]byte(clean), &resp); err == nil && len(resp.Answers) > 0 {
		return &resp, nil
	}

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
		return &resp, nil
	}

	return nil, fmt.Errorf("failed to parse JSON into StructuredDecisionResponse: %s", raw)
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
