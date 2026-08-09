package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

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

// runRubric materializes the NATIVE rubric path. It renders the template's
// inline RubricGroups into the judge prompt and evaluates via the same regional
// EvaluationClient (EvaluateInstances) as pointwise, mapping the response into a
// Result (score/explanation).
//
// Note on the proto shape: aiplatform v1.126.0's synchronous EvaluateInstances
// oneof exposes PointwiseMetricSpec/PairwiseMetricSpec and a fixed
// RubricBasedInstructionFollowing metric, but NOT an LLMBasedMetricSpec input
// carrying inline rubric_groups (that message is defined only for the batch
// EvaluateDataset `Metric`, and it references rubric groups by KEY, not inline).
// So Mizan renders the inline rubric criteria into the pointwise judge prompt on
// the native path — the criteria still drive the autorater and the result maps
// to the same {score, explanation}. See design/project-log for the deviation.
func (e *Engine) runRubric(ctx context.Context, tmpl registry.MetricTemplate, inst Instance) (Result, error) {
	if e.client == nil {
		return Result{}, fmt.Errorf("eval: no evaluation client configured")
	}
	if tmpl.MetricPromptTemplate == "" {
		return Result{}, fmt.Errorf("eval: template %q has empty metric prompt template", tmpl.ID)
	}
	if len(tmpl.RubricGroups) == 0 {
		return Result{}, fmt.Errorf("eval: rubric template %q has no rubric groups", tmpl.ID)
	}

	// Rubric is a native path; like pointwise it does not stage assets, so reject
	// any non-text field with a clear pointer to WI-4 (native multimodal).
	for name, ref := range inst.Fields {
		if ref.FilePath != "" || ref.GCSUri != "" || (ref.Modality != "" && ref.Modality != registry.ModalityText) {
			return Result{}, fmt.Errorf("%w: multimodal rubric field %q (native ContentMap is WI-P1-4)", errNotImplemented, name)
		}
	}

	jsonInstance, err := buildJSONInstance(tmpl.MetricPromptTemplate, inst)
	if err != nil {
		return Result{}, err
	}

	model, err := expandAutoraterModel(tmpl.AutoraterModel, e.projectID, e.location)
	if err != nil {
		return Result{}, err
	}

	prompt := tmpl.MetricPromptTemplate + "\n\n" + renderRubricGroups(tmpl.RubricGroups)

	spec := &aiplatformpb.PointwiseMetricSpec{
		MetricPromptTemplate: proto.String(prompt),
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
		return Result{}, fmt.Errorf("eval: EvaluateInstances (rubric): %w", err)
	}

	pr := resp.GetPointwiseMetricResult()
	if pr == nil {
		return Result{}, fmt.Errorf("eval: rubric response contained no pointwise metric result")
	}
	return Result{
		Score:       pr.Score,
		Explanation: pr.GetExplanation(),
	}, nil
}

// renderRubricGroups turns the inline RubricGroups map into a deterministic,
// human/judge-readable block appended to the metric prompt. Group names and the
// criteria order within each group are stabilized (sorted) so the same template
// always produces the same prompt.
func renderRubricGroups(groups map[string][]string) string {
	names := make([]string, 0, len(groups))
	for name := range groups {
		names = append(names, name)
	}
	sort.Strings(names)

	var b strings.Builder
	b.WriteString("Evaluate the response against the following rubric criteria. ")
	b.WriteString("Score higher when more criteria are satisfied and explain which criteria were met or missed.\n")
	for _, name := range names {
		b.WriteString("\n## ")
		b.WriteString(name)
		b.WriteString("\n")
		for _, criterion := range groups[name] {
			b.WriteString("- ")
			b.WriteString(criterion)
			b.WriteString("\n")
		}
	}
	return b.String()
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
