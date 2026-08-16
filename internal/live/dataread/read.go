// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package dataread

import (
	"context"
	"fmt"
	"sort"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// Providers is the one seam the read phase needs from its caller: a
// configured provider instance per provider configuration. The command
// layer's statelessProviders satisfies it - the same instances the
// projection builder calls ImportResourceState and ReadResource on, so the
// phase adds a verb, not a plumbing.
type Providers interface {
	ConfiguredProvider(ctx context.Context, addr addrs.AbsProviderConfig) (providers.Interface, error)
}

// Read performs the phase's reads: every eligible demanded source, in
// dependency order, one ReadDataSource call per data block. The result maps
// each data resource instance's absolute address to the value the provider
// returned, shaped for [identity.Context.DataResults].
//
// Any demanded source that is not eligible refuses fatally, every one of
// them at once, before a single network call: a partial value map would
// make resolution fail with the generic wording on exactly the sites this
// phase exists to explain. A failed read refuses fatally too, for the rule
// resolution already applies to identity holes.
//
// Values are never cached: a stale hint elsewhere costs a re-read, but a
// stale value here becomes a wrong marker. Every run reads live.
func Read(ctx context.Context, cfg *configs.Config, analysis *Analysis, provs Providers) (map[string]cty.Value, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics
	if analysis.Empty() {
		return nil, nil
	}

	for _, src := range analysis.Demanded() {
		if src.CrossStack || src.Eligible {
			// A cross-stack source keeps exactly the refusal resolution
			// raises for it today; this stage neither reads nor re-words it.
			continue
		}
		diags = diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  src.ReasonSummary,
			Detail:   src.ReasonDetail,
			Subject:  src.Config.DeclRange.Ptr(),
		})
	}
	if diags.HasErrors() {
		return nil, diags
	}

	r := &reader{
		ctx:      ctx,
		cfg:      cfg,
		analysis: analysis,
		provs:    provs,
		agg:      make(map[string]cty.Value),
		results:  make(map[string]cty.Value),
		insts:    &analyzer{ctx: ctx, cfg: cfg},
	}
	for _, src := range analysis.Demanded() {
		if src.CrossStack {
			continue
		}
		if !r.readSource(src) {
			// Stop at the first failure rather than fanning a misconfigured
			// provider's error across every remaining block: the run is
			// already refused, and each further call costs a real request.
			return nil, r.diags
		}
	}
	return r.results, r.diags
}

type reader struct {
	ctx      context.Context
	cfg      *configs.Config
	analysis *Analysis
	provs    Providers
	diags    tfdiags.Diagnostics

	// agg holds one aggregate value per (module path, resource), the shape a
	// whole-resource reference evaluates to, for later blocks in the same
	// module to evaluate against. Keyed by sourceKey.
	agg map[string]cty.Value

	// results is the instance-addressed map handed to resolution.
	results map[string]cty.Value

	// insts reuses the analyzer's module-instance walk.
	insts *analyzer
}

func (r *reader) refuse(src *Source, summary, format string, args ...any) bool {
	r.diags = r.diags.Append(&hcl.Diagnostic{
		Severity: hcl.DiagError,
		Summary:  summary,
		Detail:   fmt.Sprintf(format, args...),
		Subject:  src.Config.DeclRange.Ptr(),
	})
	return false
}

// readSource reads one data block: expansion first, then the block's
// arguments decoded against the provider's own schema, then the read
// itself. One call per block - a block expanded by count or for_each has
// statically identical arguments for every instance (an argument reading
// count.index or each.* is ineligible in this stage), so its instances
// share the one honest answer.
func (r *reader) readSource(src *Source) bool {
	node := r.cfg.Descendent(src.Module)
	if node == nil || node.Module == nil {
		return r.refuse(src, SummaryReadFailed, "%s's module is no longer in the configuration tree; this is a defect in the calling code.", src.Resource.String())
	}
	rc := src.Config

	lookup := r.lookupFor(src.Module)
	eval := node.Module.StaticEvaluator.Pure().WithDataResults(lookup)

	keys, ok := r.expansionKeys(src, eval)
	if !ok {
		return false
	}
	if len(keys) == 0 {
		// count = 0 or an empty for_each: nothing to read, and the
		// aggregate is honestly empty.
		if rc.ForEach != nil {
			r.agg[sourceKey(src.Module, src.Resource)] = cty.EmptyObjectVal
		} else {
			r.agg[sourceKey(src.Module, src.Resource)] = cty.EmptyTupleVal
		}
		return true
	}

	pcAddr := rc.ProviderConfigAddr()
	absAddr := addrs.AbsProviderConfig{
		Module:   addrs.RootModule,
		Provider: node.Module.ProviderForLocalConfig(pcAddr),
		Alias:    pcAddr.Alias,
	}
	provider, err := r.provs.ConfiguredProvider(r.ctx, absAddr)
	if err != nil {
		return r.refuse(src, SummaryProviderNotConfigurable,
			"%s's value is needed to resolve the identity of %s, and its provider could not be configured: %s.",
			src.Resource.String(), src.NeededBy, err)
	}

	schema := provider.GetProviderSchema(r.ctx)
	if schema.Diagnostics.HasErrors() {
		return r.refuse(src, SummaryProviderNotConfigurable,
			"%s's provider %s would not serve its schema: %s.",
			src.Resource.String(), absAddr.Provider.String(), schema.Diagnostics.Err())
	}
	dsSchema, ok := schema.DataSources[src.Resource.Type]
	if !ok || dsSchema.Block == nil {
		return r.refuse(src, SummaryReadFailed,
			"the configured provider %s serves no data source type %q; provider-version skew is the usual cause.",
			absAddr.Provider.String(), src.Resource.Type)
	}

	configVal, ok := r.decodeConfig(src, eval, dsSchema)
	if !ok {
		return false
	}

	unmarked, _ := configVal.UnmarkDeep()
	resp := provider.ReadDataSource(r.ctx, providers.ReadDataSourceRequest{
		TypeName: src.Resource.Type,
		Config:   unmarked,
	})
	if resp.Diagnostics.HasErrors() {
		return r.refuse(src, SummaryReadFailed,
			"reading %s before resolution failed; the provider said: %s.",
			src.Resource.String(), resp.Diagnostics.Err())
	}
	state := resp.State
	if state == cty.NilVal || state.IsNull() {
		return r.refuse(src, SummaryReadFailed,
			"reading %s before resolution returned no value.", src.Resource.String())
	}
	// The provider's schema decides what is sensitive, and the marks ride
	// the value into resolution, where a sensitive part reaching an
	// identity refuses exactly as any sensitive value does.
	if marks := dsSchema.Block.ValueMarks(state, nil, nil); len(marks) > 0 {
		state = state.MarkWithPaths(marks)
	}

	r.store(src, keys, state)
	return true
}

// lookupFor answers whole-resource data references from what the phase has
// read so far in one module - well-founded because sources read in
// dependency order.
func (r *reader) lookupFor(module addrs.Module) configs.StaticDataLookup {
	return func(addr addrs.Resource) (cty.Value, bool) {
		val, ok := r.agg[sourceKey(module, addr)]
		return val, ok
	}
}

// expansionKeys evaluates count/for_each - eligibility rule 2, now with the
// dependencies' real values in scope - into the block's instance keys.
func (r *reader) expansionKeys(src *Source, eval *configs.StaticEvaluator) ([]addrs.InstanceKey, bool) {
	rc := src.Config
	switch {
	case rc.Count != nil:
		val, ok := r.evalExpr(src, eval, rc.Count, "count")
		if !ok {
			return nil, false
		}
		if val.IsMarked() {
			return nil, r.refuse(src, SummaryNotReadable, "%s's count reads a sensitive value, so its instance keys cannot be computed before the plan.", src.Resource.String())
		}
		if val.IsNull() || !val.IsWhollyKnown() {
			return nil, r.refuse(src, SummaryNotReadable, "%s's count does not evaluate to a value knowable before the plan.", src.Resource.String())
		}
		num, err := convert.Convert(val, cty.Number)
		if err != nil {
			return nil, r.refuse(src, SummaryNotReadable, "%s's count is not a number: %s.", src.Resource.String(), err)
		}
		var n int
		if convErr := numToInt(num, &n); convErr != nil || n < 0 {
			return nil, r.refuse(src, SummaryNotReadable, "%s's count is not a whole non-negative number.", src.Resource.String())
		}
		keys := make([]addrs.InstanceKey, n)
		for i := range keys {
			keys[i] = addrs.IntKey(i)
		}
		return keys, true

	case rc.ForEach != nil:
		val, ok := r.evalExpr(src, eval, rc.ForEach, "for_each")
		if !ok {
			return nil, false
		}
		if val.IsMarked() {
			return nil, r.refuse(src, SummaryNotReadable, "%s's for_each reads a sensitive value, so its instance keys cannot be computed before the plan.", src.Resource.String())
		}
		if val.IsNull() || !val.IsWhollyKnown() {
			return nil, r.refuse(src, SummaryNotReadable, "%s's for_each does not evaluate to a value knowable before the plan.", src.Resource.String())
		}
		ty := val.Type()
		if !ty.IsMapType() && !ty.IsObjectType() && !ty.IsSetType() {
			return nil, r.refuse(src, SummaryNotReadable, "%s's for_each is neither a map nor a set of strings.", src.Resource.String())
		}
		var names []string
		for it := val.ElementIterator(); it.Next(); {
			k, v := it.Element()
			if ty.IsSetType() {
				k = v
			}
			if k.Type() != cty.String || k.IsNull() || !k.IsKnown() {
				return nil, r.refuse(src, SummaryNotReadable, "%s's for_each has a key that is not a string.", src.Resource.String())
			}
			names = append(names, k.AsString())
		}
		sort.Strings(names)
		keys := make([]addrs.InstanceKey, len(names))
		for i, name := range names {
			keys[i] = addrs.StringKey(name)
		}
		return keys, true

	default:
		return []addrs.InstanceKey{addrs.NoKey}, true
	}
}

func numToInt(num cty.Value, out *int) error {
	bf := num.AsBigFloat()
	i64, acc := bf.Int64()
	if !bf.IsInt() || acc != 0 {
		return fmt.Errorf("not a whole number")
	}
	*out = int(i64)
	return nil
}

// evalExpr evaluates one expression through the phase's evaluator, guarded
// the way every static evaluation on the live path is guarded.
func (r *reader) evalExpr(src *Source, eval *configs.StaticEvaluator, expr hcl.Expression, label string) (val cty.Value, ok bool) {
	defer func() {
		if rec := recover(); rec != nil {
			val, ok = cty.NilVal, r.refuse(src, SummaryNotReadable,
				"%s's %s could not be evaluated: %v.", src.Resource.String(), label, rec)
		}
	}()
	ident := configs.StaticIdentifier{
		Module:    src.Module,
		Subject:   fmt.Sprintf("%s's %s", src.Resource.String(), label),
		DeclRange: expr.Range(),
	}
	v, hclDiags := eval.Evaluate(r.ctx, expr, ident)
	if hclDiags.HasErrors() {
		return cty.NilVal, r.refuse(src, SummaryNotReadable,
			"%s's %s is not statically evaluable: %s.", src.Resource.String(), label, hclDiags.Error())
	}
	return v, true
}

// decodeConfig evaluates the block's arguments against the provider's own
// schema for the type, producing the config value ReadDataSource wants.
func (r *reader) decodeConfig(src *Source, eval *configs.StaticEvaluator, dsSchema providers.Schema) (val cty.Value, ok bool) {
	defer func() {
		if rec := recover(); rec != nil {
			val, ok = cty.NilVal, r.refuse(src, SummaryNotReadable,
				"%s's arguments could not be evaluated: %v.", src.Resource.String(), rec)
		}
	}()
	ident := configs.StaticIdentifier{
		Module:    src.Module,
		Subject:   src.Resource.String(),
		DeclRange: src.Config.DeclRange,
	}
	v, hclDiags := eval.DecodeBlock(r.ctx, src.Config.Config, dsSchema.Block.DecoderSpec(), ident)
	if hclDiags.HasErrors() {
		return cty.NilVal, r.refuse(src, SummaryNotReadable,
			"%s's arguments are not statically evaluable: %s.", src.Resource.String(), hclDiags.Error())
	}
	if !v.IsWhollyKnown() {
		return cty.NilVal, r.refuse(src, SummaryNotReadable,
			"%s's arguments evaluate to a value not knowable before the plan - an impure function such as uuid() or timestamp() is the usual cause.", src.Resource.String())
	}
	return v, true
}

// store records one read's value: the module-level aggregate for later
// evaluation, and one entry per instance address of every instance of the
// module for resolution.
func (r *reader) store(src *Source, keys []addrs.InstanceKey, state cty.Value) {
	var agg cty.Value
	switch keys[0].(type) {
	case nil:
		agg = state
	case addrs.IntKey:
		vals := make([]cty.Value, len(keys))
		for i := range vals {
			vals[i] = state
		}
		agg = cty.TupleVal(vals)
	case addrs.StringKey:
		vals := make(map[string]cty.Value, len(keys))
		for _, k := range keys {
			vals[string(k.(addrs.StringKey))] = state
		}
		agg = cty.ObjectVal(vals)
	}
	r.agg[sourceKey(src.Module, src.Resource)] = agg

	for _, modInst := range r.insts.moduleInstancesOf(src.Module) {
		for _, key := range keys {
			addr := src.Resource.Instance(key).Absolute(modInst)
			r.results[addr.String()] = state
		}
	}
}
