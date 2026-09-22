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
//   - A resource whose private carries no READABLE SDKv2 timeout meta:
//     the key absent, its value JSON null, or its value something other
//     than a map of durations. That is the gate in
//     [withConfiguredTimeouts], and it is what keeps this away
//     from terraform-plugin-framework resources, whose private state is a
//     map[string][]byte the framework unmarshals strictly and whose
//     `timeouts` block is read from the state OBJECT rather than the meta.
//     A framework resource's destroy timeout is the same defect reached
//     through that other carrier, GitHub issue #1240, and
//     [withConfiguredTimeoutsBlock] below is its half of the fix.
//   - A deposed object ([builder.materializeDeposed]), which reads with no
//     configuration in hand at all.
//   - An undeclared instance - the block is gone, so there is no configured
//     `timeouts` left to re-derive and the provider's default is the only
//     answer available.
const sdkv2TimeoutMetaKey = "e2bfb730-ecaa-11e6-8f88-34363bc7c4c0"

// timeoutsBlockName is the block both terraform-plugin-sdk and
// terraform-plugin-framework render a resource's timeouts under. The two
// are told apart by the private blob and by nothing else: a private
// carrying a readable SDKv2 meta ([sdkv2TimeoutMeta]) takes
// [withConfiguredTimeouts]'s meta write, and one carrying none takes
// [withConfiguredTimeoutsBlock]'s value write.
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
func configuredTimeouts(ctx context.Context, eval *configs.StaticEvaluator, modPath addrs.Module, rc *configs.Resource, schema providers.Schema) map[string]int64 {
	return timeoutsMeta(configuredTimeoutsBlock(ctx, eval, modPath, rc, schema), rc, schema)
}

// configuredTimeoutsBlock is the resource's own `timeouts` block as the
// configuration states it: an unmarked, known object of the nested block's
// own attributes, each a string, null, or (for a value the evaluator could
// not settle) unknown, which each consumer refuses on its own terms. [cty.NilVal] whenever the
// answer is not statically certain - no evaluator, no configuration, no
// NestingSingle timeouts block in the provider's schema, no block in the
// configuration, or an expression that does not resolve.
//
// The decode is its own [hcldec.PartialDecode] over just this one block,
// for [configuredAttrSeed]'s reason: [configs.StaticEvaluator.EvalContext]
// fails outright the moment any reference it was handed cannot be resolved,
// and an unresolvable sibling argument must not cost this block its answer.
//
// Both carriers read from this one decode: [timeoutsMeta] for the SDKv2
// private (#1185) and [timeoutsBlockSeed] for the framework value (#1240).
func configuredTimeoutsBlock(ctx context.Context, eval *configs.StaticEvaluator, modPath addrs.Module, rc *configs.Resource, schema providers.Schema) (out cty.Value) {
	if eval == nil || rc == nil || rc.Config == nil {
		return cty.NilVal
	}
	nested := timeoutsNestedBlock(schema.Block)
	if nested == nil {
		return cty.NilVal
	}

	// A provider's schema is data this package did not write, and hcldec is
	// being pointed at it; a panic in one resource's decode must not lose
	// the whole projection, the same guard [configuredAttrSeed] carries.
	defer func() {
		if rec := recover(); rec != nil {
			log.Printf("[WARN] projection: decoding %s's timeouts block panicked (%v); falling back to the provider's own default timeouts", rc.Addr(), rec)
			out = cty.NilVal
		}
	}()

	only := &configschema.Block{
		BlockTypes: map[string]*configschema.NestedBlock{timeoutsBlockName: nested},
	}
	spec := only.DecoderSpec()

	refs, refDiags := lang.References(addrs.ParseRef, hcldec.Variables(rc.Config, spec))
	if refDiags.HasErrors() {
		return cty.NilVal
	}
	hclCtx, ctxDiags := eval.EvalContext(ctx, configs.StaticIdentifier{
		Module:    modPath,
		Subject:   rc.Addr().String(),
		DeclRange: rc.DeclRange,
	}, refs)
	if ctxDiags.HasErrors() {
		return cty.NilVal
	}
	if hclCtx == nil {
		hclCtx = &hcl.EvalContext{}
	}

	decoded, _, valDiags := hcldec.PartialDecode(rc.Config, spec, hclCtx)
	if valDiags.HasErrors() || decoded == cty.NilVal || decoded.IsNull() || !decoded.Type().HasAttribute(timeoutsBlockName) {
		return cty.NilVal
	}
	block, _ := decoded.GetAttr(timeoutsBlockName).UnmarkDeep()
	if block.IsNull() || !block.IsKnown() || !block.Type().IsObjectType() {
		return cty.NilVal
	}
	return block
}

// timeoutsNestedBlock is the schema's timeouts block when it has the one
// shape both SDKs render - a NestingSingle block - and nil otherwise.
func timeoutsNestedBlock(block *configschema.Block) *configschema.NestedBlock {
	if block == nil {
		return nil
	}
	nested, ok := block.BlockTypes[timeoutsBlockName]
	if !ok || nested == nil || nested.Nesting != configschema.NestingSingle {
		return nil
	}
	return nested
}

// timeoutsMeta turns a decoded timeouts block into helper/schema's meta:
// one nanosecond count per attribute the configuration sets to a duration
// string. An attribute that is unset, or set to something
// [time.ParseDuration] refuses, contributes nothing, so the provider's own
// declared default for it is left in place; nil when nothing contributes.
func timeoutsMeta(block cty.Value, rc *configs.Resource, schema providers.Schema) (out map[string]int64) {
	nested := timeoutsNestedBlock(schema.Block)
	if block == cty.NilVal || nested == nil {
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
		// [configuredTimeoutsBlock] already stripped every mark; this is
		// the answer for a caller that did not, and internal/live/marksafe's
		// proof that the read below cannot panic.
		if v.IsMarked() {
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

// timeoutsBlockSeed is the decoded timeouts block as a value the prior
// state object can hold: typed exactly as the schema's nested block implies,
// so the seeded object still conforms to the schema the plan will check it
// against. [cty.NilVal] when there is nothing to seed - no block decoded,
// no attribute set, a shape other than the schema's - and, deliberately,
// when any set attribute is not a duration: terraform-plugin-framework's
// timeouts block carries a duration validator on every attribute
// (resource/timeouts/schema.go attaches validators.TimeDuration()), so a
// configuration with `delete = "not-a-duration"` never applies in stock and
// no state object ever held that string. Seeding it would be inventing a
// prior that could not exist.
func timeoutsBlockSeed(block cty.Value, schema providers.Schema) cty.Value {
	nested := timeoutsNestedBlock(schema.Block)
	if block == cty.NilVal || nested == nil {
		return cty.NilVal
	}
	want := nested.Block.ImpliedType()
	if !block.Type().Equals(want) {
		return cty.NilVal
	}
	set := 0
	for name := range nested.Attributes {
		v := block.GetAttr(name)
		if v.IsNull() {
			continue
		}
		if !v.IsKnown() || v.Type() != cty.String {
			return cty.NilVal
		}
		if v.IsMarked() {
			// Same as [timeoutsMeta]: stripped upstream, refused here.
			return cty.NilVal
		}
		if _, err := time.ParseDuration(v.AsString()); err != nil {
			return cty.NilVal
		}
		set++
	}
	if set == 0 {
		return cty.NilVal
	}
	return block
}

// sdkv2TimeoutMeta reads a private blob's SDKv2 timeout meta: the JSON
// object helper/schema's metaEncode writes, and under its own key a map of
// nanosecond durations. ok is false for every other shape, and the shapes
// are enumerated in [withConfiguredTimeouts]'s doc comment because the two
// halves of the condition below are each load-bearing and each caught a
// real defect.
func sdkv2TimeoutMeta(private []byte) (meta map[string]json.RawMessage, times map[string]int64, ok bool) {
	if len(private) == 0 {
		return nil, nil, false
	}
	// A private of literal `null` decodes to a nil meta with no error; the
	// nil map is then read (legal) and yields a nil raw message for the key,
	// which the gate below refuses. Pinned by the "null private" case, so
	// this is a stated property rather than a lucky one.
	if err := json.Unmarshal(private, &meta); err != nil {
		return nil, nil, false
	}
	if err := json.Unmarshal(meta[sdkv2TimeoutMetaKey], &times); err != nil || times == nil {
		return nil, nil, false
	}
	return meta, times, true
}

// withConfiguredTimeouts merges cfg into private's SDKv2 timeout meta and
// reports whether anything changed.
//
// # What it acts on, and what it refuses
//
// It acts on a private that already carries a READABLE timeout meta under
// the SDK's own key. helper/schema renders a `timeouts` block into a
// resource's schema only for the fields its own [schema.ResourceTimeout]
// declares non-nil, and metaEncode writes exactly those same fields into
// the instance state's meta - so for any SDKv2 resource whose schema HAS a
// timeouts block the imported stub's private always already carries this
// key with a real map of durations under it. Anything else is somebody
// else's private (terraform-plugin-framework's map[string][]byte, most of
// all) or a shape this package cannot read back, and it is returned
// byte-for-byte unchanged rather than rewritten into something its owner
// would choke on.
//
// # The one condition that enforces that, and why it is one condition
//
// Both halves of the `err != nil || times == nil` below are load-bearing,
// and an earlier version of this function had neither the second half nor
// an honest account of the first:
//
//   - err != nil catches a private whose meta value cannot be read as a map
//     of numbers at all: a partially-decodable map ({"delete":"twenty"})
//     leaves `times` NON-nil with a half-filled entry, so only the error
//     tells that one apart from a good decode. It also catches the ABSENT
//     key, because `meta[...]` yields a nil [json.RawMessage] and json
//     refuses that with "unexpected end of JSON input" - which is how a
//     framework private, or a private carrying no timeout meta at all, is
//     turned away.
//   - times == nil catches `"<key>": null`, which [json.Unmarshal] accepts
//     with NO error while leaving the map nil. That was a panic -
//     "assignment to entry in nil map" at the write below - on the
//     projection build path, which takes the whole plan down, and it is
//     reachable for any resource carrying a timeouts block. Found in
//     review of the first version of this fix; pinned by
//     TestWithConfiguredTimeoutsLeavesANonSdkPrivateAlone's "null meta"
//     case.
//
// A JSON null there is LEFT ALONE rather than treated as an empty map to
// populate, deliberately. helper/schema never writes one: metaEncode
// writes the key only when it has at least one duration to put under it.
// So a null under this key is not an SDKv2 meta missing its values, it is
// something this package did not write and does not understand - the same
// reading that turns a framework private away - and synthesizing a meta the
// provider never had would be inventing one rather than restoring one.
//
// There is deliberately no separate `if !ok` early return for the absent
// key. It read as the gate and could not fail: with the key absent the very
// next unmarshal refuses a nil [json.RawMessage] anyway, so removing that
// branch changed no behaviour and no test. A check that cannot fail is not
// a check, so it is gone and the condition that does the work says so.
func withConfiguredTimeouts(private []byte, cfg map[string]int64) ([]byte, bool) {
	if len(cfg) == 0 {
		return private, false
	}
	meta, times, ok := sdkv2TimeoutMeta(private)
	if !ok {
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

// GitHub issue #1240: the terraform-plugin-framework half of #1185. Same
// block in the schema, same configuration, the value in a different place.
//
// # Where a framework destroy's timeout comes from
//
// terraform-plugin-framework-timeouts renders `timeouts` as a
// SingleNestedBlock of Optional strings and keeps the value as an ordinary
// part of the resource's own state object: a framework Delete reads
// request.State into its model and calls data.Timeouts.Delete(ctx, default),
// which is timeouts.Value.getTimeout reading the "delete" attribute off
// that object and returning the caller's default when it is null. No
// private state is involved anywhere, which is why [withConfiguredTimeouts]
// refuses a framework private and why that refusal was never a fix for
// this.
//
// On the live path the prior object is rebuilt through ImportResourceState
// and ReadResource. The framework's import starts from a state that is null
// at every attribute and lets the resource set its id; its read hands the
// resource that state and takes back whatever the resource's Read sets,
// and a Read has nowhere to source a timeout from - every hashicorp/aws
// framework resource with a timeouts block round-trips the model's
// Timeouts field untouched (request.State.Get(&data) ...
// response.State.Set(&data)). So the projected prior carries a NULL
// timeouts object, where a stock state file carries what the last apply
// wrote there: the configured block.
//
// Measured at 53f445300b against a stub built from the framework's own
// source (timeouts_framework_test.go states which files, and what each
// does): the projected prior's timeouts attribute was JSON null for a
// resource declaring `timeouts { delete = "20s" }`; the destroy's prior
// state carried the same null; and that null prior replanned as an
// in-place Update against the unchanged configuration, on every run.
//
// # What this does about it
//
// Seeds the configured block into the prior's value, from the same decode
// [withConfiguredTimeouts] reads its durations from. This is a VALUE write,
// which #1185's private write deliberately was not, and it is what the
// issue asked to be measured before it was done: a prior that gains a
// timeouts object where the configuration declares one is the shape #1177
// and #1190 were. The measurement is in
// TestFrameworkTimeoutsSeedReplansAsNoChange: the seeded prior replans as
// NoOp against the same configuration, which is what stock's state-backed
// prior does, and the null prior it replaces replanned as Update. The seed
// removes a perpetual diff rather than adding one, because the value it
// writes is exactly the value the plan's proposed new state will hold.
//
// # The gate, and why it is the private blob
//
// It acts only on a prior whose private carries no readable SDKv2 timeout
// meta ([sdkv2TimeoutMeta]). That is the same test [withConfiguredTimeouts]
// uses, read the other way round, so the two carriers partition every
// private: an SDKv2 resource takes the meta write and is otherwise left
// byte-for-byte as #1185 shipped it (its own test pins that the value is
// not touched), and everything else takes the value write. No type name and
// no provider name decides it.
//
// The read keeps the last word: the seed is applied to what ReadResource
// RETURNED, and only where it returned null. A provider whose Read does
// fill the block from somewhere is not overridden, and
// [builder.fillResidueFor], which runs after this, leaves a non-null
// attribute alone under its own rule - so a residue record for the block,
// which the record store carries for any NestingSingle block, no longer
// overrides configuration here; configuration is read fresh every run and a
// record can be stale, the same ordering [builder.prepareRead] already
// gives every other seeded attribute.
//
// # What it deliberately does not reach
//
//   - A schema whose timeouts block is not NestingSingle, or whose
//     attribute types differ from what the configuration decoded to.
//   - A configuration whose block sets nothing, or sets anything that is
//     not a duration ([timeoutsBlockSeed]).
//   - A deposed object and an undeclared instance, for
//     [withConfiguredTimeouts]'s reasons.
func withConfiguredTimeoutsBlock(value, seed cty.Value, block *configschema.Block, private []byte) (cty.Value, bool) {
	if seed == cty.NilVal || value == cty.NilVal || value.IsNull() || !value.Type().IsObjectType() {
		return value, false
	}
	if timeoutsNestedBlock(block) == nil || !value.Type().HasAttribute(timeoutsBlockName) {
		return value, false
	}
	if _, _, sdkv2 := sdkv2TimeoutMeta(private); sdkv2 {
		return value, false
	}
	unmarked, paths := value.UnmarkDeepWithPaths()
	cur := unmarked.GetAttr(timeoutsBlockName)
	if !cur.IsNull() || !seed.Type().Equals(cur.Type()) {
		return value, false
	}
	attrs := unmarked.AsValueMap()
	attrs[timeoutsBlockName] = seed
	out := cty.ObjectVal(attrs)
	if len(paths) > 0 {
		out = out.MarkWithPaths(paths)
	}
	return out, true
}
