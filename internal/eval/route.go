package eval

// route.go owns the R-GLOBAL auto-routing policy: running the WHOLE native
// EvaluateInstances call against the GLOBAL eval host
// (aiplatform.googleapis.com / locations/global) when the resolved autorater is
// a global-only judge (e.g. the gemini-3.5 family).
//
// WHY THE HOST, NOT THE AUTORATER PATH: the region spike
// (design/spike-eval-region-autorater.md) proved the deciding factor is the eval
// endpoint HOST, not the locations/... segment in the autorater resource. A
// regional eval host 404s a global-only judge under ANY autorater location
// string (rows 2/3/4c); the global host resolves it even when the autorater path
// says a region where the model does not exist (rows M1/M2/4a). So an
// autorater-location override that leaves the host regional is a misleading
// half-fix — the ENTIRE call must move to the global host. That is why routing is
// expressed as "which client (regional vs global) runs the call", and the raw
// host<->endpoint mapping lives in NewClient (unit-tested at the wire level by
// R-GAPS).
//
// DETECTION — two layers, kept simple and testable:
//   1. Prefix fast-path (globalOnlyModelPrefixes): a KNOWN global-only family is
//      routed straight to the global host, skipping a guaranteed-to-404 regional
//      attempt, and letting the pre-flight echo (Engine.Resolve) reflect global
//      up front.
//   2. Self-correcting retry (the safety net for models NOT yet in the table):
//      attempt on the resolved regional client; on the SPECIFIC autorater
//      NOT_FOUND error, transparently retry the SAME call on the global host.
//      This is narrow on purpose — a non-autorater NotFound is NOT retried.

import (
	"context"
	"fmt"
	"os"
	"strings"

	aiplatformpb "cloud.google.com/go/aiplatform/apiv1beta1/aiplatformpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// globalLocation is the location selector for the GLOBAL Vertex eval endpoint.
// NewClient maps it to the bare host aiplatform.googleapis.com:443; the request
// carries projects/{p}/locations/global. It is the ONLY host that resolves a
// global-only autorater (spike-eval-region-autorater row 4a).
const globalLocation = "global"

// globalOnlyModelPrefixes is the single documented place listing autorater model
// families that are GLOBAL-ONLY: they do not exist on the regional native
// EvaluateInstances autorater path and 404 there (spike row 2). A resolved model
// whose bare id begins with one of these prefixes is routed straight to the
// global host.
//
// MAINTENANCE: this is a fast-path optimisation, NOT the source of correctness —
// the self-correcting retry (isAutoraterNotFound below) still catches any
// global-only model NOT listed here. Add a prefix when a new global-only family
// ships so users skip the wasted regional attempt and the pre-flight echo shows
// global up front; a stale entry only costs a fast-path, never correctness.
var globalOnlyModelPrefixes = []string{
	"gemini-3.5", // gemini-3.5-flash / -lite: global-only as of the 2026-08-09 spike
}

// isGlobalOnlyModel reports whether model's bare publisher id names a known
// global-only autorater family (globalOnlyModelPrefixes). It reduces a
// publisher-relative or fully-qualified id to its bare tail first, so
// "projects/.../models/gemini-3.5-flash" is detected too.
func isGlobalOnlyModel(model string) bool {
	bare := bareModelID(model)
	for _, p := range globalOnlyModelPrefixes {
		if strings.HasPrefix(bare, p) {
			return true
		}
	}
	return false
}

// isAutoraterNotFound narrowly matches the SPECIFIC error the Eval Service
// returns when it cannot resolve the AUTORATER model on the current host: a gRPC
// codes.NotFound whose message identifies the autorater model
// ("Failed to make GenerateContent request to autorater model <resource>.
// Autorater model not found..."). The match is deliberately narrow — it requires
// BOTH the NotFound code AND that the message is about the autorater model — so a
// non-autorater NotFound (a missing template, project, or dataset) does NOT
// trigger the global retry. status.FromError unwraps a wrapped gRPC/apierror
// error, but the caller inspects the raw client error here regardless.
func isAutoraterNotFound(err error) bool {
	if err == nil {
		return false
	}
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.NotFound {
		return false
	}
	return strings.Contains(strings.ToLower(st.Message()), "autorater model")
}

// buildEvalRequest constructs an EvaluateInstancesRequest for a specific eval
// location: it is called once per attempt so req.Location and the expanded
// autorater always match the host actually used. fullModel is the autorater
// resource expanded at loc; the closure supplied by each native path fills in the
// metric-kind-specific inputs (pointwise/rubric/pairwise) and any extra
// AutoraterConfig fields.
type buildEvalRequest func(loc, fullModel string) *aiplatformpb.EvaluateInstancesRequest

// evaluateRouted runs a native EvaluateInstances call with the R-GLOBAL
// auto-routing policy and returns the mapped response (callers do the result
// mapping). label annotates errors ("", "rubric", "pairwise") to match the
// pre-existing "eval: EvaluateInstances%s" wrapping.
//
// Policy:
//   - Known global-only model AND a global client is available AND we are not
//     already global: run on the global host directly (skip the 404ing regional
//     attempt).
//   - Otherwise: attempt on the configured regional client. On the SPECIFIC
//     autorater-NOT_FOUND error (and only then), transparently retry the SAME
//     call on the global host. If the global retry also fails, return the
//     ORIGINAL regional error plus context. Any other error is returned as-is.
func (e *Engine) evaluateRouted(ctx context.Context, model, label string, build buildEvalRequest) (*aiplatformpb.EvaluateInstancesResponse, error) {
	ctxLabel := ""
	if label != "" {
		ctxLabel = " (" + label + ")"
	}

	// Fast-path: a known global-only judge only resolves on the global host. When
	// e.location is already "global" the regional client IS the global host, so we
	// fall through to the ordinary attempt below (no separate global client needed).
	if isGlobalOnlyModel(model) && e.canRouteGlobal() {
		e.noticeForcedGlobal(model, fmt.Sprintf("%s is a known global-only judge", bareModelID(model)))
		resp, err := e.evaluateAt(ctx, globalLocation, e.globalClient, model, build)
		if err != nil {
			return nil, fmt.Errorf("eval: EvaluateInstances%s: %w", ctxLabel, err)
		}
		return resp, nil
	}

	// Regional (configured-location) attempt.
	resp, err := e.evaluateAt(ctx, e.location, e.client, model, build)
	if err == nil {
		return resp, nil
	}

	// Self-correcting retry ONLY on the specific autorater-not-found error.
	if e.canRouteGlobal() && isAutoraterNotFound(err) {
		e.noticeForcedGlobal(model, "the regional host could not resolve the autorater")
		gResp, gErr := e.evaluateAt(ctx, globalLocation, e.globalClient, model, build)
		if gErr != nil {
			// Global retry also failed: surface the ORIGINAL regional error plus the
			// global-retry context (the original error is the actionable one).
			return nil, fmt.Errorf("eval: EvaluateInstances%s: autorater %q not found on location %q and the global-host retry also failed (global error: %v): %w",
				ctxLabel, bareModelID(model), e.location, gErr, err)
		}
		return gResp, nil
	}

	return nil, fmt.Errorf("eval: EvaluateInstances%s: %w", ctxLabel, err)
}

// canRouteGlobal reports whether a distinct global-host client is available to
// route to (and we are not already running on the global host, in which case the
// primary client already targets global).
func (e *Engine) canRouteGlobal() bool {
	return e.globalClient != nil && e.location != globalLocation
}

// evaluateAt expands the autorater at loc, builds the request for loc, and runs
// it on the given client. Expanding per-location is what makes the autorater
// resource path match the host on both the regional attempt and the global retry.
func (e *Engine) evaluateAt(ctx context.Context, loc string, client EvaluationClient, model string, build buildEvalRequest) (*aiplatformpb.EvaluateInstancesResponse, error) {
	fullModel, err := expandAutoraterModel(model, e.projectID, loc)
	if err != nil {
		return nil, err
	}
	return client.EvaluateInstances(ctx, build(loc, fullModel))
}

// noticeForcedGlobal surfaces (WI-F7 echo style, on stderr by default) that a
// global-only judge forced the eval onto the global host, so the user is not
// surprised their --location was not honored as a residency region. reason is a
// short cause ("... is a known global-only judge" / "the regional host could not
// resolve the autorater").
func (e *Engine) noticeForcedGlobal(model, reason string) {
	w := e.noticeW
	if w == nil {
		w = os.Stderr
	}
	fmt.Fprintf(w, "mizan: autorater %s is global-only (%s); routing this eval to the GLOBAL host (location=global). Your configured --location is kept for labeling only.\n",
		bareModelID(model), reason)
}
