package eval

import (
	"context"
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

// runPointwise materializes a PointwiseMetricSpec + instance + AutoraterConfig,
// calls EvaluateInstances, and maps the response into a Result. Text instances
// use a JsonInstance; instances with any non-text asset use a ContentMapInstance
// with gs:// FileData (staging local files first — spike-core: native accepts
// gs:// only).
func (e *Engine) runPointwise(ctx context.Context, tmpl registry.MetricTemplate, inst Instance, model string) (Result, error) {
	if tmpl.MetricPromptTemplate == "" {
		return Result{}, fmt.Errorf("eval: template %q has empty metric prompt template", tmpl.ID)
	}
	return e.runNativePointwise(ctx, tmpl, inst, tmpl.MetricPromptTemplate, "", model)
}

// runNativePointwise is the shared native pointwise materialization used by both
// the pointwise and rubric paths (they differ only in the judge prompt: rubric
// appends its rendered criteria). Keeping this common helper removes the prior
// duplication (rev-4 / test-3 R1) without merging the native and genai paths,
// which must stay distinct.
//
// prompt is the metric prompt template placed in the spec; template variables
// are always extracted from tmpl.MetricPromptTemplate (the rubric block appends
// no placeholders), so the instance keys are the same on both paths. label
// annotates error messages with the calling path (e.g. "rubric") and is empty
// for plain pointwise.
func (e *Engine) runNativePointwise(ctx context.Context, tmpl registry.MetricTemplate, inst Instance, prompt, label, model string) (Result, error) {
	if e.client == nil {
		return Result{}, fmt.Errorf("eval: no evaluation client configured")
	}

	fullModel, err := expandAutoraterModel(model, e.projectID, e.location)
	if err != nil {
		return Result{}, err
	}

	metricInstance, err := e.buildPointwiseInstance(ctx, tmpl.MetricPromptTemplate, inst)
	if err != nil {
		return Result{}, err
	}

	spec := &aiplatformpb.PointwiseMetricSpec{
		MetricPromptTemplate: proto.String(prompt),
	}
	if tmpl.SystemInstruction != "" {
		spec.SystemInstruction = proto.String(tmpl.SystemInstruction)
	}

	autorater := &aiplatformpb.AutoraterConfig{AutoraterModel: fullModel}
	if tmpl.SamplingCount > 0 {
		autorater.SamplingCount = proto.Int32(tmpl.SamplingCount)
	}

	req := &aiplatformpb.EvaluateInstancesRequest{
		Location: fmt.Sprintf("projects/%s/locations/%s", e.projectID, e.location),
		MetricInputs: &aiplatformpb.EvaluateInstancesRequest_PointwiseMetricInput{
			PointwiseMetricInput: &aiplatformpb.PointwiseMetricInput{
				MetricSpec: spec,
				Instance:   metricInstance,
			},
		},
		AutoraterConfig: autorater,
	}

	ctxLabel := ""
	if label != "" {
		ctxLabel = " (" + label + ")"
	}

	resp, err := e.client.EvaluateInstances(ctx, req)
	if err != nil {
		return Result{}, fmt.Errorf("eval: EvaluateInstances%s: %w", ctxLabel, err)
	}

	pr := resp.GetPointwiseMetricResult()
	if pr == nil {
		return Result{}, fmt.Errorf("eval: %sresponse contained no pointwise metric result", labelPrefix(label))
	}
	return Result{
		Score:       pr.Score,
		Explanation: pr.GetExplanation(),
	}, nil
}

// labelPrefix returns "<label> " for a non-empty label, else "".
func labelPrefix(label string) string {
	if label == "" {
		return ""
	}
	return label + " "
}

// buildPointwiseInstance builds the native pointwise instance from the template's
// declared variables, validating variable/instance-key parity client-side to
// fail fast before the API call (spike-core). It selects a ContentMapInstance
// (gs:// FileData) when any referenced field is a non-text asset, else a
// JsonInstance.
func (e *Engine) buildPointwiseInstance(ctx context.Context, varTemplate string, inst Instance) (*aiplatformpb.PointwiseMetricInstance, error) {
	keys := extractVars(varTemplate)
	if keysHaveMedia(keys, inst) {
		cm, err := e.buildContentMap(ctx, keys, inst)
		if err != nil {
			return nil, err
		}
		return &aiplatformpb.PointwiseMetricInstance{
			Instance: &aiplatformpb.PointwiseMetricInstance_ContentMapInstance{ContentMapInstance: cm},
		}, nil
	}
	jsonInstance, err := buildJSONInstanceKeys(keys, inst)
	if err != nil {
		return nil, err
	}
	return &aiplatformpb.PointwiseMetricInstance{
		Instance: &aiplatformpb.PointwiseMetricInstance_JsonInstance{JsonInstance: jsonInstance},
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
func (e *Engine) runRubric(ctx context.Context, tmpl registry.MetricTemplate, inst Instance, model string) (Result, error) {
	if tmpl.MetricPromptTemplate == "" {
		return Result{}, fmt.Errorf("eval: template %q has empty metric prompt template", tmpl.ID)
	}
	if len(tmpl.RubricGroups) == 0 {
		return Result{}, fmt.Errorf("eval: rubric template %q has no rubric groups", tmpl.ID)
	}
	prompt := tmpl.MetricPromptTemplate + "\n\n" + renderRubricGroups(tmpl.RubricGroups)
	return e.runNativePointwise(ctx, tmpl, inst, prompt, "rubric", model)
}

// renderRubricGroups turns the inline RubricGroups map into a deterministic,
// human/judge-readable block appended to the metric prompt. Group names are
// sorted; criteria are emitted in their declared (slice) order. Both are stable,
// so the same template always produces the same prompt.
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
// referenced key/value pairs into the JSON string the API expects. It delegates
// to buildJSONInstanceKeys (content.go), which the multimodal/pairwise paths also
// use with an explicit key list.
func buildJSONInstance(template string, inst Instance) (string, error) {
	return buildJSONInstanceKeys(extractVars(template), inst)
}
