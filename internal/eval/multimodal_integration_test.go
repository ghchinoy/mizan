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

//go:build integration

package eval

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ghchinoy/mizan/internal/asset"
	"github.com/ghchinoy/mizan/internal/registry"
)

// liveStagingBucket is the provisioned Mizan staging bucket (bare name). Override
// with MIZAN_STAGING_BUCKET (a leading gs:// is stripped). liveProject is defined
// in rubric_custom_integration_test.go (shared across this package's live tests).
const liveStagingBucket = "ghchinoy-genai-sa-mizan-staging"

func liveLocation() string {
	if l := os.Getenv("LOCATION"); l != "" {
		return l
	}
	return "us-central1"
}

func liveBucketName() string {
	b := strings.TrimPrefix(os.Getenv("MIZAN_STAGING_BUCKET"), "gs://")
	if b == "" {
		b = liveStagingBucket
	}
	return b
}

// writeTestPNG writes a genuine, model-interpretable PNG (a solid red square) to
// a temp file and returns its path.
func writeTestPNG(t *testing.T) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	red := color.RGBA{R: 220, G: 20, B: 20, A: 255}
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			img.Set(x, y, red)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	path := filepath.Join(t.TempDir(), "mizan-live-red.png")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write png: %v", err)
	}
	return path
}

// TestLiveMultimodalPointwiseImage stages a local PNG to GCS and runs a real
// native multimodal pointwise EvaluateInstances call (ContentMap + gs:// FileData
// — the only shape native accepts). It exercises the full WI-P1-4 path:
// FilePath -> GCSStager.Stage -> native ContentMap -> EvaluateInstances.
//
//	PROJECT_ID=ghchinoy-genai-sa go test -tags integration ./internal/eval/ -run TestLiveMultimodalPointwiseImage -v
func TestLiveMultimodalPointwiseImage(t *testing.T) {
	project := liveProject(t)
	location := liveLocation()

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	stager, err := asset.NewGCSStager(ctx, liveBucketName())
	if err != nil {
		t.Fatalf("NewGCSStager: %v", err)
	}
	defer stager.Close()

	client, err := NewClient(ctx, location, "")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	eng := NewEngine(client, project, location, WithStager(stager))

	tmpl := registry.MetricTemplate{
		ID:                   "test/image-quality",
		Name:                 "Image Description Quality",
		Kind:                 registry.KindPointwise,
		Modalities:           []registry.Modality{registry.ModalityImage},
		MetricPromptTemplate: "Rate on a 1-5 scale how well the image is a solid, saturated single color. Consider the image below.\n\nImage:\n{{image}}",
		AutoraterModel:       "gemini-2.5-flash",
		SamplingCount:        1,
	}
	res, err := eng.Run(ctx, tmpl, Instance{Fields: map[string]AssetRef{
		"image": {Modality: registry.ModalityImage, FilePath: writeTestPNG(t)},
	}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Score == nil {
		t.Fatal("live multimodal result had no score")
	}
	t.Logf("LIVE multimodal image pointwise score=%v explanation=%s", *res.Score, res.Explanation)
}

// TestLiveMultimodalPointwiseImageGCS runs the same eval against a pre-staged
// gs:// URI (no local staging), proving the gs://-passthrough branch works live.
func TestLiveMultimodalPointwiseImageGCS(t *testing.T) {
	project := liveProject(t)
	location := liveLocation()

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	stager, err := asset.NewGCSStager(ctx, liveBucketName())
	if err != nil {
		t.Fatalf("NewGCSStager: %v", err)
	}
	defer stager.Close()

	// Stage once to get a real gs:// URI, then eval referencing that URI directly.
	staged, err := stager.Stage(ctx, asset.StageInput{LocalPath: writeTestPNG(t)})
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	t.Logf("staged to %s (%s)", staged.GCSUri, staged.MIME)

	client, err := NewClient(ctx, location, "")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	eng := NewEngine(client, project, location, WithStager(stager))
	tmpl := registry.MetricTemplate{
		ID:                   "test/image-quality",
		Kind:                 registry.KindPointwise,
		Modalities:           []registry.Modality{registry.ModalityImage},
		MetricPromptTemplate: "Rate 1-5 how saturated the color in this image is.\n\nImage:\n{{image}}",
		AutoraterModel:       "gemini-2.5-flash",
		SamplingCount:        1,
	}
	res, err := eng.Run(ctx, tmpl, Instance{Fields: map[string]AssetRef{
		"image": {Modality: registry.ModalityImage, GCSUri: staged.GCSUri, MimeType: staged.MIME},
	}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Score == nil {
		t.Fatal("live gs:// multimodal result had no score")
	}
	t.Logf("LIVE gs:// image pointwise score=%v explanation=%s", *res.Score, res.Explanation)
}

// TestLivePairwiseText runs a real native pairwise EvaluateInstances call and
// asserts the PairwiseChoice maps to one of BASELINE/CANDIDATE/TIE.
//
//	PROJECT_ID=ghchinoy-genai-sa go test -tags integration ./internal/eval/ -run TestLivePairwiseText -v
func TestLivePairwiseText(t *testing.T) {
	project := liveProject(t)
	location := liveLocation()

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	client, err := NewClient(ctx, location, "")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	eng := NewEngine(client, project, location)

	tmpl := registry.MetricTemplate{
		ID:                   "test/pairwise-helpfulness",
		Name:                 "Pairwise Helpfulness",
		Kind:                 registry.KindPairwise,
		Modalities:           []registry.Modality{registry.ModalityText},
		MetricPromptTemplate: "Given the user question:\n{{prompt}}\n\n## Baseline response\n{{baseline}}\n\n## Candidate response\n{{candidate}}\n\nDecide which response is more helpful.",
		BaselineFieldName:    "baseline",
		CandidateFieldName:   "candidate",
		AutoraterModel:       "gemini-2.5-flash",
		// SamplingCount and FlipEnabled left unset to exercise the >=4 / flip-on defaults.
	}
	res, err := eng.Run(ctx, tmpl, Instance{Fields: map[string]AssetRef{
		"prompt":    {Modality: registry.ModalityText, Text: "How do I reset my password?"},
		"baseline":  {Modality: registry.ModalityText, Text: "Try turning it off and on again."},
		"candidate": {Modality: registry.ModalityText, Text: "Open Settings > Security > Reset password and follow the emailed link, which expires in 15 minutes."},
	}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	switch res.PairwiseChoice {
	case "BASELINE", "CANDIDATE", "TIE":
		// ok
	default:
		t.Fatalf("unexpected pairwise choice %q", res.PairwiseChoice)
	}
	t.Logf("LIVE pairwise choice=%s explanation=%s", res.PairwiseChoice, res.Explanation)
}
