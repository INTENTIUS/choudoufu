// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
)

// resourceAttrs indexes every resource block in the *.tf files given as
// "<type>.<name>" -> attribute path -> the expression's source text. A
// nested block's attributes are keyed "<block>.<attr>", so the instance's
// launch template id reads attrs["aws_instance.bastion"]["launch_template.id"].
// Source text, not evaluated values: the edges below are references, and a
// reference is exactly its source text in canonical HCL.
func resourceAttrs(t *testing.T, files map[string]string) map[string]map[string]string {
	t.Helper()
	out := map[string]map[string]string{}
	names := make([]string, 0, len(files))
	for name := range files {
		if strings.HasSuffix(name, ".tf") {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		src := []byte(files[name])
		body := parseTF(t, name, files[name])
		for _, b := range body.Blocks {
			if b.Type != "resource" || len(b.Labels) != 2 {
				continue
			}
			attrs := map[string]string{}
			var walk func(prefix string, body *hclsyntax.Body)
			walk = func(prefix string, body *hclsyntax.Body) {
				for k, a := range body.Attributes {
					attrs[prefix+k] = string(bytes.TrimSpace(a.Expr.Range().SliceBytes(src)))
				}
				for _, nb := range body.Blocks {
					walk(prefix+nb.Type+".", nb.Body)
				}
			}
			walk("", b.Body)
			out[b.Labels[0]+"."+b.Labels[1]] = attrs
		}
	}
	return out
}

// findingsEdges is the layer's contract with behold#912's demo, one
// (resource, attribute, source) triple per edge or literal the demo's
// answers rest on. Each line is a fact a person reviewing the estate would
// state: the SSH path from the internet to the bastion, the database's
// replace-forcing attribute, and the role's reach to the table.
var findingsEdges = []struct{ addr, attr, want string }{
	// instance -> launch template -> security group -> ingress 22 from anywhere
	{"aws_instance.bastion", "launch_template.id", "aws_launch_template.bastion.id"},
	{"aws_launch_template.bastion", "vpc_security_group_ids", "[aws_security_group.bastion.id]"},
	{"aws_vpc_security_group_ingress_rule.bastion_ssh", "security_group_id", "aws_security_group.bastion.id"},
	{"aws_vpc_security_group_ingress_rule.bastion_ssh", "from_port", "22"},
	{"aws_vpc_security_group_ingress_rule.bastion_ssh", "to_port", "22"},
	{"aws_vpc_security_group_ingress_rule.bastion_ssh", "cidr_ipv4", `"0.0.0.0/0"`},
	// instance -> subnet -> association -> route table -> 0.0.0.0/0 -> igw
	{"aws_instance.bastion", "subnet_id", "aws_subnet.public.id"},
	{"aws_route_table_association.public", "subnet_id", "aws_subnet.public.id"},
	{"aws_route_table_association.public", "route_table_id", "aws_route_table.public.id"},
	{"aws_route_table.public", "route.cidr_block", `"0.0.0.0/0"`},
	{"aws_route_table.public", "route.gateway_id", "aws_internet_gateway.main.id"},
	{"aws_internet_gateway.main", "vpc_id", "aws_vpc.main.id"},
	// the database a one-line change replaces
	{"aws_db_instance.orders", "storage_encrypted", "false"},
	// role -> attachment -> policy -> table, and the bastion carries the role
	{"aws_iam_role_policy_attachment.app_data", "role", "aws_iam_role.app_data.name"},
	{"aws_iam_role_policy_attachment.app_data", "policy_arn", "aws_iam_policy.app_data.arn"},
	{"aws_iam_instance_profile.app_data", "role", "aws_iam_role.app_data.name"},
	{"aws_launch_template.bastion", "iam_instance_profile.arn", "aws_iam_instance_profile.app_data.arn"},
}

func checkFindingsEdges(attrs map[string]map[string]string) []string {
	var bad []string
	for _, e := range findingsEdges {
		r, ok := attrs[e.addr]
		if !ok {
			bad = append(bad, e.addr+": no such resource")
			continue
		}
		if got := r[e.attr]; got != e.want {
			bad = append(bad, fmt.Sprintf("%s.%s = %q, want %q", e.addr, e.attr, got, e.want))
		}
	}
	return bad
}

// TestFindingsLayerShape pins every edge and literal behold#912's expected
// answers are written from (issue #2199), at two scales, by parsing the
// generated HCL rather than reading the template.
func TestFindingsLayerShape(t *testing.T) {
	for _, scale := range []int{1, 4} {
		t.Run(fmt.Sprintf("scale=%d", scale), func(t *testing.T) {
			e := buildEstateWith(scale, "tl", true)
			attrs := resourceAttrs(t, e.files)
			for _, b := range checkFindingsEdges(attrs) {
				t.Error(b)
			}
			// The policy names the table's ARN, the reach step 6 asks about.
			if p := attrs["aws_iam_policy.app_data"]["policy"]; !strings.Contains(p, "Resource = aws_dynamodb_table.sessions.arn") {
				t.Errorf("aws_iam_policy.app_data's policy does not name aws_dynamodb_table.sessions.arn:\n%s", p)
			}
		})
	}
}

// TestFindingsLayerShapeHasTeeth breaks one edge of each path and requires
// the check to report it, so TestFindingsLayerShape cannot pass on a
// layer that lost its SSH rule, its route or its attachment.
func TestFindingsLayerShapeHasTeeth(t *testing.T) {
	for _, tc := range []struct{ file, from, to string }{
		{"bastion.tf", `cidr_ipv4         = "0.0.0.0/0"`, `cidr_ipv4         = "10.0.0.0/8"`},
		{"bastion.tf", "gateway_id = aws_internet_gateway.main.id", `gateway_id = "local"`},
		{"data.tf", "policy_arn = aws_iam_policy.app_data.arn", `policy_arn = "arn:aws:iam::aws:policy/ReadOnlyAccess"`},
		{"data.tf", "storage_encrypted    = false", "storage_encrypted    = true"},
	} {
		t.Run(tc.to, func(t *testing.T) {
			e := buildEstateWith(1, "tl", true)
			if !strings.Contains(e.files[tc.file], tc.from) {
				t.Fatalf("%s does not contain %q: the control no longer breaks anything", tc.file, tc.from)
			}
			e.files[tc.file] = strings.Replace(e.files[tc.file], tc.from, tc.to, 1)
			if bad := checkFindingsEdges(resourceAttrs(t, e.files)); len(bad) == 0 {
				t.Errorf("replacing %q with %q in %s went unreported", tc.from, tc.to, tc.file)
			}
		})
	}
}

// TestFindingsOffChangesNothing: without -findings there is no bastion.tf
// or data.tf, and every file is the one buildEstate writes, so the counts
// gen_test.go pins (79 at scale 1, 301 at scale 4) are the default's.
func TestFindingsOffChangesNothing(t *testing.T) {
	for _, scale := range []int{1, 4} {
		a, b := buildEstate(scale, "tl"), buildEstateWith(scale, "tl", false)
		if len(a.files) != len(b.files) {
			t.Fatalf("scale %d: %d files vs %d", scale, len(a.files), len(b.files))
		}
		for name, content := range a.files {
			if b.files[name] != content {
				t.Errorf("scale %d: %s differs with findings off", scale, name)
			}
		}
		for _, name := range []string{"bastion.tf", "data.tf"} {
			if _, ok := a.files[name]; ok {
				t.Errorf("scale %d: %s written without -findings", scale, name)
			}
		}
		if a.composition.findingsResources != 0 {
			t.Errorf("scale %d: findingsResources = %d without -findings", scale, a.composition.findingsResources)
		}
	}
}

// TestFindingsCountsMatchHCL pins the layer's resource count against the
// blocks it writes (every block in it is singular: no count, no for_each),
// and the total at scale 4, which behold#912's entry asserts.
func TestFindingsCountsMatchHCL(t *testing.T) {
	e := buildEstateWith(4, "tl", true)
	for file, want := range map[string]int{"bastion.tf": findingsBastionResources, "data.tf": findingsDataResources} {
		got := 0
		for _, b := range parseTF(t, file, e.files[file]).Blocks {
			if b.Type == "resource" {
				got++
			}
		}
		if got != want {
			t.Errorf("%s declares %d resources, the composition counts %d", file, got, want)
		}
	}
	if got, want := e.composition.totalResources(), 301+findingsBastionResources+findingsDataResources; got != want {
		t.Errorf("scale 4 with -findings: %d resources, want %d", got, want)
	}

	_, instances := countIAMRoleInstances(t, e.files)
	if want := IAMRoleInstances(4) + findingsIAMRoles; instances != want {
		t.Errorf("scale 4 with -findings expands to %d aws_iam_role instances, -iam-roles would print %d", instances, want)
	}
}

// TestFindingsLayerIsCanonicalAndStock holds the flagged output to the same
// two rules as the default: canonical formatting, and no choudoufu construct.
func TestFindingsLayerIsCanonicalAndStock(t *testing.T) {
	e := buildEstateWith(4, "tl", true)
	for name, content := range e.files {
		if strings.HasSuffix(name, ".tf") && string(hclwrite.Format([]byte(content))) != content {
			t.Errorf("%s is not canonically formatted", name)
		}
		for _, bad := range forbiddenSubstrings {
			if strings.Contains(content, bad) {
				t.Errorf("%s contains %q", name, bad)
			}
		}
		if liveBlockPattern.MatchString(content) {
			t.Errorf("%s contains a live block", name)
		}
	}
}
