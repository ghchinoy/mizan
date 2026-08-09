//go:build integration

package eval

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/ghchinoy/mizan/internal/registry"
)

func liveProject(t *testing.T) string {
	t.Helper()
	project := os.Getenv("PROJECT_ID")
	if project == "" {
		project = os.Getenv("MIZAN_PROJECT_ID")
	}
	if project == "" {
		t.Skip("PROJECT_ID not set; skipping live test")
	}
	return project
}

// TestLiveRubric performs a real native rubric EvaluateInstances call. Run with:
//
//	PROJECT_ID=ghchinoy-genai-sa go test -tags integration ./internal/eval/ -run TestLiveRubric -v
func TestLiveRubric(t *testing.T) {
	project := liveProject(t)
	location := os.Getenv("LOCATION")
	if location == "" {
		location = "us-central1"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	client, err := NewClient(ctx, location, "")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	eng := NewEngine(client, project, location)

	tmpl := registry.MetricTemplate{
		ID:                   "test/ad-quality-rubric",
		Name:                 "Ad Quality (rubric)",
		Kind:                 registry.KindRubric,
		Modalities:           []registry.Modality{registry.ModalityText},
		MetricPromptTemplate: "You are a strict marketing reviewer. Rate the following ad copy on a 1-5 scale.\n\nAd copy:\n{{copy}}",
		AutoraterModel:       "gemini-2.5-flash",
		SamplingCount:        1,
		RubricGroups: map[string][]string{
			"clarity": {"The core offer is unambiguous", "Free of jargon and filler"},
			"tone":    {"Matches a professional, trustworthy brand voice", "No unsupported superlatives"},
		},
	}
	res, err := eng.Run(ctx, tmpl, Instance{Fields: map[string]AssetRef{
		"copy": {Modality: registry.ModalityText, Text: "Reset your password in seconds: open Settings, tap Security, and follow the secure link we email you."},
	}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Score == nil {
		t.Fatal("live rubric result had no score")
	}
	t.Logf("LIVE RUBRIC score=%v explanation=%s", *res.Score, res.Explanation)
}

// TestLiveCustomSchema performs a real genai custom_schema call at
// location=global. Run with:
//
//	PROJECT_ID=ghchinoy-genai-sa go test -tags integration ./internal/eval/ -run TestLiveCustomSchema -v
func TestLiveCustomSchema(t *testing.T) {
	project := liveProject(t)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	genaiClient, err := NewGenaiClient(ctx, project, "global")
	if err != nil {
		t.Fatalf("NewGenaiClient: %v", err)
	}

	// The genai path does not use the native EvaluationClient; a nil is fine for
	// this custom_schema-only run.
	eng := NewEngine(nil, project, "global", WithGenaiClient(genaiClient))

	tmpl := registry.MetricTemplate{
		ID:                   "test/brand-check-custom",
		Name:                 "Brand Check (custom_schema)",
		Kind:                 registry.KindCustomSchema,
		MetricPromptTemplate: "Audit this ad copy against a professional brand voice and score it. Copy:\n{{copy}}",
		SystemInstruction:    "You are a strict, consistent brand auditor. Respond only with the requested JSON.",
		AutoraterModel:       "gemini-2.5-flash",
		ResponseSchema: &registry.Schema{JSON: `{
			"type": "object",
			"properties": {
				"overall_score": {"type": "number"},
				"brand_tone_score": {"type": "number"},
				"compliant": {"type": "boolean"},
				"flagged_issues": {"type": "array", "items": {"type": "string"}},
				"explanation": {"type": "string"}
			},
			"required": ["overall_score", "brand_tone_score", "compliant", "flagged_issues", "explanation"],
			"propertyOrdering": ["overall_score", "brand_tone_score", "compliant", "flagged_issues", "explanation"]
		}`},
	}
	res, err := eng.Run(ctx, tmpl, Instance{Fields: map[string]AssetRef{
		"copy": {Modality: registry.ModalityText, Text: "BUY NOW!!! GUARANTEED best deal EVER, you will NEVER find anything better, act fast!!!"},
	}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.CustomOutput) == 0 {
		t.Fatal("live custom_schema result had no parsed output")
	}
	for _, key := range []string{"overall_score", "brand_tone_score", "compliant", "flagged_issues", "explanation"} {
		if _, ok := res.CustomOutput[key]; !ok {
			t.Errorf("custom output missing required key %q", key)
		}
	}
	t.Logf("LIVE CUSTOM_SCHEMA raw=%s", res.RawOutput[0])
	t.Logf("LIVE CUSTOM_SCHEMA parsed=%#v", res.CustomOutput)
}
