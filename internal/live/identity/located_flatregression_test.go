// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/providers"
)

// GitHub issue #429's own first proof, restated in code rather than left as
// a one-off manual measurement: tools/row-gen/rejected.json's
// aws_cognito_user_pool_client entry (computed at bdcc47a6b8) names exactly
// two types - aws_elastic_beanstalk_configuration_template and
// aws_vpn_gateway_attachment - as "would work under the located mechanism
// as it stands" (the flat, single-string bare-"id" case) among the 18-type
// population it measured, and the issue's own Do list asks a unit that
// touches the located payload to reconfirm both before touching anything
// else.
//
// Reconfirmed 2026-08-28 against a live hashicorp/aws 6.59.0 pull
// (`CHOUDOUFU_LIVE_SCHEMAS=1 go test ./internal/live/identity/ -run
// TestLocatedTypePopulation -v`, and `terraform providers schema -json`
// for the two block shapes below): the two types have DIVERGED since
// bdcc47a6b8, for a reason that has nothing to do with composite identity.
//
//   - aws_vpn_gateway_attachment still works exactly as the ledger says:
//     untaggable, a plain top-level string "id", no wire identity schema,
//     no ratified table row - the bare-id default branch admits it.
//     [TestLocatedTypeAdmitsVPNGatewayAttachment] pins this against the
//     real block shape.
//   - aws_elastic_beanstalk_configuration_template no longer reaches the
//     located mechanism at all: it is a member of [NotImportableTypes]
//     today (issue #331's veto, which landed after bdcc47a6b8 and is
//     unrelated to this issue's payload work), so [LocatedType] refuses it
//     on condition 0 before its schema is ever read. That is the CORRECT,
//     safer answer per HANDOFF's safety rule - a type the provider will not
//     import back must never be admitted as located, or the first migrate
//     would write a record nothing could ever read back - and it is not a
//     regression this unit introduced: [TestElasticBeanstalkConfigurationTemplateIsNotImportable]
//     pins the CURRENT, correct refusal so the ledger's stale claim about
//     this one type does not get re-asserted as live behavior by a future
//     reader skimming rejected.json rather than the code.
func TestLocatedTypeAdmitsVPNGatewayAttachment(t *testing.T) {
	const typeName = "aws_vpn_gateway_attachment"
	if NotImportable(typeName) {
		t.Fatalf("%s is now in NotImportableTypes; the ledger's second flat-case example has diverged the same way "+
			"aws_elastic_beanstalk_configuration_template did, and this test's own premise needs re-reading before "+
			"anything else in this file is trusted", typeName)
	}
	// The real block shape, reduced to what this mechanism reads: two
	// Required top-level strings (vpc_id, vpn_gateway_id) neither of which
	// is "id", a Computed top-level string "id", and no tags attribute -
	// confirmed against a live `terraform providers schema -json` pull of
	// hashicorp/aws 6.59.0. No IdentitySchema, which is what keeps this on
	// the bare-id branch rather than the Composite one.
	schema := providers.Schema{Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"id":             {Type: cty.String, Computed: true},
		"vpc_id":         {Type: cty.String, Required: true},
		"vpn_gateway_id": {Type: cty.String, Required: true},
	}}}
	if !LocatedType(typeName, map[string]providers.Schema{typeName: schema}) {
		t.Fatalf("LocatedType(%q) = false against its real block shape; the ledger's own \"would work under the "+
			"located mechanism as it stands\" claim for this type no longer holds, which this unit's Accept "+
			"criteria requires calling out rather than shipping past", typeName)
	}
	plan, recordable := LocatedIdentityPlanFor(typeName, schema)
	if !recordable || plan.Composite() || plan.Composed() || plan.Named() {
		t.Errorf("LocatedIdentityPlanFor(%q) = %+v (recordable=%v); want the bare-id default (no Components, no "+
			"ImportIDParts, no Attr) - a type moving onto a composite plan changes what write-back records and "+
			"belongs in this file's own doc comment, not silently", typeName, plan, recordable)
	}
}

// TestElasticBeanstalkConfigurationTemplateIsNotImportable is this file's
// negative half: see the doc comment above. It reads only the generated
// veto table, not a hand-built schema, because [NotImportable]'s condition
// 0 in [LocatedType] is checked before any schema is - a type here is
// refused regardless of what its block looks like.
func TestElasticBeanstalkConfigurationTemplateIsNotImportable(t *testing.T) {
	const typeName = "aws_elastic_beanstalk_configuration_template"
	if !NotImportable(typeName) {
		t.Fatalf("%s is no longer in NotImportableTypes. If issue #331's veto was lifted for this type (a provider "+
			"release added a real Importer), the ledger's original claim that this type \"would work under the "+
			"located mechanism as it stands\" is worth re-measuring for real rather than assumed - re-run "+
			"CHOUDOUFU_LIVE_SCHEMAS=1's TestLocatedTypePopulation and update this file's own doc comment", typeName)
	}
	// Condition 0 refuses before any schema is consulted - LocatedType(...)
	// with a schema that would otherwise admit cleanly still answers false.
	schema := providers.Schema{Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"id": {Type: cty.String, Computed: true},
	}}}
	if LocatedType(typeName, map[string]providers.Schema{typeName: schema}) {
		t.Fatalf("LocatedType(%q) = true despite NotImportable(%q) = true; condition 0 is supposed to refuse "+
			"ahead of every schema-read condition, per LocatedType's own doc comment", typeName, typeName)
	}
}
