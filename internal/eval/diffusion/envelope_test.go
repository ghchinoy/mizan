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
	"math"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// structuredServerEnvelope is a verbatim answer set from dgem's
// structured_server.py (vLLM engine, 2026-09-25). Per-question entropy is an
// array (one value per read), which the original parser could not decode: it
// silently fell through to the flat-map fallback and lost every answer.
const structuredServerEnvelope = `{
  "answers": {
    "verdict": {"type": "noul", "label": "yes", "confidence": 0.9996037001676179,
      "probabilities": {"yes": 0.9996037001676179, "no": 0.00039629983238221624}, "noul": 0.9996037001676179},
    "q": {"type": "score", "label": "1", "confidence": 0.9931672533150858,
      "probabilities": {"1": 0.9931672533150858, "2": 0.0005492853017606589, "3": 0.0009272359554068329, "4": 0.002085031859304985, "5": 0.0032711935684415962},
      "score": 1.0217436270642555, "level": "1"}
  },
  "diagnostics": {
    "steps": 1, "stages": [["verdict", "q"]], "skipped": {}, "thought": null,
    "samples": {"n": 1, "tops": [{"verdict": ["yes", 0.9996, 0.032]}],
      "policy": {"mode": "fixed", "n": 1, "extended": null, "first_read_entropy": null}},
    "timing": {"total_ms": 64.28217887878418, "reads": 1},
    "prompt_tokens": 126,
    "questions": {
      "verdict": {"pos": 7, "entropy": [0.03213968097167205], "label_mass": 0.9733480892770467, "argmax_is_label": true},
      "q": {"pos": 12, "entropy": [0.06716041517926875], "label_mass": 0.9808653211408755, "argmax_is_label": true}
    },
    "engine": "vllm"
  }
}`

func TestParseStructuredServerEnvelope(t *testing.T) {
	resp, err := ParseStructuredContent(structuredServerEnvelope)
	if err != nil {
		t.Fatal(err)
	}
	if resp.ReadoutMode != ReadoutEnvelope {
		t.Fatalf("ReadoutMode = %q, want envelope", resp.ReadoutMode)
	}
	v, ok := resp.Answers["verdict"]
	if !ok || v.Label != "yes" || v.Noul < 0.99 {
		t.Fatalf("verdict = %+v", v)
	}
	want := ShannonEntropy(v.Probabilities)
	if math.Abs(v.Entropy-want) > 1e-12 || v.Entropy <= 0 {
		t.Errorf("verdict entropy = %v, want %v from probabilities", v.Entropy, want)
	}
	if d := resp.Diagnostics.Questions["q"]; math.Abs(d.Entropy-0.06716041517926875) > 1e-12 {
		t.Errorf("diagnostic entropy = %v", d.Entropy)
	}
	if resp.Diagnostics.Timing.TotalMs < 64 || resp.Diagnostics.Samples.N != 1 {
		t.Errorf("diagnostics = %+v", resp.Diagnostics)
	}
}

func TestParseMultiStageSamples(t *testing.T) {
	raw := `{"answers":{"a":{"label":"yes","confidence":0.9}},"diagnostics":{"steps":1,
	  "samples":{"n":[2,2],"policy":[{"mode":"fixed","n":2},{"mode":"fixed","n":2}]}}}`
	resp, err := ParseStructuredContent(raw)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Diagnostics.Samples.N != 4 || resp.Diagnostics.Samples.Policy.Mode != "fixed" {
		t.Errorf("samples = %+v", resp.Diagnostics.Samples)
	}
}

func TestParseFreeFormIsFallback(t *testing.T) {
	resp, err := ParseStructuredContent(`{"selection": "technical"}`)
	if err != nil {
		t.Fatal(err)
	}
	if resp.ReadoutMode != ReadoutFallback {
		t.Errorf("ReadoutMode = %q, want fallback", resp.ReadoutMode)
	}
}

func TestDecideGatewayHeadersRetriesAndStats(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("X-DGem-Backend"); got != "cloudrun" {
			t.Errorf("X-DGem-Backend = %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tok-1" {
			t.Errorf("Authorization = %q", got)
		}
		if atomic.AddInt32(&calls, 1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("X-DGem-Backend-Used", "cloudrun")
		w.Header().Set("X-DGem-Trace-Id", "abc123")
		content, _ := json.Marshal(structuredServerEnvelope)
		_, _ = w.Write([]byte(`{"model":"dgemma-structured","choices":[{"message":{"role":"assistant","content":` + string(content) + `}}]}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL+"/v1", "m", 5*time.Second).WithBackend("cloudrun").WithTokenSource(StaticToken("tok-1"))
	resp, stats, err := c.Decide(context.Background(), `{"questions":[]}`, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if resp.ReadoutMode != ReadoutEnvelope || stats.ReadoutMode != ReadoutEnvelope {
		t.Errorf("readout = %q / %q", resp.ReadoutMode, stats.ReadoutMode)
	}
	if stats.BackendUsed != "cloudrun" || stats.TraceID != "abc123" || stats.Retries != 1 {
		t.Errorf("stats = %+v", stats)
	}
	if stats.ServerMs < 64 || stats.SamplesN != 1 || stats.DenoiseSteps != 1 {
		t.Errorf("server telemetry = %+v", stats)
	}
}

func TestChatCompletionsURL(t *testing.T) {
	cases := map[string]string{
		"http://127.0.0.1:8080/v1":               "http://127.0.0.1:8080/v1/chat/completions",
		"https://gw.example/v1/":                 "https://gw.example/v1/chat/completions",
		"https://gw.example/v1/chat/completions": "https://gw.example/v1/chat/completions",
		"https://123.us-central1-9.prediction.vertexai.goog/v1/projects/p/locations/us-central1/endpoints/123":           "https://123.us-central1-9.prediction.vertexai.goog/v1/projects/p/locations/us-central1/endpoints/123/invoke/v1/chat/completions",
		"https://123.us-central1-9.prediction.vertexai.goog/v1/projects/p/locations/us-central1/endpoints/123/invoke/v1": "https://123.us-central1-9.prediction.vertexai.goog/v1/projects/p/locations/us-central1/endpoints/123/invoke/v1/chat/completions",
	}
	for in, want := range cases {
		if got := (&HTTPClient{BaseURL: in}).ChatCompletionsURL(); got != want {
			t.Errorf("%s -> %s, want %s", in, got, want)
		}
	}
}

func TestResolveAuthAuto(t *testing.T) {
	for in, want := range map[string]string{
		"http://127.0.0.1:8080/v1":                            "",
		"https://svc-abc-uc.a.run.app/v1":                     AuthIDToken,
		"https://1.us-central1-2.prediction.vertexai.goog/v1": AuthAccessToken,
	} {
		ts, err := ResolveAuth(AuthAuto, in, "")
		if err != nil {
			t.Fatal(err)
		}
		got := ""
		if a, ok := ts.(*adcTokenSource); ok {
			got = a.kind
		}
		if got != want {
			t.Errorf("%s -> %q, want %q", in, got, want)
		}
	}
}
