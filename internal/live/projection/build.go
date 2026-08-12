// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"

	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/lang"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/plans/objchange"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// Build materializes a projection: an in-memory prior state holding the
// live object for every resource instance whose identity the identity
// package could produce and whose live counterpart the provider could
// find.
//
// cfg is the configuration the resolutions came from; it supplies each
// instance's provider configuration address and the references that become
// the recorded dependencies. resolutions is the identity package's output.
// provs supplies configured provider instances (see [Providers]).
//
// Error diagnostics mean the projection is untrustworthy and the caller
// must abort the run: a provider errored, or misbehaved, or the
// configuration and the resolutions disagree. An instance that is merely
// absent from the live system is not an error; it is recorded in
// [Result.Omitted] and the subsequent plan will propose creating it.
//
// Build writes no files and takes no locks. The returned state is the
// caller's to use for one operation and then drop.
func Build(ctx context.Context, cfg *configs.Config, resolutions *identity.Result, provs Providers) (*Result, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	empty := &Result{State: states.NewState()}

	switch {
	case cfg == nil || cfg.Module == nil:
		diags = diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"No configuration to project",
			"Building a projection requires the configuration the identity resolutions were computed from, and none was given.",
		))
		return empty, diags
	case resolutions == nil:
		diags = diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"No identity resolutions to project",
			"Building a projection requires the output of identity resolution, and none was given.",
		))
		return empty, diags
	}

	return BuildFrom(ctx, cfg, resolutions.All(), provs)
}

// Options are the settings a projection needs that the resolutions and the
// configuration do not supply between them.
type Options struct {
	// UndeclaredProvider is the provider configuration to read instances
	// marked [identity.Resolution.Undeclared] through: resources this estate
	// owns whose resource block was deleted, which therefore have no
	// configuration to read a provider from.
	//
	// It is the provider configuration the marker sweep listed them with,
	// and that is the only defensible answer. A deleted block's provider
	// alias is not recoverable from anything, and a resource found by
	// listing through one account and region is in that account and region;
	// reading it through any other configuration would be reading somewhere
	// else. The zero value falls back to the provider the resource type
	// implies in the root module, which is right whenever the configuration
	// has one unaliased provider - the shape stateless mode v0 discovers
	// through anyway.
	UndeclaredProvider addrs.AbsProviderConfig

	// Ownership is the rule deciding which live objects may enter the prior
	// state. Nil means no check, which is what a caller that has no estate
	// concept at all - the marker rewrite in internal/live/mv, reading
	// one resource it was handed the identity of - passes. Every path that
	// builds a prior state for a plan sets it. See [Ownership].
	Ownership *Ownership
}

// BuildWith is [BuildFrom] with options. See [Options].
func BuildWith(ctx context.Context, cfg *configs.Config, resolutions []identity.Resolution, provs Providers, opts Options) (*Result, tfdiags.Diagnostics) {
	return buildFrom(ctx, cfg, resolutions, provs, opts)
}

// BuildFrom is [Build] over a plain list of resolutions rather than the
// identity package's Result.
//
// It exists for two reasons. P2's discovery pass produces resolutions that
// did not come out of static analysis - a marker lookup turns a
// needs-discovery instance into a concrete one - and it needs a way to
// hand the projection builder a merged list without reaching into another
// package's private structure. And a list of [identity.Resolution] values
// is something a test can write down by hand, which a Result is not, so
// the parent-derived paths through this package are testable before P2
// makes them reachable in practice.
//
// The list may hold resolutions in any order; ordering is this function's
// job.
func BuildFrom(ctx context.Context, cfg *configs.Config, resolutions []identity.Resolution, provs Providers) (*Result, tfdiags.Diagnostics) {
	return buildFrom(ctx, cfg, resolutions, provs, Options{})
}

func buildFrom(ctx context.Context, cfg *configs.Config, resolutions []identity.Resolution, provs Providers, opts Options) (*Result, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	empty := &Result{State: states.NewState()}

	switch {
	case cfg == nil || cfg.Module == nil:
		diags = diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"No configuration to project",
			"Building a projection requires the configuration the identity resolutions were computed from, and none was given.",
		))
		return empty, diags
	case provs == nil:
		diags = diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"No provider access",
			"Building a projection requires configured provider instances to read the live system with, and none were given.",
		))
		return empty, diags
	case len(cfg.Children) > 0:
		// Defense in depth: lint's RuleChildModule is what an operator sees.
		diags = diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Configuration with child modules reached the projection",
			"Live resource markers v0 cover the root module only. Lint rejects module calls before this point, so this is a bug in the live-markers pipeline.",
		))
		return empty, diags
	}

	// The config-side naming signal, for the identity check the provider
	// cache runs when the schemas arrive (schema_check.go). Its diagnostics
	// are dropped on purpose: a configuration whose expansion this cannot
	// enumerate is a resolution failure the caller has already seen, and a
	// second copy of it here would fail a projection over a report.
	signal, _ := identity.ScanConfig(ctx, cfg)

	b := &builder{
		cfg:        cfg,
		opts:       opts,
		providers:  newProviderCache(provs, signal),
		state:      states.NewState(),
		live:       make(map[string]cty.Value),
		omitted:    make(map[string]Omission),
		causes:     make(map[string]string),
		depsByType: make(map[string][]addrs.ConfigResource),
	}
	b.run(ctx, resolutions)

	sortOmissions(b.omissionList)
	sort.Slice(b.materialized, func(i, j int) bool {
		return b.materialized[i].String() < b.materialized[j].String()
	})

	sortUnowned(b.unownedList)

	res := &Result{
		State:        b.state,
		Materialized: b.materialized,
		Omitted:      b.omissionList,
		Unowned:      b.unownedList,
	}
	return res, diags.Append(b.diags)
}

type builder struct {
	cfg       *configs.Config
	opts      Options
	providers *providerCache

	state *states.State

	// live holds the materialized object value of each instance already in
	// the projection, keyed by address string. It is what a parent-derived
	// formula reads its parents' live IDs out of.
	live map[string]cty.Value

	omitted      map[string]Omission
	omissionList []Omission
	materialized []addrs.AbsResourceInstance
	unownedList  []Unowned

	// causes holds a short subordinate clause per omitted instance, for
	// use inside another instance's explanation. Omission.Detail is a
	// standalone sentence and reads badly nested inside one.
	causes map[string]string

	// depsByType caches the config-level dependency set per resource
	// block, since every instance of a resource shares one.
	depsByType map[string][]addrs.ConfigResource

	diags tfdiags.Diagnostics
}

func (b *builder) run(ctx context.Context, resolutions []identity.Resolution) {
	concrete, derived, needsDiscovery, cyclic := orderWork(resolutions)

	for _, r := range needsDiscovery {
		b.omit(r.Addr, ReasonNeedsDiscovery, needsDiscoveryDetail(r), needsDiscoveryCause(r))
	}

	for _, r := range cyclic {
		detail := fmt.Sprintf(
			"The identities of %s and the instances it derives from refer to each other in a cycle, so there is no order in which they can be read. This is a bug in identity resolution: a parent-derived identity must name parents that are resolvable first.",
			r.Addr,
		)
		b.diags = b.diags.Append(tfdiags.Sourceless(tfdiags.Error, "Cyclic parent-derived identities", detail))
		b.omit(r.Addr, ReasonCycle, detail, "its identity formula is part of a cycle and can never be rendered.")
	}

	for _, r := range concrete {
		b.materialize(ctx, wanted{
			addr:       r.Addr,
			importID:   r.ImportID,
			identity:   r.Identity,
			undeclared: r.Undeclared,
		})
	}
	for _, r := range derived {
		id, ok := b.renderFormula(r)
		if !ok {
			continue
		}
		b.materialize(ctx, wanted{
			addr:       r.Addr,
			importID:   id,
			identity:   r.Identity,
			undeclared: r.Undeclared,
		})
	}
}

// wanted is one instance's identity in every form this run holds it, which is
// the input [builder.materialize] works from.
//
// The two forms are not alternatives to choose between up front. The string
// is what every operator-facing line prints and what a marker rewrite
// records, so it is always carried; the identity object is what the import
// itself should use when there is one, and whether there is one is a question
// about the provider's schema, which is not known until a plugin is on the
// line. So both travel here and [importTarget] decides per resource, once the
// schema has arrived.
type wanted struct {
	addr addrs.AbsResourceInstance

	// importID is the provider's import-ID string. Always populated.
	importID string

	// identity is the provider's own resource identity object, when
	// something served one: a marker sweep's list results carry it. Null
	// otherwise.
	identity cty.Value

	undeclared bool
}

// importTarget picks the form this instance's import is asked in.
//
// The identity object wins whenever there is one and the provider serves a
// schema it fits, because it is the provider's own account of what names this
// resource - read off the list call that found it - rather than one attribute
// of that account flattened into a string. The string is the fallback, and it
// is not a lesser one: it is the only form available for a type with no
// identity schema, and the only form a configuration can produce.
//
// The two are exclusive on the wire: [providers.ImportTarget.IsIdentityBased]
// decides, and both plugin protocols error rather than falling back when they
// are handed an identity for a type with no identity schema. So the choice is
// made here, where the schema is in hand, and exactly one field is set.
func importTarget(w wanted, schema providers.Schema) providers.ImportTarget {
	byID := providers.ImportTarget{ID: w.importID}

	if schema.IdentitySchema == nil {
		return byID
	}
	if w.identity == cty.NilVal || w.identity.IsNull() {
		return byID
	}
	want := schema.IdentitySchema.ImpliedType()
	val, err := convert.Convert(w.identity, want)
	if err != nil || val.IsNull() || !val.IsWhollyKnown() {
		// The provider served an identity this provider's own schema does
		// not describe, which is a provider bug rather than anything this
		// run can act on. The import ID came off the same list result, so
		// there is a working answer and no reason to fail.
		log.Printf("[WARN] projection: %s came back with an identity that does not fit %s's identity schema (%v); importing by ID %q instead",
			w.addr, w.addr.Resource.Resource.Type, err, w.importID)
		return byID
	}
	return providers.ImportTarget{Identity: val}
}

// orderWork splits the resolutions into the work lists Build runs, in the
// order it runs them: every concrete instance first, in address order, then
// the parent-derived instances in dependency order.
//
// needsDiscovery is passed straight through to the omission list. cyclic
// holds parent-derived instances that could not be ordered because they
// form a dependency cycle among themselves. Ordering only ever needs to
// consider edges to other parent-derived instances: an edge to a concrete
// parent is satisfied by the time the derived phase starts, and an edge to
// a needs-discovery parent is never satisfiable and is handled as a missing
// parent at render time rather than as an ordering constraint.
func orderWork(resolutions []identity.Resolution) (concrete, derived, needsDiscovery, cyclic []identity.Resolution) {
	sorted := make([]identity.Resolution, len(resolutions))
	copy(sorted, resolutions)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Addr.String() < sorted[j].Addr.String()
	})

	var pending []identity.Resolution
	for _, r := range sorted {
		switch r.Class {
		case identity.ClassConcrete:
			concrete = append(concrete, r)
		case identity.ClassParentDerived:
			pending = append(pending, r)
		default:
			needsDiscovery = append(needsDiscovery, r)
		}
	}

	inPending := make(map[string]bool, len(pending))
	for _, r := range pending {
		inPending[r.Addr.String()] = true
	}

	done := make(map[string]bool, len(pending))
	for len(pending) > 0 {
		var stuck []identity.Resolution
		progressed := false

		for _, r := range pending {
			ready := true
			if r.Formula != nil {
				for _, p := range r.Formula.Parents {
					key := p.String()
					if inPending[key] && !done[key] {
						ready = false
						break
					}
				}
			}
			if !ready {
				stuck = append(stuck, r)
				continue
			}
			derived = append(derived, r)
			done[r.Addr.String()] = true
			progressed = true
		}

		if !progressed {
			// Everything left depends on something else left: a cycle.
			cyclic = stuck
			break
		}
		pending = stuck
	}

	return concrete, derived, needsDiscovery, cyclic
}

// renderFormula turns a parent-derived resolution into a concrete import
// ID by reading its parents' live values out of the projection built so
// far. It records an omission and returns false when it cannot.
func (b *builder) renderFormula(r identity.Resolution) (string, bool) {
	if r.Formula == nil {
		detail := fmt.Sprintf("Identity resolution classified %s as parent-derived but attached no formula.", r.Addr)
		b.diags = b.diags.Append(tfdiags.Sourceless(tfdiags.Error, "Parent-derived identity with no formula", detail))
		b.omit(r.Addr, ReasonFailed, detail, "identity resolution gave it no formula to render.")
		return "", false
	}

	// Check every parent first, so that the reason names the parent rather
	// than the attribute lookup that happened to fail first.
	for _, p := range r.Formula.Parents {
		if _, ok := b.live[p.String()]; ok {
			continue
		}
		b.omit(r.Addr, ReasonParentUnavailable,
			fmt.Sprintf(
				"%s is identified by a composite of its parents' live IDs, and %s is not in the projection: %s Without that parent's live ID there is no import identity for %s, so the plan will propose creating it.",
				r.Addr, p, b.causeFor(p), r.Addr,
			),
			fmt.Sprintf("its own parent %s is not in the projection.", p),
		)
		return "", false
	}

	var lookupDiags tfdiags.Diagnostics
	id, ok := r.Formula.Render(func(parent addrs.AbsResourceInstance, attr string) (string, bool) {
		val, ok := b.live[parent.String()]
		if !ok {
			return "", false
		}
		s, err := attrString(val, attr)
		if err != nil {
			lookupDiags = lookupDiags.Append(tfdiags.Sourceless(
				tfdiags.Error,
				"Cannot read a parent's identity from the projection",
				fmt.Sprintf(
					"The identity of %s is composed from %s.%s, but that value cannot be used: %s. The provider's object for the parent does not carry the identity attribute this resource type's import syntax needs.",
					r.Addr, parent, attr, err,
				),
			))
			return "", false
		}
		return s, true
	})
	if !ok {
		b.diags = b.diags.Append(lookupDiags)
		detail := fmt.Sprintf("The identity formula for %s could not be rendered from its parents' live values.", r.Addr)
		if len(lookupDiags) > 0 {
			detail = lookupDiags[0].Description().Detail
		}
		b.omit(r.Addr, ReasonFailed, detail, "its identity formula could not be rendered from its parents' live values.")
		return "", false
	}
	return id, true
}

// causeFor renders why a parent instance is not in the projection, in a
// form that reads as a clause inside its child's explanation. The chain
// has to stay legible: a route is missing because its route table needs
// discovery, not merely because it is missing.
func (b *builder) causeFor(parent addrs.AbsResourceInstance) string {
	if cause, ok := b.causes[parent.String()]; ok {
		return cause
	}
	return "it was not resolved at all, so nothing is known about it."
}

// materialize drives one instance's import: ImportResourceState with the
// given ID, then ReadResource to refresh what came back, then a write into
// the projection.
//
// undeclared says the instance has no resource block, which is the shape of
// a resource this estate owns and the configuration has stopped declaring.
// It is not an error and not a special kind of state entry: the object is
// read and written exactly as any other, and what makes the plan destroy it
// is the ordinary rule that a prior-state instance with no configuration is
// an orphan. The two things it cannot have are a provider read off its
// resource block and a dependency set read off its arguments; see
// [Options.UndeclaredProvider] for the first, and [builder.dependencies] for
// why the second is empty rather than guessed.
func (b *builder) materialize(ctx context.Context, w wanted) {
	addr := w.addr
	importID := w.importID
	typeName := addr.Resource.Resource.Type

	rc, ok := b.cfg.Module.ManagedResources[addr.Resource.Resource.String()]
	if !ok && !w.undeclared {
		detail := fmt.Sprintf(
			"Identity resolution produced %s, but that resource block is not in the configuration the projection was given. The configuration and the resolutions do not match.",
			addr,
		)
		b.diags = b.diags.Append(tfdiags.Sourceless(tfdiags.Error, "Resolved instance missing from the configuration", detail))
		b.omitFailed(addr, detail)
		return
	}

	providerAddr, providerOK := b.providerFor(rc, typeName)
	if !providerOK {
		detail := fmt.Sprintf(
			"%s is a resource this estate owns whose resource block is no longer in the configuration, and nothing in the configuration says which provider to read a %s through: it declares no provider that could serve the type and the run supplied none. The resource is left alone rather than read.",
			addr, typeName,
		)
		b.diags = b.diags.Append(tfdiags.Sourceless(tfdiags.Warning, "No provider for an undeclared resource", detail))
		b.omit(addr, ReasonFailed, detail, "no provider could be found to read it through.")
		return
	}
	entry, err := b.providers.get(ctx, providerAddr)
	if err != nil {
		detail := err.Error()
		b.diags = b.diags.Append(tfdiags.Sourceless(tfdiags.Error, "Provider unavailable", fmt.Sprintf(
			"Building the projection entry for %s needs provider %s, which could not be used: %s.", addr, providerAddr, detail,
		)))
		b.omitFailed(addr, detail)
		return
	}

	schema, schemaDiags := entry.resourceSchema(providerAddr, typeName)
	if schemaDiags.HasErrors() {
		b.diags = b.diags.Append(schemaDiags)
		b.omitFailed(addr, schemaDiags[0].Description().Detail)
		return
	}

	obj, status, matDiags := importAndRead(ctx, entry.provider, schema, typeName, importTarget(w, schema), importID)
	b.diags = b.diags.Append(matDiags)

	switch status {
	case statusAbsent:
		b.omit(addr, ReasonAbsent,
			fmt.Sprintf(
				"The provider reports no %s exists with identity %q, so this resource has not been created yet. The plan will propose creating it.",
				typeName, importID,
			),
			fmt.Sprintf("the provider reports no %s exists with identity %q.", typeName, importID),
		)
		return
	case statusFailed:
		detail := fmt.Sprintf("Reading %s with identity %q failed.", typeName, importID)
		if len(matDiags) > 0 {
			detail = fmt.Sprintf("Reading %s with identity %q failed: %s.", typeName, importID, matDiags[0].Description().Summary)
		}
		b.omitFailed(addr, detail)
		return
	}

	// Ownership is checked here, on the object the provider returned, and
	// before anything is written into the projection. Everything below this
	// point is what "the estate owns this" means in practice - a prior-state
	// entry the plan may update, and an orphan the plan may destroy once its
	// block is gone - so this is the one place the check belongs.
	if b.checkOwnership(addr, typeName, importID, schema, obj.Value) != ownershipOK {
		return
	}

	if rc != nil {
		obj.Dependencies = b.dependencies(rc, schema)
	}

	src, err := obj.Encode(schema.Block.ImpliedType(), uint64(schema.Version), uint64(schema.IdentitySchemaVersion))
	if err != nil {
		detail := fmt.Sprintf("The object read for %s could not be encoded into the projection: %s.", addr, err)
		b.diags = b.diags.Append(tfdiags.Sourceless(tfdiags.Error, "Cannot encode a projected object", detail))
		b.omitFailed(addr, detail)
		return
	}

	b.state.EnsureModule(addr.Module).SetResourceInstanceCurrent(addr.Resource, src, providerAddr, addrs.NoKey)
	b.live[addr.String()] = obj.Value
	b.materialized = append(b.materialized, addr)
	log.Printf("[TRACE] projection: materialized %s from import identity %q", addr, importID)
}

type materializeStatus int

const (
	statusMaterialized materializeStatus = iota
	// statusAbsent means the provider answered normally that there is no
	// such object. Not an error.
	statusAbsent
	// statusFailed means the provider could not answer. Always accompanied
	// by error diagnostics.
	statusFailed
)

// importAndRead is the whole provider conversation for one instance, and
// is the reason this package exists rather than calling into a graph walk:
// ImportResourceState to turn an identity into a stub object, then
// ReadResource to fill that stub in from the live system.
//
// target is the form the import is asked in - an identity object or an
// import-ID string, never both, see [importTarget]. importID is carried
// alongside whichever form was chosen because it is what every sentence here
// names the resource by: an operator reading "no aws_subnet exists with
// identity …" needs the string whether or not the wire carried it.
//
// It mirrors graphNodeImportState/graphNodeImportStateSub and
// NodeAbstractResourceInstance.refresh in internal/tofu, minus hooks, the
// evaluation context, and the already-in-state check, and with one
// deliberate semantic difference: where import treats a nonexistent remote
// object as a hard error, a projection treats it as an ordinary absence.
func importAndRead(ctx context.Context, provider providers.Interface, schema providers.Schema, typeName string, target providers.ImportTarget, importID string) (*states.ResourceInstanceObject, materializeStatus, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	if !target.IsIdentityBased() && !target.IsIDBased() {
		diags = diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Empty import identity",
			fmt.Sprintf("Nothing was computed as the import identity for a %s: no identity object and an empty import ID. Identity resolution should never produce one.", typeName),
		))
		return nil, statusFailed, diags
	}

	importResp := provider.ImportResourceState(ctx, providers.ImportResourceStateRequest{
		TypeName: typeName,
		Target:   target,
	})
	if importResp.Diagnostics.HasErrors() {
		// The provider could not answer the question. That is different
		// from answering "there is no such object", which is either an
		// empty ImportedResources or a null object out of the read below.
		diags = diags.Append(importResp.Diagnostics.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Cannot import for projection",
			fmt.Sprintf(
				"The provider failed while looking up the %s with identity %q. A projection cannot be built while a provider is erroring, because the resulting plan would propose creating resources that may already exist.",
				typeName, importID,
			),
		)))
		return nil, statusFailed, diags
	}
	diags = diags.Append(importResp.Diagnostics)

	imported, extras := pickImported(importResp.ImportedResources, typeName)
	if imported == nil {
		// The provider returned nothing at all for this identity, which is
		// how several resource types report "no such object" without an
		// error.
		return nil, statusAbsent, diags
	}
	for _, extra := range extras {
		diags = diags.Append(tfdiags.Sourceless(
			tfdiags.Warning,
			"Ignoring an additional imported object",
			fmt.Sprintf(
				"Importing the %s with identity %q also produced an object of type %q. A projection has no configuration address to file that under, so it is not included.",
				typeName, importID, extra,
			),
		))
	}

	obj := imported.AsInstanceObject()
	if obj.Value == cty.NilVal || obj.Value.IsNull() {
		return nil, statusAbsent, diags
	}

	readResp := provider.ReadResource(ctx, providers.ReadResourceRequest{
		TypeName:   typeName,
		PriorState: obj.Value,
		Private:    obj.Private,
		// A null of the dynamic pseudo-type, not the zero cty.Value: the
		// plugin client marshals ProviderMeta whenever the provider
		// declares a provider_meta schema (the AWS provider does), and
		// marshalling starts with a conformance check that panics on a
		// value with no type at all. This is the same value
		// NodeAbstractResourceInstance.providerMetas defaults to. A
		// projection has no provider_meta block to evaluate, so null is
		// also the correct answer.
		ProviderMeta:  cty.NullVal(cty.DynamicPseudoType),
		PriorIdentity: obj.Identity,
	})
	if readResp.Diagnostics.HasErrors() {
		diags = diags.Append(readResp.Diagnostics.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Cannot read for projection",
			fmt.Sprintf(
				"The provider failed while refreshing the %s imported with identity %q.",
				typeName, importID,
			),
		)))
		return nil, statusFailed, diags
	}
	diags = diags.Append(readResp.Diagnostics)

	if readResp.NewState == cty.NilVal {
		// Not reachable over the plugin RPC channel, but reachable from a
		// sloppy in-process provider, and a panic here would be a poor
		// error message.
		diags = diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"No state returned by the provider",
			fmt.Sprintf("Reading the %s imported with identity %q produced no object at all, not even a null one. This is a bug in the provider.", typeName, importID),
		))
		return nil, statusFailed, diags
	}
	if readResp.NewState.IsNull() {
		// The ordinary "it does not exist" answer.
		return nil, statusAbsent, diags
	}

	if errs := readResp.NewState.Type().TestConformance(schema.Block.ImpliedType()); len(errs) > 0 {
		for _, err := range errs {
			diags = diags.Append(tfdiags.Sourceless(
				tfdiags.Error,
				"Provider produced an invalid object",
				fmt.Sprintf(
					"Reading the %s imported with identity %q produced a value that does not conform to the provider's own schema: %s. This is a bug in the provider.",
					typeName, importID, tfdiags.FormatError(err),
				),
			))
		}
		return nil, statusFailed, diags
	}

	newVal := objchange.NormalizeObjectFromLegacySDK(readResp.NewState, schema.Block)
	if !newVal.RawEquals(readResp.NewState) {
		log.Printf("[WARN] projection: provider produced an invalid new value containing null blocks for %s %q", typeName, importID)
	}

	// Sensitivity declared by the schema has to be carried on the value,
	// because that is where the plan renderer looks for it. Marks that
	// come from configuration are not applied here: they need an
	// evaluation context, and a projection is built before one exists.
	if pvms := schema.Block.ValueMarks(newVal, nil, nil); len(pvms) > 0 {
		newVal = newVal.MarkWithPaths(pvms)
	}

	return &states.ResourceInstanceObject{
		Status:   states.ObjectReady,
		Value:    newVal,
		Private:  readResp.Private,
		Identity: readResp.NewIdentity,
	}, statusMaterialized, diags
}

// pickImported selects the imported object that belongs at the address
// being materialized, and reports the type names of any others. The import
// protocol lets a provider return several related objects from one call;
// tofu import files them under synthesized addresses, but a projection is
// building state for addresses that already exist in configuration, so
// there is nowhere to put the extras.
func pickImported(imported []providers.ImportedResource, typeName string) (*providers.ImportedResource, []string) {
	var chosen *providers.ImportedResource
	var extras []string
	for i := range imported {
		ir := imported[i]
		switch {
		case ir.TypeName == typeName && chosen == nil:
			chosen = &imported[i]
		case ir.TypeName == "" && chosen == nil:
			// Some providers leave the type name off when there is only
			// one object and it is obviously the requested type.
			chosen = &imported[i]
		default:
			name := ir.TypeName
			if name == "" {
				name = typeName
			}
			extras = append(extras, name)
		}
	}
	return chosen, extras
}

// providerFor is the provider configuration one instance is read through:
// the one its resource block names, or - for an instance whose block is gone
// - the one the run supplied, falling back to the provider the resource type
// implies in the root module.
//
// The fallback is the ordinary implied-provider rule ("aws_vpc" means the
// module's "aws" provider), with no alias, because an alias is a property of
// the resource block and the block is what is missing. A configuration whose
// deleted block used an aliased provider in a different account is therefore
// the one case this cannot serve, and it reports rather than reads through
// the wrong account: [statelessDiscoveryProvider] already refuses to
// discover across several provider configurations, so a run that got this
// far has exactly one.
func (b *builder) providerFor(rc *configs.Resource, typeName string) (addrs.AbsProviderConfig, bool) {
	if rc != nil {
		return providerConfigAddr(rc), true
	}
	if b.opts.UndeclaredProvider.Provider.Type != "" {
		return b.opts.UndeclaredProvider, true
	}
	implied := b.cfg.Module.ImpliedProviderForUnqualifiedType(impliedProviderName(typeName))
	if implied.Type == "" {
		return addrs.AbsProviderConfig{}, false
	}
	return addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: implied}, true
}

// impliedProviderName is the local provider name a resource type implies:
// everything before the first underscore, which is the rule the configuration
// loader uses for a resource block with no provider argument.
func impliedProviderName(typeName string) string {
	if i := strings.Index(typeName, "_"); i > 0 {
		return typeName[:i]
	}
	return typeName
}

// dependencies is the config-level dependency set of a resource block: the
// managed resources its arguments refer to. The plan engine uses these for
// destroy ordering when a resource's configuration is gone, which is
// exactly the case a projection has to survive.
//
// An instance with no resource block gets none, and that is a real cost
// stated rather than papered over: dependency order for a resource whose
// configuration is gone is exactly what a state file remembers and a
// projection cannot. Destroying it happens in whatever order the graph
// derives from the resources that do have configuration. In practice the
// cases that matter are handled by the cloud itself, which refuses to delete
// a VPC that still has a subnet in it, and a refusal is a legible error
// rather than a silent wrong order.
func (b *builder) dependencies(rc *configs.Resource, schema providers.Schema) []addrs.ConfigResource {
	key := rc.Addr().String()
	if deps, ok := b.depsByType[key]; ok {
		return deps
	}

	self := rc.Addr()
	seen := make(map[string]addrs.ConfigResource)

	// Reference errors are not reported here: a body that cannot be walked
	// for references is a configuration error, and it is validation's job
	// to say so with source ranges. Recording no dependency is the safe
	// answer for a structure that is thrown away at the end of the run.
	refs, _ := lang.ReferencesInBlock(addrs.ParseRef, rc.Config, schema.Block)
	dependsOn, _ := lang.References(addrs.ParseRef, rc.DependsOn)
	refs = append(refs, dependsOn...)
	for _, ref := range refs {
		var res addrs.Resource
		switch sub := ref.Subject.(type) {
		case addrs.Resource:
			res = sub
		case addrs.ResourceInstance:
			res = sub.Resource
		default:
			continue
		}
		if res.Mode != addrs.ManagedResourceMode {
			continue
		}
		if res.Equal(self) {
			continue
		}
		cr := res.InModule(addrs.RootModule)
		seen[cr.String()] = cr
	}

	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	deps := make([]addrs.ConfigResource, 0, len(keys))
	for _, k := range keys {
		deps = append(deps, seen[k])
	}
	if len(deps) == 0 {
		deps = nil
	}
	b.depsByType[key] = deps
	return deps
}

func (b *builder) omit(addr addrs.AbsResourceInstance, reason Reason, detail, cause string) {
	key := addr.String()
	if _, exists := b.omitted[key]; exists {
		return
	}
	o := Omission{Addr: addr, Reason: reason, Detail: detail}
	b.omitted[key] = o
	b.omissionList = append(b.omissionList, o)
	b.causes[key] = cause
}

// omitFailed is the common case: an omission that also produced an error
// diagnostic, so the detail is already written and the cause is the same
// for all of them.
func (b *builder) omitFailed(addr addrs.AbsResourceInstance, detail string) {
	b.omit(addr, ReasonFailed, detail, "reading it from the provider failed.")
}

func needsDiscoveryDetail(r identity.Resolution) string {
	return "No import identity exists for this instance: " + discoveryReason(r) + " Marker discovery will find it; until then the plan will propose creating it."
}

func needsDiscoveryCause(r identity.Resolution) string {
	return strings.TrimSuffix(discoveryReason(r), ".") + ", so only marker discovery can find it."
}

func discoveryReason(r identity.Resolution) string {
	reason := r.Reason
	if reason == "" {
		reason = "its identity is assigned by the provider and appears nowhere in configuration."
	}
	if !strings.HasSuffix(reason, ".") {
		reason += "."
	}
	return reason
}

// providerConfigAddr is the absolute provider configuration a resource
// block uses. Only the root module exists in stateless mode v0, so the
// module path is always the root.
func providerConfigAddr(rc *configs.Resource) addrs.AbsProviderConfig {
	return addrs.AbsProviderConfig{
		Module:   addrs.RootModule,
		Provider: rc.Provider,
		Alias:    rc.ProviderConfigAddr().Alias,
	}
}

// attrString reads one attribute out of a materialized object's value and
// renders it as the string an import identity needs, refusing anything
// that would silently produce a wrong identity.
func attrString(obj cty.Value, attr string) (string, error) {
	if obj == cty.NilVal || obj.IsNull() {
		return "", fmt.Errorf("the parent's object is null")
	}
	ty := obj.Type()
	if !ty.IsObjectType() || !ty.HasAttribute(attr) {
		return "", fmt.Errorf("the parent's object has no attribute %q", attr)
	}
	val, marks := obj.GetAttr(attr).Unmark()
	if len(marks) > 0 {
		return "", fmt.Errorf("the value of %q is marked (sensitive or ephemeral) and must not be composed into an import identity", attr)
	}
	if !val.IsKnown() {
		return "", fmt.Errorf("the value of %q is unknown", attr)
	}
	if val.IsNull() {
		return "", fmt.Errorf("the value of %q is null", attr)
	}
	str, err := convert.Convert(val, cty.String)
	if err != nil {
		return "", fmt.Errorf("the value of %q is not usable as a string: %w", attr, err)
	}
	return str.AsString(), nil
}
