// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"sort"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
)

// ModuleInstance is the addrs.ModuleInstance for a node in the static module
// tree: cfg.Path with every step unkeyed.
//
// Phase 1 (issue #59, 59b) admits only module blocks with no count or
// for_each, so every module instance this package ever builds has exactly
// one instance per module call and carries no instance key -
// [addrs.Module.UnkeyedInstanceShim] is therefore lossless here. A keyed
// step is phase 2's concern (59c, keyed for_each on module blocks); nothing
// in the static tree this package walks today reaches this function with a
// module call that would need one.
func ModuleInstance(cfg *configs.Config) addrs.ModuleInstance {
	return cfg.Path.UnkeyedInstanceShim()
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
