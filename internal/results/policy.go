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

package results

import "github.com/ghchinoy/mizan/internal/registry"

// RetentionPolicy decides, per input field, whether to store raw content inline
// or only a hash + reference (design §4.4). The ContentHash is stored regardless;
// this only governs the raw value.
type RetentionPolicy interface {
	ModeFor(in StoredInput) RetentionMode
}

// InlinePolicy stores every field inline (max reproducibility, max PII surface).
type InlinePolicy struct{}

// ModeFor always returns ModeInline.
func (InlinePolicy) ModeFor(StoredInput) RetentionMode { return ModeInline }

// ReferencePolicy stores every field by reference only (min PII; needs the asset
// to still exist to reproduce).
type ReferencePolicy struct{}

// ModeFor always returns ModeReference.
func (ReferencePolicy) ModeFor(StoredInput) RetentionMode { return ModeReference }

// HybridPolicy (the default) stores text inline and media (image/audio/video) by
// reference: text is small, high-value, low-PII; media bytes are never copied.
type HybridPolicy struct{}

// ModeFor returns ModeInline for text fields and ModeReference otherwise.
func (HybridPolicy) ModeFor(in StoredInput) RetentionMode {
	if in.Modality == registry.ModalityText {
		return ModeInline
	}
	return ModeReference
}

// NewHybridPolicy returns the default hybrid retention policy.
func NewHybridPolicy() RetentionPolicy { return HybridPolicy{} }

// PolicyFor maps a config string ("inline"|"reference"|"hybrid") to a
// RetentionPolicy. Empty or unrecognized values fall back to the hybrid default,
// matching cfg.ResultsRetention's default.
func PolicyFor(mode string) RetentionPolicy {
	switch mode {
	case string(ModeInline):
		return InlinePolicy{}
	case string(ModeReference):
		return ReferencePolicy{}
	default: // "hybrid", "" and any unknown value
		return HybridPolicy{}
	}
}
