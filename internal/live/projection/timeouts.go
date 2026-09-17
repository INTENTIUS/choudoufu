// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hcldec"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/lang"
	"github.com/intentius/choudoufu/internal/providers"
)

// GitHub issue #1185: a `timeouts` block is honoured on a create and an
// update and dropped on a destroy, so an operator who writes
// `timeouts { delete = "20s" }` waits the provider's own default instead.
//
// # Where a destroy's timeout actually comes from
//
// Not from the `timeouts` attribute in state, which is the reading that
// linked this to #1190 and was wrong. An SDKv2 resource's Delete reads its
// deadline out of the legacy diff META, and the meta reaches it as the
// PRIVATE blob:
//
//   - [tofu.NodeAbstractResourceInstance.planDestroy] calls
//     PlanResourceChange with PriorPrivate set to the prior object's
//     Private, and helper/schema returns early for a null proposed state
//     with PlannedPrivate = PriorPrivate, unread and unchanged.
//   - ApplyResourceChange then decodes that same blob into
//     InstanceDiff.Meta ([schema.ResourceTimeout.DiffDecode]), and
//     Resource.Apply takes the delete deadline from it.
//
// A non-destroy plan never needs the carrier: simpleDiff runs
// [schema.ResourceTimeout.ConfigDecode] against the configuration on every
// plan and DiffEncodes the result, which is why create and update were
// never affected.
//
// So the configured value reaches a stock destroy only because a stock
// state file holds the private blob the last apply wrote. choudoufu has no
// such state: every plan re-derives the prior object through
// ImportResourceState and ReadResource, and the private that comes back is
// the one [schema.Resource.Data] seeds an imported instance with - the
// RESOURCE'S OWN DECLARED DEFAULTS, [schema.DefaultTimeout] values fixed in
// the provider's Go code, with nothing of the operator's configuration in
// them.
//
// Measured on a kind cluster with hashicorp/kubernetes 3.2.1, a
// kubernetes_namespace declaring `timeouts { delete = "20s" }` and a
// finalizer-held ConfigMap inside it so the delete blocks:
//
//	leg                  private delete (ns)   apply -destroy
//	stock, state-backed         20000000000              20s
//	live, before this          300000000000             311s
//
// 300000000000ns is the provider's own schema.DefaultTimeout(5*time.Minute)
// for that type's delete. The wall clock is the timeout: both legs end in
// "context deadline exceeded" because the finalizer never clears.
//
// # What this does about it
//
// Re-derives the meta from configuration, which is what the live path does
// with everything else a state file would have carried (HANDOFF.md's
// foundation order, item 1). [configuredTimeouts] decodes the resource's
// own `timeouts` block with the same static evaluator
// [configuredAttrsSeed] uses, and [withConfiguredTimeouts] merges the
// result into the private blob the read produced, overwriting only the
// keys the configuration names - exactly the merge
// [schema.ResourceTimeout.ConfigDecode] performs on the plan path, which
// starts from the declared defaults and overwrites per configured key.
//
// This is a meta write, not a value write: obj.Value is untouched, so
// nothing here can move a plan's diff, change an identity or affect
// ownership. What it changes is how long the provider waits.
//
// # What it deliberately does not reach
//
//   - A resource whose private carries no SDKv2 timeout key at all. That is
//     the gate in [withConfiguredTimeouts], and it is what keeps this away
//     from terraform-plugin-framework resources, whose private state is a
//     map[string][]byte the framework unmarshals strictly and whose
//     `timeouts` block is read from the state OBJECT rather than the meta.
//     A framework resource's destroy timeout is a separate defect with a
//     separate carrier; it is not addressed here.
//   - A deposed object ([builder.materializeDeposed]), which reads with no
//     configuration in hand at all.
//   - An undeclared instance - the block is gone, so there is no configured
//     `timeouts` left to re-derive and the provider's default is the only
//     answer available.
const sdkv2TimeoutMetaKey = "e2bfb730-ecaa-11e6-8f88-34363bc7c4c0"

// timeoutsBlockName is the block both terraform-plugin-sdk and
// terraform-plugin-framework render a resource's timeouts under. Only the
// SDKv2 spelling is acted on, and the private-blob gate in
// [withConfiguredTimeouts] - not this name - is what tells the two apart.
const timeoutsBlockName = "timeouts"

// configuredTimeouts is the resource's own `timeouts` block, decoded to
// nanosecond durations keyed the way helper/schema keys its meta
// ("create", "read", "update", "delete", "default").
//
// nil whenever the answer is not statically certain: no evaluator, no
// configuration, no timeouts block in the provider's schema, an expression
// that does not resolve, or a duration string [time.ParseDuration] refuses.
// Every one of those falls back to what the provider already put in the
// private blob, which is the behaviour before this existed.
//
// The decode is its own [hcldec.PartialDecode] over just this one block,
// for [configuredAttrSeed]'s reason: [configs.StaticEvaluator.EvalContext]
// fails outright the moment any reference it was handed cannot be resolved,
// and an unresolvable sibling argument must not cost this block its answer.
func configuredTimeouts(ctx context.Context, eval *configs.StaticEvaluator, modPath addrs.Module, rc *configs.Resource, schema providers.Schema) (out map[string]int64) {
	if eval == nil || rc == nil || rc.Config == nil || schema.Block == nil {
		return nil
	}
	nested, ok := schema.Block.BlockTypes[timeoutsBlockName]
	if !ok || nested == nil || nested.Nesting != configschema.NestingSingle {
		return nil
	}

	// A provider's schema is data this package did not write, and hcldec is
	// being pointed at it; a panic in one resource's decode must not lose
	// the whole projection, the same guard [configuredAttrSeed] carries.
	defer func() {
		if rec := recover(); rec != nil {
			log.Printf("[WARN] projection: decoding %s's timeouts block panicked (%v); falling back to the provider's own default timeouts", rc.Addr(), rec)
			out = nil
		}
	}()

	only := &configschema.Block{
		BlockTypes: map[string]*configschema.NestedBlock{timeoutsBlockName: nested},
	}
	spec := only.DecoderSpec()

	refs, refDiags := lang.References(addrs.ParseRef, hcldec.Variables(rc.Config, spec))
	if refDiags.HasErrors() {
		return nil
	}
	hclCtx, ctxDiags := eval.EvalContext(ctx, configs.StaticIdentifier{
		Module:    modPath,
		Subject:   rc.Addr().String(),
		DeclRange: rc.DeclRange,
	}, refs)
	if ctxDiags.HasErrors() {
		return nil
	}
	if hclCtx == nil {
		hclCtx = &hcl.EvalContext{}
	}

	decoded, _, valDiags := hcldec.PartialDecode(rc.Config, spec, hclCtx)
	if valDiags.HasErrors() || decoded == cty.NilVal || decoded.IsNull() || !decoded.Type().HasAttribute(timeoutsBlockName) {
		return nil
	}
	block, _ := decoded.GetAttr(timeoutsBlockName).UnmarkDeep()
	if block.IsNull() || !block.IsKnown() || !block.Type().IsObjectType() {
		return nil
	}

	for name := range nested.Attributes {
		if !block.Type().HasAttribute(name) {
			continue
		}
		v := block.GetAttr(name)
		if v.IsNull() || !v.IsKnown() || v.Type() != cty.String {
			continue
		}
		d, err := time.ParseDuration(v.AsString())
		if err != nil {
			// The provider's own ConfigDecode would reject this too, with
			// its own error, on the create path. Nothing to re-derive.
			log.Printf("[WARN] projection: %s declares timeouts.%s = %q, which is not a duration; leaving the provider's own default in place", rc.Addr(), name, v.AsString())
			continue
		}
		if out == nil {
			out = make(map[string]int64, len(nested.Attributes))
		}
		out[name] = d.Nanoseconds()
	}
	return out
}

// withConfiguredTimeouts merges cfg into private's SDKv2 timeout meta and
// reports whether anything changed.
//
// The gate is the meta key's presence. helper/schema renders a `timeouts`
// block into a resource's schema only for the fields its own
// [schema.ResourceTimeout] declares non-nil, and metaEncode writes exactly
// those same fields into the instance state's meta - so for any SDKv2
// resource whose schema HAS a timeouts block the imported stub's private
// always already carries this key. A private without it is therefore not an
// SDKv2 resource missing its defaults; it is something else's private
// (terraform-plugin-framework's map[string][]byte, most of all), and this
// leaves it byte-for-byte alone rather than writing a key its owner would
// choke on.
func withConfiguredTimeouts(private []byte, cfg map[string]int64) ([]byte, bool) {
	if len(cfg) == 0 || len(private) == 0 {
		return private, false
	}
	var meta map[string]json.RawMessage
	if err := json.Unmarshal(private, &meta); err != nil {
		return private, false
	}
	raw, ok := meta[sdkv2TimeoutMetaKey]
	if !ok {
		return private, false
	}
	var times map[string]int64
	if err := json.Unmarshal(raw, &times); err != nil {
		return private, false
	}

	changed := false
	for k, v := range cfg {
		if old, had := times[k]; !had || old != v {
			changed = true
		}
		times[k] = v
	}
	if !changed {
		return private, false
	}

	encTimes, err := json.Marshal(times)
	if err != nil {
		return private, false
	}
	meta[sdkv2TimeoutMetaKey] = encTimes
	out, err := json.Marshal(meta)
	if err != nil {
		return private, false
	}
	return out, true
}
