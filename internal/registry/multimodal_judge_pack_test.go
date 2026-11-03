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
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ghchinoy/mizan/internal/registry"
	"github.com/ghchinoy/mizan/internal/registry/pack"
	"github.com/ghchinoy/mizan/internal/registry/sqlite"
)

func TestMultimodalJudgePackValidateClean(t *testing.T) {
	packDir := filepath.Join("testdata", "multimodal-judge", "packs", "multimodal-judge")
	rep, err := registry.ValidatePack(packDir)
	if err != nil {
		t.Fatalf("ValidatePack(%q): %v", packDir, err)
	}
	if rep.Errors() != 0 || rep.Warnings() != 0 {
		t.Fatalf("ValidatePack(%q) findings = %+v, want 0 errors and 0 warnings", packDir, rep.Findings)
	}
	if len(rep.Templates) != 4 {
		t.Fatalf("ValidatePack(%q) templates = %d, want 4", packDir, len(rep.Templates))
	}

	treeRoot := filepath.Join("testdata", "multimodal-judge")
	rootRep, err := registry.ValidatePack(treeRoot)
	if err != nil {
		t.Fatalf("ValidatePack(%q): %v", treeRoot, err)
	}
	if rootRep.Errors() != 0 || rootRep.Warnings() != 0 {
		t.Fatalf("ValidatePack(%q) findings = %+v, want 0 errors and 0 warnings", treeRoot, rootRep.Findings)
	}
	found := make(map[string]bool, len(rootRep.Templates))
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
			t.Errorf("ValidatePack(%q) did not discover template %q", treeRoot, id)
		}
	}
}

func TestMultimodalJudgePackImportEvalSetAndGoldenFixtures(t *testing.T) {
	ctx := t.Context()
	packDir := filepath.Join("testdata", "multimodal-judge", "packs", "multimodal-judge")

	dbPath := filepath.Join(t.TempDir(), "registry.db")
	store, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open(%q): %v", dbPath, err)
	}
	t.Cleanup(func() { _ = store.Close() })

	svc := registry.NewService(store)
	impRep, err := svc.Import(ctx, packDir, registry.ImportOptions{})
	if err != nil {
		t.Fatalf("Service.Import(%q): %v", packDir, err)
	}
	if impRep.Inserted != 4 || impRep.Conflicted != 0 || impRep.Skipped != 0 {
		t.Fatalf("Service.Import(%q) = %+v, want 4 inserted, 0 conflicted, 0 skipped", packDir, impRep)
	}

	reimpRep, err := svc.Import(ctx, packDir, registry.ImportOptions{})
	if err != nil {
		t.Fatalf("Service.Import(%q) idempotent re-import: %v", packDir, err)
	}
	if reimpRep.Unchanged != 4 || reimpRep.Inserted != 0 || reimpRep.Conflicted != 0 {
		t.Fatalf("Service.Import(%q) re-import = %+v, want 4 unchanged", packDir, reimpRep)
	}

	evalSetPath := filepath.Join(packDir, "evalsets", "multimodal-judge-core.yaml")
	evalSetBytes, err := os.ReadFile(evalSetPath)
	if err != nil {
		t.Fatalf("os.ReadFile(%q): %v", evalSetPath, err)
	}
	doc, err := pack.ParseEvalSet(evalSetBytes)
	if err != nil {
		t.Fatalf("pack.ParseEvalSet(%q): %v", evalSetPath, err)
	}
	if doc.Metadata.ID != "multimodal-judge/multimodal-judge-core" {
		t.Fatalf("ParseEvalSet(%q) ID = %q, want %q", evalSetPath, doc.Metadata.ID, "multimodal-judge/multimodal-judge-core")
	}
	if len(doc.Spec.Members) != 4 {
		t.Fatalf("ParseEvalSet(%q) members = %d, want 4", evalSetPath, len(doc.Spec.Members))
	}
	if doc.Spec.Aggregation == nil || doc.Spec.Aggregation.Threshold == nil || *doc.Spec.Aggregation.Threshold != 2.2 {
		t.Fatalf("ParseEvalSet(%q) threshold = %+v, want 2.2", evalSetPath, doc.Spec.Aggregation)
	}
	for _, m := range doc.Spec.Members {
		if _, err := svc.Get(ctx, m.Metric); err != nil {
			t.Fatalf("svc.Get(%q) for EvalSet member after import: %v", m.Metric, err)
		}
	}

	fixturePath := filepath.Join(packDir, "examples", "golden_evalset.jsonl")
	fixtureBytes, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("os.ReadFile(%q): %v", fixturePath, err)
	}
	type fixtureRow struct {
		ID       string            `json:"id"`
		Metric   string            `json:"metric"`
		Tier     string            `json:"tier"`
		Category string            `json:"category"`
		GCS      map[string]string `json:"gcs"`
		Files    map[string]string `json:"files"`
		Fields   map[string]string `json:"fields"`
		Expected any               `json:"expected"`
	}
	sc := bufio.NewScanner(bytes.NewReader(fixtureBytes))
	var rowCount int
	perMetric := make(map[string]int)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		rowCount++
		var row fixtureRow
		if err := json.Unmarshal(line, &row); err != nil {
			t.Fatalf("json.Unmarshal(%q line %d): %v", fixturePath, rowCount, err)
		}
		if row.ID == "" || row.Expected == nil {
			t.Fatalf("%q line %d = %+v, want non-empty id and expected", fixturePath, rowCount, row)
		}
		tmpl, err := svc.Get(ctx, row.Metric)
		if err != nil {
			t.Fatalf("svc.Get(%q) on line %d: %v", row.Metric, rowCount, err)
		}
		for _, in := range tmpl.Inputs {
			if !in.Required {
				continue
			}
			_, inText := row.Fields[in.Name]
			_, inFile := row.Files[in.Name]
			_, inGCS := row.GCS[in.Name]
			if !inText && !inFile && !inGCS {
				t.Errorf("fixture %q (%q) missing required input %q", row.ID, row.Metric, in.Name)
			}
		}
		perMetric[row.Metric]++
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("bufio.Scanner(%q): %v", fixturePath, err)
	}
	if rowCount != 8 {
		t.Fatalf("%q rowCount = %d, want 8", fixturePath, rowCount)
	}
	for _, m := range doc.Spec.Members {
		if perMetric[m.Metric] != 2 {
			t.Errorf("perMetric[%q] = %d, want 2", m.Metric, perMetric[m.Metric])
		}
	}
}
