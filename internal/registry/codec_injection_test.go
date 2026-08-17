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

package registry

import "testing"

// sentinelCodec is a distinctive Codec used only to prove the WithCodec
// injection MECHANISM actually replaces the default. Its Ext() is a value the
// default YAMLCodec never returns, so a test that observes it can distinguish
// "injection took effect" from "the default happened to be YAML anyway".
type sentinelCodec struct{}

func (sentinelCodec) Marshal(*MetricTemplate) ([]byte, error)   { return nil, nil }
func (sentinelCodec) Unmarshal([]byte) (*MetricTemplate, error) { return nil, nil }
func (sentinelCodec) Ext() string                               { return "sentinel-ext" }

// TestNewService_CodecInjectionHasTeeth proves WithCodec genuinely overrides the
// default codec — the discriminating counterpart to the wire smoke check
// (which cannot detect a dropped WithCodec because NewService defaults to
// YAMLCodec, GAP-1). Without WithCodec the Service must default to YAMLCodec;
// WITH WithCodec(sentinel) the Service must expose the sentinel, not the default.
func TestNewService_CodecInjectionHasTeeth(t *testing.T) {
	// (1) Default path: no WithCodec => YAMLCodec (Ext "yaml").
	def := NewService(nil)
	if _, ok := def.Codec().(YAMLCodec); !ok {
		t.Errorf("NewService(nil) default codec = %T, want YAMLCodec", def.Codec())
	}
	if got := def.Codec().Ext(); got != "yaml" {
		t.Errorf("default codec Ext() = %q, want %q", got, "yaml")
	}

	// (2) Injected path: WithCodec(sentinel) => the sentinel is actually used,
	// NOT the default. If WithCodec were a no-op the Ext would still be "yaml"
	// and this would fail — that is the teeth the wire test lacks.
	inj := NewService(nil, WithCodec(sentinelCodec{}))
	if _, ok := inj.Codec().(sentinelCodec); !ok {
		t.Errorf("NewService(WithCodec(sentinel)) codec = %T, want sentinelCodec — injection did not override the default", inj.Codec())
	}
	if got := inj.Codec().Ext(); got != "sentinel-ext" {
		t.Errorf("injected codec Ext() = %q, want %q — WithCodec did not take effect", got, "sentinel-ext")
	}
}
