// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"fmt"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
)

// receiptType and receiptNamePrefix are the naming convention
// live/RECEIPTS.md defines for a receipt: a resource of this type
// whose name argument is a literal string starting with this prefix
// (/tofu-receipts/<estate>/<effect>) is statically recognizable as one, with
// no marker or annotation beyond what is already in the resource block.
const (
	receiptType       = "aws_ssm_parameter"
	receiptNamePrefix = "/tofu-receipts/"
)

// checkReceiptLeafRule rejects any direct reference — from another managed
// resource's own configuration body, from a resource's depends_on, from an
// output's expression, or from an output's depends_on — into a resource that
// is statically recognizable as a receipt (live/RECEIPTS.md, "the leaf
// rule"). A receipt's own configuration is exempt from the walk: its value
// expression is expected to read other resources' attributes, which is how
// its hash is built, and that is the receipt's own outbound edge, not an
// inbound one.
//
// This is a direct-traversal check only, not full data-flow analysis: it
// reads the traversals off one expression at a time and does not trace a
// value through an intermediate local or across a module boundary. See
// RECEIPTS.md, "Lint enforcement" for that boundary stated in full, and
// count_index.go's checkCountIndex for the same style of traversal walk
// applied to a different rule.
func checkReceiptLeafRule(mod *configs.Module, path addrs.Module, issues *[]Issue) {
	receipts := receiptResources(mod)
	if len(receipts) == 0 {
		return
	}

	for _, resource := range mod.ManagedResources {
		addr := resource.Addr().String()
		if receipts[addr] {
			continue
		}

		// resource.Config's underlying body still carries depends_on as an
		// ordinary attribute (see count_index.go's comment on
		// hclsyntax.Body.PartialContent for why), so walking it here already
		// covers resource.DependsOn; a second pass over that field would
		// double-report the same traversal.
		if body, ok := resource.Config.(*hclsyntax.Body); ok {
			for _, traversal := range allTraversals(body) {
				reportReceiptReference(traversal, addr, path, receipts, issues)
			}
		}
	}

	names := make([]string, 0, len(mod.Outputs))
	for name := range mod.Outputs {
		names = append(names, name)
	}
	for _, name := range names {
		output := mod.Outputs[name]
		addr := "output." + name
		if output.Expr != nil {
			for _, traversal := range output.Expr.Variables() {
				reportReceiptReference(traversal, addr, path, receipts, issues)
			}
		}
		for _, traversal := range output.DependsOn {
			reportReceiptReference(traversal, addr, path, receipts, issues)
		}
	}
}

// receiptResources returns the set of managed-resource addresses (as
// addrs.Resource.String() renders them, e.g. "aws_ssm_parameter.demo_effect")
// in mod that are statically recognizable as receipts: type receiptType, with
// a name argument that evaluates to a constant string starting with
// receiptNamePrefix. A name argument that is not a static literal (built
// from a variable, a local, an interpolation) is conservatively not
// recognized — the rule never guesses at receipt-ness, only confirms it when
// it is evident on the page.
func receiptResources(mod *configs.Module) map[string]bool {
	out := make(map[string]bool)
	for _, resource := range mod.ManagedResources {
		if resource.Type != receiptType {
			continue
		}
		body, ok := resource.Config.(*hclsyntax.Body)
		if !ok {
			continue
		}
		attr, ok := body.Attributes["name"]
		if !ok {
			continue
		}
		val, diags := attr.Expr.Value(nil)
		if diags.HasErrors() || val.IsNull() || !val.IsKnown() || val.Type() != cty.String {
			continue
		}
		if strings.HasPrefix(val.AsString(), receiptNamePrefix) {
			out[resource.Addr().String()] = true
		}
	}
	return out
}

// allTraversals collects every variable traversal in body's own attribute
// expressions and nested blocks, at every depth, with no meta-argument
// exclusion: unlike checkCountIndex's walk, a receipt referenced from a
// count/for_each/provider expression is exactly the kind of dependency this
// rule exists to catch, not a position to skip.
func allTraversals(body *hclsyntax.Body) []hcl.Traversal {
	var out []hcl.Traversal
	for _, attr := range body.Attributes {
		out = append(out, attr.Expr.Variables()...)
	}
	for _, block := range body.Blocks {
		out = append(out, allTraversals(block.Body)...)
	}
	return out
}

// reportReceiptReference appends an Issue if traversal's subject resolves to
// a managed resource address in receipts.
func reportReceiptReference(traversal hcl.Traversal, fromAddr string, path addrs.Module, receipts map[string]bool, issues *[]Issue) {
	ref, refDiags := addrs.ParseRef(traversal)
	if refDiags.HasErrors() || ref == nil {
		return
	}

	var resAddr addrs.Resource
	switch subj := ref.Subject.(type) {
	case addrs.Resource:
		resAddr = subj
	case addrs.ResourceInstance:
		resAddr = subj.Resource
	default:
		return
	}
	if resAddr.Mode != addrs.ManagedResourceMode {
		return
	}

	receipt := resAddr.String()
	if !receipts[receipt] {
		return
	}

	*issues = append(*issues, Issue{
		Rule:      RuleReceiptLeaf,
		Construct: fmt.Sprintf("%s references receipt %s", fromAddr, receipt),
		Module:    path,
		Detail: fmt.Sprintf(
			"%q is a receipt under the /tofu-receipts/ naming convention "+
				"(live/RECEIPTS.md). Nothing may reference a receipt's attributes: a "+
				"receipt another resource or output depends on is load-bearing, which is "+
				"authority creep, the mechanism by which a record grows back into state. Read "+
				"the value that fed the receipt's own hash from its original source instead of "+
				"from the receipt",
			receipt,
		),
		Subject: traversal.SourceRange(),
	})
}
