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

package mcpserver

import "testing"

// TestNewServerRegisters asserts NewServer constructs a server and registers the
// four tools without panicking. It uses zero-value Deps (no handler is invoked
// here — registration is schema inference over the In/Out structs only).
func TestNewServerRegisters(t *testing.T) {
	s := NewServer(Deps{})
	if s == nil {
		t.Fatal("NewServer returned nil")
	}
}
