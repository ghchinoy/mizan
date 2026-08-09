package eval

import (
	"context"

	aiplatform "cloud.google.com/go/aiplatform/apiv1beta1"

	"github.com/ghchinoy/mizan/internal/registry"
)

// nativeEngine implements Engine via the v1beta1 EvaluateInstances API
// (pointwise/pairwise, text and multimodal via ContentMap).
//
// Scaffold stub: request construction and dispatch land in the eval
// implementation phase (docs/spikes.md Spikes 1-3 supply the concrete syntax).
type nativeEngine struct {
	client *aiplatform.EvaluationClient
	// location and autorater defaults are supplied at construction time.
	location string
}

var _ Engine = (*nativeEngine)(nil)

func (e *nativeEngine) Run(ctx context.Context, tmpl registry.MetricTemplate, inst Instance) (Result, error) {
	return Result{}, errNotImplemented
}
