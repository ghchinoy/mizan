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

package eval

// field_validation_gaps_test.go extends field_validation_test.go (FIX-1) to the
// coverage the first pass left open:
//
//   - the RUBRIC kind (native path) — unknown-field error AND a payload-proof
//     happy path — which the original file omitted entirely;
//   - MULTIPLE placeholders — an all-supplied happy path that proves BOTH values
//     reach the payload, and a single unknown extra among valid keys that names
//     ONLY the offender;
//   - MULTIMODAL field keys (--file / --gcs) validated against placeholders, not
//     just --field text: an unknown media key must hard-error BEFORE any staging
//     or API call;
//   - no-false-positive guards: a no-placeholder template with NO fields must NOT
//     error, and a truly empty prompt template defers to the per-kind guard.
//
// All assertions pin the error message to the offending key and the template id,
// and the happy paths prove the supplied value reaches the built request payload.

import (
	"context"
	"strings"
	"testing"

	"github.com/ghchinoy/mizan/internal/asset"
	"github.com/ghchinoy/mizan/internal/registry"
)

// --- Rubric kind: the third native kind, absent from the first pass. ---

// TestValidateFields_UnknownFieldRubricNative proves the FIX-1 guard fires for a
// rubric template too: an extra/unknown field is rejected before dispatch, and
// the message names the offending key and the template id.
func TestValidateFields_UnknownFieldRubricNative(t *testing.T) {
	fc := &fakeClient{}
	eng := NewEngine(fc, "p", "us-central1")

	_, err := eng.Run(context.Background(), rubricTemplate(), Instance{
		Fields: map[string]AssetRef{
			"copy":  {Modality: registry.ModalityText, Text: "Buy now."},
			"bogus": {Modality: registry.ModalityText, Text: "ignored?"},
		},
	})
	if err == nil {
		t.Fatal("want error for an extra unknown field on the rubric path, got nil (silent drop)")
	}
	if !strings.Contains(err.Error(), "bogus") || !strings.Contains(err.Error(), rubricTemplate().ID) {
		t.Errorf("error must name the unknown key and the template: %v", err)
	}
	if fc.gotReq != nil {
		t.Error("client must NOT be called when an extra rubric field would be dropped")
	}
}

// TestValidateFields_HappyPathRubric proves a correct rubric template+field is
// accepted AND that the field's value provably reaches the request payload (the
// rubric path materializes a JsonInstance via runNativePointwise).
func TestValidateFields_HappyPathRubric(t *testing.T) {
	fc := &fakeClient{resp: pointwiseResp(3, "clear")}
	eng := NewEngine(fc, "p", "us-central1")

	_, err := eng.Run(context.Background(), rubricTemplate(), Instance{
		Fields: map[string]AssetRef{"copy": {Modality: registry.ModalityText, Text: "Buy now, save big."}},
	})
	if err != nil {
		t.Fatalf("valid rubric template+field rejected: %v", err)
	}
	if fc.gotReq == nil {
		t.Fatal("client should be called for a valid rubric template+field")
	}
	ji := fc.gotReq.GetPointwiseMetricInput().GetInstance().GetJsonInstance()
	if !strings.Contains(ji, `"copy"`) || !strings.Contains(ji, "Buy now, save big.") {
		t.Errorf("rubric JsonInstance %q does not carry the supplied field value", ji)
	}
}

// --- Multiple placeholders: parity + precise offender naming. ---

// multiPlaceholderTemplate references two distinct {{placeholders}} so the
// all-supplied and one-unknown-extra cases can be exercised.
func multiPlaceholderTemplate() registry.MetricTemplate {
	t := pointwiseTemplate()
	t.ID = "test/two-field"
	t.MetricPromptTemplate = "Compare question {{question}} with answer {{answer}}."
	return t
}

// TestValidateFields_MultiplePlaceholders_AllSuppliedHappyPath proves that when a
// template has more than one placeholder and every one is supplied, the run is
// accepted and BOTH values reach the request payload (no over-eager rejection).
func TestValidateFields_MultiplePlaceholders_AllSuppliedHappyPath(t *testing.T) {
	fc := &fakeClient{resp: pointwiseResp(1, "ok")}
	eng := NewEngine(fc, "p", "us-central1")

	_, err := eng.Run(context.Background(), multiPlaceholderTemplate(), Instance{
		Fields: map[string]AssetRef{
			"question": {Modality: registry.ModalityText, Text: "What is 2+2?"},
			"answer":   {Modality: registry.ModalityText, Text: "Four."},
		},
	})
	if err != nil {
		t.Fatalf("all placeholders supplied but run rejected: %v", err)
	}
	ji := fc.gotReq.GetPointwiseMetricInput().GetInstance().GetJsonInstance()
	if !strings.Contains(ji, "What is 2+2?") || !strings.Contains(ji, "Four.") {
		t.Errorf("JsonInstance %q must carry BOTH supplied placeholder values", ji)
	}
}

// TestValidateFields_MultiplePlaceholders_OneUnknownExtra proves that a single
// unknown extra among otherwise-valid keys is rejected, and the message names
// ONLY the offender (not the valid keys) plus the template id.
func TestValidateFields_MultiplePlaceholders_OneUnknownExtra(t *testing.T) {
	fc := &fakeClient{}
	eng := NewEngine(fc, "p", "us-central1")

	_, err := eng.Run(context.Background(), multiPlaceholderTemplate(), Instance{
		Fields: map[string]AssetRef{
			"question": {Modality: registry.ModalityText, Text: "What is 2+2?"},
			"answer":   {Modality: registry.ModalityText, Text: "Four."},
			"bogus":    {Modality: registry.ModalityText, Text: "ignored?"},
		},
	})
	if err == nil {
		t.Fatal("want error for one unknown extra among valid placeholders, got nil")
	}
	// The unknown set is emitted as a slice; it must contain ONLY bogus.
	if !strings.Contains(err.Error(), "[bogus]") {
		t.Errorf("error must flag ONLY the offending key as unknown ([bogus]): %v", err)
	}
	if !strings.Contains(err.Error(), multiPlaceholderTemplate().ID) {
		t.Errorf("error must name the template id: %v", err)
	}
	// The known placeholders should be surfaced as guidance, not flagged as unknown.
	if !strings.Contains(err.Error(), "question") || !strings.Contains(err.Error(), "answer") {
		t.Errorf("error should list the known placeholders as guidance: %v", err)
	}
	if fc.gotReq != nil {
		t.Error("client must NOT be called when an extra field would be dropped")
	}
}

// --- Multimodal: --file / --gcs keys are validated too, not just --field text. ---

// TestValidateFields_MultimodalFileUnknownKey proves a media field supplied by
// local path (--file) with a key matching no placeholder hard-errors BEFORE any
// staging or API call — the key check is modality-agnostic.
func TestValidateFields_MultimodalFileUnknownKey(t *testing.T) {
	fs := &fakeStager{result: asset.StageResult{GCSUri: "gs://bkt/x.png", MIME: "image/png"}}
	fc := &fakeClient{}
	eng := NewEngine(fc, "p", "us-central1", WithStager(fs))

	tmpl := pointwiseTemplate()
	tmpl.MetricPromptTemplate = "Describe {{response}}"

	_, err := eng.Run(context.Background(), tmpl, Instance{
		Fields: map[string]AssetRef{
			// Misspelled key on a --file media asset.
			"respons": {Modality: registry.ModalityImage, FilePath: "/local/cat.png"},
		},
	})
	if err == nil {
		t.Fatal("want error for an unknown --file media field key, got nil (silent drop)")
	}
	if !strings.Contains(err.Error(), "respons") || !strings.Contains(err.Error(), tmpl.ID) {
		t.Errorf("error must name the offending media key and the template: %v", err)
	}
	if len(fs.staged) != 0 {
		t.Errorf("stager must NOT be called when the media field cannot reach the judge; staged=%+v", fs.staged)
	}
	if fc.gotReq != nil {
		t.Error("client must NOT be called for an unknown media field key")
	}
}

// TestValidateFields_MultimodalGCSUnknownKey proves the same for a pre-staged
// gs:// asset (--gcs): an unknown key hard-errors before dispatch.
func TestValidateFields_MultimodalGCSUnknownKey(t *testing.T) {
	fc := &fakeClient{}
	eng := NewEngine(fc, "p", "us-central1")

	tmpl := pointwiseTemplate()
	tmpl.MetricPromptTemplate = "Describe {{response}}"

	_, err := eng.Run(context.Background(), tmpl, Instance{
		Fields: map[string]AssetRef{
			"bogus": {Modality: registry.ModalityImage, GCSUri: "gs://bkt/cat.png", MimeType: "image/png"},
		},
	})
	if err == nil {
		t.Fatal("want error for an unknown --gcs media field key, got nil")
	}
	if !strings.Contains(err.Error(), "bogus") || !strings.Contains(err.Error(), tmpl.ID) {
		t.Errorf("error must name the offending gcs key and the template: %v", err)
	}
	if fc.gotReq != nil {
		t.Error("client must NOT be called for an unknown --gcs media field key")
	}
}

// TestValidateFields_MultimodalHappyPathMediaKeyReachesPayload proves the guard
// does NOT over-reject a correctly-keyed media field: it passes validation, is
// staged, and reaches the ContentMap payload.
func TestValidateFields_MultimodalHappyPathMediaKeyReachesPayload(t *testing.T) {
	fs := &fakeStager{result: asset.StageResult{GCSUri: "gs://bkt/staged/cat.png", MIME: "image/png"}}
	fc := &fakeClient{resp: pointwiseResp(4, "fine")}
	eng := NewEngine(fc, "p", "us-central1", WithStager(fs))

	tmpl := pointwiseTemplate()
	tmpl.MetricPromptTemplate = "Describe {{response}}"

	_, err := eng.Run(context.Background(), tmpl, Instance{
		Fields: map[string]AssetRef{
			"response": {Modality: registry.ModalityImage, FilePath: "/local/cat.png"},
		},
	})
	if err != nil {
		t.Fatalf("valid media field rejected: %v", err)
	}
	cm := fc.gotReq.GetPointwiseMetricInput().GetInstance().GetContentMapInstance()
	if cm == nil {
		t.Fatal("expected a ContentMap instance for a media field")
	}
	fd := cm.GetValues()["response"].GetContents()[0].GetParts()[0].GetFileData()
	if fd == nil || fd.GetFileUri() != "gs://bkt/staged/cat.png" {
		t.Errorf("staged media did not reach the payload under the placeholder key: %+v", fd)
	}
}

// --- No false positives: valid empty/absent-field configurations must NOT error. ---

// TestValidateFields_NoPlaceholderNoFields_NoError proves the guard does NOT fire
// when a template has no {{placeholders}} AND no fields are supplied: there is no
// value to drop, so this is a legitimate (if unusual) configuration, not a bug.
func TestValidateFields_NoPlaceholderNoFields_NoError(t *testing.T) {
	fc := &fakeClient{resp: pointwiseResp(1, "ok")}
	eng := NewEngine(fc, "p", "us-central1")

	tmpl := pointwiseTemplate()
	tmpl.MetricPromptTemplate = "Rate how concise the response is from 0 to 1." // no {{...}}, no fields

	_, err := eng.Run(context.Background(), tmpl, Instance{Fields: map[string]AssetRef{}})
	if err != nil {
		t.Fatalf("no-placeholder template with NO fields must not error (false positive): %v", err)
	}
	if fc.gotReq == nil {
		t.Fatal("client should be called: nothing is dropped when no fields are supplied")
	}
}

// TestValidateFields_NilFieldsNoPlaceholder_NoError is the nil-map twin of the
// above: a nil Fields map with a no-placeholder template must also not error.
func TestValidateFields_NilFieldsNoPlaceholder_NoError(t *testing.T) {
	fc := &fakeClient{resp: pointwiseResp(1, "ok")}
	eng := NewEngine(fc, "p", "us-central1")

	tmpl := pointwiseTemplate()
	tmpl.MetricPromptTemplate = "Rate the response overall." // no {{...}}

	if _, err := eng.Run(context.Background(), tmpl, Instance{}); err != nil {
		t.Fatalf("nil Fields with a no-placeholder template must not error: %v", err)
	}
}

// TestValidateFields_EmptyPromptDefersToPerKind documents the deliberate FIX-1
// precedence: a TRULY empty MetricPromptTemplate skips the field-validation guard
// (which would report the less-precise "no placeholders") and defers to the
// per-kind path's own "empty metric prompt template" diagnosis.
func TestValidateFields_EmptyPromptDefersToPerKind(t *testing.T) {
	fc := &fakeClient{}
	eng := NewEngine(fc, "p", "us-central1")

	tmpl := pointwiseTemplate()
	tmpl.MetricPromptTemplate = "" // empty prompt, no fields

	_, err := eng.Run(context.Background(), tmpl, Instance{})
	if err == nil {
		t.Fatal("want the per-kind empty-prompt error, got nil")
	}
	if !strings.Contains(err.Error(), "empty metric prompt template") {
		t.Errorf("empty prompt should surface the precise per-kind error, not the generic no-placeholder one: %v", err)
	}
}
