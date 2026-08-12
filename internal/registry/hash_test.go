package registry

import "testing"

// goldenFixtureHash pins the contentHash of the scaffolded google-brand
// video-brand-alignment template. If a change to the model, the codec, or the
// canonicalization shifts this value, this test fails LOUD so the shift is a
// conscious decision (design §3.6: the hash must be stable/back-compatible).
// Regenerate deliberately only when you intend to change the hash contract.
const goldenFixtureHash = "sha256:b7e4810b51c21e973ce45f8f63c1158efbdb44b76370e2a4424027ade988c9c2"

func TestContentHashGolden(t *testing.T) {
	c := NewYAMLCodec()
	tmpl, err := c.Unmarshal(readFixture(t))
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	got := contentHash(tmpl)
	if got != goldenFixtureHash {
		t.Errorf("contentHash = %q, want golden %q", got, goldenFixtureHash)
	}
}

// TestContentHashStableAcrossRoundTrip proves the hash is invariant under a
// Marshal/Unmarshal round-trip — the property Import relies on for drift
// detection and no-op short-circuiting.
func TestContentHashStableAcrossRoundTrip(t *testing.T) {
	c := NewYAMLCodec()
	orig, err := c.Unmarshal(readFixture(t))
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	h1 := contentHash(orig)

	data, err := c.Marshal(orig)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	round, err := c.Unmarshal(data)
	if err != nil {
		t.Fatalf("re-Unmarshal: %v", err)
	}
	h2 := contentHash(round)
	if h1 != h2 {
		t.Errorf("hash not stable across round-trip: %q != %q", h1, h2)
	}
}

// TestContentHashExcludesProvenance proves provenance/lifecycle fields do not
// affect the hash (design §3.6: hash covers identity + spec only).
func TestContentHashExcludesProvenance(t *testing.T) {
	c := NewYAMLCodec()
	base, err := c.Unmarshal(readFixture(t))
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	h1 := contentHash(base)

	base.Source = "pack:google-brand@/some/path"
	base.ContentHash = "sha256:whatever"
	base.Dirty = true
	base.ImportedAt = base.ImportedAt.AddDate(1, 0, 0)
	h2 := contentHash(base)
	if h1 != h2 {
		t.Errorf("provenance changed the hash: %q != %q", h1, h2)
	}
}

// TestContentHashChangesOnSpecEdit proves a real spec change shifts the hash.
func TestContentHashChangesOnSpecEdit(t *testing.T) {
	c := NewYAMLCodec()
	base, err := c.Unmarshal(readFixture(t))
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	h1 := contentHash(base)
	base.MetricPromptTemplate += " extra"
	if h2 := contentHash(base); h1 == h2 {
		t.Error("hash did not change after editing the prompt")
	}
}
