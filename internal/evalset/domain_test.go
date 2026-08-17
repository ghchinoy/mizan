package evalset

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ghchinoy/mizan/internal/registry/pack"
)

// realFixture is the on-main EvalSet manifest, reused here to prove FromDoc
// round-trips a real document.
const realFixture = "../registry/testdata/packs/google-brand/evalsets/product-video-suite.yaml"

func TestFromDoc_RealFixture(t *testing.T) {
	data, err := os.ReadFile(filepath.FromSlash(realFixture))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	doc, err := pack.ParseEvalSet(data)
	if err != nil {
		t.Fatalf("ParseEvalSet: %v", err)
	}

	set, err := FromDoc(doc)
	if err != nil {
		t.Fatalf("FromDoc: %v", err)
	}

	if set.ID != "google-brand/product-video-suite" {
		t.Errorf("ID = %q", set.ID)
	}
	if set.Version != "1.0.0" {
		t.Errorf("Version = %q", set.Version)
	}
	if set.AssetClass != "product-video" {
		t.Errorf("AssetClass = %q", set.AssetClass)
	}
	if got := set.Inputs["asset"]; got != "response" {
		t.Errorf("Inputs[asset] = %q, want response", got)
	}
	if len(set.Members) != 1 {
		t.Fatalf("members = %d, want 1", len(set.Members))
	}
	if set.Members[0].MetricID != "google-brand/video-brand-alignment" {
		t.Errorf("member id = %q", set.Members[0].MetricID)
	}
	// Weight defaults to 1.0 when absent.
	if set.Members[0].Weight != 1.0 {
		t.Errorf("member weight = %v, want default 1.0", set.Members[0].Weight)
	}
	if set.Aggregation.Method != AggMean {
		t.Errorf("method = %q, want mean", set.Aggregation.Method)
	}
	if set.Aggregation.Threshold == nil || *set.Aggregation.Threshold != 0.8 {
		t.Errorf("threshold = %v, want 0.8", set.Aggregation.Threshold)
	}
	// Gate absent in the fixture -> defaults false.
	if set.Aggregation.Gate {
		t.Error("gate = true, want false (absent -> off)")
	}
	if set.DisplayOrder != "as-listed" {
		t.Errorf("display order = %q, want as-listed", set.DisplayOrder)
	}
}

func TestFromDoc_UnsupportedMethodRejected(t *testing.T) {
	doc := &pack.EvalSetDoc{
		Metadata: pack.EvalSetMetadata{ID: "p/s"},
		Spec: pack.EvalSetSpec{
			Members:     []pack.EvalSetMember{{Metric: "p/a"}},
			Aggregation: &pack.EvalSetAggregation{Method: "median"},
		},
	}
	if _, err := FromDoc(doc); err == nil {
		t.Fatal("FromDoc accepted unsupported method 'median', want error")
	}
}

func TestFromDoc_GateThresholdWeight(t *testing.T) {
	gate := true
	w := 2.5
	thr := 0.9
	doc := &pack.EvalSetDoc{
		Metadata: pack.EvalSetMetadata{ID: "p/s", Version: "1.0.0"},
		Spec: pack.EvalSetSpec{
			Inputs: map[string]any{"asset": "response"},
			Members: []pack.EvalSetMember{
				{Metric: "p/a", Weight: &w, Bind: map[string]any{"asset": "response"}},
				{Metric: "p/b"}, // weight absent -> 1.0
			},
			Aggregation: &pack.EvalSetAggregation{
				Method:    "weighted-mean",
				Threshold: &thr,
				Gate:      &gate,
			},
		},
	}
	set, err := FromDoc(doc)
	if err != nil {
		t.Fatalf("FromDoc: %v", err)
	}
	if set.Members[0].Weight != 2.5 {
		t.Errorf("member[0] weight = %v, want 2.5", set.Members[0].Weight)
	}
	if set.Members[1].Weight != 1.0 {
		t.Errorf("member[1] weight = %v, want default 1.0", set.Members[1].Weight)
	}
	if set.Members[0].Bind["asset"] != "response" {
		t.Errorf("member[0] bind[asset] = %q", set.Members[0].Bind["asset"])
	}
	if !set.Aggregation.Gate {
		t.Error("gate = false, want true")
	}
	if set.Aggregation.Method != AggWeightedMean {
		t.Errorf("method = %q, want weighted-mean", set.Aggregation.Method)
	}
}

// TestFromDoc_EmptyMethodDefaultsToMean covers finding L1: an empty/absent
// aggregation method is normalized EXPLICITLY to AggMean (design §9), whether the
// aggregation block is present-but-method-empty or entirely absent.
func TestFromDoc_EmptyMethodDefaultsToMean(t *testing.T) {
	thr := 0.8
	tests := []struct {
		name string
		doc  *pack.EvalSetDoc
	}{
		{
			name: "aggregation present, method empty",
			doc: &pack.EvalSetDoc{
				Metadata: pack.EvalSetMetadata{ID: "p/s"},
				Spec: pack.EvalSetSpec{
					Members:     []pack.EvalSetMember{{Metric: "p/a"}},
					Aggregation: &pack.EvalSetAggregation{Threshold: &thr}, // method absent
				},
			},
		},
		{
			name: "aggregation block absent",
			doc: &pack.EvalSetDoc{
				Metadata: pack.EvalSetMetadata{ID: "p/s"},
				Spec: pack.EvalSetSpec{
					Members: []pack.EvalSetMember{{Metric: "p/a"}},
				},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			set, err := FromDoc(tc.doc)
			if err != nil {
				t.Fatalf("FromDoc: %v", err)
			}
			if set.Aggregation.Method != AggMean {
				t.Fatalf("method = %q, want %q (empty normalized to mean)", set.Aggregation.Method, AggMean)
			}
		})
	}
}

// TestFromDoc_NegativeWeightRejected covers finding: a negative member weight is
// rejected fail-closed, and the error NAMES the offending member (index + metric)
// so an author can locate it. Zero/absent still normalizes to 1.0 (unchanged).
func TestFromDoc_NegativeWeightRejected(t *testing.T) {
	neg := -1.5
	doc := &pack.EvalSetDoc{
		Metadata: pack.EvalSetMetadata{ID: "p/s"},
		Spec: pack.EvalSetSpec{
			Members: []pack.EvalSetMember{
				{Metric: "p/good"},
				{Metric: "p/bad", Weight: &neg},
			},
		},
	}
	_, err := FromDoc(doc)
	if err == nil {
		t.Fatal("FromDoc accepted a negative weight, want error")
	}
	if !strings.Contains(err.Error(), "p/bad") {
		t.Fatalf("error %q does not name the offending member (want p/bad)", err)
	}
	if !strings.Contains(err.Error(), "weight") {
		t.Fatalf("error %q does not mention weight", err)
	}
}

// TestFromDoc_MemberCap covers finding: a manifest with more than
// maxEvalSetMembers is rejected with a clear error; exactly maxEvalSetMembers is
// accepted.
func TestFromDoc_MemberCap(t *testing.T) {
	mk := func(n int) *pack.EvalSetDoc {
		members := make([]pack.EvalSetMember, n)
		for i := range members {
			members[i] = pack.EvalSetMember{Metric: "p/a"}
		}
		return &pack.EvalSetDoc{
			Metadata: pack.EvalSetMetadata{ID: "p/s"},
			Spec:     pack.EvalSetSpec{Members: members},
		}
	}

	if _, err := FromDoc(mk(maxEvalSetMembers)); err != nil {
		t.Fatalf("FromDoc rejected exactly maxEvalSetMembers (%d): %v", maxEvalSetMembers, err)
	}

	_, err := FromDoc(mk(maxEvalSetMembers + 1))
	if err == nil {
		t.Fatalf("FromDoc accepted %d members, want rejection (max %d)", maxEvalSetMembers+1, maxEvalSetMembers)
	}
	if !strings.Contains(err.Error(), "too many members") {
		t.Fatalf("error %q does not describe the member-count cap", err)
	}
}

func TestFromDoc_NonStringInputRejected(t *testing.T) {
	doc := &pack.EvalSetDoc{
		Metadata: pack.EvalSetMetadata{ID: "p/s"},
		Spec: pack.EvalSetSpec{
			Inputs:  map[string]any{"asset": 42},
			Members: []pack.EvalSetMember{{Metric: "p/a"}},
		},
	}
	if _, err := FromDoc(doc); err == nil {
		t.Fatal("FromDoc accepted non-string input value, want error")
	}
}

// TestFromDoc_NonStringValueRejectedNamesKey covers the design §6-§9 requirement
// that FromDoc rejects a non-string value in EITHER spec.inputs OR a member's
// spec.members[i].bind, and that the error NAMES the offending key so an author
// can locate it. The bind path is exercised here (the existing input-only test
// does not touch member bind conversion) and both paths assert the key appears
// in the message.
func TestFromDoc_NonStringValueRejectedNamesKey(t *testing.T) {
	tests := []struct {
		name    string
		doc     *pack.EvalSetDoc
		wantKey string
	}{
		{
			name: "non-string input value names key",
			doc: &pack.EvalSetDoc{
				Metadata: pack.EvalSetMetadata{ID: "p/s"},
				Spec: pack.EvalSetSpec{
					Inputs:  map[string]any{"asset": 42},
					Members: []pack.EvalSetMember{{Metric: "p/a"}},
				},
			},
			wantKey: "asset",
		},
		{
			name: "non-string bind value names key",
			doc: &pack.EvalSetDoc{
				Metadata: pack.EvalSetMetadata{ID: "p/s"},
				Spec: pack.EvalSetSpec{
					Members: []pack.EvalSetMember{
						{Metric: "p/a", Bind: map[string]any{"prompt": true}},
					},
				},
			},
			wantKey: "prompt",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := FromDoc(tc.doc)
			if err == nil {
				t.Fatalf("FromDoc accepted non-string value, want error")
			}
			if !strings.Contains(err.Error(), tc.wantKey) {
				t.Fatalf("error %q does not name offending key %q", err, tc.wantKey)
			}
		})
	}
}
