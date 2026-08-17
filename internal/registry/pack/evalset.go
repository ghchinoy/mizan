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

// Package pack holds small, validation-only parse types for pack manifests that
// have no home in the shipped registry domain model. Today that is EvalSetDoc:
// the P2 codec parses a `kind: EvalSet` file into this local value used ONLY for
// `pack validate` and git carriage (design §3.4a, D6). There is deliberately no
// EvalSet domain struct in the registry model, no store table, and no import/run
// path — that is the separate eval-set capability, out of P2 scope. Keeping this
// type here (not in package registry) also keeps it dependency-free: it imports
// no registry symbols, so the format can be carried and validated without
// threading anything into the store or the eval engine.
package pack

import (
	"bytes"
	"fmt"
	"io"

	yaml "gopkg.in/yaml.v3"
)

// APIVersion is the frozen pack format version (design §3.2), shared by the
// MetricTemplate and EvalSet manifests.
const APIVersion = "mizan.dev/v1alpha1"

// KindEvalSet is the manifest `kind:` for an eval-set document (design §3.4a).
const KindEvalSet = "EvalSet"

// maxEvalSetFileBytes bounds an EvalSet file read/parse. It mirrors the
// registry's MaxTemplateFileBytes (1 MiB) so a hostile pack cannot ship a
// multi-GB manifest and exhaust memory during a creds-free CI validate
// (CWE-400); the constant is duplicated rather than imported to keep this
// package free of a registry dependency.
const maxEvalSetFileBytes int64 = 1 << 20

// EvalSetMember is one ordered member of an eval-set: a reference to a metric
// template id plus optional, RESERVED-but-not-interpreted per-member fields
// (weight, required, bind) that the future runner may honor (design §3.4a). P2
// carries them and does not act on them.
type EvalSetMember struct {
	Metric   string         `yaml:"metric"`
	Weight   *float64       `yaml:"weight,omitempty"`
	Required *bool          `yaml:"required,omitempty"`
	Bind     map[string]any `yaml:"bind,omitempty"`
}

// EvalSetMetadata is the identity/attribution block of an eval-set manifest.
type EvalSetMetadata struct {
	ID          string   `yaml:"id"`
	Name        string   `yaml:"name,omitempty"`
	Description string   `yaml:"description,omitempty"`
	Version     string   `yaml:"version,omitempty"`
	AssetClass  string   `yaml:"assetClass,omitempty"`
	Tags        []string `yaml:"tags,omitempty"`
}

// EvalSetAggregation is the RESERVED aggregation config carried for the future
// runner (design §3.4a). Only Method is validated (against a reserved enum) in
// P2; Threshold and any other keys are carried, not interpreted.
type EvalSetAggregation struct {
	Method    string   `yaml:"method,omitempty"`
	Threshold *float64 `yaml:"threshold,omitempty"`
	// Gate is the opt-in pass/fail gate flag (default off). It is carried here and
	// consumed by the eval-set runner (internal/evalset); P2 validate does not
	// interpret it.
	Gate *bool `yaml:"gate,omitempty"`
}

// EvalSetSpec is the behavior block of an eval-set manifest.
type EvalSetSpec struct {
	// Inputs is the RESERVED set-level input alias map (design §3.4a): carried,
	// schema-allowed, not interpreted in P2.
	Inputs      map[string]any      `yaml:"inputs,omitempty"`
	Members     []EvalSetMember     `yaml:"members,omitempty"`
	Aggregation *EvalSetAggregation `yaml:"aggregation,omitempty"`
	Display     map[string]any      `yaml:"display,omitempty"`
}

// EvalSetDoc is the parsed shape of a `kind: EvalSet` pack file (design §3.4a).
// It is a validation-and-carriage value ONLY — never threaded into
// registry.Store or eval.Engine. The eval-set capability, when it lands, defines
// the domain type + store table; the on-disk format it reads is the one frozen
// here.
type EvalSetDoc struct {
	APIVersion string          `yaml:"apiVersion"`
	Kind       string          `yaml:"kind"`
	Metadata   EvalSetMetadata `yaml:"metadata"`
	Spec       EvalSetSpec     `yaml:"spec"`
}

// ParseEvalSet decodes one EvalSet YAML document into an EvalSetDoc. It performs
// no semantic validation (that is `pack validate`'s job, design §3.5) beyond
// bounding the decoded input size; a malformed YAML document is a hard parse
// error. The input is read through a LimitReader so an oversized manifest cannot
// exhaust memory during a creds-free validate.
func ParseEvalSet(data []byte) (*EvalSetDoc, error) {
	var doc EvalSetDoc
	dec := yaml.NewDecoder(io.LimitReader(bytes.NewReader(data), maxEvalSetFileBytes+1))
	if err := dec.Decode(&doc); err != nil && err != io.EOF {
		return nil, fmt.Errorf("pack: parse evalset yaml: %w", err)
	}
	return &doc, nil
}
