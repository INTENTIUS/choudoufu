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
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/markerkey"
)

// The for_each key rule.
//
// A for_each instance key does not stay in the configuration: it becomes part
// of the resource's address, the address becomes the tofu-address marker on
// the live resource, and the marker is the only record of ownership a
// stateless run has. Before issue #210, live/MARKERS.md bounded what a key
// may contain from two directions at once:
//
//   - AWS-legal in a tag value. MARKERS.md's list: letters and numbers
//     representable in UTF-8, space, and the characters `+ - = . _ : / @`.
//   - Escapable, and unescapable back. The escaped address uses `.` to
//     separate segments and `:` to introduce an instance key, so a key
//     carrying either one raw would produce a value that cannot be split
//     back into the address it came from. Issue #178 closed that gap rather
//     than excluding the two characters: [markerkey.Extras] admits both, and
//     internal/live/markers escapes a key's own `.`, `:` and `@` before it
//     ever reaches an address, reversibly, so the address-level separators
//     stay unambiguous. See live/MARKERS.md, "for_each key escaping".
//
// Stock OpenTofu accepts any string as a for_each key, so a key outside
// that set - "a (b)", say - was still a real parity defect, not a
// limitation: refusing it because the marker charset could not hold it was
// a defect in this fork's charset, not in the key. Issue #210 closes that
// gap the same way #178 closed the "." / ":" one: with a reversible
// escaping rather than an exclusion. [markerkey.Encode] carries any
// printable rune outside the AWS-legal set into it (as an Introducer-led
// hex escape; see its own doc comment for the full rule and what it costs
// a key already containing a literal Introducer), which is enough to admit
// nearly everything - except six characters that collide with a
// DIFFERENT, unrelated escaping rule this package does not own:
//
//   - `"`, `\`, and every non-printable rune (tab, CR, LF included) are
//     backslash- or `\u`-escaped by addrs' toHCLQuotedString when OpenTofu
//     itself renders the "declared" side of an address comparison, before
//     this package's own escaping ever runs on the text.
//   - `$` and `%` are doubled by that same function when immediately
//     followed by `{`, a transformation with no per-rune inverse.
//   - `[` and `]` are the delimiters internal/live/markers' EscapeAddress
//     scans for to find an instance key's boundaries inside a full address
//     string; a raw one inside the key corrupts that scan itself, before
//     any key-level escaping gets a chance to run.
//
// [markerkey.InvalidRune]'s doc comment has the full accounting; the short
// version is that none of the six was ever admitted before #210 either, so
// this is where the boundary widens to, not a new restriction.
//
// The declared side of the comparison renders an instance key through
// addrs' toHCLQuotedString, none of whose special cases this rule's
// admitted set can still reach (every rune it touches is now one of the
// six exclusions above), while the stamped side (internal/live/stamp's
// addressExpr, or - for a for_each block where at least one key needs
// Encode's help - stamper.forEachLookupAddressExpr's precomputed lookup
// table) computes the same [markers.EscapeKey] this package's rule exists
// to bound. For a lint-clean configuration the two sides are the same
// string by construction; see internal/live/stamp/foreach_escape_test.go
// for the proof.

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
// outside the marker character set, on a resource's own for_each and on a
// module call's for_each alike (59c, issue #59 phase 3): a module instance
// key becomes part of every address beneath it exactly as a resource
// instance key becomes part of its own, so the same character-set rule
// applies to both.
//
// "Can see" is the honest boundary. A for_each expression is checked when its
// keys are computable from the static scope alone — a literal collection, or
// one built from variables, locals, path and terraform values. Other shapes
// are skipped rather than guessed at:
//
//   - for_each over another managed resource (for_each = aws_subnet.this),
//     the estate fixture's own route-table-association idiom. Its keys are
//     the other block's keys, and that block is checked in its own right, so
//     checking it here would only duplicate the finding. Module calls have
//     no equivalent idiom - for_each on a module block only ever iterates a
//     map, an object or a set of strings - so this shape is resource-only.
//   - anything the static scope cannot evaluate, including a reference to a
//     data source. Identity resolution refuses those outright ("for_each is
//     not statically knowable"), so they never reach a marker. For a module
//     call the same non-staticness is [checkChildModules]'s own refusal
//     (RuleChildModule), which runs independently of this rule - a module
//     for_each this pass cannot evaluate is simply not checked here, exactly
//     as an unevaluable resource for_each is not, and RuleChildModule is
//     what stops the run.
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
		reportBadForEachKeys(keys, addr, addr, resource.ForEach.Range(), path, issues)
	}
	for name, call := range mod.ModuleCalls {
		if call.ForEach == nil {
			continue
		}
		instKeys, diag := identity.ChildModuleKeys(ctx, mod, fmt.Sprintf("module %q", name), call.ForEach)
		if diag != nil {
			// checkChildModules (RuleChildModule) is what refuses a module
			// for_each this pass cannot enumerate; nothing to check here.
			continue
		}
		keys := make([]string, 0, len(instKeys))
		for _, k := range instKeys {
			if sk, ok := k.(addrs.StringKey); ok {
				keys = append(keys, string(sk))
			}
		}
		modLabel := fmt.Sprintf("module %q", name)
		reportBadForEachKeys(keys, modLabel, modLabel, call.ForEach.Range(), path, issues)
	}
}

// reportBadForEachKeys is [checkForEachKeys]'s shared per-key check, over
// either a resource's own keys or a module call's. where names the block a
// key was found on ("aws_eip.pool" or "module \"wrapped\""), used both in
// the issue's Construct label and in its prose.
func reportBadForEachKeys(keys []string, where, addr string, subject hcl.Range, path addrs.Module, issues *[]Issue) {
	for _, key := range keys {
		bad, isBad := InvalidForEachKeyRune(key)
		if !isBad {
			continue
		}
		*issues = append(*issues, Issue{
			Rule:      RuleForEachKey,
			Construct: fmt.Sprintf("for_each key %q in %s", key, where),
			Module:    path,
			Detail: fmt.Sprintf(
				"the for_each key %q contains %s, which cannot survive the trip through a "+
					"tofu-address marker. An instance key becomes part of the address of every "+
					"resource at or beneath %s, the address becomes the marker on the live "+
					"resource, and that marker is the only record of ownership a live-markers "+
					"run has (live/MARKERS.md). A key may contain any printable character except "+
					"%s (live/MARKERS.md, \"for_each key escaping\") - each of those six collides "+
					"with a different, unrelated escaping rule rather than with the AWS tag-value "+
					"charset itself, which this fork's own escaping (issue #210) can now carry "+
					"almost anything else into. This is caught here rather than at apply on "+
					"purpose: a key like this applies cleanly and wedges every run after it, with "+
					"no way back that does not go outside OpenTofu. Rename the key",
				key, DescribeForEachKeyRune(bad), addr, quotedRuneList(markerkey.Excluded),
			),
			Subject: subject,
		})
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
		case "var", "local", "path", "terraform", "tofu":
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
