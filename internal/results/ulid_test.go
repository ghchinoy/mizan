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

package results

import (
	"strings"
	"testing"
)

func TestNewULIDLengthAndCharset(t *testing.T) {
	id, err := NewULID()
	if err != nil {
		t.Fatalf("NewULID: %v", err)
	}
	if len(id) != 26 {
		t.Fatalf("ULID length = %d, want 26 (%q)", len(id), id)
	}
	for i, c := range id {
		if !strings.ContainsRune(crockford, c) {
			t.Errorf("ULID[%d]=%q not in Crockford alphabet (%q)", i, c, id)
		}
	}
}

func TestNewULIDTimeSortableAndUnique(t *testing.T) {
	const n = 10000
	prev := ""
	seen := make(map[string]struct{}, n)
	for i := 0; i < n; i++ {
		id, err := NewULID()
		if err != nil {
			t.Fatalf("NewULID #%d: %v", i, err)
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate ULID at #%d: %q", i, id)
		}
		seen[id] = struct{}{}
		// IDs generated in sequence must be strictly increasing lexically, which
		// (Crockford base32 preserves order) means sortable by creation time.
		if prev != "" && id <= prev {
			t.Fatalf("ULID #%d not strictly increasing: %q <= %q", i, id, prev)
		}
		prev = id
	}
}

func TestEncodeULIDDeterministic(t *testing.T) {
	var entropy [10]byte // all zero
	got := encodeULID(0, entropy)
	want := strings.Repeat("0", 26)
	if got != want {
		t.Errorf("encodeULID(0, 0) = %q, want %q", got, want)
	}
}
