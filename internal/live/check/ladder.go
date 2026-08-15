// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package check

import (
	"sort"

	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/lint"
)

// This file is GitHub issue #175: the onboarding ladder, measured per estate
// rather than per refusal, so admission and language work can be priced in
// real deployments unblocked rather than in types or sites.
//
// Everything here is computed for rate-capable populations only (see
// rateCapableOrigins). The ranking-only populations - in-repo fixtures and
// module examples - exist to exercise a variable surface, so a per-entry
// profile over them measures the fixtures; they get no profile, and the
// artifact does not balloon with rows nobody should read.

// OnboardingClass is one rung of #175's ladder: what stands between a
// published deployment and a clean live-check verdict, derived from the
// refusal IDs that fired on it and from nothing else.
//
// The classification rule, verbatim from #175:
//
//   - clean: the configuration loaded and nothing refused it.
//   - backend-only: every finding is state-backend - the documented
//     one-line onboarding edit.
//   - backend-plus-remote-state: findings are a subset of
//     {state-backend, remote-state}, and it is not backend-only. The estate
//     also reads another stack's outputs through terraform_remote_state.
//   - admissions-only: findings are a subset of {state-backend,
//     remote-state, unadmitted-type, logical-resource}, and no higher rung
//     applies. Ratifying the types it declares (or, for a logical type,
//     declaring a record_store) is all that stands in the way.
//   - language-blocked: anything else fired - at least one
//     static-evaluability or structural refusal, the frontier the
//     operational brief names as binding.
//
// OnboardingUnreadable is the one value outside the ladder, and the one not
// derived from refusal IDs: a configuration the loader itself refused has no
// findings to classify, and calling it clean would read "nothing could be
// read" as "nothing refused this" - the exact confusion [Report.Readable]
// exists to prevent.
type OnboardingClass string

const (
	OnboardingClean              OnboardingClass = "clean"
	OnboardingBackendOnly        OnboardingClass = "backend-only"
	OnboardingBackendRemoteState OnboardingClass = "backend-plus-remote-state"
	OnboardingAdmissionsOnly     OnboardingClass = "admissions-only"
	OnboardingLanguageBlocked    OnboardingClass = "language-blocked"
	OnboardingUnreadable         OnboardingClass = "unreadable"
)

// OnboardingClasses is every class in rung order, the order the summary
// table carries them in. Unreadable last: it is not a rung, it is the entry
// the instrument could not see.
func OnboardingClasses() []OnboardingClass {
	return []OnboardingClass{
		OnboardingClean,
		OnboardingBackendOnly,
		OnboardingBackendRemoteState,
		OnboardingAdmissionsOnly,
		OnboardingLanguageBlocked,
		OnboardingUnreadable,
	}
}

// ClassifyOnboarding computes an entry's rung from whether it loaded and the
// set of refusal IDs that fired on it. The IDs named in the rule are lint
// rules; an identity-layer refusal's ID is a prose Summary ("Unresolvable
// identity") and can never collide with a lint rule slug, so the bare ID set
// is enough. See [OnboardingClass] for the rule.
func ClassifyOnboarding(loaded bool, ids []string) OnboardingClass {
	if !loaded {
		return OnboardingUnreadable
	}
	if len(ids) == 0 {
		return OnboardingClean
	}

	backendOnly, backendRemote, admissionsOnly := true, true, true
	for _, id := range ids {
		if id != string(lint.RuleStateBackend) {
			backendOnly = false
		}
		if id != string(lint.RuleStateBackend) && id != string(lint.RuleRemoteState) {
			backendRemote = false
		}
		switch id {
		case string(lint.RuleStateBackend), string(lint.RuleRemoteState),
			string(lint.RuleUnadmittedType), string(lint.RuleLogicalResource):
		default:
			admissionsOnly = false
		}
	}
	switch {
	case backendOnly:
		return OnboardingBackendOnly
	case backendRemote:
		return OnboardingBackendRemoteState
	case admissionsOnly:
		return OnboardingAdmissionsOnly
	}
	return OnboardingLanguageBlocked
}

// EntryProfile is one rate-capable entry's per-estate section: what refused
// it, whether each refusal is an artifact of unset variables, which of its
// own declared types are unadmitted, and the rung all of that puts it on.
type EntryProfile struct {
	// Class is the entry's [OnboardingClass].
	Class OnboardingClass `json:"onboarding_class"`

	// Refusals is every refusal that fired on this entry, sorted by layer
	// then ID.
	Refusals []EntryRefusal `json:"refusals,omitempty"`

	// UnadmittedTypes are the resource types this entry's own configuration
	// declares that the checker refused as unadmitted, sorted.
	//
	// They are read from the lint-layer unadmitted-type finding's per-site
	// types, which is the loaded configuration seen through the same
	// admission judgment (generated table plus provider identity schemas)
	// the verdict itself used - not a regex over files. That choice bounds
	// what the list can cover in both directions. Covered: the root module
	// and every child module the loader resolved locally, so module-sourced
	// resources within the tree are counted. Not covered: resources inside
	// module calls the loader could not read without installing them (the
	// entry's unresolved_modules count bounds that), and .tf files on disk
	// that no module block references - the loader never reads them, and
	// neither does any live-path verdict.
	UnadmittedTypes []string `json:"unadmitted_types,omitempty"`
}

// EntryRefusal is one refusal's row in an [EntryProfile].
type EntryRefusal struct {
	Layer Layer  `json:"layer"`
	ID    string `json:"id"`

	// Sites is how many places it fired in this entry.
	Sites int `json:"sites"`

	// UnsetVarOnly is true when every one of this refusal's sites references
	// a required root variable that had no value - the whole refusal may be
	// an artifact of running without the operator's tfvars rather than a
	// fact about the configuration. Set only when the caller ran
	// [Report.AttributeUnsetVariables] before folding the report in; false
	// otherwise, which understates rather than invents.
	UnsetVarOnly bool `json:"unset_var_only,omitempty"`

	// Categories breaks this refusal's sites down by reference-subject
	// category (see [configs.ReferenceCategory]) - populated only for
	// [configs.StaticValidateReferences]'s refusals, and omitted entirely
	// when none of this refusal's sites carried one, so every other rule's
	// row in the artifact stays exactly as small as it was before #178.
	Categories map[configs.ReferenceCategory]int `json:"categories,omitempty"`
}

// LadderSummary is the artifact's roll-up over every profiled entry: #175's
// ladder table and its usage-weighted admission shortlist, regenerated with
// the corpus instead of hand-computed from a scratch run.
type LadderSummary struct {
	// Origins are the rate-capable origins the summary is computed over.
	Origins []string `json:"origins"`

	// Classes counts profiled entries per rung, in rung order, zeros
	// included so the shape is stable across regenerations.
	Classes []ClassCount `json:"classes"`

	// UnadmittedDemand is the admission shortlist: each unadmitted type,
	// with how many profiled entries declare it, sorted by that count and
	// then by name. Demand is counted over every profiled entry - a
	// language-blocked estate's types still count, because admitting them
	// is still work that estate needs.
	UnadmittedDemand []TypeDemand `json:"unadmitted_demand,omitempty"`
}

// ClassCount is one rung's share of the profiled entries.
type ClassCount struct {
	Class   OnboardingClass `json:"class"`
	Configs int             `json:"configs"`
}

// TypeDemand is one unadmitted type's demand row: how many profiled entries
// declare it. Configs, not sites - "how many real deployments need this",
// the number #175 ranks admission work by.
type TypeDemand struct {
	Type    string `json:"type"`
	Configs int    `json:"configs"`
}

// profileReport builds one entry's [EntryProfile] from its report. Callers
// gate on rate capability; this only reads the report.
func profileReport(report Report) *EntryProfile {
	profile := &EntryProfile{
		Class: ClassifyOnboarding(report.Readable(), refusalIDs(report.Findings)),
	}

	types := map[string]bool{}
	for _, f := range report.Findings {
		er := EntryRefusal{
			Layer:        f.Layer,
			ID:           f.ID,
			Sites:        len(f.Sites),
			UnsetVarOnly: len(f.Sites) > 0 && f.UnsetVarSites == len(f.Sites),
		}
		for _, site := range f.Sites {
			if site.Category == "" {
				continue
			}
			if er.Categories == nil {
				er.Categories = map[configs.ReferenceCategory]int{}
			}
			er.Categories[site.Category]++
		}
		profile.Refusals = append(profile.Refusals, er)
		if f.Layer == LayerLint && f.ID == string(lint.RuleUnadmittedType) {
			for _, tc := range f.Types() {
				types[tc.Type] = true
			}
		}
	}
	sort.Slice(profile.Refusals, func(i, j int) bool {
		a, b := profile.Refusals[i], profile.Refusals[j]
		if a.Layer != b.Layer {
			return a.Layer < b.Layer
		}
		return a.ID < b.ID
	})
	profile.UnadmittedTypes = sortedKeys(types)
	return profile
}

func refusalIDs(findings []Finding) []string {
	out := make([]string, 0, len(findings))
	for _, f := range findings {
		out = append(out, f.ID)
	}
	return out
}

// ladderSummary folds every profiled entry into the [LadderSummary], or
// returns nil when nothing was profiled - a corpus with no rate-capable
// population carries no ladder rather than an empty one.
func ladderSummary(entries []CorpusEntry) *LadderSummary {
	byClass := map[OnboardingClass]int{}
	demand := map[string]int{}
	origins := map[string]bool{}
	profiled := 0

	for _, entry := range entries {
		if entry.Profile == nil {
			continue
		}
		profiled++
		origins[entry.Origin] = true
		byClass[entry.Profile.Class]++
		for _, typ := range entry.Profile.UnadmittedTypes {
			demand[typ]++
		}
	}
	if profiled == 0 {
		return nil
	}

	summary := &LadderSummary{Origins: sortedKeys(origins)}
	for _, class := range OnboardingClasses() {
		summary.Classes = append(summary.Classes, ClassCount{Class: class, Configs: byClass[class]})
	}
	for typ, n := range demand {
		summary.UnadmittedDemand = append(summary.UnadmittedDemand, TypeDemand{Type: typ, Configs: n})
	}
	sort.Slice(summary.UnadmittedDemand, func(i, j int) bool {
		a, b := summary.UnadmittedDemand[i], summary.UnadmittedDemand[j]
		if a.Configs != b.Configs {
			return a.Configs > b.Configs
		}
		return a.Type < b.Type
	})
	return summary
}
