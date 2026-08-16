// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package check

import (
	"sort"

	"github.com/intentius/choudoufu/internal/live/dataread"
	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/lint"
	"github.com/intentius/choudoufu/internal/live/passthrough"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/live/stamp"
)

// Layer is one of the analysis passes this package runs, or one it
// deliberately does not.
type Layer string

const (
	// LayerLint is [lint.CheckWith]: is this configuration inside the
	// stateless subset at all.
	LayerLint Layer = "lint"

	// LayerIdentity is [identity.ResolveWith]: can every managed resource
	// instance's identity be computed from the configuration alone.
	LayerIdentity Layer = "identity"

	// LayerDataread is [dataread.Analyze]: for every data source identity
	// resolution demands, can a live-plan read it before resolution. The
	// analysis is offline - eligibility classification only, no read - so
	// this instrument can run it without breaking its no-cloud-calls
	// contract; the read itself remains a plan-time act this instrument
	// did not perform, which is why an eligible finding is not "clean".
	LayerDataread Layer = "dataread"

	// LayerStamp is internal/live/stamp, which rewrites resource bodies to
	// carry ownership markers. Not run here: its refusals are about what a
	// provider's schema says is taggable and what a live object already
	// carries.
	LayerStamp Layer = "stamp"

	// LayerDiscovery is internal/live/discovery, which lists live objects
	// and binds the ones carrying this estate's markers. Not run here: it
	// is the cloud read this package exists to avoid.
	LayerDiscovery Layer = "discovery"

	// LayerProjection is internal/live/projection, which materializes prior
	// state from what discovery bound. Not run here for the same reason.
	LayerProjection Layer = "projection"
)

// CheckedLayers are the passes [Analyze] runs. Everything a report says is
// derived from these three and nothing else.
func CheckedLayers() []Layer { return []Layer{LayerLint, LayerIdentity, LayerDataread} }

// UncheckedLayers are the live-path stages a configuration still has to
// survive that [Analyze] cannot see, because each of them needs a cloud.
//
// This list is the reason a clean report is a narrow claim rather than a
// promise. It is asserted against the packages that actually exist by
// TestLayersClassifyEveryLivePackage, so a new stage cannot appear in
// internal/live without someone deciding whether this instrument sees it.
func UncheckedLayers() []Layer {
	return []Layer{LayerDiscovery, LayerProjection, LayerStamp}
}

// Refusal is one thing the live path can refuse, in a shape that does not
// care which package produced it.
//
// Both source tables already carry these fields under their own names, and
// this type exists only so that a report can rank a lint rule against an
// identity refusal in one list. Nothing here is authored: see [Catalog].
type Refusal struct {
	// Layer is which pass produces it.
	Layer Layer

	// ID is the refusal's stable identity: a [lint.Rule] string, or the
	// Summary an identity diagnostic carries. Reports group and rank on
	// this, never on message text.
	ID string

	// Title is the one-line summary a user sees at the head of the
	// refusal: [lint.Rule.Summary] for a lint rule, the Summary itself for
	// an identity refusal, which is already written as one.
	Title string

	// What describes the configuration shape that trips it, where the
	// source table has one. [identity.Refusal.What] fills this; lint keeps
	// its equivalent per-issue rather than per-rule, so a lint refusal
	// leaves it empty and the report shows the first site's detail
	// instead.
	What string

	// DocsRef is the shipped document that explains it, in the form all
	// three source tables use.
	//
	// It was once empty for most identity refusals, and the count of empties
	// was how the repository measured its own documentation gap. Since #110
	// every refusal has an entry in live/LIMITATIONS.md - generated from
	// these same tables - so the field is always set and the check that
	// matters is whether the entry it names exists. That is
	// TestEveryRefusalDocsRefIsResolvable, which is a stronger contract than
	// counting empties ever was: a reference to a heading nobody wrote used
	// to pass.
	DocsRef string

	// RaisedBy is the import path of the package that constructs the
	// diagnostic.
	//
	// For the two live registries it restates [Refusal.Layer] and carries
	// nothing new. It exists for the third: a pass-through refusal surfaces
	// during identity resolution but is written by internal/configs' static
	// evaluator, internal/addrs' reference parser or HCL itself, and that
	// distinction is the whole reason those refusals were invisible to
	// every instrument until #110. A reader of the artifact should be able
	// to see it without knowing the history.
	RaisedBy string
}

// The live registries' RaisedBy values. They are import paths rather than
// layer names because the pass-through registry's values are import paths -
// internal/configs, internal/addrs, hcl - and a column mixing "identity"
// with "internal/configs" reads as two different kinds of fact.
const (
	RaisedByLint       = "internal/live/lint"
	RaisedByIdentity   = "internal/live/identity"
	RaisedByDataread   = "internal/live/dataread"
	RaisedByStamp      = "internal/live/stamp"
	RaisedByDiscovery  = "internal/live/discovery"
	RaisedByProjection = "internal/live/projection"
)

// livePackages is every RaisedBy value belonging to this fork. Anything else
// is a diagnostic the live path passes through from upstream.
//
// It is a set rather than the negation of two constants. The first version
// of [Refusal.Passthrough] tested "not lint and not identity", and when
// stamp and discovery were added with their import paths written out at the
// call site rather than as constants, both classified as pass-through - so
// five entries in the generated live/LIMITATIONS.md told the reader that
// internal/live/stamp shows a diagnostic it did not write, which is the
// opposite of the fact and the exact premise of the commit that added them.
// Found by an adversarial audit.
var livePackages = map[string]bool{
	RaisedByLint:       true,
	RaisedByIdentity:   true,
	RaisedByDataread:   true,
	RaisedByStamp:      true,
	RaisedByDiscovery:  true,
	RaisedByProjection: true,
}

// Passthrough reports whether this refusal is one the live path shows a user
// without having written it. See internal/live/passthrough.
func (r Refusal) Passthrough() bool {
	return r.RaisedBy != "" && !livePackages[r.RaisedBy]
}

// Documented reports whether any shipped document explains this refusal.
func (r Refusal) Documented() bool { return r.DocsRef != "" }

// Catalog is every refusal the two checked layers can produce, sorted by
// layer then ID.
//
// It is assembled from [lint.Rules], [identity.Refusals] and
// [passthrough.Refusals] on every call, which is the property that matters:
// a refusal cannot be added to any of the three tables and be missing here,
// and one cannot be listed here that none of them has. #114's fourth
// acceptance criterion is exactly this, and it is why the corpus artifact
// can report the refusals that fired nowhere - the interesting end of that
// table, and one no instrument assembled from observed output can ever
// contain.
//
// The third source is GitHub issue #110's work. Before it, the largest
// single blocker in the corpus was in no table at all, and neither were two
// more of the top seven: they are diagnostics identity resolution passes
// through from the static evaluator, so a document generated from the two
// live registries alone omitted the top of its own list. They are
// catalogued under [LayerIdentity], because that is the pass a user hits
// them in, with [Refusal.RaisedBy] naming the package that actually wrote
// them.
func Catalog() []Refusal {
	var out []Refusal

	for _, rule := range lint.Rules() {
		out = append(out, Refusal{
			Layer:    LayerLint,
			ID:       string(rule),
			Title:    rule.Summary(),
			DocsRef:  rule.DocsRef(),
			RaisedBy: RaisedByLint,
		})
	}

	for _, refusal := range identity.Refusals() {
		out = append(out, Refusal{
			Layer:    LayerIdentity,
			ID:       refusal.Summary,
			Title:    refusal.Summary,
			What:     refusal.What,
			DocsRef:  refusal.DocsRef(),
			RaisedBy: RaisedByIdentity,
		})
	}

	for _, refusal := range passthrough.Refusals() {
		out = append(out, Refusal{
			Layer:    LayerIdentity,
			ID:       refusal.Summary,
			Title:    refusal.Summary,
			What:     refusal.What,
			DocsRef:  refusal.DocsRef(),
			RaisedBy: string(refusal.Origin),
		})
	}

	// The fourth source is issue #179's data-read phase. Its findings are
	// checked - the eligibility analysis is offline - so its registry
	// belongs in the ranked catalog, not only in [AllRefusals].
	for _, refusal := range dataread.Refusals() {
		out = append(out, Refusal{
			Layer:    LayerDataread,
			ID:       refusal.Summary,
			Title:    refusal.Summary,
			What:     refusal.What,
			DocsRef:  refusal.DocsRef(),
			RaisedBy: RaisedByDataread,
		})
	}

	sortRefusals(out)
	return out
}

// AllRefusals is every refusal the whole live path can produce, including
// the three stages this instrument cannot run.
//
// [Catalog] is deliberately narrower, and the two must not be conflated. A
// zero in the corpus artifact means "measured over 105 configurations and
// blocked none of them", which is a finding. A stamping refusal has never
// been measured by anything, because measuring it needs a cloud, and giving
// it a zero in the same column would turn "unknown" into "harmless" - the
// exact confusion the artifact's checked/unchecked layer lists exist to
// prevent. So the corpus ranks [Catalog], and this is what documentation is
// generated from.
//
// #110's first acceptance criterion is every hard refusal in the live path,
// which is this set rather than that one.
func AllRefusals() []Refusal {
	out := Catalog()

	for _, refusal := range stamp.Refusals() {
		out = append(out, Refusal{
			Layer:    LayerStamp,
			ID:       refusal.Summary,
			Title:    refusal.Summary,
			What:     refusal.What,
			DocsRef:  refusal.DocsRef(),
			RaisedBy: RaisedByStamp,
		})
	}

	for _, refusal := range discovery.Refusals() {
		out = append(out, Refusal{
			Layer:    LayerDiscovery,
			ID:       refusal.Summary,
			Title:    refusal.Summary,
			What:     refusal.What,
			DocsRef:  refusal.DocsRef(),
			RaisedBy: RaisedByDiscovery,
		})
	}

	for _, refusal := range projection.Refusals() {
		out = append(out, Refusal{
			Layer:    LayerProjection,
			ID:       refusal.Summary,
			Title:    refusal.Summary,
			What:     refusal.What,
			DocsRef:  refusal.DocsRef(),
			RaisedBy: RaisedByProjection,
		})
	}

	sortRefusals(out)
	return out
}

// LayersWithRegistries is every [Layer] [AllRefusals] draws from.
//
// It exists so that TestEveryLayerHasARegistry can compare it against
// CheckedLayers and UncheckedLayers. The first version of AllRefusals added
// stamp and discovery and stopped, describing them in a commit message as
// "the other two" when UncheckedLayers had always returned three - so
// projection's twenty-six refusals stayed in no table, AllRefusals was
// smaller than its own doc comment claimed, and nothing said so. A list an
// author can forget to extend is exactly the shape that needs the test.
func LayersWithRegistries() []Layer {
	return []Layer{LayerLint, LayerIdentity, LayerDataread, LayerStamp, LayerDiscovery, LayerProjection}
}

// lookup returns the catalog entry for one layer and ID.
//
// A miss is still possible - a diagnostic reaching a user under a Summary
// none of the three registries has - and [Analyze] reports such a finding
// with the Summary as its own title rather than dropping it. A refusal this
// instrument cannot name is still a refusal the user hit, and
// live/corpus-refusals.json counts it as unregistered so the gap is a
// number. That number is asserted at zero over the committed corpus by
// TestCorpusArtifactHasNoUnregisteredRefusals; before #110 it was three,
// and those three were the top of the ranking.
func lookup(layer Layer, id string) (Refusal, bool) {
	switch layer {
	case LayerLint:
		rule := lint.Rule(id)
		for _, known := range lint.Rules() {
			if known == rule {
				return Refusal{
					Layer:    LayerLint,
					ID:       id,
					Title:    rule.Summary(),
					DocsRef:  rule.DocsRef(),
					RaisedBy: RaisedByLint,
				}, true
			}
		}
	case LayerIdentity:
		if refusal, ok := identity.LookupRefusal(id); ok {
			return Refusal{
				Layer:    LayerIdentity,
				ID:       refusal.Summary,
				Title:    refusal.Summary,
				What:     refusal.What,
				DocsRef:  refusal.DocsRef(),
				RaisedBy: RaisedByIdentity,
			}, true
		}
		if refusal, ok := passthrough.LookupRefusal(id); ok {
			return Refusal{
				Layer:    LayerIdentity,
				ID:       refusal.Summary,
				Title:    refusal.Summary,
				What:     refusal.What,
				DocsRef:  refusal.DocsRef(),
				RaisedBy: string(refusal.Origin),
			}, true
		}
	case LayerDataread:
		if refusal, ok := dataread.LookupRefusal(id); ok {
			return Refusal{
				Layer:    LayerDataread,
				ID:       refusal.Summary,
				Title:    refusal.Summary,
				What:     refusal.What,
				DocsRef:  refusal.DocsRef(),
				RaisedBy: RaisedByDataread,
			}, true
		}
	}
	return Refusal{Layer: layer, ID: id, Title: id}, false
}

func sortRefusals(in []Refusal) {
	sort.Slice(in, func(i, j int) bool {
		if in[i].Layer != in[j].Layer {
			return in[i].Layer < in[j].Layer
		}
		return in[i].ID < in[j].ID
	})
}
