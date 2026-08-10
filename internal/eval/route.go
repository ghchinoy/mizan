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
	"io"
	"os"
	"strings"

	aiplatformpb "cloud.google.com/go/aiplatform/apiv1beta1/aiplatformpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// globalLocation is the SINGLE source of truth for the global-location string.
// NewClient maps it to the bare host aiplatform.googleapis.com:443; the request
// carries projects/{p}/locations/global. It is the ONLY host that resolves a
// global-only autorater (spike-eval-region-autorater row 4a). The exported
// GenaiLocation (model.go) aliases this const so the two cannot drift
// (review OPTIONAL-1).
const globalLocation = "global"

// globalHost is the bare global eval endpoint host, named in the retry notice so
// the user sees exactly where the call was re-routed.
const globalHost = "aiplatform.googleapis.com"

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

// isAutoraterPermissionDenied narrowly matches the SPECIFIC error the Eval
// Service returns when the AUTORATER GenerateContent call is denied at the
// project level: a gRPC codes.PermissionDenied whose message is about the
// autorater model / the inner GenerateContent request (Vertex wraps it as
// "Failed to make GenerateContent request to autorater model <resource>. If
// you're using a new project, expect a delay and retry..."). The match requires
// BOTH the PermissionDenied code AND an autorater/GenerateContent marker so an
// unrelated PermissionDenied (a caller lacking evaluateInstances, a template ACL,
// etc.) does NOT get the autorater-specific guidance. Unlike NotFound, this is
// NOT host-dependent, so it deliberately does NOT trigger the global-host retry —
// see evaluateRouted. status.FromError unwraps a wrapped gRPC/apierror error,
// mirroring isAutoraterNotFound.
func isAutoraterPermissionDenied(err error) bool {
	if err == nil {
		return false
	}
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.PermissionDenied {
		return false
	}
	msg := strings.ToLower(st.Message())
	return strings.Contains(msg, "autorater model") || strings.Contains(msg, "generatecontent")
}

// autoraterPermissionDeniedError builds the ACTIONABLE error for a project-level
// autorater PermissionDenied. The raw Vertex string ("...expect a delay and
// retry...") misdirects the user toward a transient retry for what is actually a
// non-transient project IAM/enablement condition, so this replaces that framing
// with the concrete grants to make and APPENDS the raw underlying error so
// nothing is lost. It fills project/location/model from the engine's scope.
func (e *Engine) autoraterPermissionDeniedError(ctxLabel, model string, raw error) error {
	return fmt.Errorf("eval: EvaluateInstances%s: autorater GenerateContent was denied in project %q (location %q). "+
		"This is a project IAM/enablement issue, NOT a transient delay — retrying will not help. To fix: "+
		"(a) ensure the Vertex AI API is enabled in project %q; "+
		"(b) grant the project's Vertex AI Service Agent (service-<projnum>@gcp-sa-aiplatform.iam.gserviceaccount.com) "+
		"the roles/aiplatform.serviceAgent role so it can invoke %s; "+
		"(c) if the metric references a gs:// asset, grant that service agent roles/storage.objectViewer on the staging bucket for cross-project reads. "+
		"(raw: %v)",
		ctxLabel, e.projectID, e.location, e.projectID, bareModelID(model), raw)
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
		e.noticeGlobalOnly(model)
		resp, err := e.evaluateAt(ctx, globalLocation, e.globalClient, model, build)
		if err != nil {
			if isAutoraterPermissionDenied(err) {
				return nil, e.autoraterPermissionDeniedError(ctxLabel, model, err)
			}
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
		e.noticeRetryGlobal(model)
		gResp, gErr := e.evaluateAt(ctx, globalLocation, e.globalClient, model, build)
		if gErr != nil {
			// The regional attempt was an autorater-NotFound; if the global retry was
			// itself denied by a project-level PermissionDenied, THAT is the actionable
			// signal (a permission denial is not host-dependent), so surface the
			// concrete grants to make rather than the "not found" framing.
			if isAutoraterPermissionDenied(gErr) {
				return nil, e.autoraterPermissionDeniedError(ctxLabel, model, gErr)
			}
			// Global retry also failed: surface the ORIGINAL regional error plus the
			// global-retry context (the original error is the actionable one).
			return nil, fmt.Errorf("eval: EvaluateInstances%s: autorater %q not found on location %q and the global-host retry also failed (global error: %v): %w",
				ctxLabel, bareModelID(model), e.location, gErr, err)
		}
		return gResp, nil
	}

	// A project-level autorater PermissionDenied is NOT host-dependent, so it is
	// (correctly) never retried above; classify it here so the user gets actionable
	// IAM/enablement guidance instead of the raw, misleading "expect a delay and
	// retry" Vertex string. Any other error keeps its current verbatim behavior.
	if isAutoraterPermissionDenied(err) {
		return nil, e.autoraterPermissionDeniedError(ctxLabel, model, err)
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

// noticeWriter returns the writer surfaced notices go to (stderr by default,
// overridable via WithNoticeWriter for deterministic capture in tests).
func (e *Engine) noticeWriter() io.Writer {
	if e.noticeW != nil {
		return e.noticeW
	}
	return os.Stderr
}

// noticeGlobalOnly surfaces (WI-F7 echo style, on stderr by default) that the
// prefix FAST-PATH classified the autorater as a KNOWN global-only judge and so
// routed the eval to the global host. Here we KNOW (via globalOnlyModelPrefixes)
// the model is global-only, so the notice asserts that classification. The user
// is told their --location was not honored as a residency region.
func (e *Engine) noticeGlobalOnly(model string) {
	fmt.Fprintf(e.noticeWriter(), "mizan: autorater %s is global-only (%s is a known global-only judge); routing this eval to the GLOBAL host (location=global). Your configured --location is kept for labeling only.\n",
		bareModelID(model), bareModelID(model))
}

// noticeRetryGlobal surfaces (WI-F7 echo style, on stderr by default) that the
// self-correcting RETRY fired: the autorater was not found on the configured
// regional host, so the SAME call is being retried on the global host. Unlike the
// fast-path, this route does NOT prove the model is global-only — it also fires
// for a typo'd or otherwise unresolvable regional model — so the notice describes
// the ACTION taken rather than asserting a global-only classification.
func (e *Engine) noticeRetryGlobal(model string) {
	fmt.Fprintf(e.noticeWriter(), "mizan: autorater %s not found in location %s; retrying this eval on the global host (%s, location=global). Your configured --location is kept for labeling only.\n",
		bareModelID(model), e.location, globalHost)
}
