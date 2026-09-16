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
	"strings"
	"testing"
)

// TestValidatePackRejectsExternalRefSchemas is the audit §5 case-7 end-to-end
// guard: `pack validate` over a pack containing a heuristic json-schema-valid
// template AND a custom_schema template, EACH carrying a file:// $ref, must
// report validation ERRORS (a clear "not a valid JSON Schema" message from the
// inline-only loader) — never a silent os.Open on the attacker path.
func TestValidatePackRejectsExternalRefSchemas(t *testing.T) {
	const heuristicFileRef = `apiVersion: mizan.dev/v1alpha1
kind: MetricTemplate
metadata: {id: acme/evil-heuristic, version: 1.0.0, description: attacker heuristic}
spec:
  kind: heuristic
  modalities: [text]
  inputs:
    - {name: response, modality: text, required: true}
  heuristic:
    type: json-schema-valid
    target: response
    schema: "{\"$ref\":\"file:///etc/hostname\"}"
`
	const customSchemaFileRef = `apiVersion: mizan.dev/v1alpha1
kind: MetricTemplate
metadata: {id: acme/evil-custom, version: 1.0.0, description: attacker custom_schema}
spec:
  kind: custom_schema
  modalities: [text]
  inputs:
    - {name: response, modality: text, required: true}
  responseSchema:
    $ref: "file:///etc/hostname"
`

	rep := validatePackDir(t, map[string]string{
		"templates/heuristic.yaml": heuristicFileRef,
		"templates/custom.yaml":    customSchemaFileRef,
	})

	if !rep.HasErrors() {
		t.Fatal("pack validate accepted external file:// $ref schemas; want errors")
	}
	msgs := errorMessages(rep)
	if !strings.Contains(msgs, "spec.heuristic.schema is not a valid JSON Schema") {
		t.Errorf("missing heuristic schema error; got:\n%s", msgs)
	}
	if !strings.Contains(msgs, "spec.responseSchema is not a valid JSON Schema") {
		t.Errorf("missing responseSchema error; got:\n%s", msgs)
	}
	// The failure must be the loader refusing the external ref, not an os.Open.
	if !strings.Contains(msgs, "not permitted") {
		t.Errorf("error should name the refused external ref; got:\n%s", msgs)
	}
}
