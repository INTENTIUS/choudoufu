// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package dataread

import (
	"context"

	"github.com/hashicorp/hcl/v2"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
)

// liveModuleEvaluator builds module's own [*configs.StaticEvaluator],
// enriched with lookup's data-source coverage for THAT module, and - when
// module is not the root - with its own module-call variable expressions
// re-evaluated through its PARENT's own live evaluator (built the same way,
// recursively) instead of the frozen, coverage-less one every module's
// [configs.ModuleCall.Variables] closure was captured with when the
// [*configs.Config] tree was first built.
//
// Issue #212: internal/configs's own module tree freezes each
// module-call's variable-assignment closure once, at load time, against
// its OWNING module's [*configs.StaticEvaluator] as it existed then -
// before any caller could have attached a data lookup, because no read has
// happened yet (see [configs.StaticEvaluator.WithVariables]'s doc for the
// full mechanism). A descendant module's data source whose count/for_each/
// arguments reach a var.X reference that resolves back into an ancestor's
// OWN data source - simpleinfra's efs-filesystem/main.tf:29 is the concrete
// case the issue names - hits that frozen, coverage-less evaluator and
// refuses, even when the ancestor's data source is itself perfectly
// readable.
//
// liveModuleEvaluator rebuilds the whole ancestor chain from the root down
// on every call, attaching lookup(m) to exactly module m's own evaluator at
// each level via [configs.StaticEvaluator.WithVariables] +
// [configs.ModuleCall.VariablesUsing] - never the SAME lookup closure
// reused across two different modules, so a reference resolved through
// module A's live evaluator can only ever see module A's own data sources
// as coverage, regardless of which module first asked for this chain to be
// built. See the adversarial test in liveeval_test.go: two modules each
// declaring data.test_zone.shared must never see each other's value.
//
// No caching across calls: rebuilding a handful of ancestor levels once per
// expression is cheap relative to a network read, and caching a lookup
// closure across a probe round or a read call risks exactly the kind of
// stale, wrongly-scoped reuse this function exists to avoid.
//
// Every evaluator this returns also carries #193's length()/keys() guard
// (arity.go), applied last so it is never lost to a later .With* call in
// this function or in a caller building on top of the result. It is a
// no-op for anything other than a bare reference to an unexpanded managed
// resource's own projection, so attaching it unconditionally - root or
// descendant, analysis or read - costs nothing and cannot disagree with
// itself between modules or between [Analyze] and [Read].
func liveModuleEvaluator(ctx context.Context, cfg *configs.Config, module addrs.Module, lookup func(addrs.Module) configs.StaticDataLookup, materialize bool, recordManagedRefusal func(*hcl.Diagnostic)) *configs.StaticEvaluator {
	node := cfg.Descendent(module)
	if node == nil || node.Module == nil || node.Module.StaticEvaluator == nil {
		return nil
	}
	base := node.Module.StaticEvaluator

	if len(module) > 0 {
		parentPath := module[:len(module)-1]
		parentEval := liveModuleEvaluator(ctx, cfg, parentPath, lookup, materialize, recordManagedRefusal)
		if parentEval == nil {
			return nil
		}
		parentNode := cfg.Descendent(parentPath)
		if parentNode == nil || parentNode.Module == nil {
			return nil
		}
		callName := module[len(module)-1]
		mc := parentNode.Module.ModuleCalls[callName]
		if mc == nil {
			return nil
		}
		base = base.WithVariables(mc.VariablesUsing(ctx, parentEval))
	}

	return base.Pure().
		WithDataResults(lookup(module)).
		WithModuleOutputResults(moduleOutputLookup(ctx, cfg, module, lookup, materialize, recordManagedRefusal)).
		WithFunctionOverrides(arityGuardedFunctions())
}
