// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/intentius/choudoufu/internal/live/identity"
)

// This file is issue #428's remainder ledger:
// tools/row-gen/evidence-schema-gap.json, written by -evidence-gap.
// evidenceschema.go's own [schemaGapClass] already names, per type, why a
// provider identity schema exists for a bucketEvidenceOnly type but does
// not cover it; this file is the OTHER half the issue's own "Do"
// section asks for - "the remainder (evidence-only types with NO identity
// schema) stays ledgered ... with a per-family note on what source would
// actually answer the gap" - grouped exactly the way #427's
// tools/row-gen/separator-evidence.json grouped the needs-hand-separator
// bucket: a new committed review index rather than 300-odd individual
// tools/row-gen/rejected.json entries, because rejected.json's own
// invariant (TestRejectedLedgerIsDisjointFromAdmitted, its own doc
// comment: "types a ratification batch considered and did not admit") is a
// per-type veto with a reason a human actually weighed, and most of this
// population has never been looked at by name at all - the honest fact
// readiness-gen's own StatusPendingRatification already states for exactly
// this shape ("no batch has looked at it by name").
//
// "Per family", not per type, per the issue's own wording: this repository
// tracks membership (which TYPES are in which family) as a generated fact
// - [evidenceSchemaGapFamily] is a pure function of already-committed
// artifacts - and the FAMILY carries the prose about what evidence source
// would close it, so 261 types get six honest paragraphs instead of 261
// near-identical ones.
//
// Two populations, both covered:
//
//   - hasSchemaFamily buckets, by [schemaGapClass], every
//     bucketEvidenceOnly type a provider identity schema exists for that
//     evidenceschema.go's own applySchemaFirstArgName still declined (50 at
//     commit 1eeda7c026).
//   - noSchemaFamily covers the other 261: no survey identity schema at
//     all. [evidenceSchemaGapFamily] partitions it by the strongest signal
//     available, checked in priority order: NotImportable (issue #331,
//     absolute - no evidence of any kind ever closes it), an existing
//     tools/row-gen/rejected.json entry (defer to that entry's own
//     reason), then the readiness tier the type's own facts settle
//     (#696: these families used to be named after survey-gen's path
//     tokens, which were retired).

// evidenceSchemaGapArtifact is tools/row-gen/evidence-schema-gap.json's
// whole shape.
type evidenceSchemaGapArtifact struct {
	GeneratedBy string `json:"generated_by"`
	Note        string `json:"note"`

	// EvidenceOnlySchemaFamilies partitions the 50-ish
	// evidence_only_schema.not_covered population (a provider identity
	// schema exists, applySchemaFirstArgName still declined it) by
	// [schemaGapClass].
	EvidenceOnlySchemaFamilies []evidenceGapFamily `json:"evidence_only_schema_families"`

	// NoIdentitySchemaFamilies partitions the population with no provider
	// identity schema at all by [evidenceSchemaGapFamily].
	NoIdentitySchemaFamilies []evidenceGapFamily `json:"no_identity_schema_families"`

	TotalWithSchemaNotCovered int `json:"total_with_schema_not_covered"`
	TotalNoSchema             int `json:"total_no_schema"`
}

// evidenceGapFamily is one family: what unites its members, how many there
// are, the members themselves (sorted, so this file doubles as the
// membership roster rejected.json-per-type entries would otherwise have to
// carry), and the note - what source would actually answer the gap, named
// rather than left "unknown", per the issue's own instruction.
type evidenceGapFamily struct {
	Family  string   `json:"family"`
	Count   int      `json:"count"`
	Note    string   `json:"note"`
	Members []string `json:"members"`
}

// The evidence_only_schema.not_covered family notes, keyed by
// [schemaGapClass]'s own tokens.
var schemaGapFamilyNotes = map[string]string{
	gapMultiAttribute:     "The provider's identity schema requires more than one attribute for import. row-gen's own client-named row shape (a single Components{Attrs:[name]} entry) cannot carry that; the real shape is identity.TypeIdentity.IdentityObjectOnly (issue #105, identity.SynthesizeTypeIdentity's own multi-attribute branch) - no import-string separator invented, resolution imports by identity object instead. render.go's renderClientNamedEntry and comparison.go's proposedFields build only the single-Attrs shape today. The source that closes this is not new evidence - the schema already names every attribute - it is #105's own row-gen rendering work: extend proposedFields' bucketClientNamed case (or a new bucket) to emit an IdentityObjectOnly row from more than one required attribute.",
	gapTierBTableAsserted: "Tier B by the identity table: internal/live/identity's own entry for the type builds the identity from configuration plus a Cloud-valued component (account_id/region), or binds it by a name AWS documents as unique within the account and region. Both are hand-asserted table facts no provider schema states (issue #218's ruling for the first: the schema cannot tell a client-chosen name from a server-generated one wrapped in an ARN template; tools/row-gen/uniquename.go's crossing of the provider's argument reference and the CloudFormation registry's property description for the second). identity.SynthesizeTypeIdentity and schemafirst.go's own comparison both structurally refuse to build a Cloud-valued Component from a schema alone. Closing it needs a human to read the provider's docs and hand-ratify the slot the same way every other such row was ratified, or - for a unique-name type - the second of the two texts to corroborate uniqueness; no additional schema evidence would change that.",
	gapTierATaggable:      "Tier A: the type is taggable, so marker-sweep recovery binds a live object of this type without a plan-time identity argument, and being evidence-only here is about a different question (does the schema name a CREATE-time argument) than binding. The Components row row-gen would still paste for CREATE/PLAN has no schema-only answer for these - the schema's own required-for-import set failed identity.Derivable's strict client-naming check (most commonly the Optional+Computed cohort: a settable argument the schema cannot prove is client-chosen rather than provider-defaulted). Closing it needs a worked `terraform import` example in the provider's own docs (the source live/import-grammar.json's scrape already reads for other types) or a future provider release marking the attribute Required rather than Optional+Computed.",
	gapTierCRecordCarried: "Tier C: untaggable, and the provider's schemas do not prove the declaration supplies the identity, so nothing but a record holds it once its declaration is gone. The schema exists but its required-for-import set failed the strict client-naming check. Whether a native list resource or a Cloud Control list handler can enumerate the type does not change that: binding reads the two ownership tags, and this type has nowhere to write them. Closing it needs the same worked-Import-example source tier A's family needs (to settle the identity question), and issue #233's mechanism (a marker-capable argument, or a record store) for recovery.",
}

// The no-identity-schema family notes, keyed by [evidenceSchemaGapFamily]'s
// own tokens.
var noSchemaFamilyNotes = map[string]string{
	"not-importable":      "The provider offers no classic Importer for this type at all (issue #331, identity.NotImportableTypes - tools/survey-gen's own ImportResourceState probe). No identity source - a schema, a docs page, a CloudFormation registry entry - closes this: importability is a runtime fact about whether ImportResourceState itself works, not an inference any evidence artifact in this repository could ever carry. The gap closes only if a future provider release ships an Importer for the type.",
	"already-ledgered":    "Already carries a tools/row-gen/rejected.json entry with its own reason; see that entry rather than duplicating the evidence here. (7 types at this measurement.)",
	gapTierATaggable:      "Tier A, with no provider identity schema at all: the type is taggable, so marker-sweep recovery binds a live object of this type without a plan-time identity argument, and evidence-only here is about a different question (does anything name a CREATE-time argument) than binding. Closing the Components-row gap for CREATE/PLAN needs the same source any ordinary evidence-only type needs: a worked `terraform import` example in the provider's own docs (the live/import-grammar.json scrape's own source), which for this family specifically has not yet been found or does not exist on the page.",
	gapTierBTableAsserted: "Tier B by the identity table, with no identity schema present at all: internal/live/identity's own entry for the type builds the identity from configuration plus a Cloud-valued component, or binds it by an AWS-enforced unique name - hand-asserted facts independent of whether the provider serves an identity schema. row-gen's own registry-based classifier still calls the type evidence-only because it has never been given a fresh proposal carrying that fact. The source that closes this is the table ratification that already exists, not a new extraction.",
	gapTierCRecordCarried: "Tier C, with no provider identity schema: untaggable, and nothing in the provider's schemas names the identity. For the large majority there is also no CloudFormation model to read a primaryIdentifier from (live/mapping.json's own via=cfn-unmodeled/tf-only/deprecated-service/none), and where a native list resource or an unscoped Cloud Control list handler can enumerate the type, nothing can bind what it returns - binding reads the two ownership tags and this type has nowhere to write them (issue #233). This is the least-evidenced population in the whole roster: closing any one of these needs the provider or CloudFormation to publish something new that does not exist in this repository's own artifacts today - a worked `terraform import` example naming the argument (a live/import-grammar.json scrape gap worth checking by hand before assuming the doc has none), a resource identity schema in a future provider release, or a CloudFormation registry entry with a primaryIdentifier. This note is a class-wide default, not a per-type audit - re-check the specific type's own provider doc page before citing this note as proof nothing exists.",
}

// hasSchemaFamilyOrder and noSchemaFamilyOrder fix each artifact's family
// order (largest-signal-first, matching the doc comment's own priority
// list) so the written JSON is stable across regenerations regardless of
// Go's randomized map iteration.
var hasSchemaFamilyOrder = []string{gapMultiAttribute, gapTierBTableAsserted, gapTierATaggable, gapTierCRecordCarried}
var noSchemaFamilyOrder = []string{"not-importable", "already-ledgered", gapTierBTableAsserted, gapTierATaggable, gapTierCRecordCarried}

// evidenceSchemaGapFamily classifies one bucketEvidenceOnly type with NO
// provider identity schema at all, checked in the priority order this
// file's own doc comment states: an absolute veto first (not-importable),
// then deference to an existing ledger entry, then the readiness tier the
// type's own facts settle - the identity table's hand-asserted binding
// (tier B), taggability (tier A), and otherwise tier C. A type the survey
// does not describe at all is tier C: nothing here says otherwise.
func evidenceSchemaGapFamily(typeName string, s surveyEntry, hasSurvey, notImportable, alreadyRejected bool) string {
	switch {
	case notImportable:
		return "not-importable"
	case alreadyRejected:
		return "already-ledgered"
	case tableAssertedBinding(typeName):
		return gapTierBTableAsserted
	case hasSurvey && s.Signals.Taggable:
		return gapTierATaggable
	default:
		return gapTierCRecordCarried
	}
}

// buildEvidenceSchemaGapArtifact is tools/row-gen/evidence-schema-gap.json's
// whole computation. proposals must already carry evidenceschema.go's own
// applySchemaFirstArgName mutation (loadProposals does this), so Covered
// types never appear here - only what that pass left behind.
func buildEvidenceSchemaGapArtifact(proposals []proposal, survey map[string]surveyEntry, rejected map[string]bool) evidenceSchemaGapArtifact {
	hasSchema := map[string][]string{}
	noSchema := map[string][]string{}

	for _, p := range proposals {
		if p.Bucket != bucketEvidenceOnly {
			continue
		}
		s, hasSurvey := survey[p.TFType]
		if hasSurvey && s.Identity != nil {
			class := schemaGapClass(s)
			hasSchema[class] = append(hasSchema[class], p.TFType)
			continue
		}
		family := evidenceSchemaGapFamily(p.TFType, s, hasSurvey, identity.NotImportable(p.TFType), rejected[p.TFType])
		noSchema[family] = append(noSchema[family], p.TFType)
	}

	art := evidenceSchemaGapArtifact{
		Note: "Issue #428's remainder ledger. EvidenceOnlySchemaFamilies partitions " +
			"every bucketEvidenceOnly type a provider identity schema exists for that " +
			"evidenceschema.go's applySchemaFirstArgName still declined, by schemaGapClass. " +
			"NoIdentitySchemaFamilies partitions the rest of " +
			"the bucketEvidenceOnly population (live/survey-full.json serves no identity schema for " +
			"the type at all) by evidenceSchemaGapFamily. Every family's note names what source would " +
			"actually answer the gap, per the issue's own instruction - not merely that one is missing. " +
			"Re-derive membership with `go run ./tools/row-gen -evidence-gap`; both counts move whenever " +
			"the roster, the survey pin, or tools/row-gen/rejected.json move.",
	}

	for _, class := range hasSchemaFamilyOrder {
		members := hasSchema[class]
		if len(members) == 0 {
			continue
		}
		sort.Strings(members)
		art.EvidenceOnlySchemaFamilies = append(art.EvidenceOnlySchemaFamilies, evidenceGapFamily{
			Family: class, Count: len(members), Note: schemaGapFamilyNotes[class], Members: members,
		})
		art.TotalWithSchemaNotCovered += len(members)
	}
	for _, family := range noSchemaFamilyOrder {
		members := noSchema[family]
		if len(members) == 0 {
			continue
		}
		sort.Strings(members)
		art.NoIdentitySchemaFamilies = append(art.NoIdentitySchemaFamilies, evidenceGapFamily{
			Family: family, Count: len(members), Note: noSchemaFamilyNotes[family], Members: members,
		})
		art.TotalNoSchema += len(members)
	}
	return art
}

// evidenceSchemaGapArtifactPath is tools/row-gen/evidence-schema-gap.json's
// own committed path.
const evidenceSchemaGapArtifactPath = "tools/row-gen/evidence-schema-gap.json"

// runEvidenceGap is -evidence-gap's entry point.
func runEvidenceGap(out, errOut *os.File) error {
	root, err := repoRoot()
	if err != nil {
		return err
	}
	proposals, err := loadProposals(root)
	if err != nil {
		return err
	}
	survey, err := loadSurvey(filepath.Join(root, surveyJSONRel))
	if err != nil {
		return err
	}
	rejected, err := loadRejectedTypes(root)
	if err != nil {
		return err
	}

	art := buildEvidenceSchemaGapArtifact(proposals, survey, rejected)
	art.GeneratedBy = "tools/row-gen -evidence-gap"

	data, err := json.MarshalIndent(art, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.WriteFile(filepath.Join(root, evidenceSchemaGapArtifactPath), data, 0o644); err != nil { //nolint:gosec // a committed artifact, not a secret
		return err
	}

	fmt.Fprintf(errOut, "row-gen: wrote %s\n", evidenceSchemaGapArtifactPath)
	fmt.Fprintf(out, "evidence-schema-gap: %d has-schema-not-covered (in %d families), %d no-schema (in %d families)\n",
		art.TotalWithSchemaNotCovered, len(art.EvidenceOnlySchemaFamilies), art.TotalNoSchema, len(art.NoIdentitySchemaFamilies))
	return nil
}
