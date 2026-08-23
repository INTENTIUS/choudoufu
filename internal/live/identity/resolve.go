// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"
	"github.com/zclconf/go-cty/cty/gocty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/instances"
	"github.com/intentius/choudoufu/internal/lang"
	"github.com/intentius/choudoufu/internal/live/providerscope"
	"github.com/intentius/choudoufu/internal/live/strict"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// Resolve classifies every managed resource instance in the given
// configuration, using only the configuration itself: no provider process,
// no state, no cloud reads.
//
// The returned Result holds one Resolution per instance that could be
// classified. An instance that could not be classified is absent from the
// Result and has at least one error diagnostic explaining why; callers must
// treat error diagnostics as fatal for the run, since a projection built
// from a partial identity map would plan to create resources that already
// exist.
//
// Input variable values come from the configuration's own static module
// call, i.e. whatever the caller passed to the loader, falling back to
// declared defaults.
func Resolve(ctx context.Context, cfg *configs.Config) (*Result, tfdiags.Diagnostics) {
	return ResolveWith(ctx, cfg, Context{})
}

// Context is everything a caller may tell resolution about the world outside
// the configuration. Every field is optional and the zero value is what
// [Resolve] passes: nothing here is ever fetched, discovered, or guessed, and
// a caller with none of it gets the same answers this package has always
// given.
//
// The two fields are the same shape of escape hatch for the same reason. A
// configuration does not say which account it will be applied to, and it does
// not carry the provider's account of what identifies each resource type;
// both live behind a running provider, and resolution runs in front of one.
// So each is handed in by a caller that already has it, and its absence is an
// ordinary answer rather than an error.
type Context struct {
	// Cloud is the account and region the run is against. See
	// [CloudContext].
	Cloud CloudContext

	// Schemas are the provider's managed resource type schemas, keyed by
	// type name, as GetProviderSchema returns them. They let a type absent
	// from [DefaultTable] resolve anyway, when the provider's own resource
	// identity schema describes it completely enough. See
	// [SynthesizeTypeIdentity] for the rule and for what it refuses.
	//
	// Nil is the default and means the hand table is the whole of what this
	// run knows, which is what every caller running before a plugin has
	// started passes.
	Schemas map[string]providers.Schema

	// DataResults are the values the pre-resolution data-read phase read
	// (issue #179), keyed by the data resource instance's absolute address
	// ([addrs.AbsResourceInstance.String]). They let an identity argument, a
	// count or a for_each that reads a data source resolve against the
	// provider's own answer instead of refusing as non-static: the static
	// evaluator permits and answers exactly the references these cover, and
	// nothing else changes.
	//
	// Nil is the default and means no phase ran, which is what every caller
	// running before a provider has started passes; every data-source
	// reference then refuses exactly as it always has. Nothing here is ever
	// fetched by this package - the reads happen in internal/live/dataread,
	// and a stale or invented value on this path would become a live
	// ownership marker, which is why the phase re-reads on every run and no
	// cache exists.
	DataResults map[string]cty.Value

	// ManagedResults are the values a caller read from the live system for
	// managed resource instances this estate already owns (issue #187),
	// keyed the same way as DataResults. They let a count, a for_each or an
	// identity argument that reads a COMPUTED attribute of a sibling -
	// aws_acm_certificate.cert.domain_validation_options, which the
	// certificate's own block never sets and no rule can derive from
	// configuration - resolve against what the cloud actually holds.
	//
	// It is the same seam as DataResults and shares its index; the two
	// cannot collide because a data resource's address carries the "data."
	// prefix and a managed one does not. It differs in what a caller has to
	// do to populate it: a data source can be read outright, whereas a
	// managed resource that is server-assigned has no identity until marker
	// discovery has found it, so a caller filling this in has already run
	// resolution once and discovery once. See [Context.DataResults] for
	// everything else, which is identical.
	//
	// Nil is the default and every reference to a managed attribute refuses
	// exactly as it always has. Nothing here is ever read by this package.
	//
	// A caller must put in only the resources whose attributes it means to
	// answer. An entry for a resource a for_each iterates WHOLE
	// (for_each = aws_subnet.this) is ignored rather than honoured, because
	// those instance keys come from the parent block's own expansion and are
	// configuration data that needs no read - see [resolver.managedCovered].
	ManagedResults map[string]cty.Value

	// Scope is which resource blocks this run's -target / -exclude
	// filtering leaves in the plan graph (GitHub issue #352). Nil is the
	// default and means all of them, which is every untargeted run and
	// every caller that has no graph to ask.
	//
	// A block outside it is still resolved, and still contributes its
	// resolution when it resolves - the marker sweep's declared-address set
	// is built from these resolutions and must stay complete - but its
	// refusals are dropped rather than raised, because stock OpenTofu
	// removed that block from the graph before anything evaluated it. See
	// [Scope] and [resolver.walkOutOfScope].
	Scope Scope
}

// ResolveWith is [Resolve] told everything the caller knows that the
// configuration does not carry. See [Context].
func ResolveWith(ctx context.Context, cfg *configs.Config, rctx Context) (*Result, tfdiags.Diagnostics) {
	return resolveWith(ctx, cfg, rctx)
}

// ResolveIn is [Resolve] told which cloud the run is against, so that the
// types whose import identity embeds the account or the region can be
// computed rather than deferred. See [CloudContext] for what those values
// are and where a caller gets them.
//
// A caller that does not have them passes the zero value, which is exactly
// what [Resolve] does: every type needing a value the context does not carry
// classifies [ClassNeedsDiscovery], naming the missing property, and marker
// discovery finds it instead. That fallback is the reason this parameter can
// exist at all without making the package need a provider - nothing here
// ever fetches a cloud property, and nothing here fails for want of one.
func ResolveIn(ctx context.Context, cfg *configs.Config, cloud CloudContext) (*Result, tfdiags.Diagnostics) {
	return resolveWith(ctx, cfg, Context{Cloud: cloud})
}

func resolveWith(ctx context.Context, cfg *configs.Config, rctx Context) (*Result, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	if cfg == nil || cfg.Module == nil {
		diags = diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "No configuration to resolve",
			Detail:   "Identity resolution was given an empty configuration.",
		})
		return newResult(), diags
	}
	if cfg.Module.StaticEvaluator == nil {
		diags = diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "Configuration loaded without a static evaluator",
			Detail:   "Identity resolution evaluates configuration expressions through the module's static evaluator, which this configuration does not have. Load the configuration with configs.Parser.LoadConfigDir or the configload package.",
		})
		return newResult(), diags
	}

	r := newResolver(ctx, cfg, rctx)

	result := newResult()
	// Collected for the whole configuration tree before anything is
	// classified, and for every type rather than only the admitted ones.
	// Two reasons: the config-side naming signal is what says which types
	// could join the table, so a type the table has never heard of is the
	// interesting case (signal.go); and the fallback that lets such a type
	// resolve at all consults the signal for its verdict, which has to be
	// the whole configuration's answer rather than however much of it the
	// walk happens to have reached. See [SynthesizeTypeIdentity].
	result.signal = r.collectSignal(cfg)
	r.signal = result.signal
	r.walkModule(cfg, addrs.RootModuleInstance, result)

	r.checkCollisions(result)
	r.warnUnsweepableTypes()

	return result, r.diags
}

// warnUnsweepableTypes says once per run which resource types this
// configuration relies on that no estate-wide sweep can recover.
//
// GitHub issue #107. A type absent from [DefaultTable] can still be admitted,
// when the provider's identity schema or the configuration's own arguments
// settle it ([SynthesizeTypeIdentity]) - and that admission is deliberately
// additive, so it only ever widens what plans. The sweep did not follow:
// internal/live/discovery draws its universe from [AdmittedTypes], which is
// the table's keys, so removing the last block of a synthesized type leaves
// the live resource in the account with no run that will ever propose
// removing it.
//
// The estate-wide tag sweep can see such a resource and reports it
// (discovery's ProblemUnsweepableOwnedType), but it is capability-gated and
// no command sets it today. This warning is on the path every run takes, and
// it fires while the block is still declared - which is the only moment the
// information is useful, because once the block is gone there is nothing
// left to warn about.
//
// A warning rather than an error: the type plans and applies correctly, and
// refusing it would withdraw coverage this fork went out of its way to add.
func (r *resolver) warnUnsweepableTypes() {
	names := make([]string, 0, len(r.synth))
	for typeName, entry := range r.synth {
		if entry != nil {
			names = append(names, typeName)
		}
	}
	sort.Strings(names)

	for _, typeName := range names {
		r.diags = r.diags.Append(tfdiags.Sourceless(
			tfdiags.Warning,
			SummaryNoOrphanRecovery,
			fmt.Sprintf(
				"%s is admitted by the provider's own identity schema rather than by this fork's admission table, so it plans and applies normally - but the estate-wide sweep draws its type universe from that table and will not list it. Deleting the last %s block from this configuration leaves the live resource in the account with no run proposing to remove it, and no warning at that point either. Remove it by hand when you remove the block. See live/LIMITATIONS.md, %q.",
				typeName, typeName, SummaryNoOrphanRecovery),
		))
	}
}

// walkModule classifies every managed resource instance in one node of the
// static module tree, at the given module instance, then recurses into its
// children in name order.
//
// modInst is the instance this call of cfg belongs to, chosen by the
// caller: [addrs.RootModuleInstance] for the root, or - for a for_each
// child - one [addrs.ModuleInstance.Child] call per key the enclosing
// module's for_each expands to (see the loop below). cfg.Path can no longer
// answer this by itself once a for_each module is in the tree, because
// cfg.Children holds one node per module CALL, not per instance: two
// instances of a for_each'd module share the same *configs.Config and are
// told apart only by the modInst each walkModule call carries.
//
// Recursion is depth-first and each call re-enters its own module before
// touching anything, so a resource reference resolved partway through one
// module's instances never observes another module's [resolver.mod] -
// see [resolver.enterModuleAt].
func (r *resolver) walkModule(cfg *configs.Config, modInst addrs.ModuleInstance, result *Result) {
	r.enterModuleAt(cfg, modInst)
	for _, rc := range sortedResources(cfg.Module.ManagedResources) {
		// GitHub issue #352: a -target / -exclude run evaluates only what
		// the plan graph still holds. A block the graph dropped is resolved
		// anyway - its address still has to be declared, or the marker sweep
		// reads its live objects as orphans - but it cannot refuse the run.
		// See [resolver.walkOutOfScope].
		if !r.inScope(rc) {
			r.walkOutOfScope(rc, result)
			continue
		}
		exp, ok := r.expansionFor(rc)
		if !ok {
			continue
		}
		for _, key := range exp.keys {
			addr := rc.Addr().Instance(key).Absolute(r.modInst)
			res, ok := r.instance(addr, rc.DeclRange)
			if !ok {
				continue
			}
			result.add(res)
		}
	}
	for _, name := range SortedChildNames(cfg.Children) {
		// Restored before every sibling, not just once before the loop: the
		// recursive r.walkModule call below, for whichever sibling sorts
		// before this one, ends by leaving r.mod/r.curCfg/r.modInst/r.eval
		// pointing at wherever ITS OWN deepest descendant left them -
		// enterModuleAt has no notion of a caller to return to. Without
		// this, a later sibling's own r.mod.ModuleCalls[name] lookup below
		// silently reads an unrelated module's call table (typically empty,
		// so its own count/for_each is missed entirely) whenever an
		// earlier-sorted sibling has any children of its own to recurse
		// into first. Found via govuk-infrastructure's opensearch
		// blue/green module: "blue_domain" sorts and is walked before
		// "snapshot_bucket", and blue_domain's own subtree left r.mod
		// pointing three levels down by the time snapshot_bucket's count
		// was read.
		r.enterModuleAt(cfg, modInst)
		child := cfg.Children[name]
		// [ChildCallKeys] makes the count / for_each / static dispatch, in
		// that order, and is shared with the three other walks that have to
		// build the same addresses this one does.
		keys, diag := ChildCallKeys(r.ctx, r.curCfg, name)
		if diag != nil {
			r.diags = r.diags.Append(diag)
			continue
		}
		for _, key := range keys {
			r.walkModule(child, modInst.Child(name, key), result)
		}
	}
}

// childSubject names a module call for a diagnostic about its own for_each
// expression: "module \"wrapped\"", matching how a resource's own for_each
// diagnostics name the resource.
func childSubject(name string) string {
	return fmt.Sprintf("module %q", name)
}

// checkCollisions reports two instances of the same type that resolve to
// the same identity. They would bind to one live resource, so the plan
// would treat one cloud object as two managed resources, the same class of
// ambiguity the marker path treats as a named error rather than picking a
// winner.
func (r *resolver) checkCollisions(result *Result) {
	// Grouped by Type+ident+base only - [cloudScopeKey.base] is the part
	// two resources must match exactly, the same partition the old
	// single-string key made when region played no part. Region is
	// resolved WITHIN a group instead (regionsDistinguish), because unlike
	// base it must never rule out a collision on its own: a group can hold
	// members with a known region, an unknown one, or both, and a member
	// with no known region has to be compared against every other member
	// of its own group, not just the first one seen (#217's own safety
	// direction - see regionsDistinguish's doc comment).
	//
	// [resolver.scopeContextValues] answers the same question for the rest
	// of a resource's location - a GCP project, a zone, an AWS account_id -
	// wherever the provider's own identity schema names it and the resolved
	// identity does not carry it (#200). contextDistinguish compares those
	// by regionsDistinguish's rule exactly: known on both sides and
	// different, or no answer at all.
	seen := make(map[string][]scopedResolution)
	for _, res := range result.All() {
		var ident, shown string
		switch res.Class {
		case ClassConcrete:
			ident, shown = concreteIdentityKey(res)
		case ClassParentDerived:
			ident = res.Formula.String()
			shown = ident
		default:
			continue
		}
		key := res.Type() + "\x00" + ident + "\x00" + res.cloudScope.base
		ctx := r.scopeContextValues(res)

		var collidesWith *Resolution
		for i := range seen[key] {
			if regionsDistinguish(seen[key][i].res.cloudScope, res.cloudScope) {
				continue
			}
			if contextDistinguish(seen[key][i].ctx, ctx) {
				continue
			}
			collidesWith = &seen[key][i].res
			break
		}
		seen[key] = append(seen[key], scopedResolution{res: res, ctx: ctx})
		if collidesWith == nil {
			continue
		}

		rng := hcl.Range{}
		if r.enterModuleFor(res.Addr.Module) {
			if rc := r.mod.ResourceByAddr(res.Addr.Resource.Resource); rc != nil {
				rng = rc.DeclRange
			}
		}
		r.errorf(rng, "Two resources with the same identity",
			"%s and %s both resolve to the identity %q. Both would bind to the same live resource, so one of them has to change: an identity is what tells a live-markers run which cloud object a configuration block owns.",
			collidesWith.Addr.String(), res.Addr.String(), shown)
	}
}

// concreteIdentityKey is what [resolver.checkCollisions] compares two
// concrete resolutions by: the identity the configuration actually supplies.
//
// It reads [Resolution.IdentityValues] first because [Resolution.ImportID]
// is not always the identity. For a type whose entry is
// [TypeIdentity.IdentityObjectOnly] - several identity attributes and no
// separator any schema documents to join them with - [classify] sets
// ImportID to the empty string DELIBERATELY, so that the projection imports
// by identity object instead of inventing a grammar. Keying a collision
// check on that string makes every such instance of the type look identical
// to every other, and the check refuses a configuration whose resources have
// perfectly distinct identities. Measured: three aws_autoscaling_schedule
// instances in terraform-aws-modules/autoscaling's complete example, whose
// scheduled_action_name values are "morning", "night" and
// "go-offline-to-celebrate-new-year", were reported as three resources with
// the identity "".
//
// Where both are populated they carry the same information - IdentityValues
// is the same parts, split per attribute rather than concatenated - so
// preferring the split loses nothing. It can only ever be MORE precise:
// {"a": "x", "b": "y"} and {"a": "xy", "b": ""} concatenate to the same
// string and are two different live objects, which the string alone would
// have reported as one.
//
// ImportID remains the answer for a type whose entry does not say which
// identity attribute each component supplies (aws_route_table_association is
// the documented case), where IdentityValues is nil - see its doc comment.
//
// shown is the same identity rendered for the operator who reads the
// refusal. It stays the import ID wherever there is one, because that is the
// string every other operator-facing line in this package prints; only the
// identity-object case, which has no such string, renders the attributes.
func concreteIdentityKey(res Resolution) (key, shown string) {
	if len(res.IdentityValues) == 0 {
		return res.ImportID, res.ImportID
	}
	names := make([]string, 0, len(res.IdentityValues))
	for name := range res.IdentityValues {
		names = append(names, name)
	}
	sort.Strings(names)

	var buf, disp strings.Builder
	for i, name := range names {
		buf.WriteString(name)
		buf.WriteByte(0)
		buf.WriteString(res.IdentityValues[name])
		buf.WriteByte(0)

		if i > 0 {
			disp.WriteString(", ")
		}
		fmt.Fprintf(&disp, "%s=%s", name, res.IdentityValues[name])
	}
	if res.ImportID != "" {
		return buf.String(), res.ImportID
	}
	return buf.String(), disp.String()
}

// regionsDistinguish is the only way two [cloudScopeKey] values sharing the
// same base can be told apart (#217): both sides must have determined an
// effective region, AND those regions must differ. Any other combination -
// either side's regionKnown false, or both known and equal - collides.
// This is deliberately asymmetric with how base is compared: base rules a
// pair IN or OUT on its own, region only ever rules a pair OUT, and only
// when this run is actually confident it knows both resources' regions and
// that they differ. A region this run could not determine is not evidence
// of "somewhere else" - it is exactly the same "cannot disambiguate, so
// don't" this package already applies to a `region` argument that fails to
// evaluate statically (see [resolver.resourceCloudScope]'s own doc
// comment), extended so the same caution holds when it is the OTHER side of
// a comparison that could not be determined.
func regionsDistinguish(a, b cloudScopeKey) bool {
	return a.regionKnown && b.regionKnown && a.region != b.region
}

// scopedResolution is one resolution plus the scope attributes
// [resolver.checkCollisions] compares it by that [Resolution.cloudScope]
// does not carry. See [resolver.scopeContextValues].
type scopedResolution struct {
	res Resolution
	ctx map[string]string
}

// contextDistinguish reports whether two resources' scope-context values
// place them in different parts of the cloud, so that two instances sharing
// an identity string are two objects rather than one.
//
// The rule is regionsDistinguish's, generalized: a name only ever rules a
// pair OUT, and only when BOTH sides resolved it and the two values differ.
// A name one side did not resolve is absent from that side's map and is a
// wildcard, exactly as an unknown region is (#217) - "cannot disambiguate,
// so don't". Nothing here can turn a collision this package would otherwise
// report into silence on the strength of a value it failed to read.
func contextDistinguish(a, b map[string]string) bool {
	for name, av := range a {
		if bv, resolved := b[name]; resolved && av != bv {
			return true
		}
	}
	return false
}

// scopeContextValues is the part of a resource's cloud location that
// [resolver.resourceCloudScope] structurally cannot reach: the values of
// the identity-schema attributes this run's identity string does not carry,
// read from the resource's own body.
//
// GitHub issue #200. A GCP project is to google what an account is to aws,
// and the google provider exposes it as an ordinary per-resource `project`
// argument rather than as a provider alias, so providerscope sees one
// provider configuration for resources in three different projects. The
// identity string does not carry it either: `project` is an
// optional-for-import attribute, [identityCandidates] classifies it as
// context, and [SynthesizeTypeIdentity] therefore builds an entry whose
// only component is the required attribute - a google_project_service's
// `service`. Three module instantiations enabling the same API in three
// separate projects then resolve to the identical identity and read as a
// duplicate-marker collision, which is a refusal over a configuration that
// works.
//
// Nothing here names a provider or a resource type. The names come from the
// provider's own identity schema minus whatever the resolved entry already
// says the identity is made of ([TypeIdentity.namedAttrs]), so a type whose
// entry already carries an attribute - every hand-written row with a
// [CloudRegion] component, for one - contributes it to the identity and not
// to the scope, and is compared exactly as it was before this existed. The
// same derivation gives aws `account_id`, google `zone` and `location`, and
// whatever the next provider calls the same idea.
//
// The value is read through the ordinary [resolver.resolveExpr] path rather
// than a static evaluation, which is the other half of the fix: the
// argument here is `project = google_project.environment_project.project_id`,
// a reference to a sibling whose own identity already resolved to a literal
// (#220's path, reached through [resolver.parentPart]). A static evaluation
// sees only a managed-resource reference and gives up.
//
// Two properties this must keep, and both come from being called after the
// walk rather than during it ([resolver.checkCollisions] is the only
// caller). Every instance is already resolved and memoized, so a nested
// [resolver.instance] call here raises no diagnostic that the walk has not
// already raised - which is what makes discarding this probe's diagnostics
// safe. And discarding them is required: a `project` argument that does not
// resolve is a scope this run cannot sharpen, never a reason to refuse a
// configuration, exactly as [resolver.staticRegionAttr] treats a `region`
// argument it cannot read.
//
// nil whenever the run has no schemas, the type has none, the identity
// schema names nothing the identity does not already carry, or the body
// sets none of them - in every one of which cases collision detection is
// exactly what it was before.
func (r *resolver) scopeContextValues(res Resolution) map[string]string {
	names := r.scopeContextNames(res.Type())
	if len(names) == 0 {
		return nil
	}
	if !r.enterModuleFor(res.Addr.Module) {
		return nil
	}
	rc := r.mod.ResourceByAddr(res.Addr.Resource.Resource)
	if rc == nil {
		return nil
	}
	exp, ok := r.expansionFor(rc)
	if !ok {
		return nil
	}
	scope := exp.scope(res.Addr.Resource.Key)

	want := make([]hcl.AttributeSchema, 0, len(names))
	for _, name := range names {
		want = append(want, hcl.AttributeSchema{Name: name})
	}
	content, _, diags := rc.Config.PartialContent(&hcl.BodySchema{Attributes: want})
	if diags.HasErrors() {
		return nil
	}

	var out map[string]string
	for _, name := range names {
		attr, set := content.Attributes[name]
		if !set {
			continue
		}
		val, ok := r.probeString(attr.Expr, scope, r.identifier(res.Addr, name, attr.Range))
		if !ok || val == "" {
			// An empty string is a value the configuration wrote, but it
			// is not a place to put an object under - the same distinction
			// [resolver.cloudValueFor] already draws for `region = ""` -
			// so it stays unresolved and keeps behaving as a wildcard.
			continue
		}
		if out == nil {
			out = make(map[string]string, len(names))
		}
		out[name] = val
	}
	return out
}

// scopeContextNames is [resolver.scopeContextValues]' per-type half: which
// attributes of the provider's identity schema for typeName are scope
// rather than identity, memoized because it is asked once per resolved
// instance and answered once per type.
//
// The subtraction is what makes this safe to widen: an attribute the
// resolved entry already mentions on either side ([TypeIdentity.namedAttrs]
// - a component's argument or an identity attribute another resource may
// read back) is already in the identity string every comparison starts
// from, so counting it again as scope would change nothing. What is left is
// exactly the identity-schema attributes this run's identity does not
// carry, and every one of them narrows which live object an instance means.
//
// An attribute the type's own configuration schema does not declare is
// dropped: the body cannot state it, so nothing could ever be read for it.
func (r *resolver) scopeContextNames(typeName string) []string {
	if names, asked := r.scopeCtx[typeName]; asked {
		return names
	}
	names := r.deriveScopeContextNames(typeName)
	r.scopeCtx[typeName] = names
	return names
}

func (r *resolver) deriveScopeContextNames(typeName string) []string {
	if r.schemas == nil {
		return nil
	}
	schema, served := r.schemas[typeName]
	if !served || schema.IdentitySchema == nil || schema.Block == nil {
		return nil
	}
	entry, ok := r.lookupType(typeName)
	if !ok {
		return nil
	}
	named := entry.namedAttrs()

	required, optional := identityAttrs(schema.IdentitySchema)
	var out []string
	for _, name := range append(append([]string(nil), required...), optional...) {
		if named[name] {
			continue
		}
		if arg, declared := schema.Block.Attributes[name]; !declared || arg == nil {
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// probeString resolves an expression to a string for a question no refusal
// hangs on: whether two resources are in the same place. It is
// [resolver.resolveExpr] with both of its side effects undone - the
// diagnostics it recorded, and whatever module the reference walk left the
// resolver pointing at.
//
// All four module fields are saved and restored, not three:
// [resolver.enterModuleAt] sets mod, curCfg, modInst and eval together, and
// a restore that puts back a subset leaves the resolver reading one
// module's resources through another's static evaluator. That defect has
// already been shipped once in this package, in namedLeaf's var branch.
//
// A part that is not a literal - a promise to read a parent's live ID later
// - is not a string this run has, so the whole probe fails rather than
// contributing a partial value that would compare unequal to itself.
func (r *resolver) probeString(expr hcl.Expression, scope instScope, ident configs.StaticIdentifier) (string, bool) {
	mark := len(r.diags)
	sibMark := len(r.pendingSiblingApply)
	mod, curCfg, modInst, eval := r.mod, r.curCfg, r.modInst, r.eval
	parts, ok := r.resolveExpr(expr, scope, ident)
	r.mod, r.curCfg, r.modInst, r.eval = mod, curCfg, modInst, eval
	r.diags = r.diags[:mark:mark]
	// The diagnostics this probe raised are gone, so the indexes any
	// sibling-apply refusal recorded into them are gone too. Dropping the
	// refusals with them is what stops a later withdrawal from removing a
	// diagnostic that is now something else entirely.
	r.pendingSiblingApply = r.pendingSiblingApply[:sibMark]
	if !ok {
		return "", false
	}
	var buf strings.Builder
	for _, part := range parts {
		if part.Parent != nil {
			return "", false
		}
		buf.WriteString(part.Literal)
	}
	return buf.String(), true
}

func newResolver(ctx context.Context, cfg *configs.Config, rctx Context) *resolver {
	dataIndex, badResults := buildDataResultsIndex(rctx.DataResults)
	// [Context.ManagedResults] joins the same index. The two cannot collide:
	// a data resource's [addrs.Resource.String] carries the "data." prefix
	// and a managed one does not, so one map holds both without either
	// shadowing the other, and the static evaluator's own lookup takes an
	// [addrs.Resource] that already says which mode it means.
	mgIndex, badManaged := buildResultsIndex(rctx.ManagedResults, addrs.ManagedResourceMode)
	if len(mgIndex) > 0 && dataIndex == nil {
		dataIndex = make(dataResultsIndex, len(mgIndex))
	}
	for modKey, byRes := range mgIndex {
		if dataIndex[modKey] == nil {
			dataIndex[modKey] = make(map[string]cty.Value, len(byRes))
		}
		for resKey, val := range byRes {
			dataIndex[modKey][resKey] = val
		}
	}
	r := &resolver{
		ctx:         ctx,
		rootCfg:     cfg,
		cloud:       rctx.Cloud,
		schemas:     rctx.Schemas,
		scope:       rctx.Scope,
		recordStore: recordStoreConfiguredIn(cfg),
		selection:   SelectionFor(cfg),
		secrets:     SecretsFor(cfg),
		dataIndex:   dataIndex,
		// Measured on the index rather than on len(rctx.ManagedResults) so
		// that an entry the index could not use - already reported as a
		// caller error just below - does not switch the classification on.
		managedResults: len(mgIndex) > 0,
		expansions:     make(map[string]*expansion),
		expFailed:      make(map[string]bool),
		expVisit:       make(map[string]bool),
		insts:          make(map[string]Resolution),
		instFailed:     make(map[string]bool),
		instVisit:      make(map[string]bool),
		synth:          make(map[string]*TypeIdentity),
		scopeCtx:       make(map[string][]string),
	}
	// A result the index could not use is the calling code's defect, not
	// the configuration's, and it must not vanish: dropped silently it
	// would resurface as the generic dynamic-value refusal pointing the
	// user at their own file.
	for _, key := range badResults {
		r.diags = r.diags.Append(unusableResult("data-read", "data resource", key))
	}
	for _, key := range badManaged {
		r.diags = r.diags.Append(unusableResult("managed-read", "managed resource", key))
	}
	r.enterModule(cfg)
	return r
}

// SummaryUnusableDataResult is the caller-error refusal for a
// [Context.DataResults] or [Context.ManagedResults] entry resolution cannot
// index. See [newResolver]. One summary covers both seams because the defect
// is the same one - a caller handed in a key this package cannot address -
// and only the detail differs, which is where the two are told apart.
const SummaryUnusableDataResult = "Unusable data-source result"

// unusableResult is the diagnostic for one such entry. phase and kind name
// which seam the entry arrived through, so the message points at the calling
// code that produced it rather than at whichever seam happens to be older.
func unusableResult(phase, kind, key string) tfdiags.Diagnostic {
	return tfdiags.Sourceless(
		tfdiags.Error,
		SummaryUnusableDataResult,
		fmt.Sprintf(
			"The %s phase handed resolution a result under %q, which is not usable: it must be an absolute %s instance address, and one resource's instances must share one key kind with no gaps. This is a defect in the calling code, not in the configuration.",
			phase, key, kind),
	)
}

type resolver struct {
	ctx context.Context

	// rootCfg is the configuration [Resolve] was given, kept so that a
	// parent-derived reference or a resolved instance address can look up
	// any node of the static module tree by its module path. See
	// [resolver.enterModuleFor].
	rootCfg *configs.Config

	// mod, curCfg, modInst and eval are the module currently being worked
	// on: the module whose resources [resolver.expansionFor] and
	// [resolver.resolveInstance] are reading. They are mutated by
	// [resolver.enterModule] as the walk moves between modules, so nothing
	// in this package may cache them across a call that might change
	// modules. curCfg is mod's own *configs.Config node, kept alongside it
	// only because [providerscope.Resolve] needs the Config (for its
	// Parent/Path module-tree walk) rather than the bare Module - see
	// [resolver.resourceCloudScope].
	mod     *configs.Module
	curCfg  *configs.Config
	modInst addrs.ModuleInstance
	eval    *configs.StaticEvaluator

	cloud CloudContext
	diags tfdiags.Diagnostics

	// schemas are the provider's resource type schemas when the caller had
	// them, and nil when it did not. See [Context.Schemas].
	schemas map[string]providers.Schema

	// scope is which resource blocks -target / -exclude leaves in the plan
	// graph, and nil when the run is untargeted. See [Context.Scope].
	scope Scope

	// recordStore is whether the root module's live block declares a
	// record_store, which is what admits [ClassRecordLocated] (issue #270)
	// and, before it, [ClassRecordBacked].
	//
	// It is read from the configuration here rather than taken from
	// [Context] on purpose: internal/live/lint already reads exactly this
	// fact from exactly this place (its recordStoreConfiguredIn), the two
	// must never disagree about whether a type is admitted, and a Context
	// field would be one more thing four call sites could forget to pass.
	// The configuration is the authority and both layers ask it directly.
	recordStore bool

	// selection is the root module's `markers "record"` selection: the
	// resources an operator has told this run to hold in the record store
	// instead of marking (GitHub issue #365, HANDOFF.md's third principle).
	// Nil when the configuration declares none, which is every configuration
	// written before the block existed.
	//
	// Read from the configuration here for [resolver.recordStore]'s reason,
	// and through the same function internal/live/lint, internal/live/stamp
	// and internal/live/projection read it with - see [SelectionFor]. The
	// four must agree instance for instance: a resource this resolver routes
	// to a record while stamp still marks it is merely redundant, but a
	// resource stamp declines to mark while this resolver classifies it
	// needs-discovery is created unfindable.
	selection *strict.Selection

	// secrets is the root module's `strict { secrets = ... }` setting,
	// GitHub issue #365's first principle turned into a toggle. Read from
	// the configuration here for [resolver.recordStore]'s reason and through
	// the same function internal/live/lint and internal/live/projection read
	// it with (see [SecretsFor]); an omitted argument, an absent strict block
	// and an absent live block all resolve to [strict.DefaultSecrets].
	//
	// It reaches exactly one decision in this package - a
	// [TypeIdentity.SecretMaterial] row's classification - and that decision
	// is a refusal rather than a different identity. Nothing here resolves
	// to a DIFFERENT object under one setting than under the other, which is
	// what keeps it out of the safety rule's way.
	secrets strict.Secrets

	// dataIndex is [Context.DataResults] regrouped per module instance and
	// resource, and nil when the caller passed none. See
	// [buildDataResultsIndex].
	dataIndex dataResultsIndex

	// managedResults is whether the caller supplied any
	// [Context.ManagedResults] at all. It is the first condition of the
	// provenance discriminator in managedprovenance.go and the reason that
	// whole classification is inert for a run with no provider on the line -
	// which is every offline corpus run, and which is what keeps #183's
	// cohort of unset root variables where it is.
	//
	// A separate field rather than a scan of dataIndex: the two seams share
	// one index (see [newResolver]), so "the index has entries" cannot
	// answer "the caller performed a managed read".
	managedResults bool

	// pendingSiblingApply records the identity arguments this instance could
	// not compute BECAUSE a managed result this run obtained is unknown until
	// a sibling resource is applied, together with the index of the
	// diagnostic each one raised. See [resolver.stringValue] and
	// [DiscoverySiblingApply]; [resolver.resolveInstance] consumes it.
	pendingSiblingApply []siblingApplyRefusal

	// signal is the whole configuration's naming signal, collected before
	// classification starts because the schema fallback's verdict depends on
	// it. Nil for the signal-only walk of [ScanConfig], which classifies
	// nothing.
	signal *ConfigSignal

	// synth memoizes the schema fallback per type, including its refusals: a
	// nil entry means "asked, and the schemas do not describe this type well
	// enough". See [SynthesizeTypeIdentity].
	synth map[string]*TypeIdentity

	// scopeCtx memoizes, per type, which of the provider identity schema's
	// own attributes are scope rather than identity for that type - the
	// ones [resolver.checkCollisions] reads to tell two resources apart
	// when the identity string alone cannot. See
	// [resolver.scopeContextNames]. A nil entry means "asked, and there
	// are none", which is the common case and must not be re-derived.
	scopeCtx map[string][]string

	// Tolerant module-output memo, keyed by the child module INSTANCE whose
	// outputs were evaluated. tolerantOutBusy is the same key set while an
	// evaluation is in flight, which is what stops a module whose own
	// argument reads a sibling module's output from re-entering itself
	// through [resolver.moduleOutputsLookup]. A cty.NilVal entry means
	// "asked, and this call cannot be answered", which must not be
	// re-derived either. See tolerantmodule.go.
	tolerantOut     map[string]cty.Value
	tolerantOutBusy map[string]bool

	// Expansion memo, keyed by the module instance and the resource address
	// (no instance key) - two resource blocks with the same local address in
	// different modules must not share an entry.
	expansions map[string]*expansion
	expFailed  map[string]bool
	expVisit   map[string]bool

	// Instance resolution memo, keyed by absolute instance address, which is
	// already module-qualified.
	insts      map[string]Resolution
	instFailed map[string]bool
	instVisit  map[string]bool

	// curInstanceAddr is the resource instance whose identity
	// resolveInstance is currently building, in
	// [addrs.AbsResourceInstance.String] form - empty outside any
	// resolveInstance call. Every diagnostic raised while it is set is
	// tagged with it as an [InstanceFailure] (see [resolver.errorf] and
	// [resolver.appendDiags]), so a caller can tell which instance a
	// diagnostic belongs to without parsing its rendered text. Set on entry
	// to resolveInstance and restored on exit (see resolveInstance's own
	// defer), the same save/restore discipline [resolver.enterModuleAt]
	// already uses for r.mod/r.modInst - required because resolveInstance
	// recurses into another resolveInstance call for every parent a
	// component references ([resolver.parentPart] -> [resolver.instance]),
	// and a diagnostic raised inside that nested call belongs to the
	// parent, not to whichever instance is waiting on it. See #221.
	curInstanceAddr string
}

// enterModule points the resolver at one node of the static module tree,
// with its self instance ([ModuleInstance]) as the module instance: the
// unkeyed reading that is lossless for the root and for a static module
// call, and the entry point for every caller that has not been told a more
// specific instance to use. See [resolver.enterModuleAt].
func (r *resolver) enterModule(cfg *configs.Config) {
	r.enterModuleAt(cfg, ModuleInstance(cfg))
}

// enterModuleAt points the resolver at one node of the static module tree
// as one specific instance of it, setting the module, that instance path,
// and its own static evaluator. Every entry point that is about to read
// [resolver.mod] or [resolver.eval] calls this (or [resolver.enterModule])
// first, rather than trusting whatever a previous call left behind.
//
// modInst is taken as given, never recomputed from cfg.Path: cfg.Path names
// the module CALL, and a for_each'd call's several instances all share one
// *configs.Config node, so cfg.Path alone cannot tell two of them apart (59c,
// keyed for_each on module blocks). [resolver.walkModule] is what supplies a
// keyed instance; every other caller passes the same instance
// [ModuleInstance] would compute, through [resolver.enterModule].
func (r *resolver) enterModuleAt(cfg *configs.Config, modInst addrs.ModuleInstance) {
	r.mod = cfg.Module
	r.curCfg = cfg
	r.modInst = modInst
	// Pure on purpose: an identity is a claim about which cloud object a
	// block owns, and a function that answers differently every time it is
	// called cannot make that claim. See impure.go.
	r.eval = cfg.Module.StaticEvaluator.Pure()
	// Data-read results, when the caller performed the pre-resolution read
	// phase: the evaluator answers exactly the data references the phase
	// covered in this module instance, and refuses everything else exactly
	// as before. See [Context.DataResults].
	if lookup := r.dataLookupFor(modInst); lookup != nil {
		r.eval = r.eval.WithDataResults(lookup)
	}
	// The caller's own argument expressions, re-evaluated in the scope the
	// module CALL actually has, so that a var.* reference this module
	// reaches through a function call - anything [resolver.resolveExpr]
	// does not decompose down to a bare traversal, and so never puts in
	// front of [resolver.namedDef] - sees the call's own
	// each.key/each.value/count.index instead of the load-time closure's
	// nothing. Nil for a module path with no repeating call on it, and nil
	// whenever that scope cannot be proven, either of which leaves the
	// module tree's frozen closure in place exactly as before. See
	// [resolver.callerVariables] (modulevars.go) and #252.
	if vars := r.callerVariables(modInst); vars != nil {
		r.eval = r.eval.WithVariables(vars)
	}
}

// enterModuleFor is [resolver.enterModuleAt] by module instance path, for
// the call sites - resolving a reference's parent, most of all - that have
// an address rather than a *configs.Config in hand. It reports false when
// the path names no module in this configuration's tree.
//
// The instance it enters is modInst exactly as given, keys included:
// [ConfigForModule] ignores keys on the way down to find the right
// *configs.Config (one node per call, not per instance, same reason
// [resolver.enterModuleAt] gives), but the resolver's own idea of "which
// instance is this" must not lose them, or two different instances of one
// for_each'd module would share an expansion and instance memo key (see
// [resolver.expKey]) and each would silently return the other's answer.
func (r *resolver) enterModuleFor(modInst addrs.ModuleInstance) bool {
	cfg, ok := ConfigForModule(r.rootCfg, modInst)
	if !ok || cfg.Module == nil {
		return false
	}
	r.enterModuleAt(cfg, modInst)
	return true
}

// expKey builds the expansion memo's key: the module instance a resource
// block belongs to, plus its own address within that module.
func (r *resolver) expKey(rc *configs.Resource) string {
	return r.modInst.String() + "\x00" + rc.Addr().String()
}

// lookupType is [LookupType] with the schema fallback behind it: a type the
// hand table does not cover still resolves when the provider's own identity
// schema describes it completely and the caller supplied the schemas. A
// caller that supplied none gets [LookupType] exactly.
func (r *resolver) lookupType(typeName string) (TypeIdentity, bool) {
	if entry, ok := LookupType(typeName); ok {
		return entry, true
	}
	if memo, asked := r.synth[typeName]; asked {
		if memo == nil {
			return TypeIdentity{}, false
		}
		return *memo, true
	}

	entry, ok := SynthesizeTypeIdentity(typeName, r.schemas, r.signal)
	if !ok {
		r.synth[typeName] = nil
		return TypeIdentity{}, false
	}
	r.synth[typeName] = &entry
	return entry, true
}

// instance resolves one managed resource instance, memoizing the result.
// rng is the source range to blame for the request: the resource's own
// declaration when called from the top-level walk, or the referencing
// expression when called for a parent.
func (r *resolver) instance(addr addrs.AbsResourceInstance, rng hcl.Range) (Resolution, bool) {
	key := addr.String()
	if res, ok := r.insts[key]; ok {
		return res, true
	}
	if r.instFailed[key] {
		return Resolution{}, false
	}
	if r.instVisit[key] {
		r.errorf(rng, "Circular identity reference",
			"The identity of %s depends on itself, directly or through other resources. Identity cannot be resolved for a cycle.", key)
		r.instFailed[key] = true
		return Resolution{}, false
	}
	r.instVisit[key] = true
	defer delete(r.instVisit, key)

	// diagMark is where addr's own attempt starts, so a failure below can
	// be handed to [resolver.markerFallback] - GitHub issue #289's second
	// chance for a type the marker already answers. It is captured here,
	// once, rather than inside resolveInstance, so the window covers every
	// diagnostic that call raises - including "Circular identity
	// reference" from a re-entrant call to THIS function for the same key,
	// which resolveInstance itself never sees.
	diagMark := len(r.diags)
	res, ok := r.resolveInstance(addr, rng)
	if !ok {
		if fb, fbOK := r.markerFallback(addr, diagMark); fbOK {
			res, ok = fb, true
		}
	}
	if !ok {
		r.instFailed[key] = true
		return Resolution{}, false
	}
	r.insts[key] = res
	return res, true
}

// recordFallback is [resolver.resolveInstance]'s second chance for an
// instance whose own identity component could not be folded from
// configuration this run: converting what would otherwise be a
// [ClassNeedsDiscovery] resolution into [ClassRecordLocated] when the type
// has nowhere to carry a marker either, so a discovery sweep would never
// bind the instance and a record is the only identity source left that is
// not a guess. See [RecordFallbackType] for the conditions and for why
// this is the type-admitted door into the class GitHub issue #270 opened
// for markerless types.
//
// Every call site calls this ahead of building its own ClassNeedsDiscovery
// resolution, and only takes the fallback when it returns true - a run
// with no record_store, or a type this predicate does not clear, falls
// through to the discovery answer exactly as it always has.
func (r *resolver) recordFallback(addr addrs.AbsResourceInstance, resourceType string) (Resolution, bool) {
	if !r.recordStore {
		return Resolution{}, false
	}
	if !RecordFallbackType(resourceType, r.schemas) {
		return Resolution{}, false
	}
	return Resolution{Addr: addr, Class: ClassRecordLocated}, true
}

func (r *resolver) resolveInstance(addr addrs.AbsResourceInstance, rng hcl.Range) (Resolution, bool) {
	resAddr := addr.Resource.Resource

	// Every diagnostic raised from here down - directly, or through a
	// nested r.instance call resolving a DIFFERENT instance a component
	// references - gets tagged with whichever instance owns the call frame
	// at the moment it fires (see [resolver.curInstanceAddr],
	// [resolver.errorf], [resolver.appendDiags]). Restored on return so a
	// diagnostic raised after this call unwinds - by whichever instance
	// called it, or by nothing at all - is not misattributed to addr. See
	// #221.
	prevInstanceAddr := r.curInstanceAddr
	r.curInstanceAddr = addr.String()
	defer func() { r.curInstanceAddr = prevInstanceAddr }()

	if !r.enterModuleFor(addr.Module) {
		r.errorf(rng, "Reference to a module instance that does not exist",
			"%s is in %s, which is not part of this configuration's static module tree, so its identity cannot be resolved.",
			addr.String(), addr.Module.String())
		return Resolution{}, false
	}

	rc := r.mod.ResourceByAddr(resAddr)
	if rc == nil {
		r.errorf(rng, "Reference to undeclared resource",
			"%s is not declared in this configuration, so its identity cannot be resolved.", resAddr.String())
		return Resolution{}, false
	}

	// The referenced instance key has to be one this resource actually
	// expands to; otherwise a reference like aws_subnet.this.id (whole
	// resource, no key) would silently resolve against a nonexistent
	// NoKey instance.
	exp, ok := r.expansionFor(rc)
	if !ok {
		return Resolution{}, false
	}
	if !exp.hasKey(addr.Resource.Key) {
		r.errorf(rng, "Reference to a resource instance that does not exist",
			"%s does not exist. %s", addr.String(), exp.describe(resAddr))
		return Resolution{}, false
	}

	// GitHub issue #270, ahead of r.lookupType and therefore ahead of the
	// schema fallback inside it, for the reason internal/live/lint's
	// admission.go gives for putting the markerless veto in the same
	// position: [SynthesizeTypeIdentity] would re-admit some of these types
	// from the provider's identity schema alone, which is plan-and-create-
	// only support for a type whose whole problem is that no later run can
	// find the object again. Consulted after the fallback this branch would
	// never run for the types it exists to serve.
	//
	// It cannot shadow a ratified row: [LocatedType]'s first two conditions
	// are membership in [MarkerlessTypes] and absence from the table, and
	// the two sets are disjoint. It cannot run without a record_store
	// either, which is what makes the ordering safe - an estate that has
	// not migrated reaches the refusal below exactly as before.
	if r.recordStore && LocatedType(resAddr.Type, r.schemas) {
		return Resolution{
			Addr:  addr,
			Class: ClassRecordLocated,
		}, true
	}

	// The same class by the other door: an operator's `markers = record`
	// selection (GitHub issue #365, HANDOFF.md's third principle). Above,
	// the type has nowhere to carry a marker; here it has somewhere and the
	// operator has spent it - a tag budget, a tag policy - and asked for the
	// identity to live in the record store instead.
	//
	// It sits after the automatic route rather than before it so that a type
	// both would admit takes the same path it takes today, and it is the
	// same [ClassRecordLocated] rather than a class of its own because the
	// downstream mechanism is identical: internal/live/projection's
	// materializeLocated reads the identity out of the store and hands the
	// instance on as an ordinary import. Adding a second class would have
	// been a second mechanism to keep correct.
	//
	// [SelectedLocatedRefusal] is what keeps this safe, and what it does NOT
	// skip is the point: a selected type still has to be importable and to
	// have an identity a record can hold in full. internal/live/lint has
	// already refused the configuration when either fails, with the
	// condition named; this call is the same predicate asked again at the
	// layer that acts, so a caller that skipped lint cannot get a wrong
	// identity out of it - it gets today's classification instead.
	if r.recordStore && r.selection.Selects(addr.ConfigResource()) && SelectedLocatedType(resAddr.Type, r.schemas) {
		return Resolution{
			Addr:  addr,
			Class: ClassRecordLocated,
		}, true
	}

	entry, ok := r.lookupType(resAddr.Type)
	if !ok {
		// This used to interpolate strings.Join(AdmittedTypes(), ", "),
		// which was reasonable when the table held a few dozen rows and
		// renders a 25KB diagnostic at the table's 800-plus rows. It also cited
		// "the roadmap", a document that is not in this repository - the
		// only text carrying that phrase is live/LIMITATIONS.md quoting it.
		//
		// This is lint.go's unadmitted-type refusal seen from the
		// resolution layer, and the two are meant to read as one rule, so
		// the wording tracks it. Found by the #101 message audit.
		r.errorf(rng, "Resource type outside the live-markers subset",
			"There is no identity knowledge for resource type %q, so %s cannot be admitted to a live-markers projection. "+
				"A type participates only if its identity is recoverable from the live system with no memory. "+
				"The table is generated from ratified identity rows and is not extensible from here; "+
				"see live/LIMITATIONS.md, \"unadmitted-type\".%s",
			resAddr.Type, addr.String(), r.schemaRefusal(resAddr.Type))
		return Resolution{}, false
	}

	if entry.ServerAssigned {
		res := Resolution{
			Addr:   addr,
			Class:  ClassNeedsDiscovery,
			Reason: entry.Reason,
			Cause:  DiscoveryServerAssigned,
		}
		// The identity is still the provider's to mint - the class and the
		// empty ImportID both stay - but for a row carrying
		// [TypeIdentity.UniqueName] whose name this configuration actually
		// states, the object is recognisable in a listing without a marker.
		// See [DiscoveryUniqueName] for why that is a different answer, and
		// [resolver.uniqueNameValue] for what it refuses.
		if name, ok := r.uniqueNameValue(rc, entry, exp.scope(addr.Resource.Key), addr); ok {
			res.Cause = DiscoveryUniqueName
			res.CauseArgs = append([]string(nil), entry.UniqueName.Attrs...)
			res.UniqueName = name
			return res, true
		}
		if fb, ok := r.recordFallback(addr, resAddr.Type); ok {
			return fb, true
		}
		return res, true
	}

	if entry.RecordBacked {
		// The one thing this class's record can hold that an operator may
		// have said no to: secret material (GitHub issue #365).
		// internal/live/lint has already refused such a configuration with
		// the setting named, so this is the same question asked again at the
		// layer that acts - the shape [SelectedLocatedType] already has, and
		// here for the stronger reason. Every other gate in this file
		// protects an IDENTITY; this one protects a value that would be
		// written into the estate's record store in clear, and a caller that
		// skipped lint must not be able to reach that by skipping lint.
		//
		// It is an error rather than a fall-through because there is nothing
		// to fall through to: this type has no cloud identity at any rung,
		// so the honest answer is the refusal, with the setting named.
		if entry.SecretMaterial && !strict.StoresSecrets(r.secrets) {
			r.errorf(rng, "Secret-generating resource refused",
				"%s is a %q, whose whole prior state lives in this estate's record store - and whose schema carries "+
					"secret material, so that record would hold the secret in clear. This estate's live block sets "+
					"strict { secrets = %q }, which is HANDOFF.md's \"no secrets stored by the tool\" principle, so it "+
					"is refused instead. Remove that argument to get the default, %q, which keeps the value the way a "+
					"stock OpenTofu state file keeps it; or generate and store the secret in a secret manager and have "+
					"configuration reference it by ARN or path. See live/LIMITATIONS.md, \"strict-secrets\".",
				addr.String(), resAddr.Type, r.secrets, strict.DefaultSecrets)
			return Resolution{}, false
		}
		// No cloud identity to build, ever - the whole point of this class.
		// See [ClassRecordBacked] and [TypeIdentity.RecordBacked]. A
		// resolution this shallow is safe here only because a
		// RECORD_ADMITTED type never reaches resolution at all unless a
		// live block's record_store is configured (internal/live/lint's
		// checkManagedResources), which is the caller's gate, not this
		// package's.
		return Resolution{
			Addr:  addr,
			Class: ClassRecordBacked,
		}, true
	}

	attrs, ok := r.identityArgs(rc, entry)
	if !ok {
		return Resolution{}, false
	}

	scope := exp.scope(addr.Resource.Key)

	// Computed here rather than at the successful return below because the
	// region it carries is also this resource's answer for a
	// [CloudRegion] component: see [resolver.cloudValueFor].
	cloudScope := r.resourceCloudScope(rc, scope)

	// Cloud properties are checked before anything else in the body is
	// interpreted, so that a type this run cannot name gets the one honest
	// answer - "the account is not known here" - rather than an error about
	// some argument that would have been fine had the account been known.
	// The body is decoded first only so that a component carrying BOTH a
	// cloud property and the argument the provider documents as defaulting
	// to it (a Glue catalog_id defaults to the caller's own account) can be
	// answered by the configuration instead: see [Component.Attrs] on a
	// cloud-bearing component, and cloudComponentAttr below.
	if comp, ok := r.missingCloudComponent(entry, attrs, scope, addr, cloudScope); ok {
		if fb, ok := r.recordFallback(addr, resAddr.Type); ok {
			return fb, true
		}
		return Resolution{
			Addr:   addr,
			Class:  ClassNeedsDiscovery,
			Reason: cloudReason(entry, comp.Cloud),
			Cause:  DiscoveryCloudUnknown,
			// The cloud property first, then the arguments the provider
			// documents as defaulting to it - which is what the
			// configuration can set instead, and is empty for a component
			// that has no such argument. See [DiscoveryCloudUnknown].
			CauseArgs: append([]string{string(comp.Cloud)}, comp.Attrs...),
		}, true
	}

	var parts []Part
	// The same pieces again, split by the identity attribute each component
	// supplies. It is what makes an import ask the provider for
	// {"role": …, "policy_arn": …} rather than for the two joined by a "/"
	// that only this table knows about. Attributes are kept in component
	// order so that a rendered formula reads the way the import syntax does.
	byAttr := make(map[string][]Part)
	var attrOrder []string
	addTo := func(name string, got []Part) {
		if name == "" {
			return
		}
		if _, seen := byAttr[name]; !seen {
			attrOrder = append(attrOrder, name)
		}
		byAttr[name] = append(byAttr[name], got...)
	}

	// failed tracks whether any real component below could not be resolved.
	// The loop no longer returns on the first one (GitHub issue #221): every
	// component is still evaluated even after one fails, so a second,
	// independently-broken component raises its own diagnostic instead of
	// staying invisible behind the first. internal/live/check's cascade
	// fixpoint reclassifies a dependent as data-read-eligible only when
	// EVERY diagnostic tagged with this instance's address traces back to
	// one - which requires every failing component to have actually been
	// reached and diagnosed, not just the first.
	failed := false
	// The sibling-apply bookkeeping (issue #187, [DiscoverySiblingApply]).
	// sibMark is where this instance's own refusals start, so a nested
	// resolution's entries are never read as this one's; hardFailed records
	// that at least one component failed for a reason a sibling's apply will
	// NOT settle, which is what keeps a genuinely broken argument a refusal
	// rather than turning it into a discovery.
	sibMark := len(r.pendingSiblingApply)
	hardFailed := false
	var sibArgs []string
	fail := func(sibBefore int, argName string) {
		failed = true
		if len(r.pendingSiblingApply) == sibBefore {
			hardFailed = true
			return
		}
		if argName != "" {
			sibArgs = append(sibArgs, argName)
		}
	}
	for i, comp := range entry.Components {
		sibBefore := len(r.pendingSiblingApply)
		if comp.Cloud != CloudNone {
			// A cloud-bearing component may also carry the argument the
			// provider documents as defaulting to that same cloud property.
			// The configuration's own value wins when it has one, because
			// that is what the object will actually be named: a Glue
			// catalog_id pointed at another account's Data Catalog names
			// that catalog, not this run's account. The cloud value is the
			// documented fallback for the omitted case, and
			// missingCloudComponent has already refused the case where neither
			// side has anything to offer.
			if attr := r.cloudComponentAttr(comp, attrs, scope, addr); attr != nil {
				got, ok := r.resolveExpr(attr.Expr, scope, r.identifier(addr, attr.Name, attr.Range))
				if !ok {
					fail(sibBefore, attr.Name)
					continue
				}
				parts = append(parts, got...)
				addTo(comp.identityAttrFor(attr.Name), got)
				continue
			}
			v, _ := r.cloudValueFor(comp.Cloud, cloudScope)
			got := []Part{{Literal: v}}
			parts = append(parts, got...)
			addTo(comp.identityAttrFor(""), got)
			continue
		}
		if len(comp.Attrs) == 0 {
			got := []Part{{Literal: comp.Literal}}
			parts = append(parts, got...)
			addTo(comp.identityAttrFor(""), got)
			continue
		}
		attr, conflict := r.firstApplicablePresent(comp, attrs, scope, addr)
		if conflict != nil {
			// #384: two members of the same [Component.SoleElement]
			// alternation are both genuinely non-empty for this instance -
			// the configuration does not say which one determines the
			// object, and picking one anyway is exactly the wrong-marker
			// shape HANDOFF's safety rule exists to stop. The record rung
			// is the honest second chance, tried first and only where the
			// type actually has nowhere to carry a marker and a full
			// identity can be recorded (see [resolver.recordFallback]); it
			// is a no-op for anything else, same as every other call site
			// in this function.
			if fb, ok := r.recordFallback(addr, resAddr.Type); ok {
				return fb, true
			}
			r.errorf(attr.Range, "Ambiguous list-valued identity argument",
				"%s sets more than one of %s at once (both %q and %q carry a real value). "+
					"This component's identity can only be built when exactly one of them does; the AWS API - not this "+
					"configuration - decides how the two compose into live objects, so this package will not guess "+
					"which one names this instance.",
				addr.String(), orList(comp.Attrs), attr.Name, conflict.Name)
			fail(sibBefore, "")
			continue
		}
		if attr == nil {
			if comp.OmitIfAbsent {
				// The provider's own grammar says this segment - and any
				// Literal it carries as a prefix - is absent entirely when
				// none of Attrs is set, not merely unknown. See
				// [Component.OmitIfAbsent]. Contribute nothing to the
				// import-ID string and nothing to any identity-attribute
				// formula, and move on to the next component.
				continue
			}
			if comp.Default != "" {
				// The provider documents what omission means for this
				// argument (an omitted event_bus_name is the "default"
				// bus), so the identity is computable without it - see
				// [Component.Default].
				got := []Part{{Literal: comp.Default}}
				parts = append(parts, got...)
				addTo(comp.identityAttrFor(comp.Attrs[0]), got)
				continue
			}
			if comp.ServerAssignedIfAbsent {
				// The provider's own Argument Reference documents this
				// argument as one it fills in itself when the
				// configuration leaves it blank ("If omitted, Terraform
				// will assign a random, unique name" and its siblings -
				// see [Component.ServerAssignedIfAbsent] and #190). That
				// is the identical situation the *_prefix branch just
				// below handles for a different spelling of the same
				// convention: not a missing argument, but a name this run
				// cannot compute before the object exists.
				if fb, ok := r.recordFallback(addr, resAddr.Type); ok {
					return fb, true
				}
				return Resolution{
					Addr:  addr,
					Class: ClassNeedsDiscovery,
					Reason: fmt.Sprintf(
						"%s has no value for %s; the provider assigns one automatically when it is omitted, so the value is not known until the object exists.",
						addr.String(), orList(comp.Attrs)),
					Cause:     DiscoveryNameOmitted,
					CauseArgs: append([]string(nil), comp.Attrs...),
				}, true
			}
			if prefixAttr, base := firstPrefixSibling(attrs, comp.Attrs); prefixAttr != nil {
				// The block names the object through "<base>_prefix", not
				// "<base>" - a convention the provider documents across
				// dozens of types (aws_db_parameter_group, aws_iam_role,
				// aws_s3_bucket, aws_cloudwatch_log_group, and more) to mean
				// "assign a random suffix to this prefix at create time".
				// That is not a missing argument, the way this error reads
				// for every other component: it is a name this run cannot
				// compute before the object exists, the identical situation
				// [TypeIdentity.ServerAssigned] already gives its own
				// resolution class to at the whole-type level. See #190.
				if fb, ok := r.recordFallback(addr, resAddr.Type); ok {
					return fb, true
				}
				return Resolution{
					Addr:  addr,
					Class: ClassNeedsDiscovery,
					Reason: fmt.Sprintf(
						"%s is named through %s rather than %q; the provider appends a random suffix to the prefix at create time, so the resulting name is not known until the object exists.",
						addr.String(), prefixAttr.Name, base),
					Cause:     DiscoveryNamePrefix,
					CauseArgs: []string{base, prefixAttr.Name},
				}, true
			}
			r.errorf(rc.DeclRange, "Identity argument not set",
				"%s has no value for %s, so its import identity (%s) cannot be built.",
				addr.String(), orList(comp.Attrs), entry.ImportSyntax)
			fail(sibBefore, "")
			continue
		}
		// attr is present in the body, but the name/name_prefix convention
		// the [attr == nil] branch above handles for an OMITTED base
		// argument is just as common spelled through a conditional instead:
		// `name = var.use_prefix ? null : var.name` paired with
		// `name_prefix = var.use_prefix ? "${var.name}-" : null` (this is
		// terraform-aws-modules' own aws_iam_role shape, and #190's comment
		// on [resolver.identityArgs] already names the convention). attr is
		// syntactically present either way, so firstPresent alone cannot
		// tell "named through name_prefix" apart from "named", and without
		// this check attr's null VALUE would reach stringValue below and
		// raise "Null identity argument" - a hard, wrong refusal for a
		// resource that resolves to ClassNeedsDiscovery under every other
		// spelling of the identical convention.
		//
		// The peek below evaluates attr's expression without going through
		// stringValue, so it raises no diagnostic of its own: a peek that
		// itself fails (an unresolvable reference, say) is left alone, and
		// the ordinary resolveExpr path a few lines down reports it exactly
		// as it always has. Only a clean, wholly-known null redirects here.
		if prefixAttr, ok := attrs[attr.Name+"_prefix"]; ok {
			if peekVal, peekDiags := r.evalPure(attr.Expr, scope, r.identifier(addr, attr.Name, attr.Range)); !peekDiags.HasErrors() && peekVal.IsNull() {
				if fb, ok := r.recordFallback(addr, resAddr.Type); ok {
					return fb, true
				}
				return Resolution{
					Addr:  addr,
					Class: ClassNeedsDiscovery,
					Reason: fmt.Sprintf(
						"%s is named through %s rather than %q; the provider appends a random suffix to the prefix at create time, so the resulting name is not known until the object exists.",
						addr.String(), prefixAttr.Name, attr.Name),
					Cause:     DiscoveryNamePrefix,
					CauseArgs: []string{attr.Name, prefixAttr.Name},
				}, true
			}
		}
		ident := r.identifier(addr, attr.Name, attr.Range)
		expr := attr.Expr
		if comp.PerElement {
			// One segment per element, joined by whatever separator the
			// preceding component supplies. See [Component.PerElement].
			got, ok := r.perElementParts(expr, scope, attr, ident, precedingSeparator(entry.Components, i))
			if !ok {
				fail(sibBefore, attr.Name)
				continue
			}
			parts = append(parts, got...)
			addTo(comp.identityAttrFor(attr.Name), got)
			continue
		}
		diagMark := len(r.diags)
		var got []Part
		var ok, resolvedHere bool
		if comp.SoleElement {
			// GitHub issue #346: on the each.value-as-an-EXPRESSION route the
			// list construct is in the element, not in the argument, so the
			// one-element rule has to be applied one selection in - and it
			// resolves there too, because the narrowed expression belongs to
			// the caller's module rather than to this one, which is why this
			// returns parts instead of an expression the way the syntactic
			// narrowing below does. Not applicable to any other shape, which
			// falls through to that narrowing exactly as it always has. The
			// failure path is deliberately shared with resolveExpr's below,
			// so [Component.OmitIfAbsent] and [Component.Literal] mean the
			// same thing on both routes. See [resolver.eachValueSoleElement].
			if g, gotOK, applicable := r.eachValueSoleElement(expr, scope, attr, ident); applicable {
				got, ok, resolvedHere = g, gotOK, true
			} else {
				narrowed, narrowOK := r.soleElementExpr(expr, scope, attr, ident)
				if !narrowOK {
					fail(sibBefore, attr.Name)
					continue
				}
				expr = narrowed
			}
		}
		if !resolvedHere {
			got, ok = r.resolveExpr(expr, scope, ident)
		}
		if !ok {
			// [Component.OmitIfAbsent]'s omission is not only syntactic. A
			// for_each-driven module's ordinary way to say "this instance
			// has no qualifier" is `qualifier = try(each.value.qualifier,
			// null)` - the argument IS written, so the [attr == nil] branch
			// above never sees it, and resolveExpr above just ran the FULL
			// resolution machinery on it: resolveFallbackChain's try()
			// arm-selection (fallback.go), a conditional's branch selection,
			// or a bare literal, whichever shape the author wrote - and
			// concluded the value is null, which stringValue reports as
			// "Null identity argument". That is the identical wrong-refusal
			// shape the name/name_prefix redirect above exists to avoid, for
			// the identical reason: a value this package can prove is null
			// should not be reported as a value it could not resolve. Only a
			// failure that is EXACTLY that - nothing else went wrong along
			// the way - redirects here; any other diagnostic (a truly
			// dynamic value, an unresolvable reference) stands, because
			// "not known until apply" is a true statement about it and
			// "absent" is not.
			//
			// This runs BEFORE fail() on purpose. An omitted component is not
			// a failure at all, so it must not record sibling-apply
			// provenance - a component that contributes nothing has nothing
			// waiting on an apply.
			if comp.OmitIfAbsent && onlyNullIdentityArgument(r.diags[diagMark:]) {
				r.diags = r.diags[:diagMark]
				continue
			}
			fail(sibBefore, attr.Name)
			continue
		}
		// The identity-attribute formula gets the resolved value alone; a
		// prefix Literal (see [Component.Literal] and [Component.OmitIfAbsent])
		// is punctuation in the import-ID STRING, not part of the identity
		// ATTRIBUTE's value, so it is added to parts only, after the formula
		// has already captured the unprefixed value.
		addTo(comp.identityAttrFor(attr.Name), got)
		if comp.Literal != "" {
			got = append([]Part{{Literal: comp.Literal}}, got...)
		}
		parts = append(parts, got...)
	}
	if failed {
		// Issue #187's second half. Every component that failed did so for
		// one reason - a value this run's own managed results supplied, which
		// the provider leaves unknown until a sibling resource is applied -
		// and that is a different fact from an identity this configuration
		// cannot express. The refusals raised for it are withdrawn and the
		// instance is classified instead, so that the operator is told which
		// sibling it is waiting on rather than that their argument is not
		// static. See [DiscoverySiblingApply].
		if res, ok := r.siblingApplyResolution(addr, sibMark, sibArgs, hardFailed); ok {
			return res, true
		}
		r.pendingSiblingApply = r.pendingSiblingApply[:sibMark]
		return Resolution{}, false
	}
	r.pendingSiblingApply = r.pendingSiblingApply[:sibMark]

	res := classify(addr, coalesce(parts), attrFormulas(byAttr, attrOrder), entry.IdentityObjectOnly)
	res.cloudScope = cloudScope
	return res, true
}

// attrFormulas turns the per-attribute part lists into the ordered form a
// [Formula] carries, each one coalesced the way the whole import ID is.
func attrFormulas(byAttr map[string][]Part, order []string) []AttrFormula {
	if len(order) == 0 {
		return nil
	}
	out := make([]AttrFormula, 0, len(order))
	for _, name := range order {
		out = append(out, AttrFormula{Name: name, Parts: coalesce(byAttr[name])})
	}
	return out
}

// classify turns a resolved part list into a Resolution: concrete if every
// part is a literal, parent-derived if any part waits on a live value. The
// per-attribute split follows the same fork, since it is the same parts.
// idObjectOnly is [TypeIdentity.IdentityObjectOnly]: the type's identity has
// several attributes and no separator to join them with, so there is no
// import-ID string to build and the concatenation below must not run. See
// that field's doc comment for what the concatenation would otherwise hand
// out.
func classify(addr addrs.AbsResourceInstance, parts []Part, attrs []AttrFormula, idObjectOnly bool) Resolution {
	var parents []addrs.AbsResourceInstance
	seen := make(map[string]bool)
	for _, p := range parts {
		if p.Parent == nil {
			continue
		}
		k := p.Parent.Instance.String()
		if !seen[k] {
			seen[k] = true
			parents = append(parents, p.Parent.Instance)
		}
	}

	if len(parents) == 0 {
		var buf strings.Builder
		for _, p := range parts {
			buf.WriteString(p.Literal)
		}
		var values map[string]string
		if len(attrs) > 0 {
			values = make(map[string]string, len(attrs))
			for _, a := range attrs {
				var v strings.Builder
				for _, p := range a.Parts {
					v.WriteString(p.Literal)
				}
				values[a.Name] = v.String()
			}
		}
		importID := buf.String()
		if idObjectOnly {
			// The values are the whole answer, and joining them would
			// invent a grammar no schema carries. An empty ImportID is what
			// makes the projection import by identity object or fail
			// loudly, rather than fall back to a plausible-looking string.
			importID = ""
		}
		return Resolution{
			Addr:           addr,
			Class:          ClassConcrete,
			ImportID:       importID,
			IdentityValues: values,
		}
	}

	sort.Slice(parents, func(i, j int) bool {
		return parents[i].String() < parents[j].String()
	})
	return Resolution{
		Addr:  addr,
		Class: ClassParentDerived,
		Formula: &Formula{
			Parts:   parts,
			Attrs:   attrs,
			Parents: parents,
		},
	}
}

// missingCloudComponent reports the first component of this entry's identity
// whose cloud property neither the run nor the configuration supplied, in
// component order. A component the configuration answers (see
// cloudComponentAttr) is not missing anything, whether or not the run was told
// the cloud value.
//
// It returns the whole [Component] rather than just its [CloudValue] because
// the component's [Component.Attrs] is the operator's way out: those are the
// arguments the provider documents as defaulting to the cloud property, so
// setting one makes the identity computable with no cloud call and no marker.
// The refusal used to carry only the property, which left the one sentence an
// operator reads unable to name catalog_id even though catalog_id was the
// whole fix (GitHub issue #250). A component with no such argument returns an
// empty Attrs and the sentence offers no step, which is still true of it.
func (r *resolver) missingCloudComponent(entry TypeIdentity, attrs hcl.Attributes, scope instScope, addr addrs.AbsResourceInstance, cs cloudScopeKey) (Component, bool) {
	for _, comp := range entry.Components {
		if comp.Cloud == CloudNone {
			continue
		}
		if r.cloudComponentAttr(comp, attrs, scope, addr) != nil {
			continue
		}
		if _, ok := r.cloudValueFor(comp.Cloud, cs); !ok {
			return comp, true
		}
	}
	return Component{}, false
}

// cloudValueFor is this run's value for one cloud property, for the resource
// whose scope is cs, and whether it has one. It is the single answer both
// [resolver.missingCloudComponent] and the component loop read, so the refusal
// and the identity can never disagree about what is known.
//
// [CloudRegion] is answered from the resource's own effective region rather
// than from the caller-supplied [CloudContext], because that value is
// already established per resource, from configuration alone, with no cloud
// call: [resolver.effectiveRegion] takes the resource's own `region`
// argument when it states one and otherwise the region its resolved
// provider configuration statically declares, which is exactly the
// defaulting the AWS provider itself applies. A region component and the
// `region` argument the provider documents as defaulting to it are already
// the same component in the table (every {Cloud: "region"} row carries
// Attrs: ["region"]), so the argument case is handled by
// [resolver.cloudComponentAttr] before this is reached and only the
// provider-block fallback arrives here.
//
// Two consequences worth stating, since this turns refusals into
// identities. Being per-resource, it is right in an estate with several
// provider configurations in several regions, where one global field would
// have named one region for all of them. And a region established this way
// is stable between the analysis and the apply for the same reason every
// other identity argument is - it came out of the configuration, not out of
// the environment - so it is safe to build into a marker.
//
// regionKnown false and a known-but-empty region both mean "not
// established" here (see [cloudScopeKey]'s own comment for why the two are
// distinct for collision purposes: `region = ""` is a value to compare, but
// it is not a region to name an object under). Both fall through to the
// [CloudContext], which reports its own empty string as unknown, so the run
// refuses exactly as it did before.
//
// [CloudAccountID] has no config-only source and is not answered here. See
// [CloudContext] for where the account first becomes knowable.
func (r *resolver) cloudValueFor(which CloudValue, cs cloudScopeKey) (string, bool) {
	if which == CloudRegion && cs.regionKnown && cs.region != "" {
		return cs.region, true
	}
	return r.cloud.value(which)
}

// cloudComponentAttr reports the resource argument that supplies a
// cloud-bearing component's value for this instance, or nil when the
// configuration leaves the provider's documented cloud default to stand.
//
// A component carries both a [Component.Cloud] property and [Component.Attrs]
// when the provider's own Argument Reference says the argument defaults to
// that property - "If omitted, this defaults to the AWS Account ID" on a Glue
// catalog_id, "Defaults to the Region set in the provider configuration" on a
// region override. Both halves are real identity: the account is what the
// object is named under when the argument is absent, and the argument is what
// it is named under when the argument is present, which is the whole point of
// a cross-account Data Catalog. So the choice has to be made per instance,
// here, rather than by modelling the component as one or the other.
//
// An argument written but evaluating to a clean null is an absence, not a
// value - `catalog_id = var.cross_account ? var.catalog : null` is the same
// conditional spelling [resolver.resolveInstance] already handles for
// name/name_prefix - so it falls back to the cloud default rather than
// refusing. The peek raises no diagnostic of its own: an expression that
// fails to evaluate is reported as "stated", so the ordinary resolution path
// reports it exactly as it reports any other identity argument.
func (r *resolver) cloudComponentAttr(comp Component, attrs hcl.Attributes, scope instScope, addr addrs.AbsResourceInstance) *hcl.Attribute {
	attr := firstPresent(attrs, comp.Attrs)
	if attr == nil {
		return nil
	}
	if val, diags := r.evalPure(attr.Expr, scope, r.identifier(addr, attr.Name, attr.Range)); !diags.HasErrors() && val.IsNull() {
		return nil
	}
	return attr
}

// cloudReason is the needs-discovery explanation for an identity that embeds
// a property of the cloud rather than of the configuration. It has to read
// as an operator-facing sentence, because it is printed as one: the
// projection's omission section quotes it verbatim.
func cloudReason(entry TypeIdentity, missing CloudValue) string {
	return fmt.Sprintf(
		"an %s imports by %s, which embeds the %s, and that is a property of the cloud this run is pointed at rather than of the configuration; nothing has told this run what it is, so the identity cannot be built here.",
		entry.Type, entry.ImportSyntax, missing.describe(),
	)
}

// identityArgs pulls just the arguments the type's identity needs out of
// the resource body. Everything else in the body is ignored: identity
// resolution has no business decoding a whole resource. A nested block is
// ignored too, UNLESS some component names it through [Component.Block], in
// which case exactly that block's own named leaf attributes join the result,
// under their own (unqualified) names.
func (r *resolver) identityArgs(rc *configs.Resource, entry TypeIdentity) (hcl.Attributes, bool) {
	var names []string
	seen := make(map[string]bool)
	add := func(n string) {
		if !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}

	// blockLeaves collects, per [Component.Block] name, the leaf attribute
	// names some component reads from inside it. A block-scoped leaf never
	// joins the flat `names` set above - it is read from its own block's
	// body, not the resource's top-level body - and it gets none of the
	// "<name>_prefix" treatment below, which is a top-level-argument
	// convention with no bearing on a nested block's own attributes.
	var blockNames []string
	blockLeaves := map[string]map[string]bool{}
	addBlockLeaf := func(block, leaf string) {
		leaves, ok := blockLeaves[block]
		if !ok {
			leaves = map[string]bool{}
			blockLeaves[block] = leaves
			blockNames = append(blockNames, block)
		}
		leaves[leaf] = true
	}

	for _, comp := range entry.Components {
		for _, n := range comp.Attrs {
			if comp.Block != "" {
				addBlockLeaf(comp.Block, n)
				continue
			}
			add(n)
			// Every provider that names an object through one of these
			// arguments also offers the "<name>_prefix" sibling documented
			// to make the provider assign a random suffix at create time
			// (aws_db_parameter_group's name/name_prefix, aws_iam_role's
			// name/name_prefix, and so on across the provider - #190). It is
			// never this component's own identity value, so it is pulled
			// only to let [resolver.resolveInstance] tell "nothing named
			// this object" apart from "named it in a way that is not known
			// until the object exists" - the same distinction
			// [TypeIdentity.ServerAssigned] already draws at the whole-type
			// level, drawn here per instance instead.
			if !strings.HasSuffix(n, "_prefix") {
				add(n + "_prefix")
			}
		}
	}
	sort.Strings(names)
	sort.Strings(blockNames)

	schema := &hcl.BodySchema{}
	for _, n := range names {
		schema.Attributes = append(schema.Attributes, hcl.AttributeSchema{Name: n})
	}
	for _, b := range blockNames {
		schema.Blocks = append(schema.Blocks, hcl.BlockHeaderSchema{Type: b})
	}

	content, _, diags := rc.Config.PartialContent(schema)
	if diags.HasErrors() {
		r.appendDiags(diags)
		return nil, false
	}
	if len(blockNames) == 0 {
		return content.Attributes, true
	}

	// Merge in each named block's own leaves. The provider's own schema
	// caps every block [Component.Block] names at one instance
	// (max_items: 1, checked at ratification time - see that field's doc
	// comment), so only the FIRST occurrence of a repeated block name is
	// read; a config that syntactically repeats it anyway is one the
	// provider would refuse at validate time regardless of what this reads,
	// the same "safe direction, not a wrong one" this package takes
	// elsewhere rather than specially diagnosing an already-invalid shape.
	merged := make(hcl.Attributes, len(content.Attributes))
	for k, v := range content.Attributes {
		merged[k] = v
	}
	for _, block := range content.Blocks {
		leaves, ok := blockLeaves[block.Type]
		if !ok {
			continue
		}
		delete(blockLeaves, block.Type) // first occurrence only
		leafSchema := &hcl.BodySchema{}
		for leaf := range leaves {
			leafSchema.Attributes = append(leafSchema.Attributes, hcl.AttributeSchema{Name: leaf})
		}
		leafContent, _, leafDiags := block.Body.PartialContent(leafSchema)
		if leafDiags.HasErrors() {
			r.appendDiags(leafDiags)
			return nil, false
		}
		for k, v := range leafContent.Attributes {
			if _, exists := merged[k]; !exists {
				merged[k] = v
			}
		}
	}
	return merged, true
}

// resolveExpr turns one argument expression into import-ID parts.
// soleElementExpr is [Component.SoleElement]'s whole implementation: given
// the expression a list/set-typed identity argument was written with, it
// returns the one sub-expression to resolve in its place, or refuses.
//
// hcl.ExprList succeeds only for a syntactic list/set/tuple CONSTRUCT
// ("[...]"), so a Component.Attrs list mixing a collection-typed name with a
// genuinely scalar one (aws_security_group_rule pairs
// cidr_blocks/ipv6_cidr_blocks/prefix_list_ids, all lists, with
// source_security_group_id, a plain string) narrows only the members that
// need it: it fails harmlessly on the scalar member, which falls through to
// [resolver.soleElementFromValue] and then, if that finds no collection
// either, returns unchanged to resolve as a plain scalar.
//
// soleElementFromValue is the fallback for the construct not being
// syntactic: a variable or local typed list(string)/set(string) - a
// *_cidr_blocks argument declared that way is the common shape - is exactly
// as "written in configuration, not merely producing one at apply time" as
// every other identity component in this package already requires; nothing
// about the one-element rule is specific to how the collection was spelled.
func (r *resolver) soleElementExpr(expr hcl.Expression, scope instScope, attr *hcl.Attribute, ident configs.StaticIdentifier) (hcl.Expression, bool) {
	elems, diags := hcl.ExprList(expr)
	if diags.HasErrors() || elems == nil {
		if narrowed, ok, applicable := r.soleElementFromValue(expr, scope, attr, ident); applicable {
			return narrowed, ok
		}
		// #368: a collection this package cannot count at all until the
		// value exists, because it is a pure function of a value only a
		// live read supplies - `compact(split(",", <a parent's
		// cidr_block>))`. Neither narrowing above can reach it: there is no
		// syntactic list, and evaluation cannot produce one either. See
		// [resolver.soleElementDeferred] for why deferring the count is not
		// the same as guessing it.
		if wrapped, applicable := r.soleElementDeferred(expr, scope, ident); applicable {
			return wrapped, true
		}
		return expr, true
	}
	if len(elems) != 1 {
		r.errorf(attr.Range, "Ambiguous list-valued identity argument",
			"%s has %d elements. This component's identity can only be built when %s carries exactly one value; the AWS API - not this configuration's list order - decides how more than one composes into the real object, so this package will not guess which one to use.",
			ident.Subject, len(elems), attr.Name)
		return nil, false
	}
	return elems[0], true
}

// soleElementFromValue is [resolver.soleElementExpr]'s fallback when expr is
// not a syntactic list construct: it evaluates expr the same way
// [resolver.resolveExpr] eventually would, and applies the one-element rule
// structurally when - and only when - the result is itself a known
// list/set/tuple, rather than letting the whole collection reach
// [resolver.stringValue] and refuse as "Non-string identity argument".
//
// applicable is false whenever nothing here applies: expr references a
// managed resource (isSymbolic, nothing evaluable without the cloud), or
// evaluation fails for a reason unrelated to being a collection, or it
// succeeds but is not one. In every applicable=false case, no diagnostic is
// left behind - the caller's own unchanged-expression path stands, and
// resolveExpr raises whatever it would have raised for the original
// expression. When applicable is true, ok carries the same "exactly one
// element" verdict the syntactic case enforces, and the diagnostic (if any)
// is already recorded.
//
// The strict evaluation is backed by [resolver.tolerantRetry], the fourth and
// last caller of it, for the reason [resolver.tolerantPart] is the third: a
// collection-typed identity argument reached through a module CALL argument
// whose skeleton is literal and one of whose leaves is not
// ([resolver.tolerantVariables]) refuses whole, so the one-element rule never
// gets a collection to apply itself to and the argument falls through to a
// vaguer refusal about the whole variable. The retry is reached only after
// the strict evaluation has failed and only for an expression isSymbolic has
// already cleared, so it cannot pre-empt the element-expression chase; and
// its value still has to be a known, unmarked, one-element collection below,
// which is the same bar the strict path meets.
func (r *resolver) soleElementFromValue(expr hcl.Expression, scope instScope, attr *hcl.Attribute, ident configs.StaticIdentifier) (hcl.Expression, bool, bool) {
	if r.isSymbolic(expr, scope) {
		return nil, false, false
	}
	mark := len(r.diags)
	val, ok := r.evalStatic(expr, scope, ident)
	if !ok {
		r.diags = r.diags[:mark]
		retried, retryOK := r.tolerantRetry(expr, scope, ident)
		if !retryOK {
			return nil, false, false
		}
		val = retried
	}
	ty := val.Type()
	if !ty.IsListType() && !ty.IsSetType() && !ty.IsTupleType() {
		return nil, false, false
	}
	if val.IsNull() || !val.IsWhollyKnown() {
		return nil, false, false
	}
	// IsMarked before ElementIterator, which panics on a marked value: a
	// list-typed sensitive variable used as an identity argument -
	// `variable "cidrs" { type = list(string), sensitive = true }` set on
	// aws_security_group_rule.cidr_blocks - reaches here as a marked list
	// and crashed the run. Reported not-applicable rather than diagnosed
	// here, so the whole expression carries on to [resolver.resolveExpr]
	// and refuses through [resolver.stringValue]'s "Identity derived from a
	// sensitive value", which is the message this value deserves whether or
	// not it happens to be a one-element collection.
	if val.IsMarked() {
		return nil, false, false
	}
	n := 0
	var only cty.Value
	for it := val.ElementIterator(); it.Next(); {
		_, v := it.Element()
		only = v
		n++
	}
	if n != 1 {
		r.errorf(attr.Range, "Ambiguous list-valued identity argument",
			"%s has %d elements. This component's identity can only be built when %s carries exactly one value; the AWS API - not this configuration's list order - decides how more than one composes into the real object, so this package will not guess which one to use.",
			ident.Subject, n, attr.Name)
		return nil, false, true
	}
	return &hclsyntax.LiteralValueExpr{Val: only, SrcRange: attr.Range}, true, true
}

func (r *resolver) resolveExpr(expr hcl.Expression, scope instScope, ident configs.StaticIdentifier) ([]Part, bool) {
	if !r.isSymbolic(expr, scope) {
		mark := len(r.diags)
		sibMark := len(r.pendingSiblingApply)
		val, ok := r.evalStatic(expr, scope, ident)
		if ok {
			s, ok := r.stringValueIn(val, expr, scope, ident)
			if ok {
				return []Part{{Literal: s}}, true
			}

			// stringValueIn evaluated expr cleanly but the result was not
			// wholly known (issue #284's managedCovered defect). When expr
			// is ITSELF a direct traversal into a managed resource this
			// run's results cover - the exact reason isSymbolic did not
			// already send it through the formula path below, see
			// [resolver.managedCovered] - retry through
			// [resolver.resolveTraversal]. A PARENT_DERIVED formula is a
			// better answer than the classification stringValueIn just
			// recorded: marker discovery can render the formula once the
			// sibling is found, where a NEEDS_DISCOVERY/SIBLING_APPLY
			// classification can only say "wait for it".
			//
			// Guarded on managedCovered(trav) rather than tried for any
			// unknown result: a bare traversal into a DATA resource, or an
			// unset variable reached through a local, would make
			// resolveTraversal refuse for its own unrelated reason, and
			// this must not staple a second diagnostic onto the one
			// stringValueIn already recorded for those. managedCovered is
			// also precisely the condition [resolver.isSymbolic] used to
			// decide this expr was not symbolic in the first place, so the
			// retry only ever fires on the reference that decision was
			// actually about.
			if trav, tdiags := hcl.AbsTraversalForExpr(expr); !tdiags.HasErrors() && r.managedCovered(trav) {
				preRetry := len(r.diags)
				if parts, retryOK := r.resolveTraversal(trav, scope, ident); retryOK {
					r.diags = r.diags[:mark]
					r.pendingSiblingApply = r.pendingSiblingApply[:sibMark]
					return parts, true
				}
				// The retry did not apply either. Keep stringValueIn's own
				// diagnostic, not resolveTraversal's - trav is provably a
				// managed reference (managedCovered admitted it), so
				// whatever resolveTraversal failed on is a different, less
				// specific complaint about the same expression.
				r.diags = r.diags[:preRetry]
			}

			// #354: the same retry for the other way an identity argument
			// arrives at a known value with an unknown inside it - a for_each
			// element the declared-type conversion produced, one of whose
			// attributes reads something the static scope cannot answer.
			// stringValueIn has just refused the unknown; the element's own
			// expression for that one attribute is still in hand, and
			// resolving it structurally is what turns the refusal into a
			// formula. See [resolver.eachValueDeferredParts], which declines
			// unless it reaches a resolution of its own.
			if parts, ok := r.eachValueDeferredParts(expr, scope, ident); ok {
				r.diags = r.diags[:mark]
				r.pendingSiblingApply = r.pendingSiblingApply[:sibMark]
				return parts, true
			}
			return nil, false
		}

		// evalStatic just failed to evaluate the WHOLE expression, which,
		// for a reference into a local or a module variable, means it tried
		// to evaluate that local's or that variable's entire definition,
		// not only the part this expression actually selects. #178's
		// local-values fix: retry by decomposing that definition
		// structurally instead, the same way a direct resource reference
		// already resolves (resolveTraversal/parentPart), so a
		// resource-attribute reference reached only through a local's own
		// value resolves rather than refusing the whole block. See
		// localvalue.go. It changes nothing when the shape is not one this
		// package understands: the diagnostic evalStatic already recorded
		// stands, unreplaced.
		markAfterEval := len(r.diags)
		if parts, leafOK, applicable := r.namedLeaf(expr, scope, ident); applicable {
			r.diags = append(r.diags[:mark:mark], r.diags[markAfterEval:]...)
			return parts, leafOK
		}

		// A selection expression - `cond ? A : B`, or coalesce(A, B, ...) -
		// written entirely out of var and local references, so
		// [resolver.isSymbolic] saw no managed resource anywhere in it and
		// sent the whole thing down this branch rather than to the
		// symbolic switch below. The managed resource is there; it is
		// behind one of those names, which is exactly what
		// [resolver.namedLeaf] just proved by chasing it. See
		// [resolver.resolveSelection].
		if parts, selOK, applicable := r.resolveSelection(expr, scope, ident); applicable {
			r.diags = append(r.diags[:mark:mark], r.diags[markAfterEval:]...)
			return parts, selOK
		}

		// #368: a pure function applied to a value only known at render
		// time - `try(element(split("/", var.cluster_arn), 1), "")` over a
		// module output that is another resource's arn. Like the two
		// branches above it, this arrives here rather than at the symbolic
		// switch because [resolver.isSymbolic] reads traversal ROOTS and
		// `var` is never one of the symbolic ones; the managed resource is
		// behind the name. See transform.go.
		if parts, tOK, applicable := r.resolveTransformCall(expr, scope, ident); applicable {
			r.diags = append(r.diags[:mark:mark], r.diags[markAfterEval:]...)
			return parts, tOK
		}

		// Last of all, and only where every route above has already
		// returned false: the value is in the caller's configuration, but
		// a module argument this module's caller built out of a literal
		// skeleton and one unresolvable leaf stands between this argument
		// and it. `lookup(var.ingress_with_cidr_blocks[count.index],
		// "from_port", ...)` over `ingress_with_cidr_blocks = [{ from_port
		// = 5432, ..., cidr_blocks = module.vpc.vpc_cidr_block }]` is 5432
		// whatever the one dynamic leaf turns out to be, and the sibling
		// that DOES read that leaf keeps refusing on the same rebuilt
		// value. See [resolver.tolerantPart], which restores r.diags
		// itself when it declines.
		if parts, ok := r.tolerantPart(expr, scope, ident, mark, sibMark); ok {
			return parts, true
		}

		// Truly last, at the point every route above has already returned
		// false from: a reference whose STEPS this package can read, written
		// with something other than a bare traversal, so
		// [resolver.namedLeaf]'s hcl.AbsTraversalForExpr gate never let it
		// reach the chase at all. `var.ingress_with_cidr_blocks[count.index]`
		// and `lookup(var.ingress_with_cidr_blocks[count.index],
		// "cidr_blocks", <default>)` are the two spellings, and both are
		// folded into the traversal the author would have written had the
		// index been a constant. See computedselect.go, which restores
		// r.diags itself when it declines.
		if parts, ok := r.foldedSelect(expr, scope, ident); ok {
			r.diags = append(r.diags[:mark:mark], r.diags[markAfterEval:]...)
			return parts, true
		}
		return nil, false
	}

	switch e := expr.(type) {
	case *hclsyntax.TemplateExpr:
		var parts []Part
		for _, sub := range e.Parts {
			got, ok := r.resolveExpr(sub, scope, ident)
			if !ok {
				return nil, false
			}
			parts = append(parts, got...)
		}
		return parts, true
	case *hclsyntax.TemplateWrapExpr:
		return r.resolveExpr(e.Wrapped, scope, ident)
	case *hclsyntax.ParenthesesExpr:
		return r.resolveExpr(e.Expression, scope, ident)
	case *hclsyntax.ConditionalExpr:
		return r.resolveConditional(e, scope, ident)
	case *hclsyntax.FunctionCallExpr:
		// join(sep, R.*.attr) / one(R.*.attr) over a resource that provably
		// expands to exactly one instance - see splat.go. Not applicable to
		// any other call, which falls out of the switch to the generic
		// "cannot be passed through functions or operators" refusal below.
		if parts, ok, applicable := r.resolveArityCollapse(e, scope, ident); applicable {
			return parts, ok
		}
		// element(R[*].attr, idx) resolved to the one instance idx names,
		// wrapped modulo R's own instance count exactly as element() itself
		// wraps it - see splat.go's resolveElementCall. Not applicable to
		// any other call or any collection that is not a splat over a bare
		// managed resource, which falls through to the generic refusal
		// below exactly as it always has.
		if parts, ok, applicable := r.resolveElementCall(e, scope, ident); applicable {
			return parts, ok
		}
		// element(coalescelist(A[*].attr, B[*].attr, ...), idx) resolved to
		// the winning coalescelist() argument's own idx-th instance, wrapped
		// modulo THAT argument's own length - see splat.go's
		// resolveElementCoalescelist. Not applicable unless the first
		// argument is itself a coalescelist() call, which
		// resolveElementCall's own bare-splat requirement above already
		// excludes, so trying both here is safe and order-independent.
		if parts, ok, applicable := r.resolveElementCoalescelist(e, scope, ident); applicable {
			return parts, ok
		}
		// try(A, B, ...) resolved to whichever argument the language selects,
		// when resource expansion settles which arguments raise an error -
		// see fallback.go. Same not-applicable contract as above.
		if parts, ok, applicable := r.resolveFallbackChain(e, scope, ident); applicable {
			return parts, ok
		}
		// coalesce(A, B, ...) resolved to whichever argument the language
		// selects, when every argument before it is provably null or empty
		// and that one is provably neither - see coalesce.go. Same
		// not-applicable contract as above. This is the door the call
		// arrives at when a managed resource is named directly inside it;
		// the branch above [resolver.namedLeaf] is the one it arrives at
		// when every argument is a var or local reference.
		if parts, ok, applicable := r.resolveCoalesceCall(e, scope, ident); applicable {
			return parts, ok
		}
		// lookup(each.value, "key", fallback) over an element bound as an
		// expression, resolved to whichever of the two the language selects -
		// see [resolver.resolveLookupCall]. Same not-applicable contract as
		// above; a lookup whose own value evaluates never reaches here at all,
		// because the non-symbolic branch above took it.
		if parts, ok, applicable := r.resolveLookupCall(e, scope, ident); applicable {
			return parts, ok
		}
		// #368: split/compact/element/one applied to a single deferred
		// parent read - see transform.go. Last of the call handlers, so
		// every shape one of the others already owns keeps its own answer;
		// this one only ever sees a call none of them claimed.
		if parts, ok, applicable := r.resolveTransformCall(e, scope, ident); applicable {
			return parts, ok
		}
	case *hclsyntax.IndexExpr:
		// concat(A[*].attr, B[*].attr, ..., [literal])[N] where N is not a
		// literal (count.index, a local) - see splat.go's
		// resolveConcatIndex, whose own doc comment explains why this
		// shape needs both this case and the next one. Not applicable to
		// anything but an index into a concat() call, which falls through
		// to the generic refusal below exactly as it always has.
		if parts, ok, applicable := r.resolveConcatIndex(e, scope, ident); applicable {
			return parts, ok
		}
		// #368: `split(sep, <deferred read>)[N]` - see transform.go.
		if parts, ok, applicable := r.resolveTransformCall(e, scope, ident); applicable {
			return parts, ok
		}
	case *hclsyntax.RelativeTraversalExpr:
		// concat(A[*].attr, B[*].attr, ..., [literal])[N] where N IS a
		// literal, e.g. concat(...)[0] - #324's own local.this_sg_id shape.
		// HCL folds a constant index into a traversal step rather than
		// building an IndexExpr, so this arrives as a RelativeTraversalExpr
		// wrapping the concat() call, not as the case above - see
		// resolveConcatIndex's doc comment. Trying it here, before
		// resolveIndexedTraversal runs below, changes nothing for the
		// shape resolveIndexedTraversal itself owns (R[idx].attr, a
		// RelativeTraversalExpr wrapping an *IndexExpr with a trailing
		// TraverseAttr): resolveConcatIndex requires its Source to be a
		// concat() FunctionCallExpr and its Traversal to be a single
		// TraverseIndex, which that shape never is, so it reports
		// applicable=false and falls through to resolveIndexedTraversal
		// unchanged.
		if parts, ok, applicable := r.resolveConcatIndex(e, scope, ident); applicable {
			return parts, ok
		}
		// #368: the same `split(sep, <deferred read>)[0]` shape with a
		// constant index, which HCL folds into a traversal step exactly as
		// it folds concat(...)[0] above. It cannot claim
		// [resolver.resolveIndexedTraversal]'s own R[idx].attr shape: that
		// one's single traversal step is a TraverseAttr, and
		// [resolver.peelTransform] recognizes only a TraverseIndex, so the
		// pipeline comes back empty and this declines.
		if parts, ok, applicable := r.resolveTransformCall(e, scope, ident); applicable {
			return parts, ok
		}
	}

	trav, diags := hcl.AbsTraversalForExpr(expr)
	if diags.HasErrors() {
		if parts, ok, applicable := r.resolveIndexedTraversal(expr, scope, ident); applicable {
			return parts, ok
		}
		r.errorf(expr.Range(), "Identity not resolvable from configuration",
			"%s refers to another resource inside an expression that identity resolution cannot follow. "+
				"A resource reference contributes to an identity only as a whole reference or as an interpolation in a string template; "+
				"it cannot be passed through functions or operators, because the value it produces is not known until apply.",
			ident.Subject)
		return nil, false
	}
	return r.resolveTraversal(trav, scope, ident)
}

// resolveConditional decomposes `cond ? A : B` (GitHub issue #196) by
// evaluating cond statically and recursing [resolver.resolveExpr] into
// whichever branch it selects, so a resource reference in the branch NOT
// taken is never consulted - it does not even need to be resolvable.
//
// This reaches only the case [resolver.resolveExpr]'s own top-level
// isSymbolic check could not already dispatch to evalStatic: cond, A and B
// combined reference a managed resource somewhere (isSymbolic sees every
// hcl.Expression.Variables() in the whole ConditionalExpr, regardless of
// which branch runs), so `cond ? "literal" : "literal"` and similar
// resource-free conditionals never reach here - they already resolve
// through the ordinary evalStatic path above.
//
// It draws no new boundary: cond is rejected exactly when
// [resolver.isSymbolic] would reject it as a bare expression (a managed
// resource, or each.value over a for_each'd resource), and the selected
// branch is handed back to resolveExpr, which applies every existing rule -
// parentPart's registered-IdentityAttrs check, the provider-schema
// Computed-flag boundary in [resolver.siblingLiteralExpr] - unchanged.
func (r *resolver) resolveConditional(e *hclsyntax.ConditionalExpr, scope instScope, ident configs.StaticIdentifier) ([]Part, bool) {
	if r.isSymbolic(e.Condition, scope) {
		// [resolver.isSymbolic]'s each.value case is blanket over the WHOLE
		// element, not over which attribute a particular reference selects:
		// once a for_each element carries even one poisoned leaf anywhere,
		// every each.value.<attr> reference into it - including a plain
		// string literal sibling, terraform-aws-modules/alb's own
		// `try(each.value.target_type, null) == "lambda"` guard among them -
		// is symbolic here. eachValueCondTolerant tries the same structural
		// resolution [resolver.eachValueSelect] already gives a bare
		// each.value.<attr> reference, for the one operator shape a
		// for_each'd module's own guards are actually built from: an
		// equality test (composed with &&/||/!) against a literal. See its
		// own doc.
		if b, ok := r.eachValueCondTolerant(e.Condition, scope, ident); ok {
			if b {
				return r.resolveExpr(e.TrueResult, scope, ident)
			}
			return r.resolveExpr(e.FalseResult, scope, ident)
		}
		r.errorf(e.Condition.Range(), "Identity not resolvable from configuration",
			"%s selects between branches of a conditional expression using another resource's value. "+
				"A resource reference contributes to an identity only as a whole reference or as an interpolation in a string template; "+
				"it cannot be passed through functions or operators, because the value it produces is not known until apply - so which branch applies is not known either.",
			ident.Subject)
		return nil, false
	}

	condVal, ok := r.evalStatic(e.Condition, scope, ident)
	if !ok {
		// evalStatic already recorded why (an unset variable, an impure
		// call, and so on).
		return nil, false
	}
	condVal, convErr := convert.Convert(condVal, cty.Bool)
	if convErr != nil || condVal.IsNull() || !condVal.IsKnown() {
		r.errorf(e.Condition.Range(), "Identity not resolvable from configuration",
			"%s's conditional expression condition did not evaluate to a known true/false value.",
			ident.Subject)
		return nil, false
	}
	// cty.Value.True panics on a marked value, and `variable "x" { type =
	// bool, sensitive = true }` used as the condition produces one - so this
	// check is what stands between an ordinary configuration and a crashed
	// run, not a nicety. Refusing rather than reading it also keeps the
	// package's existing stance: [resolver.stringValue] already refuses a
	// sensitive identity value outright, and which branch a sensitive
	// condition selected is one bit of that same value, rendered into the
	// marker and into plan output.
	if condVal.IsMarked() {
		r.errorf(e.Condition.Range(), "Identity derived from a sensitive value",
			"%s selects between branches of a conditional expression using a sensitive value. "+
				"An import identity is written to logs and plan output, so which branch a sensitive condition chose cannot be recorded there. "+
				"If the value is not genuinely secret, wrap it in nonsensitive(...) to use it here.",
			ident.Subject)
		return nil, false
	}

	if condVal.True() {
		return r.resolveExpr(e.TrueResult, scope, ident)
	}
	return r.resolveExpr(e.FalseResult, scope, ident)
}

// eachValueCondTolerant answers a conditional's condition when
// [resolver.isSymbolic] called it symbolic only because it reads
// each.value.<attr> against a for_each element whose value never proved
// (scope.eachValueExpr/eachValueDeferred, not scope.vars) - never for an
// actual managed-resource reference, which isSymbolic reports the identical
// way and which this leaves refused exactly as before.
//
// The shape it decides is an equality test, composed with &&, || and !:
// terraform-aws-modules/alb's own `try(each.value.target_type, null) ==
// "lambda" ? null : ...` is the corpus site, and the same guard - "is this
// attribute equal to a literal" - is how nearly every terraform-aws-modules
// block branches on one attribute of a for_each element. Each operand is
// resolved through [resolver.resolveExpr], the SAME machinery a bare
// each.value.<attr> identity argument already uses (eachvalue.go), so a
// truly unresolvable operand - the attribute actually is a sibling
// reference, not a literal - still leaves this undecided and the caller's
// refusal stands; only a literal comparison, previously blocked by nothing
// but the blanket each.value blanket check, newly decides.
//
// Boolean connectives compose by ordinary three-valued (Kleene) logic, the
// same rule [resolver.forCondIncludesTolerant] uses for a for-expression's
// own filter clause: AND is false whenever either side decides false
// whatever the other side is, and OR is true whenever either side decides
// true.
func (r *resolver) eachValueCondTolerant(cond hclsyntax.Expression, scope instScope, ident configs.StaticIdentifier) (bool, bool) {
	switch e := cond.(type) {
	case *hclsyntax.ParenthesesExpr:
		return r.eachValueCondTolerant(e.Expression, scope, ident)

	case *hclsyntax.UnaryOpExpr:
		if e.Op != hclsyntax.OpLogicalNot {
			return false, false
		}
		b, ok := r.eachValueCondTolerant(e.Val, scope, ident)
		if !ok {
			return false, false
		}
		return !b, true

	case *hclsyntax.BinaryOpExpr:
		switch e.Op {
		case hclsyntax.OpEqual, hclsyntax.OpNotEqual:
			lv, lok := r.eachValueCondOperand(e.LHS, scope, ident)
			rv, rok := r.eachValueCondOperand(e.RHS, scope, ident)
			if !lok || !rok {
				return false, false
			}
			eq := lv.RawEquals(rv)
			if e.Op == hclsyntax.OpNotEqual {
				return !eq, true
			}
			return eq, true

		case hclsyntax.OpLogicalAnd:
			lInc, lOK := r.eachValueCondTolerant(e.LHS, scope, ident)
			if lOK && !lInc {
				return false, true
			}
			rInc, rOK := r.eachValueCondTolerant(e.RHS, scope, ident)
			if rOK && !rInc {
				return false, true
			}
			if lOK && rOK {
				return lInc && rInc, true
			}
			return false, false

		case hclsyntax.OpLogicalOr:
			lInc, lOK := r.eachValueCondTolerant(e.LHS, scope, ident)
			if lOK && lInc {
				return true, true
			}
			rInc, rOK := r.eachValueCondTolerant(e.RHS, scope, ident)
			if rOK && rInc {
				return true, true
			}
			if lOK && rOK {
				return lInc || rInc, true
			}
			return false, false
		}
	}
	return false, false
}

// eachValueCondOperand resolves one side of an equality test inside
// [resolver.eachValueCondTolerant]. A plain literal evaluates as itself
// through the ordinary strict evaluator; anything symbolic - most often
// try(each.value.attr, fallback) or a bare each.value.attr - is resolved
// through [resolver.resolveExpr], the same entry point every other identity
// argument uses, and taken only when it lands on exactly one Literal part:
// which branch a ternary takes has to be fully known, never a Formula
// deferred on a sibling's own apply.
func (r *resolver) eachValueCondOperand(expr hcl.Expression, scope instScope, ident configs.StaticIdentifier) (cty.Value, bool) {
	if v, diags := r.evalPure(expr, scope, ident); !diags.HasErrors() && !v.IsNull() && v.IsWhollyKnown() && !v.IsMarked() {
		return v, true
	}
	mark, sibMark := len(r.diags), len(r.pendingSiblingApply)
	parts, ok := r.resolveExpr(expr, scope, ident)
	r.diags = r.diags[:mark]
	r.pendingSiblingApply = r.pendingSiblingApply[:sibMark]
	if !ok || len(parts) != 1 || parts[0].Parent != nil {
		return cty.NilVal, false
	}
	return cty.StringVal(parts[0].Literal), true
}

// resolveIndexedTraversal decomposes a reference into another resource
// selected by a computed index - aws_subnet.this[each.key].id or
// aws_instance.this[count.index].private_ip - which hcl.AbsTraversalForExpr
// cannot turn into a traversal because the index is an expression rather
// than a literal HCL parses directly into a traversal step. The index is
// evaluated the same way any other identity-argument expression is
// ([resolver.evalStatic]), against the same per-instance scope that already
// carries each.key/each.value or count.index ([expansion.scope]) - so this
// is not new evaluation machinery, only a second way to reach
// [resolver.parentPart] once the addressed instance is known, alongside the
// literal-index and bare-reference paths [resolver.resolveTraversal]
// already has via hcl.AbsTraversalForExpr.
//
// applicable is false whenever expr is not this shape at all: not a single
// attribute step following one index into what turns out to be a bare
// managed-resource reference. The caller's own "cannot follow" diagnostic
// stands unreplaced in that case. When applicable is true, ok reports
// whether resolution succeeded, and a diagnostic has already been recorded
// in its place when it did not - either by this function directly, or by
// whatever evaluated the index or the target's own expansion.
func (r *resolver) resolveIndexedTraversal(expr hcl.Expression, scope instScope, ident configs.StaticIdentifier) (parts []Part, ok bool, applicable bool) {
	rel, isRel := expr.(*hclsyntax.RelativeTraversalExpr)
	if !isRel {
		return nil, false, false
	}
	idx, isIdx := rel.Source.(*hclsyntax.IndexExpr)
	if !isIdx {
		return nil, false, false
	}
	if len(rel.Traversal) != 1 {
		return nil, false, false
	}
	attrStep, isAttr := rel.Traversal[0].(hcl.TraverseAttr)
	if !isAttr {
		return nil, false, false
	}

	trav, diags := hcl.AbsTraversalForExpr(idx.Collection)
	if diags.HasErrors() {
		return nil, false, false
	}
	ref, refDiags := addrs.ParseRef(trav)
	if refDiags.HasErrors() {
		return nil, false, false
	}
	resAddr, isRes := ref.Subject.(addrs.Resource)
	if !isRes || len(ref.Remaining) > 0 || resAddr.Mode != addrs.ManagedResourceMode {
		return nil, false, false
	}

	rc := r.mod.ResourceByAddr(resAddr)
	if rc == nil {
		return nil, false, false
	}
	exp, expOK := r.expansionFor(rc)
	if !expOK {
		// The target's own expansion already failed and already carries a
		// diagnostic, wherever that first happened - see [resolveResourceRef]
		// for the same pattern.
		return nil, false, true
	}

	mark := len(r.diags)
	keyVal, keyOK := r.evalStatic(idx.Key, scope, ident)
	if !keyOK {
		// The same each.value-is-blanket-symbolic wall
		// [resolver.eachValueCondTolerant] answers for a conditional's own
		// condition: idx.Key is very often a plain LITERAL sibling of a
		// for_each element that carries an unrelated poisoned leaf
		// elsewhere - terraform-aws-modules/alb's own
		// aws_lb_target_group.this[each.value.target_group_key].arn is
		// exactly this shape (target_group_key is a plain string in every
		// element of var.additional_target_group_attachments; target_id, a
		// sibling attribute, is the poisoned one). Reuse
		// [resolver.eachValueCondOperand]'s structural resolution - the
		// same one-Literal-part bar it already enforces - rather than
		// leave a computed index refused merely because SOME other
		// attribute of the same element is unprovable.
		if v, ok := r.eachValueCondOperand(idx.Key, scope, ident); ok {
			r.diags = r.diags[:mark]
			keyVal, keyOK = v, true
		}
	}
	if !keyOK {
		// evalStatic (or the fallback above) already recorded why.
		return nil, false, true
	}
	key, keyIsValid := indexKeyValue(keyVal)
	if !keyIsValid {
		r.errorf(idx.Key.Range(), "Identity not resolvable from configuration",
			"%s indexes %s with a value that is not a string or a whole number, so it cannot select one of its instances.",
			ident.Subject, resAddr.String())
		return nil, false, true
	}
	if !exp.hasKey(key) {
		r.errorf(idx.SrcRange, "Reference to a resource instance that does not exist",
			"%s does not exist. %s", resAddr.Instance(key).String(), exp.describe(resAddr))
		return nil, false, true
	}

	got, gotOK := r.parentPart(resAddr.Instance(key).Absolute(r.modInst), attrStep.Name, expr.Range(), ident)
	return got, gotOK, true
}

// elementIndexValue turns an evaluated element() index argument into a plain
// int, converting through cty.Number first so a value that is already a
// number in a different cty representation (or, degenerately, a numeric
// string) still resolves - the same tolerance [resolver.resolveConditional]
// applies converting its own condition to cty.Bool. Unlike [indexKeyValue],
// this deliberately allows a negative result: element()'s own wraparound
// (see [resolver.resolveElementCall]) is defined for one, and refusing it
// here would refuse a shape the function itself accepts.
func elementIndexValue(val cty.Value) (int, bool) {
	if val.IsNull() || !val.IsKnown() || val.IsMarked() {
		return 0, false
	}
	num, err := convert.Convert(val, cty.Number)
	if err != nil {
		return 0, false
	}
	var n int
	if err := gocty.FromCtyValue(num, &n); err != nil {
		return 0, false
	}
	return n, true
}

// indexKeyValue turns an evaluated index expression into the instance key it
// names, the same two key kinds every resource expansion in this package
// already produces ([resolver.countExpansion], [resolver.forEachExpansion]).
func indexKeyValue(val cty.Value) (addrs.InstanceKey, bool) {
	if val.IsNull() || !val.IsKnown() || val.IsMarked() {
		return nil, false
	}
	switch {
	case val.Type() == cty.String:
		return addrs.StringKey(val.AsString()), true
	case val.Type() == cty.Number:
		var n int
		if err := gocty.FromCtyValue(val, &n); err != nil {
			return nil, false
		}
		return addrs.IntKey(n), true
	}
	return nil, false
}

// resolveTraversal turns a reference to another resource's attribute into a
// single part: a literal when that parent is already concrete, a parent
// reference otherwise.
func (r *resolver) resolveTraversal(trav hcl.Traversal, scope instScope, ident configs.StaticIdentifier) ([]Part, bool) {
	rng := trav.SourceRange()

	if trav.RootName() == "each" && scope.eachParent != nil {
		// each.value in a for_each-over-a-resource block is the parent
		// instance with the same key, so each.value.id is that parent's
		// id.
		if len(trav) != 3 || !isAttrStep(trav[1], "value") {
			r.errorf(rng, "Unsupported each.value reference",
				"Only each.value.<attribute> can contribute to an identity when for_each iterates over another resource, but %s was referenced by %s.",
				traversalString(trav), ident.Subject)
			return nil, false
		}
		attrStep, ok := trav[2].(hcl.TraverseAttr)
		if !ok {
			r.errorf(rng, "Unsupported each.value reference",
				"Only each.value.<attribute> can contribute to an identity when for_each iterates over another resource, but %s was referenced by %s.",
				traversalString(trav), ident.Subject)
			return nil, false
		}
		parent := scope.eachParent.Instance(scope.key).Absolute(r.modInst)
		return r.parentPart(parent, attrStep.Name, rng, ident)
	}

	if trav.RootName() == "each" && scope.eachValueExpr != nil &&
		len(trav) >= 2 && isAttrStep(trav[1], "value") {
		// #260: each.value is an expression here, not a value. See
		// eachvalue.go.
		return r.eachValuePart(trav, scope, ident)
	}

	ref, refDiags := addrs.ParseRef(trav)
	if refDiags.HasErrors() {
		r.appendDiags(refDiags)
		return nil, false
	}

	var instAddr addrs.ResourceInstance
	switch subject := ref.Subject.(type) {
	case addrs.Resource:
		instAddr = subject.Instance(addrs.NoKey)
	case addrs.ResourceInstance:
		instAddr = subject
	default:
		r.errorf(rng, "Identity not resolvable from configuration",
			"%s refers to %s, which identity resolution cannot read. An identity can be composed from constants, variables, locals, path and terraform/tofu values, and other managed resources' identity attributes.",
			ident.Subject, ref.Subject.String())
		return nil, false
	}
	if instAddr.Resource.Mode != addrs.ManagedResourceMode {
		r.errorf(rng, "Identity not resolvable from configuration",
			"%s refers to %s, an ephemeral resource. An ephemeral value exists only for the duration of one run, so it cannot name a cloud object that has to be findable on the next one.",
			ident.Subject, instAddr.String())
		return nil, false
	}
	if len(ref.Remaining) != 1 {
		r.errorf(rng, "Identity not resolvable from configuration",
			"%s refers to %s, but an identity can only be built from a single attribute of another resource (its identity attribute).",
			ident.Subject, traversalString(trav))
		return nil, false
	}
	attrStep, ok := ref.Remaining[0].(hcl.TraverseAttr)
	if !ok {
		r.errorf(rng, "Identity not resolvable from configuration",
			"%s indexes into %s. An identity can only be built from a single attribute of another resource.",
			ident.Subject, instAddr.String())
		return nil, false
	}

	return r.parentPart(instAddr.Absolute(r.modInst), attrStep.Name, rng, ident)
}

func (r *resolver) parentPart(parent addrs.AbsResourceInstance, attrName string, rng hcl.Range, ident configs.StaticIdentifier) ([]Part, bool) {
	parentRes, ok := r.instance(parent, rng)
	if !ok {
		r.errorf(rng, "Unresolvable identity",
			"%s depends on the identity of %s, which could not be resolved (see the other error).",
			ident.Subject, parent.String())
		return nil, false
	}

	entry, _ := r.lookupType(parent.Resource.Resource.Type)

	// The parent's own resolution already answers this, one attribute at a
	// time, whenever the parent's entry says which component supplies which
	// identity attribute: that split is [Resolution.IdentityValues] for a
	// concrete parent and [Formula.Attrs] for a parent-derived one. Reading
	// it is not a new claim about the cloud - it is the same parts, from the
	// same arguments, that the parent's own identity is built from, so a
	// reference to one attribute can never disagree with the identity the
	// parent already asserts.
	//
	// It is tried before IdentityAttrs because it is the more precise of the
	// two, and because the fallback below is wrong wherever they differ. See
	// [Resolution.attrParts].
	if got, ok := parentRes.attrParts(attrName); ok {
		return got, true
	}

	if !entry.hasIdentityAttr(attrName) {
		// attrName is not part of parent's cloud identity at all - but it
		// may still be an ordinary argument parent's own block sets
		// literally, which is fully static and has nothing to do with
		// identity (GitHub issue #220): aws_route53_record.mx reading
		// aws_route53_zone.production.name, where the zone's identity is
		// zone_id and name is just a plain string the block wrote. See
		// [resolver.siblingLiteralExpr] for the boundary that makes this
		// safe.
		if expr, scope, applicable := r.siblingLiteralExpr(parent, attrName); applicable {
			return r.resolveExpr(expr, scope, ident)
		}

		// A record-backed parent ([ClassRecordBacked]) has no cloud
		// identity for hasIdentityAttr to recognise and no IdentityAttrs
		// on its row, but it is not unknowable: its whole object is
		// hydrated from the record store before any formula renders
		// (internal/live/projection's builder.run materializes every
		// record-backed resolution ahead of both the concrete and the
		// derived phase, and builder.renderFormula's parent lookup is
		// class-agnostic). So a promise to read the attribute later is
		// exactly as good here as it is for a parent-derived parent, and
		// the tail of this function already knows how to make one.
		//
		// The rule is keyed on the class and settled by the provider's own
		// schema rather than by type name, so it covers every row row-gen
		// marks RecordBacked and nothing else. Without schemas we cannot
		// tell a real attribute from a typo, and the refusal below stands.
		//
		// A concrete parent ([ClassConcrete]) reaches the same conclusion
		// by the same argument, one phase earlier in the same builder:
		// builder.run materializes every concrete resolution - import,
		// then ReadResource - before it renders a single formula, so by
		// the time this promise is read the parent's whole provider object
		// is in b.live, and builder.renderFormula's lookup takes an
		// arbitrary attribute off it with attrString rather than off its
		// identity. What the parent's row says about its IDENTITY
		// attributes is therefore not the boundary of what can be read
		// from it; it is only the boundary of what can be read without
		// touching the cloud, which is a different question and is
		// already answered above by attrParts and siblingLiteralExpr.
		//
		// This is strictly a widening of what resolves: every case that
		// reaches here refuses today. It is not a widening of what stays
		// offline - a child that takes this branch becomes
		// [ClassParentDerived] and so needs its parent read live, where
		// before it was not resolvable at all. Nothing that resolves
		// concrete today can reach this line, because the concrete
		// shortcut below sits outside this block and only genuine
		// identity attributes ever get there.
		//
		// The concrete case carries one condition the record-backed case
		// does not: the parent's entry has to be a ratified [DefaultTable]
		// row rather than one [SynthesizeTypeIdentity] inferred. A
		// synthesized entry is already a reading of the provider's
		// identity schema, and the CONCRETE classification that this
		// branch's whole argument rests on - the parent is imported and
		// read before any formula renders - is only as good as that
		// reading. Deferring a second value to it stacks an inference on
		// an inference. That is a deliberately conservative line and not a
		// claim that the synthesized case is wrong; it is left refused
		// pending its own decision, along with what
		// [resolver.siblingLiteralExpr]'s Computed boundary should mean
		// once the value comes from the live object rather than from
		// configuration.
		//
		// A NEEDS-DISCOVERY parent ([ClassNeedsDiscovery]) joins the
		// concrete case on the same argument, one phase EARLIER in the same
		// pipeline, and this is GitHub issue #346's second half. Its
		// identity is not in the configuration, so marker discovery finds
		// it: internal/command/live_plan.go replaces the resolution list
		// with discovery's own (merged = disco.Resolutions), in which every
		// discovered instance is CONCRETE. builder.run then materializes
		// every concrete resolution - import, then ReadResource - before it
		// renders a single formula, so by the time this promise is read the
		// parent's whole provider object is in b.live exactly as it is for
		// a parent that was concrete from the start, and
		// builder.renderFormula's lookup takes an arbitrary attribute off
		// it with attrString.
		//
		// A parent discovery does NOT find stays needs-discovery, is
		// omitted by builder.run, and renderFormula's own parent check then
		// omits this child with ReasonParentUnavailable. That is the same
		// outcome a ParentRef to an identity attribute of an undiscovered
		// parent already has today (the tail of this function makes one for
		// every needs-discovery parent whose attribute IS an identity
		// attribute), and it is a missing marker rather than a wrong one:
		// the plan proposes creating the child rather than binding it to
		// something it guessed.
		//
		// Nothing here reads anything, and nothing here changes when
		// discovery runs relative to resolution. See
		// [projection.ReadInstances]'s own note on the classifyOrphans
		// hazard: that hazard belongs to a design that makes a FIRST
		// resolution pass non-fatal so a second one can read live values,
		// and this is not one - the refusal it replaces is decided inside a
		// single ordinary pass, before discovery has run at all.
		_, ratifiedRow := LookupType(parent.Resource.Resource.Type)
		deferrable := parentRes.Class == ClassRecordBacked ||
			((parentRes.Class == ClassConcrete || parentRes.Class == ClassNeedsDiscovery) && ratifiedRow)
		if deferrable && r.stringAttrInSchema(parent.Resource.Resource.Type, attrName) {
			return []Part{{Parent: &ParentRef{Instance: parent, Attr: attrName}}}, true
		}

		detail := fmt.Sprintf(
			"%s reads %s.%s, but %q is not an identity attribute of %s. ",
			ident.Subject, parent.String(), attrName, attrName, parent.Resource.Resource.Type)
		switch {
		case parentRes.Class == ClassRecordBacked && r.schemas == nil:
			detail += fmt.Sprintf("%s keeps its whole object in this estate's record store, so any attribute of it can be read - but no provider schemas were available to this run to confirm that %q is one of them.", parent.Resource.Resource.Type, attrName)
			r.errorf(rng, "Not an identity attribute", "%s", detail)
			return nil, false
		case parentRes.Class == ClassRecordBacked:
			detail += fmt.Sprintf("%s keeps its whole object in this estate's record store, so any attribute its schema declares can be read - but its schema declares no string-valued %q.", parent.Resource.Resource.Type, attrName)
			r.errorf(rng, "Not an identity attribute", "%s", detail)
			return nil, false
		case (parentRes.Class == ClassConcrete || parentRes.Class == ClassNeedsDiscovery) && ratifiedRow && r.schemas == nil:
			detail += fmt.Sprintf("%s resolves to an object that is read live before this identity is rendered, so any attribute of it can be read - but no provider schemas were available to this run to confirm that %q is one of them.", parent.String(), attrName)
			r.errorf(rng, "Not an identity attribute", "%s", detail)
			return nil, false
		case (parentRes.Class == ClassConcrete || parentRes.Class == ClassNeedsDiscovery) && ratifiedRow:
			detail += fmt.Sprintf("%s resolves to an object that is read live before this identity is rendered, so any attribute its schema declares can be read - but %s's schema declares no string-valued %q.", parent.String(), parent.Resource.Resource.Type, attrName)
			r.errorf(rng, "Not an identity attribute", "%s", detail)
			return nil, false
		}
		if len(entry.IdentityAttrs) == 0 {
			detail += fmt.Sprintf("No attribute of %s carries its import identity, so nothing about it can be recovered without reading the cloud.", parent.Resource.Resource.Type)
		} else {
			detail += fmt.Sprintf("Only %s can be resolved without reading the cloud; every other attribute is known only after apply.", orList(entry.IdentityAttrs))
		}
		r.errorf(rng, "Not an identity attribute", "%s", detail)
		return nil, false
	}

	if parentRes.Class == ClassConcrete {
		// The parent's identity is already a string, so this stays
		// concrete rather than becoming a formula.
		return []Part{{Literal: parentRes.ImportID}}, true
	}
	return []Part{{Parent: &ParentRef{Instance: parent, Attr: attrName}}}, true
}

// stringAttrInSchema reports whether the provider's schema for typeName
// declares a top-level attribute called attrName whose value can be used as
// a string. It is the whole of the guard on reading a record-backed
// parent's attribute (see [resolver.parentPart]): the record store hydrates
// the parent's entire object, so the only question left is whether the
// attribute exists at all and whether a marker could be built out of it.
//
// Computed is deliberately not consulted here, unlike in
// [resolver.siblingLiteralExpr]. That function reads the CONFIGURATION's
// expression for the attribute and needs the provider to have no path to a
// different value; this one reads the persisted OBJECT, which is whatever
// the provider actually returned, so a Computed attribute is precisely the
// interesting case - random_pet's id and terraform_data's output are both
// Computed and both perfectly readable from a record.
//
// False whenever the run has no schemas, the type has none, the name is not
// a top-level attribute (a nested block is not one), or its type has no
// conversion to string. The caller falls back to its own refusal in every
// such case.
func (r *resolver) stringAttrInSchema(typeName, attrName string) bool {
	if r.schemas == nil {
		return false
	}
	typeSchema, ok := r.schemas[typeName]
	if !ok || typeSchema.Block == nil {
		return false
	}
	attrSchema, ok := typeSchema.Block.Attributes[attrName]
	if !ok {
		return false
	}
	if attrSchema.Type == cty.NilType {
		return false
	}
	if attrSchema.Type.Equals(cty.String) {
		return true
	}
	// GetConversionUnsafe returns nil for identical types, which the check
	// above has already taken, so reaching here means a genuine conversion
	// is needed. Unsafe is the right strictness: number-to-string and
	// bool-to-string are both real, both lossless in this direction, and
	// both already how markers render a non-string identity component.
	return convert.GetConversionUnsafe(attrSchema.Type, cty.String) != nil
}

// siblingLiteralExpr returns the expression parent's own resource block
// gives attrName, and the per-instance scope to evaluate it in, when doing
// so is safe: GitHub issue #220. A sibling's cloud identity
// ([TypeIdentity.IdentityAttrs]) is one thing; an ordinary argument the
// block wrote itself is another, and nothing about the second kind depends
// on the cloud - the value sits in configuration exactly where a plain
// read of it would find one of this package's own identity components.
//
// The boundary is the provider schema's own Computed flag, not a name
// list: an attribute the schema marks Computed, with or without Optional
// alongside it, is one the provider can invent or override - S3's
// Optional+Computed "bucket" argument is the shape this refuses even
// though most callers set it themselves, because Computed is the schema's
// own admission that the stored value need not equal what the
// configuration wrote. Required, and Optional-without-Computed, are the
// only two flag combinations left, and both mean the provider has no path
// of its own to a different value - matching the same rule
// [SynthesizeTypeIdentity]'s locallyDefaultable already applies from the
// opposite direction, to decide what MAY be missing rather than what is
// safe to read.
//
// applicable is false whenever this rule does not apply at all: no
// schemas were supplied to this run, the sibling's type has none, attrName
// names no top-level attribute of it, that attribute is Computed, or the
// sibling's own block does not set it (an Optional-without-Computed
// argument left out is simply absent, not a value to resolve). The caller
// falls back to its own refusal, unchanged, in every such case.
func (r *resolver) siblingLiteralExpr(parent addrs.AbsResourceInstance, attrName string) (expr hcl.Expression, scope instScope, applicable bool) {
	if r.schemas == nil {
		return nil, instScope{}, false
	}
	typeSchema, hasSchema := r.schemas[parent.Resource.Resource.Type]
	if !hasSchema || typeSchema.Block == nil {
		return nil, instScope{}, false
	}
	attrSchema, known := typeSchema.Block.Attributes[attrName]
	if !known || attrSchema.Computed {
		return nil, instScope{}, false
	}

	if !r.enterModuleFor(parent.Module) {
		return nil, instScope{}, false
	}
	rc := r.mod.ResourceByAddr(parent.Resource.Resource)
	if rc == nil {
		return nil, instScope{}, false
	}
	content, _, diags := rc.Config.PartialContent(&hcl.BodySchema{
		Attributes: []hcl.AttributeSchema{{Name: attrName}},
	})
	if diags.HasErrors() {
		return nil, instScope{}, false
	}
	attr, set := content.Attributes[attrName]
	if !set {
		return nil, instScope{}, false
	}

	exp, expOK := r.expansionFor(rc)
	if !expOK || !exp.hasKey(parent.Resource.Key) {
		// The sibling's own expansion already failed, or (should not
		// happen: parent is an instance [resolver.instance] already
		// walked) does not carry this key - either way nothing here can
		// answer, and no new diagnostic belongs to this attribute read.
		return nil, instScope{}, false
	}
	return attr.Expr, exp.scope(parent.Resource.Key), true
}

// isSymbolic reports whether an expression references something whose value
// this package refuses to evaluate and instead handles structurally: a
// managed resource, or each.value when for_each iterates over a resource.
func (r *resolver) isSymbolic(expr hcl.Expression, scope instScope) bool {
	for _, trav := range expr.Variables() {
		switch trav.RootName() {
		case "each":
			// each.value handled structurally rather than evaluated: the
			// for_each parent's instance with the same key, or (#260) the
			// element expression this key's each.value stands for. each.key
			// is never either - it is bound as an ordinary value - so the
			// "value" step is required in both cases.
			if len(trav) >= 2 && isAttrStep(trav[1], "value") &&
				(scope.eachParent != nil || scope.eachValueExpr != nil) {
				return true
			}
		case "count", "var", "local", "path", "terraform", "tofu", "module", "data", "self":
			// Not symbolic: either statically evaluable or a case
			// evalStatic will reject with its own message.
		default:
			if _, bound := scope.vars[trav.RootName()]; bound {
				// A for-comprehension's own loop variable (see
				// [resolver.evalPure]'s identical check), reached here
				// because a caller evaluates one piece of the
				// comprehension - a value clause referencing a sibling,
				// #220's own shape - outside the ForExpr node that would
				// otherwise scope it for hcl.Expression.Variables(). Not a
				// resource reference: evalPure already supplies it through
				// the child EvalContext this same scope builds.
				continue
			}
			// Anything else in a resource argument is a managed resource
			// reference; whether it is declared is checked later.
			if r.managedCovered(trav) {
				// A managed attribute the caller's live read covers
				// (issue #187, [Context.ManagedResults]) is answerable by
				// the static evaluator, exactly as a data source's is once
				// the data-read phase has run. Calling it symbolic here
				// would route it to the keys-from-the-parent's-expansion
				// path, which cannot use a value at all.
				continue
			}
			return true
		}
	}
	return false
}

// evalStatic evaluates an expression that references no managed resources,
// through the module's static evaluator plus a child scope carrying
// each/count for the instance being resolved.
func (r *resolver) evalStatic(expr hcl.Expression, scope instScope, ident configs.StaticIdentifier) (cty.Value, bool) {
	if names := impureCallsIn(expr); len(names) > 0 {
		r.errorf(expr.Range(), "Identity derived from an impure function",
			"%s calls %s, which returns a different value every time it is evaluated. "+
				"An identity is the answer to \"which live object does this block own\", and a value that changes between runs cannot answer it: "+
				"the first apply would create a resource under a name nothing can compute again, and every plan after it would propose creating another one. "+
				"Nothing detects that afterwards, because each run's fabricated identity looks like a perfectly ordinary one. "+
				"Pass the value in as a variable, or let the cloud assign the name and let the tofu-address marker record the ownership.",
			ident.Subject, orListQuoted(names))
		return cty.NilVal, false
	}

	val, diags := r.evalPure(expr, scope, ident)
	if diags.HasErrors() {
		r.appendDiags(diags)
		return cty.NilVal, false
	}
	return val, true
}

// evalPure is the evaluation itself, with its diagnostics handed back
// rather than recorded. [resolver.evalStatic] records them, because an
// argument that will not evaluate is a resolution failure; the config-side
// naming signal (signal.go) discards them, because an argument it cannot
// read is still an argument the configuration sets.
//
// The recover guards against a panic this package's own traversal filter
// cannot see coming: a var.* reference inside a module reached through a
// for_each'd ancestor call (59c, issue #59 phase 3) can resolve, several
// layers down inside [configs.StaticEvaluator]'s own variable-resolution
// machinery, to an expression that itself references the ancestor's own
// each.key or each.value. That resolution runs through
// cfg.Module.StaticEvaluator - built once by internal/configs when the
// module tree is loaded, entirely independent of [resolver.eval]'s own
// per-instance [configs.StaticEvaluator.WithRepetitionData] dup (see
// [instScope.repetition]) - so it never receives repetition data at all and
// panics ("Not Available in Static Context") rather than erroring. This
// package never evaluates such an expression on purpose (see
// [ChildModuleKeys]'s doc: a module call's own for_each is evaluated in its
// parent's scope, never a child's variables), but nothing stops a resource
// argument from referencing one anyway, and a crash here would take the
// whole run down over one identity component this package was always going
// to refuse. Degrading to a clean "cannot evaluate" is the same choice
// [lint.evalStatic] already makes for the class of panic it guards against.
//
// #213 closed the sibling gap this comment used to describe alongside this
// one: a local's own definition referencing each.value/each.key/count.index
// directly, reached from an identity-bearing expression in the SAME
// instance's own arguments. That case no longer reaches this recover at
// all - [instScope.repetition] carries the instance's repetition data on
// [resolver.eval] itself, so every nested [configs.StaticEvaluator] scope
// this instance's own evaluation builds (a local's own definition among
// them) sees the same each/count the instance's other arguments see, and a
// reference outside what is actually known refuses cleanly through
// [configs.StaticIdentifier]'s ordinary diagnostics instead of panicking.
func (r *resolver) evalPure(expr hcl.Expression, scope instScope, ident configs.StaticIdentifier) (val cty.Value, diags tfdiags.Diagnostics) {
	defer func() {
		if rec := recover(); rec != nil {
			val = cty.NilVal
			diags = tfdiags.Diagnostics{}.Append(&hcl.Diagnostic{
				Severity: hcl.DiagError,
				Summary:  "Expression not evaluable here",
				Detail: fmt.Sprintf(
					"%s could not be evaluated: %v. This is most often a reference that, several layers down, depends on a for_each key this package does not propagate across a module boundary (issue #59, 59c) - see live/LIMITATIONS.md, \"child-module\".",
					ident.Subject, rec),
				Subject: expr.Range().Ptr(),
			})
		}
	}()

	var travs []hcl.Traversal
	for _, trav := range expr.Variables() {
		if _, bound := scope.vars[trav.RootName()]; bound {
			// A for-comprehension's own loop variable, bound by
			// forEachOverComprehension below - never "each" or "count",
			// which are answered through [instScope.repetition] and
			// [configs.StaticEvaluator.WithRepetitionData] below instead,
			// at every depth a reference is resolved at, not only this top
			// level. This is a local binding the static evaluator has no
			// notion of, and would either panic on or misreport as an
			// undeclared reference.
			continue
		}
		travs = append(travs, trav)
	}

	refs, refDiags := lang.References(addrs.ParseRef, travs)
	if refDiags.HasErrors() {
		return cty.NilVal, diags.Append(refDiags)
	}

	// scope.repetition is exactly the each.key/each.value/count.index this
	// resource instance's own arguments already see (built once, in
	// [expansion.scope], from the same expansion that decided this
	// instance exists) - never re-derived here, so a local value reached
	// through this expression sees the identical values, not a
	// recomputation of them. See [configs.StaticEvaluator.WithRepetitionData].
	eval := r.eval.WithRepetitionData(scope.repetition)
	hclCtx, ctxDiags := eval.EvalContext(r.ctx, ident, refs)
	if ctxDiags.HasErrors() {
		return cty.NilVal, diags.Append(ctxDiags)
	}
	if hclCtx == nil {
		hclCtx = &hcl.EvalContext{}
	}
	if len(scope.vars) > 0 {
		child := hclCtx.NewChild()
		child.Variables = scope.vars
		hclCtx = child
	}

	val, valDiags := expr.Value(hclCtx)
	if valDiags.HasErrors() {
		return cty.NilVal, diags.Append(valDiags)
	}
	return val, diags
}

// onlyNullIdentityArgument reports whether every diagnostic in a batch is
// exactly [resolver.stringValue]'s "Null identity argument" - the signature
// a resolveExpr call leaves behind when it fully resolved a component's
// value and that value is a clean, wholly-known null, as opposed to any
// other reason resolution failed along the way. An empty batch is not this:
// resolveExpr always records at least one diagnostic when it returns false,
// so an empty slice means the caller mismeasured, not that nothing is wrong.
// See the OmitIfAbsent redirect in [resolver.resolveInstance].
func onlyNullIdentityArgument(diags tfdiags.Diagnostics) bool {
	if len(diags) == 0 {
		return false
	}
	for _, d := range diags {
		if d.Description().Summary != "Null identity argument" {
			return false
		}
	}
	return true
}

// siblingApplyRefusal is one identity argument [resolver.stringValue] refused
// because a managed value this run obtained is unknown until a sibling
// resource is applied: which argument, which sibling, and where the
// diagnostic it raised sits in r.diags.
//
// The diagnostic is raised exactly as it always was. It is REMOVED, by index,
// only if the whole instance turns out to have failed for this reason and
// nothing else - see [resolver.instanceNeedsSiblingApply]. Raising it first
// and withdrawing it later, rather than suppressing it up front, is what
// keeps #221's guarantee that a component failing for an unrelated reason
// still reports its own refusal beside this one.
type siblingApplyRefusal struct {
	arg     string
	sibling string
	diagIdx int
}

func (r *resolver) stringValue(val cty.Value, expr hcl.Expression, ident configs.StaticIdentifier) (string, bool) {
	return r.stringValueIn(val, expr, instScope{}, ident)
}

func (r *resolver) stringValueIn(val cty.Value, expr hcl.Expression, scope instScope, ident configs.StaticIdentifier) (string, bool) {
	if val.IsMarked() {
		r.errorf(expr.Range(), "Identity derived from a sensitive value",
			"%s is derived from a sensitive value. An import identity is written to logs and plan output, so it cannot be sensitive. If the value is not genuinely secret - a data source such as tfe_outputs that marks its whole result sensitive is the common case - wrap it in nonsensitive(...) to use it here.", ident.Subject)
		return "", false
	}
	if val.IsNull() {
		r.errorf(expr.Range(), "Null identity argument",
			"%s evaluated to null, which cannot be part of an import identity.", ident.Subject)
		return "", false
	}
	if !val.IsWhollyKnown() {
		r.errorf(expr.Range(), "Non-static identity argument",
			"%s cannot be evaluated from configuration alone. Every part of an identity must be a constant, or derived from variables, locals and pure functions, or a reference to another resource's identity attribute. A function that returns a different value on each call - uuid(), timestamp(), bcrypt() - evaluates to an unknown value here, including when it is reached through a local or written in .tf.json.", ident.Subject)
		// The diagnostic above is unchanged and stays. What is recorded here
		// is only WHERE the unknown came from, for the caller that may
		// withdraw it: an unknown this run's own managed results supplied is
		// a value waiting on a sibling's apply, which is a different fact
		// from an argument the configuration cannot express. See
		// managedprovenance.go and [DiscoverySiblingApply].
		if sib, ok := r.managedFrom(expr, scope); ok {
			r.pendingSiblingApply = append(r.pendingSiblingApply, siblingApplyRefusal{
				arg:     ident.Subject,
				sibling: sib,
				diagIdx: len(r.diags) - 1,
			})
		}
		return "", false
	}
	str, err := convert.Convert(val, cty.String)
	if err != nil {
		r.errorf(expr.Range(), "Non-string identity argument",
			"%s cannot be used as part of an import identity: %s.", ident.Subject, err)
		return "", false
	}
	return str.AsString(), true
}

func (r *resolver) identifier(addr addrs.AbsResourceInstance, attrName string, rng hcl.Range) configs.StaticIdentifier {
	return configs.StaticIdentifier{
		Module:    addr.Module.Module(),
		Subject:   fmt.Sprintf("%s.%s", addr.String(), attrName),
		DeclRange: rng,
	}
}

// resourceCloudScope is [Resolution.cloudScope]'s whole implementation: a
// [cloudScopeKey] that agrees for two resources only when they plausibly
// target the same account and region, so [resolver.checkCollisions] can
// tell a genuine duplicate-identity collision from two same-named
// resources that simply live in different places.
//
// It has two independent inputs, both general across every managed
// resource type and every provider, not just AWS:
//
//   - The resource's resolved absolute provider configuration
//     ([providerscope.ResolveResource]), which already walks every
//     enclosing module call's own `providers = { ... }` mapping. Two
//     module calls of the same child module, each remapping `aws` to a
//     different aliased provider block, are exactly this: same resource
//     address shape inside the module, different account or region
//     outside it (found in the corpus: simpleinfra's dev-desktops calls
//     ./aws-region once per region this way).
//   - A literal `region` argument set directly on the resource body, read
//     the same PartialContent-then-static-evaluate way every identity
//     argument already is. This one is AWS-specific in practice - it is
//     the per-resource region override the AWS provider has exposed on
//     almost every resource type since it adopted the endpoints
//     framework - but nothing here names a resource type to reach it: any
//     resource in any provider that happens to declare a statically
//     known `region` argument gets the same treatment. (Found in the
//     corpus: govuk-infrastructure's chat estate declares the identical
//     aws_cloudwatch_log_group name "/aws/bedrock" twice, once with
//     region = "eu-west-1" and once with region = "eu-west-2" - the same
//     provider configuration both times, since neither block uses a
//     provider alias, so only the region argument tells them apart.)
//
// The provider configuration always contributes [cloudScopeKey.base], which
// [resolver.checkCollisions] requires to match exactly. The region comes
// from [resolver.effectiveRegion] - the literal argument when the resource
// states one, the resolved provider configuration's own static `region`
// otherwise (#217) - and lands in [cloudScopeKey.region]/regionKnown, which
// checkCollisions treats altogether differently: regionKnown false is a
// wildcard that never rules out a collision on its own, so a resource this
// function could not place in any region is exactly as suspect a duplicate
// as it always was, not silently cleared by an unrelated sibling's known
// one. A resource in a single-region, single-account estate never states a
// region anywhere this can find one, so every instance shares base alone
// with regionKnown false - the collision key in that case reduces to
// exactly what it was before this existed, Type+identity, with no drop in
// sensitivity.
//
// This is deliberately best-effort in the OTHER direction: a `region`
// argument that fails to evaluate statically (references something a plain
// identity argument could not either) is silently treated as unknown
// rather than refused, because collision detection choosing not to
// disambiguate a resource pair is strictly safer than a spurious refusal
// over an argument nothing else in this run needed to read. See
// [resolver.staticRegionAttr] and [resolver.providerRegionAttr].
func (r *resolver) resourceCloudScope(rc *configs.Resource, scope instScope) cloudScopeKey {
	abs := providerscope.ResolveResource(r.curCfg, rc)
	region, ok := r.effectiveRegion(rc, abs, scope)
	return cloudScopeKey{base: abs.String(), region: region, regionKnown: ok}
}

// effectiveRegion is [resolver.resourceCloudScope]'s own region component
// (#217): the resource's own literal `region` argument when it states one,
// falling back to the region its resolved provider configuration itself
// declares statically when it does not. Two resources of the same identity
// that both target the same effective region must produce the same scope
// string regardless of which of them spelled the region out - a resource
// stating `region = "us-east-1"` and its sibling silently inheriting the
// identical region from the enclosing `provider "aws"` block collided
// before #217's own regression and must keep colliding now. ok is false,
// and no region joins the scope at all, whenever NEITHER side resolves
// statically: [resolver.staticRegionAttr]'s and
// [resolver.providerRegionAttr]'s own doc comments explain why that must
// fall back to "no override" rather than a refusal - the same reasoning
// extends here in the direction #217 asks for, since an unknown region on
// either side of a comparison must collide (report) rather than silently
// distinguish two resources that might be identical.
func (r *resolver) effectiveRegion(rc *configs.Resource, abs addrs.AbsProviderConfig, scope instScope) (string, bool) {
	if region, ok := r.staticRegionAttr(rc, scope); ok {
		return region, true
	}
	return r.providerRegionAttr(abs)
}

// staticRegionAttr reads a resource's own `region` argument, when the body
// sets one and it evaluates to a known, non-null string from configuration
// alone. It never records a diagnostic: unlike every other argument this
// package reads, a `region` override is consulted only to sharpen
// [resolver.checkCollisions], not to build the identity itself, so a
// failure here must fall back to "no override", not refuse the run.
func (r *resolver) staticRegionAttr(rc *configs.Resource, scope instScope) (string, bool) {
	content, _, diags := rc.Config.PartialContent(&hcl.BodySchema{
		Attributes: []hcl.AttributeSchema{{Name: "region"}},
	})
	if diags.HasErrors() {
		return "", false
	}
	attr, ok := content.Attributes["region"]
	if !ok || r.isSymbolic(attr.Expr, scope) {
		return "", false
	}
	ident := configs.StaticIdentifier{
		Module:    r.modInst.Module(),
		Subject:   fmt.Sprintf("%s.region", rc.Addr().String()),
		DeclRange: attr.Range,
	}
	val, evalDiags := r.evalPure(attr.Expr, scope, ident)
	if evalDiags.HasErrors() || val.IsMarked() || val.IsNull() || !val.IsWhollyKnown() {
		return "", false
	}
	str, err := convert.Convert(val, cty.String)
	if err != nil {
		return "", false
	}
	return str.AsString(), true
}

// providerRegionAttr reads abs's own provider configuration block's
// `region` argument, when one exists and it evaluates to a known, non-null
// string from configuration alone - [resolver.effectiveRegion]'s fallback
// for a resource whose own body states no `region` override, so that
// resource inherits the region its provider block would actually apply
// (#217).
//
// The block is looked for in abs's OWN module, which is usually but not
// always the root. [providerscope.Resolve] anchors at the root for every
// shape except one: a module that declares its own content-bearing
// `provider` block for the (name, alias) pair a resource in it names is
// served by that block directly, and Resolve returns Module: cur.Path for
// it - stock OpenTofu's own behaviour, and the shape internal/live/lint's
// checkModuleProviderBlocks (#201) exists to admit. This function used to
// search r.rootCfg.Module unconditionally on the stated claim that "every
// return path sets Module: addrs.RootModule", which that return path
// contradicts. The consequence was not academic once #250 routed a
// {Cloud: "region"} component through here: a resource under a child
// module's own provider block rendered its identity with the ROOT block's
// region when the root had one, and refused as region-unknown when it did
// not, both silently.
//
// The module's own evaluator is used for the same reason: a provider
// block's expressions reference the variables and locals of the module the
// block is written in, which is the module this lookup found it in.
//
// The match against abs is the same deterministic shape
// internal/live/dataread/analyze.go's findProviderConfig already uses for
// the identical problem (an [addrs.AbsProviderConfig] naming a provider
// type by its fully-qualified source, a config block naming it by local
// name): iterate the module's ProviderConfigs in sorted key order,
// filtering first by Alias and then by comparing
// [configs.Module.ProviderForLocalConfig] of the block's own local name
// against abs.Provider. Never [configs.Module.LocalNameForProvider] run in
// the other direction - that lookup is keyed by the very FQN this function
// is trying to resolve and picks one winner out of a Go map with no stable
// order, which is exactly the nondeterminism a lookup keyed by the thing
// being resolved produces (a real defect elsewhere in this codebase, not a
// hypothetical one).
//
// Like [resolver.staticRegionAttr], this never records a diagnostic and
// never treats an unresolvable region as anything other than "no
// override" - a provider block this function cannot resolve, a `region`
// this run cannot statically evaluate, or a provider configured with its
// own `for_each` (whose `region` may differ per key, which this function
// declines to guess at) all report ok=false, which [resolver.effectiveRegion]
// (and #217's own safety direction) treats as "cannot distinguish these two
// resources by region", not as a value of "no region".
func (r *resolver) providerRegionAttr(abs addrs.AbsProviderConfig) (string, bool) {
	if r.rootCfg == nil || r.rootCfg.Module == nil {
		return "", false
	}
	owner := r.rootCfg.Descendent(abs.Module)
	if owner == nil || owner.Module == nil {
		return "", false
	}
	mod := owner.Module

	keys := make([]string, 0, len(mod.ProviderConfigs))
	for k := range mod.ProviderConfigs {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var pc *configs.Provider
	for _, k := range keys {
		cand := mod.ProviderConfigs[k]
		if cand.Alias != abs.Alias {
			continue
		}
		if mod.ProviderForLocalConfig(addrs.LocalProviderConfig{LocalName: cand.Name}) != abs.Provider {
			continue
		}
		pc = cand
		break
	}
	if pc == nil || pc.Config == nil || pc.ForEach != nil {
		return "", false
	}

	content, _, diags := pc.Config.PartialContent(&hcl.BodySchema{
		Attributes: []hcl.AttributeSchema{{Name: "region"}},
	})
	if diags.HasErrors() {
		return "", false
	}
	attr, ok := content.Attributes["region"]
	if !ok || r.isSymbolic(attr.Expr, instScope{}) {
		return "", false
	}

	// A provider block's own expressions live in, and reference variables
	// and locals of, the module the BLOCK is written in - which very often
	// differs from whatever module the resolver is currently walking a
	// resource in ([resolver.mod]'s own doc comment). r.eval is swapped to
	// that module's own pure evaluator for the one call below and restored
	// immediately after; nothing else in this function or its caller
	// depends on r.eval, so the swap has no visible effect beyond it.
	saved := r.eval
	r.eval = mod.StaticEvaluator.Pure()
	defer func() { r.eval = saved }()

	ident := configs.StaticIdentifier{
		Module:    abs.Module,
		Subject:   fmt.Sprintf("provider.%s.region", pc.Name),
		DeclRange: attr.Range,
	}
	val, evalDiags := r.evalPure(attr.Expr, instScope{}, ident)
	if evalDiags.HasErrors() || val.IsMarked() || val.IsNull() || !val.IsWhollyKnown() {
		return "", false
	}
	str, err := convert.Convert(val, cty.String)
	if err != nil {
		return "", false
	}
	return str.AsString(), true
}

func (r *resolver) errorf(rng hcl.Range, summary, format string, args ...any) {
	diag := &hcl.Diagnostic{
		Severity: hcl.DiagError,
		Summary:  summary,
		Detail:   fmt.Sprintf(format, args...),
		Subject:  rng.Ptr(),
	}
	if r.curInstanceAddr != "" {
		// Freshly built, so there is nothing to preserve: unlike
		// [resolver.appendDiags], no wrap is needed here.
		diag.Extra = InstanceFailure{Addr: r.curInstanceAddr}
	}
	r.diags = r.diags.Append(diag)
}

// appendDiags is [resolver.errorf] for diagnostics this package did not
// itself construct - an [addrs.ParseRef] failure, an hcl.Body's own
// PartialContent diagnostics, [resolver.evalPure]'s. It takes the same
// argument shape [tfdiags.Diagnostics.Append] itself does (hcl.Diagnostics,
// *hcl.Diagnostic, tfdiags.Diagnostics, ...) rather than picking one, since
// its callers pass whichever shape their own diagnostics already came back
// as.
//
// Every diagnostic gets the same [InstanceFailure] tag errorf attaches,
// wrapping whatever Extra it already carries rather than discarding it (see
// [instanceFailureDiag] and [InstanceFailure.UnwrapDiagnosticExtra]) -
// otherwise a data-source reference refused inside a failing component
// would lose the [configs.RefusedReference] internal/live/dataread and
// internal/live/check's classifyDataSite both depend on.
func (r *resolver) appendDiags(new ...any) {
	if r.curInstanceAddr == "" {
		r.diags = r.diags.Append(new...)
		return
	}
	var toTag tfdiags.Diagnostics
	toTag = toTag.Append(new...)
	if len(toTag) == 0 {
		return
	}
	tagged := make(tfdiags.Diagnostics, len(toTag))
	for i, d := range toTag {
		tagged[i] = instanceFailureDiag{Diagnostic: d, tag: InstanceFailure{Addr: r.curInstanceAddr, inner: d.ExtraInfo()}}
	}
	r.diags = r.diags.Append(tagged)
}

// instanceFailureDiag decorates a [tfdiags.Diagnostic] with an
// [InstanceFailure], leaving everything else about it - including whatever
// Extra it already carried, reachable through InstanceFailure's own unwrap
// chain - untouched. See [resolver.appendDiags].
type instanceFailureDiag struct {
	tfdiags.Diagnostic
	tag InstanceFailure
}

func (d instanceFailureDiag) ExtraInfo() interface{} { return d.tag }

// ---- expansion -------------------------------------------------------

// expansion is how a resource block expands into instances, plus whatever
// each instance needs in scope to evaluate its own arguments.
type expansion struct {
	keys []addrs.InstanceKey

	// counted is set when the expansion came from count, so that
	// count.index is in scope.
	counted bool

	// eachValues holds each.value per key for a for_each over a static
	// collection. Nil when for_each iterates over another resource.
	//
	// It is TOTAL over keys for an expansion built by evaluating the
	// for_each expression whole, and PARTIAL for a keyOnly expansion, where
	// a key is present only if that one key's own value expression
	// evaluated on its own. Absence and cty.NilVal must therefore not be
	// conflated: [expansion.scope] reads presence, so a key this map does
	// not mention keeps each.value unbound and refusing.
	eachValues map[addrs.InstanceKey]cty.Value

	// eachValueExprs is #260: for a key whose element did NOT evaluate as a
	// value, the element's own value EXPRESSION, so that each.value.<attr>
	// can select one attribute out of it structurally instead of asking for
	// the whole element. One dynamic attribute inside an element leaves
	// eachValues without that key, which used to refuse every literal
	// sibling beside it; a selection is per-attribute where a value is
	// all-or-nothing.
	//
	// Read only where eachValues has no entry for the key, so a key whose
	// value proved keeps its old binding and its old behaviour exactly.
	eachValueExprs map[addrs.InstanceKey]elemBinding

	// eachValueDeferred is #354: the element's own expression kept BESIDE a
	// bound value rather than instead of it, for a key whose value is an
	// object the declared-type conversion produced with unknown attributes in
	// it ([convertedElems]).
	//
	// eachValueExprs and this are not two spellings of one thing. That one
	// replaces a value nothing could evaluate; this one supplements a value
	// that IS bound and IS what the module sees, for the attributes of it
	// that came back unknown. The difference is visible in
	// [resolver.isSymbolic], which sees eachValueExprs' binding and routes
	// the whole argument down the symbolic path, and does not see this one at
	// all - so an argument reading a KNOWN attribute of the bound value
	// resolves exactly as it did before, and only one that evaluated to an
	// unknown ever consults this.
	eachValueDeferred map[addrs.InstanceKey]elemBinding

	// eachParent is set when for_each iterates over another managed
	// resource: each.value is then that resource's instance with the same
	// key, which is a symbolic reference rather than a value.
	eachParent *addrs.Resource

	// keyOnly marks an expansion built by #178's key-set fix
	// (staticForEachKeys in localvalue.go): the for_each source is an
	// object constructor whose keys are statically known but whose values
	// are not - typically a managed resource's attribute reached through
	// one of them - so eachValues is left unpopulated for those keys rather
	// than filled with a guess. each.key resolves normally, at any depth
	// (directly, or reached through a local's own definition - #213);
	// each.value for such a key is not covered by
	// [instances.RepetitionData.EachValue] and refuses cleanly with
	// "Dynamic value in static context" ([configs.StaticEvaluator]'s own
	// diagnostic, not a recovered panic - see [resolver.evalPure]).
	// Resolving each.value symbolically in this position - the for_each
	// half of the local-values fix, as opposed to the plain-reference half
	// [resolver.namedLeaf] builds - is a further extension this fix does
	// not make.
	keyOnly bool

	// managedFrom is the managed resource block, module-relative, whose
	// covered-but-not-wholly-known value this expansion's for_each was built
	// from. Empty when the for_each drew on nothing of the sort, which is
	// every expansion in a run that supplied no [Context.ManagedResults].
	//
	// It is the provenance carrier for each.key and each.value: an identity
	// argument written `each.value.name` names no resource at all, so nothing
	// in the argument itself can say where its unknown came from. See
	// managedprovenance.go.
	managedFrom string
}

func (e *expansion) hasKey(key addrs.InstanceKey) bool {
	for _, k := range e.keys {
		if k == key {
			return true
		}
	}
	return false
}

// describe explains an expansion in an error about a bad instance
// reference.
func (e *expansion) describe(res addrs.Resource) string {
	if len(e.keys) == 0 {
		return fmt.Sprintf("%s expands to no instances at all.", res.String())
	}
	strs := make([]string, 0, len(e.keys))
	for _, k := range e.keys {
		strs = append(strs, res.Instance(k).String())
	}
	return fmt.Sprintf("%s expands to: %s.", res.String(), strings.Join(strs, ", "))
}

// scope builds the per-instance evaluation scope for key: the same
// each.key/each.value/count.index this instance's own arguments already
// see, carried as [instances.RepetitionData] so a local's own definition -
// reached transitively, not only referenced directly - sees exactly the
// same values (see [instScope.repetition] and #213). A field left at
// cty.NilVal below is deliberate, not an oversight: each.value under
// e.eachParent or e.keyOnly is symbolic (this package refuses to evaluate
// it, and resolves each.value.<attr> structurally instead - see
// [resolver.resolveTraversal]), so leaving RepetitionData.EachValue unset
// makes a bare each.value reached through a local refuse exactly as a
// direct one already does, rather than fabricate a value nothing here
// actually knows.
func (e *expansion) scope(key addrs.InstanceKey) instScope {
	sc := instScope{key: key, eachParent: e.eachParent, managedFrom: e.managedFrom}
	switch {
	case e.counted:
		idx, ok := key.(addrs.IntKey)
		if !ok {
			return sc
		}
		sc.repetition = instances.RepetitionData{CountIndex: cty.NumberIntVal(int64(idx))}
	case e.keyOnly:
		// Tested before eachValues, because a keyOnly expansion may carry
		// values for SOME of its keys: the key set was proven structurally,
		// and separately, key by key, some of the value expressions beside
		// those keys evaluated on their own. A key with a proven value gets
		// it; a key without one is left exactly as it was before those
		// values were carried at all - only each.key bound, so a bare
		// each.value or an each.value.<attr> for that instance refuses with
		// "Dynamic value in static context" rather than reading a value
		// nothing here knows.
		sc.repetition = instances.RepetitionData{EachKey: keyValue(key)}
		if v, ok := e.eachValues[key]; ok {
			sc.repetition.EachValue = v
			// #354: the value is the binding, and where it is an object the
			// declared-type conversion produced, some of its attributes may be
			// unknown. The element's own expression rides alongside for those,
			// and for nothing else - see [expansion.eachValueDeferred].
			if b, ok := e.eachValueDeferred[key]; ok && b.expr != nil {
				sc.eachValueDeferred = &b
			}
			break
		}
		// #260: no proven value, but the element's own expression is in
		// hand. each.value stays unbound - nothing here knows the element as
		// a VALUE, and binding a partial one is what would let try() fall
		// back over an attribute that is present and merely unresolvable -
		// and the expression is threaded instead, for
		// [resolver.eachValueSelect] to select one attribute out of.
		if b, ok := e.eachValueExprs[key]; ok && b.expr != nil {
			sc.eachValueExpr = &b
		}
	case e.eachValues != nil:
		sc.repetition = instances.RepetitionData{EachKey: keyValue(key), EachValue: e.eachValues[key]}
		// #354, the tolerant-retry route: the value is the binding and the
		// element's own expression rides beside it for the unknowns inside
		// that value. Empty for every expansion built any other way.
		if b, ok := e.eachValueDeferred[key]; ok && b.expr != nil {
			sc.eachValueDeferred = &b
		}
	case e.eachParent != nil:
		// each.value is symbolic here, so only each.key has a value; a
		// reference to each.value is handled structurally instead.
		sc.repetition = instances.RepetitionData{EachKey: keyValue(key)}
	}
	return sc
}

// instScope is the per-instance evaluation scope: the instance key, the
// each.key/each.value/count.index values that are known (handed to
// [configs.StaticEvaluator.WithRepetitionData] so they reach a local's own
// definition, not only the top-level expression - see #213), any other
// top-level binding this package's own for-comprehension handling supplies
// (a loop variable name, never "each" or "count"), and the parent resource
// that each.value stands for when it is not known.
type instScope struct {
	key        addrs.InstanceKey
	repetition instances.RepetitionData
	vars       map[string]cty.Value
	eachParent *addrs.Resource

	// eachValueExpr is set when this instance's each.value is known as an
	// EXPRESSION rather than as a value (#260): the element of the for_each
	// source that belongs to this key, plus the scope and module instance it
	// was written in. It and repetition.EachValue are mutually exclusive by
	// construction ([expansion.scope]), so nothing has to decide between a
	// value and a selection - only one is ever present.
	//
	// A pointer, not a value: elemBinding carries an instScope of its own.
	eachValueExpr *elemBinding

	// eachValueDeferred is #354's layered binding: a value IS bound in
	// repetition.EachValue and this carries the element's own expression
	// beside it, for the attributes of that value that came back unknown.
	//
	// Deliberately invisible to [resolver.isSymbolic], unlike eachValueExpr:
	// an argument reading this element resolves through the ordinary
	// evaluation first and reaches this only where that evaluation produced
	// an unknown, so nothing that resolves today can be re-routed by it. See
	// [resolver.eachValueDeferredParts].
	eachValueDeferred *elemBinding

	// managedFrom is [expansion.managedFrom] carried down to the instance,
	// so that an identity argument reading each.* can say where its unknown
	// came from. Empty for every scope built anywhere else, which is what
	// makes an instScope{} literal - and there are dozens - inert.
	managedFrom string
}

func (r *resolver) expansionFor(rc *configs.Resource) (*expansion, bool) {
	key := r.expKey(rc)
	if exp, ok := r.expansions[key]; ok {
		return exp, true
	}
	if r.expFailed[key] {
		return nil, false
	}
	if r.expVisit[key] {
		// rc.Addr(), not key: expKey joins the module instance and the
		// address with a NUL byte, which rendered into the diagnostic as a
		// control character ahead of the address. resolve.go's other cycle
		// message (Circular identity reference) uses the address directly
		// and always has. Found by the #101 message audit.
		r.errorf(rc.DeclRange, "Circular for_each reference",
			"The instances of %s depend on themselves, directly or through other resources' for_each expressions.", rc.Addr().String())
		r.expFailed[key] = true
		return nil, false
	}
	r.expVisit[key] = true
	defer delete(r.expVisit, key)

	exp, ok := r.buildExpansion(rc)
	if !ok {
		r.expFailed[key] = true
		return nil, false
	}
	r.expansions[key] = exp
	return exp, true
}

func (r *resolver) buildExpansion(rc *configs.Resource) (*expansion, bool) {
	addr := rc.Addr()

	switch {
	case rc.Count != nil:
		return r.countExpansion(rc)

	case rc.ForEach != nil:
		return r.forEachExpansion(rc)

	case rc.Enabled != nil:
		ident := r.moduleIdentifier(addr.String()+" lifecycle.enabled", rc.Enabled.Range())
		val, ok := r.evalStatic(rc.Enabled, instScope{}, ident)
		if !ok {
			return nil, false
		}
		if val.IsMarked() {
			// Same crash as count above, one branch further down: b.False()
			// asserts the value is unmarked and panics when it is not.
			// internal/command/e2etest/testdata/ephemeral-repetition/enabled
			// is the reproducing configuration.
			r.errorf(rc.Enabled.Range(), "Sensitive lifecycle.enabled expression",
				"Whether %s exists is decided by a sensitive or ephemeral value. Existence decides which markers this run writes and which live objects it claims, so it cannot come from a value this run may not record.", addr.String())
			return nil, false
		}
		b, err := convert.Convert(val, cty.Bool)
		if err != nil || !b.IsKnown() || b.IsNull() {
			r.errorf(rc.Enabled.Range(), "Non-static lifecycle.enabled expression",
				"Whether %s exists cannot be determined from configuration alone, so its instances cannot be enumerated.", addr.String())
			return nil, false
		}
		if b.False() {
			return &expansion{}, true
		}
		return &expansion{keys: []addrs.InstanceKey{addrs.NoKey}}, true

	default:
		return &expansion{keys: []addrs.InstanceKey{addrs.NoKey}}, true
	}
}

// countExpansion resolves rc.Count. It is the same evaluation buildExpansion
// always did, except that length(<resource>) - the whole expression, or
// nested under a comparison, a ternary, or parentheses, the shapes count
// actually uses in the corpus (#178) - is rewritten to that resource's own
// already-computed instance count before the generic static evaluator ever
// sees it. That is the count analogue of forEachOverResource: the keys come
// from the resolver's own expansion of the referenced resource, never from
// evaluating the referenced resource's attributes.
func (r *resolver) countExpansion(rc *configs.Resource) (*expansion, bool) {
	addr := rc.Addr()
	expr := rc.Count
	if rewritten, changed, ok := r.rewriteResourceLength(expr); changed {
		// changed is true only when a length(<resource>) call was actually
		// matched, so !ok here always means that resource's own expansion
		// failed - the error is already on r.diags (see expansionFor and
		// the cycle guard above it), and there is nothing more to say.
		// A resource reference in a shape this does not recognize comes
		// back as changed=false instead, expr unmodified, and falls
		// through to evalStatic below, which raises the ordinary "Dynamic
		// value in static context" refusal for it - the honest answer,
		// since this rule explains length(<resource>), not every shape
		// that might wrap one.
		if !ok {
			return nil, false
		}
		expr = rewritten
	}

	ident := r.moduleIdentifier(addr.String()+" count", rc.Count.Range())
	mark := len(r.diags)
	val, ok := r.evalStatic(expr, instScope{}, ident)
	if !ok {
		// A count whose only obstacle is a module argument this module's
		// caller built out of a literal skeleton and one unresolvable leaf.
		// length(var.worker_groups) over `worker_groups = [{...}, {...}]`
		// is the shape: the list's LENGTH is written in the caller's
		// configuration whatever any element evaluates to.
		//
		// Accepted only when the retry produces a wholly known value, so a
		// count that merely moves from "could not be computed" to "computed
		// to an unknown" keeps the diagnostic it already had rather than
		// trading it for a vaguer one. See [resolver.tolerantRetry].
		retried, retryOK := r.tolerantRetry(expr, instScope{}, ident)
		if !retryOK || !retried.IsWhollyKnown() || retried.IsNull() {
			return nil, false
		}
		r.diags = r.diags[:mark]
		val = retried
	}
	if val.IsMarked() {
		// Before the marked check, gocty.FromCtyValue below panicked
		// ("value is marked, so must be unmarked first") and took the
		// whole run down. An ephemeral variable in count is the shortest
		// way there - internal/command/e2etest/testdata/
		// ephemeral-repetition/count is exactly that configuration - and
		// a sensitive one reaches it too. for_each already refused its
		// own marked value a few lines below; count did not.
		r.errorf(rc.Count.Range(), "Sensitive count expression",
			"The count for %s is sensitive or ephemeral, so the instance keys it produces cannot become part of resource addresses. Addresses are written to markers, logs and plan output.", addr.String())
		return nil, false
	}
	if !val.IsKnown() || val.IsNull() {
		r.errorf(rc.Count.Range(), "Non-static count expression",
			"The count for %s evaluated to null, or to a value not knowable from configuration alone. Instance keys are the addresses a projection binds against, so a count has to be a whole number this run can compute before anything is read from the cloud; guessing a cardinality would silently drop or invent instances.", addr.String())
		return nil, false
	}
	num, err := convert.Convert(val, cty.Number)
	if err != nil {
		r.errorf(rc.Count.Range(), "Invalid count",
			"The count for %s is not a number: %s.", addr.String(), err)
		return nil, false
	}
	var n int
	if err := gocty.FromCtyValue(num, &n); err != nil {
		r.errorf(rc.Count.Range(), "Invalid count",
			"The count for %s is not a whole number: %s.", addr.String(), err)
		return nil, false
	}
	if n < 0 {
		r.errorf(rc.Count.Range(), "Invalid count",
			"The count for %s is negative.", addr.String())
		return nil, false
	}
	exp := &expansion{counted: true}
	for i := 0; i < n; i++ {
		exp.keys = append(exp.keys, addrs.IntKey(i))
	}
	return exp, true
}

// lengthOfResource reports whether expr is exactly length(<resource>): a
// call to the length function whose single argument is a bare reference to
// a whole managed resource that itself uses count or for_each. Only a
// multi-instance resource makes length(<resource>) mean "how many
// instances there are": OpenTofu evaluates length() of a single-instance
// resource reference over that resource's own object attributes instead, a
// different number this resolver has no way to reproduce without a schema
// read, so that case is left alone for the generic evaluator's own
// refusal rather than guessed at.
func (r *resolver) lengthOfResource(expr hcl.Expression) (*configs.Resource, bool) {
	call, diags := hcl.ExprCall(expr)
	if diags.HasErrors() {
		return nil, false
	}
	if call.Name != "length" || len(call.Arguments) != 1 {
		return nil, false
	}
	trav, diags := hcl.AbsTraversalForExpr(call.Arguments[0])
	if diags.HasErrors() {
		return nil, false
	}
	ref, refDiags := addrs.ParseRef(trav)
	if refDiags.HasErrors() {
		return nil, false
	}
	resAddr, ok := ref.Subject.(addrs.Resource)
	if !ok || len(ref.Remaining) > 0 || resAddr.Mode != addrs.ManagedResourceMode {
		return nil, false
	}
	parentRC := r.mod.ResourceByAddr(resAddr)
	if parentRC == nil || (parentRC.Count == nil && parentRC.ForEach == nil) {
		return nil, false
	}
	return parentRC, true
}

// rewriteResourceLength looks for length(<resource>) inside expr and
// replaces each one with that resource's own already-computed instance
// count from expansionFor - the memoized, cycle-guarded expansion every
// other resource's count and for_each already goes through. It recurses
// through parentheses, ternaries and comparisons, because those are the
// wrapped forms count actually uses in the corpus (#178); it does not
// invent handling for shapes that are not there.
//
// changed reports whether expr referenced a managed resource at all. When
// changed is false, rewritten is expr itself and ok is always true: the
// caller can evaluate it exactly as before, because nothing here applies.
// When changed is true, ok is false in two different situations the caller
// must tell apart: a matched length(<resource>) call whose resource failed
// to resolve (the failure is already on r.diags - give up, do not fall
// back), or a resource reference in a shape this function does not
// recognize (nothing has been reported yet - the caller should evaluate the
// original expr through the generic path instead, which raises the
// ordinary "Dynamic value in static context" refusal for it).
func (r *resolver) rewriteResourceLength(expr hcl.Expression) (rewritten hcl.Expression, changed bool, ok bool) {
	if !r.isSymbolic(expr, instScope{}) {
		return expr, false, true
	}

	if parentRC, isLen := r.lengthOfResource(expr); isLen {
		parentExp, expOK := r.expansionFor(parentRC)
		if !expOK {
			return nil, true, false
		}
		return &hclsyntax.LiteralValueExpr{
			Val:      cty.NumberIntVal(int64(len(parentExp.keys))),
			SrcRange: expr.Range(),
		}, true, true
	}

	switch e := expr.(type) {
	case *hclsyntax.ParenthesesExpr:
		inner, ch, ok := r.rewriteResourceLength(e.Expression)
		if !ok {
			return nil, ch, false
		}
		cp := *e
		cp.Expression = inner.(hclsyntax.Expression)
		return &cp, true, true

	case *hclsyntax.ConditionalExpr:
		cond, ch, ok := r.rewriteResourceLength(e.Condition)
		if !ok {
			return nil, ch, false
		}
		tr, ch, ok := r.rewriteResourceLength(e.TrueResult)
		if !ok {
			return nil, ch, false
		}
		fr, ch, ok := r.rewriteResourceLength(e.FalseResult)
		if !ok {
			return nil, ch, false
		}
		cp := *e
		cp.Condition = cond.(hclsyntax.Expression)
		cp.TrueResult = tr.(hclsyntax.Expression)
		cp.FalseResult = fr.(hclsyntax.Expression)
		return &cp, true, true

	case *hclsyntax.BinaryOpExpr:
		lhs, ch, ok := r.rewriteResourceLength(e.LHS)
		if !ok {
			return nil, ch, false
		}
		rhs, ch, ok := r.rewriteResourceLength(e.RHS)
		if !ok {
			return nil, ch, false
		}
		cp := *e
		cp.LHS = lhs.(hclsyntax.Expression)
		cp.RHS = rhs.(hclsyntax.Expression)
		return &cp, true, true
	}

	// isSymbolic is true but expr is none of the recognized wrapper shapes:
	// a bare reference, an attribute traversal, a different function, a
	// for expression, and so on. Left unrecognized on purpose - the caller
	// falls back to the generic evaluator's own message rather than a
	// bespoke one here.
	return nil, false, false
}

func (r *resolver) forEachExpansion(rc *configs.Resource) (*expansion, bool) {
	addr := rc.Addr()
	expr := rc.ForEach

	// for_each over another resource: the keys are that resource's keys,
	// which is config data even though the values are not.
	if r.isSymbolic(expr, instScope{}) {
		if fe, ok := unwrapForExpr(expr); ok {
			if got, exOK, applicable := r.forEachOverComprehension(rc, fe); applicable {
				return got, exOK
			}
		}
		// toset([for x in <static collection> : <expr>]) - a keyless
		// (tuple-producing) comprehension, the idiomatic way to turn a
		// for-expression into the set for_each requires. Distinct from the
		// case just above: there, the collection clause IS the resource
		// being iterated; here, the collection is ordinary static data and
		// a sibling reference, if any, lives in the per-element value
		// clause instead. See [resolver.forEachOverTupleComprehension].
		if inner, ok := unwrapToSet(expr); ok {
			if fe, ok := unwrapForExpr(inner); ok && fe.KeyExpr == nil {
				if got, exOK, applicable := r.forEachOverTupleComprehension(rc, fe); applicable {
					return got, exOK
				}
			}
		}
		return r.forEachOverResource(rc)
	}

	ident := r.moduleIdentifier(addr.String()+" for_each", expr.Range())
	mark := len(r.diags)
	// #354: element expressions collected alongside a tolerantly-retried
	// VALUE, empty for every other route through this function. See below.
	var deferredExprs map[string]elemBinding
	val, ok := r.evalStatic(expr, instScope{}, ident)
	if !ok {
		// #178's key-set fix: an object constructor's key set is knowable
		// whatever its values are, and the key set is all a for_each
		// expansion needs to enumerate instances - a resource reference
		// buried in one of the VALUES must not refuse the whole block the
		// way evaluating the object as a single value just did. Tried only
		// after the whole-value evaluation above has already failed, so a
		// for_each that already worked keeps going through the unchanged
		// path below, with eachValues populated the way every consumer of
		// it already expects. See localvalue.go.
		// tupleIsArgs is false at the top: for_each accepts a map, an
		// object, or a set of strings, never a list, so a tuple standing
		// here is not a set of merge() arguments and its elements' own keys
		// are not this block's instance keys.
		if keys, elems, structOK := r.staticForEachKeys(expr, ident, 0, false); structOK {
			r.diags = r.diags[:mark]
			// Whatever the chase proved a key's value to be, carried onto the
			// expansion so each.value resolves for that key alone. A key with
			// no proven value is simply absent from the map, which is how
			// [expansion.scope] tells the two apart; nothing here fills a gap
			// with a neighbouring key's value or with a zero value.
			//
			// #260 adds the second, weaker answer beside it: for a key with
			// no proven value, the element's own EXPRESSION, which supports
			// a per-attribute selection where a value supports nothing at
			// all. The two never both apply to one key.
			byName := map[string]elemBinding{}
			for i, name := range keys {
				if i < len(elems) {
					byName[name] = elems[i]
				}
			}
			sorted := append([]string(nil), keys...)
			sort.Strings(sorted)
			exp := &expansion{keyOnly: true}
			for _, name := range sorted {
				k := addrs.StringKey(name)
				exp.keys = append(exp.keys, k)
				b := byName[name]
				switch {
				case b.val != cty.NilVal:
					if exp.eachValues == nil {
						exp.eachValues = make(map[addrs.InstanceKey]cty.Value)
					}
					exp.eachValues[k] = b.val
					if b.expr != nil {
						// #354: a value AND an expression, which
						// [convertedElems] now produces for an element the
						// declared type converted to an object with unknown
						// attributes in it. The value is the binding - it is
						// what the module sees - and the expression is kept
						// beside it for the attributes of that value that came
						// back unknown, and for nothing else. See
						// [expansion.deferred] and
						// [resolver.eachValueDeferredParts].
						if exp.eachValueDeferred == nil {
							exp.eachValueDeferred = make(map[addrs.InstanceKey]elemBinding)
						}
						exp.eachValueDeferred[k] = b
					}
				case b.expr != nil:
					if exp.eachValueExprs == nil {
						exp.eachValueExprs = make(map[addrs.InstanceKey]elemBinding)
					}
					exp.eachValueExprs[k] = b
				}
			}
			return r.checkedForEachKeys(rc, exp)
		}
		// Last of all: the key set is in the caller's configuration, but a
		// module argument stands between this for_each and it. `for_each =
		// var.capacity_providers` over `capacity_providers = { ASG = { ...
		// module.autoscaling.autoscaling_group_arn ... } }` names one
		// instance, keyed ASG, whatever the leaf turns out to be. See
		// [resolver.tolerantVariables].
		//
		// Tried after staticForEachKeys, never before, because that chase
		// can carry an element EXPRESSION forward and resolve a sibling's
		// identity concretely where this can only produce an unknown.
		//
		// forEachKeysKnown is the acceptance test, so a retry that leaves
		// the key set itself in doubt - a set whose elements are unknown,
		// most of all, since a set's elements ARE its keys - changes
		// nothing and the original diagnostic stands.
		retried, retryOK := r.tolerantRetry(expr, instScope{}, ident)
		if !retryOK || !forEachKeysKnown(retried) || retried.IsNull() || retried.IsMarked() {
			return nil, false
		}
		r.diags = r.diags[:mark]
		val = retried
		// #354: the retry's value is the binding, exactly as it has always
		// been. Beside it, the element EXPRESSIONS the same source can still
		// be chased for, so an identity argument that reads one of the
		// unknowns inside that value has somewhere else to look. Nothing
		// here changes a key or a value. See
		// [resolver.elementExprBindings] and
		// [resolver.eachValueDeferredParts].
		deferredExprs = r.elementExprBindings(expr, ident)
	}
	if !forEachKeysKnown(val) || val.IsNull() {
		r.errorf(expr.Range(), "Non-static for_each expression",
			"The for_each value for %s cannot be determined from configuration alone. Instance keys are the addresses a projection binds against, so they must be knowable before anything is read from the cloud.", addr.String())
		return nil, false
	}
	if val.IsMarked() {
		r.errorf(expr.Range(), "Sensitive for_each expression",
			"The for_each value for %s is sensitive, so it cannot become part of resource addresses.", addr.String())
		return nil, false
	}

	ty := val.Type()
	exp := &expansion{eachValues: make(map[addrs.InstanceKey]cty.Value)}
	// Provenance, recorded once for every instance this expansion will
	// produce: the key set came out of a managed value this run obtained,
	// and the element values that came with it may be unknown until that
	// sibling is applied. Empty for any run that supplied no managed
	// results. See managedprovenance.go.
	exp.managedFrom, _ = r.managedFromExpr(expr)
	switch {
	case ty.IsMapType(), ty.IsObjectType():
		elems := make(map[string]cty.Value)
		var names []string
		for it := val.ElementIterator(); it.Next(); {
			k, v := it.Element()
			name := k.AsString()
			names = append(names, name)
			elems[name] = v
		}
		sort.Strings(names)
		for _, name := range names {
			k := addrs.StringKey(name)
			exp.keys = append(exp.keys, k)
			exp.eachValues[k] = elems[name]
			if b, ok := deferredExprs[name]; ok && b.expr != nil {
				if exp.eachValueDeferred == nil {
					exp.eachValueDeferred = make(map[addrs.InstanceKey]elemBinding)
				}
				exp.eachValueDeferred[k] = b
			}
		}
		return r.checkedForEachKeys(rc, exp)

	case ty.IsSetType():
		// An empty set built from a for-expression with no matching source
		// elements - toset([for x in var.y : x if <false for everything>]),
		// the shape a filtered comprehension produces whenever nothing
		// passes the filter - carries cty.DynamicPseudoType as its element
		// type, because cty has nothing to infer a concrete one from. Stock
		// OpenTofu's own for_each validation accepts that element type
		// alongside cty.String (internal/lang/evalchecks/eval_for_each.go's
		// performSetTypeChecks); this package's own check did not, so an
		// empty for_each set refused here even though OpenTofu itself
		// accepts it downstream. Checked as emptiness rather than the type
		// directly, since a zero-length set has no keys to enumerate either
		// way and the distinction does not matter to this package.
		if val.LengthInt() == 0 {
			return r.checkedForEachKeys(rc, exp)
		}
		if ty.ElementType() != cty.String {
			r.errorf(expr.Range(), "Invalid for_each set",
				"The for_each value for %s is a set of %s. Only a set of strings can produce instance keys.", addr.String(), ty.ElementType().FriendlyName())
			return nil, false
		}
		var names []string
		for it := val.ElementIterator(); it.Next(); {
			_, v := it.Element()
			// cty hoists an element's marks to the containing SET, so the
			// whole-value IsMarked test above already covers this branch -
			// but a list, map, object and tuple do NOT hoist, and the same
			// three lines appear over those elsewhere. Tested here so the
			// safe case is safe for a stated reason rather than by
			// accident, and so a future edit that widens this branch to
			// another collection kind cannot silently start panicking.
			if v.IsMarked() {
				r.errorf(expr.Range(), "Sensitive for_each expression",
					"The for_each value for %s contains a sensitive element, so it cannot become part of resource addresses.", addr.String())
				return nil, false
			}
			names = append(names, v.AsString())
		}
		sort.Strings(names)
		for _, name := range names {
			k := addrs.StringKey(name)
			exp.keys = append(exp.keys, k)
			exp.eachValues[k] = cty.StringVal(name)
		}
		return r.checkedForEachKeys(rc, exp)

	default:
		r.errorf(expr.Range(), "Invalid for_each value",
			"The for_each value for %s is %s. for_each accepts a map, an object, or a set of strings.", addr.String(), ty.FriendlyName())
		return nil, false
	}
}

// forEachOverResource handles `for_each = <other resource>`: the fixture's
// aws_route_table_association.this iterating over aws_subnet.this. The keys
// come from the parent's own expansion, so they are knowable even though
// every value in the parent is a live ID.
func (r *resolver) forEachOverResource(rc *configs.Resource) (*expansion, bool) {
	addr := rc.Addr()
	expr := rc.ForEach

	parentAddr, parentExp, ok := r.resolveResourceRef(expr, addr.String(), expr.Range())
	if !ok {
		return nil, false
	}

	parent := parentAddr
	return &expansion{
		keys:       append([]addrs.InstanceKey(nil), parentExp.keys...),
		eachParent: &parent,
	}, true
}

// resolveResourceRef resolves expr as a bare reference to a whole managed
// resource - for_each = aws_subnet.this itself, or the collection clause of
// a for-comprehension over one (forEachOverComprehension below) - into that
// resource's own already-computed expansion. subject and rng scope the
// diagnostic to whichever site actually wrote the reference.
func (r *resolver) resolveResourceRef(expr hcl.Expression, subject string, rng hcl.Range) (addrs.Resource, *expansion, bool) {
	trav, diags := hcl.AbsTraversalForExpr(expr)
	if diags.HasErrors() {
		r.errorf(rng, "Non-static for_each expression",
			"The for_each value for %s is computed from another resource's attributes. Only a plain reference to another resource (for_each = aws_subnet.this) can have its instance keys resolved from configuration; anything computed from resource attributes is known only after the cloud is read.", subject)
		return addrs.Resource{}, nil, false
	}
	ref, refDiags := addrs.ParseRef(trav)
	if refDiags.HasErrors() {
		r.appendDiags(refDiags)
		return addrs.Resource{}, nil, false
	}
	parentAddr, ok := ref.Subject.(addrs.Resource)
	if !ok || len(ref.Remaining) > 0 || parentAddr.Mode != addrs.ManagedResourceMode {
		// The expansion path is out of answers here, but a LIVE READ of the
		// referenced block would settle it: `for dvo in
		// aws_acm_certificate.cert.domain_validation_options` names an
		// attribute, and an attribute is a value rather than a key set. That
		// is issue #187's shape, and the demand is recorded structurally so
		// [DemandedManagedReads] can name the block a second pass would need
		// - the diagnostic itself is byte-for-byte what it always was.
		r.errorf(rng, "Non-static for_each expression",
			"The for_each value for %s refers to %s. Instance keys can be propagated only from a whole managed resource (for_each = aws_subnet.this).", subject, ref.Subject.String())
		r.demandedManaged(ref.Subject, subject)
		return addrs.Resource{}, nil, false
	}

	parentRC := r.mod.ResourceByAddr(parentAddr)
	if parentRC == nil {
		r.errorf(rng, "Reference to undeclared resource",
			"The for_each value for %s refers to %s, which is not declared in this configuration.", subject, parentAddr.String())
		return addrs.Resource{}, nil, false
	}
	parentExp, ok := r.expansionFor(parentRC)
	if !ok {
		return addrs.Resource{}, nil, false
	}
	if parentExp.eachValues == nil && parentExp.eachParent == nil && !parentExp.keyOnly {
		r.errorf(rng, "for_each over a resource that is not keyed",
			"The for_each value for %s is %s, which does not use for_each, so it is not a map of instances. OpenTofu accepts only a map or a set of strings as a for_each argument.", subject, parentAddr.String())
		return addrs.Resource{}, nil, false
	}
	return parentAddr, parentExp, true
}

// forEachOverComprehension recognizes `for_each = { for k, v in <resource> :
// keyExpr => valExpr if cond }`: a for-comprehension whose collection is a
// bare reference to another managed resource. It resolves when the key
// clause and the "if" filter depend only on the comprehension's own key
// variable and on locally-evaluable data, never on the value variable -
// the same test [resolver.evalPure]'s marked-value handling already makes
// unnecessary to guess at, because it is checked structurally, on
// expr.Variables(), rather than by evaluating anything the parent keeps
// symbolic. The parent's own already-computed expansion.keys is the only
// input this needs; it never looks at what a value var would carry.
//
// applicable is false whenever the shape does not match at all: not an
// object-producing for-expression (for_each never accepts the tuple a
// key-less for-expression produces directly), or a collection clause that
// is not a bare reference to a for_each'd managed resource. The caller
// falls back to forEachOverResource's own diagnostic on the whole
// expression in that case - the same "computed from another resource's
// attributes" message a comprehension already got before this function
// existed. When applicable is true, ok reports whether resolution actually
// succeeded, and a diagnostic has already been recorded in its place when
// it did not.
func (r *resolver) forEachOverComprehension(rc *configs.Resource, fe *hclsyntax.ForExpr) (exp *expansion, ok bool, applicable bool) {
	if fe.KeyExpr == nil {
		// A key-less for-expression produces a tuple, never a map or object;
		// for_each cannot take that shape directly (only wrapped in
		// toset(...), a different top-level expression this function is
		// never reached for). Nothing here applies to it.
		return nil, false, false
	}

	if _, diags := hcl.AbsTraversalForExpr(fe.CollExpr); diags.HasErrors() {
		// The collection clause is not a bare reference to begin with - some
		// other expression this function does not recognize, symbolic for a
		// reason unrelated to "for_each over a resource". Not this shape:
		// stay silent and let the whole-expression fallback in
		// forEachExpansion raise its own diagnostic, the same one a
		// comprehension always got before this function existed.
		return nil, false, false
	}

	addr := rc.Addr()
	subject := addr.String()
	rng := rc.ForEach.Range()

	// The collection clause may be a sibling resource's own literal
	// argument rather than the resource itself - `for domain in
	// fastly_tls_subscription.subscription.domains : ...` (GitHub issue
	// #220), as opposed to `for k, v in aws_subnet.this : ...` below. Tried
	// first because it is the more specific shape: a bare resource
	// reference has no remaining traversal steps for this to match, so the
	// two never compete for the same expression.
	if got, ok, applicable := r.comprehensionOverSiblingAttr(rc, fe, subject, rng); applicable {
		return got, ok, true
	}

	// The collection clause looks like a plain resource reference, so this
	// IS the shape from here on: every failure below is this function's to
	// report, not forEachOverResource's, or the site would be counted (and
	// diagnosed) twice for the one for_each argument that produced it.
	_, parentExp, resolved := r.resolveResourceRef(fe.CollExpr, subject, rng)
	if !resolved {
		return nil, false, true
	}

	if fe.ValVar != "" && (refsRoot(fe.KeyExpr, fe.ValVar) || (fe.CondExpr != nil && refsRoot(fe.CondExpr, fe.ValVar))) {
		r.errorf(rng, "Non-static for_each expression",
			"The for_each value for %s is a comprehension whose key or filter reads %s, its own iterated value. Only the key and locally-evaluable data can decide which instances exist and what they are named; a value read from the iterated resource is known only after the cloud is read.", subject, fe.ValVar)
		return nil, false, true
	}

	ident := r.moduleIdentifier(subject+" for_each", rng)
	seen := map[string]bool{}
	var names []string
	for _, pk := range parentExp.keys {
		var scope instScope
		if fe.KeyVar != "" {
			scope.vars = map[string]cty.Value{fe.KeyVar: keyValue(pk)}
		}
		if fe.CondExpr != nil {
			condVal, condOK := r.evalStatic(fe.CondExpr, scope, ident)
			if !condOK {
				return nil, false, true
			}
			include, err := convert.Convert(condVal, cty.Bool)
			if err != nil || include.IsNull() || !include.IsKnown() {
				r.errorf(fe.CondExpr.Range(), "Invalid for_each condition",
					"The if clause of %s's for_each comprehension did not evaluate to a known boolean.", subject)
				return nil, false, true
			}
			// cty.Value.False calls True, which asserts the value is
			// unmarked and panics when it is not: `if var.flag` with
			// `variable "flag" { type = bool, sensitive = true }` is all it
			// takes. Refused for the same reason the key clause below is -
			// which instances exist decides which addresses this run writes
			// into cloud tags, so it cannot be decided by a value this run
			// must not record.
			if include.IsMarked() {
				r.errorf(fe.CondExpr.Range(), "Sensitive for_each expression",
					"The if clause of %s's for_each comprehension is sensitive, so it cannot decide which resource addresses exist.", subject)
				return nil, false, true
			}
			if include.False() {
				continue
			}
		}
		keyVal, keyOK := r.evalStatic(fe.KeyExpr, scope, ident)
		if !keyOK {
			return nil, false, true
		}
		ks, err := convert.Convert(keyVal, cty.String)
		if err != nil || ks.IsNull() || !ks.IsKnown() {
			r.errorf(fe.KeyExpr.Range(), "Invalid for_each key",
				"The key clause of %s's for_each comprehension did not evaluate to a known string.", subject)
			return nil, false, true
		}
		// A marked key would panic in AsString below, and is refused for
		// the same reason [resolver.forEachExpansion] refuses a marked
		// for_each value outright: an instance key becomes a marker value
		// written to the cloud.
		if ks.IsMarked() {
			r.errorf(fe.KeyExpr.Range(), "Sensitive for_each expression",
				"The key clause of %s's for_each comprehension is sensitive, so it cannot become part of resource addresses.", subject)
			return nil, false, true
		}
		name := ks.AsString()
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}

	sort.Strings(names)
	built := &expansion{keyOnly: true}
	for _, n := range names {
		built.keys = append(built.keys, addrs.StringKey(n))
	}
	result, checkedOK := r.checkedForEachKeys(rc, built)
	return result, checkedOK, true
}

// comprehensionOverSiblingAttr recognizes `for_each = { for x in
// <sibling>.<attr> : keyExpr => valExpr }`, where <attr> is not the sibling
// resource itself (that shape is [resolver.forEachOverComprehension]'s own,
// via [resolver.resolveResourceRef]) but one of the sibling's own literal
// arguments - GitHub issue #220's fastly_tls_subscription.subscription.domains,
// where domains is set verbatim from var.domains. The rule that makes
// reading it safe is [resolver.siblingLiteralExpr]'s: it is the same one
// [resolver.parentPart] already applies to a single identity component,
// used here for a for_each's own collection clause instead.
//
// Deliberately unconcerned with valExpr: only the key clause and the "if"
// filter decide which instances exist, the same restriction
// [resolver.forEachOverComprehension] already enforces for a for_each over
// a whole resource, and for the identical reason - the value clause is
// read only through each.value once an instance exists, and this
// package's own keyOnly expansion already refuses a bare each.value
// cleanly rather than guess at it (see [expansion.keyOnly]). This is
// exactly what lets fastly-tls-subscription's own valExpr reach into
// managed_dns_challenges, a genuinely apply-time attribute, without that
// blocking the for_each itself.
//
// applicable is false whenever the collection clause is not this shape at
// all: not a single-attribute traversal into a resource, or an attribute
// [resolver.siblingLiteralExpr] cannot confirm is a plain literal. The
// caller falls back to [resolver.resolveResourceRef]'s own handling (and
// its own diagnostic) in that case, unchanged.
func (r *resolver) comprehensionOverSiblingAttr(rc *configs.Resource, fe *hclsyntax.ForExpr, subject string, rng hcl.Range) (exp *expansion, ok bool, applicable bool) {
	trav, diags := hcl.AbsTraversalForExpr(fe.CollExpr)
	if diags.HasErrors() {
		return nil, false, false
	}
	ref, refDiags := addrs.ParseRef(trav)
	if refDiags.HasErrors() {
		return nil, false, false
	}
	var parentInstAddr addrs.ResourceInstance
	switch subj := ref.Subject.(type) {
	case addrs.Resource:
		parentInstAddr = subj.Instance(addrs.NoKey)
	case addrs.ResourceInstance:
		parentInstAddr = subj
	default:
		return nil, false, false
	}
	if parentInstAddr.Resource.Mode != addrs.ManagedResourceMode || len(ref.Remaining) != 1 {
		return nil, false, false
	}
	attrStep, isAttr := ref.Remaining[0].(hcl.TraverseAttr)
	if !isAttr {
		return nil, false, false
	}

	parent := parentInstAddr.Absolute(r.modInst)
	collExpr, collScope, exprApplicable := r.siblingLiteralExpr(parent, attrStep.Name)
	if !exprApplicable {
		return nil, false, false
	}

	// This IS the shape from here on: every failure below is this
	// function's to report.
	ident := r.moduleIdentifier(subject+" for_each", rng)
	collVal, collOK := r.evalStatic(collExpr, collScope, ident)
	if !collOK {
		return nil, false, true
	}
	if !collVal.IsWhollyKnown() || collVal.IsNull() {
		r.errorf(fe.CollExpr.Range(), "Non-static for_each expression",
			"The for_each value for %s iterates over %s, which cannot be determined from configuration alone.", subject, traversalString(trav))
		return nil, false, true
	}
	// IsMarked before the iterator below, which panics on a marked value:
	// the sibling's own literal argument may be a sensitive variable, and
	// then this collection is marked. Refused rather than read, for the
	// reason [resolver.forEachExpansion] refuses a marked for_each value
	// outright - the instance keys become resource addresses written to
	// cloud tags.
	if collVal.IsMarked() {
		r.errorf(fe.CollExpr.Range(), "Sensitive for_each expression",
			"The for_each value for %s iterates over %s, which is sensitive, so it cannot become part of resource addresses.", subject, traversalString(trav))
		return nil, false, true
	}

	ty := collVal.Type()
	if !ty.IsListType() && !ty.IsSetType() && !ty.IsTupleType() && !ty.IsMapType() && !ty.IsObjectType() {
		r.errorf(fe.CollExpr.Range(), "Non-static for_each expression",
			"The for_each value for %s iterates over %s, which is %s, not a collection.", subject, traversalString(trav), ty.FriendlyName())
		return nil, false, true
	}

	seen := map[string]bool{}
	var names []string
	for it := collVal.ElementIterator(); it.Next(); {
		_, v := it.Element()
		scope := instScope{}
		if fe.ValVar != "" {
			scope.vars = map[string]cty.Value{fe.ValVar: v}
		}
		if fe.CondExpr != nil {
			condVal, condOK := r.evalStatic(fe.CondExpr, scope, ident)
			if !condOK {
				return nil, false, true
			}
			include, err := convert.Convert(condVal, cty.Bool)
			if err != nil || include.IsNull() || !include.IsKnown() {
				r.errorf(fe.CondExpr.Range(), "Invalid for_each condition",
					"The if clause of %s's for_each comprehension did not evaluate to a known boolean.", subject)
				return nil, false, true
			}
			// cty.Value.False calls True, which asserts the value is
			// unmarked and panics when it is not: `if var.flag` with
			// `variable "flag" { type = bool, sensitive = true }` is all it
			// takes. Refused for the same reason the key clause below is -
			// which instances exist decides which addresses this run writes
			// into cloud tags, so it cannot be decided by a value this run
			// must not record.
			if include.IsMarked() {
				r.errorf(fe.CondExpr.Range(), "Sensitive for_each expression",
					"The if clause of %s's for_each comprehension is sensitive, so it cannot decide which resource addresses exist.", subject)
				return nil, false, true
			}
			if include.False() {
				continue
			}
		}
		keyVal, keyOK := r.evalStatic(fe.KeyExpr, scope, ident)
		if !keyOK {
			return nil, false, true
		}
		ks, err := convert.Convert(keyVal, cty.String)
		if err != nil || ks.IsNull() || !ks.IsKnown() {
			r.errorf(fe.KeyExpr.Range(), "Invalid for_each key",
				"The key clause of %s's for_each comprehension did not evaluate to a known string.", subject)
			return nil, false, true
		}
		// A marked key would panic in AsString below, and is refused for
		// the same reason [resolver.forEachExpansion] refuses a marked
		// for_each value outright: an instance key becomes a marker value
		// written to the cloud.
		if ks.IsMarked() {
			r.errorf(fe.KeyExpr.Range(), "Sensitive for_each expression",
				"The key clause of %s's for_each comprehension is sensitive, so it cannot become part of resource addresses.", subject)
			return nil, false, true
		}
		name := ks.AsString()
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}

	sort.Strings(names)
	built := &expansion{keyOnly: true}
	for _, n := range names {
		built.keys = append(built.keys, addrs.StringKey(n))
	}
	result, checkedOK := r.checkedForEachKeys(rc, built)
	return result, checkedOK, true
}

// forEachOverTupleComprehension recognizes `for_each = toset([for x in
// <collection> : <expr>])`: a keyless (tuple-producing) for-expression
// wrapped in toset(), the idiomatic way to turn a for-expression into the
// set for_each requires - GitHub issue #220's govuk-infrastructure
// aws_ecr_lifecycle_policy, whose for_each is
// `toset([for repo in local.lifecycle_policy_repositories :
// aws_ecr_repository.github_repositories[repo].name])`.
//
// Unlike [resolver.comprehensionOverSiblingAttr], the collection here is
// never a sibling at all: it is ordinary static data (a local, a
// variable, a literal list) that alone decides how many instances exist.
// A symbolic reference belongs in the per-element expression instead,
// exactly where the govuk-infrastructure site puts it, and
// [resolver.resolveExpr] is what already resolves such a reference for an
// identity component - reused here unchanged, because indexing into a
// sibling by the comprehension's own loop variable
// (aws_ecr_repository.github_repositories[repo]) is the identical shape
// [resolver.resolveIndexedTraversal] already handles.
//
// Every resolved element must still be a plain literal: a for_each key
// can never wait on an apply-time value, so an element whose resolution
// comes back parent-derived (the sibling's own argument was itself a
// reference to something genuinely computed) refuses here rather than
// silently degrading to a formula no for_each key can be.
//
// applicable is false only when the collection clause itself is symbolic
// - referencing a managed resource - because then the SHAPE of the
// for_each, not just one string inside it, would depend on a value this
// package cannot read before the cloud does: the same refusal
// [resolver.forEachOverResource] already gives a for_each that names a
// resource directly.
func (r *resolver) forEachOverTupleComprehension(rc *configs.Resource, fe *hclsyntax.ForExpr) (exp *expansion, ok bool, applicable bool) {
	if r.isSymbolic(fe.CollExpr, instScope{}) {
		return nil, false, false
	}

	addr := rc.Addr()
	subject := addr.String()
	rng := rc.ForEach.Range()
	ident := r.moduleIdentifier(subject+" for_each", rng)

	collVal, collOK := r.evalStatic(fe.CollExpr, instScope{}, ident)
	if !collOK {
		return nil, false, true
	}
	if !collVal.IsWhollyKnown() || collVal.IsNull() {
		r.errorf(fe.CollExpr.Range(), "Non-static for_each expression",
			"The for_each value for %s cannot be determined from configuration alone.", subject)
		return nil, false, true
	}
	// IsMarked before the iterator below, for the reason
	// [resolver.comprehensionOverSiblingAttr] tests it: cty panics on a
	// marked value there, and a comprehension over a sensitive local or
	// variable is an ordinary way to produce one.
	if collVal.IsMarked() {
		r.errorf(fe.CollExpr.Range(), "Sensitive for_each expression",
			"The for_each value for %s iterates over a sensitive value, so it cannot become part of resource addresses.", subject)
		return nil, false, true
	}

	ty := collVal.Type()
	if !ty.IsListType() && !ty.IsSetType() && !ty.IsTupleType() {
		r.errorf(fe.CollExpr.Range(), "Invalid for_each value",
			"The for_each value for %s iterates over a value of type %s, not a collection.", subject, ty.FriendlyName())
		return nil, false, true
	}

	seen := map[string]bool{}
	var names []string
	for it := collVal.ElementIterator(); it.Next(); {
		_, v := it.Element()
		scope := instScope{}
		if fe.ValVar != "" {
			scope.vars = map[string]cty.Value{fe.ValVar: v}
		}
		if fe.CondExpr != nil {
			condVal, condOK := r.evalStatic(fe.CondExpr, scope, ident)
			if !condOK {
				return nil, false, true
			}
			include, err := convert.Convert(condVal, cty.Bool)
			if err != nil || include.IsNull() || !include.IsKnown() {
				r.errorf(fe.CondExpr.Range(), "Invalid for_each condition",
					"The if clause of %s's for_each comprehension did not evaluate to a known boolean.", subject)
				return nil, false, true
			}
			// cty.Value.False calls True, which asserts the value is
			// unmarked and panics when it is not: `if var.flag` with
			// `variable "flag" { type = bool, sensitive = true }` is all it
			// takes. Refused for the same reason the key clause below is -
			// which instances exist decides which addresses this run writes
			// into cloud tags, so it cannot be decided by a value this run
			// must not record.
			if include.IsMarked() {
				r.errorf(fe.CondExpr.Range(), "Sensitive for_each expression",
					"The if clause of %s's for_each comprehension is sensitive, so it cannot decide which resource addresses exist.", subject)
				return nil, false, true
			}
			if include.False() {
				continue
			}
		}
		parts, valOK := r.resolveExpr(fe.ValExpr, scope, ident)
		if !valOK {
			return nil, false, true
		}
		coalesced := coalesce(parts)
		if len(coalesced) != 1 || coalesced[0].Parent != nil {
			r.errorf(fe.ValExpr.Range(), "Non-static for_each expression",
				"The for_each value for %s is a comprehension whose element depends on a value that is not known until apply.", subject)
			return nil, false, true
		}
		name := coalesced[0].Literal
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}

	sort.Strings(names)
	built := &expansion{keyOnly: true}
	for _, n := range names {
		built.keys = append(built.keys, addrs.StringKey(n))
	}
	result, checkedOK := r.checkedForEachKeys(rc, built)
	return result, checkedOK, true
}

// unwrapToSet reports whether expr is a call to toset() with exactly one
// argument, returning that argument - the wrapper
// [resolver.forEachOverTupleComprehension]'s own shape is written through,
// since a keyless for-expression produces a tuple and for_each accepts
// only a map or a set.
func unwrapToSet(expr hcl.Expression) (hcl.Expression, bool) {
	call, ok := expr.(*hclsyntax.FunctionCallExpr)
	if !ok || call.Name != "toset" || len(call.Args) != 1 {
		return nil, false
	}
	return call.Args[0], true
}

// unwrapForExpr reports whether expr is, ignoring surrounding parentheses, a
// *hclsyntax.ForExpr - the shape forEachOverComprehension knows how to read.
func unwrapForExpr(expr hcl.Expression) (*hclsyntax.ForExpr, bool) {
	for {
		switch e := expr.(type) {
		case *hclsyntax.ParenthesesExpr:
			expr = e.Expression
		case *hclsyntax.ForExpr:
			return e, true
		default:
			return nil, false
		}
	}
}

// refsRoot reports whether expr references name as the root of one of its
// free variables - the same expr.Variables() traversal isSymbolic already
// reads, just checked against a single name instead of a fixed keyword set.
func refsRoot(expr hcl.Expression, name string) bool {
	for _, trav := range expr.Variables() {
		if trav.RootName() == name {
			return true
		}
	}
	return false
}

func (r *resolver) moduleIdentifier(subject string, rng hcl.Range) configs.StaticIdentifier {
	return configs.StaticIdentifier{
		Module:    r.modInst.Module(),
		Subject:   subject,
		DeclRange: rng,
	}
}

// ---- small helpers ---------------------------------------------------

func sortedResources(resources map[string]*configs.Resource) []*configs.Resource {
	out := make([]*configs.Resource, 0, len(resources))
	for _, rc := range resources {
		out = append(out, rc)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Addr().String() < out[j].Addr().String()
	})
	return out
}

// uniqueNameValue reads the account-unique name entry declares, out of this
// instance's own configuration. ok is false whenever the value is anything
// other than a string this run can compute today, and every one of those
// refusals is silent - no diagnostic, no failed resolution.
//
// Silence is the contract, not an oversight. The caller's fallback is the
// ordinary [DiscoveryServerAssigned] answer, which is exactly what this
// instance was before [TypeIdentity.UniqueName] existed: it needs a marker,
// and if it cannot carry one, internal/live/stamp refuses the apply and says
// why. Raising a diagnostic here instead would turn a configuration that was
// already refused into a configuration refused twice, with the second message
// blaming an argument the author has set perfectly well.
//
// What it refuses, and why each is a refusal rather than a bind:
//
//   - the argument is absent, or evaluates to null. There is no name to
//     match, so no listing can be searched for one.
//   - the value is not wholly known - a reference to another resource's
//     computed attribute, uuid(), a variable with no value here. The name
//     exists but this run cannot say what it is, and matching on a value
//     this run guessed at is the wrong-marker outcome the whole mechanism is
//     built to avoid.
//   - the value is marked sensitive. A name that binds a live object is
//     compared against a listing and reported in a plan; a sensitive one has
//     no business there, which is the same line [resolver.stringValue]
//     draws.
//   - the value is the empty string. AWS will not accept it as a name, so
//     nothing in a listing can carry it - but a map lookup on "" would match
//     any listed object whose own property was absent or empty, which is the
//     multiple-match hazard arriving through the back door.
func (r *resolver) uniqueNameValue(rc *configs.Resource, entry TypeIdentity, scope instScope, addr addrs.AbsResourceInstance) (string, bool) {
	if !entry.UniqueName.Set() {
		return "", false
	}
	schema := &hcl.BodySchema{}
	for _, n := range entry.UniqueName.Attrs {
		schema.Attributes = append(schema.Attributes, hcl.AttributeSchema{Name: n})
	}
	content, _, diags := rc.Config.PartialContent(schema)
	if diags.HasErrors() {
		return "", false
	}
	attr := firstPresent(content.Attributes, entry.UniqueName.Attrs)
	if attr == nil {
		return "", false
	}
	val, evalDiags := r.evalPure(attr.Expr, scope, r.identifier(addr, attr.Name, attr.Range))
	if evalDiags.HasErrors() || val == cty.NilVal {
		return "", false
	}
	if val.IsMarked() || val.IsNull() || !val.IsWhollyKnown() {
		return "", false
	}
	str, err := convert.Convert(val, cty.String)
	if err != nil {
		return "", false
	}
	s := str.AsString()
	if s == "" {
		return "", false
	}
	return s, true
}

func firstPresent(attrs hcl.Attributes, names []string) *hcl.Attribute {
	for _, n := range names {
		if a, ok := attrs[n]; ok {
			return a
		}
	}
	return nil
}

// firstApplicablePresent is [firstPresent], narrowed for a
// [Component.SoleElement] alternation (GitHub issue #369): a candidate whose
// own expression is a statically known list/set/tuple with ZERO elements is
// not "which of the alternation's members applies is unclear" - zero is
// never ambiguous, it is this member contributing nothing, the identical
// fact [Component.OmitIfAbsent] already gives a name to when the argument is
// missing from the body entirely. aws_security_group_rule's own
// prefix_list_ids/cidr_blocks/ipv6_cidr_blocks/source_security_group_id
// alternation is exactly this shape: a computed_ingress_with_source_security_group_id
// block (terraform-aws-modules/security-group's own main.tf) sets
// source_security_group_id to a real value and prefix_list_ids to
// var.ingress_prefix_list_ids, which defaults to []. Reading the empty list
// as though it were the member that determines this instance's identity -
// what [firstPresent] alone does, since it only asks whether the NAME was
// written, never what the value turned out to be - raised "Ambiguous
// list-valued identity argument" for zero elements on a resource whose
// identity a sibling member already settles.
//
// So the search moves to the next name in Attrs instead of stopping, and if
// every candidate turns out to be a definite empty list (or absent
// entirely), this returns nil exactly as [firstPresent] would for an
// all-absent alternation - the caller's existing "no candidate present"
// handling (OmitIfAbsent, Default, ServerAssignedIfAbsent, the *_prefix
// convention, or "Identity argument not set") applies unchanged, because a
// set of members that each definitely contribute nothing IS an alternation
// with nothing set. A candidate this cannot prove empty - a real value, a
// non-static or symbolic expression, or a collection with one or more than
// one element - is returned exactly as [firstPresent] would return it, so
// every diagnostic the rest of this package already raises for it
// (including the genuine ambiguity of MORE than one element, which never
// changes) fires exactly as before: this only ever demotes a proven-empty
// list from "present" to "absent", it never resolves or picks a value
// itself.
//
// GitHub issue #384: demoting a proven-empty candidate is the SAFE half of
// this function's job; the unsafe half was stopping at the first candidate
// it could not prove empty, without ever looking at the others. Two
// alternatives of the SAME alternation can be genuinely non-empty AT ONCE -
// terraform-aws-modules/security-group's `egress_rules = ["all-all"]`
// defaults both `egress_cidr_blocks` (-> cidr_blocks) and
// `egress_ipv6_cidr_blocks` (-> ipv6_cidr_blocks) to a real one-element
// list, and AWS creates two separate live rule objects for the one declared
// instance. [Component.SoleElement]'s whole contract - "exactly one of
// these is ever populated for a real object" - is false for that instance,
// and the old code silently returned whichever name Attrs happened to list
// first: a real value, so the caller went on to build a concrete identity
// out of it, bound to the wrong live object with nothing to say so. That is
// HANDOFF's "wrong marker" outcome by construction, not by accident, and
// convergence never reveals it - the plan looks plausible either way.
//
// So this now keeps scanning past the first non-empty candidate instead of
// returning immediately, and if a SECOND one also cannot be proven empty,
// reports the conflict instead of a winner: conflict is non-nil and attr is
// the first of the two (their identities as a pair, not a choice between
// them - the caller decides what "more than one candidate is real" means,
// this function only establishes that it is true). Determinism is not
// correctness here: returning "the first" deterministically would only make
// the wrong marker reproducible, which is worse, because it would look
// trustworthy. A single non-empty candidate is unaffected - returned exactly
// as before, with conflict nil - so every ordinary SoleElement resolution
// (issue #369's own zero-element case included) keeps its answer.
func (r *resolver) firstApplicablePresent(comp Component, attrs hcl.Attributes, scope instScope, addr addrs.AbsResourceInstance) (attr *hcl.Attribute, conflict *hcl.Attribute) {
	if !comp.SoleElement {
		return firstPresent(attrs, comp.Attrs), nil
	}
	remaining := comp.Attrs
	for len(remaining) > 0 {
		cand := firstPresent(attrs, remaining)
		if cand == nil {
			break
		}
		ident := r.identifier(addr, cand.Name, cand.Range)
		if !r.definitelyEmptyList(cand.Expr, scope, ident) {
			if attr != nil {
				return attr, cand
			}
			attr = cand
		}
		next := make([]string, 0, len(remaining)-1)
		for _, n := range remaining {
			if n != cand.Name {
				next = append(next, n)
			}
		}
		remaining = next
	}
	return attr, nil
}

// definitelyEmptyList reports whether expr - one member of a
// [Component.SoleElement] alternation - is a statically known
// list/set/tuple with exactly zero elements, without leaving behind any
// diagnostic of its own: it is a probe [resolver.firstApplicablePresent]
// uses to decide whether a candidate should be skipped, not a resolution,
// and every other outcome (a scalar, a symbolic or non-static expression, a
// collection this cannot count cleanly, one element, or more than one) is
// reported false and left for [resolver.soleElementExpr] and
// [resolver.soleElementFromValue] to resolve or refuse exactly as they
// always have - this duplicates none of their diagnostics, it only ever
// answers "definitely nothing here".
//
// An impure call is excluded on purpose, the same reason [resolver.evalStatic]
// refuses one outright for an identity argument: a value that can change
// between runs must never silently decide which alternation member this
// package skips.
func (r *resolver) definitelyEmptyList(expr hcl.Expression, scope instScope, ident configs.StaticIdentifier) bool {
	if elems, diags := hcl.ExprList(expr); !diags.HasErrors() && elems != nil {
		return len(elems) == 0
	}
	if len(impureCallsIn(expr)) > 0 {
		return false
	}
	if r.isSymbolic(expr, scope) {
		return false
	}
	val, diags := r.evalPure(expr, scope, ident)
	if diags.HasErrors() || val == cty.NilVal {
		retried, ok := r.tolerantRetry(expr, scope, ident)
		if !ok {
			return false
		}
		val = retried
	}
	return isEmptyCollection(val)
}

// isEmptyCollection reports whether val is a known, unmarked list, set or
// tuple with zero elements - the same shape test
// [resolver.soleElementFromValue] applies before counting, kept separate so
// [resolver.definitelyEmptyList] never has to raise, or risk raising, any of
// that function's own diagnostics.
func isEmptyCollection(val cty.Value) bool {
	ty := val.Type()
	if !ty.IsListType() && !ty.IsSetType() && !ty.IsTupleType() {
		return false
	}
	if val.IsNull() || !val.IsWhollyKnown() || val.IsMarked() {
		return false
	}
	return val.LengthInt() == 0
}

// firstPrefixSibling is [firstPresent] for the "<name>_prefix" convention
// [resolver.identityArgs] pulls alongside every plain name: it reports the
// first one actually set in the resource body, and which of names it is a
// sibling of, so the caller can name the base argument in its own message.
func firstPrefixSibling(attrs hcl.Attributes, names []string) (attr *hcl.Attribute, base string) {
	for _, n := range names {
		if a, ok := attrs[n+"_prefix"]; ok {
			return a, n
		}
	}
	return nil, ""
}

// coalesce merges adjacent literal parts, so that a formula's parts
// alternate literal and parent as far as possible.
func coalesce(parts []Part) []Part {
	out := make([]Part, 0, len(parts))
	for _, p := range parts {
		if p.Parent == nil && len(out) > 0 && out[len(out)-1].Parent == nil {
			out[len(out)-1].Literal += p.Literal
			continue
		}
		out = append(out, p)
	}
	return out
}

func keyValue(key addrs.InstanceKey) cty.Value {
	switch k := key.(type) {
	case addrs.StringKey:
		return cty.StringVal(string(k))
	case addrs.IntKey:
		return cty.NumberIntVal(int64(k))
	default:
		return cty.NullVal(cty.String)
	}
}

func isAttrStep(step hcl.Traverser, name string) bool {
	attr, ok := step.(hcl.TraverseAttr)
	return ok && attr.Name == name
}

func traversalString(trav hcl.Traversal) string {
	ref, diags := addrs.ParseRef(trav)
	if diags.HasErrors() || ref == nil {
		return trav.RootName()
	}
	return ref.DisplayString()
}

func orList(names []string) string {
	quoted := quoteAll(names)
	switch len(quoted) {
	case 0:
		return "(none)"
	case 1:
		return quoted[0]
	case 2:
		return quoted[0] + " or " + quoted[1]
	default:
		return strings.Join(quoted[:len(quoted)-1], ", ") + ", or " + quoted[len(quoted)-1]
	}
}

func quoteAll(names []string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, fmt.Sprintf("%q", n))
	}
	return out
}
