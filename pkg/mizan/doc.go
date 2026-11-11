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

// Package mizan provides Mizan's embeddable Go evaluation SDK for scoring and
// ranking text and multimodal (image, audio, video, music) outputs in-process
// without SQLite state, GCS staging, or CLI subprocesses.
//
// # Enterprise and Vertex AI Client Sharing
//
// Construct an [Evaluator] over any caller-owned [google.golang.org/genai.Client],
// including clients configured with genai.BackendVertexAI, genai.BackendGeminiAPI,
// or genai.BackendEnterprise:
//
//	eval, err := mizan.NewEvaluator(genaiClient,
//	    mizan.WithBackendLabel("enterprise"),
//	    mizan.WithModelOverride("gemini-4-argon"),
//	)
//
// # Runtime N-Variation Multimodal Evaluation
//
// Bind an ordered slice of [Variation] items to a single template placeholder via
// [Variations] and derive allowed discrete choices dynamically via
// [WithChoicesFromVariations] or [WithChoices]:
//
//	res, err := eval.Run(ctx, tmpl, mizan.Instance{
//	    "brief": mizan.Text("Energetic 6-second product reveal"),
//	    "variations": mizan.Variations(
//	        mizan.Variation{ID: "cut_a", Asset: mizan.URI("gs://bkt/a.mp4", "video/mp4")},
//	        mizan.Variation{ID: "cut_b", Asset: mizan.URI("gs://bkt/b.mp4", "video/mp4")},
//	    ),
//	}, mizan.WithChoicesFromVariations("none"), mizan.WithMediaResolution("low"))
package mizan
