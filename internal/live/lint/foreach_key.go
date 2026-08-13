// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/markerkey"
)

// The for_each key rule.
//
// A for_each instance key does not stay in the configuration: it becomes part
// of the resource's address, the address becomes the tofu-address marker on
// the live resource, and the marker is the only record of ownership a
// stateless run has. live/MARKERS.md therefore bounds what a key may
// contain from two directions at once, and this rule is the enforcement it
// asks for by name ("for_each keys containing `.` or `:` (or any character
// outside the AWS-allowed set enumerated above) are outside the stateless
// subset and should be rejected by lint").
//
// The two bounds:
//
//   - AWS-legal in a tag value. MARKERS.md's list: letters and numbers
//     representable in UTF-8, space, and the characters `+ - = . _ : / @`.
//     A key outside it cannot be written as a marker at all.
//   - Escapable, and unescapable back. The escaped address uses `.` to
//     separate segments and `:` to introduce an instance key, so a key
//     carrying either one produces a value that cannot be split back into
//     the address it came from. `.` and `:` are AWS-legal, which is exactly
//     what made this class of key dangerous rather than merely invalid: it
//     passed lint, stamped a marker, and applied, and only the NEXT run
//     found the wedge (a `:` key reads as a malformed marker; a `.` key
//     makes discovery.UnescapeAddress refuse on deletion), with no in-band
//     way back out.
//
// The intersection is the set this rule admits: letters and numbers, space,
// and `+ - = _ / @`.
//
// That set also happens to close the raw-each.key escaping gap in
// internal/live/stamp. The declared side of the comparison renders an
// instance key through addrs' toHCLQuotedString, which backslash-escapes
// `"`, `\`, tab/CR/LF, `$`/`%` before `{`, and any non-printable rune, while
// the stamped side interpolates each.key raw. Every character that makes
// those two disagree is outside the set above, so for a lint-clean
// configuration the two sides are the same string by construction. See
// stamp.addressExpr.

// ValidForEachKey reports whether a for_each instance key survives the round
// trip through a tofu-address marker: escapable to a marker value, and
// unescapable back to the address it came from.
//
// It is exported because the rule has a second enforcement point:
// internal/live/identity refuses to resolve a resource whose expansion
// produces a key outside the set, so a configuration that reaches identity
// resolution without passing lint (a caller that skipped it, an expression
// lint could not evaluate but identity could) still cannot mint a marker
// nothing can read back. The rule itself lives in [markerkey], one level
// below both packages, so that neither has to import the other to share it.
func ValidForEachKey(key string) bool {
	return markerkey.Valid(key)
}

// InvalidForEachKeyRune returns the first character of key that puts it
// outside the set, and whether there was one. See [markerkey.InvalidRune].
func InvalidForEachKeyRune(key string) (rune, bool) {
	return markerkey.InvalidRune(key)
}

// DescribeForEachKeyRune renders an offending character for a diagnostic. See
// [markerkey.DescribeRune].
func DescribeForEachKeyRune(r rune) string {
	return markerkey.DescribeRune(r)
}

// checkForEachKeys reports every for_each key this rule can see that falls
// outside the marker character set.
//
// "Can see" is the honest boundary. A for_each expression is checked when its
// keys are computable from the static scope alone — a literal collection, or
// one built from variables, locals, path and terraform values. Two other
// shapes are skipped rather than guessed at:
//
//   - for_each over another managed resource (for_each = aws_subnet.this),
//     the estate fixture's own route-table-association idiom. Its keys are
//     the other block's keys, and that block is checked in its own right, so
//     checking it here would only duplicate the finding.
//   - anything the static scope cannot evaluate, including a reference to a
//     data source. Identity resolution refuses those outright ("for_each is
//     not statically knowable"), so they never reach a marker.
func checkForEachKeys(ctx context.Context, mod *configs.Module, path addrs.Module, issues *[]Issue) {
	for _, resource := range mod.ManagedResources {
		if resource.ForEach == nil {
			continue
		}
		keys, ok := staticForEachKeys(ctx, mod, resource.ForEach)
		if !ok {
			continue
		}
		addr := resource.Addr().String()
		for _, key := range keys {
			bad, isBad := InvalidForEachKeyRune(key)
			if !isBad {
				continue
			}
			*issues = append(*issues, Issue{
				Rule:      RuleForEachKey,
				Construct: fmt.Sprintf("for_each key %q in %s", key, addr),
				Module:    path,
				Detail: fmt.Sprintf(
					"the for_each key %q contains %s, which cannot survive the trip through a "+
						"tofu-address marker. An instance key becomes part of the resource's address, the "+
						"address becomes the marker on the live resource, and that marker is the only record "+
						"of ownership a live-markers run has (live/MARKERS.md). A key may contain letters, "+
						"digits, space, and %s: the AWS tag-value character set, less \".\" and \":\", which "+
						"separate the segments of an escaped address and so cannot appear inside one. This is "+
						"caught here rather than at apply on purpose: a key like this applies cleanly and "+
						"wedges every run after it, with no way back that does not go outside OpenTofu. "+
						"Rename the key",
					key, DescribeForEachKeyRune(bad), quotedRuneList(markerkey.Extras),
				),
				Subject: resource.ForEach.Range(),
			})
		}
	}
}

// quotedRuneList renders the extra-character set for a diagnostic.
func quotedRuneList(set string) string {
	parts := make([]string, 0, len(set))
	for _, r := range set {
		parts = append(parts, fmt.Sprintf("%q", string(r)))
	}
	return strings.Join(parts, " ")
}

// staticForEachKeys computes the instance keys a for_each expression produces,
// or reports that they are not computable here.
//
// The traversal pre-filter is not an optimization: staticScopeData panics by
// contract on repetition, resource, module, output and check references
// ("Not Available in Static Context"), so an expression mentioning one must
// not be handed to the static evaluator at all.
func staticForEachKeys(ctx context.Context, mod *configs.Module, expr hcl.Expression) ([]string, bool) {
	if mod == nil || mod.StaticEvaluator == nil {
		return nil, false
	}
	for _, trav := range expr.Variables() {
		switch trav.RootName() {
		case "var", "local", "path", "terraform":
			// Evaluable in a static scope.
		default:
			return nil, false
		}
	}

	val, ok := evalStatic(ctx, mod.StaticEvaluator, expr, "for_each")
	if !ok || val == cty.NilVal || val.IsNull() || !val.IsWhollyKnown() || val.IsMarked() {
		return nil, false
	}

	ty := val.Type()
	var keys []string
	switch {
	case ty.IsMapType(), ty.IsObjectType():
		for it := val.ElementIterator(); it.Next(); {
			k, _ := it.Element()
			if k.Type() != cty.String || k.IsNull() {
				return nil, false
			}
			keys = append(keys, k.AsString())
		}
	case ty.IsSetType(), ty.IsListType(), ty.IsTupleType():
		for it := val.ElementIterator(); it.Next(); {
			_, v := it.Element()
			if v.Type() != cty.String || v.IsNull() {
				// A non-string collection is not a legal for_each at all;
				// identity resolution says so with its own message.
				return nil, false
			}
			keys = append(keys, v.AsString())
		}
	default:
		return nil, false
	}

	// Sorted so that two runs over one configuration report the same issues
	// in the same order; cty iterates a map in key order already, but an
	// object type's attribute order is not something to rely on here.
	sort.Strings(keys)
	return keys, true
}

// evalStatic evaluates an expression through the module's static evaluator,
// degrading to "cannot check" instead of failing the run. subject names the
// position being evaluated ("for_each", "count") in the identifier the
// evaluator is handed; the diagnostics that would render it are dropped, so
// it matters only in a debugger.
//
// Both halves of the degrade are deliberate. Diagnostics are dropped because
// an expression this pass cannot evaluate is not its finding to report —
// identity resolution evaluates the same expression in a richer scope and
// reports what it finds there. The recover is because the static scope's
// data source panics rather than erroring for the reference classes it does
// not serve; the traversal pre-filters in [staticForEachKeys] and
// [staticCount] keep those out, and this is the second line so that a lint
// pass can never be the thing that crashes a run.
func evalStatic(ctx context.Context, eval *configs.StaticEvaluator, expr hcl.Expression, subject string) (val cty.Value, ok bool) {
	defer func() {
		if recover() != nil {
			val, ok = cty.NilVal, false
		}
	}()

	ident := configs.StaticIdentifier{
		Module:    addrs.RootModule,
		Subject:   subject,
		DeclRange: expr.Range(),
	}
	v, diags := eval.Evaluate(ctx, expr, ident)
	if diags.HasErrors() {
		return cty.NilVal, false
	}
	return v, true
}
