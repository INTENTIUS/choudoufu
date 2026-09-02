// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package tofu

import (
	"context"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// ResourceIdentityResolver lets something outside this package (a fork, a
// plugin, a future upstream event-driven runtime) supply the identity of a
// resource instance that this graph walk has no prior state for, so that
// NodePlannableResourceInstance can import it instead of planning a create.
//
// This is the seam ruled in the foundation-order ruling (#388)
// (ruling 3, "the plan-node seam"): identity resolution moves from a
// separate static evaluation pass over configuration text into one hook at
// the node that already evaluates real values. Nil by default; a nil
// resolver leaves NodePlannableResourceInstance.managedResourceExecute's
// behavior exactly as it is today (plan a create, or whatever validation
// would otherwise do).
//
// Deliberately, nothing in this signature names a graph-node type, an
// EvalContext, or any other internal/tofu internal: upstream's proposed
// event-model runtime
// (https://github.com/opentofu/opentofu/blob/main/rfc/20251001-eval-plan-apply-architecture.md,
// opentofu/opentofu#3414)
// evaluates "a specific resource instance is desired" as an event rather
// than a graph-node method, and the same resolver implementation must be
// callable from there with no adaptation if this fork ever rebases onto it.
// For the same reason this package must never import the fork's live-mode
// package: the dependency runs the other way, that package implements this
// interface and hands this package an instance of it.
//
// The three inputs are exactly what NodeAbstractResourceInstance.plan
// evaluates before it calls the provider: the resource instance's address,
// its evaluated configuration value, and the resource's schema. See
// managedResourceExecute in node_resource_plan_instance.go for where the
// configuration value is evaluated for this call: n.plan (in
// node_resource_abstract_instance.go) evaluates it once already, into a
// local called origConfigVal, by calling evalCtx.EvaluateBlock(ctx,
// config.Config, schema.Block, nil, keyData) after computing keyData from
// n.Config.ForEach and the instance's resource key. managedResourceExecute
// runs before n.plan and does not have keyData or origConfigVal in scope
// yet, so resolving identity at that point means repeating that same
// evaluation call early (see evaluateConfigForIdentity below); the two
// evaluations are redundant only in the sense that both run the same
// deterministic HCL evaluation over the same inputs, so there is no
// observable difference, and n.plan's own evaluation is unaffected.
type ResourceIdentityResolver interface {
	// ResolveResourceIdentity is asked once per resource instance, only
	// when that instance has no prior state and nothing else (an `import`
	// block) already targets it. found is false when the resolver has
	// nothing to say about this instance, in which case
	// managedResourceExecute proceeds exactly as it does today.
	ResolveResourceIdentity(ctx context.Context, addr addrs.AbsResourceInstance, config cty.Value, schema providers.Schema) (target providers.ImportTarget, found bool, diags tfdiags.Diagnostics)
}

// ConfigValueAdjuster lets something outside this package rewrite a
// resource instance's evaluated configuration value before it is planned,
// so that a schema-generic surface (markers, in the live fork) can be
// stamped onto the value the provider will actually see.
//
// It must be applied to the evaluated configuration value BEFORE
// ignore_changes processing, and only ever to that value, never to
// anything PlanResourceChange returns. That ordering is not a local
// choice: opentofu/opentofu#3016 (apparentlymart, 2025-07-15) is a
// provider-protocol invariant requiring OpenTofu to send the provider
// exactly the planned new state the provider itself returned, because
// providers encode cross-argument API constraints in their own planning
// logic. A post-plan mutation would violate that unconditionally, for any
// provider, which is why this hook exists on the configuration value
// instead. See NodeAbstractResourceInstance.plan in
// node_resource_abstract_instance.go: it evaluates the instance's
// configuration into a local called origConfigVal via
// evalCtx.EvaluateBlock, and that is the only place, and the only value,
// this adjuster is given - applied immediately after that evaluation and
// before n.processIgnoreChanges runs over the same value.
type ConfigValueAdjuster interface {
	// AdjustConfigValue returns the configuration value to plan with. A
	// nil adjuster (the default) leaves the evaluated configuration value
	// untouched.
	AdjustConfigValue(ctx context.Context, addr addrs.AbsResourceInstance, config cty.Value, schema providers.Schema) (cty.Value, tfdiags.Diagnostics)
}

// IgnoreChangesAdjuster is an optional capability a [ConfigValueAdjuster]
// may additionally implement (checked with a type assertion on the same
// value [EvalContext.ConfigValueAdjuster] returns - see this package's
// only caller, in node_resource_abstract_instance.go), to add extra
// ignore_changes paths for one resource instance's plan on top of
// whatever the configuration's own lifecycle block already lists.
//
// GitHub issue #451: it exists because [ConfigValueAdjuster] genuinely
// cannot do this itself. That interface's whole contract - by design, see
// its own doc comment - is (ctx, addr, evaluated config value, schema)
// with no prior state to compare a value against and no way to reach
// configs.Resource.Managed.IgnoreChanges from the cty.Value it returns.
// Some effects a fork's config-synthesis pass could once achieve by
// rewriting `lifecycle { ignore_changes = [...] }` into the HCL body
// before evaluation (internal/live/stamp's #380 fix, in this fork) need
// a real ignore_changes entry to reproduce at the node - "leave whatever
// is already there untouched, regardless of what this run's own logic
// would otherwise compute" cannot be expressed by returning a value,
// because a value is exactly one answer and ignore_changes is a standing
// instruction to prefer the PRIOR one, forever, until the entry is
// removed. This is that seam's second, narrower hook: same interface-shape
// discipline as [ResourceIdentityResolver] and [ConfigValueAdjuster] -
// nothing here names a graph-node type, an EvalContext, or any other
// internal/tofu internal either - added as its own optional interface
// rather than a second return value on AdjustConfigValue so that
// [ConfigValueAdjuster]'s already-proven contract stays exactly as it was.
type IgnoreChangesAdjuster interface {
	// AdjustIgnoreChanges returns extra paths to treat as ignored for this
	// resource instance's plan, unioned with whatever
	// configs.Resource.Managed.IgnoreChanges already lists. A nil or empty
	// result adds nothing.
	AdjustIgnoreChanges(ctx context.Context, addr addrs.AbsResourceInstance, schema providers.Schema) []cty.Path
}
