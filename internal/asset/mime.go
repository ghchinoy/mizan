// Package asset handles ingestion of local files and GCS-staged assets into
// the modality/MIME representation the eval engine needs.
package asset

import (
	"mime"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/ghchinoy/mizan/internal/registry"
)

// DetectMIME returns a best-effort MIME type for the given filename and
// (optional) leading bytes, using http.DetectContentType with an extension
// fallback for containers it handles poorly.
func DetectMIME(filename string, head []byte) string {
	if len(head) > 0 {
		if ct := http.DetectContentType(head); ct != "application/octet-stream" {
			return ct
		}
	}
	if ext := filepath.Ext(filename); ext != "" {
		if ct := mime.TypeByExtension(ext); ct != "" {
			return ct
		}
	}
	return "application/octet-stream"
}

// ModalityForMIME maps a MIME type to a registry.Modality.
func ModalityForMIME(mimeType string) registry.Modality {
	switch {
	case strings.HasPrefix(mimeType, "image/"):
		return registry.ModalityImage
	case strings.HasPrefix(mimeType, "audio/"):
		return registry.ModalityAudio
	case strings.HasPrefix(mimeType, "video/"):
		return registry.ModalityVideo
	default:
		return registry.ModalityText
	}
}
