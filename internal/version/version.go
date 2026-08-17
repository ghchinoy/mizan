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

// Package version is the single source of truth for the mizan build version,
// git commit, and build date. The package-level vars are the injection points
// for `-ldflags -X`; the Makefile and release workflow populate them at build
// time. They default to placeholder values so a plain `go build` (no ldflags)
// still produces a usable binary.
package version

import "fmt"

// Injected at build time via -ldflags -X github.com/ghchinoy/mizan/internal/version.<var>=<value>.
// Keep these as plain package-level string vars: `go build -ldflags -X` can only
// set string variables, and only when addressed by their full package path.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// Info is the resolved build metadata. Field tags drive the `--output json`
// rendering so the JSON shape is stable and documented in one place.
type Info struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date"`
}

// Get returns the build metadata as injected (or the defaults when built
// without ldflags).
func Get() Info {
	return Info{Version: version, Commit: commit, Date: date}
}

// String renders the human-readable one-line form:
//
//	mizan <version> (commit <commit>, built <date>)
func (i Info) String() string {
	return fmt.Sprintf("mizan %s (commit %s, built %s)", i.Version, i.Commit, i.Date)
}
