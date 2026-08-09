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

	// Pairwise defaults (implementation-plan WI-P1-4): FlipEnabled on for
	// position-bias mitigation, SamplingCount >= 4. The P1 registry model has no
	// tri-state for FlipEnabled, so an unset (zero-value false) template gets the
	// default-on behavior; a non-zero SamplingCount is honored as-is. See the
	// WI-P1-4 project log for the tri-state follow-up.
	sampling := tmpl.SamplingCount
	if sampling <= 0 {
		sampling = pairwiseDefaultSamplingCount
	}
	flip := tmpl.FlipEnabled
	if !flip {
		flip = true
	}
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
