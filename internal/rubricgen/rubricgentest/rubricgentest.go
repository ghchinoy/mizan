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

// Package rubricgentest provides a REUSABLE, network-free test double for the
// rubricgen.Client seam, modeled on internal/eval/evaltest's FakeGenaiClient:
// scriptable per-call turns plus a CallsLog for request assertions. It lives in
// its own (non-test) package so both internal/rubricgen tests and cmd/mizan tests
// can share one fake; nothing links it into the shipped binary because only
// *_test.go files import it.
package rubricgentest

import (
	"context"

	"github.com/ghchinoy/mizan/internal/rubricgen"
)

// Call captures the arguments of one Generate invocation.
type Call struct {
	Contents []rubricgen.InstanceContent
	Spec     rubricgen.Spec
}

// turn is one scripted (rubrics, error) outcome.
type turn struct {
	rubrics []rubricgen.Rubric
	err     error
}

// FakeClient is a scriptable, network-free rubricgen.Client. Scripted turns are
// consumed in order (one per call); once the queue drains the sticky Rubrics /
// Err fallback is returned for every subsequent call. A zero-value FakeClient
// returns (nil, nil) — set Rubrics or push a turn for a useful default.
type FakeClient struct {
	// CallsLog captures every Generate invocation's arguments, in call order.
	CallsLog []Call

	// Rubrics / Err are the sticky fallback returned once the queue drains.
	Rubrics []rubricgen.Rubric
	Err     error

	turns []turn
}

// PushRubrics queues one successful result for the next call. Returns the
// receiver for chaining.
func (f *FakeClient) PushRubrics(rubrics []rubricgen.Rubric) *FakeClient {
	f.turns = append(f.turns, turn{rubrics: rubrics})
	return f
}

// PushError queues one error for the next call. Returns the receiver.
func (f *FakeClient) PushError(err error) *FakeClient {
	f.turns = append(f.turns, turn{err: err})
	return f
}

// Generate records the call and returns the next scripted outcome (or the sticky
// fallback). It satisfies rubricgen.Client.
func (f *FakeClient) Generate(_ context.Context, contents []rubricgen.InstanceContent, spec rubricgen.Spec) ([]rubricgen.Rubric, error) {
	f.CallsLog = append(f.CallsLog, Call{Contents: contents, Spec: spec})
	if len(f.turns) > 0 {
		t := f.turns[0]
		f.turns = f.turns[1:]
		return t.rubrics, t.err
	}
	return f.Rubrics, f.Err
}

// Calls returns the number of Generate calls received.
func (f *FakeClient) Calls() int { return len(f.CallsLog) }

// LastCall returns the most recent recorded call, or nil if none.
func (f *FakeClient) LastCall() *Call {
	if len(f.CallsLog) == 0 {
		return nil
	}
	return &f.CallsLog[len(f.CallsLog)-1]
}

// Rubric is a small helper to build a rubricgen.Rubric with a description (and
// optional type/importance) for scripting.
func Rubric(description, typ, importance string) rubricgen.Rubric {
	r := rubricgen.Rubric{Type: typ, Importance: importance}
	r.Content.Property.Description = description
	return r
}
