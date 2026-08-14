// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package check

import (
	"sort"

	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/lint"
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
// derived from these two and nothing else.
func CheckedLayers() []Layer { return []Layer{LayerLint, LayerIdentity} }

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

	// DocsRef is the shipped document that explains it, in the form both
	// source tables use. Empty means no shipped document describes this
	// refusal, which is a gap those tables already count
	// ([identity.UndocumentedRefusals]) and which a report repeats rather
	// than hides: a user who cannot look it up should be told that is why.
	DocsRef string
}

// Documented reports whether any shipped document explains this refusal.
func (r Refusal) Documented() bool { return r.DocsRef != "" }

// Catalog is every refusal the two checked layers can produce, sorted by
// layer then ID.
//
// It is assembled from [lint.Rules] and [identity.Refusals] on every call,
// which is the property that matters: a refusal cannot be added to either
// package's table and be missing here, and one cannot be listed here that
// neither package has. #114's fourth acceptance criterion is exactly this,
// and it is why the corpus artifact can report the refusals that fired
// nowhere - the interesting end of that table, and one no instrument
// assembled from observed output can ever contain.
//
// The one gap is stated on [lint.Rules]: a lint.Rule constant with no
// ruleInfo entry would be absent from both. GitHub issue #110 tracks it.
func Catalog() []Refusal {
	var out []Refusal

	for _, rule := range lint.Rules() {
		out = append(out, Refusal{
			Layer:   LayerLint,
			ID:      string(rule),
			Title:   rule.Summary(),
			DocsRef: rule.DocsRef(),
		})
	}

	for _, refusal := range identity.Refusals() {
		out = append(out, Refusal{
			Layer:   LayerIdentity,
			ID:      refusal.Summary,
			Title:   refusal.Summary,
			What:    refusal.What,
			DocsRef: refusal.DocsRef,
		})
	}

	sortRefusals(out)
	return out
}

// lookup returns the catalog entry for one layer and ID.
//
// A miss is possible in one direction only: an identity diagnostic whose
// Summary is not in the registry. identity's own TestRefusalsRegistered
// makes that a build-time failure in that package, so reaching it here
// means the two have drifted, and [Analyze] reports the finding with the
// Summary as its own title rather than dropping it. A refusal this
// instrument cannot name is still a refusal the user hit.
func lookup(layer Layer, id string) (Refusal, bool) {
	switch layer {
	case LayerLint:
		rule := lint.Rule(id)
		for _, known := range lint.Rules() {
			if known == rule {
				return Refusal{
					Layer:   LayerLint,
					ID:      id,
					Title:   rule.Summary(),
					DocsRef: rule.DocsRef(),
				}, true
			}
		}
	case LayerIdentity:
		if refusal, ok := identity.LookupRefusal(id); ok {
			return Refusal{
				Layer:   LayerIdentity,
				ID:      refusal.Summary,
				Title:   refusal.Summary,
				What:    refusal.What,
				DocsRef: refusal.DocsRef,
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
