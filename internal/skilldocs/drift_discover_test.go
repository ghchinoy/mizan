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

package skilldocs

// This is the contract gate for the discover-and-import-templates skill
// (plugins/mizan-authoring, Phase-5 Tier-2). Coverage is deliberately REUSED
// rather than duplicated:
//
//   - `registry import -o json` renders a registry.ImportReport, which is ALREADY
//     drift-gated by drift_rubric_test.go (marker
//     `<!-- drift:registry-import -o json -->` in the rubric-generate skill).
//     discover-and-import references that single gate/shape instead of declaring a
//     second ImportReport contract block, so no ImportReport gate is added here.
//
//   - `registry list -o json` / `registry get -o json` render
//     registry.MetricTemplate ([]MetricTemplate and one, respectively). This skill
//     documents only the STABLE IDENTITY fields it tells the agent to read for
//     discovery (ID, Name, Description, Version, Kind, Tags, Authors, License,
//     Source). The gate below is HERMETIC (encodes a constructed MetricTemplate;
//     no network/ADC) and anchors those documented field names to the REAL encoder
//     on BOTH sides: each identity field must (a) appear as a top-level key in the
//     rendered registry.MetricTemplate json, and (b) be named in the SKILL.md. A
//     rename on the struct (or a stray edit in the doc) breaks the tie. The full
//     template body is the authoring surface (author-and-validate-a-template-pack)
//     and is not re-documented here. A synthetic-drift self-test proves the gate
//     discriminates.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/ghchinoy/mizan/internal/registry"
)

// discoverSkillRelPath is the discover-and-import-templates SKILL.md relative to
// this test file (<repo>/internal/skilldocs, so "../.." is the repo root).
var discoverSkillRelPath = filepath.Join(
	"..", "..",
	"plugins", "mizan-authoring", "skills", "discover-and-import-templates", "SKILL.md",
)

// discoveryIdentityFields are the stable registry.MetricTemplate top-level json
// keys the skill tells the agent to read for discovery. They are the contract
// discover-and-import depends on; the gate anchors each to the real encoder.
var discoveryIdentityFields = []string{
	"ID", "Name", "Description", "Version", "Kind", "Tags", "Authors", "License", "Source",
}

func discoverSkillText(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed; cannot locate SKILL.md")
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(thisFile), discoverSkillRelPath))
	if err != nil {
		t.Fatalf("read discover-and-import-templates SKILL.md: %v", err)
	}
	return string(raw)
}

// renderTemplateTopKeys renders a registry.MetricTemplate exactly as `registry
// list`/`get -o json` do (cmd/mizan.printJSON == json.Encoder with SetIndent) and
// returns the set of TOP-LEVEL keys. cmd/mizan is package main and cannot be
// imported, so the encoder is replicated, not re-invoked.
func renderTemplateTopKeys(t *testing.T) map[string]bool {
	t.Helper()
	// A representative template with the identity fields populated. Value-typed
	// identity fields have no omitempty, so they render regardless; populating
	// them keeps the fixture explicit.
	tmpl := registry.MetricTemplate{
		ID:                   "acme/tone",
		Name:                 "Brand tone",
		Description:          "Rate how well a response matches the brand voice.",
		Version:              "1.0.0",
		Authors:              []registry.Author{{Name: "Alice Example"}},
		License:              "Apache-2.0",
		Tags:                 []string{"brand", "tone"},
		Kind:                 registry.KindPointwise,
		MetricPromptTemplate: "Rate {{response}}",
		Source:               "pack:acme@origin",
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(tmpl); err != nil {
		t.Fatalf("encode registry.MetricTemplate: %v", err)
	}
	var live map[string]any
	if err := json.Unmarshal(buf.Bytes(), &live); err != nil {
		t.Fatalf("re-parse rendered registry.MetricTemplate json: %v", err)
	}
	out := map[string]bool{}
	for k := range live {
		out[k] = true
	}
	return out
}

// structTopKeys returns the top-level json key names a struct declares (skipping
// json:"-" fields). Used to prove the documented identity fields are REAL fields.
func structTopKeys(tp reflect.Type) map[string]bool {
	out := map[string]bool{}
	for tp.Kind() == reflect.Pointer {
		tp = tp.Elem()
	}
	for i := 0; i < tp.NumField(); i++ {
		f := tp.Field(i)
		if !f.IsExported() {
			continue
		}
		name := f.Name
		if tag, ok := f.Tag.Lookup("json"); ok {
			first := strings.Split(tag, ",")[0]
			if first == "-" {
				continue
			}
			if first != "" {
				name = first
			}
		}
		out[name] = true
	}
	return out
}

// TestDiscoverIdentityFieldsAnchored is the primary gate: every documented
// discovery identity field must be a REAL top-level key the CLI's `registry
// list`/`get -o json` path emits, AND must be named in the SKILL.md. This ties
// the doc to the encoder on both sides — a struct rename or a doc edit that drops
// a field fails the gate.
func TestDiscoverIdentityFieldsAnchored(t *testing.T) {
	live := renderTemplateTopKeys(t)
	declared := structTopKeys(reflect.TypeOf(registry.MetricTemplate{}))
	doc := discoverSkillText(t)

	for _, f := range discoveryIdentityFields {
		if !declared[f] {
			t.Errorf("identity field %q is not a declared registry.MetricTemplate json field (drift)", f)
		}
		if !live[f] {
			t.Errorf("identity field %q is not emitted by registry list/get -o json (drift)", f)
		}
		if !strings.Contains(doc, "`"+f+"`") {
			t.Errorf("SKILL.md does not document identity field %q it relies on", f)
		}
	}
}

// TestDiscoverIdentityFieldsCatchSyntheticDrift proves the anchor actually
// discriminates: a renamed struct field (simulated by removing it from the live
// key set) must be reported missing.
func TestDiscoverIdentityFieldsCatchSyntheticDrift(t *testing.T) {
	live := renderTemplateTopKeys(t)

	// Baseline: every identity field is present today.
	for _, f := range discoveryIdentityFields {
		if !live[f] {
			t.Fatalf("baseline expected identity field %q present before synthetic mutation", f)
		}
	}

	// Simulate the CLI renaming ID -> Identifier: the presence check must fail.
	mutated := clone(live)
	delete(mutated, "ID")
	mutated["Identifier"] = true
	if mutated["ID"] {
		t.Fatal("synthetic mutation did not remove ID")
	}
	missing := false
	for _, f := range discoveryIdentityFields {
		if !mutated[f] {
			missing = true
		}
	}
	if !missing {
		t.Error("synthetic drift (renamed ID) not caught by the identity-field anchor")
	}
}

// codeFenceRe matches any fenced code block (the invoked commands), as opposed to
// prose. Deferred surfaces may be named in prose (as disclaimers) but must never
// appear inside an executable code block.
var codeFenceRe = regexp.MustCompile("(?ms)^[ \t]*```[a-zA-Z0-9]*[ \t]*\r?\n(.*?)^[ \t]*```")

// TestDiscoverDoesNotUseTagFlag guards the deferred-surface invariant: the skill
// must NOT instruct the agent to RUN tag-filtered discovery (`registry list
// --tag`) or any other deferred command. It may document `--tag` in PROSE as an
// explicit "not used / deferred" disclaimer, so the check scans only the fenced
// code blocks (the actual invocations), not the prose.
func TestDiscoverDoesNotUseTagFlag(t *testing.T) {
	doc := discoverSkillText(t)
	var code strings.Builder
	for _, m := range codeFenceRe.FindAllStringSubmatch(doc, -1) {
		code.WriteString(m[1])
		code.WriteString("\n")
	}
	blocks := code.String()
	for _, forbidden := range []string{
		"--tag",
		"results summary",
		"results trend",
		"mizan mcp",
	} {
		if strings.Contains(blocks, forbidden) {
			t.Errorf("discover-and-import SKILL.md INVOKES deferred surface %q in a code block; it must only be referenced as a prose disclaimer", forbidden)
		}
	}
}
