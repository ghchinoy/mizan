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

package evalset

import (
	"fmt"
	"sort"

	"github.com/ghchinoy/mizan/internal/eval"
	"github.com/ghchinoy/mizan/internal/registry"
)

// ExpectedFields returns the distinct input placeholder or role field names
// consumed by tmpl at evaluation time.
func ExpectedFields(tmpl registry.MetricTemplate) ([]string, error) {
	switch tmpl.Kind {
	case registry.KindHeuristic:
		target := "response"
		if tmpl.Heuristic != nil && tmpl.Heuristic.Target != "" {
			target = tmpl.Heuristic.Target
		}
		return []string{target}, nil
	case registry.KindComputation, registry.KindPrebuilt:
		if tmpl.Native == nil {
			return nil, nil
		}
		bindings, err := registry.ResolveNativeFields(tmpl.Native)
		if err != nil {
			return nil, err
		}
		var fields []string
		seen := make(map[string]bool, len(bindings))
		for _, b := range bindings {
			if b.Field != "" && !seen[b.Field] {
				seen[b.Field] = true
				fields = append(fields, b.Field)
			}
		}
		return fields, nil
	case registry.KindPairwise:
		var fields []string
		seen := make(map[string]bool)
		add := func(name string) {
			if name != "" && !seen[name] {
				seen[name] = true
				fields = append(fields, name)
			}
		}
		add(tmpl.BaselineFieldName)
		add(tmpl.CandidateFieldName)
		for _, v := range registry.ExtractPlaceholders(tmpl.MetricPromptTemplate) {
			add(v)
		}
		return fields, nil
	default:
		return registry.ExtractPlaceholders(tmpl.MetricPromptTemplate), nil
	}
}

// projectMemberInstance builds a member's isolated eval.Instance by projecting
// shared suite inputs onto the member's expected placeholders using:
//
//  1. Member.Bind overrides (setInputName -> memberPlaceholderName),
//  2. Set.Inputs default aliases (setInputName -> memberPlaceholderName),
//  3. Identity fallback (expectedPlaceholder -> shared[expectedPlaceholder]).
//
// It returns the projected eval.Instance (with its own map copy so members
// cannot mutate shared inputs) and the set of shared input keys consumed.
func projectMemberInstance(set Set, member Member, tmpl registry.MetricTemplate, shared map[string]eval.AssetRef) (eval.Instance, map[string]bool, error) {
	expected, err := ExpectedFields(tmpl)
	if err != nil {
		return eval.Instance{}, nil, err
	}

	// Synthetic / unauthored prompt templates (MetricPromptTemplate == "") carry no
	// extractable placeholders; pass shared inputs by copy so Engine.Run's own
	// empty-prompt guard (or fake unit-test runners) handle them directly.
	if len(expected) == 0 && tmpl.MetricPromptTemplate == "" &&
		tmpl.Kind != registry.KindHeuristic && tmpl.Kind != registry.KindComputation && tmpl.Kind != registry.KindPrebuilt {
		if len(shared) == 0 {
			return eval.Instance{}, nil, nil
		}
		consumed := make(map[string]bool, len(shared))
		fields := make(map[string]eval.AssetRef, len(shared))
		for k, v := range shared {
			target := k
			if mapped, ok := set.Inputs[k]; ok && mapped != "" {
				target = mapped
			}
			if mapped, ok := member.Bind[k]; ok && mapped != "" {
				target = mapped
			}
			fields[target] = v
			consumed[k] = true
		}
		return eval.Instance{Fields: fields}, consumed, nil
	}

	expectedSet := make(map[string]bool, len(expected))
	for _, f := range expected {
		expectedSet[f] = true
	}
	for setKey, targetField := range member.Bind {
		if len(expected) > 0 && !expectedSet[targetField] {
			return eval.Instance{}, nil, fmt.Errorf("evalset: member %q bind maps set input %q to unknown placeholder %q (template expects %v)", member.MetricID, setKey, targetField, expected)
		}
	}

	if len(shared) == 0 {
		return eval.Instance{}, nil, nil
	}

	effectiveBind := make(map[string]string)
	for setKey, defaultTarget := range set.Inputs {
		if expectedSet[defaultTarget] {
			effectiveBind[setKey] = defaultTarget
		}
	}
	for setKey, targetField := range member.Bind {
		effectiveBind[setKey] = targetField
	}

	bindKeys := make([]string, 0, len(effectiveBind))
	for k := range effectiveBind {
		bindKeys = append(bindKeys, k)
	}
	sort.Strings(bindKeys)

	fields := make(map[string]eval.AssetRef, len(expected))
	consumed := make(map[string]bool, len(expected))
	targetSource := make(map[string]string, len(expected))

	for _, setKey := range bindKeys {
		targetField := effectiveBind[setKey]
		val, ok := shared[setKey]
		if !ok {
			continue
		}
		if prevKey, dup := targetSource[targetField]; dup && prevKey != setKey {
			return eval.Instance{}, nil, fmt.Errorf("evalset: member %q binds both set inputs %q and %q to placeholder %q", member.MetricID, prevKey, setKey, targetField)
		}
		targetSource[targetField] = setKey
		fields[targetField] = val
		consumed[setKey] = true
	}

	for _, expField := range expected {
		if _, alreadyBound := fields[expField]; alreadyBound {
			continue
		}
		if mapped, hasExplicit := effectiveBind[expField]; hasExplicit && mapped != expField {
			continue
		}
		if val, ok := shared[expField]; ok {
			fields[expField] = val
			consumed[expField] = true
		}
	}

	return eval.Instance{Fields: fields}, consumed, nil
}
