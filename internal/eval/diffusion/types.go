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

// Package diffusion implements the client transport and payload translation
// for Google DeepMind's DiffusionGemma models using discrete block diffusion
// slot readout.
package diffusion

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ChatCompletionRequest is the OpenAI-compatible payload format sent to the
// DiffusionGemma inference server.
type ChatCompletionRequest struct {
	Model       string        `json:"model,omitempty"`
	Messages    []ChatMessage `json:"messages"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Stream      bool          `json:"stream,omitempty"`
	Logprobs    bool          `json:"logprobs,omitempty"`
	TopLogprobs int           `json:"top_logprobs,omitempty"`
}

// ChatMessage represents a single chat turn.
type ChatMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"`
}

// RawContent returns the content as a string if text, or marshals to JSON string.
func (m ChatMessage) RawContent() string {
	switch v := m.Content.(type) {
	case string:
		return v
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}

// ContentPart represents an element in an OpenAI-compatible multimodal content array.
type ContentPart struct {
	Type     string        `json:"type"`
	Text     string        `json:"text,omitempty"`
	ImageURL *ImageURLPart `json:"image_url,omitempty"`
}

// ImageURLPart holds an image URL or base64 data URI.
type ImageURLPart struct {
	URL string `json:"url"`
}

// ChatCompletionResponse is the standard OpenAI-compatible response.
type ChatCompletionResponse struct {
	ID      string       `json:"id"`
	Object  string       `json:"object"`
	Created int64        `json:"created"`
	Model   string       `json:"model"`
	Choices []ChatChoice `json:"choices"`
	Usage   Usage        `json:"usage"`
}

// ChatChoice contains the assistant message and optional token logprobs.
type ChatChoice struct {
	Index        int             `json:"index"`
	Message      ChatMessage     `json:"message"`
	FinishReason string          `json:"finish_reason"`
	Logprobs     *ChoiceLogprobs `json:"logprobs,omitempty"`
}

// ChoiceLogprobs holds OpenAI/vLLM token-level log-probabilities.
type ChoiceLogprobs struct {
	Content []TokenLogprob `json:"content"`
}

// TokenLogprob represents the log-probability and top-k alternatives for a single output token.
type TokenLogprob struct {
	Token       string           `json:"token"`
	Logprob     float64          `json:"logprob"`
	TopLogprobs []TopLogprobItem `json:"top_logprobs,omitempty"`
}

// TopLogprobItem represents a single candidate token and its logprob at a given position.
type TopLogprobItem struct {
	Token   string  `json:"token"`
	Logprob float64 `json:"logprob"`
}

// Usage reports token statistics.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// DecisionSchemaPayload is the JSON structure sent as the system message to DiffusionGemma.
type DecisionSchemaPayload struct {
	Instructions string           `json:"instructions,omitempty"`
	Questions    []QuestionSchema `json:"questions"`
}

// QuestionSchema defines one slot question for DiffusionGemma.
type QuestionSchema struct {
	ID           string         `json:"id"`
	Type         string         `json:"type"` // "boolean", "choice", "score"
	Instructions string         `json:"instructions"`
	Options      []ChoiceOption `json:"options,omitempty"`
	Levels       []string       `json:"levels,omitempty"`
}

// ChoiceOption defines an option in a choice question.
type ChoiceOption struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// StructuredDecisionResponse represents the parsed discrete diffusion decision content.
type StructuredDecisionResponse struct {
	Answers     map[string]QuestionAnswer `json:"answers"`
	Diagnostics Diagnostics               `json:"diagnostics"`
}

// QuestionAnswer holds the evaluated result for a single question.
type QuestionAnswer struct {
	Type          string             `json:"type"`
	Label         string             `json:"label"`
	Confidence    float64            `json:"confidence"`
	Logprob       float64            `json:"logprob,omitempty"`
	Entropy       float64            `json:"entropy,omitempty"`
	Stderr        float64            `json:"stderr"`
	Agreement     float64            `json:"agreement"`
	Probabilities map[string]float64 `json:"probabilities"`

	// Type-specific values:
	Choice string  `json:"choice,omitempty"` // For choice type
	Score  float64 `json:"score,omitempty"`  // For score type
	Level  string  `json:"level,omitempty"`  // For score level
	Noul   float64 `json:"noul,omitempty"`   // Probability of true/yes
}

// Diagnostics contains server-side timing, sampling, and slot entropy data.
type Diagnostics struct {
	Hole      string                        `json:"hole"`
	Steps     int                           `json:"steps"`
	Timing    TimingStats                   `json:"timing"`
	Samples   SampleStats                   `json:"samples"`
	Questions map[string]QuestionDiagnostic `json:"questions"`
}

// TimingStats details execution breakdown on Metal / GPU.
type TimingStats struct {
	DenoiseMs    float64 `json:"denoise_ms"`
	PrefillMs    float64 `json:"prefill_ms"`
	PromptTokens int     `json:"prompt_tokens"`
	ReusedTokens int     `json:"reused_tokens"`
	Rounds       int     `json:"rounds"`
	Samples      int     `json:"samples"`
	StepsRun     int     `json:"steps_run"`
}

// SampleStats details the multi-read sampling policy.
type SampleStats struct {
	N      int          `json:"n"`
	Policy SamplePolicy `json:"policy"`
}

// SamplePolicy reveals whether the auto-sampling threshold was triggered.
type SamplePolicy struct {
	AutoThreshold float64 `json:"auto_threshold"`
	Extended      bool    `json:"extended"`
	MaxSamples    int     `json:"max_samples"`
	Mode          string  `json:"mode"`
}

// QuestionDiagnostic holds per-question entropy metrics.
type QuestionDiagnostic struct {
	Argmax       string  `json:"argmax"`
	Entropy      float64 `json:"entropy"`
	LabelMass    float64 `json:"label_mass"`
	PrimaryToken string  `json:"primary_token"`
}

// RequestStats records measured client and server telemetry.
type RequestStats struct {
	Model        string
	Endpoint     string
	WallTime     time.Duration
	PrefillMs    float64
	DenoiseMs    float64
	PromptTokens int
	OutputTokens int
	TotalTokens  int
	ReusedTokens int
	DenoiseSteps int
	SamplesN     int
	Extended     bool
	Diagnostics  *Diagnostics
}

// BuildMultimodalContent converts text and image inputs into a valid OpenAI vision payload.
func BuildMultimodalContent(textContent string, imageInputs []string) (interface{}, error) {
	if len(imageInputs) == 0 {
		return textContent, nil
	}

	var parts []ContentPart

	for _, img := range imageInputs {
		img = strings.TrimSpace(img)
		if img == "" {
			continue
		}

		var imageURI string
		if strings.HasPrefix(img, "http://") || strings.HasPrefix(img, "https://") || strings.HasPrefix(img, "data:") {
			imageURI = img
		} else {
			fileData, err := os.ReadFile(img)
			if err != nil {
				return nil, fmt.Errorf("diffusion: read image file %q: %w", img, err)
			}
			mimeType := detectImageMime(img, fileData)
			encoded := base64.StdEncoding.EncodeToString(fileData)
			imageURI = fmt.Sprintf("data:%s;base64,%s", mimeType, encoded)
		}

		parts = append(parts, ContentPart{
			Type:     "image_url",
			ImageURL: &ImageURLPart{URL: imageURI},
		})
	}

	if textContent != "" {
		parts = append(parts, ContentPart{
			Type: "text",
			Text: textContent,
		})
	}

	return parts, nil
}

func detectImageMime(filename string, data []byte) string {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	}

	if len(data) >= 512 {
		return http.DetectContentType(data[:512])
	}
	if len(data) > 0 {
		return http.DetectContentType(data)
	}
	return "application/octet-stream"
}
