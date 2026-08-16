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
	"github.com/hashicorp/hcl/v2/ext/dynblock"
	"github.com/hashicorp/hcl/v2/hcldec"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/encryption"
	"github.com/intentius/choudoufu/internal/lang"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// encryptedDataSourceReader is the builtin terraform provider's special
// read entry point for terraform_remote_state
// (internal/builtin/providers/tf/provider.go, ReadDataSourceEncrypted): its
// ordinary ReadDataSource panics deliberately, because a state read needs
// the resource instance address (the encryption key name is derived from
// it) and an [encryption.Encryption] that ReadDataSource's signature has no
// room for. internal/tofu's own data-source eval draws the identical type
// assertion (node_resource_abstract_instance.go, ProviderWithEncryption) -
// this is a second, narrower copy of that interface rather than an import
// of internal/tofu, which this package does not otherwise depend on.
type encryptedDataSourceReader interface {
	ReadDataSourceEncrypted(ctx context.Context, req providers.ReadDataSourceRequest, path addrs.AbsResourceInstance, enc encryption.Encryption) providers.ReadDataSourceResponse
}

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
		if src.Eligible {
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

	// encBuilt, enc and encDiags cache the configuration's own encryption
	// setup (its "terraform { encryption { ... } }" block, or none), built
	// at most once and only when a terraform_remote_state source is
	// actually read - the same cost-avoidance the whole phase promises for
	// a configuration that never needed it. This is the real encryption a
	// user's own configuration declares for remote-state targets, not a
	// hardcoded no-op: a foreign stack's genuinely encrypted state fails to
	// decode honestly (see [SummaryCrossStackStateUnavailable]) exactly
	// when this run has no matching encryption configuration for it.
	encBuilt bool
	enc      encryption.Encryption
	encDiags hcl.Diagnostics
}

// encryption lazily builds this run's [encryption.Encryption] from the
// loaded configuration's own encryption block, the same construction
// internal/command/meta_encryption.go does for every other command that
// reads state. Built once and reused across every terraform_remote_state
// source this phase reads.
func (r *reader) encryption() (encryption.Encryption, hcl.Diagnostics) {
	if !r.encBuilt {
		r.encBuilt = true
		r.enc, r.encDiags = encryption.New(r.ctx, encryption.DefaultRegistry, r.cfg.Module.Encryption, r.cfg.Module.StaticEvaluator)
	}
	return r.enc, r.encDiags
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
// itself. A block whose arguments are the same for every instance
// ([Source.PerInstance] false) gets one provider call, reused across every
// instance; a block that reads its own count.index or each.key/each.value
// ([Source.PerInstance] true) gets one provider call per instance, each
// with that instance's own repetition value bound - the same per-instance
// shape stock OpenTofu's own plan-time data-source read has.
func (r *reader) readSource(src *Source) bool {
	node := r.cfg.Descendent(src.Module)
	if node == nil || node.Module == nil {
		return r.refuse(src, SummaryReadFailed, "%s's module is no longer in the configuration tree; this is a defect in the calling code.", src.Resource.String())
	}
	rc := src.Config

	// tfe_outputs' auth-surface check, offline and before any read is
	// attempted - the maintainer's ruling on #181 moves it here from
	// eligibility (analyze.go no longer draws it), so that a token missing
	// from THIS run's environment surfaces as a read-time refusal rather
	// than as an eligibility verdict about the configuration itself.
	if src.TfeOutputs {
		_, providerCfg := r.insts.findProviderConfig(node, rc)
		if ok, detail := r.insts.tfeAuthAvailable(providerCfg); !ok {
			return r.refuse(src, SummaryCrossStackOutputsUnavailable,
				"%s's value is needed to resolve the identity of %s, but %s.",
				src.Resource.String(), src.NeededBy, detail)
		}
	}

	// liveModuleEvaluator, not node.Module.StaticEvaluator directly: a
	// module-call variable reaching this source's own for_each/count/
	// arguments may resolve back into an ancestor module's own expression,
	// which needs that ancestor's own data lookup attached to see its own
	// data sources as coverage (issue #212) - the frozen evaluator
	// [configs.ModuleCall.decodeStaticVariables] captured at load time
	// never has one. r.lookupFor is already correctly scoped per module
	// (see its own doc), so passing it straight through keeps every
	// module's coverage bounded to that module's own reads, at every level
	// of the chain.
	eval := liveModuleEvaluator(r.ctx, r.cfg, src.Module, r.lookupFor)
	if eval == nil {
		return r.refuse(src, SummaryReadFailed, "%s's module is no longer in the configuration tree; this is a defect in the calling code.", src.Resource.String())
	}

	keys, eachVals, ok := r.expansionKeys(src, eval)
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
		switch {
		case src.TfeOutputs:
			return r.refuse(src, SummaryCrossStackOutputsUnavailable,
				"%s's value is needed to resolve the identity of %s, and its TFC/TFE workspace could not be reached: %s.",
				src.Resource.String(), src.NeededBy, err)
		case src.RemoteState:
			return r.refuse(src, SummaryCrossStackStateUnavailable,
				"%s's value is needed to resolve the identity of %s, and its provider could not be configured: %s.",
				src.Resource.String(), src.NeededBy, err)
		}
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

	if !src.PerInstance {
		// The common case: every instance's arguments are identical, so one
		// provider call answers all of them - the "one honest answer,
		// shared" cost bound this phase's doc.go promises.
		configVal, ok := r.decodeConfig(src, eval, dsSchema)
		if !ok {
			return false
		}
		state, ok := r.callRead(src, provider, dsSchema, configVal, keys)
		if !ok {
			return false
		}
		r.store(src, keys, func(addrs.InstanceKey) cty.Value { return state })
		return true
	}

	// src.PerInstance: at least one argument reads this block's own
	// count.index or each.key/each.value, so instances can genuinely
	// differ. Stock OpenTofu reads such a block once per instance, with
	// that instance's own repetition value bound
	// (internal/tofu/node_resource_abstract_instance.go's readDataSource,
	// fed by evaluationStateData.GetCountAttr/GetForEachAttr in
	// internal/tofu/evaluate.go) - this phase matches that shape instead of
	// sharing one answer across instances that were never promised to
	// agree.
	states := make(map[addrs.InstanceKey]cty.Value, len(keys))
	for _, key := range keys {
		configVal, ok := r.decodeConfigForInstance(src, eval, dsSchema, key, eachVals)
		if !ok {
			return false
		}
		state, ok := r.callRead(src, provider, dsSchema, configVal, []addrs.InstanceKey{key})
		if !ok {
			return false
		}
		states[key] = state
	}
	r.store(src, keys, func(key addrs.InstanceKey) cty.Value { return states[key] })
	return true
}

// callRead performs one provider read for one already-decoded config value
// and turns the response into a stored, mark-carrying state value or a
// refusal - the part of the read that stock OpenTofu's plan-time read and
// this phase's pre-resolution read do identically, whether the config value
// came from the block's one shared answer or from one instance's own
// binding.
func (r *reader) callRead(src *Source, provider providers.Interface, dsSchema providers.Schema, configVal cty.Value, keys []addrs.InstanceKey) (cty.Value, bool) {
	unmarked, _ := configVal.UnmarkDeep()
	req := providers.ReadDataSourceRequest{
		TypeName: src.Resource.Type,
		Config:   unmarked,
	}
	var resp providers.ReadDataSourceResponse
	if src.RemoteState {
		// terraform_remote_state's ReadDataSource panics deliberately
		// (internal/builtin/providers/tf/provider.go): the real entry point
		// needs the resource instance address and this run's own
		// encryption setup, which ReadDataSource's signature has no room
		// for. ConfiguredProvider already resolved this to the builtin
		// "terraform" provider - the same instance terraform_data has used
		// since #73 - so the type assertion always succeeds in practice;
		// the error branch is a defensive backstop, not a live path.
		tfp, ok := provider.(encryptedDataSourceReader)
		if !ok {
			return cty.NilVal, r.refuse(src, SummaryCrossStackStateUnavailable,
				"%s's provider does not support the encrypted remote-state read path this stage requires; this is a defect in the calling code.",
				src.Resource.String())
		}
		enc, encDiags := r.encryption()
		if encDiags.HasErrors() {
			return cty.NilVal, r.refuse(src, SummaryCrossStackStateUnavailable,
				"%s's value is needed to resolve the identity of %s, and this configuration's own encryption block could not be built: %s.",
				src.Resource.String(), src.NeededBy, encDiags.Error())
		}
		resp = tfp.ReadDataSourceEncrypted(r.ctx, req, r.representativeInstance(src, keys), enc)
	} else {
		resp = provider.ReadDataSource(r.ctx, req)
	}
	if resp.Diagnostics.HasErrors() {
		switch {
		case src.TfeOutputs:
			return cty.NilVal, r.refuse(src, SummaryCrossStackOutputsUnavailable,
				"reading %s's outputs from its TFC/TFE workspace failed; the provider said: %s.",
				src.Resource.String(), resp.Diagnostics.Err())
		case src.RemoteState:
			return cty.NilVal, r.refuse(src, SummaryCrossStackStateUnavailable,
				"reading %s from its backend failed; the backend said: %s.",
				src.Resource.String(), resp.Diagnostics.Err())
		}
		return cty.NilVal, r.refuse(src, SummaryReadFailed,
			"reading %s before resolution failed; the provider said: %s.",
			src.Resource.String(), resp.Diagnostics.Err())
	}
	state := resp.State
	if state == cty.NilVal || state.IsNull() {
		switch {
		case src.TfeOutputs:
			return cty.NilVal, r.refuse(src, SummaryCrossStackOutputsUnavailable,
				"reading %s returned no output values; the workspace may have no current state version.",
				src.Resource.String())
		case src.RemoteState:
			return cty.NilVal, r.refuse(src, SummaryCrossStackStateUnavailable,
				"reading %s returned no state; the backend may be unreachable or misconfigured.",
				src.Resource.String())
		}
		return cty.NilVal, r.refuse(src, SummaryReadFailed,
			"reading %s before resolution returned no value.", src.Resource.String())
	}
	// The provider's schema decides what is sensitive, and the marks ride
	// the value into resolution, where a sensitive part reaching an
	// identity refuses exactly as any sensitive value does.
	if marks := dsSchema.Block.ValueMarks(state, nil, nil); len(marks) > 0 {
		state = state.MarkWithPaths(marks)
	}
	return state, true
}

// representativeInstance picks one absolute instance address for a
// terraform_remote_state read's encryption-key lookup
// (encryption.Encryption.RemoteState keys by name, derived from this
// address the same way [tf.Provider.ReadDataSourceEncrypted] derives it).
// One call serves every instance of a count- or for_each-expanded block
// (the same "one honest answer, shared" rule [reader.readSource]'s doc
// comment states), so any one instance's address is representative; the
// first module instance and the first key are simplest and deterministic.
func (r *reader) representativeInstance(src *Source, keys []addrs.InstanceKey) addrs.AbsResourceInstance {
	key := addrs.InstanceKey(addrs.NoKey)
	if len(keys) > 0 {
		key = keys[0]
	}
	modInst := addrs.RootModuleInstance
	if insts := r.insts.moduleInstancesOf(src.Module); len(insts) > 0 {
		modInst = insts[0]
	}
	return src.Resource.Instance(key).Absolute(modInst)
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
// eachVals carries each key's own each.value, keyed the same way, for
// [reader.decodeConfigForInstance] to bind on a [Source.PerInstance] block;
// it is nil for a count-expanded or unexpanded block, where the only
// per-instance repetition value is the key itself.
func (r *reader) expansionKeys(src *Source, eval *configs.StaticEvaluator) ([]addrs.InstanceKey, map[string]cty.Value, bool) {
	rc := src.Config
	switch {
	case rc.Count != nil:
		val, ok := r.evalExpr(src, eval, rc.Count, "count")
		if !ok {
			return nil, nil, false
		}
		if val.IsMarked() {
			return nil, nil, r.refuse(src, SummaryNotReadable, "%s's count reads a sensitive value, so its instance keys cannot be computed before the plan.", src.Resource.String())
		}
		if val.IsNull() || !val.IsWhollyKnown() {
			return nil, nil, r.refuse(src, SummaryNotReadable, "%s's count does not evaluate to a value knowable before the plan.", src.Resource.String())
		}
		num, err := convert.Convert(val, cty.Number)
		if err != nil {
			return nil, nil, r.refuse(src, SummaryNotReadable, "%s's count is not a number: %s.", src.Resource.String(), err)
		}
		var n int
		if convErr := numToInt(num, &n); convErr != nil || n < 0 {
			return nil, nil, r.refuse(src, SummaryNotReadable, "%s's count is not a whole non-negative number.", src.Resource.String())
		}
		keys := make([]addrs.InstanceKey, n)
		for i := range keys {
			keys[i] = addrs.IntKey(i)
		}
		return keys, nil, true

	case rc.ForEach != nil:
		val, ok := r.evalExpr(src, eval, rc.ForEach, "for_each")
		if !ok {
			return nil, nil, false
		}
		if val.IsMarked() {
			return nil, nil, r.refuse(src, SummaryNotReadable, "%s's for_each reads a sensitive value, so its instance keys cannot be computed before the plan.", src.Resource.String())
		}
		if val.IsNull() || !val.IsWhollyKnown() {
			return nil, nil, r.refuse(src, SummaryNotReadable, "%s's for_each does not evaluate to a value knowable before the plan.", src.Resource.String())
		}
		ty := val.Type()
		if !ty.IsMapType() && !ty.IsObjectType() && !ty.IsSetType() {
			return nil, nil, r.refuse(src, SummaryNotReadable, "%s's for_each is neither a map nor a set of strings.", src.Resource.String())
		}
		var names []string
		eachVals := make(map[string]cty.Value)
		for it := val.ElementIterator(); it.Next(); {
			k, v := it.Element()
			if ty.IsSetType() {
				// A set's key IS its element, so k stops being the
				// cty-synthesized key that is never marked and becomes
				// whatever the configuration put in the set.
				k = v
			}
			if k.IsMarked() {
				return nil, nil, r.refuse(src, SummaryNotReadable, "%s's for_each reads a sensitive value, so its instance keys cannot be computed before the plan.", src.Resource.String())
			}
			if k.Type() != cty.String || k.IsNull() || !k.IsKnown() {
				return nil, nil, r.refuse(src, SummaryNotReadable, "%s's for_each has a key that is not a string.", src.Resource.String())
			}
			names = append(names, k.AsString())
			eachVals[k.AsString()] = v
		}
		sort.Strings(names)
		keys := make([]addrs.InstanceKey, len(names))
		for i, name := range names {
			keys[i] = addrs.StringKey(name)
		}
		return keys, eachVals, true

	default:
		return []addrs.InstanceKey{addrs.NoKey}, nil, true
	}
}

func numToInt(num cty.Value, out *int) error {
	// IsMarked before AsBigFloat, which panics on a marked value. The one
	// caller today refuses a marked count before converting, so this is the
	// same answer arrived at one function earlier; it is tested here as
	// well because the guard that makes it safe is not in this function.
	if num.IsMarked() {
		return fmt.Errorf("sensitive")
	}
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
// schema for the type, producing the config value ReadDataSource wants. It
// is used only for a block whose arguments are the same for every instance
// ([Source.PerInstance] false); [reader.decodeConfigForInstance] is its
// per-instance counterpart.
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

// decodeConfigForInstance is [reader.decodeConfig]'s counterpart for a
// [Source.PerInstance] block: it decodes the same body against the same
// schema, but with this instance's own count.index or each.key/each.value
// bound instead of refused, the same binding
// [analyzer.withRepetitionPlaceholders] validated was legitimate for this
// block at analysis time - only now with the real value, because a real
// value is exactly what a guess must never stand in for on this path.
//
// It rebuilds the evaluation context itself rather than reusing
// [configs.StaticEvaluator.DecodeBlock]: that method resolves every
// referenced traversal through the module's static scope, and the static
// scope panics on count.index/each.key/each.value unconditionally
// (internal/configs/static_scope.go's GetCountAttr/GetForEachAttr) because
// it has no per-instance repetition data to answer with. This function
// supplies that data itself, the same way [identity]'s own per-instance
// resolution does (internal/live/identity/resolve.go's evalPure): resolve
// every OTHER reference through the static evaluator as usual, then bind
// "each"/"count" as one more variable on the resulting context before
// decoding.
func (r *reader) decodeConfigForInstance(src *Source, eval *configs.StaticEvaluator, dsSchema providers.Schema, key addrs.InstanceKey, eachVals map[string]cty.Value) (val cty.Value, ok bool) {
	defer func() {
		if rec := recover(); rec != nil {
			val, ok = cty.NilVal, r.refuse(src, SummaryNotReadable,
				"%s's arguments could not be evaluated: %v.", src.Resource.String(), rec)
		}
	}()
	rc := src.Config
	body := rc.Config
	spec := dsSchema.Block.DecoderSpec()

	selfRoots := make(map[string]bool, 2)
	if rc.Count != nil {
		selfRoots["count"] = true
	}
	if rc.ForEach != nil {
		selfRoots["each"] = true
	}

	ident := configs.StaticIdentifier{
		Module:    src.Module,
		Subject:   src.Resource.String(),
		DeclRange: rc.DeclRange,
	}

	// Expand any "dynamic" block first, the same two-phase split
	// [configs.StaticEvaluator.DecodeBlock] now uses for the non-per-instance
	// path ([reader.decodeConfig]) - this function cannot reuse DecodeBlock
	// itself because it also needs this instance's own count.index/
	// each.key/each.value bound, which DecodeBlock's static scope refuses
	// unconditionally. A dynamic block's own for_each reading this source's
	// own each/count is not supported here (it would need those bound
	// during expansion too); it refuses cleanly through the same "not
	// statically evaluable" path below rather than doing anything with a
	// wrong or guessed value.
	var expandTravs []hcl.Traversal
	for _, trav := range dynblock.ExpandVariablesHCLDec(body, spec) {
		if selfRoots[trav.RootName()] {
			continue
		}
		expandTravs = append(expandTravs, trav)
	}
	expandRefs, expandRefDiags := lang.References(addrs.ParseRef, expandTravs)
	if expandRefDiags.HasErrors() {
		return cty.NilVal, r.refuse(src, SummaryNotReadable,
			"%s's arguments are not statically evaluable: %s.", src.Resource.String(), expandRefDiags.Err())
	}
	expandCtx, expandCtxDiags := eval.EvalContextWithParent(r.ctx, nil, ident, expandRefs)
	if expandCtxDiags.HasErrors() {
		return cty.NilVal, r.refuse(src, SummaryNotReadable,
			"%s's arguments are not statically evaluable: %s.", src.Resource.String(), expandCtxDiags.Error())
	}
	if expandCtx == nil {
		expandCtx = &hcl.EvalContext{}
	}
	body = dynblock.Expand(body, expandCtx)

	var travs []hcl.Traversal
	for _, trav := range hcldec.Variables(body, spec) {
		if selfRoots[trav.RootName()] {
			continue
		}
		travs = append(travs, trav)
	}
	refs, refDiags := lang.References(addrs.ParseRef, travs)
	if refDiags.HasErrors() {
		return cty.NilVal, r.refuse(src, SummaryNotReadable,
			"%s's arguments are not statically evaluable: %s.", src.Resource.String(), refDiags.Err())
	}

	hclCtx, ctxDiags := eval.EvalContextWithParent(r.ctx, nil, ident, refs)
	if ctxDiags.HasErrors() {
		return cty.NilVal, r.refuse(src, SummaryNotReadable,
			"%s's arguments are not statically evaluable: %s.", src.Resource.String(), ctxDiags.Error())
	}
	if hclCtx == nil {
		hclCtx = &hcl.EvalContext{}
	}
	if len(selfRoots) > 0 {
		child := hclCtx.NewChild()
		vars := make(map[string]cty.Value, len(selfRoots))
		if selfRoots["count"] {
			idx, _ := key.(addrs.IntKey)
			vars["count"] = cty.ObjectVal(map[string]cty.Value{
				"index": cty.NumberIntVal(int64(idx)),
			})
		}
		if selfRoots["each"] {
			var keyVal, valueVal cty.Value
			if sk, ok := key.(addrs.StringKey); ok {
				keyVal = cty.StringVal(string(sk))
				valueVal, ok = eachVals[string(sk)]
				if !ok {
					valueVal = cty.NullVal(cty.DynamicPseudoType)
				}
			} else {
				keyVal = cty.UnknownVal(cty.String)
				valueVal = cty.UnknownVal(cty.DynamicPseudoType)
			}
			vars["each"] = cty.ObjectVal(map[string]cty.Value{
				"key":   keyVal,
				"value": valueVal,
			})
		}
		child.Variables = vars
		hclCtx = child
	}

	v, valDiags := hcldec.Decode(body, spec, hclCtx)
	if valDiags.HasErrors() {
		return cty.NilVal, r.refuse(src, SummaryNotReadable,
			"%s's arguments are not statically evaluable: %s.", src.Resource.String(), valDiags.Error())
	}
	if !v.IsWhollyKnown() {
		return cty.NilVal, r.refuse(src, SummaryNotReadable,
			"%s's arguments evaluate to a value not knowable before the plan - an impure function such as uuid() or timestamp() is the usual cause.", src.Resource.String())
	}
	return v, true
}

// store records one read's value(s): the module-level aggregate for later
// evaluation, and one entry per instance address of every instance of the
// module for resolution. stateFor answers one instance key's value - a
// constant function for a block whose instances share one answer, or a map
// lookup for a [Source.PerInstance] block whose instances do not.
func (r *reader) store(src *Source, keys []addrs.InstanceKey, stateFor func(addrs.InstanceKey) cty.Value) {
	var agg cty.Value
	switch keys[0].(type) {
	case nil:
		agg = stateFor(keys[0])
	case addrs.IntKey:
		vals := make([]cty.Value, len(keys))
		for i, k := range keys {
			vals[i] = stateFor(k)
		}
		agg = cty.TupleVal(vals)
	case addrs.StringKey:
		vals := make(map[string]cty.Value, len(keys))
		for _, k := range keys {
			vals[string(k.(addrs.StringKey))] = stateFor(k)
		}
		agg = cty.ObjectVal(vals)
	}
	r.agg[sourceKey(src.Module, src.Resource)] = agg

	for _, modInst := range r.insts.moduleInstancesOf(src.Module) {
		for _, key := range keys {
			addr := src.Resource.Instance(key).Absolute(modInst)
			r.results[addr.String()] = stateFor(key)
		}
	}
}
