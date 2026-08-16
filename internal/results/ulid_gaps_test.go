package results

import "testing"

// TestIncrEntropyCarry proves the 10-byte big-endian counter carries across a
// byte boundary: incrementing 0x..FF rolls the low byte to 0x00 and bumps the
// next byte, returning true (no overflow).
func TestIncrEntropyCarry(t *testing.T) {
	var e [10]byte
	e[9] = 0xFF
	if ok := incrEntropy(&e); !ok {
		t.Fatal("incrEntropy returned false (overflow) on a single carry, want true")
	}
	want := [10]byte{0, 0, 0, 0, 0, 0, 0, 0, 1, 0}
	if e != want {
		t.Errorf("after carry e = %v, want %v", e, want)
	}
}

// TestIncrEntropyOverflow proves the all-0xFF counter wraps to all-zero and
// reports overflow (false) — the astronomically-unlikely same-millisecond
// exhaustion branch NewULID guards against by bumping the timestamp.
func TestIncrEntropyOverflow(t *testing.T) {
	var e [10]byte
	for i := range e {
		e[i] = 0xFF
	}
	if ok := incrEntropy(&e); ok {
		t.Fatal("incrEntropy returned true on all-0xFF, want false (overflow)")
	}
	var zero [10]byte
	if e != zero {
		t.Errorf("after overflow e = %v, want all zero", e)
	}
}

// TestNewULIDMonotonicSameMillisecond proves that IDs generated back-to-back
// within a single millisecond stay strictly increasing — the monotonic-entropy
// path (ms <= ulidLastMS) rather than the fresh-random path. A tight loop keeps
// most iterations inside one ms; every pair must still be strictly ordered.
func TestNewULIDMonotonicSameMillisecond(t *testing.T) {
	const n = 2000
	prev, err := NewULID()
	if err != nil {
		t.Fatalf("NewULID: %v", err)
	}
	for i := 1; i < n; i++ {
		id, err := NewULID()
		if err != nil {
			t.Fatalf("NewULID #%d: %v", i, err)
		}
		if id <= prev {
			t.Fatalf("ULID #%d not strictly increasing: %q <= %q", i, id, prev)
		}
		prev = id
	}
}
