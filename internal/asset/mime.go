// Package asset handles ingestion of local files and GCS-staged assets into
// the modality/MIME representation the eval engine needs.
package asset

import (
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/ghchinoy/mizan/internal/registry"
)

// sniffLen is the number of leading bytes DetectMIMEFile reads for content
// sniffing. http.DetectContentType only ever inspects the first 512 bytes, and
// the container magic numbers we check live well within that window.
const sniffLen = 512

// extMIME is a curated extension -> MIME map for the media formats Mizan stages
// for native multimodal eval. Because a mismatched MIME silently drops the
// asset on the native path (spike-core), correctness of the TOP-LEVEL type
// (image/audio/video) is load-bearing, so we do not rely on the host's
// mime.types database for these — we pin them here.
var extMIME = map[string]string{
	// image
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".png":  "image/png",
	".gif":  "image/gif",
	".webp": "image/webp",
	// audio
	".mp3":  "audio/mpeg",
	".mpga": "audio/mpeg",
	".wav":  "audio/wav",
	".ogg":  "audio/ogg",
	".oga":  "audio/ogg",
	".flac": "audio/flac",
	".m4a":  "audio/mp4",
	".aac":  "audio/aac",
	// video
	".mp4":  "video/mp4",
	".m4v":  "video/mp4",
	".webm": "video/webm",
	".mov":  "video/quicktime",
	".mpeg": "video/mpeg",
	".mpg":  "video/mpeg",
}

// DetectMIME returns a best-effort MIME type for the given filename and
// (optional) leading bytes. Content sniffing takes precedence over the
// extension because a wrong top-level type causes the native eval path to drop
// the asset silently (spike-core); the extension is only a fallback when the
// bytes are inconclusive.
func DetectMIME(filename string, head []byte) string {
	if ct := sniffMIME(head); ct != "" {
		return ct
	}
	if ext := strings.ToLower(filepath.Ext(filename)); ext != "" {
		if ct, ok := extMIME[ext]; ok {
			return ct
		}
		if ct := mime.TypeByExtension(ext); ct != "" {
			return normalizeMIME(ct)
		}
	}
	return "application/octet-stream"
}

// DetectMIMEFile opens path, reads its leading bytes, and returns the detected
// MIME type. It combines a content sniff of the file header with an extension
// fallback, so it works for both correctly- and misleadingly-named files.
func DetectMIMEFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("asset: open %q: %w", path, err)
	}
	defer f.Close()

	head := make([]byte, sniffLen)
	n, err := f.Read(head)
	if err != nil && n == 0 {
		// Empty file (io.EOF with n==0): fall back to the extension only.
		return DetectMIME(path, nil), nil
	}
	return DetectMIME(path, head[:n]), nil
}

// sniffMIME inspects magic bytes to identify the media container types that
// http.DetectContentType handles poorly or not at all (wav, ogg, flac, mov,
// webm), falling back to http.DetectContentType for the rest. It returns "" if
// the bytes are inconclusive so callers can try the extension.
func sniffMIME(head []byte) string {
	if len(head) == 0 {
		return ""
	}

	switch {
	case len(head) >= 3 && head[0] == 0xFF && head[1] == 0xD8 && head[2] == 0xFF:
		return "image/jpeg"
	case len(head) >= 8 && string(head[:8]) == "\x89PNG\r\n\x1a\n":
		return "image/png"
	case len(head) >= 6 && (string(head[:6]) == "GIF87a" || string(head[:6]) == "GIF89a"):
		return "image/gif"
	case len(head) >= 4 && string(head[:4]) == "OggS":
		return "audio/ogg"
	case len(head) >= 4 && string(head[:4]) == "fLaC":
		return "audio/flac"
	case len(head) >= 3 && string(head[:3]) == "ID3":
		return "audio/mpeg"
	case len(head) >= 2 && head[0] == 0xFF && (head[1]&0xE0) == 0xE0:
		// MPEG audio frame sync (MP3 without ID3 tag).
		return "audio/mpeg"
	case len(head) >= 4 && head[0] == 0x1A && head[1] == 0x45 && head[2] == 0xDF && head[3] == 0xA3:
		// EBML header: Matroska / WebM.
		return "video/webm"
	case len(head) >= 12 && string(head[:4]) == "RIFF":
		switch string(head[8:12]) {
		case "WEBP":
			return "image/webp"
		case "WAVE":
			return "audio/wav"
		case "AVI ":
			return "video/x-msvideo"
		}
	case len(head) >= 12 && string(head[4:8]) == "ftyp":
		// ISO Base Media File Format (MP4 / QuickTime / M4A). Distinguish by
		// the major brand.
		brand := strings.TrimSpace(string(head[8:12]))
		switch {
		case strings.HasPrefix(brand, "qt"):
			return "video/quicktime"
		case strings.HasPrefix(brand, "M4A"):
			return "audio/mp4"
		default:
			return "video/mp4"
		}
	}

	if ct := http.DetectContentType(head); ct != "application/octet-stream" &&
		!strings.HasPrefix(ct, "text/plain") {
		return normalizeMIME(ct)
	}
	return ""
}

// normalizeMIME canonicalizes a few MIME aliases to the forms the eval service
// and our modality mapping expect, and strips parameters (e.g. "; charset=…").
func normalizeMIME(ct string) string {
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	switch ct {
	case "audio/wave", "audio/x-wav":
		return "audio/wav"
	case "audio/x-flac":
		return "audio/flac"
	case "audio/mp3":
		return "audio/mpeg"
	case "video/x-m4v":
		return "video/mp4"
	}
	return ct
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
