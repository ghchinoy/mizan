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

package pack

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v5"
	yaml "gopkg.in/yaml.v3"
)

func TestParseEvalSet(t *testing.T) {
	data := []byte(`apiVersion: mizan.dev/v1alpha1
kind: EvalSet
metadata:
  id: acme/suite
  name: Suite
  version: 1.0.0
  assetClass: product-video
spec:
  inputs:
    asset: response
  members:
    - metric: acme/one
    - metric: acme/two
      weight: 2
      required: true
      bind:
        candidate: asset
  aggregation:
    method: mean
    threshold: 0.8
  display:
    order: as-listed
`)
	doc, err := ParseEvalSet(data)
	if err != nil {
		t.Fatalf("ParseEvalSet: %v", err)
	}
	if doc.Kind != KindEvalSet {
		t.Errorf("Kind = %q, want %q", doc.Kind, KindEvalSet)
	}
	if doc.Metadata.ID != "acme/suite" || doc.Metadata.AssetClass != "product-video" {
		t.Errorf("metadata = %+v", doc.Metadata)
	}
	if len(doc.Spec.Members) != 2 {
		t.Fatalf("members = %d, want 2", len(doc.Spec.Members))
	}
	if doc.Spec.Members[0].Metric != "acme/one" {
		t.Errorf("member[0].Metric = %q", doc.Spec.Members[0].Metric)
	}
	// reserved fields are carried, not dropped.
	if doc.Spec.Members[1].Weight == nil || *doc.Spec.Members[1].Weight != 2 {
		t.Errorf("member[1].Weight not carried: %+v", doc.Spec.Members[1])
	}
	if doc.Spec.Members[1].Bind["candidate"] != "asset" {
		t.Errorf("member[1].Bind not carried: %+v", doc.Spec.Members[1].Bind)
	}
	if doc.Spec.Aggregation == nil || doc.Spec.Aggregation.Method != "mean" {
		t.Errorf("aggregation not carried: %+v", doc.Spec.Aggregation)
	}
	if doc.Spec.Inputs["asset"] != "response" {
		t.Errorf("reserved spec.inputs not carried: %+v", doc.Spec.Inputs)
	}
}

// TestParseEvalSetGate covers the end-to-end gate carriage the test-engineer
// flagged as untested: (1) ParseEvalSet decodes aggregation.gate:true into
// EvalSetAggregation.Gate (pointer to true), and (2) the shipped
// schema/evalset.json ACCEPTS a manifest carrying aggregation.gate:true.
func TestParseEvalSetGate(t *testing.T) {
	data := []byte(`apiVersion: mizan.dev/v1alpha1
kind: EvalSet
metadata:
  id: acme/suite
  name: Suite
  version: 1.0.0
  assetClass: product-video
spec:
  members:
    - metric: acme/one
  aggregation:
    method: mean
    threshold: 0.8
    gate: true
`)

	// (1) parse carries gate as *bool(true).
	doc, err := ParseEvalSet(data)
	if err != nil {
		t.Fatalf("ParseEvalSet: %v", err)
	}
	if doc.Spec.Aggregation == nil {
		t.Fatal("aggregation not carried")
	}
	if doc.Spec.Aggregation.Gate == nil {
		t.Fatal("aggregation.gate not carried (want *bool true, got nil)")
	}
	if !*doc.Spec.Aggregation.Gate {
		t.Fatalf("aggregation.gate = %v, want true", *doc.Spec.Aggregation.Gate)
	}

	// (2) the shipped schema accepts a gate:true manifest.
	schema := compileEvalSetSchema(t)
	if err := schema.Validate(yamlToJSON(t, data)); err != nil {
		t.Fatalf("schema rejected a valid gate:true manifest: %v", err)
	}
}

// compileEvalSetSchema compiles the shipped schema/evalset.json (the single
// source of truth also embedded by internal/registry) so the pack test can
// assert the schema accepts the gate property without importing registry (which
// would be an import cycle: registry imports pack).
func compileEvalSetSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	path := filepath.FromSlash("../schema/evalset.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read schema %s: %v", path, err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("evalset.json", bytes.NewReader(data)); err != nil {
		t.Fatalf("add schema: %v", err)
	}
	s, err := c.Compile("evalset.json")
	if err != nil {
		t.Fatalf("compile schema: %v", err)
	}
	return s
}

// yamlToJSON converts a YAML manifest into the generic JSON value shape the
// jsonschema validator expects (map[string]any / []any / float64 / …), mirroring
// the registry validator's yamlToJSONValue.
func yamlToJSON(t *testing.T, data []byte) any {
	t.Helper()
	var y any
	if err := yaml.Unmarshal(data, &y); err != nil {
		t.Fatalf("yaml unmarshal: %v", err)
	}
	jb, err := json.Marshal(y)
	if err != nil {
		t.Fatalf("json marshal: %v", err)
	}
	var v any
	if err := json.Unmarshal(jb, &v); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	return v
}

func TestParseEvalSetEmpty(t *testing.T) {
	doc, err := ParseEvalSet(nil)
	if err != nil {
		t.Fatalf("ParseEvalSet(nil): %v", err)
	}
	if doc.Kind != "" {
		t.Errorf("empty doc Kind = %q, want empty", doc.Kind)
	}
}

func TestParseEvalSetMalformed(t *testing.T) {
	if _, err := ParseEvalSet([]byte("kind: EvalSet\n\tbad: indent")); err == nil {
		t.Fatal("expected a parse error for malformed YAML")
	}
}
