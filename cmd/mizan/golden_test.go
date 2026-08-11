package main

// golden_test.go pins the exact rendered output of the CLI's renderers to golden
// files under testdata/. It complements the assertion-based tests in
// eval_render_test.go by catching ANY drift in the full formatted output (column
// alignment, ordering, footers) that a substring assertion would miss.
//
// Workflow: the renderers are pure (io.Writer in, deterministic bytes out), so
// each case renders into a buffer and is compared byte-for-byte against its
// testdata/<name>.golden file. To (re)generate the goldens after an intentional
// output change, run:
//
//	go test ./cmd/mizan -run TestGolden -update
//
// then review the diff before committing. Without -update the goldens are
// read-only expectations. Inputs are fixed (durations are set to constants) so
// the output is stable across runs and machines.

import (
	"bytes"
	"flag"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ghchinoy/mizan/internal/eval"
	"github.com/ghchinoy/mizan/internal/registry"
)

var update = flag.Bool("update", false, "update golden files under testdata/")

// checkGolden renders via fn and compares (or, with -update, rewrites) the golden
// file testdata/<name>.golden.
func checkGolden(t *testing.T, name string, fn func(w io.Writer) error) {
	t.Helper()
	var buf bytes.Buffer
	if err := fn(&buf); err != nil {
		t.Fatalf("render %s: %v", name, err)
	}
	got := buf.Bytes()
	path := filepath.Join("testdata", name+".golden")

	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("update golden %s: %v", path, err)
		}
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v (run `go test ./cmd/mizan -run TestGolden -update` to create it)", path, err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("output for %s does not match %s\n--- got ---\n%s\n--- want ---\n%s",
			name, path, got, want)
	}
}

func fixedScore(v float32) *float32 { return &v }

// customSchemaResult is a genai-path custom_schema result with token stats.
func customSchemaResult() eval.Result {
	return eval.Result{
		Explanation:  "meets the brand guidelines",
		CustomOutput: map[string]any{"compliant": true, "overall_score": float64(8), "issues": []any{}},
		Stats: eval.Stats{
			Duration:   1500 * time.Millisecond,
			TokenUsage: &eval.TokenUsage{PromptTokens: 120, CandidatesTokens: 34, TotalTokens: 154},
		},
	}
}

// rubricDetailGoldenResult is a rubric per-criterion result as the engine emits it.
func rubricDetailGoldenResult() eval.Result {
	return eval.Result{
		Score:        fixedScore(4),
		RubricDetail: true,
		CustomOutput: map[string]any{
			"per_criterion": []any{
				map[string]any{"group": "clarity", "criterion": "The message is unambiguous", "score": 4, "rationale": "mostly clear"},
				map[string]any{"group": "tone", "criterion": "Matches a professional brand voice", "score": 3, "rationale": "a bit casual"},
			},
			"overall_score": float64(4),
			"explanation":   "Solid ad copy.",
		},
		Stats: eval.Stats{Duration: 2 * time.Second},
	}
}

// sampleTemplate is a representative rubric template exercising most fields.
func sampleTemplate() *registry.MetricTemplate {
	return &registry.MetricTemplate{
		ID:                   "google-brand/ad-copy",
		Name:                 "Ad Copy Quality",
		Description:          "Judges ad copy against brand rubric",
		Kind:                 registry.KindRubric,
		Modalities:           []registry.Modality{registry.ModalityText},
		AutoraterModel:       "gemini-2.5-flash",
		SamplingCount:        4,
		MetricPromptTemplate: "Evaluate the ad copy: {{response}}\nConsider tone and clarity.",
		RubricGroups: map[string][]string{
			"tone":    {"Matches a professional brand voice", "Avoids slang"},
			"clarity": {"The message is unambiguous"},
		},
	}
}

// TestGolden runs every golden case. Table format is the default; JSON cases pin
// the machine-output contract too.
func TestGolden(t *testing.T) {
	// renderResult — table, pointwise (native, no stats).
	t.Run("renderResult_pointwise_table", func(t *testing.T) {
		outputFormat = outputTable
		res := eval.Result{Score: fixedScore(4.5), Explanation: "Clear and correct."}
		checkGolden(t, "renderResult_pointwise_table", func(w io.Writer) error {
			return renderResult(w, res, false)
		})
	})

	// renderResult — table, pairwise.
	t.Run("renderResult_pairwise_table", func(t *testing.T) {
		outputFormat = outputTable
		res := eval.Result{PairwiseChoice: "CANDIDATE", Explanation: "Candidate is more helpful."}
		checkGolden(t, "renderResult_pairwise_table", func(w io.Writer) error {
			return renderResult(w, res, false)
		})
	})

	// renderResult — JSON, pairwise with a flip warning (machine-output contract:
	// no score, a choice, and the "warnings" array serialized for consumers).
	t.Run("renderResult_pairwise_json", func(t *testing.T) {
		outputFormat = outputJSON
		res := eval.Result{
			PairwiseChoice: "CANDIDATE",
			Explanation:    "Candidate is more helpful.",
			Warnings:       []string{"pairwise flip is enabled: the Choice is the de-biased, authoritative verdict."},
		}
		checkGolden(t, "renderResult_pairwise_json", func(w io.Writer) error {
			return renderResult(w, res, false)
		})
	})

	// renderResult — table, native pointwise WITH --stats (the "not available" note).
	t.Run("renderResult_native_stats_table", func(t *testing.T) {
		outputFormat = outputTable
		res := eval.Result{Score: fixedScore(4), Explanation: "ok", Stats: eval.Stats{Duration: 750 * time.Millisecond}}
		checkGolden(t, "renderResult_native_stats_table", func(w io.Writer) error {
			return renderResult(w, res, true)
		})
	})

	// renderResult — table, custom_schema WITH --stats (token breakdown + custom output).
	t.Run("renderResult_custom_stats_table", func(t *testing.T) {
		outputFormat = outputTable
		checkGolden(t, "renderResult_custom_stats_table", func(w io.Writer) error {
			return renderResult(w, customSchemaResult(), true)
		})
	})

	// renderResult — JSON, custom_schema (machine-output contract).
	t.Run("renderResult_custom_json", func(t *testing.T) {
		outputFormat = outputJSON
		checkGolden(t, "renderResult_custom_json", func(w io.Writer) error {
			return renderResult(w, customSchemaResult(), false)
		})
	})

	// renderRubricDetailResult — routed via renderResult (table, with stats).
	t.Run("renderRubricDetail_table", func(t *testing.T) {
		outputFormat = outputTable
		checkGolden(t, "renderRubricDetail_table", func(w io.Writer) error {
			return renderResult(w, rubricDetailGoldenResult(), true)
		})
	})

	// printPreflight — native regional target, project from the env file and the
	// default location (the source hints reuse the config resolver — POLA #1).
	t.Run("printPreflight_native", func(t *testing.T) {
		checkGolden(t, "printPreflight_native", func(w io.Writer) error {
			printPreflight(w, eval.ResolvedTarget{Project: "my-project", Location: "us-central1", Model: "gemini-2.5-flash", Path: "native"}, "env-file", "default")
			return nil
		})
	})

	// printPreflight — genai/global target; project from an exported env var and
	// the global location forced by the genai path.
	t.Run("printPreflight_genai", func(t *testing.T) {
		checkGolden(t, "printPreflight_genai", func(w io.Writer) error {
			printPreflight(w, eval.ResolvedTarget{Project: "my-project", Location: "global", Model: "gemini-3.5-flash", Path: "genai"}, "env", "global-path")
			return nil
		})
	})

	// printPreflight — native path auto-routed to the global host for a known
	// global-only judge (FIX-SRC): location=global but path=native, so the source
	// is the routing (src=global-route), NOT a fully-qualified model resource.
	t.Run("printPreflight_global_route", func(t *testing.T) {
		checkGolden(t, "printPreflight_global_route", func(w io.Writer) error {
			printPreflight(w, eval.ResolvedTarget{Project: "my-project", Location: "global", Model: "gemini-3.5-flash", Path: "native"}, "env-file", "global-route")
			return nil
		})
	})

	// printPreflight — native path whose location=global came from a fully-qualified
	// model RESOURCE explicitly pinned to locations/global (review OPTIONAL): the
	// location string matches the global-route case above, but its provenance is the
	// resource, so the source is src=model — NOT src=global-route.
	t.Run("printPreflight_global_model", func(t *testing.T) {
		checkGolden(t, "printPreflight_global_model", func(w io.Writer) error {
			printPreflight(w, eval.ResolvedTarget{Project: "my-project", Location: "global", Model: "gemini-2.5-flash", Path: "native", LocationFromModelResource: true}, "env-file", "model")
			return nil
		})
	})

	// printPreflight — project supplied by the per-invocation --project flag
	// (FEAT-PROJECT): src=flag, the highest-precedence project source.
	t.Run("printPreflight_project_flag", func(t *testing.T) {
		checkGolden(t, "printPreflight_project_flag", func(w io.Writer) error {
			printPreflight(w, eval.ResolvedTarget{Project: "flag-project", Location: "us-central1", Model: "gemini-2.5-flash", Path: "native"}, "flag", "default")
			return nil
		})
	})

	// renderTemplate — single template, table.
	t.Run("renderTemplate_table", func(t *testing.T) {
		outputFormat = outputTable
		checkGolden(t, "renderTemplate_table", func(w io.Writer) error {
			return renderTemplate(w, sampleTemplate())
		})
	})

	// renderTemplate — single template, JSON.
	t.Run("renderTemplate_json", func(t *testing.T) {
		outputFormat = outputJSON
		checkGolden(t, "renderTemplate_json", func(w io.Writer) error {
			return renderTemplate(w, sampleTemplate())
		})
	})

	// renderTemplateList — table over multiple templates.
	t.Run("renderTemplateList_table", func(t *testing.T) {
		outputFormat = outputTable
		list := []registry.MetricTemplate{
			{ID: "google-brand/ad-copy", Name: "Ad Copy Quality", Kind: registry.KindRubric, AutoraterModel: "gemini-2.5-flash"},
			{ID: "core/helpfulness", Name: "Helpfulness", Kind: registry.KindPointwise, AutoraterModel: "gemini-3.5-flash"},
		}
		checkGolden(t, "renderTemplateList_table", func(w io.Writer) error {
			return renderTemplateList(w, list)
		})
	})
}
