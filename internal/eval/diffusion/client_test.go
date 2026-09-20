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
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDecideBoul(t *testing.T) {
	mockResponse := ChatCompletionResponse{
		ID:    "chatcmpl-123",
		Model: "diffgemma-26b-a4b-it-q4",
		Choices: []ChatChoice{
			{
				Index: 0,
				Message: ChatMessage{
					Role: "assistant",
					Content: `{
						"answers": {
							"verdict": {
								"type": "boolean",
								"label": "yes",
								"confidence": 0.985,
								"stderr": 0.0042,
								"agreement": 1.0
							}
						},
						"diagnostics": {
							"hole": "slot",
							"steps": 1,
							"timing": {
								"denoise_ms": 855.2,
								"prefill_ms": 310.5,
								"prompt_tokens": 120,
								"reused_tokens": 90,
								"steps_run": 1
							},
							"samples": {
								"n": 1,
								"policy": {"mode": "auto"}
							}
						}
					}`,
				},
				FinishReason: "stop",
			},
		},
		Usage: Usage{
			PromptTokens:     120,
			CompletionTokens: 10,
			TotalTokens:      130,
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("missing or incorrect authorization header: %s", r.Header.Get("Authorization"))
		}
		_ = json.NewEncoder(w).Encode(mockResponse)
	}))
	defer server.Close()

	c := NewClient(server.URL+"/v1", "diffgemma-26b-a4b-it-q4", 5*time.Second).WithAuthToken("test-token")

	schema := `{"questions": [{"id": "verdict", "type": "boolean"}]}`
	state := "The input text is safe."

	resp, stats, err := c.Decide(context.Background(), schema, state)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}

	ans, ok := resp.Answers["verdict"]
	if !ok {
		t.Fatalf("missing 'verdict' in answers")
	}
	if ans.Label != "yes" {
		t.Errorf("Label = %q, want 'yes'", ans.Label)
	}
	if ans.Confidence != 0.985 {
		t.Errorf("Confidence = %v, want 0.985", ans.Confidence)
	}
	if ans.Stderr != 0.0042 {
		t.Errorf("Stderr = %v, want 0.0042", ans.Stderr)
	}

	if stats.DenoiseMs != 855.2 {
		t.Errorf("DenoiseMs = %v, want 855.2", stats.DenoiseMs)
	}
	if stats.PrefillMs != 310.5 {
		t.Errorf("PrefillMs = %v, want 310.5", stats.PrefillMs)
	}
}

func TestDecideChoice(t *testing.T) {
	mockResponse := ChatCompletionResponse{
		ID:    "chatcmpl-456",
		Model: "diffgemma-26b-a4b-it-q4",
		Choices: []ChatChoice{
			{
				Index: 0,
				Message: ChatMessage{
					Role: "assistant",
					Content: `{
						"answers": {
							"category": {
								"type": "choice",
								"choice": "billing",
								"label": "billing",
								"confidence": 0.991,
								"probabilities": {"billing": 0.991, "technical": 0.009}
							}
						}
					}`,
				},
			},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(mockResponse)
	}))
	defer server.Close()

	c := NewClient(server.URL+"/v1", "diffgemma-26b-a4b-it-q4", 5*time.Second)

	resp, _, err := c.Decide(context.Background(), "{}", "state")
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}

	ans := resp.Answers["category"]
	if ans.Choice != "billing" {
		t.Errorf("Choice = %q, want 'billing'", ans.Choice)
	}
	if ans.Probabilities["billing"] != 0.991 {
		t.Errorf("Probabilities[billing] = %v, want 0.991", ans.Probabilities["billing"])
	}
}

func TestDecideMultimodalImage(t *testing.T) {
	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "test.png")
	// Minimal valid PNG header
	_ = os.WriteFile(imgPath, []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15c4"), 0600)

	var receivedReq ChatCompletionRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&receivedReq)
		mockResp := ChatCompletionResponse{
			Choices: []ChatChoice{
				{
					Message: ChatMessage{
						Role:    "assistant",
						Content: `{"answers": {"safe": {"type": "boolean", "label": "yes"}}}`,
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(mockResp)
	}))
	defer server.Close()

	c := NewClient(server.URL+"/v1", "diffgemma-26b-a4b-it-q4", 5*time.Second)
	_, _, err := c.Decide(context.Background(), "{}", "check image", imgPath)
	if err != nil {
		t.Fatalf("Decide with image: %v", err)
	}

	// Verify user message contained multimodal content parts
	userMsg := receivedReq.Messages[1]
	parts, ok := userMsg.Content.([]any)
	if !ok {
		t.Fatalf("expected []any content parts, got %T", userMsg.Content)
	}
	if len(parts) != 2 {
		t.Fatalf("expected 2 parts (image + text), got %d", len(parts))
	}
}

func TestDecideVLLMWithLogprobs(t *testing.T) {
	mockResponse := ChatCompletionResponse{
		ID:    "chatcmpl-vllm-789",
		Model: "nvidia/diffusiongemma-26B-A4B-it-NVFP4",
		Choices: []ChatChoice{
			{
				Index: 0,
				Message: ChatMessage{
					Role:    "assistant",
					Content: `{"selection": "billing"}`,
				},
				Logprobs: &ChoiceLogprobs{
					Content: []TokenLogprob{
						{Token: "{", Logprob: -0.0001},
						{Token: "\"selection\"", Logprob: -0.0001},
						{Token: ":", Logprob: -0.0001},
						{
							Token:   "\"billing\"",
							Logprob: -0.051293, // exp(-0.051293) ≈ 0.95
							TopLogprobs: []TopLogprobItem{
								{Token: "\"billing\"", Logprob: -0.051293},
								{Token: "\"account\"", Logprob: -2.995732},
							},
						},
						{Token: "}", Logprob: -0.0001},
					},
				},
				FinishReason: "stop",
			},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(mockResponse)
	}))
	defer server.Close()

	c := NewClient(server.URL+"/v1", "nvidia/diffusiongemma-26B-A4B-it-NVFP4", 5*time.Second)
	resp, _, err := c.Decide(context.Background(), "{}", "state")
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}

	ans, ok := resp.Answers["selection"]
	if !ok {
		t.Fatalf("missing 'selection' in answers")
	}
	if ans.Choice != "billing" {
		t.Errorf("Choice = %q, want 'billing'", ans.Choice)
	}
	if ans.Confidence < 0.94 || ans.Confidence > 0.96 {
		t.Errorf("Confidence = %v, want ~0.95", ans.Confidence)
	}
	if ans.Entropy <= 0 {
		t.Errorf("Entropy = %v, want > 0", ans.Entropy)
	}
}
