// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package check

import (
	"sort"
)

// Corpus folds many [Report]s into the ranked table GitHub issue #102 asks
// for: which refusal fired, in how many configurations, at how many sites.
//
// The ranking is by configurations blocked, not by raw site count. #102's
// own design note settles this and the reason is worth keeping next to the
// code: a raw site count is dominated by whichever configuration happened to
// be largest, so one 400-resource estate with an unadmitted type would
// outrank a refusal that stops every configuration in the corpus dead. Both
// numbers are emitted, because both are cheap and they answer different
// questions - "how many people does this stop" and "how much work is it to
// fix once you are stopped".
type Corpus struct {
	// Entries are the per-configuration results, in the order added.
	Entries []CorpusEntry `json:"entries"`

	// Refusals is every refusal in [Catalog], ranked, including the ones
	// that fired nowhere.
	//
	// Carrying the zeroes is the point of building this from the catalog
	// rather than from what was observed. A refusal that blocks nothing in
	// a corpus of real configurations is evidence about where not to spend
	// the next campaign, and it is invisible to any instrument that only
	// records what it saw fire.
	Refusals []CorpusRefusal `json:"refusals"`

	// Totals are the corpus-wide counts.
	Totals CorpusTotals `json:"totals"`

	// Checked and Unchecked are the layers behind every number above,
	// repeated here so the artifact carries its own scope. See
	// [UncheckedLayers].
	Checked   []Layer `json:"checked_layers"`
	Unchecked []Layer `json:"unchecked_layers"`
}

// CorpusEntry is one configuration's line in the table.
type CorpusEntry struct {
	// Name identifies the configuration, as the runner was told it.
	Name string `json:"name"`

	// Blocked is whether anything refused it.
	Blocked bool `json:"blocked"`

	// Loaded is whether the configuration could be read at all. A false
	// here is itself a measurement (see [Load]), not a skipped row.
	Loaded bool `json:"loaded"`

	// LoadError is the first load diagnostic, when Loaded is false.
	LoadError string `json:"load_error,omitempty"`

	// Instances is how many managed resource instances resolved.
	Instances int `json:"instances"`

	// Sites is how many refused sites it had.
	Sites int `json:"sites"`

	// UnresolvedModules is how many module calls could not be read without
	// installing them. Their contents are not measured in this row.
	UnresolvedModules int `json:"unresolved_modules"`

	// UnsetVariables is how many required root variables had no value.
	UnsetVariables int `json:"unset_variables"`

	// Schemas is whether provider schemas were available for this entry.
	Schemas bool `json:"schemas"`

	// Shadowed is how many identity refusals landed on a construct lint
	// had already refused. See [Report.Shadowed].
	Shadowed int `json:"shadowed,omitempty"`
}

// CorpusRefusal is one refusal's row in the ranked table.
type CorpusRefusal struct {
	Layer Layer  `json:"layer"`
	ID    string `json:"id"`
	Title string `json:"title"`

	// What describes the shape that trips it, where the source table has
	// one.
	What string `json:"what,omitempty"`

	// DocsRef is the shipped document explaining it. Empty is a documented
	// gap, not an oversight here; see [Refusal.DocsRef].
	DocsRef string `json:"docs_ref,omitempty"`

	// Configs is how many corpus configurations this refusal blocked, and
	// the number the table is ranked by.
	Configs int `json:"configs"`

	// Sites is how many places it fired across the whole corpus.
	Sites int `json:"sites"`

	// Types is the per-resource-type breakdown, for the type-shaped rules
	// only. See [Finding.Types].
	Types []TypeCount `json:"types,omitempty"`

	// Examples are a few "file:line" sites, for a reader who wants to see
	// one. Capped: this artifact is a work queue, not a log.
	Examples []string `json:"examples,omitempty"`

	// Registered is false for a refusal that fired but is in neither
	// source table.
	//
	// These exist. identity's TestRefusalsRegistered scans that package's
	// own source for Summary literals, so a diagnostic raised by the
	// static evaluator and passed through - "Unknown variable", for one -
	// reaches a user as a refusal while appearing in no registry. Counting
	// them here is how the gap becomes a number instead of a surprise;
	// closing it is GitHub issue #110's.
	Registered bool `json:"registered"`
}

// CorpusTotals are the corpus-wide counts.
type CorpusTotals struct {
	Configs        int `json:"configs"`
	Loaded         int `json:"loaded"`
	Blocked        int `json:"blocked"`
	Clean          int `json:"clean"`
	Instances      int `json:"instances"`
	Sites          int `json:"sites"`
	RefusalsFired  int `json:"refusals_fired"`
	RefusalsInSet  int `json:"refusals_in_set"`
	WithoutSchemas int `json:"configs_without_schemas"`

	// Unregistered is how many refusals fired that are in neither source
	// table. See [CorpusRefusal.Registered].
	Unregistered int `json:"refusals_unregistered"`
}

// maxExamples is how many "file:line" sites each refusal row carries.
const maxExamples = 5

// NewCorpus starts an empty corpus fold.
func NewCorpus() *Corpus {
	return &Corpus{
		Checked:   CheckedLayers(),
		Unchecked: UncheckedLayers(),
	}
}

// Add folds one configuration's report in under the given name.
func (c *Corpus) Add(name string, report Report) {
	entry := CorpusEntry{
		Name:              name,
		Blocked:           report.Blocked(),
		Loaded:            report.Load.Config != nil,
		Instances:         report.Instances,
		Sites:             report.Sites(),
		UnresolvedModules: len(report.Load.UnresolvedModules),
		UnsetVariables:    len(report.Load.UnsetVariables()),
		Schemas:           report.Schemas,
		Shadowed:          report.Shadowed,
	}
	if !entry.Loaded && len(report.Load.Diags) > 0 {
		entry.LoadError = report.Load.Diags[0].Error()
	}
	c.Entries = append(c.Entries, entry)

	for _, finding := range report.Findings {
		row := c.row(finding.Refusal)
		row.Configs++
		row.Sites += len(finding.Sites)
		row.Types = mergeTypes(row.Types, finding.Types())
		row.Registered = finding.Registered
		for _, site := range finding.Sites {
			if len(row.Examples) >= maxExamples {
				break
			}
			row.Examples = append(row.Examples, site.location())
		}
	}
}

// row finds or creates the table row for one refusal. A refusal fired but
// absent from the catalog gets a row anyway, so drift between this package
// and identity's registry shows up as an extra row rather than as a silently
// dropped measurement.
func (c *Corpus) row(refusal Refusal) *CorpusRefusal {
	for i := range c.Refusals {
		if c.Refusals[i].Layer == refusal.Layer && c.Refusals[i].ID == refusal.ID {
			return &c.Refusals[i]
		}
	}
	c.Refusals = append(c.Refusals, CorpusRefusal{
		Layer:   refusal.Layer,
		ID:      refusal.ID,
		Title:   refusal.Title,
		What:    refusal.What,
		DocsRef: refusal.DocsRef,
	})
	return &c.Refusals[len(c.Refusals)-1]
}

// Finish fills in the refusals that fired nowhere, computes the totals and
// sorts the table. Call it once, after the last [Corpus.Add].
func (c *Corpus) Finish() {
	for _, refusal := range Catalog() {
		row := c.row(refusal)
		// A catalog entry is registered by definition. Rows created by
		// Add for a refusal that fired keep whatever Add recorded.
		if row.Configs == 0 {
			row.Registered = true
		}
	}

	c.Totals = CorpusTotals{
		Configs:       len(c.Entries),
		RefusalsInSet: len(c.Refusals),
	}
	for _, entry := range c.Entries {
		if entry.Loaded {
			c.Totals.Loaded++
		}
		if entry.Blocked {
			c.Totals.Blocked++
		} else if entry.Loaded {
			c.Totals.Clean++
		}
		if !entry.Schemas {
			c.Totals.WithoutSchemas++
		}
		c.Totals.Instances += entry.Instances
		c.Totals.Sites += entry.Sites
	}
	for _, row := range c.Refusals {
		if row.Configs > 0 {
			c.Totals.RefusalsFired++
			if !row.Registered {
				c.Totals.Unregistered++
			}
		}
	}

	sort.Slice(c.Refusals, func(i, j int) bool {
		a, b := c.Refusals[i], c.Refusals[j]
		if a.Configs != b.Configs {
			return a.Configs > b.Configs
		}
		if a.Sites != b.Sites {
			return a.Sites > b.Sites
		}
		if a.Layer != b.Layer {
			return a.Layer < b.Layer
		}
		return a.ID < b.ID
	})
	sort.Slice(c.Entries, func(i, j int) bool { return c.Entries[i].Name < c.Entries[j].Name })
}

// mergeTypes folds one finding's type breakdown into a row's running one.
func mergeTypes(into []TypeCount, add []TypeCount) []TypeCount {
	if len(add) == 0 {
		return into
	}
	counts := make(map[string]int, len(into)+len(add))
	for _, tc := range into {
		counts[tc.Type] += tc.Sites
	}
	for _, tc := range add {
		counts[tc.Type] += tc.Sites
	}

	out := make([]TypeCount, 0, len(counts))
	for name, n := range counts {
		out = append(out, TypeCount{Type: name, Sites: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Sites != out[j].Sites {
			return out[i].Sites > out[j].Sites
		}
		return out[i].Type < out[j].Type
	})
	return out
}
