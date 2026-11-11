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

package mizan

import (
	"fmt"
	"slices"
	"strings"

	"google.golang.org/genai"

	"github.com/ghchinoy/mizan/internal/asset"
	"github.com/ghchinoy/mizan/internal/eval"
	"github.com/ghchinoy/mizan/internal/registry"
)

// Asset represents a single text, media, or N-variation input bound to a
// template placeholder in an [Instance].
type Asset struct {
	ref eval.AssetRef
}

// Variation binds a stable identifier and optional short description to an
// [Asset] inside a multi-asset [Variations] placeholder.
type Variation struct {
	ID          string
	Asset       Asset
	Description string
}

// Text constructs a UTF-8 text [Asset].
func Text(s string) Asset {
	return Asset{ref: eval.AssetRef{
		Modality: registry.ModalityText,
		Text:     s,
	}}
}

// Bytes constructs an inline media [Asset] from in-memory bytes and an optional
// MIME type (when mimeType is empty, Mizan sniffs the container header). Total
// inline bytes across a single evaluation call must not exceed 20 MiB.
func Bytes(data []byte, mimeType string) Asset {
	trimmed := strings.TrimSpace(mimeType)
	resolved := trimmed
	if resolved == "" {
		resolved = asset.DetectMIME("", data)
	}
	return Asset{ref: eval.AssetRef{
		Modality: asset.ModalityForMIME(resolved),
		Bytes:    slices.Clone(data),
		MimeType: trimmed,
	}}
}

// URI constructs a remote media [Asset] from a gs:// or SSRF-validated https://
// URI and an optional MIME type (when mimeType is empty, Mizan infers it from
// the URI path extension).
func URI(rawURI, mimeType string) Asset {
	trimmedURI := strings.TrimSpace(rawURI)
	trimmedMIME := strings.TrimSpace(mimeType)
	resolved := trimmedMIME
	if resolved == "" {
		resolved = asset.DetectMIME(trimmedURI, nil)
	}
	return Asset{ref: eval.AssetRef{
		Modality: asset.ModalityForMIME(resolved),
		URI:      trimmedURI,
		MimeType: trimmedMIME,
	}}
}

// File constructs a local-file media [Asset]. For confused-deputy safety, local
// file reads are disabled by default on [Evaluator] unless opted in via
// [WithLocalFileAccess].
func File(localPath string) Asset {
	return Asset{ref: eval.AssetRef{
		FilePath: localPath,
	}}
}

// FromGenaiPart converts a caller-constructed [genai.Part] (Text, InlineData, or
// FileData, preserving VideoMetadata clip offsets/FPS and per-part
// MediaResolution) into a validated [Asset].
func FromGenaiPart(p *genai.Part) (Asset, error) {
	if p == nil {
		return Asset{}, fmt.Errorf("mizan: genai.Part is nil")
	}
	if p.VideoMetadata != nil {
		if err := eval.ValidateVideoMetadata(p.VideoMetadata); err != nil {
			return Asset{}, fmt.Errorf("mizan: %w", err)
		}
	}
	var vm *genai.VideoMetadata
	if p.VideoMetadata != nil {
		cp := *p.VideoMetadata
		if p.VideoMetadata.FPS != nil {
			fps := *p.VideoMetadata.FPS
			cp.FPS = &fps
		}
		vm = &cp
	}
	var pr *genai.PartMediaResolution
	if p.MediaResolution != nil {
		cp := *p.MediaResolution
		if p.MediaResolution.NumTokens != nil {
			nt := *p.MediaResolution.NumTokens
			cp.NumTokens = &nt
		}
		pr = &cp
	}

	switch {
	case p.InlineData != nil:
		if len(p.InlineData.Data) == 0 {
			return Asset{}, fmt.Errorf("mizan: genai.Part InlineData has empty bytes")
		}
		mime := strings.TrimSpace(p.InlineData.MIMEType)
		if mime == "" {
			mime = asset.DetectMIME("", p.InlineData.Data)
		}
		if mime == "" || mime == "application/octet-stream" {
			return Asset{}, fmt.Errorf("mizan: genai.Part InlineData requires a media MIME type")
		}
		return Asset{ref: eval.AssetRef{
			Modality:       asset.ModalityForMIME(mime),
			Bytes:          slices.Clone(p.InlineData.Data),
			MimeType:       mime,
			VideoMeta:      vm,
			PartResolution: pr,
		}}, nil
	case p.FileData != nil:
		rawURI := strings.TrimSpace(p.FileData.FileURI)
		if err := eval.ValidateMediaURI(rawURI, nil, nil); err != nil {
			return Asset{}, fmt.Errorf("mizan: %w", err)
		}
		mime := strings.TrimSpace(p.FileData.MIMEType)
		if mime == "" {
			mime = asset.DetectMIME(rawURI, nil)
		}
		if mime == "" || mime == "application/octet-stream" {
			return Asset{}, fmt.Errorf("mizan: genai.Part FileData %q requires a media MIME type", rawURI)
		}
		return Asset{ref: eval.AssetRef{
			Modality:       asset.ModalityForMIME(mime),
			URI:            rawURI,
			MimeType:       mime,
			VideoMeta:      vm,
			PartResolution: pr,
		}}, nil
	case p.Text != "":
		return Asset{ref: eval.AssetRef{
			Modality: registry.ModalityText,
			Text:     p.Text,
		}}, nil
	default:
		return Asset{}, fmt.Errorf("mizan: unsupported genai.Part (expected non-empty Text, InlineData, or FileData)")
	}
}

// Variations bundles an ordered slice of labeled [Variation] items onto a single
// template placeholder (up to [eval.MaxVariations] = 32 items).
func Variations(items ...Variation) Asset {
	refs := make([]eval.AssetRef, len(items))
	var groupMod registry.Modality
	for i, it := range items {
		r := it.Asset.ref
		r.Label = it.ID
		r.Description = it.Description
		refs[i] = r
		if i == 0 {
			groupMod = r.Modality
		} else if r.Modality != "" && groupMod != r.Modality {
			groupMod = ""
		}
	}
	return Asset{ref: eval.AssetRef{
		Modality: groupMod,
		Items:    refs,
	}}
}

// WithVideoMetadata returns a copy of a decorated with clip StartOffset,
// EndOffset, and/or FPS sampling metadata for video evaluation.
func (a Asset) WithVideoMetadata(meta *genai.VideoMetadata) Asset {
	cp := a
	if meta == nil {
		cp.ref.VideoMeta = nil
		return cp
	}
	vm := *meta
	if meta.FPS != nil {
		fps := *meta.FPS
		vm.FPS = &fps
	}
	cp.ref.VideoMeta = &vm
	return cp
}

func toEvalInstance(inst Instance) eval.Instance {
	if len(inst) == 0 {
		return eval.Instance{}
	}
	fields := make(map[string]eval.AssetRef, len(inst))
	for k, v := range inst {
		fields[k] = v.ref
	}
	return eval.Instance{Fields: fields}
}
