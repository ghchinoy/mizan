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

// This is the contract gate for the configure-mizan skill (plugins/mizan-setup),
// the Phase-5 (Tier-2) sibling of the eval.Result / results.Result gates. The
// skill documents TWO -o json shapes that no existing drift test covers:
//
//  1. `mizan config show -o json` renders a *config.Config via
//     cmd/mizan.printJSON (a thin json.Encoder(SetIndent) wrapper). This gate
//     replicates that encoder over a fully-populated config.Config and asserts
//     the documented json key-set equals it (+ completeness over the real
//     struct). The Sources field is a map keyed by the `config set` keys — its
//     children are DATA, not a declared schema — so it is treated as a freeform
//     leaf (mirroring Outcome.CustomOutput in the results gate).
//  2. `mizan version -o json` renders a version.Info; this gate diffs the
//     documented block against the real struct's rendered key-set.
//
// Both gates are HERMETIC by construction: they encode constructed structs (no
// LoadConfig, no environment probe, no network, no ADC). `config set` output is
// TEXT ("set <key> (<env>) in <path>"), not JSON, so it is covered by a
// text/exit-code contract note in the SKILL.md, not a json gate. Each gate
// includes a synthetic-drift self-test proving it discriminates.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/ghchinoy/mizan/internal/config"
	"github.com/ghchinoy/mizan/internal/version"
)

// configSkillRelPath is the configure-mizan SKILL.md relative to this test file
// (<repo>/internal/skilldocs, so "../.." is the repo root).
var configSkillRelPath = filepath.Join(
	"..", "..",
	"plugins", "mizan-setup", "skills", "configure-mizan", "SKILL.md",
)

const (
	configDriftMarker  = "<!-- drift:config.Config -->"
	versionDriftMarker = "<!-- drift:version.Info -->"
)

// configFreeformPaths: Sources is a map[string]config.Source keyed by the
// `config set` keys; its children are values, not part of config.Config's
// contract. Treat it as an opaque leaf (like Outcome.CustomOutput).
var configFreeformPaths = map[string]bool{
	"Sources": true,
}

// configSkillText reads the configure-mizan SKILL.md from this test file's own
// location so the gate is independent of `go test`'s working directory.
func configSkillText(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed; cannot locate SKILL.md")
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(thisFile), configSkillRelPath))
	if err != nil {
		t.Fatalf("read configure-mizan SKILL.md: %v", err)
	}
	return string(raw)
}

// documentedConfigBlock extracts the fenced json object after marker in the
// configure-mizan SKILL.md and returns the parsed object. (fenceRe is defined in
// drift_test.go.)
func documentedConfigBlock(t *testing.T, marker string) map[string]any {
	t.Helper()
	text := configSkillText(t)
	mi := strings.Index(text, marker)
	if mi < 0 {
		t.Fatalf("configure-mizan SKILL.md is missing the %q marker", marker)
	}
	m := fenceRe.FindStringSubmatch(text[mi:])
	if m == nil {
		t.Fatalf("no fenced ```json block found after the %q marker", marker)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(m[1]), &doc); err != nil {
		t.Fatalf("documented json block after %q is not valid JSON: %v\nblock:\n%s", marker, err, m[1])
	}
	return doc
}

// collectConfigKeys walks a decoded JSON object, path-anchored, never descending
// below a freeform path. Arrays/scalars are leaves.
func collectConfigKeys(prefix string, v any, out map[string]bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return
	}
	for k, val := range m {
		path := k
		if prefix != "" {
			path = prefix + "." + k
		}
		out[path] = true
		if configFreeformPaths[path] {
			continue
		}
		collectConfigKeys(path, val, out)
	}
}

func configKeySet(m map[string]any) map[string]bool {
	out := map[string]bool{}
	collectConfigKeys("", m, out)
	return out
}

// configStructKeys reflects over a struct type and returns the path-anchored json
// key names it declares, skipping json:"-" fields and never descending below a
// freeform path. config.Config's only non-scalar field (Sources) is freeform, so
// this stays a shallow, single-level completeness proof.
func configStructKeys(prefix string, t reflect.Type, out map[string]bool) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
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
		path := name
		if prefix != "" {
			path = prefix + "." + name
		}
		out[path] = true
	}
}

// replicateConfigJSON renders cfg exactly as `config show -o json` does
// (cmd/mizan.printJSON == json.Encoder with SetIndent). cmd/mizan is package main
// and cannot be imported, so the encoder is replicated, not re-invoked.
func replicateConfigJSON(t *testing.T, cfg config.Config) map[string]any {
	t.Helper()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(cfg); err != nil {
		t.Fatalf("encode config.Config: %v", err)
	}
	var live map[string]any
	if err := json.Unmarshal(buf.Bytes(), &live); err != nil {
		t.Fatalf("re-parse rendered config.Config json: %v", err)
	}
	return live
}

// representativeConfig populates every field of config.Config, including a
// non-empty Sources map so the (omitempty) Sources key is present in the render.
func representativeConfig() config.Config {
	return config.Config{
		ProjectID:            "my-project",
		Location:             "us-central1",
		StagingBucket:        "my-bucket",
		APIEndpoint:          "https://aiplatform.googleapis.com",
		DiffusionEndpoint:    "http://127.0.0.1:8080/v1",
		DiffusionModel:       "diffgemma-26b-a4b-it-q4",
		RegistryDBPath:       "/home/you/.config/mizan/registry.db",
		ResultsBackend:       "sqlite",
		ResultsDBPath:        "/home/you/.config/mizan/results.db",
		ResultsRetention:     "hybrid",
		PackCacheDir:         "/home/you/.cache/mizan/packs",
		DefaultTemplatesRepo: "github.com/ghchinoy/mizan-templates",
		DefaultModel:         "gemini-2.5-pro",
		AuthorName:           "Alice Example",
		DefaultLicense:       "Apache-2.0",
		Sources: map[string]config.Source{
			"project-id": config.SourceEnv,
			"location":   config.SourceDefault,
		},
	}
}

// TestConfigShowDriftKeySetEquality is the primary config gate: the config.Config
// key set documented in configure-mizan's SKILL.md must EQUAL the key set the
// CLI's `config show -o json` path renders. A field added/renamed/retagged on
// config.Config changes the rendered set and fails here until the skill updates.
func TestConfigShowDriftKeySetEquality(t *testing.T) {
	documented := configKeySet(documentedConfigBlock(t, configDriftMarker))
	live := configKeySet(replicateConfigJSON(t, representativeConfig()))

	if missing := diff(live, documented); len(missing) > 0 {
		t.Errorf("SKILL.md config.Config block is MISSING keys the CLI emits (drift): %v", missing)
	}
	if extra := diff(documented, live); len(extra) > 0 {
		t.Errorf("SKILL.md config.Config block documents keys the CLI does NOT emit (drift): %v", extra)
	}
	if !reflect.DeepEqual(sortedKeys(documented), sortedKeys(live)) {
		t.Errorf("documented vs live config.Config key-set mismatch:\n documented: %v\n live:       %v",
			sortedKeys(documented), sortedKeys(live))
	}
}

// TestConfigShowDriftStructCompleteness proves the documented block covers EVERY
// exported config.Config json field (Sources treated as a freeform leaf).
func TestConfigShowDriftStructCompleteness(t *testing.T) {
	documented := configKeySet(documentedConfigBlock(t, configDriftMarker))
	declared := map[string]bool{}
	configStructKeys("", reflect.TypeOf(config.Config{}), declared)

	if missing := diff(declared, documented); len(missing) > 0 {
		t.Errorf("config.Config declares json fields NOT documented in SKILL.md (drift): %v\n"+
			"declared: %v\ndocumented: %v", missing, sortedKeys(declared), sortedKeys(documented))
	}
}

// TestVersionInfoDriftKeySetEquality gates `mizan version -o json`: the documented
// version.Info key set must EQUAL the rendered one.
func TestVersionInfoDriftKeySetEquality(t *testing.T) {
	documented := configKeySet(documentedConfigBlock(t, versionDriftMarker))

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(version.Info{Version: "v0.1.0", Commit: "abcdef0", Date: "2026-09-16"}); err != nil {
		t.Fatalf("encode version.Info: %v", err)
	}
	var liveObj map[string]any
	if err := json.Unmarshal(buf.Bytes(), &liveObj); err != nil {
		t.Fatalf("re-parse rendered version.Info json: %v", err)
	}
	live := configKeySet(liveObj)

	if !reflect.DeepEqual(sortedKeys(documented), sortedKeys(live)) {
		t.Errorf("documented vs live version.Info key-set mismatch:\n documented: %v\n live:       %v",
			sortedKeys(documented), sortedKeys(live))
	}

	// Completeness over the real struct (all exported json fields documented).
	declared := map[string]bool{}
	configStructKeys("", reflect.TypeOf(version.Info{}), declared)
	if missing := diff(declared, documented); len(missing) > 0 {
		t.Errorf("version.Info declares json fields NOT documented in SKILL.md (drift): %v", missing)
	}
}

// TestConfigDriftCatchesSyntheticDrift proves the comparison logic actually
// catches add/remove/rename drift, guarding the gate itself against a future
// refactor that silently neuters it.
func TestConfigDriftCatchesSyntheticDrift(t *testing.T) {
	documented := configKeySet(documentedConfigBlock(t, configDriftMarker))
	live := configKeySet(replicateConfigJSON(t, representativeConfig()))

	if len(diff(live, documented)) != 0 || len(diff(documented, live)) != 0 {
		t.Fatalf("baseline expected equal key-sets before synthetic mutation")
	}

	added := clone(live)
	added["NewSetting"] = true
	if got := diff(added, documented); len(got) != 1 || got[0] != "NewSetting" {
		t.Errorf("added-field drift not caught: %v", got)
	}

	removed := clone(live)
	delete(removed, "ProjectID")
	if got := diff(documented, removed); len(got) != 1 || got[0] != "ProjectID" {
		t.Errorf("removed-field drift not caught: %v", got)
	}

	renamed := clone(live)
	delete(renamed, "DefaultModel")
	renamed["Model"] = true
	if got := diff(documented, renamed); len(got) != 1 || got[0] != "DefaultModel" {
		t.Errorf("renamed-field (old name) drift not caught: %v", got)
	}
	if got := diff(renamed, documented); len(got) != 1 || got[0] != "Model" {
		t.Errorf("renamed-field (new name) drift not caught: %v", got)
	}

	// Sources is a freeform leaf: its children must NOT appear in the key-set.
	if live["Sources.project-id"] {
		t.Error("Sources children leaked into the key-set; Sources must be a freeform leaf")
	}
}
