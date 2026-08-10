package version

import "testing"

// TestInfoString asserts the human-readable format against injected known
// values, so a change to the output shape is caught here.
func TestInfoString(t *testing.T) {
	info := Info{Version: "v1.2.3", Commit: "abc1234", Date: "2026-08-10T00:00:00Z"}
	got := info.String()
	want := "mizan v1.2.3 (commit abc1234, built 2026-08-10T00:00:00Z)"
	if got != want {
		t.Errorf("Info.String() = %q, want %q", got, want)
	}
}

// TestGetDefaults documents the placeholder values a plain `go build` (no
// ldflags) yields, so an accidental change to the defaults is caught.
func TestGetDefaults(t *testing.T) {
	got := Get()
	want := Info{Version: "dev", Commit: "none", Date: "unknown"}
	if got != want {
		t.Errorf("Get() = %+v, want %+v", got, want)
	}
}
