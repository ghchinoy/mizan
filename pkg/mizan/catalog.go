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

package mizan

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"

	"github.com/ghchinoy/mizan/internal/evalset"
	"github.com/ghchinoy/mizan/internal/registry"
	"github.com/ghchinoy/mizan/internal/registry/pack"
)

const (
	maxManifestFileBytes int64 = registry.MaxTemplateFileBytes // 1 MiB per file
	maxCatalogTotalBytes int64 = 64 << 20                      // 64 MiB aggregate across LoadCatalogFS
)

// TemplateSource resolves [Template] manifests by ID during [Evaluator.RunSet].
// [*Catalog] implements TemplateSource.
type TemplateSource interface {
	Template(id string) (Template, bool)
}

// ParseTemplate parses and strictly validates a single MetricTemplate YAML
// manifest (enforcing JSON Schema additionalProperties:false, identity, kind,
// placeholder parity, and SHA-256 ContentHash).
func ParseTemplate(data []byte) (Template, error) {
	tmpl, err := registry.ParseTemplateStrict(data)
	if err != nil {
		return Template{}, fmt.Errorf("mizan: %w", err)
	}
	return Template{raw: *tmpl}, nil
}

// ParseEvalSet parses and strictly validates a single EvalSet YAML manifest.
func ParseEvalSet(data []byte) (EvalSet, error) {
	doc, err := registry.ParseEvalSetStrict(data)
	if err != nil {
		return EvalSet{}, fmt.Errorf("mizan: %w", err)
	}
	set, err := evalset.FromDoc(doc)
	if err != nil {
		return EvalSet{}, fmt.Errorf("mizan: %w", err)
	}
	return EvalSet{raw: set}, nil
}

// Catalog is a concurrency-safe, in-memory collection of [Template] and
// [EvalSet] manifests (requiring zero SQLite files or disk locks).
type Catalog struct {
	mu        sync.RWMutex
	templates map[string]Template
	evalSets  map[string]EvalSet
}

// NewCatalog returns an empty in-memory [Catalog].
func NewCatalog() *Catalog {
	return &Catalog{
		templates: make(map[string]Template),
		evalSets:  make(map[string]EvalSet),
	}
}

// AddTemplate inserts tmpl into the catalog, returning an error if its ID is
// empty or already registered.
func (c *Catalog) AddTemplate(tmpl Template) error {
	if tmpl.ID() == "" {
		return fmt.Errorf("mizan: cannot add template with empty ID")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.templates[tmpl.ID()]; exists {
		return fmt.Errorf("mizan: duplicate template ID %q in catalog", tmpl.ID())
	}
	c.templates[tmpl.ID()] = tmpl
	return nil
}

// AddEvalSet inserts set into the catalog, returning an error if its ID is
// empty or already registered.
func (c *Catalog) AddEvalSet(set EvalSet) error {
	if set.ID() == "" {
		return fmt.Errorf("mizan: cannot add evalset with empty ID")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.evalSets[set.ID()]; exists {
		return fmt.Errorf("mizan: duplicate evalset ID %q in catalog", set.ID())
	}
	c.evalSets[set.ID()] = set
	return nil
}

// Template looks up a [Template] by "<namespace>/<slug>" ID.
func (c *Catalog) Template(id string) (Template, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	t, ok := c.templates[id]
	return t, ok
}

// Templates returns all templates in deterministic ID order.
func (c *Catalog) Templates() []Template {
	c.mu.RLock()
	defer c.mu.RUnlock()
	ids := make([]string, 0, len(c.templates))
	for id := range c.templates {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]Template, len(ids))
	for i, id := range ids {
		out[i] = c.templates[id]
	}
	return out
}

// EvalSet looks up an [EvalSet] by "<namespace>/<slug>" ID.
func (c *Catalog) EvalSet(id string) (EvalSet, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	s, ok := c.evalSets[id]
	return s, ok
}

// EvalSets returns all suites in deterministic ID order.
func (c *Catalog) EvalSets() []EvalSet {
	c.mu.RLock()
	defer c.mu.RUnlock()
	ids := make([]string, 0, len(c.evalSets))
	for id := range c.evalSets {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]EvalSet, len(ids))
	for i, id := range ids {
		out[i] = c.evalSets[id]
	}
	return out
}

type manifestHeader struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
}

// LoadCatalogFS walks fsys (for example an embed.FS or os.DirFS over a
// mizan-templates pack tree), parses and validates every MetricTemplate and
// EvalSet YAML manifest (capped at 1 MiB per file and 64 MiB total), and returns
// an in-memory [*Catalog]. Pack metadata manifests (kind: Pack) are skipped.
func LoadCatalogFS(fsys fs.FS) (*Catalog, error) {
	if fsys == nil {
		return nil, fmt.Errorf("mizan: nil fs.FS")
	}
	cat := NewCatalog()
	var totalBytes int64

	err := fs.WalkDir(fsys, ".", func(fpath string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		ext := strings.ToLower(path.Ext(fpath))
		if ext != ".yaml" && ext != ".yml" {
			return nil
		}

		f, err := fsys.Open(fpath)
		if err != nil {
			return fmt.Errorf("mizan: open %q: %w", fpath, err)
		}
		data, readErr := io.ReadAll(io.LimitReader(f, maxManifestFileBytes+1))
		_ = f.Close()
		if readErr != nil {
			return fmt.Errorf("mizan: read %q: %w", fpath, readErr)
		}
		if int64(len(data)) > maxManifestFileBytes {
			return fmt.Errorf("mizan: manifest %q exceeds %d-byte per-file limit", fpath, maxManifestFileBytes)
		}
		totalBytes += int64(len(data))
		if totalBytes > maxCatalogTotalBytes {
			return fmt.Errorf("mizan: catalog exceeds %d-byte aggregate limit", maxCatalogTotalBytes)
		}

		var hdr manifestHeader
		if err := yaml.Unmarshal(data, &hdr); err != nil {
			return fmt.Errorf("mizan: parse %q YAML header: %w", fpath, err)
		}
		switch hdr.Kind {
		case "MetricTemplate":
			tmpl, err := ParseTemplate(data)
			if err != nil {
				return fmt.Errorf("%s: %w", fpath, err)
			}
			if err := cat.AddTemplate(tmpl); err != nil {
				return fmt.Errorf("%s: %w", fpath, err)
			}
		case pack.KindEvalSet:
			set, err := ParseEvalSet(data)
			if err != nil {
				return fmt.Errorf("%s: %w", fpath, err)
			}
			if err := cat.AddEvalSet(set); err != nil {
				return fmt.Errorf("%s: %w", fpath, err)
			}
		case "Pack":
			return nil
		default:
			return fmt.Errorf("mizan: unsupported manifest kind %q in %q (want MetricTemplate or EvalSet)", hdr.Kind, fpath)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return cat, nil
}

type sourceAdapter struct {
	src TemplateSource
}

func (a sourceAdapter) Get(_ context.Context, id string) (*registry.MetricTemplate, error) {
	if a.src == nil {
		return nil, fmt.Errorf("mizan: nil TemplateSource")
	}
	t, ok := a.src.Template(id)
	if !ok {
		return nil, fmt.Errorf("mizan: template %q not found in TemplateSource", id)
	}
	cp := t.raw
	return &cp, nil
}
