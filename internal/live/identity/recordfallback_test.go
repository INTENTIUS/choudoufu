// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/providers"
)

// The gauntlet's corpus-autoscaling-complete crossing found this shape live:
// aws_autoscaling_group has a ratified DefaultTable row (identity is the
// "name" argument, resolved statically whenever a configuration states one),
// so it is not in [MarkerlessTypes] - but its schema carries no top-level
// tags map at all ([TestNamePrefixDefersToDiscovery]'s sibling types,
// aws_db_parameter_group and aws_iam_role, both have one). An instance named
// through name_prefix therefore has neither a static name nor anywhere to
// carry an ownership marker: before [RecordFallbackType] existed,
// internal/live/stamp escalated it to "Unmarked apply of a marker-only
// resource" - a hard refusal on a construct stock OpenTofu applies without
// complaint, HANDOFF's first table row.
//
// This asserts the CLASS every instance in the fixture resolves to, by
// value: the untaggable name_prefix instance must resolve
// [ClassRecordLocated] (the record rung, HANDOFF's safety rule - never a
// wrong marker, never an outright refusal) precisely because a record_store
// is declared, its sibling that states a literal name must stay
// [ClassConcrete] with the literal name as its import ID (proving the
// fallback does not swallow the ordinary path), and the same fixture with no
// record_store declared must go back to [ClassNeedsDiscovery] (proving the
// fallback is not silently unconditional - see
// [TestRecordFallbackRequiresARecordStore]).
func TestRecordFallbackClassifiesUntaggableNamePrefix(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "record-fallback-untaggable"), nil)
	schemas := map[string]providers.Schema{
		"aws_autoscaling_group": locatedSchema(map[string]*configschema.Attribute{
			"name": {Type: cty.String, Optional: true, Computed: true},
		}),
	}

	result, diags := ResolveWith(context.Background(), cfg, Context{Schemas: schemas})
	assertNoErrors(t, diags)

	prefixed := resolutionAt(t, result, "aws_autoscaling_group.prefixed")
	if prefixed.Class != ClassRecordLocated {
		t.Fatalf("aws_autoscaling_group.prefixed resolved %s, want %s: untaggable and named through name_prefix, with a record_store declared, must drop to the record rung rather than refuse", prefixed.Class, ClassRecordLocated)
	}
	if prefixed.ImportID != "" {
		t.Errorf("aws_autoscaling_group.prefixed carries ImportID %q; ClassRecordLocated's identity comes from the store, never from this package, and a non-empty value here would be a wrong identity nothing verified", prefixed.ImportID)
	}

	named := resolutionAt(t, result, "aws_autoscaling_group.named")
	if named.Class != ClassConcrete {
		t.Fatalf("aws_autoscaling_group.named resolved %s, want %s: it states a literal name and must resolve from configuration alone, never reaching the fallback", named.Class, ClassConcrete)
	}
	if named.ImportID != "web-static" {
		t.Errorf("aws_autoscaling_group.named resolved to import ID %q, want %q - the literal value its own name argument states", named.ImportID, "web-static")
	}
}

// TestRecordFallbackRequiresARecordStore is the other half of the by-value
// assertion above: the identical fixture's untaggable name_prefix instance,
// resolved with the SAME schemas but no record_store in the configuration,
// must stay [ClassNeedsDiscovery] rather than silently landing on the record
// rung. [resolver.recordFallback] gates on [resolver.recordStore] before it
// ever asks [RecordFallbackType], and this is what proves that gate is live
// rather than vestigial - a record_store is the operator's own migration
// step (HANDOFF's foundation), and this fallback must never invent one.
func TestRecordFallbackRequiresARecordStore(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "name-prefix-discovery"), nil)
	schemas := map[string]providers.Schema{
		"aws_db_parameter_group": locatedSchema(map[string]*configschema.Attribute{
			"name":   {Type: cty.String, Optional: true, Computed: true},
			"family": {Type: cty.String, Required: true},
		}),
	}

	result, diags := ResolveWith(context.Background(), cfg, Context{Schemas: schemas})
	assertNoErrors(t, diags)

	prefixed := resolutionAt(t, result, "aws_db_parameter_group.prefixed")
	if prefixed.Class != ClassNeedsDiscovery {
		t.Fatalf("aws_db_parameter_group.prefixed resolved %s with no record_store declared, want %s: the fallback must never fire without an operator-declared store", prefixed.Class, ClassNeedsDiscovery)
	}
}

// TestRecordFallbackTypeAgreesWithMarkersTaggable is the schema-only half:
// [RecordFallbackType] must clear a type only when [markers.Taggable] itself
// says the type's schema has nowhere to carry a marker. This is the
// predicate [TestRecordFallbackClassifiesUntaggableNamePrefix] exercises
// through a full resolve; this asserts it directly, against both a clean
// untaggable schema and its taggable twin, so a regression that stopped
// checking taggability at all - not just one that got the schema shape
// wrong - is caught here rather than only downstream.
func TestRecordFallbackTypeAgreesWithMarkersTaggable(t *testing.T) {
	untaggable := locatedSchema(map[string]*configschema.Attribute{
		"name": {Type: cty.String, Optional: true, Computed: true},
	})
	if !RecordFallbackType("aws_autoscaling_group", map[string]providers.Schema{"aws_autoscaling_group": untaggable}) {
		t.Fatal("RecordFallbackType(untaggable ASG-shaped schema) = false, want true")
	}

	taggable := locatedSchema(map[string]*configschema.Attribute{
		"name": {Type: cty.String, Optional: true, Computed: true},
		"tags": {Type: cty.Map(cty.String), Optional: true},
	})
	if RecordFallbackType("aws_autoscaling_group", map[string]providers.Schema{"aws_autoscaling_group": taggable}) {
		t.Fatal("RecordFallbackType(taggable ASG-shaped schema) = true, want false: a settable top-level tags map means the ordinary marker-fallback path applies, not the record rung")
	}
}

// route53RecordSiblingApplySchema is hashicorp/aws 6.59.0's own
// aws_route53_record shape, reduced to what the three predicates in
// [RecordFallbackType] actually read: no top-level tags map (so
// [markers.Taggable] is false), a top-level string id, and the identity
// schema the provider really serves - required name/type/zone_id, optional
// account_id/set_identifier, as recorded in live/survey-full.json.
//
// The identity schema is not decoration here. Without it,
// [LocatedIdentityPlanFor] falls to its documented-import-string branch and
// refuses the type, which is the answer this fixture would then be measuring
// instead of the one it is about.
func route53RecordSiblingApplySchema() providers.Schema {
	s := locatedSchema(map[string]*configschema.Attribute{
		"zone_id": {Type: cty.String, Required: true},
		"name":    {Type: cty.String, Required: true},
		"type":    {Type: cty.String, Required: true},
		"ttl":     {Type: cty.Number, Optional: true},
		"records": {Type: cty.Set(cty.String), Optional: true},
	})
	s.IdentitySchema = &configschema.Object{
		Nesting: configschema.NestingSingle,
		Attributes: map[string]*configschema.Attribute{
			"name":           {Type: cty.String, Required: true},
			"type":           {Type: cty.String, Required: true},
			"zone_id":        {Type: cty.String, Required: true},
			"account_id":     {Type: cty.String, Optional: true},
			"set_identifier": {Type: cty.String, Optional: true},
		},
	}
	s.IdentitySchemaVersion = 1
	return s
}

// siblingApplyUnknownCertificate is the one managed result both instances in
// testdata/record-fallback-sibling-apply read: a certificate this run has
// listed but not applied, so the provider has not filled in
// domain_validation_options and the value comes back unknown. That is the
// exact condition [resolver.siblingApplyResolution] exists for.
func siblingApplyUnknownCertificate() map[string]cty.Value {
	return map[string]cty.Value{
		"aws_acm_certificate.this": cty.ObjectVal(map[string]cty.Value{
			"domain_validation_options": cty.UnknownVal(cty.Set(cty.Object(map[string]cty.Type{
				"resource_record_name":  cty.String,
				"resource_record_type":  cty.String,
				"resource_record_value": cty.String,
			}))),
		}),
	}
}

// TestRecordFallbackClassifiesSiblingApplyUntaggable is the gauntlet's
// corpus-alb-complete crossing found live, one door over from
// [TestRecordFallbackClassifiesUntaggableNamePrefix].
//
// terraform-aws-modules/terraform-aws-acm builds
// aws_route53_record.validation's name and type out of
// aws_acm_certificate.this's domain_validation_options, which the provider
// does not fill in until the certificate is applied. Identity resolution
// classifies that honestly - NEEDS_DISCOVERY, cause SIBLING_APPLY, "the
// value is not known until that object exists" - and for a taggable type
// that answer is a real promise: the marker written at create time is what
// a later sweep finds.
//
// aws_route53_record has no tags map. The promise cannot be kept for it, and
// internal/command/live_plan escalates the unstamped instance to "Unmarked
// apply of a marker-only resource": a hard refusal, with no way out, for two
// objects corpus-alb-complete had already migrated. HANDOFF's fifth row says
// where such an instance belongs - the record rung - and
// [RecordFallbackType] already answers exactly the question that needs
// asking. It was simply never asked on this path.
//
// Asserted BY CLASS, in both directions, on one fixture so the two answers
// cannot drift apart:
//
//   - aws_route53_record.validation must be [ClassRecordLocated], and must
//     carry NO ImportID: the record holds the identity, and a value invented
//     here would be an identity nothing verified.
//   - aws_s3_bucket.logs, with the IDENTICAL sibling-apply dependency on the
//     IDENTICAL unknown, must stay [ClassNeedsDiscovery] with cause
//     [DiscoverySiblingApply], because it is taggable and its marker really
//     can be swept. This is what proves the change is narrow rather than a
//     blanket re-route of every sibling-apply instance onto the record.
//
// Mutation checks, both run:
//
//   - Removing the recordFallback call from [resolver.siblingApplyResolution]'s
//     own branch in resolve.go makes the route53 assertion fail (back to
//     NEEDS_DISCOVERY) and leaves the s3 assertion passing.
//   - Making that call unconditional (ignoring RecordFallbackType's
//     taggability check) makes the S3 assertion fail, which is the direction
//     that matters: a taggable type must never be quietly moved off the
//     marker it can actually carry.
func TestRecordFallbackClassifiesSiblingApplyUntaggable(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "record-fallback-sibling-apply"), nil)
	schemas := map[string]providers.Schema{
		"aws_route53_record": route53RecordSiblingApplySchema(),
		"aws_s3_bucket": locatedSchema(map[string]*configschema.Attribute{
			"bucket": {Type: cty.String, Optional: true, Computed: true},
			"tags":   {Type: cty.Map(cty.String), Optional: true},
		}),
	}

	result, diags := ResolveWith(context.Background(), cfg, Context{
		Schemas:        schemas,
		ManagedResults: siblingApplyUnknownCertificate(),
	})
	assertNoErrors(t, diags)

	rec := resolutionAt(t, result, "aws_route53_record.validation")
	if rec.Class != ClassRecordLocated {
		t.Fatalf("aws_route53_record.validation resolved %s (cause %s, reason %q), want %s: its name and type wait on a sibling apply AND the type has nowhere to carry a marker, so a discovery sweep can never bind it",
			rec.Class, rec.Cause, rec.Reason, ClassRecordLocated)
	}
	if rec.ImportID != "" {
		t.Errorf("aws_route53_record.validation carries ImportID %q; a record-located identity comes from the store, and a value invented here is an identity nothing verified", rec.ImportID)
	}

	bucket := resolutionAt(t, result, "aws_s3_bucket.logs")
	if bucket.Class != ClassNeedsDiscovery {
		t.Fatalf("aws_s3_bucket.logs resolved %s, want %s: it has the identical sibling-apply dependency but IS taggable, so the marker it carries is what finds it and the record must not take over",
			bucket.Class, ClassNeedsDiscovery)
	}
	if bucket.Cause != DiscoverySiblingApply {
		t.Errorf("aws_s3_bucket.logs resolved NEEDS_DISCOVERY with cause %s, want %s", bucket.Cause, DiscoverySiblingApply)
	}
}
