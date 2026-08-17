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

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ghchinoy/mizan/internal/eval"
)

// These CLI-level integration tests drive the real cobra commands end-to-end:
// `registry create` (with the new rubric / custom_schema authoring flags) into a
// throwaway SQLite DB, then `eval run` against live Vertex AI. Gated by
// PROJECT_ID. Run with:
//
//	PROJECT_ID=ghchinoy-genai-sa go test -tags integration ./cmd/mizan/ -run TestLiveCLI -v

func liveCLIProject(t *testing.T) string {
	t.Helper()
	p := os.Getenv("PROJECT_ID")
	if p == "" {
		p = os.Getenv("MIZAN_PROJECT_ID")
	}
	if p == "" {
		t.Skip("PROJECT_ID not set; skipping live CLI test")
	}
	return p
}

// useTempDB points the registry at a fresh SQLite file for the duration of the
// test so live runs never touch a developer's real registry.
func useTempDB(t *testing.T) {
	t.Helper()
	db := filepath.Join(t.TempDir(), "registry.db")
	t.Setenv("MIZAN_REGISTRY_DB", db)
}

func TestLiveCLIRubric(t *testing.T) {
	liveCLIProject(t)
	useTempDB(t)

	out, err := executeRoot(t,
		"registry", "create",
		"--id", "test/cli-ad-quality-rubric",
		"--name", "Ad Quality (rubric, CLI)",
		"--kind", "rubric",
		"--prompt", "You are a strict marketing reviewer. Rate the following ad copy on a 1-5 scale.\n\nAd copy:\n{{copy}}",
		"--sampling-count", "1",
		"--rubric-group", "clarity=The core offer is unambiguous;Free of jargon and filler",
		"--rubric-group", "tone=Matches a professional, trustworthy brand voice;No unsupported superlatives",
	)
	if err != nil {
		t.Fatalf("registry create: %v (out=%q)", err, out)
	}
	t.Logf("CREATE RUBRIC output:\n%s", out)

	runOut, err := executeRoot(t,
		"--output", "json",
		"eval", "run",
		"--metric", "test/cli-ad-quality-rubric",
		"--field", "copy=Reset your password in seconds: open Settings, tap Security, and follow the secure link we email you.",
	)
	if err != nil {
		t.Fatalf("eval run: %v (out=%q)", err, runOut)
	}
	t.Logf("EVAL RUN RUBRIC output:\n%s", runOut)

	var res eval.Result
	if err := json.Unmarshal([]byte(runOut), &res); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if res.Score == nil {
		t.Fatal("live rubric result had no score")
	}
	if strings.TrimSpace(res.Explanation) == "" {
		t.Fatal("live rubric result had no explanation")
	}
}

func TestLiveCLICustomSchema(t *testing.T) {
	liveCLIProject(t)
	useTempDB(t)

	schema := `{
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
	}`
	schemaPath := filepath.Join(t.TempDir(), "schema.json")
	if err := os.WriteFile(schemaPath, []byte(schema), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := executeRoot(t,
		"registry", "create",
		"--id", "test/cli-brand-check-custom",
		"--name", "Brand Check (custom_schema, CLI)",
		"--kind", "custom_schema",
		"--prompt", "Audit this ad copy against a professional brand voice and score it. Copy:\n{{copy}}",
		"--system", "You are a strict, consistent brand auditor. Respond only with the requested JSON.",
		"--response-schema-file", schemaPath,
	)
	if err != nil {
		t.Fatalf("registry create: %v (out=%q)", err, out)
	}
	t.Logf("CREATE CUSTOM_SCHEMA output:\n%s", out)

	runOut, err := executeRoot(t,
		"--output", "json",
		"eval", "run",
		"--metric", "test/cli-brand-check-custom",
		"--field", "copy=BUY NOW!!! GUARANTEED best deal EVER, you will NEVER find anything better, act fast!!!",
	)
	if err != nil {
		t.Fatalf("eval run: %v (out=%q)", err, runOut)
	}
	t.Logf("EVAL RUN CUSTOM_SCHEMA output:\n%s", runOut)

	var res eval.Result
	if err := json.Unmarshal([]byte(runOut), &res); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if len(res.CustomOutput) == 0 {
		t.Fatal("live custom_schema result had no parsed output")
	}
	for _, key := range []string{"overall_score", "brand_tone_score", "compliant", "flagged_issues", "explanation"} {
		if _, ok := res.CustomOutput[key]; !ok {
			t.Errorf("custom output missing required key %q", key)
		}
	}
}
