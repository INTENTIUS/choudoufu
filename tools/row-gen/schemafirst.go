// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/intentius/choudoufu/internal/live/identity"
)

// This file is issue #387's measurement, ruling 2 of
// the foundation-order ruling (#388): for every config-identified
// ratified row the provider also serves an identity schema for, does the
// schema say the same thing the row does?
//
// It used to also decide which rows to DROP from the emitted table
// (internal/live/identity/table_generated.go,
// internal/live/lint/admission_generated.go), narrowed by two safety nets
// against internal/live/check's identity golden and a hand ledger of
// refusal-probe corpus evidence. That was the wrong shape: a ratchet held
// by whether a gitignored, network-fetched corpus happens to declare a type
// is fixture luck, not a rule, and it only ever bought safety for the exact
// instruments that happened to be run before landing - the golden and the
// default (schema-less) refusal-probe sweep - while doing nothing for a
// caller that DOES supply schemas, which is the case ruling 2 is actually
// about.
//
// So this file now only measures. The ledger (tools/row-gen/ratified.json)
// and the emitted table are both untouched; nothing here removes a row from
// either. What acts on the measurement is the runtime precedence inversion
// in internal/live/identity/resolve.go's lookupType and
// internal/live/lint's admitted() (see admission.go and resolve.go's own
// doc comments): when a caller supplies real provider schemas AND
// [identity.SynthesizeTypeIdentity] reproduces the row by the same
// comparison this file makes, the synthesized entry is used instead of the
// row. A caller with no schemas - internal/live/check's identity golden,
// refusal-probe's default sweep - sees no change at all, because the
// schema branch is never reached. The ledger shrink (deleting a row from
// ratified.json once it is truly redundant) is deferred to #388, after the
// static evaluator this table exists for retires.
//
// # Why the comparison below is offline
//
// tools/row-gen has no provider schema of its own to call
// [identity.SynthesizeTypeIdentity] with - classifyAll's whole pipeline
// (importgrammar.go, survey.go, contentmatch.go) already treats
// live/import-grammar.json and live/survey-full.json as its evidence
// instead of a live schema, for the same reason. identity_schema_required
// is that evidence's own name for the schema's required-for-import
// attribute set, and reproduceSchemaRow below rebuilds, from it alone, the
// same shape [synthesizeTypeIdentity] would: one Component per required
// attribute, Attrs holding that attribute's own name, no separator and no
// cloud slot, because a schema-derived entry has neither. The runtime
// inversion this file feeds re-runs the identical comparison at resolution
// time, over the REAL schemas and the real synthesized entry, so the
// generator's offline approximation only ever decides what gets MEASURED
// here - never, by itself, what a live run does.
//
// # Why "id" is set aside
//
// [identity.SynthesizeTypeIdentity]'s own doc comment: it deliberately never
// adds "id" to IdentityAttrs, because whether a type's id attribute equals
// its import identity is "precisely the inference a schema does not carry".
// Many hand-ratified rows add it anyway, for types where a human already
// knows the two coincide (aws_dynamodb_table's id IS its name). Counting
// that as a disagreement would call every such row unreproduced for a
// reason unrelated to whether the SCHEMA reproduces it, so
// reproduceSchemaRow removes "id" from the ratified claim before comparing.
//
// Measured at df84674046: of 575 config-identified table rows (not
// RecordBacked, not ServerAssigned, not NonAWSProvider - the identity comes
// from Components, same population [identity.SynthesizeTypeIdentity] can
// ever apply to), 161 have a live/import-grammar.json
// identity_schema_required; this file's own rule reproduces 134 of them
// (live/schema-precedence.json's reproduced_count).
// The remaining 27 are the shapes GitHub issue #387 itself names, and
// notReproducedClass below labels each by which one: an ARN-shaped identity
// assembled from region/account/name (aws_sns_topic, aws_sqs_queue,
// aws_ecs_capacity_provider, and aws_s3_account_public_access_block, whose
// single component reads account_id both as a plain argument AND as a
// Cloud-context default - every one carries a Cloud-valued component,
// which [synthesizeTypeIdentity] never produces), an optional trailing
// segment (aws_lambda_permission's qualifier, aws_route53_record's
// set_identifier - a component the provider's own grammar documents as
// absent rather than empty when omitted), an any-of argument (aws_route -
// a component with more than one alternative Attrs, a fallback chain no
// schema states a preference order for), or, when none of those three
// shapes fits, a plain disagreement - a renamed identity attribute
// (aws_securityhub_member's account_id argument feeding a
// member_account_id identity attr) or an IdentityAttrs value the row
// claims that the schema's required set does not name.
//
// This count is one below the 135 an earlier, less strict pass of this same
// rule reported (and two below the 136 the issue's own measurement cites):
// that pass did not yet refuse a Cloud-valued component
// (aws_s3_account_public_access_block's account_id happens to satisfy the
// same-name check on Attrs alone, but the component is a cloud-context
// default, not a plain client-supplied argument, so a real synthesized
// entry would require the caller to set it explicitly - a real behavioural
// difference the stricter rule now catches). Re-run -schema-precedence's own
// summary whenever the true source of the remaining one-off gap against
// the issue's 136 is found, rather than treating either number as fixed.
// schemaFirstReproduced is every config-identified ratified type with a
// provider identity schema (live/import-grammar.json's
// identity_schema_required) whose row the schema reproduces, sorted. See
// this file's own doc comment for what "reproduces" means.
func schemaFirstReproduced(ratified map[string]identity.TypeIdentity, grammar map[string]importGrammarRow) []string {
	var out []string
	for t := range schemaFirstCandidates(ratified, grammar) {
		e := ratified[t]
		g := grammar[t]
		if reproduceSchemaRow(e, g) {
			out = append(out, t)
		}
	}
	sort.Strings(out)
	return out
}

// schemaFirstCandidates is every config-identified ratified type the
// provider also serves an identity schema for - the population
// [schemaFirstReproduced] and [buildSchemaReproducesBucket] both partition,
// reproduced against not.
func schemaFirstCandidates(ratified map[string]identity.TypeIdentity, grammar map[string]importGrammarRow) map[string]bool {
	out := map[string]bool{}
	for t, e := range ratified {
		if e.RecordBacked || e.ServerAssigned || e.NonAWSProvider {
			continue // not config-identified: nothing here is a claim [SynthesizeTypeIdentity] could ever reproduce
		}
		g, ok := grammar[t]
		if !ok || len(g.IdentitySchemaRequired) == 0 {
			continue // no provider identity schema to compare against at all
		}
		out[t] = true
	}
	return out
}

// reproduceSchemaRow is [schemaFirstReproduced]'s per-type decision. See
// this file's own doc comment for what "reproduces" means and why the
// comparison is offline.
func reproduceSchemaRow(e identity.TypeIdentity, g importGrammarRow) bool {
	required := append([]string(nil), g.IdentitySchemaRequired...)
	sort.Strings(required)

	var attrNames []string
	for _, c := range e.Components {
		switch {
		case len(c.Attrs) == 0:
			continue // a separator literal or a bare cloud slot: a schema-derived entry has neither
		case c.Cloud != identity.CloudNone:
			return false // a cloud-context component: [synthesizeTypeIdentity] never builds one
		case len(c.Attrs) != 1:
			return false // a fallback chain (more than one alternative argument): a judgment call no schema states
		default:
			attrNames = append(attrNames, c.Attrs[0])
		}
	}
	sort.Strings(attrNames)
	if !equalStringSlices(attrNames, required) {
		return false
	}

	claimed := withoutLiteralID(e.IdentityAttrs)
	sort.Strings(claimed)
	return len(claimed) == 0 || equalStringSlices(claimed, required)
}

// notReproducedClass labels why reproduceSchemaRow refused e, over the same
// three shapes issue #387's own measurement names, falling back to "other"
// for a plain disagreement (a renamed argument, an IdentityAttrs value the
// schema's required set does not name) that fits none of them. Only ever
// called on a type reproduceSchemaRow already returned false for; the
// classes are checked in this order because a row can carry more than one
// shape (an ARN-shaped row can also carry an optional trailing segment) and
// the ARN read is the more informative one to report first.
func notReproducedClass(e identity.TypeIdentity) string {
	for _, c := range e.Components {
		if c.Cloud != identity.CloudNone {
			return "arn-shaped"
		}
	}
	for _, c := range e.Components {
		if c.OmitIfAbsent {
			return "optional-trailing"
		}
	}
	for _, c := range e.Components {
		if len(c.Attrs) > 1 {
			return "any-of"
		}
	}
	return "other"
}

// withoutLiteralID returns ss with every "id" entry removed. See this
// file's own doc comment for why "id" is set aside rather than compared.
func withoutLiteralID(ss []string) []string {
	var out []string
	for _, s := range ss {
		if s != "id" {
			out = append(out, s)
		}
	}
	return out
}

// equalStringSlices compares two already-sorted string slices for exact
// equality.
func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// notReproducedEntry is one candidate [schemaFirstReproduced] does not
// name, with the shape it was refused for (notReproducedClass).
type notReproducedEntry struct {
	Type  string `json:"type"`
	Class string `json:"class"`
}

// schemaReproducesBucket is [schemaPrecedenceArtifact]'s body: every
// config-identified ratified type with a provider identity schema
// (HasIdentitySchema, live/import-grammar.json's identity_schema_required),
// partitioned into Reproduced (this file's own same-name comparison agrees
// with the row) and NotReproduced (it does not, labelled by shape - see
// [notReproducedClass]). ReproducedCount + NotReproducedCount ==
// HasIdentitySchema always.
type schemaReproducesBucket struct {
	HasIdentitySchema int `json:"has_identity_schema"`

	Reproduced      []string `json:"reproduced"`
	ReproducedCount int      `json:"reproduced_count"`

	NotReproduced      []notReproducedEntry `json:"not_reproduced"`
	NotReproducedCount int                  `json:"not_reproduced_count"`
}

// buildSchemaReproducesBucket is the whole of issue #387's measurement,
// over [schemaFirstCandidates].
func buildSchemaReproducesBucket(ratified map[string]identity.TypeIdentity, grammar map[string]importGrammarRow) schemaReproducesBucket {
	candidates := schemaFirstCandidates(ratified, grammar)
	reproduced := schemaFirstReproduced(ratified, grammar)
	reproducedSet := setOf(reproduced)

	var notReproduced []notReproducedEntry
	for t := range candidates {
		if reproducedSet[t] {
			continue
		}
		notReproduced = append(notReproduced, notReproducedEntry{Type: t, Class: notReproducedClass(ratified[t])})
	}
	sort.Slice(notReproduced, func(i, j int) bool { return notReproduced[i].Type < notReproduced[j].Type })

	return schemaReproducesBucket{
		HasIdentitySchema:  len(candidates),
		Reproduced:         reproduced,
		ReproducedCount:    len(reproduced),
		NotReproduced:      notReproduced,
		NotReproducedCount: len(notReproduced),
	}
}

// schemaPrecedenceJSONRel is live/schema-precedence.json's committed path.
//
// Issue #695 gave this measurement its own file. It used to be one bucket
// inside live/rowgen-convergence.json, an artifact whose headline metric
// (adopted-unchanged) is on record as not predicting onboarding success and
// which #695 deleted. This measurement is a different claim and keeps its
// reader: tools/provider-bump-report prints the before/after of exactly
// these counts, because a provider release that changes which rows the
// schema reproduces changes what internal/live/identity's preferSynthesized
// does at resolution time. Nothing about the measurement itself moved - the
// object below is the old schema_reproduces bucket, field for field.
const schemaPrecedenceJSONRel = "live/schema-precedence.json"

// schemaPrecedenceArtifact is live/schema-precedence.json's whole shape:
// [schemaReproducesBucket] promoted to the top level, with provenance.
type schemaPrecedenceArtifact struct {
	GeneratedBy string `json:"generated_by"`
	Note        string `json:"note"`

	schemaReproducesBucket
}

// runSchemaPrecedence is -schema-precedence's entry point.
func runSchemaPrecedence(out, errOut *os.File) error {
	root, err := repoRoot()
	if err != nil {
		return err
	}
	ratified, err := loadRatified(filepath.Join(root, ratifiedJSONRel))
	if err != nil {
		return fmt.Errorf("reading %s: %w", ratifiedJSONRel, err)
	}
	grammar, err := loadImportGrammar(filepath.Join(root, importGrammarJSONRel))
	if err != nil {
		return fmt.Errorf("reading %s: %w", importGrammarJSONRel, err)
	}

	art := schemaPrecedenceArtifact{
		GeneratedBy: "tools/row-gen -schema-precedence (go run ./tools/row-gen -schema-precedence)",
		Note: "Issue #387, ruling 2 of the foundation-order ruling (#388): for every config-identified " +
			"row in tools/row-gen/ratified.json that the provider also serves an identity schema for " +
			"(live/import-grammar.json's identity_schema_required), does the schema say the same thing " +
			"the row does? internal/live/identity's preferSynthesized makes the identical comparison at " +
			"resolution time against the real provider schemas and prefers the synthesized entry when it " +
			"holds, so a type moving between reproduced and not_reproduced is a change in what a live " +
			"run does. Regenerate with `go run ./tools/row-gen -schema-precedence`.",
		schemaReproducesBucket: buildSchemaReproducesBucket(ratified, grammar),
	}

	if err := writeJSONArtifact(filepath.Join(root, schemaPrecedenceJSONRel), art); err != nil {
		return fmt.Errorf("writing %s: %w", schemaPrecedenceJSONRel, err)
	}

	fmt.Fprintf(out, "wrote %s\n", schemaPrecedenceJSONRel)
	fmt.Fprintf(errOut, "row-gen -schema-precedence: %d candidates with a provider identity schema, %d reproduced, %d not\n",
		art.HasIdentitySchema, art.ReproducedCount, art.NotReproducedCount)
	return nil
}
