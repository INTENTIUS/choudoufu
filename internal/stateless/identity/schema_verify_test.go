// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/opentofu/opentofu/internal/configs/configschema"
	"github.com/opentofu/opentofu/internal/providers"
	"github.com/opentofu/opentofu/internal/tfdiags"
)

// ---------------------------------------------------------------------------
// A fake provider's schemas.
//
// The shapes here are copied from what the real AWS provider (6.58.0)
// serves, because the point of the check is to agree with a real provider
// and the ways a real provider is awkward are the ways that matter. Two of
// them: every AWS identity schema carries account_id and region as
// optional-for-import context alongside the attribute that actually names
// the resource, and the legacy SDK marks both aws_s3_bucket's bucket
// argument and aws_vpc's id attribute Optional+Computed, which is why
// neither of those two is derivable from the schemas alone.
// ---------------------------------------------------------------------------

type fakeType struct {
	// args are the type's configuration arguments: name to one of "req",
	// "opt" (optional, not computed), "optcomp" (the legacy SDK's
	// Optional+Computed) and "comp".
	args map[string]string
	// identity are the identity schema's attributes: name to "req"
	// (required for import) or "opt" (optional for import). A nil map means
	// the provider serves no identity schema for the type.
	identity map[string]string
}

func fakeProviderSchemas(types map[string]fakeType) map[string]providers.Schema {
	out := make(map[string]providers.Schema, len(types))
	for name, ft := range types {
		block := &configschema.Block{Attributes: map[string]*configschema.Attribute{}}
		for argName, kind := range ft.args {
			attr := &configschema.Attribute{Type: cty.String}
			switch kind {
			case "req":
				attr.Required = true
			case "opt":
				attr.Optional = true
			case "optcomp":
				attr.Optional, attr.Computed = true, true
			case "comp":
				attr.Computed = true
			default:
				panic("unknown argument kind " + kind)
			}
			block.Attributes[argName] = attr
		}

		schema := providers.Schema{Block: block}
		if ft.identity != nil {
			body := &configschema.Object{
				Attributes: map[string]*configschema.Attribute{},
				Nesting:    configschema.NestingSingle,
			}
			for attrName, kind := range ft.identity {
				attr := &configschema.Attribute{Type: cty.String}
				switch kind {
				case "req":
					attr.Required = true
				case "opt":
					attr.Optional = true
				default:
					panic("unknown identity attribute kind " + kind)
				}
				body.Attributes[attrName] = attr
			}
			schema.IdentitySchema = body
			schema.IdentitySchemaVersion = 1
		}
		out[name] = schema
	}
	return out
}

// serverAssignedLike is the shape every marker-path AWS type has: the
// identity is the id attribute, which the SDK reports as Optional+Computed.
func serverAssignedLike(extraArgs ...string) fakeType {
	ft := fakeType{
		args:     map[string]string{"id": "optcomp", "region": "optcomp", "tags": "opt"},
		identity: map[string]string{"id": "req", "region": "opt", "account_id": "opt"},
	}
	for _, a := range extraArgs {
		ft.args[a] = "optcomp"
	}
	return ft
}

// ---------------------------------------------------------------------------
// Agreement
// ---------------------------------------------------------------------------

func TestVerifyTableAgreement(t *testing.T) {
	// The real provider's shapes for six of the table's types, one per
	// admission path.
	schemas := fakeProviderSchemas(map[string]fakeType{
		"aws_vpc": serverAssignedLike("cidr_block"),
		"aws_eip": serverAssignedLike("allocation_id", "domain"),
		"aws_s3_bucket": {
			args:     map[string]string{"bucket": "optcomp", "bucket_prefix": "opt", "id": "optcomp", "region": "optcomp"},
			identity: map[string]string{"bucket": "req", "region": "opt", "account_id": "opt"},
		},
		"aws_ssm_parameter": {
			args:     map[string]string{"name": "req", "value": "opt", "id": "optcomp", "region": "optcomp"},
			identity: map[string]string{"name": "req", "region": "opt", "account_id": "opt"},
		},
		"aws_iam_role_policy_attachment": {
			args:     map[string]string{"role": "req", "policy_arn": "req", "id": "optcomp"},
			identity: map[string]string{"role": "req", "policy_arn": "req", "account_id": "opt"},
		},
		"aws_route": {
			args: map[string]string{
				"route_table_id": "req", "destination_cidr_block": "optcomp",
				"destination_ipv6_cidr_block": "optcomp", "destination_prefix_list_id": "optcomp",
				"id": "optcomp", "region": "optcomp",
			},
			// The provider requires only the route table for import and
			// takes any one of the three destinations optionally, while the
			// table picks whichever destination the configuration uses.
			identity: map[string]string{
				"route_table_id": "req", "destination_cidr_block": "opt",
				"destination_ipv6_cidr_block": "opt", "destination_prefix_list_id": "opt",
				"region": "opt", "account_id": "opt",
			},
		},
	})

	v := VerifyTable(schemas)

	if len(v.Findings) != 0 {
		for _, f := range v.Findings {
			t.Errorf("unexpected finding on %s (%s): %s", f.Type, f.Kind, f.Detail)
		}
	}
	if got, want := len(v.Agreed), 6; got != want {
		t.Errorf("confirmed %d entries, want %d (%v)", got, want, v.Agreed)
	}
	// Every other table entry is unserved by this provider and skipped
	// rather than reported.
	for _, s := range v.Skipped {
		if _, served := schemas[s.Type]; served {
			t.Errorf("%s was served but skipped: %s", s.Type, s.Reason)
		}
	}
	if got, want := len(v.Agreed)+len(v.Skipped), len(DefaultTable); got != want {
		t.Errorf("%d entries accounted for, table holds %d", got, want)
	}
	if diags := v.Diagnostics(); len(diags) != 0 {
		t.Errorf("agreement produced diagnostics:\n%s", diags.Err())
	}
}

func TestVerifyTableSkips(t *testing.T) {
	schemas := fakeProviderSchemas(map[string]fakeType{
		// Served, but the provider has no identity schema for it - the real
		// state of aws_ecs_cluster in provider 6.58.0.
		"aws_ecs_cluster": {args: map[string]string{"name": "req", "id": "optcomp"}},
	})

	v := verifyTable(map[string]TypeIdentity{
		"aws_ecs_cluster": DefaultTable["aws_ecs_cluster"],
		"aws_s3_bucket":   DefaultTable["aws_s3_bucket"],
	}, schemas, nil)

	if len(v.Agreed) != 0 {
		t.Errorf("nothing could be confirmed, got %v", v.Agreed)
	}
	if len(v.Findings) != 0 {
		t.Errorf("unexpected findings: %v", v.Findings)
	}
	reasons := map[string]string{}
	for _, s := range v.Skipped {
		reasons[s.Type] = s.Reason
	}
	if !strings.Contains(reasons["aws_ecs_cluster"], "no identity schema") {
		t.Errorf("aws_ecs_cluster skipped for the wrong reason: %q", reasons["aws_ecs_cluster"])
	}
	if !strings.Contains(reasons["aws_s3_bucket"], "does not serve") {
		t.Errorf("aws_s3_bucket skipped for the wrong reason: %q", reasons["aws_s3_bucket"])
	}
}

// ---------------------------------------------------------------------------
// Divergence
// ---------------------------------------------------------------------------

// TestVerifyTableDivergence is the real one. The provider's identity for an
// aws_route_table_association is the rtbassoc- ID it assigns, and the
// table builds the association's identity out of subnet and route table,
// which is the provider's documented *import string* grammar and not its
// identity. Both sides have to appear in the report.
func TestVerifyTableDivergence(t *testing.T) {
	schemas := fakeProviderSchemas(map[string]fakeType{
		"aws_route_table_association": {
			args:     map[string]string{"id": "optcomp", "subnet_id": "opt", "gateway_id": "opt", "route_table_id": "req", "region": "optcomp"},
			identity: map[string]string{"id": "req", "region": "opt", "account_id": "opt"},
		},
	})

	v := verifyTable(map[string]TypeIdentity{
		"aws_route_table_association": DefaultTable["aws_route_table_association"],
	}, schemas, nil)

	kinds := findingKinds(v)
	if !kinds[FindingUnnamedIdentityAttr] {
		t.Errorf("the identity attribute the table never names was not reported: %v", v.Findings)
	}
	if !kinds[FindingComponentOutsideIdentity] {
		t.Errorf("the components outside the identity schema were not reported: %v", v.Findings)
	}
	if len(v.Agreed) != 0 {
		t.Errorf("a diverging entry was also counted as confirmed: %v", v.Agreed)
	}

	for _, f := range v.Findings {
		if f.Breaking {
			t.Errorf("%s was reported as breaking; a differently-described identity is not: %s", f.Kind, f.Detail)
		}
		if f.TableSide == "" || f.SchemaSide == "" {
			t.Errorf("%s names only one side (table=%q schema=%q)", f.Kind, f.TableSide, f.SchemaSide)
		}
	}

	// Both sides in the sentence: the table's components and the
	// provider's identity attribute.
	unnamed := findingOf(t, v, FindingUnnamedIdentityAttr)
	for _, want := range []string{"id", "subnet_id", "route_table_id", "aws_route_table_association"} {
		if !strings.Contains(unnamed.Detail, want) {
			t.Errorf("the divergence detail does not name %q:\n%s", want, unnamed.Detail)
		}
	}

	diags := v.Diagnostics()
	if diags.HasErrors() {
		t.Errorf("divergence must not be an error:\n%s", diags.Err())
	}
	if len(diags) != len(v.Findings) {
		t.Errorf("got %d diagnostics for %d findings", len(diags), len(v.Findings))
	}
	for _, d := range diags {
		if d.Severity() != tfdiags.Warning {
			t.Errorf("divergence diagnostic has severity %v, want warning", d.Severity())
		}
	}
}

// TestVerifyServerAssignedDisputed is the divergence that would matter
// most: the table sends a type to marker discovery and the provider says
// the configuration names it outright.
func TestVerifyServerAssignedDisputed(t *testing.T) {
	schemas := fakeProviderSchemas(map[string]fakeType{
		"aws_vpc": {
			args:     map[string]string{"id": "optcomp", "name": "req"},
			identity: map[string]string{"name": "req", "account_id": "opt"},
		},
	})

	v := verifyTable(map[string]TypeIdentity{"aws_vpc": DefaultTable["aws_vpc"]}, schemas, nil)

	f := findingOf(t, v, FindingServerAssignedDisputed)
	if f.Breaking {
		t.Error("a disputed server-assigned claim is a divergence, not a breakage")
	}
	if !strings.Contains(f.Detail, "name") || !strings.Contains(f.Detail, "marker discovery") {
		t.Errorf("the detail does not name both sides:\n%s", f.Detail)
	}
	// The unnamed-attribute check fires too: the table's entry names id,
	// and the provider's identity is name.
	if !findingKinds(v)[FindingUnnamedIdentityAttr] {
		t.Errorf("expected the unnamed identity attribute to be reported as well: %v", v.Findings)
	}
}

// ---------------------------------------------------------------------------
// Breaking
// ---------------------------------------------------------------------------

func TestVerifyTableBreaking(t *testing.T) {
	schemas := fakeProviderSchemas(map[string]fakeType{
		"aws_thing": {
			args:     map[string]string{"id": "optcomp", "name": "req"},
			identity: map[string]string{"name": "req"},
		},
	})

	table := map[string]TypeIdentity{
		"aws_thing": {
			Type:          "aws_thing",
			Components:    []Component{attr("nombre", "nom")},
			IdentityAttrs: []string{"name", "identifier"},
		},
	}

	v := verifyTable(table, schemas, nil)

	arg := findingOf(t, v, FindingArgumentNotInSchema)
	if !arg.Breaking {
		t.Error("an argument the type does not have must be breaking")
	}
	for _, want := range []string{"nombre", "nom", "aws_thing"} {
		if !strings.Contains(arg.Detail, want) {
			t.Errorf("the detail does not name %q:\n%s", want, arg.Detail)
		}
	}

	attrFinding := findingOf(t, v, FindingAttributeNotInSchema)
	if !attrFinding.Breaking {
		t.Error("an identity attribute the type does not have must be breaking")
	}
	if !strings.Contains(attrFinding.Detail, "identifier") {
		t.Errorf("the detail does not name the missing attribute:\n%s", attrFinding.Detail)
	}
	if strings.Contains(attrFinding.Detail, `"name"`) {
		t.Errorf("the attribute the type does have was reported too:\n%s", attrFinding.Detail)
	}

	diags := v.Diagnostics()
	if !diags.HasErrors() {
		t.Error("a breaking finding must produce an error diagnostic")
	}
}

// A breaking finding does not need an identity schema: it is a claim about
// the resource schema, which every served type has.
func TestVerifyTableBreakingWithoutIdentitySchema(t *testing.T) {
	schemas := fakeProviderSchemas(map[string]fakeType{
		"aws_thing": {args: map[string]string{"id": "optcomp"}},
	})

	v := verifyTable(map[string]TypeIdentity{
		"aws_thing": {Type: "aws_thing", Components: []Component{attr("name")}},
	}, schemas, nil)

	if !findingKinds(v)[FindingArgumentNotInSchema] {
		t.Fatalf("expected the missing argument to be reported without an identity schema: %v", v.Findings)
	}
	if len(v.Skipped) != 1 {
		t.Errorf("the entry is still unverifiable as an identity: %v", v.Skipped)
	}
}

// ---------------------------------------------------------------------------
// Summary
// ---------------------------------------------------------------------------

func TestVerificationSummary(t *testing.T) {
	schemas := fakeProviderSchemas(map[string]fakeType{
		"aws_ssm_parameter": {
			args:     map[string]string{"name": "req", "id": "optcomp"},
			identity: map[string]string{"name": "req", "account_id": "opt"},
		},
	})
	v := VerifyTable(schemas)
	s := v.Summary()
	for _, want := range []string{"1 table entries confirmed", "0 breaking", "1 types admit themselves"} {
		if !strings.Contains(s, want) {
			t.Errorf("summary does not contain %q:\n%s", want, s)
		}
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func findingKinds(v Verification) map[FindingKind]bool {
	out := map[FindingKind]bool{}
	for _, f := range v.Findings {
		out[f.Kind] = true
	}
	return out
}

func findingOf(t *testing.T, v Verification, kind FindingKind) Finding {
	t.Helper()
	for _, f := range v.Findings {
		if f.Kind == kind {
			return f
		}
	}
	t.Fatalf("no %s finding in %v", kind, v.Findings)
	return Finding{}
}
