// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package stamp

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/lang"
)

// This file is GitHub issue #115: a per-instance marker could be wrong four
// ways and only warn.
//
// [sameExpr] compares structure, and it recognises exactly one template -
// the one this pass would have written. Everything else that merely mentions
// count.index went to [stamper.staticString], which cannot evaluate a
// repetition reference, and out through verifyUnreadable as a warning saying
// the tag was left as written. So:
//
//	"aws_eip.pool:${count.index}"          accepted, correct
//	"aws_eip.pool[${count.index}]"         warning, wrong marker applied
//	"${count.index}"                       warning, wrong marker applied
//	"pool-${count.index}"                  warning, wrong marker applied
//	format("aws_eip.pool:%d", count.index) warning, and actually correct
//
// An operator following the conflict message's own advice - "write it as an
// expression that interpolates ${count.index}" - can land on any of the
// middle three, get a warning rather than an error, and apply a marker
// discovery will never match. That is the failure the must-stamp rule exists
// to prevent, reached by a different road.
//
// The last line is the reason the answer is not simply "make the warning an
// error". format() produces the right marker; so does a template built from
// a local holding the prefix. Refusing those would refuse configurations
// that work, and #115 lists comparing evaluated values as the alternative.
// That is what this does: evaluate both expressions once per instance the
// block expands to, and compare the strings. A structurally different
// expression producing the right marker is right, and one producing the
// wrong marker is wrong on every instance rather than unreadable.
//
// It also gives the chunked case an answer it did not have. When an address
// is split across continuation tags, this pass's own expression is a
// substr() window over the template, which no interpolation of ${count.index}
// can match structurally - so every continuation tag's conflict message
// demanded a form that could not be written, while a sibling message in the
// same file said continuation tags are never edited by hand. Comparing
// values ends that: a matching expression verifies, and a non-matching one
// is told to remove the tag, which is a remedy that exists (this pass
// appends a marker the configuration does not set).

// perInstanceVerdict compares the author's expression against the one this
// pass would write, instance by instance.
//
// ok is false when the comparison could not be made at all - the block's
// instance keys are not statically known, or one of the two expressions
// would not evaluate even with the repetition data supplied. The caller
// falls back to its previous behaviour there, which is the honest one: this
// run cannot check the tag, and says so.
func (s *stamper) perInstanceVerdict(ctx context.Context, rc *configs.Resource, m marker, cur hclsyntax.Expression) (v verdict, detail string, ok bool) {
	keys, ok := s.instanceKeys(ctx, rc)
	if !ok {
		return 0, "", false
	}
	if len(keys) == 0 {
		// count = 0, or an empty for_each. There is nothing to stamp and
		// nothing to disagree with, so the tag as written is not this run's
		// business either way. Verifying is right; declining would send the
		// caller to a message saying the expression could not be read, which
		// is untrue - there were simply no instances to read it for.
		return verifyOK, "", true
	}

	base, ok := s.repetitionContext(ctx, rc, cur, m.expr, m.legacyExpr)
	if !ok {
		return 0, "", false
	}

	for _, key := range keys {
		scope := base.NewChild()
		scope.Variables = key.vars()

		want, ok := evalString(m.expr, scope)
		if !ok {
			// This pass's own expression did not evaluate, which is a bug
			// here rather than anything the configuration did. Decline the
			// comparison rather than blame the author for it.
			return 0, "", false
		}
		got, ok := evalString(cur, scope)
		if !ok {
			return 0, "", false
		}
		if got == want {
			continue
		}
		if m.legacyExpr != nil {
			// A configuration already stamped under the pre-issue-#178
			// grammar (only possible for a key containing "@") produces the
			// legacy value here, not the current one, and that is still
			// correct: see marker.legacyExpr's doc comment.
			if legacyWant, ok := evalString(m.legacyExpr, scope); ok && got == legacyWant {
				continue
			}
		}
		return verifyConflict, perInstanceConflictDetail(rc, m, key, got, want), true
	}
	return verifyOK, "", true
}

// perInstanceConflictDetail is the message for an expression that evaluates
// cleanly and produces the wrong marker.
//
// It quotes the first instance that disagrees, with both values, because
// "your expression is wrong" is not actionable and "it produces X where this
// run would write Y" is.
func perInstanceConflictDetail(rc *configs.Resource, m marker, key instanceKey, got, want string) string {
	addr := rc.Addr()

	if isContinuationKey(m.key) {
		return fmt.Sprintf(
			"%s sets %s to an expression that produces %q for %s, and this run would write %q there. "+
				"%s is a tofu-address continuation tag: this address does not fit one tag value, so it is "+
				"split across %s and its continuations, and the split is worked out from the longest instance "+
				"in the block. Remove the tag and let this run write the whole set - it appends the markers a "+
				"configuration does not set, so removing it is enough. See live/MARKERS.md, "+
				"\"tofu-address continuation tags\".",
			addr, m.key, got, key.describe(rc), want, m.key, TagAddress)
	}

	return fmt.Sprintf(
		"%s sets %s to an expression that produces %q for %s, and this run would write %q there. "+
			"A marker that does not match the address is one discovery will never find, so this is refused "+
			"rather than left as written. Remove the tag and let this run stamp it, or write an expression "+
			"that produces the same value - interpolating %s into the escaped address is the shape this pass "+
			"writes, but any expression producing the same string verifies.",
		addr, m.key, got, key.describe(rc), want, instanceRefKeyword(rc))
}

// isContinuationKey reports whether a marker key is one of tofu-address's
// continuation tags rather than tofu-address or tofu-slot.
func isContinuationKey(key string) bool {
	return key != TagAddress && strings.HasPrefix(key, TagAddress+"-")
}

// instanceKey is one instance of a count- or for_each-expanded block, in the
// form the repetition variables need.
type instanceKey struct {
	index int    // count.index, when counted
	key   string // each.key, otherwise
	count bool

	// value is each.value, as the for_each expression actually produced it.
	// cty.NilVal when this pass could not determine it, which makes an
	// expression referring to each.value fail to evaluate and the whole
	// comparison decline - see [instanceKey.vars].
	value cty.Value
}

func (k instanceKey) vars() map[string]cty.Value {
	if k.count {
		return map[string]cty.Value{
			"count": cty.ObjectVal(map[string]cty.Value{
				"index": cty.NumberIntVal(int64(k.index)),
			}),
		}
	}

	each := map[string]cty.Value{"key": cty.StringVal(k.key)}
	if k.value != cty.NilVal {
		each["value"] = k.value
	}
	// When value is unknown, each.value is simply absent from the object, so
	// an expression using it fails to evaluate and [perInstanceVerdict]
	// declines rather than comparing.
	//
	// The first version of this bound each.value to the KEY unconditionally,
	// with a comment claiming that an expression built from a map's values
	// "will disagree on the comparison and be refused". It does the
	// opposite. The substitution is applied to both sides, so
	// "aws_eip.pool:${each.value}" over `{ a = "zzz" }` evaluates to
	// "aws_eip.pool:a" here, matches, and verifies - while the plan writes
	// "aws_eip.pool:zzz" and discovery never finds it. An adversarial audit
	// caught it: the change had turned a warning into silence, which is the
	// one outcome this phase exists to remove.
	return map[string]cty.Value{"each": cty.ObjectVal(each)}
}

func (k instanceKey) describe(rc *configs.Resource) string {
	if k.count {
		return fmt.Sprintf("%s[%d]", rc.Addr(), k.index)
	}
	return fmt.Sprintf("%s[%q]", rc.Addr(), k.key)
}

// instanceKeys returns the instances a block expands to, when this pass can
// read them from configuration alone. It reuses the same two readers
// [stamper.chunkCount] already uses, so a block whose chunk count is known
// is a block whose instances are known.
func (s *stamper) instanceKeys(ctx context.Context, rc *configs.Resource) ([]instanceKey, bool) {
	switch {
	case rc.Count != nil:
		n, ok := s.staticCount(ctx, rc)
		if !ok || n < 0 {
			return nil, false
		}
		out := make([]instanceKey, 0, n)
		for i := 0; i < n; i++ {
			out = append(out, instanceKey{index: i, count: true})
		}
		return out, true
	case rc.ForEach != nil:
		return s.forEachInstanceKeys(ctx, rc)
	}
	return nil, false
}

// forEachInstanceKeys reads a for_each block's instances, binding each.value
// to what the expression actually produced rather than to the key.
//
// A set of strings has value == key by definition, and that is the shape a
// marker expression would plausibly reach for. A map does not, and pretending
// otherwise is what made a wrong marker verify (see [instanceKey.vars]). An
// element this pass cannot represent leaves value at cty.NilVal, which makes
// any expression using each.value decline the comparison.
func (s *stamper) forEachInstanceKeys(ctx context.Context, rc *configs.Resource) ([]instanceKey, bool) {
	keys, ok := s.staticForEachKeys(ctx, rc)
	if !ok {
		return nil, false
	}

	values := map[string]cty.Value{}
	if val, ok := s.evalStatic(ctx, rc.ForEach, "for_each"); ok && val != cty.NilVal && !val.IsNull() && val.IsWhollyKnown() && !val.IsMarked() {
		ty := val.Type()
		switch {
		case ty.IsMapType(), ty.IsObjectType():
			for it := val.ElementIterator(); it.Next(); {
				k, v := it.Element()
				if k.Type() == cty.String && !k.IsNull() {
					values[k.AsString()] = v
				}
			}
		case ty.IsSetType(), ty.IsListType(), ty.IsTupleType():
			for it := val.ElementIterator(); it.Next(); {
				_, v := it.Element()
				if v.Type() == cty.String && !v.IsNull() {
					values[v.AsString()] = v
				}
			}
		}
	}

	out := make([]instanceKey, 0, len(keys))
	for _, k := range keys {
		out = append(out, instanceKey{key: k, value: values[k]})
	}
	return out, true
}

// repetitionContext builds the evaluation context the two expressions share:
// everything the module's static evaluator can answer, with the repetition
// references left out so they can be supplied per instance by the caller.
//
// It mirrors internal/live/identity's evalPure, including the recover: the
// static scope panics rather than erroring on a reference it has no
// repetition data for, and this pass degrades to "cannot check" rather than
// taking the run down.
func (s *stamper) repetitionContext(ctx context.Context, rc *configs.Resource, exprs ...hclsyntax.Expression) (out *hcl.EvalContext, ok bool) {
	defer func() {
		if recover() != nil {
			out, ok = nil, false
		}
	}()

	if s.mod == nil || s.mod.StaticEvaluator == nil {
		return nil, false
	}

	var travs []hcl.Traversal
	for _, expr := range exprs {
		if expr == nil {
			continue
		}
		for _, trav := range expr.Variables() {
			switch trav.RootName() {
			case "each", "count":
				// Supplied per instance by the caller. The static scope
				// panics on these.
				continue
			}
			travs = append(travs, trav)
		}
	}

	refs, refDiags := lang.References(addrs.ParseRef, travs)
	if refDiags.HasErrors() {
		return nil, false
	}

	hclCtx, diags := s.mod.StaticEvaluator.EvalContext(ctx, configs.StaticIdentifier{
		Module:    s.modInst.Module(),
		Subject:   fmt.Sprintf("%s.tags", rc.Addr()),
		DeclRange: rc.DeclRange,
	}, refs)
	if diags.HasErrors() {
		return nil, false
	}
	if hclCtx == nil {
		hclCtx = &hcl.EvalContext{}
	}
	return hclCtx, true
}

// evalString evaluates one expression to the string it would contribute to
// a tags map, reporting not-ok for anything that has no such string:
// unknown, null, marked, or a type that does not convert.
//
// The conversion is not incidental. `"${count.index}"` is a template wrapping
// a single interpolation, so HCL hands back the number unconverted - and
// requiring cty.String here made that one expression, of the four #115 lists
// as wrong, decline the comparison and keep its old warning. It reaches the
// tags map as "0" and would be written to the cloud as "0", so "0" is the
// value to compare.
func evalString(expr hclsyntax.Expression, scope *hcl.EvalContext) (s string, ok bool) {
	defer func() {
		if recover() != nil {
			s, ok = "", false
		}
	}()

	if expr == nil {
		return "", false
	}
	val, diags := expr.Value(scope)
	if diags.HasErrors() || val.IsNull() || !val.IsWhollyKnown() || val.IsMarked() {
		return "", false
	}
	str, err := convert.Convert(val, cty.String)
	if err != nil || str.IsNull() {
		return "", false
	}
	return str.AsString(), true
}
