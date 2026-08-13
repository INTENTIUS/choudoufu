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

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
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
	if mod == nil || mod.StaticEvaluator == nil {
		return nil, staticEvalDiag(expr.Range(), subject, "no static evaluator is available to evaluate it")
	}

	for _, trav := range expr.Variables() {
		switch trav.RootName() {
		case "var", "local", "path", "terraform":
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
	var names []string
	switch {
	case ty.IsMapType(), ty.IsObjectType():
		for it := val.ElementIterator(); it.Next(); {
			k, _ := it.Element()
			if k.Type() != cty.String || k.IsNull() {
				return nil, invalidForEachDiag(expr.Range(), subject, ty)
			}
			names = append(names, k.AsString())
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
			names = append(names, v.AsString())
		}
	default:
		return nil, invalidForEachDiag(expr.Range(), subject, ty)
	}

	sort.Strings(names)
	keys := make([]addrs.InstanceKey, 0, len(names))
	for _, name := range names {
		keys = append(keys, addrs.StringKey(name))
	}
	return keys, nil
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
