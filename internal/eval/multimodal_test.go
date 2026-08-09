package eval

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	aiplatformpb "cloud.google.com/go/aiplatform/apiv1beta1/aiplatformpb"
	"google.golang.org/protobuf/proto"

	"github.com/ghchinoy/mizan/internal/asset"
	"github.com/ghchinoy/mizan/internal/registry"
)

// fakeStager is a network-free asset.Stager. It records the inputs it received
// and returns a canned result (or a gs:// pass-through), so the engine's native
// ContentMap materialization is testable without GCS.
type fakeStager struct {
	staged []asset.StageInput
	result asset.StageResult
	err    error
}

func (f *fakeStager) Stage(_ context.Context, in asset.StageInput) (asset.StageResult, error) {
	f.staged = append(f.staged, in)
	if f.err != nil {
		return asset.StageResult{}, f.err
	}
	if in.GCSUri != "" {
		// Pass-through: keep the URI, resolve MIME from the override or the fake.
		r := f.result
		r.GCSUri = in.GCSUri
		if r.MIME == "" {
			r.MIME = in.MIME
		}
		return r, nil
	}
	return f.result, nil
}

func okPointwiseResp() *aiplatformpb.EvaluateInstancesResponse {
	return &aiplatformpb.EvaluateInstancesResponse{
		EvaluationResults: &aiplatformpb.EvaluateInstancesResponse_PointwiseMetricResult{
			PointwiseMetricResult: &aiplatformpb.PointwiseMetricResult{
				Score:       proto.Float32(4),
				Explanation: "fine",
			},
		},
	}
}

// TestRunPointwiseLocalFileStaged proves a local FilePath is staged to gs://
// through the configured Stager and materialized as a native FileData Part.
func TestRunPointwiseLocalFileStaged(t *testing.T) {
	fs := &fakeStager{result: asset.StageResult{GCSUri: "gs://bkt/mizan-staging/deadbeef.png", MIME: "image/png"}}
	fc := &fakeClient{resp: okPointwiseResp()}
	eng := NewEngine(fc, "p", "us-central1", WithStager(fs))

	tmpl := pointwiseTemplate()
	tmpl.MetricPromptTemplate = "Describe {{response}}"
	res, err := eng.Run(context.Background(), tmpl, Instance{
		Fields: map[string]AssetRef{
			"response": {Modality: registry.ModalityImage, FilePath: "/local/cat.png"},
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Score == nil || *res.Score != 4 {
		t.Errorf("Score = %v, want 4", res.Score)
	}

	// The stager saw the local path.
	if len(fs.staged) != 1 || fs.staged[0].LocalPath != "/local/cat.png" {
		t.Fatalf("stager inputs = %+v, want one LocalPath=/local/cat.png", fs.staged)
	}

	// The request carries a ContentMap with the staged gs:// FileData.
	cm := fc.gotReq.GetPointwiseMetricInput().GetInstance().GetContentMapInstance()
	if cm == nil {
		t.Fatal("expected ContentMap instance")
	}
	fd := cm.GetValues()["response"].GetContents()[0].GetParts()[0].GetFileData()
	if fd == nil || fd.GetFileUri() != "gs://bkt/mizan-staging/deadbeef.png" || fd.GetMimeType() != "image/png" {
		t.Errorf("FileData = %+v, want staged gs:// image/png", fd)
	}
	// A text-only spec still carries the prompt.
	if got := fc.gotReq.GetPointwiseMetricInput().GetMetricSpec().GetMetricPromptTemplate(); got != "Describe {{response}}" {
		t.Errorf("prompt = %q", got)
	}
}

// TestRunPointwiseMixedTextAndMedia proves a text field and a media field coexist
// in one ContentMap (text -> text Part, media -> FileData Part).
func TestRunPointwiseMixedTextAndMedia(t *testing.T) {
	fs := &fakeStager{result: asset.StageResult{GCSUri: "gs://bkt/x.png", MIME: "image/png"}}
	fc := &fakeClient{resp: okPointwiseResp()}
	eng := NewEngine(fc, "p", "us-central1", WithStager(fs))

	tmpl := pointwiseTemplate()
	tmpl.MetricPromptTemplate = "Rate {{caption}} for image {{image}}"
	_, err := eng.Run(context.Background(), tmpl, Instance{
		Fields: map[string]AssetRef{
			"caption": {Modality: registry.ModalityText, Text: "a cat"},
			"image":   {Modality: registry.ModalityImage, FilePath: "/local/cat.png"},
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	cm := fc.gotReq.GetPointwiseMetricInput().GetInstance().GetContentMapInstance()
	if cm == nil {
		t.Fatal("expected ContentMap instance")
	}
	if txt := cm.GetValues()["caption"].GetContents()[0].GetParts()[0].GetText(); txt != "a cat" {
		t.Errorf("caption text Part = %q, want 'a cat'", txt)
	}
	if fd := cm.GetValues()["image"].GetContents()[0].GetParts()[0].GetFileData(); fd == nil {
		t.Error("image field should be a FileData Part")
	}
}

// TestRunPointwiseGCSNoMIMEUnknownExt proves a gs:// asset whose MIME cannot be
// resolved (no override, unknown extension, no stager) fails clearly rather than
// shipping a droppable type.
func TestRunPointwiseGCSNoMIMEUnknownExt(t *testing.T) {
	fc := &fakeClient{}
	eng := NewEngine(fc, "p", "us-central1")
	tmpl := pointwiseTemplate()
	tmpl.MetricPromptTemplate = "Describe {{response}}"
	_, err := eng.Run(context.Background(), tmpl, Instance{
		Fields: map[string]AssetRef{
			"response": {Modality: registry.ModalityImage, GCSUri: "gs://bkt/object-without-extension"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "MIME") {
		t.Fatalf("want a MIME error, got %v", err)
	}
	if fc.gotReq != nil {
		t.Error("client should not be called when MIME cannot be resolved")
	}
}

// TestRunPointwiseMultimodalMissingVar proves var/instance-key parity holds on
// the multimodal path: a missing referenced variable errors before the API call.
func TestRunPointwiseMultimodalMissingVar(t *testing.T) {
	fs := &fakeStager{result: asset.StageResult{GCSUri: "gs://bkt/x.png", MIME: "image/png"}}
	fc := &fakeClient{}
	eng := NewEngine(fc, "p", "us-central1", WithStager(fs))
	tmpl := pointwiseTemplate()
	tmpl.MetricPromptTemplate = "Compare {{image}} and {{caption}}"
	_, err := eng.Run(context.Background(), tmpl, Instance{
		Fields: map[string]AssetRef{
			"image": {Modality: registry.ModalityImage, FilePath: "/local/cat.png"},
			// caption missing
		},
	})
	if err == nil || !strings.Contains(err.Error(), "caption") {
		t.Fatalf("want missing-variable error naming 'caption', got %v", err)
	}
	if fc.gotReq != nil {
		t.Error("client should not be called when a variable is missing")
	}
}

// TestRunPointwiseStagerError proves the native multimodal path propagates a
// Stager failure as a clear, wrapped error before any API call (test-4 gap 1:
// the stager.Stage error branch).
func TestRunPointwiseStagerError(t *testing.T) {
	fs := &fakeStager{err: errors.New("boom: upload failed")}
	fc := &fakeClient{}
	eng := NewEngine(fc, "p", "us-central1", WithStager(fs))

	tmpl := pointwiseTemplate()
	tmpl.MetricPromptTemplate = "Describe {{response}}"
	_, err := eng.Run(context.Background(), tmpl, Instance{
		Fields: map[string]AssetRef{
			"response": {Modality: registry.ModalityImage, FilePath: "/local/cat.png"},
		},
	})
	if err == nil {
		t.Fatal("expected an error when the stager fails")
	}
	if !strings.Contains(err.Error(), "stage asset") || !strings.Contains(err.Error(), "boom") {
		t.Errorf("error should wrap the stager failure with 'stage asset' context: %v", err)
	}
	if fc.gotReq != nil {
		t.Error("client must not be called when staging fails")
	}
}

// TestToNativeFileDataPartNoURIOrText proves the converter's default error branch:
// a non-text ref with neither a FilePath nor a gs:// URI is rejected (test-4 gap
// 2).
func TestToNativeFileDataPartNoURIOrText(t *testing.T) {
	eng := NewEngine(&fakeClient{}, "p", "us-central1")
	_, err := eng.toNativeFileDataPart(context.Background(), AssetRef{Modality: registry.ModalityImage})
	if err == nil {
		t.Fatal("expected an error for a media ref with no file path or gs:// URI")
	}
	if !strings.Contains(err.Error(), "no file path or gs:// URI") {
		t.Errorf("error = %v, want it to name the missing file path / gs:// URI", err)
	}
}

// TestToGenaiInlinePartHardening covers the carried-forward WI-5 security fixes:
// an oversized file and a non-regular file are both rejected before the bytes are
// read into memory.
func TestToGenaiInlinePartHardening(t *testing.T) {
	dir := t.TempDir()

	// Oversize file: create a sparse file just past the cap (cheap; Size() is
	// checked before any read).
	big := filepath.Join(dir, "big.bin")
	f, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxInlineBytes + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, err := toGenaiInlinePart(AssetRef{Modality: registry.ModalityImage, FilePath: big}); err == nil {
		t.Error("oversize file should be rejected")
	} else if !strings.Contains(err.Error(), "cap") {
		t.Errorf("oversize error should mention the cap: %v", err)
	}

	// Non-regular file: a directory is not a regular file.
	if _, err := toGenaiInlinePart(AssetRef{Modality: registry.ModalityImage, FilePath: dir}); err == nil {
		t.Error("directory should be rejected")
	} else if !strings.Contains(err.Error(), "regular file") {
		t.Errorf("non-regular error should say 'regular file': %v", err)
	}

	// A small regular file still works (regression guard for the happy path).
	small := filepath.Join(dir, "ok.txt")
	if err := os.WriteFile(small, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := toGenaiInlinePart(AssetRef{Modality: registry.ModalityImage, FilePath: small})
	if err != nil {
		t.Fatalf("small file: %v", err)
	}
	if p.InlineData == nil || string(p.InlineData.Data) != "hello" {
		t.Errorf("inline data = %+v, want 'hello'", p.InlineData)
	}
}
