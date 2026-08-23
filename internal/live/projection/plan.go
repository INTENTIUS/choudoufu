// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/providerscope"
	"github.com/intentius/choudoufu/internal/plans/objchange"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// PlanInstances asks the provider what a resource's attributes WOULD be,
// without creating anything, and returns the answers in the shape
// [identity.Context.ManagedResults] takes.
//
// # Why this exists, and why it is not a cloud read
//
// [ReadInstances] answers "what is out there" and needs a cloud. This answers
// a different question - "what would the provider fill in" - and needs only
// the provider process. The distinction matters because a whole family of
// configurations is refused today for values that are knowable before
// anything exists.
//
// The canonical case is the ACM/Route53 validation pattern, which appears in
// two unrelated organisations' estates in this corpus and on HashiCorp's own
// documentation page:
//
//	resource "aws_route53_record" "cert_validation" {
//	  for_each = { for dvo in aws_acm_certificate.cert.domain_validation_options : dvo.domain_name => ... }
//	}
//
// domain_validation_options is computed, so the static evaluator cannot fold
// the key set and refuses "Non-static for_each expression". Stock OpenTofu
// plans this configuration - the AWS provider fills each element's
// domain_name during PlanResourceChange, from domain_name and
// subject_alternative_names, both of which the configuration states. No cloud
// call is involved, and no schema records the relationship: only the provider
// knows it. So the honest way to obtain the value is to ask the provider the
// same question stock asks.
//
// # What it deliberately does not do
//
// It plans a CREATE: prior state is null, so the answer is what the provider
// would fill for a resource that does not exist yet. That is the right
// question for resolving a configuration, and the wrong one for adopting a
// live object - [ReadInstances] is that.
//
// It plans only resources with no count and no for_each. A repeated resource
// has one planned value per instance and the key set is exactly what is in
// doubt, so planning "the" resource would be answering with one instance's
// values for all of them. That is a wrong value rather than a missing one.
//
// It never fails the caller. Any resource whose configuration will not decode
// statically, or whose plan the provider declines, is simply absent from the
// result: the resolution that wanted it then refuses exactly as it does
// today. A provider that returns an error about a value it cannot compute is
// answering the question, and the answer is "no".
//
// # Why the provider arrives as a seam rather than as one instance
//
// provs is [Providers], the same seam [Build] and [dataread.Read] take, and
// it is not a convenience. A resource selects its provider configuration per
// block, and the answer the provider gives depends on how that configuration
// is configured: an AWS provider pointed at one region derives a different
// ARN than the same provider pointed at another. Planning an aliased resource
// through the root default provider therefore mints a value from the WRONG
// region, and a wrong value ranks below a missing one everywhere in this
// package. [providerscope.ResolveResource] settles which configuration a
// block names, honouring an ancestor module call's providers mapping; a block
// whose provider cannot be configured contributes nothing, exactly as one the
// provider declines does.
//
// The type's schema comes from that same provider's own GetProviderSchema
// response rather than from a map the caller merged across providers, for the
// same reason and in the same shape [dataread.reader.readSource] does it: the
// process that decodes the block and the process that plans it must be one
// process.
func PlanInstances(ctx context.Context, cfg *configs.Config, provs Providers) (map[string]cty.Value, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics
	out := map[string]cty.Value{}
	if cfg == nil || cfg.Module == nil || provs == nil {
		return out, diags
	}
	eval := cfg.Module.StaticEvaluator
	if eval == nil {
		return out, diags
	}

	planModule(ctx, cfg, &planProviders{source: provs, entries: map[string]*planProviderEntry{}}, out)
	return out, diags
}

// planProviders resolves each provider configuration once per pass and
// remembers both the configured instance and its schemas. It is deliberately
// separate from [providerCache], which additionally runs the identity-schema
// verification [Build] needs and this pass has no business raising.
//
// The failure is sticky and silent. A provider that will not configure is not
// retried once per resource block, and it produces no diagnostic: every
// resource it would have served is simply absent from the result, which is
// the one outcome this whole pass promises for anything it cannot answer.
type planProviders struct {
	source  Providers
	entries map[string]*planProviderEntry
}

type planProviderEntry struct {
	provider providers.Configured
	schemas  map[string]providers.Schema
	ok       bool
}

func (p *planProviders) get(ctx context.Context, addr addrs.AbsProviderConfig) *planProviderEntry {
	key := addr.String()
	if e, seen := p.entries[key]; seen {
		return e
	}
	e := &planProviderEntry{}
	p.entries[key] = e

	prov, err := p.source.ConfiguredProvider(ctx, addr)
	if err != nil || prov == nil {
		return e
	}
	schema := prov.GetProviderSchema(ctx)
	if schema.Diagnostics.HasErrors() {
		return e
	}
	e.provider = prov
	e.schemas = schema.ResourceTypes
	e.ok = true
	return e
}

// planModule plans one module node and then its children.
//
// The recursion is the point rather than a detail: the resource whose
// computed attribute a for_each reads is very often in a shared module, not
// beside the block that reads it - the ACM certificate this exists for sits
// in simpleinfra's shared/modules/acm-certificate while four different
// estates call it. Walking the root alone would have found nothing at all.
//
// Keys are ABSOLUTE instance addresses ("module.acm.aws_acm_certificate.cert"),
// because that is what identity's own index parses them as
// (addrs.ParseAbsResourceInstanceStr in dataresults.go). A bare
// "aws_acm_certificate.cert" from a child module would be filed against a
// root resource that does not exist, which is a wrong value rather than a
// missing one.
func planModule(ctx context.Context, cfg *configs.Config, provs *planProviders, out map[string]cty.Value) {
	if cfg == nil || cfg.Module == nil {
		return
	}
	eval := cfg.Module.StaticEvaluator
	if eval == nil {
		return
	}
	for _, res := range cfg.Module.ManagedResources {
		if res.Count != nil || res.ForEach != nil {
			// See PlanInstances' doc comment: one planned value cannot stand
			// for a set of instances whose key set is the thing in question.
			continue
		}
		// The block's OWN provider configuration, not the root default one:
		// see PlanInstances' doc comment on the wrong-region hazard.
		entry := provs.get(ctx, providerscope.ResolveResource(cfg, res))
		if !entry.ok {
			continue
		}
		schema, ok := entry.schemas[res.Type]
		if !ok || schema.Block == nil {
			continue
		}
		val, ok := planOne(ctx, eval, cfg.Path, res, schema, entry.provider)
		if !ok {
			continue
		}
		out[res.Addr().Absolute(cfg.Path.UnkeyedInstanceShim()).Instance(addrs.NoKey).String()] = val
	}
	for _, child := range cfg.Children {
		// A repeated module call has one set of values per instance, for the
		// same reason a repeated resource does, and cfg.Path carries no key
		// to tell them apart. Planning it would file one instance's values
		// under an address that names all of them.
		if child != nil && child.Path.IsRoot() {
			continue
		}
		if call, ok := cfg.Module.ModuleCalls[child.Path[len(child.Path)-1]]; ok &&
			(call.Count != nil || call.ForEach != nil) {
			continue
		}
		planModule(ctx, child, provs, out)
	}
}

// planOne decodes one resource's configuration statically and asks the
// provider to plan a create from it. The bool is "the provider answered";
// every other outcome leaves the resource out of the result, because a
// resolution that refuses for want of this value is the status quo and is
// safe, while a fabricated value is not.
func planOne(ctx context.Context, eval *configs.StaticEvaluator, modPath addrs.Module, res *configs.Resource, schema providers.Schema, prov providers.Configured) (val cty.Value, ok bool) {
	// A provider plugin is a subprocess doing arbitrary work on a value this
	// package built. dataread's own decode guards the same way, for the same
	// reason: one badly-shaped configuration must not take the whole
	// instrument down.
	defer func() {
		if rec := recover(); rec != nil {
			val, ok = cty.NilVal, false
		}
	}()

	ident := configs.StaticIdentifier{
		Module:    modPath,
		Subject:   res.Addr().String(),
		DeclRange: res.DeclRange,
	}
	configVal, hclDiags := eval.DecodeBlock(ctx, res.Config, schema.Block.DecoderSpec(), ident)
	if hclDiags.HasErrors() || configVal == cty.NilVal {
		return cty.NilVal, false
	}
	// Unlike internal/live/dataread's decode, a partly-unknown value is
	// wanted here rather than refused. The whole point is that some
	// arguments are known and the provider can derive more from them; an
	// argument that is not statically evaluable simply arrives unknown and
	// the provider says what it can.

	// Unmarked before it crosses the plugin channel, and the paths kept so
	// the answer can be re-marked on the way back - the same unmark/re-mark
	// pair upstream's own plan performs around this identical call
	// (internal/tofu/node_resource_abstract_instance.go's
	// unmarkedConfigVal/unmarkedPaths, then
	// combinePathValueMarks(unmarkedPaths, schema.Block.ValueMarks(...))).
	//
	// It is load-bearing rather than tidy. StaticEvaluator.DecodeBlock,
	// unlike its DecodeExpression sibling, has no sensitive-value guard, and
	// internal/configs/static_scope.go marks a `sensitive = true` input
	// variable on the way in - so a block reading one decodes to a MARKED
	// configVal, cty's msgpack encoder refuses a marked value outright
	// ("value has marks, so it cannot be serialized"), and the refusal
	// arrives as an ordinary provider diagnostic, which the error branch
	// below reads as "the provider declined" and drops the resource from the
	// result. Silently: this pass never fails its caller, so the only
	// evidence was a TRACE line, and any resource whose identity derived from
	// that block then refused to resolve and was proposed for CREATION beside
	// a live object that already existed. Found by the audit of #343/#344's
	// fix to builder.normalizeIdentityAttrs, which is the same defect one
	// call site over; pinned by TestPlanOneAsksTheProviderWithAnUnmarkedValue
	// and TestPlanInstancesSurvivesASensitiveVariable.
	//
	// The marks go back on the ANSWER rather than being discarded, because
	// the answer is what a caller feeds to identity resolution, and
	// internal/live/identity refuses a marked value as an identity component
	// on purpose (a component becomes a cloud tag in plaintext). Dropping
	// them here would convert this silent omission into a silent leak, which
	// is the worse of the two. The schema's own Sensitive attributes are
	// combined in for the same reason [importAndRead] applies them to a
	// concrete read: a value off the wire carries no marks, and both seams
	// feed the same ManagedResults index.
	unmarkedConfigVal, configPaths := configVal.UnmarkDeepWithPaths()

	prior := cty.NullVal(schema.Block.ImpliedType())
	proposed := objchange.ProposedNew(schema.Block, prior, unmarkedConfigVal)

	resp := prov.PlanResourceChange(ctx, providers.PlanResourceChangeRequest{
		TypeName:         res.Type,
		PriorState:       prior,
		ProposedNewState: proposed,
		Config:           unmarkedConfigVal,
		// A null of the dynamic pseudo-type rather than the zero cty.Value:
		// the plugin client marshals ProviderMeta whenever the provider
		// declares a provider_meta schema, and a value with no type at all
		// panics the conformance check. Same call, same reason, as
		// liveimport's stamp.go.
		ProviderMeta: cty.NullVal(cty.DynamicPseudoType),
	})
	if resp.Diagnostics.HasErrors() {
		return cty.NilVal, false
	}
	if resp.PlannedState == cty.NilVal || resp.PlannedState.IsNull() {
		return cty.NilVal, false
	}
	return remarkPlanned(resp.PlannedState, schema.Block, configPaths), true
}

// remarkPlanned puts back onto a provider's answer the sensitivity the
// question was stripped of, plus whatever the schema declares sensitive on
// its own.
//
// It is upstream's own two-source composition
// (combinePathValueMarks(unmarkedPaths, schema.Block.ValueMarks(...)) at the
// end of node_resource_abstract_instance.go's plan), stated once so that
// every caller in this package that unmarks for an RPC re-marks the same
// way. A path in configPaths that names nothing in the planned value simply
// matches nothing: cty.Value.MarkWithPaths is a transform walk, not a
// lookup.
func remarkPlanned(planned cty.Value, block *configschema.Block, configPaths []cty.PathValueMarks) cty.Value {
	if planned == cty.NilVal || planned.IsNull() {
		return planned
	}
	var schemaPaths []cty.PathValueMarks
	if block != nil {
		// ValueMarks descends with GetAttr and ElementIterator, both of
		// which panic on a marked receiver. planned came off the wire, so it
		// carries no marks - and this call is made before any are added.
		schemaPaths = block.ValueMarks(planned, nil, nil)
	}
	combined := combineValueMarks(configPaths, schemaPaths)
	if len(combined) == 0 {
		return planned
	}
	return planned.MarkWithPaths(combined)
}

// ProviderConfigValue statically evaluates a configuration's own DEFAULT
// (unaliased) provider block for one provider type, so a caller can configure
// a provider the way this estate configures it.
//
// It exists because [PlanInstances] needs a CONFIGURED provider -
// PlanResourceChange is on that interface - and what the provider is
// configured with can change the answer. An AWS provider pointed at one
// region derives a different ARN than the same provider pointed at another,
// and a planned value obtained under the wrong configuration is a wrong
// value, which outranks a missing one. So there is no default here and no
// fallback: a configuration whose provider block does not evaluate statically
// gets no planned values at all, and refuses exactly as it does today.
//
// The aliased blocks are deliberately not read. A resource selects its
// provider per block, and pairing the wrong one with a resource is the same
// error as the wrong region. Handling aliases means resolving each resource's
// own provider reference, which is worth doing and is not this.
func ProviderConfigValue(ctx context.Context, cfg *configs.Config, providerType string, schema *configschema.Block) (val cty.Value, ok bool) {
	defer func() {
		if rec := recover(); rec != nil {
			val, ok = cty.NilVal, false
		}
	}()
	if cfg == nil || cfg.Module == nil || schema == nil {
		return cty.NilVal, false
	}
	eval := cfg.Module.StaticEvaluator
	if eval == nil {
		return cty.NilVal, false
	}
	pc, found := cfg.Module.ProviderConfigs[providerType]
	if !found || pc == nil || pc.Alias != "" {
		return cty.NilVal, false
	}
	ident := configs.StaticIdentifier{
		Module:    cfg.Path,
		Subject:   "provider." + providerType,
		DeclRange: pc.DeclRange,
	}
	v, hclDiags := eval.DecodeBlock(ctx, pc.Config, schema.DecoderSpec(), ident)
	if hclDiags.HasErrors() || v == cty.NilVal {
		return cty.NilVal, false
	}
	// A provider configured from values this run cannot know is a provider
	// configured differently from the operator's, which is the case this
	// function refuses rather than guesses at.
	if !v.IsWhollyKnown() {
		return cty.NilVal, false
	}
	return v, true
}
