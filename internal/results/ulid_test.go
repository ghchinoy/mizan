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
