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
	"fmt"
	"io"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

// refuseExternalRefs is a jsonschema URL loader that rejects ALL external
// reference resolution — file://, http(s)://, and any relative/absolute ref
// that resolves to a document outside the compiled root. Same-document refs
// ("#/...") never reach a loader (they resolve against the already-added root
// resource), so they keep working. This closes the default `file` loader's
// os.Open on attacker-controlled paths (and the /dev/zero OOM vector) at every
// schema compile site that handles untrusted pack content.
func refuseExternalRefs(uri string) (io.ReadCloser, error) {
	return nil, fmt.Errorf(
		"jsonschema: external $ref %q is not permitted; only inline same-document \"#/...\" references are allowed",
		uri)
}

// NewInlineOnlyCompiler returns a jsonschema.Compiler that cannot load any
// external reference. Use this everywhere a schema is compiled instead of
// jsonschema.NewCompiler(): it pins the per-instance Compiler.LoadURL (not any
// process-global state) to a loader that refuses every external ref, so a
// schema string embedded in an untrusted pack cannot trigger os.Open on an
// attacker-controlled path. Legitimate inline same-document ("#/...") refs are
// unaffected — they resolve against the added root resource and never reach
// the loader.
func NewInlineOnlyCompiler() *jsonschema.Compiler {
	c := jsonschema.NewCompiler()
	c.LoadURL = refuseExternalRefs
	return c
}
