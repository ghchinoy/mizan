package eval

import (
	"context"
	"errors"

	"google.golang.org/genai"

	"github.com/ghchinoy/mizan/internal/registry"
)

// errNotImplemented marks scaffold stubs wired in a later phase.
var errNotImplemented = errors.New("eval: not implemented")

// customEngine implements Engine via a direct genai GenerateContent call with
// a strict ResponseSchema, for KindCustomSchema templates whose output shape
// exceeds what EvaluateInstances can express.
//
// Scaffold stub: retry/backoff and schema parsing land in the implementation
// phase (docs/spikes.md Spike 4).
type customEngine struct {
	client *genai.Client
}

var _ Engine = (*customEngine)(nil)

func (e *customEngine) Run(ctx context.Context, tmpl registry.MetricTemplate, inst Instance) (Result, error) {
	return Result{}, errNotImplemented
}
