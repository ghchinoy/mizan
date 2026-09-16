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

package eval

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v5"

	"github.com/ghchinoy/mizan/internal/registry"
)

// runHeuristic evaluates a KindHeuristic template deterministically and WITHOUT
// any credential, client, or network access (design §4.B). It is a FREE function
// (not an *Engine method) BY DESIGN so it structurally cannot reference
// e.client / e.globalClient / e.genai or reach Vertex/genai — the credential-free
// invariant is enforced by construction, not by convention. It maps a boolean
// pass/fail to Score = 1.0 (pass) / 0.0 (fail) via *float32 (not a bespoke enum)
// so heuristic results flow unchanged through eval-set aggregation, gating
// thresholds, and B3's numeric rollups, and sets a deterministic Explanation.
func runHeuristic(tmpl registry.MetricTemplate, inst Instance) (Result, error) {
	spec := tmpl.Heuristic
	if spec == nil {
		return Result{}, fmt.Errorf("eval: heuristic template %q has no heuristic spec", tmpl.ID)
	}
	if spec.Target == "" {
		return Result{}, fmt.Errorf("eval: heuristic template %q has no target field", tmpl.ID)
	}

	ref, ok := inst.Fields[spec.Target]
	if !ok {
		return Result{}, fmt.Errorf("eval: heuristic template %q target field %q was not supplied", tmpl.ID, spec.Target)
	}
	// v1 is text-modality only: the check operates on a single text field.
	if ref.Modality != "" && ref.Modality != registry.ModalityText {
		return Result{}, fmt.Errorf("eval: heuristic template %q target field %q must be text (got modality %q)", tmpl.ID, spec.Target, ref.Modality)
	}
	text := ref.Text

	pass, explanation, err := evalHeuristicCheck(spec, text)
	if err != nil {
		return Result{}, err
	}
	return Result{Score: heuristicScore(pass), Explanation: explanation}, nil
}

// evalHeuristicCheck runs the deterministic check against text, returning the
// boolean verdict and a stable, human-readable explanation. A compile/parse
// failure of a rule the template carries (a bad regex or schema that slipped past
// `pack validate`) is returned as an error, never a silent fail.
func evalHeuristicCheck(spec *registry.HeuristicSpec, text string) (bool, string, error) {
	switch spec.Type {
	case registry.HeuristicContains:
		hay, needle := text, spec.Value
		if spec.CaseInsensitive {
			hay, needle = strings.ToLower(hay), strings.ToLower(needle)
		}
		if strings.Contains(hay, needle) {
			return true, fmt.Sprintf("matched: text contains %q", spec.Value), nil
		}
		return false, fmt.Sprintf("no match: text does not contain %q", spec.Value), nil

	case registry.HeuristicEquals:
		a, b := text, spec.Value
		if spec.CaseInsensitive {
			a, b = strings.ToLower(a), strings.ToLower(b)
		}
		if a == b {
			return true, fmt.Sprintf("matched: text equals %q", spec.Value), nil
		}
		return false, fmt.Sprintf("no match: text does not equal %q", spec.Value), nil

	case registry.HeuristicRegex:
		re, err := compileHeuristicRegex(spec)
		if err != nil {
			return false, "", err
		}
		if re.MatchString(text) {
			return true, fmt.Sprintf("matched: text matches regex %q", spec.Value), nil
		}
		return false, fmt.Sprintf("no match: text does not match regex %q", spec.Value), nil

	case registry.HeuristicJSONValid:
		if json.Valid([]byte(text)) {
			return true, "matched: text is well-formed JSON", nil
		}
		return false, "no match: text is not well-formed JSON", nil

	case registry.HeuristicJSONSchemaValid:
		schema, err := compileHeuristicSchema(spec)
		if err != nil {
			return false, "", err
		}
		var doc any
		if err := json.Unmarshal([]byte(text), &doc); err != nil {
			return false, fmt.Sprintf("no match: text is not well-formed JSON: %v", err), nil
		}
		if err := schema.Validate(doc); err != nil {
			return false, "no match: text does not validate against the schema", nil
		}
		return true, "matched: text validates against the schema", nil

	default:
		return false, "", fmt.Errorf("eval: unknown heuristic type %q", spec.Type)
	}
}

// compileHeuristicRegex compiles the regex operand with Go's regexp (RE2), which
// has no catastrophic backtracking — a deliberate safety property. CaseInsensitive
// is applied as the RE2 (?i) flag.
func compileHeuristicRegex(spec *registry.HeuristicSpec) (*regexp.Regexp, error) {
	re, err := registry.CompileHeuristicRegex(spec)
	if err != nil {
		return nil, fmt.Errorf("eval: heuristic regex %q is invalid: %w", spec.Value, err)
	}
	return re, nil
}

// compileHeuristicSchema compiles the JSON-Schema operand using the same
// santhosh-tekuri/jsonschema compiler the registry's validateResponseSchema uses,
// so a schema that passes `pack validate` compiles identically here.
func compileHeuristicSchema(spec *registry.HeuristicSpec) (*jsonschema.Schema, error) {
	if strings.TrimSpace(spec.Schema) == "" {
		return nil, fmt.Errorf("eval: heuristic json-schema-valid check has no schema")
	}
	c := registry.NewInlineOnlyCompiler()
	if err := c.AddResource("heuristic-schema.json", bytes.NewReader([]byte(spec.Schema))); err != nil {
		return nil, fmt.Errorf("eval: heuristic schema is invalid: %w", err)
	}
	schema, err := c.Compile("heuristic-schema.json")
	if err != nil {
		return nil, fmt.Errorf("eval: heuristic schema is invalid: %w", err)
	}
	return schema, nil
}

// heuristicScore maps a boolean verdict to the float score representation
// (1.0 pass / 0.0 fail) via a fresh *float32.
func heuristicScore(pass bool) *float32 {
	var s float32
	if pass {
		s = 1.0
	}
	return &s
}
