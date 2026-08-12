// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"fmt"

	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
)

// hashFunctions is the set of function names Guard 2 recognizes as "visibly
// a hash call" when one of them is the outermost expression of a receipt's
// value. The set is the config language's hash family over in-memory input
// (the filesha* variants read from disk, which a receipt's declared inputs
// never do). Weak members (md5, sha1) are included deliberately: Guard 2's
// concern is that the value is content-opaque and not a second copy of the
// inputs, not collision resistance, and the secrets rule in
// receipt_secret.go is what polices hashing material that must not be
// guessable.
var hashFunctions = map[string]bool{
	"md5":          true,
	"sha1":         true,
	"sha256":       true,
	"sha512":       true,
	"base64sha256": true,
	"base64sha512": true,
}

// checkReceiptValueRule enforces live/RECEIPTS.md's Guard 2 ("Hash-only
// values, and never SecureString") on every resource that is statically
// recognizable as a receipt (see receiptResources in receipt_leaf.go for the
// recognition and its conservative boundary).
//
// Two shapes are rejected. A receipt declared with a literal
// type = "SecureString" is rejected outright: a hash is not a secret, and
// SecureString drags KMS key custody into a mechanism whose purpose is a
// plain, cheap comparison point. And a receipt whose value expression is not
// visibly one of the two documented flavors is flagged: a hash receipt's
// value has a hash function as its outermost call, and an existence
// receipt's value is a constant literal (e.g. "done"). Anything else — the
// raw inputs, an attribute read, a value routed through a local — is not
// evidently either flavor, so it is reported even when the local really does
// hold a hash. That is the deliberate direction for this rule: the fix is to
// inline the hash call so the discipline is evident on the page, and it is
// the direction Guard 2's own heuristic asks for. Like every rule in this
// package, it reads one expression at a time and never traces value flow
// through locals; a type argument built from a variable rather than a
// literal is likewise not judged.
func checkReceiptValueRule(mod *configs.Module, path addrs.Module, issues *[]Issue) {
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

		if attr, ok := body.Attributes["type"]; ok {
			val, diags := attr.Expr.Value(nil)
			if !diags.HasErrors() && !val.IsNull() && val.IsKnown() &&
				val.Type() == cty.String && val.AsString() == "SecureString" {
				*issues = append(*issues, Issue{
					Rule:      RuleReceiptValue,
					Construct: fmt.Sprintf("receipt %s declares type SecureString", addr),
					Module:    path,
					Detail: fmt.Sprintf(
						"%q is a receipt under the /tofu-receipts/ naming convention "+
							"(live/RECEIPTS.md), and a receipt is never a SecureString. Its value "+
							"is a hash or a constant, neither of which is a secret, so encryption buys "+
							"no confidentiality and costs real complexity: KMS key custody, key policy, "+
							"and decrypt permissions on a mechanism whose whole purpose is a plain, "+
							"cheap, readable comparison point. Declare type = \"String\"",
						addr,
					),
					Subject: attr.Expr.Range(),
				})
				// One verdict per receipt: a SecureString receipt is already
				// outside Guard 2, and judging its value shape on top of that
				// would be noise while the type is being fixed.
				continue
			}
		}

		attr, ok := body.Attributes["value"]
		if !ok {
			continue
		}
		if receiptValueShape(attr.Expr) {
			continue
		}
		*issues = append(*issues, Issue{
			Rule:      RuleReceiptValue,
			Construct: fmt.Sprintf("receipt %s value is not visibly a hash or a constant", addr),
			Module:    path,
			Detail: fmt.Sprintf(
				"%q is a receipt under the /tofu-receipts/ naming convention "+
					"(live/RECEIPTS.md), and its value must be evidently one of the two "+
					"documented flavors: a hash receipt wraps its declared inputs in a hash "+
					"function, e.g. sha256(jsonencode(...)), and an existence receipt uses a "+
					"constant literal, e.g. \"done\". This value is neither, which usually means "+
					"the raw inputs (or something derived from the effect's output) are being "+
					"stored, the second copy of configuration data receipts exist to avoid. If "+
					"the value really is a hash computed elsewhere, inline the hash call here so "+
					"the discipline is evident on the page",
				addr,
			),
			Subject: attr.Expr.Range(),
		})
	}
}

// receiptValueShape reports whether expr is visibly one of Guard 2's two
// value flavors: a call whose outermost function is in hashFunctions (hash
// receipt), or an expression that evaluates to a known constant with no
// evaluation context at all (existence receipt). Parentheses are unwrapped
// first; nothing else is normalized, and no value flow is traced.
func receiptValueShape(expr hclsyntax.Expression) bool {
	for {
		paren, ok := expr.(*hclsyntax.ParenthesesExpr)
		if !ok {
			break
		}
		expr = paren.Expression
	}

	if call, ok := expr.(*hclsyntax.FunctionCallExpr); ok {
		return hashFunctions[call.Name]
	}

	val, diags := expr.Value(nil)
	return !diags.HasErrors() && !val.IsNull() && val.IsKnown()
}
