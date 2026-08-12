// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"fmt"

	"github.com/hashicorp/hcl/v2/hclsyntax"

	"github.com/opentofu/opentofu/internal/addrs"
	"github.com/opentofu/opentofu/internal/configs"
)

// checkReceiptSecretRule enforces the directly visible case of
// stateless/RECEIPTS.md's "Secrets discipline": a resource that is
// statically recognizable as a receipt (see receiptResources in
// receipt_leaf.go) whose value expression contains a direct reference to an
// input variable declared sensitive = true in the same module. Hashing does
// not launder a secret — a hash over low-entropy secret material is
// offline-guessable by anyone holding the receipt and knowing the input
// shape — so the reference is rejected whether or not it sits inside a hash
// call.
//
// The boundary is the same direct-traversal boundary the leaf rule states
// in RECEIPTS.md's "Lint enforcement" section: one expression's traversals
// are read off the page, and value flow is never traced. A sensitive value
// routed through a local, arriving through a module boundary, or read from
// a resource or data-source attribute whose provider schema marks it
// sensitive is not caught, because each of those requires data-flow
// analysis or schema knowledge this package deliberately does not have.
// Those cases remain a review rule, and RECEIPTS.md says so.
func checkReceiptSecretRule(mod *configs.Module, path addrs.Module, issues *[]Issue) {
	receipts := receiptResources(mod)
	if len(receipts) == 0 {
		return
	}

	for _, resource := range mod.ManagedResources {
		addr := resource.Addr().String()
		if !receipts[addr] {
			continue
		}
		body, ok := resource.Config.(*hclsyntax.Body)
		if !ok {
			continue
		}
		attr, ok := body.Attributes["value"]
		if !ok {
			continue
		}

		for _, traversal := range attr.Expr.Variables() {
			ref, refDiags := addrs.ParseRef(traversal)
			if refDiags.HasErrors() || ref == nil {
				continue
			}
			v, ok := ref.Subject.(addrs.InputVariable)
			if !ok {
				continue
			}
			decl, ok := mod.Variables[v.Name]
			if !ok || !decl.Sensitive {
				continue
			}
			*issues = append(*issues, Issue{
				Rule:      RuleReceiptSecret,
				Construct: fmt.Sprintf("receipt %s value reads sensitive variable var.%s", addr, v.Name),
				Module:    path,
				Detail: fmt.Sprintf(
					"%q is a receipt under the /tofu-receipts/ naming convention "+
						"(stateless/RECEIPTS.md), and its value expression reads var.%s, which is "+
						"declared sensitive. Effect inputs must reference secrets by pointer, never "+
						"by value: hashing does not help, because a hash over low-entropy secret "+
						"material is offline-guessable by anyone holding the receipt and knowing "+
						"the input shape. Feed the secret's ARN and version identifier into the "+
						"hash instead, which leaks nothing and still answers whether it rotated",
					addr, v.Name,
				),
				Subject: traversal.SourceRange(),
			})
		}
	}
}
