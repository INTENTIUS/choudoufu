// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
)

// ZeroInstanceBlock is one resource block that produces no instances at
// all this run, together with the provider configuration it would have
// been managed by.
type ZeroInstanceBlock struct {
	// Addr is the block's address at one module INSTANCE, so a for_each'd
	// module whose call keys differ in whether the block expands - the same
	// block address under module.x["a"] and module.x["b"] - is answered per
	// instance rather than once for the block.
	Addr addrs.AbsResource

	// Provider is the provider configuration the block declares, resolved
	// against the module instance Addr sits in.
	Provider addrs.AbsProviderConfig
}

// ZeroInstanceBlocks reports every resource block - managed and data, at
// every module instance, in every module - whose `count`, `for_each` or
// `lifecycle.enabled` PROVABLY resolves to zero instances from
// configuration and variables alone, with nothing read from the cloud and
// no state consulted.
//
// It exists for GitHub issue #349. The evaluation graph tofu.Context.Eval
// builds (internal/tofu/graph_builder_eval.go, the same one `tofu console`
// uses) answers a reference into a resource block that is absent from the
// state it was handed with cty.DynamicVal - an unknown - whether that
// block has zero instances or merely has not been materialized yet.
// internal/tofu/evaluate.go's GetResource says so directly: on the plan and
// apply walks a count-gated resource missing from state answers
// cty.EmptyTupleVal, because those walks have already expanded the
// configuration and know the difference, while every other walk falls
// through to a DynamicVal that deliberately does not claim to know. The
// consequence for choudoufu is that `try(aws_lambda_layer_version.this[0].arn,
// "")` cannot reach its own stated alternative during output evaluation:
// try() recovers from an evaluation ERROR, and an unknown is not one, so an
// output a real plan resolves to "" is left unresolved instead.
//
// Answering the question the eval graph declines to answer is what this
// function is for, and it answers it off the SAME expansion signal identity
// resolution itself trusts - [resolver.expansionFor], the memoized,
// cycle-guarded evaluation every count and for_each in this package already
// goes through - so the two layers can never disagree about which blocks
// have instances. Nothing here evaluates a count for itself.
//
// # "Provably" is strict here, and deliberately stricter than lint's
//
// internal/live/lint's blockHasNoInstances reads the same question off
// [ConfigSignal] and treats an ABSENCE as zero instances, which is the
// right, conservative reading for an admission verdict: a block whose
// expansion could not be enumerated has no instance whose type needs
// admitting either way. This function cannot borrow that reading. Its
// answer becomes an empty collection in a state a plan then diffs against,
// so "could not enumerate" read as "zero" would hand the plan a concrete
// prior value for an output whose real prior value is unknown - a wrong
// answer that renders as a clean diff, which is the exact shape HANDOFF.md's
// "a wrong marker outranks a missing one" is about. So only an expansion
// that RESOLVED, and resolved to no keys, is reported; an expansion that
// failed to resolve contributes nothing, and the caller keeps the unknown
// it already had.
//
// Diagnostics are dropped rather than returned, for the same reason
// [ScanConfig]'s are advisory: a count this pass cannot evaluate is not a
// new refusal, it is one more block this function has nothing to say about,
// and identity resolution proper raises whatever is genuinely wrong with it.
func ZeroInstanceBlocks(ctx context.Context, cfg *configs.Config) []ZeroInstanceBlock {
	if cfg == nil || cfg.Module == nil || cfg.Module.StaticEvaluator == nil {
		return nil
	}
	r := newResolver(ctx, cfg, Context{})
	var out []ZeroInstanceBlock
	r.collectZeroInstancesInto(cfg, addrs.RootModuleInstance, &out)
	return out
}

// collectZeroInstancesInto is [ZeroInstanceBlocks]'s recursive step. Its
// walk mirrors [resolver.collectSignalInto] exactly, including the
// re-entering of the module before every sibling child - see that
// function's own doc for why both are load-bearing rather than tidy - and
// differs from it in only two ways: data resources are visited as well as
// managed ones, since a zero-instance `data` block is half of what #349 is
// about, and a block is recorded when its expansion succeeds with no keys
// rather than when it succeeds with some.
func (r *resolver) collectZeroInstancesInto(cfg *configs.Config, modInst addrs.ModuleInstance, out *[]ZeroInstanceBlock) {
	r.enterModuleAt(cfg, modInst)
	for _, rc := range sortedResources(cfg.Module.ManagedResources) {
		r.recordIfZeroInstances(rc, modInst, out)
	}
	r.enterModuleAt(cfg, modInst)
	for _, rc := range sortedResources(cfg.Module.DataResources) {
		r.recordIfZeroInstances(rc, modInst, out)
	}
	for _, name := range SortedChildNames(cfg.Children) {
		r.enterModuleAt(cfg, modInst)
		child := cfg.Children[name]
		keys, diag := ChildCallKeys(r.ctx, r.curCfg, name)
		if diag != nil {
			// A module call whose own count or for_each this pass cannot
			// enumerate contributes nothing, exactly as in
			// [resolver.collectSignalInto]: its resources' expansions are
			// not knowable either, and "not knowable" is not "zero".
			continue
		}
		for _, key := range keys {
			r.collectZeroInstancesInto(child, modInst.Child(name, key), out)
		}
	}
}

func (r *resolver) recordIfZeroInstances(rc *configs.Resource, modInst addrs.ModuleInstance, out *[]ZeroInstanceBlock) {
	// A block with no count, no for_each and no lifecycle.enabled always
	// has exactly one instance, so it can never be reported here. Skipping
	// it up front keeps this walk from evaluating anything for the common
	// case, and keeps a resolver diagnostic from being produced for a block
	// that could not possibly answer the question.
	if rc.Count == nil && rc.ForEach == nil && rc.Enabled == nil {
		return
	}
	exp, ok := r.expansionFor(rc)
	if !ok || len(exp.keys) > 0 {
		return
	}
	*out = append(*out, ZeroInstanceBlock{
		Addr: rc.Addr().Absolute(modInst),
		Provider: addrs.AbsProviderConfig{
			Module:   modInst.Module(),
			Provider: rc.Provider,
			Alias:    rc.ProviderConfigAddr().Alias,
		},
	})
}
