// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tofu"
)

// These tests drive the seam in schema_check.go: a provider that serves
// resource identity schemas, and what the projection does with the identity
// table's claims once it can see them.

// identifiedBy attaches an identity schema to one of the fake provider's
// resource types. required names the attributes the provider requires for
// import; account_id and region come along as the optional context every
// AWS identity schema carries.
func identifiedBy(schema providers.Schema, required ...string) providers.Schema {
	attrs := map[string]*configschema.Attribute{
		"account_id": {Type: cty.String, Optional: true},
		"region":     {Type: cty.String, Optional: true},
	}
	for _, name := range required {
		attrs[name] = &configschema.Attribute{Type: cty.String, Required: true}
	}
	schema.IdentitySchema = &configschema.Object{Attributes: attrs, Nesting: configschema.NestingSingle}
	schema.IdentitySchemaVersion = 1
	return schema
}

// identifyingSchemas is the package's caricature of the AWS provider, with
// the identity schemas the real one serves for these types added.
func identifyingSchemas() map[string]providers.Schema {
	out := fakeSchemas()
	out["aws_s3_bucket"] = identifiedBy(out["aws_s3_bucket"], "bucket")
	out["aws_iam_role"] = identifiedBy(out["aws_iam_role"], "name")
	out["aws_iam_role_policy_attachment"] = identifiedBy(out["aws_iam_role_policy_attachment"], "role", "policy_arn")
	out["aws_vpc"] = identifiedBy(out["aws_vpc"], "id")
	// The real divergence: the provider identifies an association by the
	// rtbassoc- ID it assigns, and the table builds the documented import
	// string out of subnet and route table.
	out["aws_route_table_association"] = identifiedBy(out["aws_route_table_association"], "id")
	// Served with no identity schema at all, as in provider 6.58.0.
	return out
}

func cacheWith(t *testing.T, schemas map[string]providers.Schema) *providerEntry {
	t.Helper()

	p := &tofu.MockProvider{
		GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			Provider:      providers.Schema{Block: &configschema.Block{}},
			ResourceTypes: schemas,
		},
	}
	p.ConfigureProviderCalled = true

	entry, err := newProviderCache(SingleProvider(awsProvider, p), nil).get(context.Background(), awsProvider)
	if err != nil {
		t.Fatalf("reaching the fake provider: %s", err)
	}
	return entry
}

// The table's claims are checked as soon as the schemas arrive, and the
// ones the provider confirms produce nothing at all.
func TestProviderCacheVerifiesTheIdentityTable(t *testing.T) {
	entry := cacheWith(t, identifyingSchemas())

	agreed := map[string]bool{}
	for _, typeName := range entry.verification.Agreed {
		agreed[typeName] = true
	}
	for _, typeName := range []string{"aws_s3_bucket", "aws_iam_role", "aws_iam_role_policy_attachment", "aws_vpc"} {
		if !agreed[typeName] {
			t.Errorf("%s should have been confirmed by its identity schema; findings: %v", typeName, entry.verification.Findings)
		}
	}

	// Types the provider serves without an identity schema are
	// unverifiable rather than wrong.
	skipped := map[string]string{}
	for _, s := range entry.verification.Skipped {
		skipped[s.Type] = s.Reason
	}
	if reason, ok := skipped["aws_ecs_cluster"]; !ok || !strings.Contains(reason, "no identity schema") {
		t.Errorf("aws_ecs_cluster should be unverifiable, got %q", reason)
	}

	for _, typeName := range []string{"aws_s3_bucket", "aws_iam_role", "aws_ecs_cluster"} {
		if _, diags := entry.resourceSchema(awsProvider, typeName); diags.HasErrors() {
			t.Errorf("a confirmed type produced diagnostics for %s:\n%s", typeName, diags.Err())
		}
	}
}

// A divergence is reported in the verification and is not allowed to fail a
// run: the table carries an inference no schema has, so the two describing
// one identity differently is a thing for the table's maintainer, not for
// the operator planning an estate.
func TestDivergenceDoesNotFailTheRun(t *testing.T) {
	entry := cacheWith(t, identifyingSchemas())

	var found bool
	for _, f := range entry.verification.Findings {
		if f.Type != "aws_route_table_association" {
			continue
		}
		found = true
		if f.Breaking {
			t.Errorf("a differently-described identity was called breaking: %s", f.Detail)
		}
	}
	if !found {
		t.Errorf("the association's divergence was not reported: %v", entry.verification.Findings)
	}

	if _, diags := entry.resourceSchema(awsProvider, "aws_route_table_association"); diags.HasErrors() {
		t.Errorf("a divergence failed the run:\n%s", diags.Err())
	}

	// The maintainer-facing rendering is still available, as warnings.
	diags := entry.verification.Diagnostics()
	if len(diags) == 0 {
		t.Fatal("the verification renders no diagnostics at all")
	}
	if diags.HasErrors() {
		t.Errorf("divergences rendered as errors:\n%s", diags.Err())
	}
}

// A table entry that composes an identity from arguments the type does not
// have is a different matter: there is no identity to compute, so the
// type's use fails here rather than proceeding on a guess.
func TestBreakingClaimFailsWhereTheTypeIsUsed(t *testing.T) {
	schemas := identifyingSchemas()
	// A provider that has renamed the argument the table reads.
	bucket := schemas["aws_s3_bucket"]
	block := *bucket.Block
	attrs := map[string]*configschema.Attribute{}
	for name, a := range block.Attributes {
		if name == "bucket" {
			continue
		}
		attrs[name] = a
	}
	block.Attributes = attrs
	bucket.Block = &block
	schemas["aws_s3_bucket"] = bucket

	entry := cacheWith(t, schemas)

	_, diags := entry.resourceSchema(awsProvider, "aws_s3_bucket")
	if !diags.HasErrors() {
		t.Fatalf("a table entry naming an absent argument did not fail: %v", entry.verification.Findings)
	}
	if got := diags.Err().Error(); !strings.Contains(got, "bucket") {
		t.Errorf("the error does not name the argument:\n%s", got)
	}

	// Only the type it concerns.
	if _, diags := entry.resourceSchema(awsProvider, "aws_iam_role"); diags.HasErrors() {
		t.Errorf("an unrelated type failed too:\n%s", diags.Err())
	}
}

// The other breaking claim - the table offers an identity attribute the
// type does not have - is reported and does not stop a run. It costs
// something only when another resource references that attribute, and that
// run already fails where the formula is rendered; failing every run that
// touches the type would turn one stale row into an outage for
// configurations that never reference it.
func TestAnAbsentIdentityAttributeIsReportedNotFatal(t *testing.T) {
	schemas := identifyingSchemas()
	// The caricature of aws_eip in internal/command's fixtures is exactly
	// this shape: no allocation_id, which the table offers as an identity
	// source.
	eip := schemas["aws_eip"]
	block := *eip.Block
	attrs := map[string]*configschema.Attribute{}
	for name, a := range block.Attributes {
		if name == "allocation_id" {
			continue
		}
		attrs[name] = a
	}
	block.Attributes = attrs
	eip.Block = &block
	schemas["aws_eip"] = eip

	entry := cacheWith(t, schemas)

	var found identity.Finding
	for _, f := range entry.verification.Findings {
		if f.Kind == identity.FindingAttributeNotInSchema {
			found = f
		}
	}
	if found.Type != "aws_eip" {
		t.Fatalf("the absent identity attribute was not reported: %v", entry.verification.Findings)
	}
	if !found.Breaking {
		t.Error("an attribute the type does not have is still a breaking claim about the table")
	}
	if !strings.Contains(found.Detail, "allocation_id") {
		t.Errorf("the finding does not name the attribute:\n%s", found.Detail)
	}

	if _, diags := entry.resourceSchema(awsProvider, "aws_eip"); diags.HasErrors() {
		t.Errorf("a run that does not reference the attribute was stopped:\n%s", diags.Err())
	}
}

// The derivable set travels with the verification, which is how a wiring
// batch gets a schema-backed candidate list out of a run.
func TestVerificationReportsDerivableTypes(t *testing.T) {
	schemas := identifyingSchemas()
	// aws_dynamodb_table's name is a required argument in the real
	// provider, which is what makes the type self-admitting.
	table := schemas["aws_dynamodb_table"]
	block := *table.Block
	attrs := map[string]*configschema.Attribute{}
	for name, a := range block.Attributes {
		attrs[name] = a
	}
	attrs["name"] = &configschema.Attribute{Type: cty.String, Required: true}
	block.Attributes = attrs
	table.Block = &block
	schemas["aws_dynamodb_table"] = identifiedBy(table, "name")

	entry := cacheWith(t, schemas)

	var derivable []identity.DerivableType
	for _, d := range entry.verification.Derivable {
		derivable = append(derivable, d)
	}
	if len(derivable) != 1 || derivable[0].Type != "aws_dynamodb_table" {
		t.Fatalf("derivable set is %v, want only aws_dynamodb_table", derivable)
	}
	if !derivable[0].InTable {
		t.Error("aws_dynamodb_table is in the hand table and should be marked so")
	}
}
