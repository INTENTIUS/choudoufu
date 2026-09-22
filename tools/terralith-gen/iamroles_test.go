// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

// TestIAMRoleInstancesMatchGeneratedHCL pins IAMRoleInstances against the
// generated HCL, not against buildEstate's own counters (issue #1230).
//
// The number a real account's IAM role quota is charged for is the INSTANCE
// count after count and for_each expand, and the issue's own trap is that
// counting `resource "aws_iam_role"` blocks undercounts it: 23 blocks at
// scale 3, 33 instances. So this regenerates at several scales, parses
// every *.tf the way terraform would read it, and expands each aws_iam_role
// block itself: `count` from its literal or from the module's var.pod_size
// (bound through pods.tf's `pod_size = local.pod_size` and main.tf's
// locals), and the module call's for_each by the size of the set it
// iterates. A generator change that moves either side fails here rather
// than as a false refusal (or a false pass) on a paid run.
//
// TestCompositionCountsAreExact and TestExpansionCountsAreExact read the
// composition counters; this deliberately does not, so a counter that
// drifted from the HCL it claims to describe cannot vouch for itself.
func TestIAMRoleInstancesMatchGeneratedHCL(t *testing.T) {
	for _, scale := range []int{1, 3, 4, 10} {
		t.Run(fmt.Sprintf("scale=%d", scale), func(t *testing.T) {
			est := buildEstate(scale, "tl")
			blocks, instances := countIAMRoleInstances(t, est.files)

			if got, want := IAMRoleInstances(scale), instances; got != want {
				t.Errorf("IAMRoleInstances(%d) = %d, but the generated HCL expands to %d aws_iam_role instances (%d blocks)",
					scale, got, want, blocks)
			}

			// The undercount #1230 warns about, pinned so the distinction
			// stays measured: blocks are one per named team, one per
			// service, plus count_team and pod_role.
			wantBlocks := teamsPerScale*scale + servicesPerScale*scale + 2
			if blocks != wantBlocks {
				t.Errorf("aws_iam_role blocks = %d, want %d", blocks, wantBlocks)
			}
			if blocks >= instances {
				t.Errorf("blocks (%d) >= instances (%d): nothing expanded, so a block count and an instance count would agree and this test would prove nothing", blocks, instances)
			}
		})
	}
}

// countIAMRoleInstances returns (blocks, instances) for aws_iam_role across
// every *.tf in files, expanding count and the module call's for_each.
func countIAMRoleInstances(t *testing.T, files map[string]string) (blocks, instances int) {
	t.Helper()

	// locals.pod_size, from main.tf.
	localPodSize := readNumberAttr(t, parseTF(t, "main.tf", files["main.tf"]), "locals", nil, "pod_size", nil)

	// The one module call, from pods.tf: how many instances its for_each
	// makes, and what it binds var.pod_size to.
	var modInstances, modPodSize int
	var modSource string
	for _, b := range parseTF(t, "pods.tf", files["pods.tf"]).Blocks {
		if b.Type != "module" {
			continue
		}
		if modSource != "" {
			t.Fatalf("pods.tf declares more than one module call; this test expands exactly one")
		}
		src, diags := b.Body.Attributes["source"].Expr.Value(nil)
		if diags.HasErrors() || src.Type() != cty.String {
			t.Fatalf("pods.tf module source is not a string literal: %s", diags.Error())
		}
		modSource = src.AsString()
		modInstances = forEachSetSize(t, b.Body.Attributes["for_each"].Expr)
		modPodSize = evalInt(t, b.Body.Attributes["pod_size"].Expr, &hcl.EvalContext{
			Variables: map[string]cty.Value{"local": cty.ObjectVal(map[string]cty.Value{"pod_size": cty.NumberIntVal(int64(localPodSize))})},
		})
	}
	if modSource != "./modules/team_pod" {
		t.Fatalf("pods.tf module source = %q, want ./modules/team_pod", modSource)
	}
	if modInstances < 2 {
		t.Fatalf("module call has %d instance(s); the module-nested shape needs more than one", modInstances)
	}

	for name, content := range files {
		if !strings.HasSuffix(name, ".tf") {
			continue
		}
		inModule := strings.HasPrefix(name, "modules/team_pod/")
		multiplier := 1
		var ctx *hcl.EvalContext
		if inModule {
			multiplier = modInstances
			ctx = &hcl.EvalContext{Variables: map[string]cty.Value{
				"var": cty.ObjectVal(map[string]cty.Value{"pod_size": cty.NumberIntVal(int64(modPodSize))}),
			}}
		}
		for _, b := range parseTF(t, name, content).Blocks {
			if b.Type != "resource" || len(b.Labels) != 2 || b.Labels[0] != "aws_iam_role" {
				continue
			}
			blocks++
			if _, ok := b.Body.Attributes["for_each"]; ok {
				t.Fatalf("%s: aws_iam_role.%s carries for_each, which this test does not expand; extend it", name, b.Labels[1])
			}
			n := 1
			if c, ok := b.Body.Attributes["count"]; ok {
				n = evalInt(t, c.Expr, ctx)
			}
			instances += n * multiplier
		}
	}
	return blocks, instances
}

func parseTF(t *testing.T, name, content string) *hclsyntax.Body {
	t.Helper()
	f, diags := hclsyntax.ParseConfig([]byte(content), name, hcl.InitialPos)
	if diags.HasErrors() {
		t.Fatalf("parsing %s: %s", name, diags.Error())
	}
	return f.Body.(*hclsyntax.Body)
}

// readNumberAttr finds the one block of type blockType (with the given
// labels) and evaluates its attr as an int.
func readNumberAttr(t *testing.T, body *hclsyntax.Body, blockType string, labels []string, attr string, ctx *hcl.EvalContext) int {
	t.Helper()
	for _, b := range body.Blocks {
		if b.Type != blockType || strings.Join(b.Labels, "/") != strings.Join(labels, "/") {
			continue
		}
		a, ok := b.Body.Attributes[attr]
		if !ok {
			t.Fatalf("%s block has no %s attribute", blockType, attr)
		}
		return evalInt(t, a.Expr, ctx)
	}
	t.Fatalf("no %s block found", blockType)
	return 0
}

// forEachSetSize sizes a `for_each = toset([...])` expression without a
// full terraform function table: toset over a literal tuple has as many
// members as the tuple has distinct elements.
func forEachSetSize(t *testing.T, expr hclsyntax.Expression) int {
	t.Helper()
	call, ok := expr.(*hclsyntax.FunctionCallExpr)
	if !ok || call.Name != "toset" || len(call.Args) != 1 {
		t.Fatalf("for_each is not toset(<one arg>): %T", expr)
	}
	v, diags := call.Args[0].Value(nil)
	if diags.HasErrors() {
		t.Fatalf("evaluating for_each's tuple: %s", diags.Error())
	}
	seen := map[string]bool{}
	for it := v.ElementIterator(); it.Next(); {
		_, ev := it.Element()
		seen[ev.AsString()] = true
	}
	return len(seen)
}

func evalInt(t *testing.T, expr hclsyntax.Expression, ctx *hcl.EvalContext) int {
	t.Helper()
	v, diags := expr.Value(ctx)
	if diags.HasErrors() {
		t.Fatalf("evaluating %s: %s", expr.Range().String(), diags.Error())
	}
	var n int
	if err := gocty(v, &n); err != nil {
		t.Fatalf("%s is not an integer: %v", expr.Range().String(), err)
	}
	return n
}

func gocty(v cty.Value, n *int) error {
	if v.Type() != cty.Number {
		return fmt.Errorf("type %s", v.Type().FriendlyName())
	}
	bf := v.AsBigFloat()
	if !bf.IsInt() {
		return fmt.Errorf("%s is not integral", bf.String())
	}
	i, _ := bf.Int64()
	*n = int(i)
	return nil
}

// TestIAMRolesFlagPrintsOneInteger is the contract terralith-scale.sh
// reads: -iam-roles writes the formula's value for -scale to stdout as one
// integer and a newline, and refuses a scale the generator would refuse.
func TestIAMRolesFlagPrintsOneInteger(t *testing.T) {
	for _, tc := range []struct {
		scale int
		want  string
	}{{1, "11\n"}, {3, "33\n"}, {50, "550\n"}, {136, "1496\n"}} {
		var out bytes.Buffer
		if err := printIAMRoles(&out, tc.scale); err != nil {
			t.Fatalf("scale=%d: %v", tc.scale, err)
		}
		if out.String() != tc.want {
			t.Errorf("scale=%d: printed %q, want %q", tc.scale, out.String(), tc.want)
		}
	}
	var out bytes.Buffer
	if err := printIAMRoles(&out, 0); err == nil || out.Len() != 0 {
		t.Errorf("scale=0: err=%v out=%q, want an error and nothing printed - a script reading stdout must never see a number for a scale the generator refuses", err, out.String())
	}
}
