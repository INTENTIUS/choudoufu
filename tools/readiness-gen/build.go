// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package main implements tools/readiness-gen, issue #418's join.
//
// the tier definitions (#417) fixes four names - marker-carried,
// declaration-carried, record-carried, excluded by design - and what each
// one means: what recovers a type's identity, and at what cost, when the
// strongest recovery path is gone. It deliberately does not say how many
// types land in each tier; that is this generator's job, computed from
// artifacts already committed rather than argued in prose.
//
// # The join, and why it is not a straight lookup
//
// The ruling's own "Mechanism note for #418" section is explicit that the
// four tiers are not uniformly derivable from generated rosters: tiers A
// through C each have one (live/survey-full.json's taggability signal,
// [identity.MarkerlessTypes]), but tier D's population - the two types a
// maintainer ruling excluded on credential-material grounds - is a
// hand-written list inside internal/live/harness, because "generates
// credential material this fork can never read back" is a judgment call,
// not a schema fact. This generator reads
// [harness.SanctionedCredentialExclusions] rather than re-deriving tier D
// from tools/row-gen/rejected.json's free text, which
// live/derivation_guard_test.go and internal/live/harness's own
// credentialReason doc comment already call the weakest part of that
// ledger.
//
// # What is NOT recoverable from a committed artifact
//
// live/marker_identity_split_test.go's own surveyPathByType comment states
// the fact this generator has to live with: "live/rowgen-buckets.json
// carries only counts, not per-type membership, so which of the two legs
// fired cannot be read off a committed artifact and needs a row-gen run to
// settle." That file only wrote it about one contradiction; it is true of
// the whole artifact. tools/row-gen -emit writes live/rowgen-buckets.json
// as seven aggregate integers (server_assigned, client_named, composite,
// assembled, needs_hand_separator, fold_child, evidence_only) and nothing
// else - no per-type roster survives into the committed tree. So for a
// type that is not yet admitted, not in [identity.MarkerlessTypes] and not
// tier D, this generator cannot ask row-gen's classifier which of those
// seven buckets the type is in; the classifier's answer exists only inside
// a `go run ./tools/row-gen` process.
//
// What it does instead, documented here because the ruling leaves this
// representational choice to this issue: it reads live/survey-full.json's
// own per-type Path column (SURVEY.md's five-token taxonomy, computed from
// the provider's raw schema alone, independent of row-gen's richer,
// doc-scraped classifier) to assign a DESTINED tier to an unratified type -
// "marker" is destined tier A, the four client-suppliable paths are
// destined tier B, and the two untaggable, non-client-suppliable paths
// ("moves to Ops", "enumerable, unbindable") are destined tier C, the same
// shape HANDOFF.md's "696 of 1699 types can be held only by a record" line
// already names. Within that destined-C population, tools/row-gen/rejected.json's
// free-text reason - when the type has one - is read for row-gen's own
// slice vocabulary ("needs hand separator", "no Import section at all", "no
// worked example") to choose between needs-separator, needs-evidence and
// the default, pending-ratification. This is a best-effort text classifier
// over ledger prose written for humans, not a structured field, and it will
// not agree with row-gen's own bucket for every entry - see
// classifyRejectedReason's own comment for a named case where it does not
// (aws_quicksight_folder). A type with no rejected.json entry at all - most
// of the destined-C population, since the ledger holds only 100 of roughly
// five hundred candidates - defaults to pending-ratification: row-gen has
// some proposal for it (rowgen-buckets.json's own "mapped": 1699 says every
// type gets one), no batch has looked at it by name, and that is exactly
// what the status means.
//
// live/mapping.json is read too, and its per-type via/fold_parent columns
// are surfaced in each row's facts (mappingVia, mappingFoldParent) as
// supporting evidence - the ruling's own tier definitions never reference the
// TF-to-CFN mapping directly, so this generator does not let it move a
// tier or status decision, only explain one.
//
// # What is approximated
//
// The ruling's tier C section carves out "the small set already working under
// the located mechanism" (issue #270's ClassRecordLocated) as status
// in-contract rather than pending-mechanism. Whether a markerless type is
// actually located-eligible is [identity.LocatedType], and that function
// needs live provider schemas - condition 2 (the identity attribute is not
// itself a secret) and condition 3 (the identity is recordable in full) are
// both schema reads. Loading a provider is outside this generator's
// declared inputs (survey-full.json, rowgen-buckets.json, the admitted set,
// MarkerlessTypes, rejected.json, mapping.json), so locatedApprox below is
// a static proxy: not vetoed by [identity.NotImportable] (condition 0,
// itself schema-free) and not named in [identity.IDNotProvenWholeTypes]
// (a proxy for condition 3's "recordable in full" - it does not prove
// recordability, it only clears the one committed, generated roster that
// disproves it for a documented-composite type). It does not evaluate
// condition 2 at all, so it can pass a type that a live run would still
// refuse for carrying a secret; internal/live/harness's own
// credentialExclusionsAreTwo ratchet is what keeps that blind spot small,
// since it holds every OTHER credential-reasoned rejected.json entry to
// zero. This is stated once, here, rather than claimed as exact.
//
// [noLocatedIdentityAttrTypes] is the one other schema fact locatedApprox
// now excludes, and it is named rather than derived for the same reason:
// GitHub issue #430's full-population sweep of [identity.MarkerlessTypes]
// (CHOUDOUFU_LIVE_SCHEMAS=1's TestLocatedTypePopulation, 159 types at
// hashicorp/aws 6.59.0, measured 2026-08-29) found three types this
// generator's static proxy passed as in-contract that a live
// [identity.LocatedType] call refuses outright: none of the three exports a
// top-level string "id" at all - the attribute [identity.LocatedIdentityPlanFor]'s
// bare-string fallback reads - so there is nothing for that fallback to
// record, and neither of this generator's other two static proxies catches
// it (each type is importable, and each type's Import section documents no
// composite string for [identity.IDNotProvenWholeTypes] to have caught in
// the first place). See [noLocatedIdentityAttrTypes]'s own doc comment for
// per-type evidence.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/intentius/choudoufu/internal/live/harness"
	"github.com/intentius/choudoufu/internal/live/identity"
)

// The four tier names, spelled exactly as the tier definitions (#417)'s
// "What this settles for #418" section fixes them.
const (
	TierMarkerCarried      = "marker-carried"
	TierDeclarationCarried = "declaration-carried"
	TierRecordCarried      = "record-carried"
	TierExcludedByDesign   = "excluded by design"
)

// The six status tokens issue #418 names.
const (
	StatusInContract          = "in-contract"
	StatusPendingRatification = "pending-ratification"
	StatusNeedsSeparator      = "needs-separator"
	StatusNeedsEvidence       = "needs-evidence"
	StatusPendingMechanism    = "pending-mechanism"
	StatusExcluded            = "excluded"
)

// Committed artifact paths, repo-relative.
const (
	SurveyFullJSONRel = "live/survey-full.json"
	MappingJSONRel    = "live/mapping.json"
	RejectedJSONRel   = "tools/row-gen/rejected.json"
	OutputJSONRel     = "live/readiness.json"
)

// GeneratedBy is the artifact's own generated_by field.
const GeneratedBy = "go run ./tools/readiness-gen"

// noLocatedIdentityAttrTypes is the one hand-verified correction
// classify's locatedApprox proxy needs beyond identity.NotImportable and
// identity.IDNotProvenWholeTypes - see this file's package doc comment,
// "What is approximated", for why the fact cannot be derived from a
// committed roster and has to be named instead, the same way
// harness.SanctionedCredentialExclusions names a live-measured fact this
// generator's other inputs cannot see.
//
// All three are [identity.MarkerlessTypes] members with no wire
// IdentitySchema, no [identity.IDNotProvenWholeTypes] membership (their
// Import sections document no composite string), and no top-level string
// "id" attribute at all - so identity.LocatedIdentityPlanFor's bare-"id"
// default, the last of [identity.LocatedType]'s three schema-read
// conditions, has nothing to read and the type is refused. Measured
// 2026-08-29 (issue #430) against a live hashicorp/aws 6.59.0 pull
// (CHOUDOUFU_LIVE_SCHEMAS=1's TestLocatedTypePopulation,
// internal/live/identity/located_test.go), by the schema each type serves
// instead:
//
//   - aws_apigatewayv2_routing_rule: exports routing_rule_id and
//     routing_rule_arn; no "id".
//   - aws_network_interface_permission: exports
//     network_interface_permission_id; no "id".
//   - aws_notifications_event_rule: exports arn; no "id".
//
// A type here is not vetoed by anything else this generator reads - each is
// importable ([identity.NotImportable] false) and documents no composite
// import ([identity.IDNotProvenWholeTypes] does not name it) - so without
// this list classify would have called all three in-contract, which
// [identity.LocatedType] itself already refuses. Growing this list past a
// fresh measurement of the same test is a live-schema finding, not a
// ratification; shrinking it means [identity.LocatedIdentityPlanFor] grew a
// new source for one of these three types (a ratified row's IdentityAttrs,
// a documented grammar joining DocumentedImportIDs) and the entry should be
// re-verified and dropped rather than left stale.
var noLocatedIdentityAttrTypes = map[string]bool{
	"aws_apigatewayv2_routing_rule":    true,
	"aws_network_interface_permission": true,
	"aws_notifications_event_rule":     true,
}

// Artifact is live/readiness.json's shape: every provider resource type
// this fork's provider roster knows about, tiered and statused exactly
// once, plus the facts that decided it.
type Artifact struct {
	GeneratedBy     string `json:"generated_by"`
	Provider        string `json:"provider"`
	ProviderVersion string `json:"provider_version"`
	Counts          Counts `json:"counts"`
	Types           []Row  `json:"types"`
}

// Counts is the partition summary: every type sorted into exactly one tier
// and exactly one status, so both maps' values sum to Counts.Types.
type Counts struct {
	Types    int            `json:"types"`
	Tiers    map[string]int `json:"tiers"`
	Statuses map[string]int `json:"statuses"`
}

// Row is one provider resource type's readiness verdict.
type Row struct {
	Type   string `json:"type"`
	Tier   string `json:"tier"`
	Status string `json:"status"`
	Facts  Facts  `json:"facts"`
}

// Facts are the input signals that decided Row's tier and status - the
// intersections the ruling's own prose describes (markerless-veto against
// evidence-only, rejected-ledger against buckets) made explicit fields
// rather than left for a reader to re-derive.
type Facts struct {
	// Taggable is live/survey-full.json's signals.taggable: the schema
	// carries a settable top-level tags argument. Tier A's own defining
	// signal.
	Taggable bool `json:"taggable"`

	// SurveyPath is live/survey-full.json's per-type Path column.
	SurveyPath string `json:"survey_path"`

	// Admitted is whether the type is in identity.DefaultTable today - a
	// ratification batch's row exists, so binding it works right now.
	Admitted bool `json:"admitted"`

	// Markerless is whether identity.MarkerlessTypes vetoes the type: the
	// provider mints its identity and it carries no tags argument.
	Markerless bool `json:"markerless"`

	// NotImportable is identity.NotImportable(type): the provider offers no
	// classic Importer at all, independent of taggability or identity
	// shape.
	NotImportable bool `json:"not_importable"`

	// LocatedApprox is set only when Markerless is true. It approximates
	// identity.LocatedType's verdict from static rosters alone - see this
	// file's package doc comment, "What is approximated".
	LocatedApprox bool `json:"located_approx,omitempty"`

	// IDNotProvenWhole is whether identity.IDNotProvenWholeTypes names the
	// type: its documented import is composite and no source proves the
	// exported id attribute holds the whole string.
	IDNotProvenWhole bool `json:"id_not_proven_whole,omitempty"`

	// NoLocatedIdentityAttr is whether [noLocatedIdentityAttrTypes] names
	// the type - see that map's own doc comment. Set only when Markerless
	// is true.
	NoLocatedIdentityAttr bool `json:"no_located_identity_attr,omitempty"`

	// TierD is whether harness.SanctionedCredentialExclusions names the
	// type - the maintainer's hand ruling, not a derived signal.
	TierD bool `json:"tier_d"`

	// Rejected is whether tools/row-gen/rejected.json carries an entry for
	// the type, and RejectedReason is its free-text reason when it has one
	// (some entries carry only recovered_from provenance).
	Rejected       bool   `json:"rejected"`
	RejectedReason string `json:"rejected_reason,omitempty"`

	// MappingVia and MappingFoldParent are live/mapping.json's per-type
	// via and fold_parent columns - supporting evidence surfaced here, not
	// something this generator branches its tier or status on.
	MappingVia        string `json:"mapping_via,omitempty"`
	MappingFoldParent string `json:"mapping_fold_parent,omitempty"`
}

// surveyType is the subset of live/survey-full.json's per-type row this
// generator reads.
type surveyType struct {
	Type    string `json:"type"`
	Path    string `json:"path"`
	Signals struct {
		Taggable bool `json:"taggable"`
	} `json:"signals"`
}

// surveyFull is live/survey-full.json's shape, narrowed to what this
// generator uses.
type surveyFull struct {
	Provider        string `json:"provider"`
	ProviderVersion string `json:"provider_version"`
	Counts          struct {
		Types int `json:"types"`
	} `json:"counts"`
	Types []surveyType `json:"types"`
}

// mappingRow is one row of live/mapping.json this generator reads.
type mappingRow struct {
	TFType     string `json:"tf_type"`
	Via        string `json:"via"`
	FoldParent string `json:"fold_parent"`
}

// mappingArtifact is live/mapping.json's shape, narrowed to what this
// generator uses.
type mappingArtifact struct {
	Rows []mappingRow `json:"rows"`
}

// rejectedEntry is one tools/row-gen/rejected.json entry, narrowed to the
// field this generator reads.
type rejectedEntry struct {
	Reason string `json:"reason,omitempty"`
}

// rejectedArtifact is tools/row-gen/rejected.json's shape.
type rejectedArtifact struct {
	Rejected map[string]rejectedEntry `json:"rejected"`
}

// repoRoot resolves the checkout's root from this file's own location, the
// same trick tools/row-gen's and tools/survey-gen's repoRoot use, so the
// tool runs from any directory.
func repoRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("cannot resolve the repository root: runtime.Caller failed")
	}
	// This file lives at tools/readiness-gen/build.go.
	return filepath.Abs(filepath.Join(filepath.Dir(file), "..", ".."))
}

// loadSurvey reads live/survey-full.json and self-checks its header against
// its own body, the same discipline internal/live/harness's Survey() and
// live/admission_coverage_test.go's providerTypeUniverse both hold it to.
func loadSurvey(root string) (*surveyFull, error) {
	var s surveyFull
	if err := decodeJSON(root, SurveyFullJSONRel, &s); err != nil {
		return nil, err
	}
	if len(s.Types) != s.Counts.Types {
		return nil, fmt.Errorf("%s lists %d types but its own counts.types says %d; one of the two is stale",
			SurveyFullJSONRel, len(s.Types), s.Counts.Types)
	}
	return &s, nil
}

func loadMapping(root string) (map[string]mappingRow, error) {
	var m mappingArtifact
	if err := decodeJSON(root, MappingJSONRel, &m); err != nil {
		return nil, err
	}
	out := make(map[string]mappingRow, len(m.Rows))
	for _, r := range m.Rows {
		out[r.TFType] = r
	}
	return out, nil
}

func loadRejected(root string) (map[string]rejectedEntry, error) {
	var r rejectedArtifact
	if err := decodeJSON(root, RejectedJSONRel, &r); err != nil {
		return nil, err
	}
	if len(r.Rejected) == 0 {
		return nil, fmt.Errorf("%s decoded to an empty veto set; the shape this generator reads has changed", RejectedJSONRel)
	}
	return r.Rejected, nil
}

func decodeJSON(root, rel string, v any) error {
	data, err := os.ReadFile(filepath.Clean(filepath.Join(root, filepath.FromSlash(rel))))
	if err != nil {
		return fmt.Errorf("reading %s: %w", rel, err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("decoding %s: %w", rel, err)
	}
	return nil
}

// Build reads every committed input under root and computes the readiness
// partition. It touches no network and no provider plugin.
func Build(root string) (Artifact, error) {
	survey, err := loadSurvey(root)
	if err != nil {
		return Artifact{}, err
	}
	mapping, err := loadMapping(root)
	if err != nil {
		return Artifact{}, err
	}
	rejected, err := loadRejected(root)
	if err != nil {
		return Artifact{}, err
	}

	tierD := make(map[string]bool, len(harness.SanctionedCredentialExclusions))
	for _, t := range harness.SanctionedCredentialExclusions {
		tierD[t] = true
	}

	rows := make([]Row, 0, len(survey.Types))
	for _, st := range survey.Types {
		m := mapping[st.Type]
		re, hasRejected := rejected[st.Type]
		rows = append(rows, classify(st, m, hasRejected, re, tierD))
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Type < rows[j].Type })

	counts := Counts{
		Types:    len(rows),
		Tiers:    map[string]int{},
		Statuses: map[string]int{},
	}
	for _, r := range rows {
		counts.Tiers[r.Tier]++
		counts.Statuses[r.Status]++
	}

	return Artifact{
		GeneratedBy:     GeneratedBy,
		Provider:        survey.Provider,
		ProviderVersion: survey.ProviderVersion,
		Counts:          counts,
		Types:           rows,
	}, nil
}

// classify is the whole tier/status rule, applied to one type. Precedence,
// fixed by the ruling and by the task this generator was built against:
//
//  1. Tier D overrides everything else, unconditionally.
//  2. An admitted type (identity.DefaultTable) is in-contract, tier A if
//     taggable else B.
//  3. A markerless type (identity.MarkerlessTypes) is tier C,
//     pending-mechanism, unless the located-route approximation says it
//     already works.
//  4. Everything else is destined-tiered from live/survey-full.json's Path
//     column and statused from tools/row-gen/rejected.json's free text
//     (or pending-ratification, the default) - see the package doc comment.
func classify(st surveyType, m mappingRow, hasRejected bool, re rejectedEntry, tierD map[string]bool) Row {
	facts := Facts{
		Taggable:          st.Signals.Taggable,
		SurveyPath:        st.Path,
		NotImportable:     identity.NotImportable(st.Type),
		TierD:             tierD[st.Type],
		Rejected:          hasRejected,
		RejectedReason:    re.Reason,
		MappingVia:        m.Via,
		MappingFoldParent: m.FoldParent,
	}

	if facts.TierD {
		facts.Admitted = false
		return Row{Type: st.Type, Tier: TierExcludedByDesign, Status: StatusExcluded, Facts: facts}
	}

	if _, admitted := identity.DefaultTable[st.Type]; admitted {
		facts.Admitted = true
		tier := TierDeclarationCarried
		if st.Signals.Taggable {
			tier = TierMarkerCarried
		}
		return Row{Type: st.Type, Tier: tier, Status: StatusInContract, Facts: facts}
	}

	if _, markerless := identity.MarkerlessTypes[st.Type]; markerless {
		facts.Markerless = true
		_, idNotProvenWhole := identity.IDNotProvenWholeTypes[st.Type]
		facts.IDNotProvenWhole = idNotProvenWhole
		facts.NoLocatedIdentityAttr = noLocatedIdentityAttrTypes[st.Type]
		locatedApprox := !facts.NotImportable && !idNotProvenWhole && !facts.NoLocatedIdentityAttr
		facts.LocatedApprox = locatedApprox
		if locatedApprox {
			return Row{Type: st.Type, Tier: TierRecordCarried, Status: StatusInContract, Facts: facts}
		}
		return Row{Type: st.Type, Tier: TierRecordCarried, Status: StatusPendingMechanism, Facts: facts}
	}

	tier := destinedTier(st.Signals.Taggable, st.Path)
	status := StatusPendingRatification
	if hasRejected {
		status = classifyRejectedReason(re.Reason)
	}
	return Row{Type: st.Type, Tier: tier, Status: status, Facts: facts}
}

// The seven survey Path tokens live/survey-full.json's own classifier
// produces - see tools/survey-gen/classify.go's pathClientNamed and
// neighboring consts, which this generator does not import (a tools/*-gen
// binary importing another one's package is not this repository's shape;
// the tokens are stable, published vocabulary, SURVEY.md's own contract).
const (
	surveyPathMarker               = "marker"
	surveyPathClientNamed          = "client-named"
	surveyPathParentDerived        = "parent-derived"
	surveyPathAccountDerived       = "account-derived"
	surveyPathUniqueName           = "unique-name"
	surveyPathEnumerableUnbindable = "enumerable, unbindable"
	surveyPathOps                  = "moves to Ops"
)

// destinedTier assigns a not-yet-admitted, not-markerless, not-tier-D
// type's destined tier.
//
// Taggable is the primary and, on its own, sufficient signal for tier A:
// the ruling's own Population paragraph defines tier A as "every taggable
// type: the schema carries a settable top-level tags argument", and
// signals.taggable is computed "by the same predicate
// internal/live/markers.Taggable applies at run time" (the ruling's own tier C
// mechanism note, about the same field). This is deliberately NOT the same
// question as "does live/survey-full.json's Path column say marker": that
// column's own classifier checks client-named evidence before taggability
// (tools/survey-gen/classify.go's priority order), so 61 of the 1699 types
// are taggable and ALSO schema-provably client-named, and Path picks the
// stronger admission-table shape (client-named) for them - which is a fact
// about which ROW a ratification batch would paste, not a fact about
// whether the tag recovers the object when everything else is gone, which
// is what this tier is about. A type in that overlap keeps the taggable
// population's own guarantee (marker-sweep recovery needs no record and no
// surviving configuration) on top of whatever its declaration also
// supplies, so it is tier A here even where Path says otherwise.
//
// For an untaggable type, Path is reliable: survey-gen's classifier only
// reaches the four client-suppliable paths (client-named, parent-derived,
// account-derived, unique-name) for a type it can prove resolves from
// configuration alone, and it never assigns them to a taggable type either
// (they are the paths checked BEFORE marker, so a taggable type reaching
// them is exactly the overlap above). An untaggable type outside those four
// is destined tier C by elimination - the same "moves to Ops" / "enumerable,
// unbindable" population HANDOFF.md's "696 of 1699 types can be held only
// by a record" line names.
func destinedTier(taggable bool, path string) string {
	if taggable {
		return TierMarkerCarried
	}
	switch path {
	case surveyPathClientNamed, surveyPathParentDerived, surveyPathAccountDerived, surveyPathUniqueName:
		return TierDeclarationCarried
	case surveyPathEnumerableUnbindable, surveyPathOps, surveyPathMarker:
		// surveyPathMarker cannot occur here (that path implies taggable),
		// listed only so the switch is exhaustive over every token this
		// package's consts name rather than silently falling to default for
		// one of them.
		return TierRecordCarried
	default:
		// A path token live/survey-full.json has not produced against any
		// pin this generator was built against. Recording it as
		// declaration-carried would be a silent guess; the fallback is the
		// untaggable, no-clean-identity tier - the safer of the four to be
		// wrong in, since it never claims a stronger recovery path than the
		// type might have. TestDestinedTierCoversEverySurveyPath pins the
		// token set so a new one is caught in CI rather than silently
		// defaulted here.
		return TierRecordCarried
	}
}

// classifyRejectedReason is the best-effort text classifier over
// tools/row-gen/rejected.json's free-text reason, described in this file's
// package doc comment. Separator is checked before evidence because
// row-gen's own "needs hand separator" slice framing is the stronger,
// more literal signal, and several entries carry both phrases (a
// hand-separator gap IS an evidence gap - there is no worked example to
// read a separator from).
//
// Known imprecision: aws_quicksight_folder's reason mentions "needs hand
// separator" only as a comparison to nine sibling types while describing a
// live classifier bug (the type's own identity is config-supplied, not
// composite-needing-a-separator); this classifies it needs-separator, which
// is the row-gen slice its siblings are in and not the true story for this
// one type. Left as the documented cost of a text classifier over prose
// written for a human reader rather than for this generator.
func classifyRejectedReason(reason string) string {
	low := strings.ToLower(reason)
	switch {
	case strings.Contains(low, "hand separator"):
		return StatusNeedsSeparator
	case strings.Contains(low, "no import section"),
		strings.Contains(low, "no worked example"),
		strings.Contains(low, "lack of import evidence"),
		strings.Contains(low, "no import evidence"):
		return StatusNeedsEvidence
	default:
		return StatusPendingRatification
	}
}
