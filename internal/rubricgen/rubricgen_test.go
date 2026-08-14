package rubricgen

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// newTestClient builds a RESTClient pointed at a local httptest server, bypassing
// ADC/NewRESTClient so Generate's request-building, defensive parsing, and error
// surfacing can be exercised without credentials or network.
func newTestClient(srv *httptest.Server) *RESTClient {
	return &RESTClient{
		httpClient: srv.Client(),
		baseURL:    srv.URL,
		projectID:  "proj",
		location:   "us-central1",
	}
}

func TestGenerateSuccessDeclaredOrder(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wantPath := "/v1beta1/projects/proj/locations/us-central1:generateInstanceRubrics"
		if r.URL.Path != wantPath {
			t.Errorf("path = %q, want %q", r.URL.Path, wantPath)
		}
		var req generateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if req.PredefinedRubricGenerationSpec == nil || req.PredefinedRubricGenerationSpec.MetricSpecName != "general_quality_v1" {
			t.Errorf("predefined spec = %+v, want metric_spec_name general_quality_v1", req.PredefinedRubricGenerationSpec)
		}
		if len(req.Contents) != 1 || len(req.Contents[0].Parts) != 1 || req.Contents[0].Parts[0].Text != "the prompt" {
			t.Errorf("contents = %+v, want single text part 'the prompt'", req.Contents)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"generatedRubrics":[
			{"rubricId":"r1","type":"T1","importance":"HIGH","content":{"property":{"description":"first"}}},
			{"rubricId":"r2","type":"T2","importance":"LOW","content":{"property":{"description":"second"}}}
		]}`))
	}))
	defer srv.Close()

	got, err := newTestClient(srv).Generate(context.Background(),
		TextContents("the prompt"), Spec{PredefinedMetric: "general_quality_v1"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(got) != 2 || got[0].Content.Property.Description != "first" || got[1].Content.Property.Description != "second" {
		t.Fatalf("rubrics not returned in declared order: %+v", got)
	}
}

func TestGenerateVertexErrorSurfacedVerbatim(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":400,"status":"INVALID_ARGUMENT","message":"recipe general_quality_v2 not found"}}`))
	}))
	defer srv.Close()

	_, err := newTestClient(srv).Generate(context.Background(),
		TextContents("x"), Spec{PredefinedMetric: "general_quality_v2"})
	if err == nil {
		t.Fatal("expected error for HTTP 400")
	}
	if !strings.Contains(err.Error(), "recipe general_quality_v2 not found") ||
		!strings.Contains(err.Error(), "INVALID_ARGUMENT") {
		t.Fatalf("error did not surface Vertex message verbatim: %v", err)
	}
}

func TestGenerateNon200NonEnvelopeFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("upstream exploded"))
	}))
	defer srv.Close()

	_, err := newTestClient(srv).Generate(context.Background(),
		TextContents("x"), Spec{PredefinedMetric: "general_quality_v1"})
	if err == nil || !strings.Contains(err.Error(), "upstream exploded") {
		t.Fatalf("expected snippet fallback, got %v", err)
	}
}

func TestGenerateEmptyRubricsIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"generatedRubrics":[]}`))
	}))
	defer srv.Close()

	_, err := newTestClient(srv).Generate(context.Background(),
		TextContents("x"), Spec{PredefinedMetric: "general_quality_v1"})
	if err == nil || !strings.Contains(err.Error(), "no rubrics") {
		t.Fatalf("expected no-rubrics error, got %v", err)
	}
}

func TestGenerateMalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"generatedRubrics": not-json`))
	}))
	defer srv.Close()

	_, err := newTestClient(srv).Generate(context.Background(),
		TextContents("x"), Spec{PredefinedMetric: "general_quality_v1"})
	if err == nil || !strings.Contains(err.Error(), "parse response") {
		t.Fatalf("expected parse error, got %v", err)
	}
}

func TestGenerateResponseCap(t *testing.T) {
	huge := strings.Repeat("a", maxResponseBytes+10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(huge))
	}))
	defer srv.Close()

	_, err := newTestClient(srv).Generate(context.Background(),
		TextContents("x"), Spec{PredefinedMetric: "general_quality_v1"})
	if err == nil || !strings.Contains(err.Error(), "cap") {
		t.Fatalf("expected response-cap error, got %v", err)
	}
}

func TestGenerateRubricCountCap(t *testing.T) {
	// A response with more than maxRubrics entries must be rejected (defensive
	// bound) rather than flooding the draft template / registry.
	var b strings.Builder
	b.WriteString(`{"generatedRubrics":[`)
	for i := 0; i < maxRubrics+1; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`{"content":{"property":{"description":"c"}}}`)
	}
	b.WriteString(`]}`)
	payload := b.String()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(payload))
	}))
	defer srv.Close()

	_, err := newTestClient(srv).Generate(context.Background(),
		TextContents("x"), Spec{PredefinedMetric: "general_quality_v1"})
	if err == nil || !strings.Contains(err.Error(), "cap") {
		t.Fatalf("expected rubric-count-cap error, got %v", err)
	}
}

func TestGenerateRequiresContents(t *testing.T) {
	// The empty-contents guard must fire locally, before any request is issued.
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("server should not be called when contents are empty")
	}))
	defer srv.Close()

	_, err := newTestClient(srv).Generate(context.Background(), nil, Spec{PredefinedMetric: "general_quality_v1"})
	if err == nil || !strings.Contains(err.Error(), "content") {
		t.Fatalf("expected local contents-required error, got %v", err)
	}
}

func TestDecodeErrorMessageWithoutStatus(t *testing.T) {
	// A Vertex error envelope that carries a message but no status must still
	// surface the message verbatim (the no-status branch of decodeError).
	err := decodeError(http.StatusBadRequest,
		[]byte(`{"error":{"code":400,"message":"prompt too long"}}`))
	if err == nil || !strings.Contains(err.Error(), "prompt too long") {
		t.Fatalf("decodeError = %v, want verbatim message", err)
	}
	if strings.Contains(err.Error(), "INVALID_ARGUMENT") {
		t.Fatalf("decodeError leaked a status where none was provided: %v", err)
	}
}

func TestGenerateRequiresRecipe(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("server should not be called when recipe is empty")
	}))
	defer srv.Close()

	_, err := newTestClient(srv).Generate(context.Background(), TextContents("x"), Spec{})
	if err == nil || !strings.Contains(err.Error(), "recipe") {
		t.Fatalf("expected local recipe-required error, got %v", err)
	}
}

func TestRestBaseURL(t *testing.T) {
	tests := []struct {
		name, location, apiEndpoint, want string
		wantErr                           bool
	}{
		{name: "regional", location: "us-central1", want: "https://us-central1-aiplatform.googleapis.com"},
		{name: "global-explicit", location: "global", want: "https://aiplatform.googleapis.com"},
		{name: "empty-is-global", location: "", want: "https://aiplatform.googleapis.com"},
		{name: "valid-endpoint-override", apiEndpoint: "us-east4-aiplatform.googleapis.com", want: "https://us-east4-aiplatform.googleapis.com"},
		{name: "valid-endpoint-with-port", apiEndpoint: "us-west1-aiplatform.googleapis.com:443", want: "https://us-west1-aiplatform.googleapis.com"},
		{name: "hostile-endpoint-rejected", apiEndpoint: "evil.example.com", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := restBaseURL(tc.location, tc.apiEndpoint)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("restBaseURL(%q,%q) = %q, want error", tc.location, tc.apiEndpoint, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("restBaseURL(%q,%q): %v", tc.location, tc.apiEndpoint, err)
			}
			if got != tc.want {
				t.Errorf("restBaseURL(%q,%q) = %q, want %q", tc.location, tc.apiEndpoint, got, tc.want)
			}
		})
	}
}

func TestToRubricGroupsDeclaredOrderAndSkipEmpty(t *testing.T) {
	rubrics := []Rubric{
		mkRubric("clarity crit"),
		mkRubric("   "), // blank -> skipped
		mkRubric("tone crit"),
	}
	got := ToRubricGroups(rubrics, "general_quality")
	want := map[string][]string{"general_quality": {"clarity crit", "tone crit"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ToRubricGroups = %#v, want %#v", got, want)
	}
}

func TestToRubricGroupsEmptyWhenNoCriteria(t *testing.T) {
	got := ToRubricGroups([]Rubric{mkRubric(""), mkRubric("  ")}, "g")
	if len(got) != 0 {
		t.Fatalf("ToRubricGroups = %#v, want empty map", got)
	}
}

func TestDecodeErrorEmptyBody(t *testing.T) {
	err := decodeError(http.StatusForbidden, nil)
	if err == nil || !strings.Contains(err.Error(), "empty body") {
		t.Fatalf("decodeError(empty) = %v, want empty-body message", err)
	}
}

func mkRubric(desc string) Rubric {
	r := Rubric{}
	r.Content.Property.Description = desc
	return r
}
