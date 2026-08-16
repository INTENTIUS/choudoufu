// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"fmt"
	"sort"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"
	"github.com/zclconf/go-cty/cty/gocty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/instances"
)

// ModuleInstance is the addrs.ModuleInstance for a node in the static module
// tree: cfg.Path with every step unkeyed.
//
// This is the right (and lossless) reading for a module call this package
// has not been told an instance key for: a static call has exactly one
// instance and [addrs.Module.UnkeyedInstanceShim] names it exactly. A
// for_each-expanded call (59c, keyed for_each on module blocks) has more
// than one, and a caller walking into a specific one builds its own
// addrs.ModuleInstance with [addrs.ModuleInstance.Child] rather than calling
// this - see [ChildModuleKeys] and resolve.go's walkModule, which is the
// only place in this package that does.
func ModuleInstance(cfg *configs.Config) addrs.ModuleInstance {
	return cfg.Path.UnkeyedInstanceShim()
}

// ChildModuleKeys evaluates a module call's for_each expression and returns
// the instance keys it expands to, sorted, or a diagnostic explaining why it
// could not.
//
// mod is the module the call is written in (the parent, not the child):
// exactly like a resource's own for_each, a module call's for_each is
// evaluated through the static evaluator of the module the call block
// itself lives in, not the module the call points at - the child's own
// variables (and anything in the child that depends on them) are a
// different, and much harder, question this function does not answer. See
// the package doc's note on why 59c never evaluates var.* inside a keyed
// module's own resources.
//
// The evaluable scope mirrors [live/lint.staticForEachKeys] and a
// resource's own for_each (resolve.go's forEachExpansion) exactly: a
// literal collection, or one built from variables, locals, path and
// terraform values. Anything else - a reference to another resource, to
// each/count of an enclosing scope, to a module or data source - is refused
// as non-static, with the same reasoning [resolver.forEachExpansion] gives
// for a resource: instance keys become part of an address, and an address
// has to be knowable before anything is read from the cloud.
//
// A nil expr (no for_each: a static module call) reports the single
// unkeyed instance every static call has always had.
func ChildModuleKeys(ctx context.Context, mod *configs.Module, subject string, expr hcl.Expression) ([]addrs.InstanceKey, *hcl.Diagnostic) {
	if expr == nil {
		return []addrs.InstanceKey{addrs.NoKey}, nil
	}
	elems, diag := childModuleForEachElements(ctx, mod, subject, expr)
	if diag != nil {
		// The whole expression did not evaluate, but a for_each only
		// needs its KEYS to be static - the paired values are read
		// back through each.value, from inside an instance that
		// already exists, and [ChildModuleRepetitionData] still
		// refuses those separately. See [staticForEachKeyNames].
		if names, ok := staticForEachKeyNames(ctx, mod, subject, expr); ok {
			keys := make([]addrs.InstanceKey, 0, len(names))
			for _, name := range names {
				keys = append(keys, addrs.StringKey(name))
			}
			return keys, nil
		}
		return nil, diag
	}

	names := make([]string, 0, len(elems))
	for name := range elems {
		names = append(names, name)
	}
	sort.Strings(names)
	keys := make([]addrs.InstanceKey, 0, len(names))
	for _, name := range names {
		keys = append(keys, addrs.StringKey(name))
	}
	return keys, nil
}

// childModuleForEachElements evaluates a module call's for_each expression
// fully - exactly the evaluation [ChildModuleKeys] itself performs, and
// bound by the same evaluable-scope restriction (var, local, path,
// terraform, tofu only) - and returns every element it produces, keyed by
// its string key: for a map or an object, the key's own paired value; for a
// set of strings, the string itself (each.value equals each.key there, the
// same reading [resolver.forEachExpansion] gives a resource's own for_each
// over a set).
//
// It exists so that a module call's each.value - not only its keys - can be
// recovered without a second, potentially divergent evaluation: this is the
// one place both [ChildModuleKeys] and [ChildModuleRepetitionData] evaluate
// a module call's for_each, so the two can never disagree about what one
// key's value is.
//
// When it refuses, both callers fall back to [staticForEachKeyNames] -
// again the same one function, so they cannot disagree about which
// instances exist either. That fallback proves keys and never values, so
// nothing it admits can put a value here that this function did not
// produce.
func childModuleForEachElements(ctx context.Context, mod *configs.Module, subject string, expr hcl.Expression) (map[string]cty.Value, *hcl.Diagnostic) {
	if mod == nil || mod.StaticEvaluator == nil {
		return nil, staticEvalDiag(expr.Range(), subject, "no static evaluator is available to evaluate it")
	}

	for _, trav := range expr.Variables() {
		switch trav.RootName() {
		case "var", "local", "path", "terraform", "tofu":
			// Evaluable in a static scope.
		default:
			return nil, staticEvalDiag(expr.Range(), subject, fmt.Sprintf("it references %q, which is not knowable from configuration alone", trav.RootName()))
		}
	}

	ident := configs.StaticIdentifier{Module: addrs.RootModule, Subject: subject, DeclRange: expr.Range()}
	val, hclDiags := mod.StaticEvaluator.Pure().Evaluate(ctx, expr, ident)
	if hclDiags.HasErrors() {
		return nil, staticEvalDiag(expr.Range(), subject, hclDiags.Error())
	}
	if !val.IsWhollyKnown() || val.IsNull() {
		return nil, &hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "Non-static for_each expression",
			Detail: fmt.Sprintf(
				"The for_each value for %s cannot be determined from configuration alone. Instance keys become part of every address inside the module, and those addresses are what a tofu-address marker records, so they must be knowable before anything is read from the cloud.",
				subject),
			Subject: expr.Range().Ptr(),
		}
	}
	if val.IsMarked() {
		return nil, &hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "Sensitive for_each expression",
			Detail:   fmt.Sprintf("The for_each value for %s is sensitive, so it cannot become part of the addresses inside the module.", subject),
			Subject:  expr.Range().Ptr(),
		}
	}

	ty := val.Type()
	elems := make(map[string]cty.Value)
	switch {
	case ty.IsMapType(), ty.IsObjectType():
		for it := val.ElementIterator(); it.Next(); {
			k, v := it.Element()
			if k.Type() != cty.String || k.IsNull() {
				return nil, invalidForEachDiag(expr.Range(), subject, ty)
			}
			elems[k.AsString()] = v
		}
	case ty.IsSetType():
		if ty.ElementType() != cty.String {
			return nil, invalidForEachDiag(expr.Range(), subject, ty)
		}
		for it := val.ElementIterator(); it.Next(); {
			_, v := it.Element()
			if v.Type() != cty.String || v.IsNull() {
				return nil, invalidForEachDiag(expr.Range(), subject, ty)
			}
			elems[v.AsString()] = v
		}
	default:
		return nil, invalidForEachDiag(expr.Range(), subject, ty)
	}

	return elems, nil
}

// ChildModuleRepetitionData evaluates a module call's own count or for_each
// expression - exactly as [ChildModuleCountKeys] and [ChildModuleKeys]
// themselves do - and returns the each.key/each.value or count.index data
// that ONE specific instance of that call sees: the same data
// [resolver.walkModule] already used, via those two functions, to decide
// that this instance exists in the first place.
//
// It exists for localvalue.go's namedDef: a var.* reference resolved at a
// module boundary needs the CALL's own repetition data (the scope the
// call's argument expression is evaluated in - see [ChildModuleKeys]'s doc)
// before it can hand that argument expression to
// [configs.StaticEvaluator.WithRepetitionData], the same seam a resource's
// own for_each/count already threads through [expansion.scope].
//
// key must be one of the keys the same count or for_each expression would
// itself produce for this call - ordinarily the calling instance's own
// [addrs.ModuleInstance.CallInstance] key. ok is false whenever that cannot
// be confirmed by re-deriving it from the expression: exactly one of
// countExpr/forEachExpr must be non-nil and evaluate successfully, and key
// must actually be a member of what it produces. There is no path here that
// returns data for a key it has not itself just verified - a caller must
// never substitute a guess for a false ok.
func ChildModuleRepetitionData(ctx context.Context, mod *configs.Module, subject string, countExpr, forEachExpr hcl.Expression, key addrs.InstanceKey) (instances.RepetitionData, bool) {
	switch {
	case countExpr != nil:
		idx, ok := key.(addrs.IntKey)
		if !ok {
			return instances.RepetitionData{}, false
		}
		keys, diag := ChildModuleCountKeys(ctx, mod, subject, countExpr)
		if diag != nil {
			return instances.RepetitionData{}, false
		}
		found := false
		for _, k := range keys {
			if k == idx {
				found = true
				break
			}
		}
		if !found {
			return instances.RepetitionData{}, false
		}
		return instances.RepetitionData{CountIndex: cty.NumberIntVal(int64(idx))}, true

	case forEachExpr != nil:
		strKey, ok := key.(addrs.StringKey)
		if !ok {
			return instances.RepetitionData{}, false
		}
		elems, diag := childModuleForEachElements(ctx, mod, subject, forEachExpr)
		if diag != nil {
			// The whole expression did not evaluate, but its KEY set
			// may still be proven - the same widening [ChildModuleKeys]
			// applies, so the two cannot disagree about which instances
			// exist. EachValue stays cty.NilVal, and
			// [configs.StaticEvaluator.repetitionAttr] answers
			// each.value only for a non-nil one, so a reference to the
			// value this pass could not read refuses where it is
			// written instead of being guessed at here.
			names, keysOK := staticForEachKeyNames(ctx, mod, subject, forEachExpr)
			if !keysOK {
				return instances.RepetitionData{}, false
			}
			for _, name := range names {
				if name == string(strKey) {
					return instances.RepetitionData{EachKey: cty.StringVal(name)}, true
				}
			}
			return instances.RepetitionData{}, false
		}
		val, ok := elems[string(strKey)]
		if !ok {
			return instances.RepetitionData{}, false
		}
		return instances.RepetitionData{EachKey: cty.StringVal(string(strKey)), EachValue: val}, true

	default:
		return instances.RepetitionData{}, false
	}
}

// ChildModuleCountKeys evaluates a module call's count expression and
// returns the instance keys it expands to - addrs.IntKey(0)..IntKey(n-1) -
// or a diagnostic explaining why it could not. It is [ChildModuleKeys]'s
// count counterpart, and exists because [resolver.walkModule] previously
// read only a module call's for_each and treated a count-gated call as
// always having exactly one unkeyed instance, whatever count actually
// evaluated to - silently walking a disabled (count = 0) module's
// resources as if they existed, and addressing a single-instance one
// (count = 1) as the call itself rather than as [0], the same mismatch a
// resource's own count/for_each split has always kept apart.
//
// The evaluable scope, and the reasoning for refusing anything wider, are
// identical to [ChildModuleKeys]: var, local, path, terraform and tofu
// only, because instance keys become part of every address inside the
// module and have to be knowable before anything is read from the cloud.
//
// A nil expr reports the single unkeyed instance every static call has
// always had, exactly as [ChildModuleKeys] does for a nil for_each -
// callers try count first and fall back to for_each, mirroring
// configs.ModuleCall's own mutual exclusivity of the two.
func ChildModuleCountKeys(ctx context.Context, mod *configs.Module, subject string, expr hcl.Expression) ([]addrs.InstanceKey, *hcl.Diagnostic) {
	if expr == nil {
		return []addrs.InstanceKey{addrs.NoKey}, nil
	}
	if mod == nil || mod.StaticEvaluator == nil {
		return nil, staticEvalCountDiag(expr.Range(), subject, "no static evaluator is available to evaluate it")
	}

	for _, trav := range expr.Variables() {
		switch trav.RootName() {
		case "var", "local", "path", "terraform", "tofu":
			// Evaluable in a static scope.
		default:
			return nil, staticEvalCountDiag(expr.Range(), subject, fmt.Sprintf("it references %q, which is not knowable from configuration alone", trav.RootName()))
		}
	}

	ident := configs.StaticIdentifier{Module: addrs.RootModule, Subject: subject, DeclRange: expr.Range()}
	val, hclDiags := mod.StaticEvaluator.Pure().Evaluate(ctx, expr, ident)
	if hclDiags.HasErrors() {
		return nil, staticEvalCountDiag(expr.Range(), subject, hclDiags.Error())
	}
	if val.IsMarked() {
		return nil, &hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "Sensitive count expression",
			Detail:   fmt.Sprintf("The count for %s is sensitive or ephemeral, so the instance keys it produces cannot become part of addresses inside the module.", subject),
			Subject:  expr.Range().Ptr(),
		}
	}
	if !val.IsKnown() || val.IsNull() {
		return nil, &hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "Non-static count expression",
			Detail: fmt.Sprintf(
				"The count for %s evaluated to null, or to a value not knowable from configuration alone. Instance keys become part of every address inside the module, and those addresses are what a tofu-address marker records, so they must be knowable before anything is read from the cloud.",
				subject),
			Subject: expr.Range().Ptr(),
		}
	}
	num, err := convert.Convert(val, cty.Number)
	if err != nil {
		return nil, invalidCountDiag(expr.Range(), subject, fmt.Sprintf("is not a number: %s", err))
	}
	var n int
	if err := gocty.FromCtyValue(num, &n); err != nil {
		return nil, invalidCountDiag(expr.Range(), subject, fmt.Sprintf("is not a whole number: %s", err))
	}
	if n < 0 {
		return nil, invalidCountDiag(expr.Range(), subject, "is negative")
	}

	keys := make([]addrs.InstanceKey, 0, n)
	for i := 0; i < n; i++ {
		keys = append(keys, addrs.IntKey(i))
	}
	return keys, nil
}

func staticEvalCountDiag(rng hcl.Range, subject, why string) *hcl.Diagnostic {
	return &hcl.Diagnostic{
		Severity: hcl.DiagError,
		Summary:  "Non-static count expression",
		Detail:   fmt.Sprintf("The count for %s cannot be determined from configuration alone: %s.", subject, why),
		Subject:  rng.Ptr(),
	}
}

func invalidCountDiag(rng hcl.Range, subject, why string) *hcl.Diagnostic {
	return &hcl.Diagnostic{
		Severity: hcl.DiagError,
		Summary:  "Invalid count",
		Detail:   fmt.Sprintf("The count for %s %s.", subject, why),
		Subject:  rng.Ptr(),
	}
}

func staticEvalDiag(rng hcl.Range, subject, why string) *hcl.Diagnostic {
	return &hcl.Diagnostic{
		Severity: hcl.DiagError,
		Summary:  "Non-static for_each expression",
		Detail:   fmt.Sprintf("The for_each value for %s cannot be determined from configuration alone: %s.", subject, why),
		Subject:  rng.Ptr(),
	}
}

func invalidForEachDiag(rng hcl.Range, subject string, ty cty.Type) *hcl.Diagnostic {
	return &hcl.Diagnostic{
		Severity: hcl.DiagError,
		Summary:  "Invalid for_each value",
		Detail:   fmt.Sprintf("The for_each value for %s is %s. for_each on a module block accepts a map, an object, or a set of strings.", subject, ty.FriendlyName()),
		Subject:  rng.Ptr(),
	}
}

// ConfigForModule looks up the *configs.Config for a module instance within
// a configuration tree, descending root.Children by each step's name in
// turn. It reports false if any step names a module the tree does not
// have - the address belongs to no module in this configuration.
//
// Instance keys in modInst are ignored on the way down, for the same reason
// [ModuleInstance] never sets one: the static tree this package walks has
// exactly one instance per module call.
func ConfigForModule(root *configs.Config, modInst addrs.ModuleInstance) (*configs.Config, bool) {
	cur := root
	for _, step := range modInst {
		if cur == nil {
			return nil, false
		}
		child, ok := cur.Children[step.Name]
		if !ok {
			return nil, false
		}
		cur = child
	}
	return cur, true
}

// SortedChildNames returns a config's child module call names, sorted, so
// that a recursive walk visits them in a deterministic order.
func SortedChildNames(children map[string]*configs.Config) []string {
	out := make([]string, 0, len(children))
	for name := range children {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
