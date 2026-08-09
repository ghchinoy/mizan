package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	aiplatform "cloud.google.com/go/aiplatform/apiv1beta1"
	aiplatformpb "cloud.google.com/go/aiplatform/apiv1beta1/aiplatformpb"
	"google.golang.org/api/option"
	"google.golang.org/protobuf/proto"

	"github.com/ghchinoy/mizan/internal/registry"
)

// NewClient builds a live EvaluationClient targeting the specific regional
// endpoint {location}-aiplatform.googleapis.com:443. The "us" multi-region
// endpoint 404s and must never be used (spike-core). Callers close the returned
// client. The concrete *aiplatform.EvaluationClient satisfies EvaluationClient.
func NewClient(ctx context.Context, location, apiEndpoint string) (*aiplatform.EvaluationClient, error) {
	endpoint := apiEndpoint
	if endpoint == "" {
		if location == "" || location == "global" {
			endpoint = "aiplatform.googleapis.com:443"
		} else {
			endpoint = fmt.Sprintf("%s-aiplatform.googleapis.com:443", location)
		}
	}
	return aiplatform.NewEvaluationClient(ctx, option.WithEndpoint(endpoint))
}

// runPointwise materializes a PointwiseMetricSpec + JsonInstance +
// AutoraterConfig, calls EvaluateInstances, and maps the response into a
// Result. This slice handles the text path only.
func (e *Engine) runPointwise(ctx context.Context, tmpl registry.MetricTemplate, inst Instance) (Result, error) {
	if e.client == nil {
		return Result{}, fmt.Errorf("eval: no evaluation client configured")
	}
	if tmpl.MetricPromptTemplate == "" {
		return Result{}, fmt.Errorf("eval: template %q has empty metric prompt template", tmpl.ID)
	}

	// Text-only slice: reject any non-text asset with a clear pointer to WI-4.
	for name, ref := range inst.Fields {
		if ref.FilePath != "" || ref.GCSUri != "" || (ref.Modality != "" && ref.Modality != registry.ModalityText) {
			return Result{}, fmt.Errorf("%w: multimodal field %q (native ContentMap is WI-P1-4)", errNotImplemented, name)
		}
	}

	// Build the JSON instance from the template's declared variables, validating
	// variable/instance-key parity client-side to fail fast before the API call
	// (spike-core recommendation).
	jsonInstance, err := buildJSONInstance(tmpl.MetricPromptTemplate, inst)
	if err != nil {
		return Result{}, err
	}

	model, err := expandAutoraterModel(tmpl.AutoraterModel, e.projectID, e.location)
	if err != nil {
		return Result{}, err
	}

	spec := &aiplatformpb.PointwiseMetricSpec{
		MetricPromptTemplate: proto.String(tmpl.MetricPromptTemplate),
	}
	if tmpl.SystemInstruction != "" {
		spec.SystemInstruction = proto.String(tmpl.SystemInstruction)
	}

	autorater := &aiplatformpb.AutoraterConfig{AutoraterModel: model}
	if tmpl.SamplingCount > 0 {
		autorater.SamplingCount = proto.Int32(tmpl.SamplingCount)
	}

	req := &aiplatformpb.EvaluateInstancesRequest{
		Location: fmt.Sprintf("projects/%s/locations/%s", e.projectID, e.location),
		MetricInputs: &aiplatformpb.EvaluateInstancesRequest_PointwiseMetricInput{
			PointwiseMetricInput: &aiplatformpb.PointwiseMetricInput{
				MetricSpec: spec,
				Instance: &aiplatformpb.PointwiseMetricInstance{
					Instance: &aiplatformpb.PointwiseMetricInstance_JsonInstance{
						JsonInstance: jsonInstance,
					},
				},
			},
		},
		AutoraterConfig: autorater,
	}

	resp, err := e.client.EvaluateInstances(ctx, req)
	if err != nil {
		return Result{}, fmt.Errorf("eval: EvaluateInstances: %w", err)
	}

	pr := resp.GetPointwiseMetricResult()
	if pr == nil {
		return Result{}, fmt.Errorf("eval: response contained no pointwise metric result")
	}
	return Result{
		Score:       pr.Score,
		Explanation: pr.GetExplanation(),
	}, nil
}

// buildJSONInstance extracts {{var}} placeholders from the template, validates
// that every referenced variable has a value in the instance, and marshals the
// referenced key/value pairs into the JSON string the API expects.
func buildJSONInstance(template string, inst Instance) (string, error) {
	vars := extractVars(template)
	if len(vars) == 0 {
		// The API rejects a non-empty instance when the template has no
		// variables; an empty JSON object is the correct payload.
		return "{}", nil
	}
	fields := map[string]string{}
	var missing []string
	for _, v := range vars {
		ref, ok := inst.Fields[v]
		if !ok {
			missing = append(missing, v)
			continue
		}
		fields[v] = ref.Text
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return "", fmt.Errorf("eval: instance is missing values for template variables %v", missing)
	}
	b, err := json.Marshal(fields)
	if err != nil {
		return "", fmt.Errorf("eval: marshal json instance: %w", err)
	}
	return string(b), nil
}
