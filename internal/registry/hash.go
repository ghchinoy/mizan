package registry

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// contentHash returns a deterministic SHA-256 fingerprint over a template's
// canonicalized identity + spec (design §3.6). It is used for drift detection,
// import no-op short-circuiting, and the future remote change signal.
//
// EXCLUDED by construction: the provenance/lifecycle fields that are not part of
// "what the template is" — Source, ContentHash (the hash cannot include itself),
// Dirty, CreatedAt, UpdatedAt, ImportedAt. Everything else (the full current
// spec, including the RFC-0001 additive fields RatingRubric/RubricDetail) is
// covered so a change to any authored field shifts the hash.
//
// Determinism: the canonical view is a fixed-field-order struct (json.Marshal
// emits struct fields in declaration order and sorts map keys), and every slice
// preserves authored order, so the hash is stable across Marshal/Unmarshal
// round-trips. A golden-hash test pins it so a future change that silently
// shifts the hash is caught.
func contentHash(t *MetricTemplate) string {
	type canonAuthor struct {
		Name  string `json:"name"`
		Email string `json:"email"`
	}
	type canonInput struct {
		Name     string `json:"name"`
		Modality string `json:"modality"`
		Required bool   `json:"required"`
	}
	// Field order here is the canonical order; do not reorder without expecting
	// the golden hash to change.
	canon := struct {
		ID                   string                       `json:"id"`
		Name                 string                       `json:"name"`
		Description          string                       `json:"description"`
		Version              string                       `json:"version"`
		Authors              []canonAuthor                `json:"authors"`
		Maintainers          []string                     `json:"maintainers"`
		License              string                       `json:"license"`
		Tags                 []string                     `json:"tags"`
		Kind                 string                       `json:"kind"`
		Modalities           []string                     `json:"modalities"`
		Inputs               []canonInput                 `json:"inputs"`
		MetricPromptTemplate string                       `json:"metricPromptTemplate"`
		SystemInstruction    string                       `json:"systemInstruction"`
		CandidateFieldName   string                       `json:"candidateFieldName"`
		BaselineFieldName    string                       `json:"baselineFieldName"`
		RubricGroups         map[string][]string          `json:"rubricGroups"`
		ResponseSchema       string                       `json:"responseSchema"`
		RatingRubric         map[string]map[string]string `json:"ratingRubric"`
		RubricDetail         *RubricDetail                `json:"rubricDetail"`
		AutoraterModel       string                       `json:"autoraterModel"`
		SamplingCount        int32                        `json:"samplingCount"`
		FlipEnabled          bool                         `json:"flipEnabled"`
	}{
		ID:                   t.ID,
		Name:                 t.Name,
		Description:          t.Description,
		Version:              t.Version,
		Maintainers:          t.Maintainers,
		License:              t.License,
		Tags:                 t.Tags,
		Kind:                 string(t.Kind),
		MetricPromptTemplate: t.MetricPromptTemplate,
		SystemInstruction:    t.SystemInstruction,
		CandidateFieldName:   t.CandidateFieldName,
		BaselineFieldName:    t.BaselineFieldName,
		RubricGroups:         t.RubricGroups,
		RatingRubric:         t.RatingRubric,
		RubricDetail:         t.RubricDetail,
		AutoraterModel:       t.AutoraterModel,
		SamplingCount:        t.SamplingCount,
		FlipEnabled:          t.FlipEnabled,
	}
	for _, a := range t.Authors {
		canon.Authors = append(canon.Authors, canonAuthor(a))
	}
	for _, m := range t.Modalities {
		canon.Modalities = append(canon.Modalities, string(m))
	}
	for _, in := range t.Inputs {
		canon.Inputs = append(canon.Inputs, canonInput{
			Name:     in.Name,
			Modality: string(in.Modality),
			Required: in.Required,
		})
	}
	if t.ResponseSchema != nil {
		canon.ResponseSchema = t.ResponseSchema.JSON
	}

	b, err := json.Marshal(canon)
	if err != nil {
		// The canonical view contains only JSON-encodable types, so Marshal
		// cannot fail in practice; hash the error text if it somehow does so the
		// caller still gets a stable, non-empty value rather than a panic.
		sum := sha256.Sum256([]byte("registry: contentHash marshal error: " + err.Error()))
		return "sha256:" + hex.EncodeToString(sum[:])
	}
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}
