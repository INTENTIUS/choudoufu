// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"sort"

	"github.com/intentius/choudoufu/internal/live/identity"
)

// This file is ruling 2 of rfc/20260823-foundation-order-ruling.md (issue
// #387): wherever the provider's own resource identity schema reproduces a
// ratified row, the schema is the source and the row leaves the EMITTED
// table - internal/live/identity/table_generated.go and
// internal/live/lint/admission_generated.go - though not, in this pass,
// tools/row-gen/ratified.json itself; the ledger stays intact and the drop
// is recorded instead, in live/rowgen-convergence.json's own
// schema_reproduces bucket, so the count is an artifact and not a sentence.
// A future pass may delete the row from ratified.json outright; this one
// does not, because the ledger is the only place the row's full ratified
// content survives if the offline approximation below ever turns out wrong
// for a type the golden did not happen to exercise.
//
// Once a row is dropped here, [identity.SynthesizeTypeIdentity] is the only
// thing that resolves the type from then on - the real function, called at
// resolution time with the real provider schemas, not the offline
// approximation this file uses to DECIDE which rows to drop. Every consumer
// that used to find the type in [identity.DefaultTable] already falls back
// to that function when the table misses: [resolver.lookupType]
// (internal/live/identity/resolve.go) and lint's admitted()
// (internal/live/lint/admission.go) both do today, unconditionally, for any
// type the table does not cover. So dropping a row inverts precedence for
// exactly that type without either function's own code changing at all -
// the schema was always consulted the moment the row disappeared; what
// changes here is which rows disappear.
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
// cloud slot, because a schema-derived entry has neither.
//
// # Why "id" is set aside
//
// [identity.SynthesizeTypeIdentity]'s own doc comment: it deliberately never
// adds "id" to IdentityAttrs, because whether a type's id attribute equals
// its import identity is "precisely the inference a schema does not carry".
// Many hand-ratified rows add it anyway, for types where a human already
// knows the two coincide (aws_dynamodb_table's id IS its name). Refusing to
// drop such a row over that one extra claim would keep the row in the
// ledger for a reason unrelated to whether the SCHEMA reproduces it, so
// reproduceSchemaRow removes "id" from the ratified claim before comparing
// - and TestIdentityGolden is what catches it if some other resource's own
// identity actually depended on this type's ".id" resolving through
// IdentityAttrs, since that is the one observable place a dropped "id"
// claim could still matter.
//
// Measured at 14d6027d2e: of 575 config-identified table rows (not
// RecordBacked, not ServerAssigned, not NonAWSProvider - the identity comes
// from Components, same population [identity.SynthesizeTypeIdentity] can
// ever apply to), 161 have a live/import-grammar.json
// identity_schema_required; this file's own rule reproduces 135 of them.
// The remaining 26 are the shapes GitHub issue #387 itself names: an
// ARN-shaped identity assembled from region/account/name
// (aws_sns_topic, aws_sqs_queue, aws_ecs_capacity_provider among them - every
// one carries a Cloud-valued component, which reproduceSchemaRow refuses on
// sight because [synthesizeTypeIdentity] never produces one), an optional
// trailing segment (aws_lambda_permission's qualifier, aws_route53_record's
// set_identifier - a component whose Attrs holds more than one alternative,
// which reproduceSchemaRow also refuses), and an any-of/renamed argument
// (aws_route, aws_securityhub_member, aws_organizations_delegated_administrator
// - the ratified Component's own argument name does not match the schema's
// required attribute name at all, which is not a "same-name" mapping by
// construction). This count is one below the 136 the issue's own
// measurement cites; the difference is a single row this offline rule
// classifies differently, most likely on the same "id" or cloud-component
// boundary the rule above draws - re-run -convergence's own summary
// whenever the true source of that difference is found, rather than
// treating either number as fixed.
// schemaFirstHeldByCorpus is schemaFirstDrop's second safety net, alongside
// [goldenExercisedTypes]. Our own fixture trees are a committed,
// deterministic generator input; `.corpus` cannot be - it is gitignored,
// fetched from the network by tools/corpus-fetch, and absent from a fresh
// clone, so a generated table cannot depend on it and still be
// reproducible. This table is the same "hold pending evidence" ledger
// tools/row-gen/annotations.json and rejected.json already are, filled in
// by hand from a real -diff run rather than derived.
//
// Verified 2026-08-23 against the pinned corpus manifest's own sources
// (`go run ./tools/corpus-fetch`, then `go run ./tools/refusal-probe -diff`
// before/after dropping the full 134-candidate set, then again after
// goldenExercisedTypes moved from parsing the golden's rendered output to
// scanning fixture source directly): every key here is a candidate neither
// our own fixtures nor the golden declare, but that a real
// terraform-aws-modules or published-deployment configuration does, and
// dropping it raised unadmitted-type refusals in the corpus sweep. Six
// candidates first found this way (aws_api_gateway_integration,
// aws_api_gateway_method, aws_appautoscaling_policy,
// aws_eks_access_policy_association, aws_s3_directory_bucket,
// aws_ssoadmin_account_assignment) turned out to also be declared in our own
// fixture trees, so goldenExercisedTypes now catches them on its own and
// they were removed from here rather than kept as a redundant second
// safety net.
//
// A future worker with a fresher corpus sweep clears an entry by re-running
// the same check and finding it clean; entries are never added speculatively
// without a run backing them, and clearing a wrong entry needs a run too, not
// a guess.
var schemaFirstHeldByCorpus = map[string]string{
	"aws_launch_configuration":                  "declared in 2 corpus configs; dropping it raised unadmitted-type there",
	"aws_organizations_policy_attachment":       "declared in 1 corpus config; dropping it raised unadmitted-type there",
	"aws_route53_vpc_association_authorization": "declared in 1 corpus config; dropping it raised unadmitted-type there",
	"aws_s3tables_table_bucket_policy":          "declared in 1 corpus config; dropping it raised unadmitted-type there",
	"aws_scheduler_schedule":                    "declared in 1 corpus config; dropping it raised unadmitted-type there",
	"aws_vpc_security_group_vpc_association":    "declared in 1 corpus config; dropping it raised unadmitted-type there",
}

// schemaFirstDrop is [schemaFirstReproduced] narrowed by
// [goldenExercisedTypes] and [schemaFirstHeldByCorpus]: candidates is the
// full offline-reproducible set, dropped is the subset actually safe to
// remove from the emitted table today - every candidate neither safety net
// names. See goldenexercised.go's own doc comment and
// schemaFirstHeldByCorpus's above for why the narrowing exists: a candidate
// either one names would not merely render differently if dropped, it would
// disappear from that schema-less instrument's output entirely, which the
// golden's byte-identical bar and refusal-probe's zero-worse bar both treat
// as evidence the row was not reproducible after all.
func schemaFirstDrop(ratified map[string]identity.TypeIdentity, grammar map[string]importGrammarRow, goldenExercised map[string]bool) (candidates, dropped []string) {
	candidates = schemaFirstReproduced(ratified, grammar)
	for _, t := range candidates {
		if goldenExercised[t] {
			continue
		}
		if _, held := schemaFirstHeldByCorpus[t]; held {
			continue
		}
		dropped = append(dropped, t)
	}
	return candidates, dropped
}

func schemaFirstReproduced(ratified map[string]identity.TypeIdentity, grammar map[string]importGrammarRow) []string {
	var out []string
	for t, e := range ratified {
		if e.RecordBacked || e.ServerAssigned || e.NonAWSProvider {
			continue // not config-identified: nothing here is a claim [SynthesizeTypeIdentity] could ever reproduce
		}
		g, ok := grammar[t]
		if !ok || len(g.IdentitySchemaRequired) == 0 {
			continue // no provider identity schema to compare against at all
		}
		if reproduceSchemaRow(e, g) {
			out = append(out, t)
		}
	}
	sort.Strings(out)
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
