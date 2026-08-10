package version

import (
	"encoding/json"
	"testing"
)

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

// TestInfoJSONShape pins the exact JSON rendering of Info — field names, field
// order, and 2-space indentation (the repo convention used by cmd's printJSON).
// It covers both injected known values and the unset-ldflags defaults so a
// change to a json tag, the field order, or the indentation is caught at the
// single source of truth rather than only via the command wiring.
func TestInfoJSONShape(t *testing.T) {
	tests := []struct {
		name string
		info Info
		want string
	}{
		{
			name: "injected known values",
			info: Info{Version: "v1.2.3", Commit: "abc1234", Date: "2026-08-10T00:00:00Z"},
			want: `{
  "version": "v1.2.3",
  "commit": "abc1234",
  "date": "2026-08-10T00:00:00Z"
}`,
		},
		{
			name: "unset ldflags defaults",
			info: Get(),
			want: `{
  "version": "dev",
  "commit": "none",
  "date": "unknown"
}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, err := json.MarshalIndent(tt.info, "", "  ")
			if err != nil {
				t.Fatalf("MarshalIndent: %v", err)
			}
			if got := string(b); got != tt.want {
				t.Errorf("Info JSON =\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}
