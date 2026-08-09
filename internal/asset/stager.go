package asset

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// Stager stages a local asset to GCS (if needed) and returns a gs:// URI plus
// the resolved MIME type. An input already given as a gs:// URI is returned
// as-is (MIME detected from extension / provided). Implementations must be safe
// for concurrent use.
type Stager interface {
	Stage(ctx context.Context, in StageInput) (StageResult, error)
}

// StageInput describes a single asset to stage. Exactly one of LocalPath or
// GCSUri must be set.
type StageInput struct {
	LocalPath string // local file to upload (mutually exclusive with GCSUri)
	GCSUri    string // already gs://... -> returned as-is
	MIME      string // optional explicit override; else detected
}

// StageResult is the staged asset: a gs:// URI the native eval path can consume
// and the resolved, top-level-accurate MIME type.
type StageResult struct {
	GCSUri string // gs://bucket/object
	MIME   string // resolved top-level-accurate MIME
}

// ErrNoBucket is returned when a staging operation that requires an upload is
// attempted without a configured staging bucket. WI-4 surfaces this to the user
// when a multimodal eval is attempted with no StagingBucket set.
var ErrNoBucket = errors.New("asset: no staging bucket configured; set StagingBucket (MIZAN_STAGING_BUCKET / GENMEDIA_BUCKET) to a gs:// bucket for multimodal eval")

// validate checks the mutual-exclusivity contract of a StageInput and reports a
// clear error otherwise.
func (in StageInput) validate() error {
	hasLocal := strings.TrimSpace(in.LocalPath) != ""
	hasGCS := strings.TrimSpace(in.GCSUri) != ""
	switch {
	case hasLocal && hasGCS:
		return fmt.Errorf("asset: StageInput has both LocalPath (%q) and GCSUri (%q); set exactly one", in.LocalPath, in.GCSUri)
	case !hasLocal && !hasGCS:
		return errors.New("asset: StageInput has neither LocalPath nor GCSUri; set exactly one")
	}
	if hasGCS && !strings.HasPrefix(in.GCSUri, "gs://") {
		return fmt.Errorf("asset: GCSUri %q is not a gs:// URI", in.GCSUri)
	}
	return nil
}

// resolveMIME returns the explicit override when provided, otherwise a detected
// MIME type. name is the file name (local path or gs:// object) used for the
// extension fallback; head may be nil when no bytes are available (e.g. a
// gs:// pass-through), in which case detection relies on the extension.
//
// The override is treated as an operator-trusted hint, but it is not accepted
// blindly: when content sniffing yields an unambiguous type whose TOP-LEVEL
// class (image/audio/video) conflicts with the override, the sniffed type wins.
// A wrong top-level type silently drops the asset on the native path
// (spike-core), so an override (which WI-4 may source from a dataset) must not
// be able to force a droppable type over what the bytes clearly say. When the
// sniff is inconclusive, the override is honored.
func resolveMIME(override, name string, head []byte) string {
	if m := strings.TrimSpace(override); m != "" {
		m = normalizeMIME(m)
		if sniffed := sniffMIME(head, strings.ToLower(filepath.Ext(name))); sniffed != "" &&
			topLevelType(sniffed) != topLevelType(m) {
			return sniffed
		}
		return m
	}
	return DetectMIME(name, head)
}

// topLevelType returns the portion of a MIME type before the "/" (e.g. "audio"
// for "audio/mp4"), used to compare only the load-bearing top-level class.
func topLevelType(mimeType string) string {
	if i := strings.IndexByte(mimeType, '/'); i >= 0 {
		return mimeType[:i]
	}
	return mimeType
}

// gcsObjectName returns the object name portion of a gs:// URI (used for MIME
// detection from the extension of a pass-through URI).
func gcsObjectName(gcsURI string) string {
	trimmed := strings.TrimPrefix(gcsURI, "gs://")
	if i := strings.IndexByte(trimmed, '/'); i >= 0 {
		return trimmed[i+1:]
	}
	return filepath.Base(trimmed)
}
