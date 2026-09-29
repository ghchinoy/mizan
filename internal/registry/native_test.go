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

package registry

import (
	"reflect"
	"strings"
	"testing"
)

// computationExampleYAML is the documented kind:computation example
// (docs/user-guide.md). It must validate with NO errors and NO warnings.
const computationExampleYAML = `apiVersion: mizan.dev/v1alpha1
kind: MetricTemplate
metadata:
  id: nlg/rouge-l-summary
  name: ROUGE-L summary overlap
  description: Summary-level ROUGE-L F-measure against a reference summary; no model involved.
  version: 1.0.0
  license: Apache-2.0
spec:
  kind: computation
  modalities: [text]
  inputs:
    - name: summary
      modality: text
      required: true
    - name: reference
      modality: text
      required: true
  native:
    metric: rouge
    responseField: summary
    rougeType: rougeLsum
    passThreshold: 0.4
`

// prebuiltExampleYAML is the documented kind:prebuilt example.
const prebuiltExampleYAML = `apiVersion: mizan.dev/v1alpha1
kind: MetricTemplate
metadata:
  id: rag/groundedness
  name: RAG answer groundedness
  description: Vertex prebuilt groundedness judge; every claim must be supported by the retrieved context.
  version: 1.0.0
  license: Apache-2.0
spec:
  kind: prebuilt
  modalities: [text]
  inputs:
    - name: answer
      modality: text
      required: true
    - name: context
      modality: text
      required: true
  native:
    metric: groundedness
    responseField: answer
    contextField: context
    passThreshold: 1
  autorater:
    model: gemini-2.5-flash
`

func TestValidatePackAcceptsNativeExamples(t *testing.T) {
	for name, doc := range map[string]string{"computation": computationExampleYAML, "prebuilt": prebuiltExampleYAML} {
		t.Run(name, func(t *testing.T) {
			rep := validatePackDir(t, map[string]string{"templates/t.yaml": doc})
			if len(rep.Findings) != 0 {
				t.Fatalf("findings:\n%s%s", errorMessages(rep), warningMessages(rep))
			}
			if len(rep.Templates) != 1 || rep.Templates[0].Native == nil {
				t.Fatalf("template not carried with its native spec: %+v", rep.Templates)
			}
			if err := ValidateTemplateSchema([]byte(doc)); err != nil {
				t.Errorf("schema: %v", err)
			}
		})
	}
}

// TestNativeCodecRoundTripAndHash: spec.native survives Unmarshal -> Marshal ->
// Unmarshal unchanged, and it is part of the content hash (editing the threshold
// shifts it) while a template with NO native spec keeps a hash independent of
// the new field.
func TestNativeCodecRoundTripAndHash(t *testing.T) {
	c := YAMLCodec{}
	t1, err := c.Unmarshal([]byte(computationExampleYAML))
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if t1.Kind != KindComputation || t1.Native == nil || t1.Native.Metric != "rouge" || *t1.Native.PassThreshold != 0.4 {
		t.Fatalf("decoded = %+v / %+v", t1, t1.Native)
	}
	out, err := c.Marshal(t1)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(out), "native:") {
		t.Fatalf("marshaled YAML lacks native:\n%s", out)
	}
	t2, err := c.Unmarshal(out)
	if err != nil {
		t.Fatalf("re-Unmarshal: %v", err)
	}
	if !reflect.DeepEqual(t1.Native, t2.Native) {
		t.Errorf("native round-trip: %+v vs %+v", t1.Native, t2.Native)
	}

	h1 := contentHash(t1)
	thr := 0.5
	t2.Native.PassThreshold = &thr
	if contentHash(t2) == h1 {
		t.Error("changing spec.native.passThreshold must shift the content hash")
	}
	// Nil Native is omitted from the canonical form, so pre-existing templates
	// keep their golden hashes.
	plain := &MetricTemplate{ID: "a/b", Kind: KindPointwise}
	before := contentHash(plain)
	plain.Native = nil
	if contentHash(plain) != before {
		t.Error("nil Native must not affect the hash")
	}
}

func TestNormalizeKindNative(t *testing.T) {
	for _, k := range []string{"computation", "prebuilt"} {
		got, err := NormalizeKind(k)
		if err != nil || string(got) != k {
			t.Errorf("NormalizeKind(%q) = %q, %v", k, got, err)
		}
	}
	_, err := NormalizeKind("computed")
	if err == nil || !strings.Contains(err.Error(), "computation, prebuilt") {
		t.Errorf("unknown-kind error = %v, want it to list computation, prebuilt", err)
	}
}

// TestValidatePackNativeDefects: one case per rule.
func TestValidatePackNativeDefects(t *testing.T) {
	base := func(kind, native, extra string) string {
		return `apiVersion: mizan.dev/v1alpha1
kind: MetricTemplate
metadata:
  id: acme/native
  description: d
  version: 1.0.0
  license: Apache-2.0
spec:
  kind: ` + kind + `
  modalities: [text]
  inputs:
    - name: response
      modality: text
      required: true
    - name: reference
      modality: text
` + native + extra
	}
	cases := []struct {
		name, doc, want string
	}{
		{"computation requires native", base("computation", "", ""), `kind "computation" requires spec.native`},
		{"prebuilt requires native", base("prebuilt", "", "  autorater:\n    model: gemini-2.5-flash\n"), `kind "prebuilt" requires spec.native`},
		{"native forbidden on pointwise", base("pointwise", "  native:\n    metric: bleu\n", "  metricPromptTemplate: \"{{response}} {{reference}}\"\n  autorater:\n    model: gemini-2.5-flash\n"),
			`kind "pointwise" must not set spec.native`},
		{"metric wrong kind", base("computation", "  native:\n    metric: fluency\n", ""), `is a kind "prebuilt" metric`},
		{"unknown metric (schema)", base("computation", "  native:\n    metric: meteor\n", ""), "value must be one of"},
		{"undeclared field", base("computation", "  native:\n    metric: bleu\n    referenceField: gold\n", ""), `reference field "gold" is not declared`},
		{"required role missing", base("prebuilt", "  native:\n    metric: groundedness\n", "  autorater:\n    model: gemini-2.5-flash\n"), "requires spec.native.contextField"},
		{"role the metric does not read", base("prebuilt", "  native:\n    metric: fluency\n    referenceField: reference\n", ""), `does not read a reference`},
		{"computation forbids prompt", base("computation", "  native:\n    metric: bleu\n", "  metricPromptTemplate: \"{{response}}\"\n"), "must not set spec.metricPromptTemplate"},
		{"computation forbids autorater", base("computation", "  native:\n    metric: bleu\n", "  autorater:\n    model: gemini-2.5-flash\n"), "must not set spec.autorater"},
		{"stemmer rejected", base("computation", "  native:\n    metric: rouge\n    useStemmer: true\n", ""), "useStemmer: true is not supported"},
		{"rougeType on bleu", base("computation", "  native:\n    metric: bleu\n    rougeType: rouge1\n", ""), `rougeType only applies to metric "rouge"`},
		{"toolName required", base("computation", "  native:\n    metric: trajectory_single_tool_use\n", ""), "requires spec.native.toolName"},
		{"threshold on pairwise", base("prebuilt",
			"  native:\n    metric: pairwise_summarization_quality\n    contextField: reference\n    instructionField: reference\n    baselineField: response\n    passThreshold: 1\n",
			"  autorater:\n    model: gemini-2.5-flash\n"), "passThreshold does not apply"},
		{"prompt+instruction aliases", base("prebuilt", "  native:\n    metric: fulfillment\n    promptField: reference\n    instructionField: reference\n", ""), "set only one"},
		{"same input two roles", base("computation", "  native:\n    metric: bleu\n    referenceField: response\n", ""), "maps input \"response\" to both"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rep := validatePackDir(t, map[string]string{"templates/t.yaml": tc.doc})
			errs := errorMessages(rep)
			if !strings.Contains(errs, tc.want) {
				t.Errorf("errors do not contain %q:\n%s", tc.want, errs)
			}
		})
	}

	// A REQUIRED input the native mapping never reads is an error.
	doc := strings.Replace(base("computation", "  native:\n    metric: trajectory_single_tool_use\n    toolName: search\n", ""),
		"    - name: reference\n      modality: text\n", "    - name: reference\n      modality: text\n      required: true\n", 1)
	rep := validatePackDir(t, map[string]string{"templates/t.yaml": doc})
	if !strings.Contains(errorMessages(rep), `required input "reference" is never mapped by spec.native`) {
		t.Errorf("errors:\n%s", errorMessages(rep))
	}
}

// TestComputationNoAutoraterLint: a computation template is not nagged about a
// missing autorater.model (it has none by design); a prebuilt one still is.
func TestComputationNoAutoraterLint(t *testing.T) {
	rep := validatePackDir(t, map[string]string{"templates/t.yaml": computationExampleYAML})
	if w := warningMessages(rep); strings.Contains(w, "autorater") {
		t.Errorf("computation got an autorater lint: %s", w)
	}
	noModel := strings.Replace(prebuiltExampleYAML, "  autorater:\n    model: gemini-2.5-flash\n", "", 1)
	rep = validatePackDir(t, map[string]string{"templates/t.yaml": noModel})
	if w := warningMessages(rep); !strings.Contains(w, "no autorater.model") {
		t.Errorf("prebuilt without a model should lint, warnings: %q", w)
	}
}

// TestResolveNativeFieldsDefaults: response/reference default names apply; an
// optional reference is bound only when mapped.
func TestResolveNativeFieldsDefaults(t *testing.T) {
	got, err := ResolveNativeFields(&NativeMetricSpec{Metric: "bleu"})
	if err != nil {
		t.Fatal(err)
	}
	want := []NativeFieldBinding{{NativeRoleResponse, "response", true}, {NativeRoleReference, "reference", true}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("bleu bindings = %+v, want %+v", got, want)
	}
	got, err = ResolveNativeFields(&NativeMetricSpec{Metric: "question_answering_relevance", PromptField: "q"})
	if err != nil {
		t.Fatal(err)
	}
	want = []NativeFieldBinding{{NativeRoleResponse, "response", true}, {NativeRoleInstruction, "q", true}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("qa relevance bindings = %+v, want %+v", got, want)
	}
	if len(NativeMetricIDs(KindComputation)) != 13 || len(NativeMetricIDs(KindPrebuilt)) != 14 {
		t.Errorf("metric counts = %d / %d, want 13 / 14", len(NativeMetricIDs(KindComputation)), len(NativeMetricIDs(KindPrebuilt)))
	}
}
