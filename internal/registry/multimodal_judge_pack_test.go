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

package registry_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ghchinoy/mizan/internal/registry"
	"github.com/ghchinoy/mizan/internal/registry/pack"
	"github.com/ghchinoy/mizan/internal/registry/sqlite"
)

func TestMultimodalJudgePackValidateClean(t *testing.T) {
	packDir := filepath.Join("..", "..", "packs", "multimodal-judge")
	rep, err := registry.ValidatePack(packDir)
	if err != nil {
		t.Fatalf("ValidatePack(%s): %v", packDir, err)
	}
	if rep.Errors() != 0 || rep.Warnings() != 0 {
		t.Fatalf("ValidatePack(%s) findings = %+v (want 0 errors, 0 warnings)", packDir, rep.Findings)
	}
	if len(rep.Templates) != 4 {
		t.Fatalf("ValidatePack(%s) templates = %d, want 4", packDir, len(rep.Templates))
	}

	repoRoot := filepath.Join("..", "..")
	rootRep, err := registry.ValidatePack(repoRoot)
	if err != nil {
		t.Fatalf("ValidatePack(%s): %v", repoRoot, err)
	}
	if rootRep.Errors() != 0 || rootRep.Warnings() != 0 {
		t.Fatalf("ValidatePack(%s) findings = %+v (want 0 errors, 0 warnings)", repoRoot, rootRep.Findings)
	}
	found := map[string]bool{}
	for _, tmpl := range rootRep.Templates {
		found[tmpl.ID] = true
	}
	wantIDs := []string{
		"multimodal-judge/fine-print-verify",
		"multimodal-judge/som-ui-grounding",
		"multimodal-judge/visual-pairwise-preference",
		"multimodal-judge/untrusted-ocr-quarantine",
	}
	for _, id := range wantIDs {
		if !found[id] {
			t.Errorf("repo-root ValidatePack did not discover template %q", id)
		}
	}
}

func TestMultimodalJudgePackImportEvalSetAndGoldenFixtures(t *testing.T) {
	ctx := context.Background()
	packDir := filepath.Join("..", "..", "packs", "multimodal-judge")

	dbPath := filepath.Join(t.TempDir(), "registry.db")
	store, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	svc := registry.NewService(store)
	impRep, err := svc.Import(ctx, packDir, registry.ImportOptions{})
	if err != nil {
		t.Fatalf("Service.Import(%s): %v", packDir, err)
	}
	if impRep.Inserted != 4 || impRep.Conflicted != 0 || impRep.Skipped != 0 {
		t.Fatalf("ImportReport = %+v, want 4 inserted, 0 conflicted, 0 skipped", impRep)
	}

	reimpRep, err := svc.Import(ctx, packDir, registry.ImportOptions{})
	if err != nil {
		t.Fatalf("idempotent re-import: %v", err)
	}
	if reimpRep.Unchanged != 4 || reimpRep.Inserted != 0 || reimpRep.Conflicted != 0 {
		t.Fatalf("re-import = %+v, want 4 unchanged", reimpRep)
	}

	evalSetBytes, err := os.ReadFile(filepath.Join(packDir, "evalsets", "multimodal-judge-core.yaml"))
	if err != nil {
		t.Fatalf("read evalset manifest: %v", err)
	}
	doc, err := pack.ParseEvalSet(evalSetBytes)
	if err != nil {
		t.Fatalf("ParseEvalSet: %v", err)
	}
	if doc.Metadata.ID != "multimodal-judge/multimodal-judge-core" {
		t.Fatalf("EvalSet ID = %q, want multimodal-judge/multimodal-judge-core", doc.Metadata.ID)
	}
	if len(doc.Spec.Members) != 4 {
		t.Fatalf("EvalSet members = %d, want 4", len(doc.Spec.Members))
	}
	for _, m := range doc.Spec.Members {
		if _, err := svc.Get(ctx, m.Metric); err != nil {
			t.Fatalf("EvalSet member %q missing after import: %v", m.Metric, err)
		}
	}

	fixtureBytes, err := os.ReadFile(filepath.Join(packDir, "fixtures", "golden_evalset.jsonl"))
	if err != nil {
		t.Fatalf("read golden_evalset.jsonl: %v", err)
	}
	type fixtureRow struct {
		ID       string            `json:"id"`
		Metric   string            `json:"metric"`
		Tier     string            `json:"tier"`
		Category string            `json:"category"`
		GCS      map[string]string `json:"gcs"`
		Fields   map[string]string `json:"fields"`
		Expected any               `json:"expected"`
	}
	sc := bufio.NewScanner(bytes.NewReader(fixtureBytes))
	var rowCount int
	perMetric := map[string]int{}
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		rowCount++
		var r fixtureRow
		if err := json.Unmarshal(line, &r); err != nil {
			t.Fatalf("line %d invalid JSON: %v", rowCount, err)
		}
		if r.ID == "" || r.Expected == nil {
			t.Fatalf("line %d missing id or expected: %+v", rowCount, r)
		}
		tmpl, err := svc.Get(ctx, r.Metric)
		if err != nil {
			t.Fatalf("line %d metric %q not in imported registry: %v", rowCount, r.Metric, err)
		}
		for _, in := range tmpl.Inputs {
			if !in.Required {
				continue
			}
			_, inText := r.Fields[in.Name]
			_, inGCS := r.GCS[in.Name]
			if !inText && !inGCS {
				t.Errorf("fixture %s (%s) missing required input %q", r.ID, r.Metric, in.Name)
			}
		}
		perMetric[r.Metric]++
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan golden_evalset.jsonl: %v", err)
	}
	if rowCount != 8 {
		t.Fatalf("golden_evalset.jsonl rows = %d, want 8", rowCount)
	}
	for _, m := range doc.Spec.Members {
		if perMetric[m.Metric] != 2 {
			t.Errorf("fixture count for %s = %d, want 2", m.Metric, perMetric[m.Metric])
		}
	}
}
