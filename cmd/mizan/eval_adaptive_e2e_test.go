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

package main

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"github.com/ghchinoy/mizan/internal/config"
	"github.com/ghchinoy/mizan/internal/eval"
	"github.com/ghchinoy/mizan/internal/eval/evaltest"
	"github.com/ghchinoy/mizan/internal/rubricgen"
	"github.com/ghchinoy/mizan/internal/rubricgen/rubricgentest"
	"github.com/ghchinoy/mizan/internal/wire"
)

// TestEvalAdaptiveSaveAsPersistsProvenanceE2E is the true end-to-end CUJ 8 test
// the PR C audit flagged as previously unwritable: it drives the full
// `eval adaptive --save-as` path — generate → score → FREEZE — with BOTH live
// clients replaced by fakes (the newRubricGenerator seam for Stage 1, the
// openEngine seam for Stage 2) and a REAL temp-file sqlite store, so it needs no
// ADC/Vertex credentials. It proves the generated rubric's provenance
// round-trips through the default backend on the save path (design §4.4 /
// RFC-0001), which the shared-builder-level tests could only assert in memory.
func TestEvalAdaptiveSaveAsPersistsProvenanceE2E(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "registry.db")
	t.Setenv("MIZAN_REGISTRY_DB", dbPath)

	// Stage 1 seam: fake generator returns canned criteria (no live generation).
	genFake := &rubricgentest.FakeClient{Rubrics: []rubricgen.Rubric{
		rubricgentest.Rubric("Answers the question directly", "quality", "HIGH"),
		rubricgentest.Rubric("Cites a supporting source", "grounding", "MEDIUM"),
	}}
	prevGen := newRubricGenerator
	newRubricGenerator = func(context.Context, *config.Config) (rubricgen.Client, error) { return genFake, nil }
	t.Cleanup(func() { newRubricGenerator = prevGen })

	// Stage 2 seam: fake engine over a fake EvaluationClient — no ADC/Vertex. This
	// is the seam the audit asked for: without it, openEngine → wire.NewEngine
	// needs live credentials and the save path cannot be exercised in a unit test.
	evalFake := (&evaltest.FakeEvaluationClient{}).PushResponse(evaltest.NewPointwiseResponse(4, "solid answer"))
	prevEng := openEngine
	openEngine = func(context.Context, *config.Config) (*eval.Engine, func() error, error) {
		return eval.NewEngine(evalFake, "test-project", "us-central1"), func() error { return nil }, nil
	}
	t.Cleanup(func() { openEngine = prevEng })

	cmd := newEvalAdaptiveCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--prompt", "Explain photosynthesis", "--response", "Plants convert light into energy.", "--save-as", "acme/quality"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("eval adaptive --save-as: %v", err)
	}

	// Both seams were actually driven (the path really ran, end to end).
	if genFake.Calls() != 1 {
		t.Fatalf("generator calls = %d, want 1", genFake.Calls())
	}
	if got := len(evalFake.CallsLog); got != 1 {
		t.Fatalf("eval client calls = %d, want 1", got)
	}

	// Read the frozen template back through the SAME composition root the command
	// used, from the real temp sqlite store, and assert provenance survived.
	cfg, err := config.LoadConfig()
	if err != nil && err != config.ErrMissingProjectID {
		t.Fatalf("LoadConfig: %v", err)
	}
	svc, closeSvc, err := wire.OpenService(cfg)
	if err != nil {
		t.Fatalf("OpenService: %v", err)
	}
	defer func() { _ = closeSvc() }()

	got, err := svc.Get(context.Background(), "acme/quality")
	if err != nil {
		t.Fatalf("Get(acme/quality): %v", err)
	}
	if got.RubricProvenance == nil {
		t.Fatal("frozen template has nil RubricProvenance — provenance was dropped on the save path")
	}
	if got.RubricProvenance.Method != "adaptive-generated" {
		t.Fatalf("provenance Method = %q, want adaptive-generated", got.RubricProvenance.Method)
	}
	if n := len(got.RubricProvenance.RubricMeta); n != 2 {
		t.Fatalf("provenance RubricMeta len = %d, want 2 (round-trip lost per-criterion metadata)", n)
	}
	if c := got.RubricProvenance.RubricMeta[0].Criterion; c != "Answers the question directly" {
		t.Fatalf("provenance RubricMeta[0].Criterion = %q, want the first generated criterion", c)
	}
	if len(got.RubricGroups) == 0 {
		t.Fatal("frozen template has no RubricGroups — the runnable rubric was not persisted")
	}
}
