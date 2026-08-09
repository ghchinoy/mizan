package eval

// pairwise.go holds the native pairwise path (KindPairwise). It materializes a
// PairwiseMetricSpec (baseline/candidate field names + judge prompt), an
// AutoraterConfig (FlipEnabled + SamplingCount), and a pairwise instance
// (JsonInstance for text, ContentMapInstance with gs:// FileData for
// multimodal), calls EvaluateInstances on the same regional client as pointwise,
// and maps the PairwiseChoice enum to a "BASELINE"/"CANDIDATE"/"TIE" string
// (1/2/3, confirmed live — spike-core).

import (
	"context"
	"fmt"
	"strings"

	aiplatformpb "cloud.google.com/go/aiplatform/apiv1beta1/aiplatformpb"
	"google.golang.org/protobuf/proto"

	"github.com/ghchinoy/mizan/internal/registry"
)

// pairwiseDefaultSamplingCount is applied when a pairwise template leaves
// SamplingCount unset. Pairwise benefits from multiple samples (with FlipEnabled)
// to average out position bias, so the default is >= 4 (implementation-plan
// WI-P1-4).
const pairwiseDefaultSamplingCount int32 = 4

// runPairwise materializes and runs a native pairwise evaluation.
func (e *Engine) runPairwise(ctx context.Context, tmpl registry.MetricTemplate, inst Instance) (Result, error) {
	if e.client == nil {
		return Result{}, fmt.Errorf("eval: no evaluation client configured")
	}
	if tmpl.MetricPromptTemplate == "" {
		return Result{}, fmt.Errorf("eval: template %q has empty metric prompt template", tmpl.ID)
	}
	if tmpl.BaselineFieldName == "" || tmpl.CandidateFieldName == "" {
		return Result{}, fmt.Errorf("eval: pairwise template %q must set BaselineFieldName and CandidateFieldName", tmpl.ID)
	}
	if err := validatePairwisePlaceholders(tmpl); err != nil {
		return Result{}, err
	}

	model, err := expandAutoraterModel(tmpl.AutoraterModel, e.projectID, e.location)
	if err != nil {
		return Result{}, err
	}

	metricInstance, err := e.buildPairwiseInstance(ctx, tmpl, inst)
	if err != nil {
		return Result{}, err
	}

	spec := &aiplatformpb.PairwiseMetricSpec{
		MetricPromptTemplate:       proto.String(tmpl.MetricPromptTemplate),
		BaselineResponseFieldName:  tmpl.BaselineFieldName,
		CandidateResponseFieldName: tmpl.CandidateFieldName,
	}
	if tmpl.SystemInstruction != "" {
		spec.SystemInstruction = proto.String(tmpl.SystemInstruction)
	}

	// Pairwise defaults (implementation-plan WI-P1-4). A non-zero template
	// SamplingCount is honored as-is; otherwise it defaults to >= 4.
	sampling := tmpl.SamplingCount
	if sampling <= 0 {
		sampling = pairwiseDefaultSamplingCount
	}
	// FlipEnabled is unconditionally true in P1 for position-bias mitigation.
	// The P1 registry model stores FlipEnabled as a plain bool, which cannot
	// express a tri-state: an unset field is indistinguishable from an operator
	// explicitly setting false (both are the zero value), so P1 cannot honor an
	// explicit-false and always enables flip. A real explicit-false override is a
	// P2 registry change (add a nullable/tri-state field) — see the WI-P1-4 log.
	flip := true
	autorater := &aiplatformpb.AutoraterConfig{
		AutoraterModel: model,
		SamplingCount:  proto.Int32(sampling),
		FlipEnabled:    proto.Bool(flip),
	}

	req := &aiplatformpb.EvaluateInstancesRequest{
		Location: fmt.Sprintf("projects/%s/locations/%s", e.projectID, e.location),
		MetricInputs: &aiplatformpb.EvaluateInstancesRequest_PairwiseMetricInput{
			PairwiseMetricInput: &aiplatformpb.PairwiseMetricInput{
				MetricSpec: spec,
				Instance:   metricInstance,
			},
		},
		AutoraterConfig: autorater,
	}

	resp, err := e.client.EvaluateInstances(ctx, req)
	if err != nil {
		return Result{}, fmt.Errorf("eval: EvaluateInstances (pairwise): %w", err)
	}

	pr := resp.GetPairwiseMetricResult()
	if pr == nil {
		return Result{}, fmt.Errorf("eval: pairwise response contained no pairwise metric result")
	}
	return Result{
		PairwiseChoice: pairwiseChoiceString(pr.GetPairwiseChoice()),
		Explanation:    pr.GetExplanation(),
	}, nil
}

// buildPairwiseInstance builds the native pairwise instance from the baseline and
// candidate field names plus any additional {{var}} placeholders in the metric
// prompt. It validates variable/instance-key parity client-side and selects a
// ContentMapInstance (gs:// FileData) when any referenced field is a non-text
// asset (multimodal pairwise), else a JsonInstance.
func (e *Engine) buildPairwiseInstance(ctx context.Context, tmpl registry.MetricTemplate, inst Instance) (*aiplatformpb.PairwiseMetricInstance, error) {
	keys := pairwiseKeys(tmpl)
	if keysHaveMedia(keys, inst) {
		cm, err := e.buildContentMap(ctx, keys, inst)
		if err != nil {
			return nil, err
		}
		return &aiplatformpb.PairwiseMetricInstance{
			Instance: &aiplatformpb.PairwiseMetricInstance_ContentMapInstance{ContentMapInstance: cm},
		}, nil
	}
	jsonInstance, err := buildJSONInstanceKeys(keys, inst)
	if err != nil {
		return nil, err
	}
	return &aiplatformpb.PairwiseMetricInstance{
		Instance: &aiplatformpb.PairwiseMetricInstance_JsonInstance{JsonInstance: jsonInstance},
	}, nil
}

// validatePairwisePlaceholders fails fast, client-side, when the metric prompt
// template does not reference the baseline and/or candidate field-name
// placeholders as {{name}}. The Eval Service rejects a pairwise instance whose
// keys are absent from the template (confirmed live — dev note WI-P1-4) with an
// opaque server-side error; this converts that into a clear, actionable message
// naming the missing placeholder(s) BEFORE any API call. It reuses extractVars so
// the double-brace parsing matches the rest of the engine and a valid template
// (one that does reference both) is never rejected.
func validatePairwisePlaceholders(tmpl registry.MetricTemplate) error {
	vars := map[string]bool{}
	for _, v := range extractVars(tmpl.MetricPromptTemplate) {
		vars[v] = true
	}
	var missing []string
	if !vars[tmpl.BaselineFieldName] {
		missing = append(missing, fmt.Sprintf("baseline {{%s}}", tmpl.BaselineFieldName))
	}
	if !vars[tmpl.CandidateFieldName] {
		missing = append(missing, fmt.Sprintf("candidate {{%s}}", tmpl.CandidateFieldName))
	}
	if len(missing) > 0 {
		return fmt.Errorf("eval: pairwise template %q metric prompt must reference the %s placeholder(s); the API rejects instance keys not present in the template", tmpl.ID, strings.Join(missing, " and "))
	}
	return nil
}

// pairwiseKeys returns the instance keys a pairwise eval references: the baseline
// and candidate field names, plus any additional {{var}} placeholders in the
// metric prompt (de-duplicated, first-seen order: baseline, candidate, then
// prompt vars).
func pairwiseKeys(tmpl registry.MetricTemplate) []string {
	seen := map[string]bool{}
	var keys []string
	for _, k := range []string{tmpl.BaselineFieldName, tmpl.CandidateFieldName} {
		if k != "" && !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	for _, v := range extractVars(tmpl.MetricPromptTemplate) {
		if !seen[v] {
			seen[v] = true
			keys = append(keys, v)
		}
	}
	return keys
}

// pairwiseChoiceString maps the PairwiseChoice enum (BASELINE=1, CANDIDATE=2,
// TIE=3, confirmed live — spike-core) to the string surfaced in Result.
func pairwiseChoiceString(c aiplatformpb.PairwiseChoice) string {
	switch c {
	case aiplatformpb.PairwiseChoice_BASELINE:
		return "BASELINE"
	case aiplatformpb.PairwiseChoice_CANDIDATE:
		return "CANDIDATE"
	case aiplatformpb.PairwiseChoice_TIE:
		return "TIE"
	default:
		return "UNSPECIFIED"
	}
}
