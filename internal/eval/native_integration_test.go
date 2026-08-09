//go:build integration

package eval

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/ghchinoy/mizan/internal/registry"
)

// TestLivePointwiseText performs a real EvaluateInstances text-pointwise call.
// It is guarded by the `integration` build tag and by PROJECT_ID being set;
// it uses in-container ADC. Run with:
//
//	PROJECT_ID=ghchinoy-genai-sa go test -tags integration ./internal/eval/ -run TestLivePointwiseText -v
func TestLivePointwiseText(t *testing.T) {
	project := os.Getenv("PROJECT_ID")
	if project == "" {
		project = os.Getenv("MIZAN_PROJECT_ID")
	}
	if project == "" {
		t.Skip("PROJECT_ID not set; skipping live EvaluateInstances test")
	}
	location := os.Getenv("LOCATION")
	if location == "" {
		location = "us-central1"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	client, err := NewClient(ctx, location, "")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	eng := NewEngine(client, project, location)

	tmpl := registry.MetricTemplate{
		ID:                   "test/helpfulness",
		Name:                 "Helpfulness",
		Kind:                 registry.KindPointwise,
		Modalities:           []registry.Modality{registry.ModalityText},
		MetricPromptTemplate: "You are a strict rater. Rate the helpfulness of the response on a 1-5 scale.\n\nResponse:\n{{response}}",
		AutoraterModel:       "gemini-2.5-flash",
		SamplingCount:        1,
	}
	res, err := eng.Run(ctx, tmpl, Instance{Fields: map[string]AssetRef{
		"response": {Modality: registry.ModalityText, Text: "To reset your password, open Settings > Security > Reset password and follow the emailed link, which expires in 15 minutes."},
	}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Score == nil {
		t.Fatal("live result had no score")
	}
	if res.Explanation == "" {
		t.Fatal("live result had no explanation")
	}
	t.Logf("LIVE score=%v explanation=%s", *res.Score, res.Explanation)
}
