// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package harness

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// The committed artifacts a measurement may read, by repo-relative path.
//
// Every one of these is a generator's output committed to the tree, which
// is what lets the whole registry sweep offline. Reading the committed copy
// rather than regenerating is deliberate and matches what the ratchets this
// package absorbed already did: the drift tests that tie each artifact to
// its inputs live beside their generators, and a ratchet that regenerated
// would be measuring the tree rather than what shipped.
const (
	SurveyFullJSON  = "live/survey-full.json"
	MappingJSON     = "live/mapping.json"
	ConvergenceJSON = "live/rowgen-convergence.json"
	AnnotationsJSON = "tools/row-gen/annotations.json"
	RejectedJSON    = "tools/row-gen/rejected.json"
	CorpusJSON      = "live/corpus-refusals.json"
)

// readJSON decodes a committed artifact into v, caching the decoded value
// under key so several entries reading the same file parse it once.
func readJSON[T any](r *Repo, rel string, key string) (*T, error) {
	if v, ok := r.cache[key]; ok {
		if t, ok := v.(*T); ok {
			return t, nil
		}
		return nil, fmt.Errorf("cache key %q holds a %T, not a %T", key, v, new(T))
	}
	data, err := os.ReadFile(filepath.Clean(r.Path(rel)))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", rel, err)
	}
	var out T
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("decoding %s: %w", rel, err)
	}
	r.cache[key] = &out
	return &out, nil
}

// SurveyFull is live/survey-full.json, tools/survey-gen's record of the
// pinned provider's own GetProviderSchema response. It is the external
// roster most of this registry is held against: nothing in the fork's
// admission machinery contributes a type to it.
type SurveyFull struct {
	Provider        string `json:"provider"`
	ProviderVersion string `json:"provider_version"`
	Counts          struct {
		Types int `json:"types"`
	} `json:"counts"`
	Types []struct {
		Type    string `json:"type"`
		Signals struct {
			Taggable bool `json:"taggable"`
		} `json:"signals"`
	} `json:"types"`
}

// Survey reads and self-checks live/survey-full.json.
//
// The self-check is the artifact's own counts.types against the length of
// its own type list. An artifact whose header disagrees with its body is
// stale in a way no consumer would otherwise notice, and this roster is
// the denominator of two entries.
func (r *Repo) Survey() (*SurveyFull, error) {
	s, err := readJSON[SurveyFull](r, SurveyFullJSON, "survey-full")
	if err != nil {
		return nil, err
	}
	if len(s.Types) != s.Counts.Types {
		return nil, fmt.Errorf("%s lists %d types but its own counts.types says %d; one of the two is stale",
			SurveyFullJSON, len(s.Types), s.Counts.Types)
	}
	return s, nil
}

// SurveyTypes is the provider's type roster as a set.
func (r *Repo) SurveyTypes() (map[string]bool, error) {
	s, err := r.Survey()
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(s.Types))
	for _, e := range s.Types {
		out[e.Type] = true
	}
	if len(out) != len(s.Types) {
		return nil, fmt.Errorf("%s lists a type twice: %d entries, %d distinct", SurveyFullJSON, len(s.Types), len(out))
	}
	return out, nil
}

// Mapping is live/mapping.json's summary and roster.
type Mapping struct {
	Counts struct {
		Types        int `json:"types"`
		Unclassified int `json:"unclassified"`
	} `json:"counts"`
	Rows []struct {
		TFType string  `json:"tf_type"`
		Via    string  `json:"via"`
		Note   *string `json:"note"`
	} `json:"rows"`
}

// Mapping reads live/mapping.json.
func (r *Repo) Mapping() (*Mapping, error) { return readJSON[Mapping](r, MappingJSON, "mapping") }

// Convergence is live/rowgen-convergence.json's summary.
//
// Named fields only: this reads the artifact as a consumer, and adding a
// field to the artifact must not change what this package measures.
type Convergence struct {
	Summary struct {
		AdmittedTotal         int `json:"admitted_total"`
		Compared              int `json:"compared"`
		NotInMappedSet        int `json:"not_in_mapped_set"`
		AdoptedUnchanged      int `json:"adopted_unchanged"`
		GenuineMismatches     int `json:"genuine_mismatches"`
		Annotated             int `json:"annotated"`
		UnannotatedMismatches int `json:"unannotated_mismatches"`
	} `json:"summary"`
	Types []struct {
		TFType    string `json:"tf_type"`
		Matched   bool   `json:"matched"`
		Annotated bool   `json:"annotated"`
	} `json:"types"`
}

// Convergence reads live/rowgen-convergence.json.
func (r *Repo) Convergence() (*Convergence, error) {
	return readJSON[Convergence](r, ConvergenceJSON, "convergence")
}

// Annotations is tools/row-gen/annotations.json's ruling ledger.
type Annotations struct {
	Rulings map[string]struct {
		Reason string `json:"reason"`
		Exit   string `json:"exit"`
	} `json:"rulings"`
}

// Annotations reads tools/row-gen/annotations.json as plain JSON rather
// than through row-gen's own loader, for the reason
// live/admission_coverage_test.go already gives about rejected.json: this
// is a guard on the file's contents and should not depend on the package it
// guards being able to parse it.
func (r *Repo) Annotations() (*Annotations, error) {
	a, err := readJSON[Annotations](r, AnnotationsJSON, "annotations")
	if err != nil {
		return nil, err
	}
	if len(a.Rulings) == 0 {
		return nil, fmt.Errorf("%s decoded to an empty ledger; the shape this package reads has changed", AnnotationsJSON)
	}
	return a, nil
}

// Rejected is tools/row-gen/rejected.json's veto set.
type Rejected struct {
	Note     string `json:"note"`
	Rejected map[string]struct {
		Reason string `json:"reason"`
	} `json:"rejected"`
}

// Rejected reads tools/row-gen/rejected.json.
func (r *Repo) Rejected() (*Rejected, error) {
	rj, err := readJSON[Rejected](r, RejectedJSON, "rejected")
	if err != nil {
		return nil, err
	}
	if len(rj.Rejected) == 0 {
		return nil, fmt.Errorf("%s decoded to an empty veto set; the shape this package reads has changed", RejectedJSON)
	}
	return rj, nil
}

// Corpus is the part of live/corpus-refusals.json this package reads: which
// analysis layers the artifact says it covered.
//
// All three lists, not two. The artifact grew a third
// ("partially_checked_layers") when [check.PartiallyCheckedLayers] arrived,
// and this struct kept reading two - so an artifact could name a stage as
// partly checked, with a share this package never compared against the code
// that computed it, and the assumption below would still pass. A field a
// reader does not read is a claim nobody holds.
type Corpus struct {
	CheckedLayers   []string        `json:"checked_layers"`
	PartialLayers   []CorpusPartial `json:"partially_checked_layers"`
	UncheckedLayers []string        `json:"unchecked_layers"`
}

// CorpusPartial is one partly-checked stage as the artifact records it. The
// share - how many of the stage's refusals the run computed, out of how many
// the stage has - is the whole content of "partly", so it is pinned rather
// than the layer name alone.
type CorpusPartial struct {
	Layer    string   `json:"layer"`
	Refusals []string `json:"refusals"`
	Total    int      `json:"total"`
}

// Corpus reads live/corpus-refusals.json.
func (r *Repo) Corpus() (*Corpus, error) { return readJSON[Corpus](r, CorpusJSON, "corpus") }
