// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
)

// The per-attribute half of an identity: the same information the import-ID
// string holds, minus the separator characters that only the string has.
// Those characters are the part of this table no provider schema can back,
// so splitting them out is what lets an import ask by identity object.

// TestConcreteIdentityValues pins the split over the estate fixture, which is
// where every shape in the table meets a real configuration.
func TestConcreteIdentityValues(t *testing.T) {
	cfg := loadConfig(t, estateDir(t), nil)

	result, diags := Resolve(context.Background(), cfg)
	assertNoErrors(t, diags)

	tests := []struct {
		addr string
		want map[string]string
	}{
		// One attribute, read from the argument of the same name.
		{`aws_s3_bucket.data`, map[string]string{"bucket": "tofu-stateless-e2e-data"}},
		{`aws_iam_role.app`, map[string]string{"name": "tofu-stateless-e2e-app"}},
		{`aws_cloudwatch_log_group.app`, map[string]string{"name": "/stateless-e2e/app"}},
		{`aws_cloudwatch_metric_alarm.cpu`, map[string]string{"alarm_name": "tofu-stateless-e2e-cpu"}},

		// Two attributes and a separator. The ":" is in the import ID and in
		// neither attribute, which is the whole point.
		{`aws_iam_role_policy.app`, map[string]string{
			"role": "tofu-stateless-e2e-app",
			"name": "tofu-stateless-e2e-app-inline",
		}},
		{`aws_iam_role_policy_attachment.app`, map[string]string{
			"role":       "tofu-stateless-e2e-app",
			"policy_arn": "arn:aws:iam::aws:policy/ReadOnlyAccess",
		}},

		// The child collapses to a literal because its parent is concrete,
		// and the attribute follows the same collapse.
		{`aws_s3_bucket_policy.data`, map[string]string{"bucket": "tofu-stateless-e2e-data"}},
	}

	for _, tc := range tests {
		t.Run(tc.addr, func(t *testing.T) {
			r := resolutionAt(t, result, tc.addr)
			if !reflect.DeepEqual(r.IdentityValues, tc.want) {
				t.Errorf("identity values are %s, want %s", showValues(r.IdentityValues), showValues(tc.want))
			}
			// The string and the split have to be the same claim: every
			// attribute's value appears in the import ID.
			for _, v := range tc.want {
				if !strings.Contains(r.ImportID, v) {
					t.Errorf("import ID %q does not contain the %q the split claims", r.ImportID, v)
				}
			}
		})
	}
}

// TestParentDerivedIdentityAttrs is the same split for a formula: each
// attribute carries its own parts, and rendering them gives the identity
// object the import should ask by.
func TestParentDerivedIdentityAttrs(t *testing.T) {
	cfg := loadConfig(t, estateDir(t), nil)

	result, diags := Resolve(context.Background(), cfg)
	assertNoErrors(t, diags)

	r := resolutionAt(t, result, `aws_route.internet_gateway`)
	if r.Formula == nil {
		t.Fatalf("%s is not parent-derived", r)
	}

	var names []string
	for _, a := range r.Formula.Attrs {
		names = append(names, a.Name)
	}
	if !reflect.DeepEqual(names, []string{"route_table_id", "destination_cidr_block"}) {
		t.Fatalf("the formula splits into %v, want [route_table_id destination_cidr_block]", names)
	}

	// Rendering with the route table's live ID gives both halves separately,
	// with the "_" that joins them into an import ID nowhere in sight.
	values, ok := r.Formula.RenderAttrs(func(_ addrs.AbsResourceInstance, _ string) (string, bool) {
		return "rtb-live", true
	})
	if !ok {
		t.Fatal("rendering the per-attribute formula failed on a lookup that always succeeds")
	}
	want := map[string]string{
		"route_table_id":         "rtb-live",
		"destination_cidr_block": "0.0.0.0/0",
	}
	if !reflect.DeepEqual(values, want) {
		t.Errorf("rendered %s, want %s", showValues(values), showValues(want))
	}

	// And the string form still renders the same thing joined, because both
	// come off the same parts.
	id, ok := r.Formula.Render(func(_ addrs.AbsResourceInstance, _ string) (string, bool) {
		return "rtb-live", true
	})
	if !ok || id != "rtb-live_0.0.0.0/0" {
		t.Errorf("the import ID rendered as %q (ok=%v), want rtb-live_0.0.0.0/0", id, ok)
	}
}

// TestIdentityValuesRefusedWhereTheConfigurationHasNone is the honest empty
// answer. A route table association is identified by the rtbassoc- ID the
// provider assigns; the table builds the documented import *string* out of a
// subnet and a route table, and says so by supplying no identity attribute
// for either half.
func TestIdentityValuesRefusedWhereTheConfigurationHasNone(t *testing.T) {
	cfg := loadConfig(t, estateDir(t), nil)

	result, diags := Resolve(context.Background(), cfg)
	assertNoErrors(t, diags)

	r := resolutionAt(t, result, `aws_route_table_association.this["a"]`)
	if r.Formula == nil {
		t.Fatalf("%s is not parent-derived", r)
	}
	if len(r.Formula.Attrs) != 0 {
		t.Errorf("the association's formula splits into %v; nothing in its configuration is an identity attribute", r.Formula.Attrs)
	}
	values, ok := r.Formula.RenderAttrs(func(_ addrs.AbsResourceInstance, _ string) (string, bool) {
		return "x", true
	})
	if !ok || values != nil {
		t.Errorf("rendering an association's identity produced %s, want nothing", showValues(values))
	}
}

// TestSNSTopicComposesOneAttribute: several components, one identity
// attribute. The colons inside an ARN are inside a value rather than between
// two of them, so unlike every other separator in the table they survive.
func TestSNSTopicComposesOneAttribute(t *testing.T) {
	cfg := loadConfig(t, estateDir(t), nil)

	result, diags := ResolveIn(context.Background(), cfg, CloudContext{
		AccountID: "000000000000",
		Region:    "us-east-1",
	})
	assertNoErrors(t, diags)

	r := resolutionAt(t, result, `aws_sns_topic.alerts`)
	if r.Class != ClassConcrete {
		t.Fatalf("%s did not resolve concrete against a cloud context", r)
	}
	want := map[string]string{"arn": r.ImportID}
	if !reflect.DeepEqual(r.IdentityValues, want) {
		t.Errorf("identity values are %s, want the whole ARN under one attribute: %s", showValues(r.IdentityValues), showValues(want))
	}
}

// TestComposesIdentityAgainstRealRequirements walks the table against the
// attributes the real AWS provider requires for import (live/survey.json's
// record of them, transcribed), so that the rule is checked rather than
// asserted twice.
func TestComposesIdentity(t *testing.T) {
	tests := []struct {
		typeName string
		required []string
		want     bool
	}{
		{"aws_s3_bucket", []string{"bucket"}, true},
		{"aws_iam_role", []string{"name"}, true},
		{"aws_iam_role_policy_attachment", []string{"policy_arn", "role"}, true},
		{"aws_route53_record", []string{"name", "type", "zone_id"}, true},
		{"aws_route", []string{"route_table_id"}, true},
		{"aws_lb_target_group_attachment", []string{"target_group_arn", "target_id"}, true},
		{"aws_sns_topic", []string{"arn"}, true},

		// The provider identifies an association by the ID it assigns, and
		// the configuration holds no such value.
		{"aws_route_table_association", []string{"id"}, false},
		// A server-assigned type composes nothing whatever the schema says.
		{"aws_vpc", []string{"id"}, false},
		// A type whose entry names an attribute the provider does not
		// require is not thereby able to compose the one it does.
		{"aws_s3_bucket", []string{"arn"}, false},
	}

	for _, tc := range tests {
		t.Run(tc.typeName+"/"+strings.Join(tc.required, "+"), func(t *testing.T) {
			entry, ok := LookupType(tc.typeName)
			if !ok {
				t.Fatalf("%s is not in the table", tc.typeName)
			}
			if got := entry.ComposesIdentity(tc.required); got != tc.want {
				t.Errorf("ComposesIdentity(%v) = %v, want %v", tc.required, got, tc.want)
			}
		})
	}
}

// TestIdentityAttrNotInSchemaIsReported is the per-attribute check in
// schema_verify: an entry that claims to supply an attribute the provider's
// identity schema does not have.
func TestIdentityAttrNotInSchemaIsReported(t *testing.T) {
	table := map[string]TypeIdentity{
		"aws_thing": {
			Type: "aws_thing",
			Components: []Component{
				inAttr("handle", attr("name")),
			},
			ImportSyntax:  "NAME",
			IdentityAttrs: []string{"name"},
		},
	}
	schemas := fakeProviderSchemas(map[string]fakeType{
		"aws_thing": {
			args:     map[string]string{"name": "req"},
			identity: map[string]string{"name": "req"},
		},
	})

	v := verifyTable(table, schemas, nil)

	var found bool
	for _, f := range v.Findings {
		if f.Kind == FindingIdentityAttrNotInSchema {
			found = true
			if f.Breaking {
				t.Error("a per-attribute divergence was reported as breaking; the import-ID string still works")
			}
			if !strings.Contains(f.Detail, `"handle"`) {
				t.Errorf("the finding does not name the attribute it disputes: %s", f.Detail)
			}
		}
	}
	if !found {
		t.Errorf("no per-attribute finding for an entry claiming an attribute the schema lacks:\n%v", v.Findings)
	}
	if len(v.IdentityImportable) != 0 {
		t.Errorf("the entry was still counted importable by identity: %v", v.IdentityImportable)
	}
}

// ---- helpers ---------------------------------------------------------

func resolutionAt(t *testing.T, result *Result, addr string) Resolution {
	t.Helper()
	for _, r := range result.All() {
		if r.Addr.String() == addr {
			return r
		}
	}
	t.Fatalf("%s is not in the result", addr)
	return Resolution{}
}

func showValues(m map[string]string) string {
	if len(m) == 0 {
		return "{}"
	}
	parts := make([]string, 0, len(m))
	for k, v := range m {
		parts = append(parts, fmt.Sprintf("%s=%q", k, v))
	}
	sort.Strings(parts)
	return "{" + strings.Join(parts, " ") + "}"
}
