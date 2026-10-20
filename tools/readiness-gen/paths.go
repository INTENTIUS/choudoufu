// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"strings"

	"github.com/intentius/choudoufu/internal/live/identity"
)

// This file is the one place the survey-path names are spelled (the
// maintainer's ruling of 2026-10-20 on #696, #2134 item 12): how each type
// comes back on a run with no cache. The readiness tier stays the only
// classification in code and data. The path is never stored; it is read
// here, at render time, from the facts the tier was read from, and rendered
// into live/readiness-summary.json for live/READINESS.md's two path tables,
// so the tables cannot drift from the tiers they split.
//
// live/coverage_vocabulary_test.go whitelists this file and no other.

// The paths, in the order the tables list them.
const (
	pathMarker         = "marker"
	pathClientNamed    = "client-named"
	pathParentDerived  = "parent-derived"
	pathAccountDerived = "account-derived"
	pathProjectDerived = "project-derived"
	pathUniqueName     = "unique-name"
	pathServerMinted   = "server-minted"
	pathUnbindable     = "unbindable"
	pathExcluded       = "excluded by design"
)

var (
	awsPathOrder    = []string{pathMarker, pathClientNamed, pathParentDerived, pathAccountDerived, pathUniqueName, pathServerMinted, pathUnbindable, pathExcluded}
	googlePathOrder = []string{pathMarker, pathClientNamed, pathParentDerived, pathProjectDerived, pathUniqueName, pathServerMinted, pathUnbindable}
)

// awsPathMeaning and googlePathMeaning are each table's third column.
var awsPathMeaning = map[string]string{
	pathMarker:         "The `tofu-address` tag on the object is the identity; the sweep reads it back.",
	pathClientNamed:    "The configuration names it, so the name re-derives every run.",
	pathParentDerived:  "Composed over a parent type the configuration also declares.",
	pathAccountDerived: "The configuration plus the run's account or region rebuilds it.",
	pathUniqueName:     "AWS refuses to issue the name twice in an account and region, so discovery binds by name.",
	pathServerMinted:   "The API mints the identity and the object carries no marker; only a record holds it.",
	pathUnbindable:     "No marker, and the declaration does not rebuild it; a record is the only copy.",
	pathExcluded:       "A credential this fork declines to manage.",
}

var googlePathMeaning = map[string]string{
	pathMarker:         "The `tofu-address` label on the object is the identity; the sweep reads it back.",
	pathClientNamed:    "The configuration names it, under a project or location it also names or the provider supplies.",
	pathParentDerived:  "Composed over a containing resource the configuration also names; every IAM type is this shape.",
	pathProjectDerived: "Only project, region or zone names it: one per project or region.",
	pathUniqueName:     "Nothing in the google source states a name the API refuses to issue twice, so no type is here.",
	pathServerMinted:   "The API mints the identity and the type has no labels; only a record holds it.",
	pathUnbindable:     "No row and no identity-schema route; a record is the only copy.",
}

// pathFacts are the per-type facts the paths read beyond a [Row]: for AWS,
// live/survey-full.json's required-for-import identity attributes and
// live/mapping.json's CloudFormation service; for GCP, live/gcp-survey.json's
// parents and scope.
type pathFacts struct {
	awsAttrs     map[string][]string
	awsService   map[string]string
	googleFacts  map[string]googlePathFact
	awsTableType map[string]bool
}

type googlePathFact struct {
	Parents []string
	Scoped  bool
}

func loadPathFacts(root string) (pathFacts, error) {
	var survey struct {
		Types []struct {
			Type     string `json:"type"`
			Identity *struct {
				RequiredForImport []string `json:"required_for_import"`
			} `json:"identity"`
		} `json:"types"`
	}
	if err := decodeJSON(root, SurveyFullJSONRel, &survey); err != nil {
		return pathFacts{}, err
	}
	var mapping struct {
		Rows []struct {
			TFType     string `json:"tf_type"`
			CFNType    string `json:"cfn_type"`
			FoldParent string `json:"fold_parent"`
		} `json:"rows"`
	}
	if err := decodeJSON(root, MappingJSONRel, &mapping); err != nil {
		return pathFacts{}, err
	}
	var gs struct {
		Types []struct {
			Type    string   `json:"type"`
			Parents []string `json:"parents"`
			Scoped  bool     `json:"scoped"`
		} `json:"types"`
	}
	if err := decodeJSON(root, GCPSurveyJSONRel, &gs); err != nil {
		return pathFacts{}, err
	}

	pf := pathFacts{
		awsAttrs:     map[string][]string{},
		awsService:   map[string]string{},
		googleFacts:  map[string]googlePathFact{},
		awsTableType: map[string]bool{},
	}
	for _, t := range survey.Types {
		if t.Identity != nil {
			pf.awsAttrs[t.Type] = t.Identity.RequiredForImport
		}
	}
	for _, r := range mapping.Rows {
		cfn := r.CFNType
		if cfn == "" {
			cfn = r.FoldParent
		}
		if parts := strings.Split(cfn, "::"); len(parts) == 3 {
			pf.awsService[r.TFType] = parts[1]
		}
	}
	for _, t := range gs.Types {
		pf.googleFacts[t.Type] = googlePathFact{Parents: t.Parents, Scoped: t.Scoped}
	}
	// An AWS row is one the table does not mark as another provider's,
	// read off the row rather than off a provider-name prefix.
	for t, ti := range identity.DefaultTable {
		if !ti.NonAWSProvider {
			pf.awsTableType[t] = true
		}
	}
	return pf, nil
}

func (pf pathFacts) serviceOf(t string) (string, bool) {
	s, ok := pf.awsService[t]
	return s, ok
}

// awsPath reads an AWS row's path from its tier and the identity table the
// tier was read from. Tier B splits by what the declaration's identity is
// built from, in the order survey-gen's retired classifier tried them: the
// run's account or region, a name AWS keeps unique, a parent type, and
// otherwise the configuration alone. An admitted server-assigned row with
// none of those is server-minted, whatever its tier. A parent is found by the same
// convention discovery uses (identity.ParentOf over the admitted table, and
// identity.ParentByConvention over the identity schema's attributes for a
// type the table has no row for yet).
func awsPath(r Row, pf pathFacts) string {
	switch r.Tier {
	case TierMarkerCarried:
		return pathMarker
	case TierExcludedByDesign:
		return pathExcluded
	case TierRecordCarried:
		if r.Facts.Markerless {
			return pathServerMinted
		}
		return pathUnbindable
	}
	if entry, ok := identity.DefaultTable[r.Type]; ok {
		for _, c := range entry.Components {
			if c.Cloud != identity.CloudNone {
				return pathAccountDerived
			}
		}
		// A unique-name row is server-assigned too (its id is minted), and
		// the name is what binds it, so this is asked first.
		if entry.UniqueName.Set() {
			return pathUniqueName
		}
		if entry.ServerAssigned {
			return pathServerMinted
		}
		if len(identity.ParentOf(r.Type, pf.awsTableType, pf.serviceOf)) > 0 {
			return pathParentDerived
		}
		return pathClientNamed
	}
	for _, attr := range pf.awsAttrs[r.Type] {
		if _, ok := identity.ParentByConvention(attr, r.Type, pf.awsTableType, pf.serviceOf); ok {
			return pathParentDerived
		}
	}
	return pathClientNamed
}

// googlePath reads a GCP row's path from its tier facts and
// live/gcp-survey.json's parents and scope.
func googlePath(r Row, pf pathFacts) string {
	switch {
	case r.Facts.LabelSurface:
		return pathMarker
	case r.Facts.Markerless:
		return pathServerMinted
	case r.Tier == TierDeclarationCarried:
		f := pf.googleFacts[r.Type]
		switch {
		case len(f.Parents) > 0:
			return pathParentDerived
		case f.Scoped:
			return pathProjectDerived
		}
		return pathClientNamed
	}
	return pathUnbindable
}

// sitePaths tallies rows by path and tier, in order, every path in the
// platform's list appearing at least once (with no tier when no type is
// there) so a path that empties stays visible.
func sitePaths(rows []Row, order []string, meaning map[string]string, path func(Row) string) []sitePath {
	type key struct{ path, tier string }
	counts := map[key]int{}
	seen := map[string]bool{}
	for _, r := range rows {
		p := path(r)
		counts[key{p, r.Tier}]++
		seen[p] = true
	}
	var out []sitePath
	for _, p := range order {
		if !seen[p] {
			out = append(out, sitePath{Path: p, Means: meaning[p]})
			continue
		}
		for _, tier := range tierOrder {
			if n := counts[key{p, tier}]; n > 0 {
				out = append(out, sitePath{Path: p, Tier: tier, Count: n, Means: meaning[p]})
			}
		}
	}
	return out
}

func awsSitePaths(a Artifact, pf pathFacts) []sitePath {
	return sitePaths(a.Types, awsPathOrder, awsPathMeaning, func(r Row) string { return awsPath(r, pf) })
}

func googleSitePaths(a Artifact, pf pathFacts) []sitePath {
	return sitePaths(a.Google.Types, googlePathOrder, googlePathMeaning, func(r Row) string { return googlePath(r, pf) })
}
