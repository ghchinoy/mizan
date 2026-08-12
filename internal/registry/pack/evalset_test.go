package pack

import "testing"

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
