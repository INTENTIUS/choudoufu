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
