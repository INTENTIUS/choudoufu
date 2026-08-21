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

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hcldec"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/lang"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/moved"
	"github.com/intentius/choudoufu/internal/live/providerscope"
	"github.com/intentius/choudoufu/internal/live/staterecord"
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
	//
	// [Options.UndeclaredProviders] takes precedence over this field per
	// instance, when it has an entry; this field is then only the fallback
	// for an undeclared instance the map does not name. A single-provider
	// caller can keep setting only this field, exactly as every caller did
	// before issue #69.
	UndeclaredProvider addrs.AbsProviderConfig

	// UndeclaredProviders overrides UndeclaredProvider per resolved instance
	// address (keyed by [addrs.AbsResourceInstance.String]), for an estate
	// whose managed resources span more than one provider configuration
	// (issue #69, aliased providers - typically multi-region). A sweep run
	// once per distinct provider configuration
	// ([internal/command/live_plan.go]'s statelessDiscover,
	// [discovery.Merge]) attributes each undeclared resource it finds to the
	// provider configuration that found it, because that account and region
	// is where the resource actually lives and any other provider
	// configuration would read the wrong place.
	//
	// Nil for a single-provider estate, which is what keeps
	// UndeclaredProvider alone sufficient and this package's behavior
	// byte-identical to before issue #69 whenever there is only one provider
	// configuration to begin with.
	UndeclaredProviders map[string]addrs.AbsProviderConfig

	// Ownership is the rule deciding which live objects may enter the prior
	// state. Nil means no check, which is what a caller that has no estate
	// concept at all - the marker rewrite in internal/live/mv, reading
	// one resource it was handed the identity of - passes. Every path that
	// builds a prior state for a plan sets it. See [Ownership].
	Ownership *Ownership

	// RecordStore is where GitHub issue #73's record-backed resource
	// instances (identity.ClassRecordBacked) read their prior state from.
	// Nil means no store: since internal/live/lint refuses every
	// RECORD_ADMITTED type before resolution runs unless a live block
	// configures one, resolutions of that class ordinarily only arrive
	// here when this is also set - see builder.materializeRecord for the
	// defensive path when they do not.
	RecordStore staterecord.Store

	// RecordKeyPrefix is the key namespace RecordStore's keys are built
	// under - ordinarily [RecordKeyPrefix](estate), or a record_store
	// block's key_prefix override. Unused when RecordStore is nil.
	RecordKeyPrefix string

	// LocatedStore is where GitHub issue #270's record-located instances
	// (identity.ClassRecordLocated) read their IMPORT IDENTITY from - not
	// their state, which is read from the cloud like any other resource's.
	//
	// It is a separate field from [Options.RecordStore], and a
	// [*LocatedStore] rather than a [staterecord.Store], because the two
	// answer different questions and only one of them may ever be
	// enumerated. See located.go: a located key lives under its own
	// namespace root and this type exposes no List, so
	// builder.discoverOrphanedRecords - which takes a staterecord.Store -
	// cannot be handed one and cannot be given a located key by one. The
	// underlying store is ordinarily the same one RecordStore holds.
	//
	// Nil means no store, and builder.materializeLocated then raises the
	// "Record-located instance with no record store" error rather than
	// guessing. internal/live/lint's admission gate is supposed to make
	// that unreachable, the same way it does for record-backed.
	LocatedStore *LocatedStore

	// ResidueStore is where GitHub issue #275's argument-level residue is
	// read from: the values this estate last SENT for arguments the
	// provider's Read never gives back, so that a cold replan does not
	// re-propose sending them forever.
	//
	// A third namespace in ordinarily the same underlying store, a third
	// point-lookup type with no List, and separate from both of the others
	// for residue.go's reason: a residue key describes the arguments of a
	// live cloud object, and the only enumeration in this package proposes
	// destroying what it finds.
	//
	// Nil means no store, which is not an error at any level: an estate
	// with no record_store block simply keeps the perpetual update it had
	// before this mechanism existed. That is visible, which is why it is
	// allowed to be the default.
	ResidueStore *ResidueStore
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
	}

	b := newBuilder(ctx, cfg, provs, opts)
	b.run(ctx, resolutions)

	sortOmissions(b.omissionList)
	sort.Slice(b.materialized, func(i, j int) bool {
		return b.materialized[i].String() < b.materialized[j].String()
	})

	sortUnowned(b.unownedList)
	sort.Slice(b.recordVersions, func(i, j int) bool {
		return b.recordVersions[i].Addr.String() < b.recordVersions[j].Addr.String()
	})
	sort.Slice(b.locatedVersions, func(i, j int) bool {
		return b.locatedVersions[i].Addr.String() < b.locatedVersions[j].Addr.String()
	})
	sort.Slice(b.residueVersions, func(i, j int) bool {
		return b.residueVersions[i].Addr.String() < b.residueVersions[j].Addr.String()
	})
	sort.Slice(b.policyList, func(i, j int) bool {
		return b.policyList[i].Addr.String() < b.policyList[j].Addr.String()
	})

	res := &Result{
		State:           b.state,
		Materialized:    b.materialized,
		Omitted:         b.omissionList,
		Unowned:         b.unownedList,
		RecordVersions:  b.recordVersions,
		LocatedVersions: b.locatedVersions,
		ResidueVersions: b.residueVersions,
		Policy:          b.policyList,
	}
	return res, diags.Append(b.diags)
}

// newBuilder is the one place a builder is constructed, shared by
// [buildFrom] and by [ReadInstances] so that a narrow read talks to
// providers through exactly the same cache, the same identity check and the
// same `moved` set a full projection does. A second construction site here
// is how one of the two would come to read through a differently-configured
// provider than the other.
func newBuilder(ctx context.Context, cfg *configs.Config, provs Providers, opts Options) *builder {
	// The config-side naming signal, for the identity check the provider
	// cache runs when the schemas arrive (schema_check.go). Its diagnostics
	// are dropped on purpose: a configuration whose expansion this cannot
	// enumerate is a resolution failure the caller has already seen, and a
	// second copy of it here would fail a projection over a report.
	signal, _ := identity.ScanConfig(ctx, cfg)

	return &builder{
		cfg:        cfg,
		opts:       opts,
		providers:  newProviderCache(provs, signal),
		state:      states.NewState(),
		live:       make(map[string]cty.Value),
		omitted:    make(map[string]Omission),
		causes:     make(map[string]string),
		depsByType: make(map[string][]addrs.ConfigResource),
		// The `moved` blocks this configuration's markers may follow (GitHub
		// issue #198), computed once for the whole projection because
		// [builder.checkOwnership] asks about them per instance. A
		// configuration with no moved blocks gets an empty slice here and
		// [moved.Accepts] degenerates to one address comparison.
		movedStmts: moved.Honoured(cfg),
	}
}

type builder struct {
	cfg       *configs.Config
	opts      Options
	providers *providerCache

	// movedStmts is the honoured `moved` statements of cfg, which decide
	// which prior tofu-address markers still name a declared instance. See
	// [builder.checkOwnership].
	movedStmts []moved.Statement

	state *states.State

	// live holds the materialized object value of each instance already in
	// the projection, keyed by address string. It is what a parent-derived
	// formula reads its parents' live IDs out of.
	live map[string]cty.Value

	omitted      map[string]Omission
	omissionList []Omission
	materialized []addrs.AbsResourceInstance
	unownedList  []Unowned
	policyList   []PolicyOutcome

	// recordVersions is the version read at plan time for every
	// record-backed instance whose record actually existed - GitHub issue
	// #73's write-back needs it to open PutIfVersion/Delete with the right
	// expected version. An instance with no prior record (about to be
	// created) has no entry here, which write-back reads as expectedVersion
	// "" - a create assertion, exactly [staterecord.Store]'s own convention.
	recordVersions []RecordVersion

	// locatedVersions is recordVersions' counterpart for GitHub issue
	// #270's located instances: the version read at plan time for every
	// located record that already existed, so [WriteBack] can open its
	// conditional Put with the right expected version. An instance with no
	// entry here had no located record, which write-back reads as
	// expectedVersion "" - a create assertion.
	locatedVersions []RecordVersion

	// residueVersions is the same field again for GitHub issue #275's
	// residue records: the version read at plan time for every residue
	// record that already existed, so [WriteBack]'s conditional Put opens
	// with the right expected version. An instance with no entry here had
	// no residue record, which write-back reads as expectedVersion "" - a
	// create assertion.
	residueVersions []RecordVersion

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
	concrete, derived, needsDiscovery, cyclic, recordBacked, located := orderWork(resolutions)

	for _, r := range needsDiscovery {
		b.omit(r.Addr, ReasonNeedsDiscovery, needsDiscoveryDetail(r), needsDiscoveryCause(r))
	}

	declaredRecordBacked := make(map[string]bool, len(recordBacked))
	for _, r := range recordBacked {
		declaredRecordBacked[r.Addr.String()] = true
		b.materializeRecord(ctx, r.Addr, false)
	}
	b.discoverOrphanedRecords(ctx, declaredRecordBacked)

	// GitHub issue #270's located instances run before the concrete ones so
	// that a parent-derived formula naming a located parent finds it in
	// b.live by the time the derived phase reads it - the same reason
	// concrete runs before derived. There is deliberately no
	// discoverOrphanedRecords counterpart: nothing enumerates the located
	// namespace, which is the point of it.
	for _, r := range located {
		b.materializeLocated(ctx, r.Addr)
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
			values:     r.IdentityValues,
			undeclared: r.Undeclared,
		})
	}
	for _, r := range derived {
		id, values, ok := b.renderFormula(r)
		if !ok {
			continue
		}
		b.materialize(ctx, wanted{
			addr:       r.Addr,
			importID:   id,
			identity:   r.Identity,
			values:     values,
			undeclared: r.Undeclared,
		})
	}
}

// wanted is one instance's identity in every form this run holds it, which is
// the input [builder.materialize] works from.
//
// The forms are not alternatives to choose between up front. The string is
// what every operator-facing line prints and what a marker rewrite records,
// so it is always carried; whether either identity form can be used at all is
// a question about the provider's schema, which is not known until a plugin
// is on the line. So all of them travel here and [importTarget] decides per
// resource, once the schema has arrived.
type wanted struct {
	addr addrs.AbsResourceInstance

	// importID is the provider's import-ID string.
	//
	// Empty for a type identified by several attributes with no separator
	// between them: there is no such string, and inventing one is what
	// GitHub issue #105 exists to prevent. See
	// [identity.TypeIdentity.IdentityObjectOnly]. Such an instance is
	// imported by identity object or refused, never by an approximation.
	importID string

	// identity is the provider's own resource identity object, when
	// something served one: a marker sweep's list results carry it. Null
	// otherwise.
	identity cty.Value

	// values is the identity the configuration supplies, one string per
	// identity attribute, which is importID unjoined. Nil for a type whose
	// entry does not say which attribute each component feeds. See
	// [identity.Resolution.IdentityValues].
	values map[string]string

	undeclared bool
}

// importTarget picks the form this instance's import is asked in.
//
// There are three sources and they rank in this order:
//
//   - The provider's own identity object, when a list call served one. It is
//     the provider's account of what names this resource, unaltered.
//   - The identity the configuration supplies, attribute by attribute, when
//     it covers every attribute the provider requires for import. This is
//     the same information the import-ID string holds, minus the separator
//     characters that only the string has - and the separators are the half
//     of the identity table that no schema can back.
//   - The import-ID string. Not a lesser answer: it is the only form
//     available for a type the provider serves no identity schema for, and
//     the only one for a type whose identity is something the configuration
//     does not hold (aws_route_table_association).
//
// The first two are exclusive with the third on the wire:
// [providers.ImportTarget.IsIdentityBased] decides, and both plugin protocols
// error rather than falling back when they are handed an identity for a type
// with no identity schema. So the choice is made here, where the schema is in
// hand, and exactly one field is set.
func importTarget(w wanted, schema providers.Schema) providers.ImportTarget {
	byID := providers.ImportTarget{ID: w.importID}

	if schema.IdentitySchema == nil {
		return byID
	}

	if w.identity != cty.NilVal && !w.identity.IsNull() {
		val, err := convert.Convert(w.identity, schema.IdentitySchema.ImpliedType())
		if err == nil && !val.IsNull() && val.IsWhollyKnown() {
			return providers.ImportTarget{Identity: val}
		}
		// The provider served an identity its own schema does not describe,
		// which is a provider bug rather than anything this run can act on.
		// The import ID came off the same list result, so there is a working
		// answer and no reason to fail.
		log.Printf("[WARN] projection: %s came back with an identity that does not fit %s's identity schema (%v); falling back",
			w.addr, w.addr.Resource.Resource.Type, err)
	}

	if val, ok := identityFromValues(w, schema); ok {
		return providers.ImportTarget{Identity: val}
	}

	// The fallback is the string, and for a type that has none there is no
	// fallback to take. byID is {ID: ""}, which [importAndRead] refuses
	// rather than sending to a provider - GitHub issue #105's point exactly:
	// the danger was never an empty ID, it was a plausible one. A
	// separator-less composite whose values were joined anyway would arrive
	// here as "prodsvc" and be imported against a real account.
	if w.importID == "" {
		log.Printf("[WARN] projection: %s is identified by identity object only and one could not be built from configuration; there is no import ID to fall back to",
			w.addr)
	}
	return byID
}

// identityFromValues builds an identity object out of what the configuration
// said, checked against the provider's identity schema rather than asserted.
//
// Two bars, and both are about the schema being the authority. Every
// attribute the provider requires for import has to be one the configuration
// supplied, because an identity missing a required attribute names nothing;
// and every attribute the configuration supplied has to be one the schema
// has, because a table entry that maps an argument onto an attribute this
// provider version does not carry is a stale inference and the import ID it
// also produced is the safe reading of it. Failing either drops to the
// string, which is what every run did before this existed.
//
// Optional attributes the configuration says nothing about are left null on
// purpose: in the AWS provider they are account_id and region, the context
// the provider fills in from its own configuration, and filling them in from
// here would be this package guessing which account a run is against - the
// thing [identity.CloudContext] exists to refuse.
func identityFromValues(w wanted, schema providers.Schema) (cty.Value, bool) {
	if len(w.values) == 0 {
		return cty.NilVal, false
	}
	body := schema.IdentitySchema

	vals := make(map[string]cty.Value, len(body.Attributes))
	for name, at := range body.Attributes {
		if at.NestedType != nil {
			// No AWS identity schema has one, and a string per attribute is
			// the only shape the identity table can express, so an identity
			// with structure in it is one this cannot build rather than one
			// to approximate.
			log.Printf("[TRACE] projection: %s's identity attribute %q has a nested type, which an identity built from configuration cannot fill; importing by ID %q",
				w.addr.Resource.Resource.Type, name, w.importID)
			return cty.NilVal, false
		}
		raw, supplied := w.values[name]
		if !supplied {
			if at.Required {
				log.Printf("[TRACE] projection: %s supplies no %q, which %s's identity schema requires; importing by ID %q",
					w.addr, name, w.addr.Resource.Resource.Type, w.importID)
				return cty.NilVal, false
			}
			vals[name] = cty.NullVal(at.Type)
			continue
		}
		val, err := convert.Convert(cty.StringVal(raw), at.Type)
		if err != nil {
			log.Printf("[WARN] projection: %s's %q is %q, which is not a %s; importing by ID %q",
				w.addr, name, raw, at.Type.FriendlyName(), w.importID)
			return cty.NilVal, false
		}
		vals[name] = val
	}
	for name := range w.values {
		if _, ok := body.Attributes[name]; !ok {
			log.Printf("[TRACE] projection: the identity table supplies %q for %s and the provider's identity schema has no such attribute; importing by ID %q",
				name, w.addr.Resource.Resource.Type, w.importID)
			return cty.NilVal, false
		}
	}
	return cty.ObjectVal(vals), true
}

// normalizeIdentityAttrs asks the provider that just materialized obj
// whether obj's own identity-bearing attributes would survive being
// resubmitted as a brand-new create, and adopts the provider's answer where
// they would not.
//
// GitHub issue #281. A rendered identity component answers "may I delete
// this", but it is also reused as ordinary prior-state, and from there it
// has to survive comparison against the SAME argument's value on every
// later plan. When a provider normalizes an argument before it ever reaches
// the wire - AWS Route 53 strips a record name's trailing dot at plan time,
// confirmed empirically against floci: a create-shaped PlanResourceChange
// answers "foo.example.com" for a config literal of "foo.example.com." -
// an identity built from the raw configuration string can bind the correct
// live object under the wrong spelling. Import succeeds regardless - the
// API accepts either form - so the object materializes, but its ReadResource
// answer can still carry whatever spelling the import happened to ask for
// rather than the provider's own canonical one, and the ordinary plan that
// follows compares that spelling against the config's own (normalized)
// rendering and proposes a forced replace, once per run, forever.
//
// This names no resource type anywhere. It works by feeding the provider
// exactly what it already told this run: PriorState is null (a synthetic
// create), Config is obj stripped of its purely-computed attributes (see
// configValue), and whatever PlannedState says for an identity-bearing
// attribute IS what that attribute canonicalizes to under this provider,
// because the provider - never this package - decides what its own create
// path does to a string.
//
// Scoped narrowly on purpose:
//
//   - Only identity-bearing attributes are ever candidates - the ones
//     [identity.TypeIdentity.Components] named for this instance (w.values's
//     keys), never every attribute obj carries. A component the
//     configuration built the import identity from is exactly the value
//     every later run keeps comparing against configuration, which is the
//     one place a normalization mismatch turns into a standing replace.
//   - Only a plain top-level string attribute the resource schema and the
//     identity table agree is the same attribute is ever a candidate. A
//     component whose identity attribute is not also a same-named schema
//     attribute (an inAttr composite built from several components, or a
//     nested type) is left exactly as ReadResource returned it.
//
// Deliberately NOT gated on whether obj's current value already disagrees,
// textually, with what the configuration wrote (w.values): agreement
// between the two is exactly what the broken case looks like. w.values is
// what built the import request in the first place, so when a provider
// preserves an identity-object import's own input verbatim - rather than
// overwriting it with ReadResource's fresh answer - obj and the
// configuration agree with EACH OTHER while both disagree with what the
// provider's own create path would normalize either of them to (issue
// #281's exact shape). One PlanResourceChange call covers every candidate
// attribute of one instance; an instance with no identity-bearing string
// attributes makes no call at all.
//
// Any provider error - a synthetic create-shaped plan that does not
// validate for reasons this question never touches - leaves obj exactly as
// ReadResource returned it. This can only fail closed: the worst outcome is
// the pre-#281 behavior for that one instance, never a value this package
// invented.
//
// The return value is the same information restated as plain strings, keyed
// by attribute name: what [builder.materialize] uses to bring the LOGGED
// identity - importID, built from the same raw values before any of this
// ran - back in step with the object it now actually describes, so an
// operator reading TF_LOG=trace sees the spelling this instance settled on
// rather than the one that turned out not to survive contact with the
// provider. Nil when nothing changed.
func (b *builder) normalizeIdentityAttrs(ctx context.Context, provider providers.Interface, schema providers.Schema, typeName string, w wanted, obj *states.ResourceInstanceObject) map[string]string {
	if obj == nil || len(w.values) == 0 || schema.Block == nil {
		return nil
	}
	if obj.Value == cty.NilVal || obj.Value.IsNull() || !obj.Value.IsWhollyKnown() {
		return nil
	}

	type candidate struct {
		name string
		live cty.Value
	}
	var candidates []candidate
	for name := range w.values {
		attrSchema, declared := schema.Block.Attributes[name]
		if !declared || attrSchema.NestedType != nil || attrSchema.Type != cty.String {
			continue
		}
		live := obj.Value.GetAttr(name)
		if !live.IsKnown() || live.IsNull() || live.IsMarked() {
			// A marked value - sensitive, in this codebase's only sense of
			// the word - never becomes an identity component or a log
			// line, the same rule [resolver.stringValue] already applies to
			// every OTHER source an identity is built from. Refused, not
			// unmarked: this instance's identity-bearing attribute is left
			// exactly as ReadResource returned it.
			continue
		}
		// Deliberately not gated on "does this already agree with what the
		// configuration wrote": that agreement is exactly what the broken
		// case looks like. w.values["name"] is the raw configuration
		// string, which is also what built the import request in the
		// first place - so when a provider preserves an identity-object
		// import's own input verbatim (rather than overwriting it with
		// ReadResource's fresh answer), obj's value and the configuration
		// agree with EACH OTHER while both disagree with what the
		// provider's own create path would normalize either of them to.
		// Checked once per instance below, never per attribute.
		candidates = append(candidates, candidate{name: name, live: live})
	}
	if len(candidates) == 0 {
		return nil
	}

	priorNull := cty.NullVal(schema.Block.ImpliedType())
	cfgVal := configValue(schema.Block, obj.Value)
	proposed := objchange.ProposedNew(schema.Block, priorNull, cfgVal)
	planResp := provider.PlanResourceChange(ctx, providers.PlanResourceChangeRequest{
		TypeName:         typeName,
		PriorState:       priorNull,
		ProposedNewState: proposed,
		Config:           cfgVal,
		// See importAndRead's identical call: a null of the dynamic
		// pseudo-type, never the zero cty.Value, or a provider that
		// declares a provider_meta schema panics its own conformance check.
		ProviderMeta: cty.NullVal(cty.DynamicPseudoType),
	})
	if planResp.Diagnostics.HasErrors() {
		log.Printf("[TRACE] projection: %s's identity-bearing attributes could not be checked against the provider's own create-time plan (%s); left as ReadResource returned them",
			w.addr, planResp.Diagnostics.Err())
		return nil
	}
	if planResp.PlannedState == cty.NilVal || planResp.PlannedState.IsNull() {
		return nil
	}

	updates := make(map[string]cty.Value, len(candidates))
	changed := make(map[string]string, len(candidates))
	for _, c := range candidates {
		planned := planResp.PlannedState.GetAttr(c.name)
		if !planned.IsKnown() || planned.IsNull() || planned.Type() != cty.String || planned.IsMarked() {
			continue
		}
		if c.live.IsMarked() {
			// Unreachable in practice - the candidate loop above already
			// filtered this out - but proven here too, at the point of
			// use, rather than trusted across the struct field it travels
			// through.
			continue
		}
		if planned.RawEquals(c.live) {
			continue
		}
		updates[c.name] = planned
		changed[c.name] = planned.AsString()
		log.Printf("[TRACE] projection: %s's %s normalized from %q to %q by the provider's own create-time plan; adopting the provider's spelling",
			w.addr, c.name, c.live.AsString(), planned.AsString())
	}
	if len(updates) == 0 {
		return nil
	}
	obj.Value = withReplacedAttrs(obj.Value, updates)
	return changed
}

// configValue is the live object as a configuration would express it: every
// attribute the provider alone can set is nulled, and everything else is
// carried across as it stands. It exists so that [builder.normalizeIdentityAttrs]
// can ask a provider "what would you store if this were a brand-new create",
// using the object the provider itself already returned as the config -
// mirroring internal/live/mv/rewrite.go's identical helper, which asks the
// same kind of question for a tags-only rewrite. Types are never altered,
// only values are nulled, so the result still conforms to the schema's
// implied type.
func configValue(block *configschema.Block, val cty.Value) cty.Value {
	if block == nil || val == cty.NilVal || val.IsNull() || !val.IsKnown() {
		return val
	}

	vals := make(map[string]cty.Value, len(block.Attributes)+len(block.BlockTypes))
	for name, attr := range block.Attributes {
		v := val.GetAttr(name)
		switch {
		case attr.Computed && !attr.Optional && !attr.Required:
			vals[name] = cty.NullVal(v.Type())
		case attr.NestedType != nil:
			vals[name] = configNestedObject(attr.NestedType, v)
		default:
			vals[name] = v
		}
	}
	for name, nested := range block.BlockTypes {
		v := val.GetAttr(name)
		switch nested.Nesting {
		case configschema.NestingSingle, configschema.NestingGroup:
			vals[name] = configValue(&nested.Block, v)
		default:
			vals[name] = mapElementsForConfig(v, func(elem cty.Value) cty.Value {
				return configValue(&nested.Block, elem)
			})
		}
	}
	return cty.ObjectVal(vals)
}

// configNestedObject is configValue for an attribute whose type is a nested
// object rather than a block.
func configNestedObject(obj *configschema.Object, val cty.Value) cty.Value {
	if val == cty.NilVal || val.IsNull() || !val.IsKnown() {
		return val
	}

	one := func(v cty.Value) cty.Value {
		if v.IsNull() || !v.IsKnown() {
			return v
		}
		vals := make(map[string]cty.Value, len(obj.Attributes))
		for name, attr := range obj.Attributes {
			av := v.GetAttr(name)
			switch {
			case attr.Computed && !attr.Optional && !attr.Required:
				vals[name] = cty.NullVal(av.Type())
			case attr.NestedType != nil:
				vals[name] = configNestedObject(attr.NestedType, av)
			default:
				vals[name] = av
			}
		}
		return cty.ObjectVal(vals)
	}

	if obj.Nesting == configschema.NestingSingle || obj.Nesting == configschema.NestingGroup {
		return one(val)
	}
	return mapElementsForConfig(val, one)
}

// mapElementsForConfig rebuilds a collection with every element passed
// through f, preserving the collection kind. An empty or unknown collection
// comes back untouched, since there is nothing to map and rebuilding one
// risks changing its type - and so does a marked one: this helper only ever
// feeds [objchange.ProposedNew] a config value, never an identity component
// or a log line, but LengthInt and ElementIterator both panic on a marked
// receiver, so the same "leave it alone" answer covers both reasons.
func mapElementsForConfig(val cty.Value, f func(cty.Value) cty.Value) cty.Value {
	if val == cty.NilVal || val.IsNull() || !val.IsKnown() || val.IsMarked() || val.LengthInt() == 0 {
		return val
	}

	ty := val.Type()
	switch {
	case ty.IsMapType(), ty.IsObjectType():
		elems := make(map[string]cty.Value)
		for it := val.ElementIterator(); it.Next(); {
			k, v := it.Element()
			elems[k.AsString()] = f(v)
		}
		if ty.IsObjectType() {
			return cty.ObjectVal(elems)
		}
		return cty.MapVal(elems)
	default:
		var elems []cty.Value
		for it := val.ElementIterator(); it.Next(); {
			_, v := it.Element()
			elems = append(elems, f(v))
		}
		if ty.IsSetType() {
			return cty.SetVal(elems)
		}
		return cty.ListVal(elems)
	}
}

// withReplacedAttrs is obj with the named top-level attributes replaced by
// updates, and everything else carried across unchanged.
func withReplacedAttrs(obj cty.Value, updates map[string]cty.Value) cty.Value {
	ty := obj.Type()
	vals := make(map[string]cty.Value, len(ty.AttributeTypes()))
	for name := range ty.AttributeTypes() {
		if v, ok := updates[name]; ok {
			vals[name] = v
			continue
		}
		vals[name] = obj.GetAttr(name)
	}
	return cty.ObjectVal(vals)
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
//
// recordBacked holds GitHub issue #73's record-backed instances
// (identity.ClassRecordBacked), materialized from the record store rather
// than from any of the other four lists' identity machinery - see
// builder.materializeRecord.
//
// located holds GitHub issue #270's record-located instances
// (identity.ClassRecordLocated). It is a list of its own and NOT part of
// needsDiscovery, which is the whole reason this function has an explicit
// case for the class rather than leaving it to the default below: marker
// discovery is a tag sweep, a located type has no tag by definition, and
// routing one there would make the run lint clean and then fail at apply
// against internal/live/stamp's unmarked-apply refusal - a plan refusal
// traded for an apply refusal, which is forbidden. See
// TestOrderWorkRoutesLocatedExplicitly.
func orderWork(resolutions []identity.Resolution) (concrete, derived, needsDiscovery, cyclic, recordBacked, located []identity.Resolution) {
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
		case identity.ClassRecordBacked:
			recordBacked = append(recordBacked, r)
		case identity.ClassRecordLocated:
			located = append(located, r)
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

	return concrete, derived, needsDiscovery, cyclic, recordBacked, located
}

// Schemas is how [EmptyImportIdentityDiagnostics] learns which identity
// attributes a resource type's provider actually has - the same subset of
// [github.com/intentius/choudoufu/internal/tofu.Schemas] that
// [github.com/intentius/choudoufu/internal/live/stamp.Schemas] declares for
// the same reason, so an offline caller that already acquired schemas for
// stamping can hand the same value here.
type Schemas interface {
	ResourceTypeConfig(provider addrs.Provider, resourceMode addrs.ResourceMode, resourceType string) (*providers.Schema, uint64)
}

// CyclicIdentityDiagnostics reports every "Cyclic parent-derived identities"
// refusal [Build] would raise, computed from resolutions alone: [orderWork]
// classifies a parent-derived resolution as cyclic purely from the
// Addr/Formula.Parents graph among resolutions, before any provider is asked
// to import or read anything. This is that half of [builder.run] (see its
// "for _, r := range cyclic" loop) with the live-materializing halves left
// out, so a caller with no provider handle can still see it.
//
// # It cannot fire over resolutions that came from identity.ResolveWith
//
// GitHub issue #262 asked whether any configuration makes this refusal
// reach a user through internal/live/check's Analyze, and the answer is no,
// by construction rather than for want of a fixture:
//
//   - Every [Part] carrying a non-nil ParentRef is built by identity's
//     parentPart, and parentPart returns nothing unless r.instance already
//     resolved that parent to completion. A cyclic configuration therefore
//     fails inside identity, as "Circular identity reference", and neither
//     instance reaches a Resolution at all.
//   - identity's classify is the only place a [Formula] is constructed, and
//     it reads Parents off those same Parts.
//
// So a resolution's Formula.Parents can only name instances that finished
// resolving before it did. That orders the graph by completion time, which
// is acyclic, and orderWork's cyclic list is consequently always empty for
// anything ResolveWith produced. The refusal is defence in depth against a
// bug in identity resolution - which is what its own detail text says - and
// not a refusal a configuration trips. Any caller assembling resolutions by
// some other route still needs it.
//
// internal/live/check's TestAnalyzeCannotReachCyclicParentDerivedIdentities
// pins the empirical half over the configuration that shape describes.
func CyclicIdentityDiagnostics(resolutions []identity.Resolution) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	_, _, _, cyclic, _, _ := orderWork(resolutions)
	for _, r := range cyclic {
		detail := fmt.Sprintf(
			"The identities of %s and the instances it derives from refer to each other in a cycle, so there is no order in which they can be read. This is a bug in identity resolution: a parent-derived identity must name parents that are resolvable first.",
			r.Addr,
		)
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, "Cyclic parent-derived identities", detail))
	}
	return diags
}

// EmptyImportIdentityDiagnostics reports every "Empty import identity"
// refusal [Build] would raise for a [identity.ClassConcrete] resolution,
// computed with no live call: [importTarget] decides purely from the
// resolution's own statically-resolved identity/values and the provider's
// schema whether an identity object or an import-ID string can be built, and
// [importAndRead] refuses before ever touching a provider when neither can.
//
// A [identity.ClassParentDerived] resolution is not checked here. Its
// identity is a formula over a parent's live value ([builder.renderFormula]
// reads b.live, populated only by a prior [builder.materialize] call against
// a real provider), so whether its import target ends up empty cannot be
// decided offline - the omission is real, not an oversight.
//
// cfg supplies each resolution's provider configuration, the same way
// [builder.providerFor] does when a resource block still declares the
// instance; a resolution whose block is gone (Undeclared) is skipped, since
// this entry point has no [Options.UndeclaredProvider] to fall back to and
// guessing one would be worse than staying silent about it.
func EmptyImportIdentityDiagnostics(cfg *configs.Config, resolutions []identity.Resolution, schemas Schemas) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	concrete, _, _, _, _, _ := orderWork(resolutions)
	for _, r := range concrete {
		if r.Undeclared {
			continue
		}
		modCfg, ok := identity.ConfigForModule(cfg, r.Addr.Module)
		if !ok || modCfg == nil || modCfg.Module == nil {
			continue
		}
		rc := modCfg.Module.ManagedResources[r.Addr.Resource.Resource.String()]
		if rc == nil {
			continue
		}
		providerAddr := providerConfigAddr(modCfg, rc)
		typeName := r.Type()
		schema, _ := schemas.ResourceTypeConfig(providerAddr.Provider, addrs.ManagedResourceMode, typeName)
		if schema == nil {
			continue
		}
		w := wanted{
			addr:       r.Addr,
			importID:   r.ImportID,
			identity:   r.Identity,
			values:     r.IdentityValues,
			undeclared: r.Undeclared,
		}
		target := importTarget(w, *schema)
		if !target.IsIdentityBased() && !target.IsIDBased() {
			diags = diags.Append(tfdiags.Sourceless(
				tfdiags.Error,
				"Empty import identity",
				fmt.Sprintf("Nothing was computed as the import identity for a %s: no identity object and no import ID. For a type identified by several attributes with no separator between them, the identity object is the only form there is (see internal/live/identity's IdentityObjectOnly), so an identity the provider's schema would not accept leaves nothing to import by - which is refused here rather than approximated with a string.", typeName),
			))
		}
	}
	return diags
}

// renderFormula turns a parent-derived resolution into a concrete import ID -
// and into the same identity attribute by attribute, when the formula carries
// that split - by reading its parents' live values out of the projection
// built so far. It records an omission and returns false when it cannot.
func (b *builder) renderFormula(r identity.Resolution) (string, map[string]string, bool) {
	if r.Formula == nil {
		detail := fmt.Sprintf("Identity resolution classified %s as parent-derived but attached no formula.", r.Addr)
		b.diags = b.diags.Append(tfdiags.Sourceless(tfdiags.Error, "Parent-derived identity with no formula", detail))
		b.omit(r.Addr, ReasonFailed, detail, "identity resolution gave it no formula to render.")
		return "", nil, false
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
		return "", nil, false
	}

	var lookupDiags tfdiags.Diagnostics
	lookup := func(parent addrs.AbsResourceInstance, attr string) (string, bool) {
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
	}

	id, ok := r.Formula.Render(lookup)
	var values map[string]string
	if ok {
		// The same lookups again over the same parts; both renders succeed
		// or neither does, since the attribute parts are a subset of the
		// whole.
		values, ok = r.Formula.RenderAttrs(lookup)
	}
	if !ok {
		b.diags = b.diags.Append(lookupDiags)
		detail := fmt.Sprintf("The identity formula for %s could not be rendered from its parents' live values.", r.Addr)
		if len(lookupDiags) > 0 {
			detail = lookupDiags[0].Description().Detail
		}
		b.omit(r.Addr, ReasonFailed, detail, "its identity formula could not be rendered from its parents' live values.")
		return "", nil, false
	}
	return id, values, true
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

	modPath := addr.Module.Module()
	var rc *configs.Resource
	var modEval *configs.StaticEvaluator
	if modCfg, ok := identity.ConfigForModule(b.cfg, addr.Module); ok && modCfg.Module != nil {
		rc = modCfg.Module.ManagedResources[addr.Resource.Resource.String()]
		modEval = modCfg.Module.StaticEvaluator
	}
	if rc == nil && !w.undeclared {
		detail := fmt.Sprintf(
			"Identity resolution produced %s, but that resource block is not in the configuration the projection was given. The configuration and the resolutions do not match.",
			addr,
		)
		b.diags = b.diags.Append(tfdiags.Sourceless(tfdiags.Error, "Resolved instance missing from the configuration", detail))
		b.omitFailed(addr, detail)
		return
	}

	providerAddr, providerOK := b.providerFor(rc, modPath, typeName, addr)
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

	// GitHub issue #287 item 8: seed BEFORE the read, not after, because the
	// gap this closes is in what [providers.Configured.ImportResourceState]
	// hands the provider going into [providers.Configured.ReadResource], not
	// in anything this package does with the result. See
	// [configuredTagsSeed]'s doc comment for the mechanism.
	tagsSeed, tagsSeedOK := configuredTagsSeed(ctx, modEval, modPath, rc, schema)

	obj, status, matDiags := importAndRead(ctx, entry.provider, schema, typeName, importTarget(w, schema), importID, tagsSeed, tagsSeedOK)
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

	// Adopt the provider's own spelling of an identity-bearing attribute
	// before anything downstream compares it to configuration - see
	// [builder.normalizeIdentityAttrs] (GitHub issue #281). Ahead of the
	// ownership check on purpose: a mismatch here is about what the value
	// SAYS, not who owns the object, and normalizing first means ownership
	// reads the same object every other check downstream will.
	//
	// importID is rewritten to match: it is built from the same raw
	// component values that just proved not to survive the provider's own
	// create-time plan, and every log line below this point - most of all
	// materialize's own "materialized ... from import identity" trace -
	// exists so an operator can compare what ran against what the live
	// system holds. Left unrewritten, that line would keep printing the
	// spelling this run just found unstable, on an instance whose state
	// no longer carries it.
	for name, newVal := range b.normalizeIdentityAttrs(ctx, entry.provider, schema, typeName, w, obj) {
		if oldVal, ok := w.values[name]; ok && oldVal != "" {
			importID = strings.Replace(importID, oldVal, newVal, 1)
		}
	}

	// Ownership is checked here, on the object the provider returned, and
	// before anything is written into the projection. Everything below this
	// point is what "the estate owns this" means in practice - a prior-state
	// entry the plan may update, and an orphan the plan may destroy once its
	// block is gone - so this is the one place the check belongs.
	// declared is deliberately not just "rc != nil": a surplus count member
	// or a sweep orphan can sit inside a resource block that still exists
	// (rc found by block address, not by this specific instance), and
	// w.undeclared is the resolution's own word on whether this exact
	// instance is one the configuration currently expands to. A surplus
	// member is the one case this still approximates - discovery's bind()
	// does not set Undeclared for it - and that is the same block-level
	// coarsening internal/live/stamp's PolicyUntag already documents,
	// rather than a new one.
	if b.checkOwnership(addr, typeName, importID, schema, obj.Value, rc != nil && !w.undeclared) != ownershipOK {
		return
	}

	// GitHub issue #275's residue, applied AFTER the ownership check and
	// never before it. Ownership is decided on what the CLOUD said, and a
	// stored value must never be in a position to answer "does this estate
	// own this" - that is what the marker is for. Filling first would let a
	// record about a filename argue about a tag.
	b.fillResidueFor(ctx, addr, schema, obj)

	if rc != nil {
		obj.Dependencies = b.dependencies(rc, modPath, schema)
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

// materializeRecord is [builder.materialize]'s counterpart for GitHub issue
// #73's record-backed instances (identity.ClassRecordBacked): it bypasses
// importAndRead entirely and hydrates prior state from
// [Options.RecordStore] instead of a provider's ImportResourceState/
// ReadResource conversation. There is no ownership check - a record-backed
// instance has no cloud object and therefore no ownership tag to carry, the
// same reason [identity.ClassRecordBacked]'s doc comment gives for why no
// marker sweep ever applies to it either - and no import identity of any
// kind: the record itself is the whole of what makes the instance exist.
//
// undeclared marks an instance builder.discoverOrphanedRecords found in the
// store with no matching resource block - a record-backed resource whose
// configuration was removed. It is the record-backed analog of
// [wanted.undeclared]: since a record-backed instance carries no marker
// either, the record store's own key listing is the only way such a
// resource is ever found again, which is exactly what
// discoverOrphanedRecords is for. An undeclared instance's provider is
// implied from its bare type name ([addrs.ImpliedProviderForUnqualifiedType])
// rather than read from a resource block that no longer exists, and it gets
// no computed dependency set, for the same reason [builder.materialize]
// gives undeclared marker-found instances: destroy ordering for a resource
// whose configuration is gone is exactly what a state file remembers and a
// projection cannot.
//
// A provider connection is still needed, for the same two reasons ordinary
// materialize needs one: [providers.Schema] is what lets the stored,
// self-describing value be converted onto today's schema before it is
// trusted (a record written under an older provider version might not
// conform), and configs.Resource plus that schema is what
// builder.dependencies needs to compute destroy ordering.
func (b *builder) materializeRecord(ctx context.Context, addr addrs.AbsResourceInstance, undeclared bool) {
	typeName := addr.Resource.Resource.Type

	modPath := addr.Module.Module()
	modCfg, modOK := identity.ConfigForModule(b.cfg, addr.Module)
	var rc *configs.Resource
	if modOK && modCfg.Module != nil {
		rc = modCfg.Module.ManagedResources[addr.Resource.Resource.String()]
	}
	if rc == nil && !undeclared {
		detail := fmt.Sprintf(
			"Identity resolution produced %s as record-backed, but that resource block is not in the configuration the projection was given. The configuration and the resolutions do not match.",
			addr,
		)
		b.diags = b.diags.Append(tfdiags.Sourceless(tfdiags.Error, "Resolved instance missing from the configuration", detail))
		b.omitFailed(addr, detail)
		return
	}

	if b.opts.RecordStore == nil {
		// Reachable only if a caller resolves a RECORD_ADMITTED type without
		// also configuring a store - internal/live/lint's admission gate is
		// supposed to make that impossible, so this is an internal
		// inconsistency rather than a configuration mistake an operator
		// could have made.
		detail := fmt.Sprintf(
			"%s resolved to a record-backed identity, but no record store was configured for this projection. This is an internal inconsistency: a RECORD_ADMITTED type should never reach here without a live block's record_store.",
			addr,
		)
		b.diags = b.diags.Append(tfdiags.Sourceless(tfdiags.Error, "Record-backed instance with no record store", detail))
		b.omitFailed(addr, detail)
		return
	}

	var providerAddr addrs.AbsProviderConfig
	if rc != nil {
		providerAddr = providerConfigAddr(modCfg, rc)
	} else {
		providerAddr = addrs.AbsProviderConfig{
			Module:   addrs.RootModule,
			Provider: addrs.ImpliedProviderForUnqualifiedType(impliedProviderName(typeName)),
		}
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

	key := RecordKey(b.opts.RecordKeyPrefix, addr)
	payload, version, exists, err := b.opts.RecordStore.Get(ctx, key)
	if err != nil {
		detail := fmt.Sprintf("Reading the persisted record for %s failed: %s.", addr, err)
		b.diags = b.diags.Append(tfdiags.Sourceless(tfdiags.Error, "Cannot read a persisted record", detail))
		b.omitFailed(addr, detail)
		return
	}
	if !exists {
		b.omit(addr, ReasonAbsent,
			fmt.Sprintf(
				"No persisted record exists yet for %s, so this resource has not been created yet. The plan will propose creating it.",
				addr,
			),
			"no persisted record exists yet for it.",
		)
		return
	}

	val, private, status, err := decodeRecordPayload(payload)
	if err != nil {
		detail := fmt.Sprintf("The persisted record for %s could not be read: %s.", addr, err)
		b.diags = b.diags.Append(tfdiags.Sourceless(tfdiags.Error, "Cannot decode a persisted record", detail))
		b.omitFailed(addr, detail)
		return
	}
	// The record's own sensitivity comes off before the conversion and goes
	// back on after it, which is the same order
	// states.ResourceInstanceObjectSrc.Decode uses: unmarshal, then
	// MarkWithPaths. Converting a marked value would work - cty's convert
	// carries marks - but the paths are needed separately anyway, and doing
	// it in one visible place keeps every accessor below this line reading
	// a value nothing has marked.
	unmarkedVal, sensitive := val.UnmarkDeepWithPaths()
	converted, err := convert.Convert(unmarkedVal, schema.Block.ImpliedType())
	if err != nil {
		detail := fmt.Sprintf(
			"The persisted record for %s does not fit %s's current schema: %s. This usually means the record was written under a different provider version. Delete the stale record from the store to let the plan re-create it, or pin the provider version that wrote it.",
			addr, typeName, err,
		)
		b.diags = b.diags.Append(tfdiags.Sourceless(tfdiags.Error, "Persisted record does not match the current schema", detail))
		b.omitFailed(addr, detail)
		return
	}
	// Re-marking before obj.Encode is the whole point of persisting the
	// paths: Encode derives AttrSensitivePaths from the value's own marks,
	// and that is what the plan graph's "before" side decodes back out.
	// Without this the plan proposes a sensitivity-only in-place update for
	// every sensitive attribute, every run, forever.
	if len(sensitive) > 0 {
		converted = converted.MarkWithPaths(sensitive)
	}

	// status came out of the record itself (decodeRecordPayload), not a
	// hardcoded states.ObjectReady: a tainted object has to stay tainted
	// through this hydration, or the ordinary tofu plan graph never sees
	// the reason to force a replace (issue #216 - a record-backed
	// resource has no state file to carry that bit anywhere else).
	obj := &states.ResourceInstanceObject{
		Status:  status,
		Value:   converted,
		Private: private,
	}
	if rc != nil {
		obj.Dependencies = b.dependencies(rc, modPath, schema)
	}

	src, err := obj.Encode(schema.Block.ImpliedType(), uint64(schema.Version), uint64(schema.IdentitySchemaVersion))
	if err != nil {
		detail := fmt.Sprintf("The persisted record for %s could not be encoded into the projection: %s.", addr, err)
		b.diags = b.diags.Append(tfdiags.Sourceless(tfdiags.Error, "Cannot encode a projected object", detail))
		b.omitFailed(addr, detail)
		return
	}

	b.state.EnsureModule(addr.Module).SetResourceInstanceCurrent(addr.Resource, src, providerAddr, addrs.NoKey)
	b.live[addr.String()] = converted
	b.materialized = append(b.materialized, addr)
	b.recordVersions = append(b.recordVersions, RecordVersion{Addr: addr, Version: version})
	log.Printf("[TRACE] projection: materialized %s from a persisted record", addr)
}

// discoverOrphanedRecords is record-backed resources' answer to the
// question ordinary marker discovery answers for cloud resources: "what
// does this estate own that its current configuration no longer declares?"
// A record-backed instance carries no marker and has no cloud object a
// sweep could find, so [Options.RecordStore]'s own key listing is the only
// remaining source of truth - every key under [Options.RecordKeyPrefix] IS
// the estate's set of record-backed resources, the same way an estate's
// tagged live objects are its set of cloud-backed ones.
//
// known is the set of addresses already materialized from a resolved
// resource block (builder.run's declaredRecordBacked); every other decoded
// key is undeclared, and is materialized the same way an undeclared
// marker-found resource is: written into the projection so the ordinary
// no-configuration-for-this-prior-state-entry rule makes the plan propose
// destroying it, and its record deleted by WriteBack once the destroy
// succeeds and the address drops out of the final state.
//
// A key this package cannot make sense of - [RecordKey]'s reverse,
// [RecordAddr], returning false - is skipped rather than treated as an
// error: a record store's namespace is not a promise that every key in it
// is one of this package's, and a stray or foreign key here is not this
// run's business to fail over.
func (b *builder) discoverOrphanedRecords(ctx context.Context, known map[string]bool) {
	if b.opts.RecordStore == nil {
		return
	}
	keys, err := b.opts.RecordStore.List(ctx, b.opts.RecordKeyPrefix)
	if err != nil {
		b.diags = b.diags.Append(tfdiags.Sourceless(tfdiags.Error, "Cannot list the record store",
			fmt.Sprintf("Listing the record store to find record-backed resources whose configuration block was removed failed: %s.", err),
		))
		return
	}
	for _, key := range keys {
		addr, ok := RecordAddr(b.opts.RecordKeyPrefix, key)
		if !ok {
			continue
		}
		if known[addr.String()] {
			continue
		}
		b.materializeRecord(ctx, addr, true)
	}
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

// notFoundDiagnosticSignals are the textual shapes an error-severity
// diagnostic out of ImportResourceState takes when a provider is actually
// answering "there is no such object", not failing to answer at all.
// Textual is all there can be: a diagnostic that has crossed the provider
// plugin protocol carries only Summary/Detail strings (tfdiags.Description),
// nothing structured about the underlying provider error survives the wire.
//
//   - "couldn't find resource" is the exact, hardcoded default message
//     terraform-plugin-sdk's retry.NotFoundError renders when a provider's
//     internal finder comes back empty and sets no more specific text. It
//     is a generic SDK convention used across the whole of
//     terraform-provider-aws wherever a resource's Read is built on a
//     "find the live object or report NotFoundError" finder, not a
//     type-specific string. aws_lambda_permission - whose import lookup
//     calls GetPolicy on the function, not the permission, and so 404s
//     with this shape the moment the function itself does not exist yet
//     either - is a confirmed instance (issue #297), not the only one this
//     is meant to cover.
//   - "ResourceNotFoundException" is AWS's own API error code, for a
//     provider that surfaces the untranslated API error instead of going
//     through the generic finder convention above.
var notFoundDiagnosticSignals = []string{
	"couldn't find resource",
	"ResourceNotFoundException",
}

// notFoundDiagnostics reports whether every error-severity diagnostic in
// diags matches one of [notFoundDiagnosticSignals], so an
// ImportResourceState response that came back with diagnostics can still be
// folded into statusAbsent the same as an empty ImportedResources list or a
// null read result - the "ordinary absence" this package's doc comments
// promise. detail is the first matching diagnostic's rendered text, for the
// warning that replaces the discarded error.
//
// A response mixing a not-found-shaped diagnostic with any other
// error-severity diagnostic - a credentials problem, a malformed request, a
// genuine provider failure - does not match: every error present has to be
// not-found-shaped, or this reports false and the caller's existing hard
// stop applies untouched. That also means a warning-only response (no
// errors at all) never reaches this function, since it is only consulted
// under importResp.Diagnostics.HasErrors().
func notFoundDiagnostics(diags tfdiags.Diagnostics) (bool, string) {
	sawError := false
	detail := ""
	for _, d := range diags {
		if d.Severity() != tfdiags.Error {
			continue
		}
		sawError = true
		desc := d.Description()
		text := strings.TrimSpace(desc.Summary + ": " + desc.Detail)
		matched := false
		for _, signal := range notFoundDiagnosticSignals {
			if strings.Contains(desc.Summary, signal) || strings.Contains(desc.Detail, signal) {
				matched = true
				break
			}
		}
		if !matched {
			return false, ""
		}
		if detail == "" {
			detail = text
		}
	}
	return sawError, detail
}

// configuredTagsSeed statically evaluates a taggable resource's own,
// AS-WRITTEN "tags" argument - before [stamp.Stamp] ever touches it - for
// GitHub issue #287 item 8: a stamped resource with a provider-level
// default_tags block showed a permanent, spurious "tags" in-place diff,
// re-adding the SAME keys on every single plan.
//
// The mechanism, verified directly against a live floci build rather than
// inferred from the plan output (see the issue and
// live/e2e/corpus-cncf-k8s-infra-aws-capa-ami/run.sh): CreateRole and
// GetRole both correctly return every tag, default_tags merged client-side
// by terraform-provider-aws before the request ever reaches the emulator.
// The gap is upstream of both. [providers.Configured.ImportResourceState]
// answers a bare identity with no configuration in hand, so a provider
// whose ReadResource distinguishes "explicitly declared" tags from
// "arrived through the provider's own default_tags" using only the prior
// state it is given sees an empty PriorState.tags and falls back to
// comparing raw tag VALUES against its default_tags config - which
// misclassifies any tag the configuration also declares explicitly, if it
// happens to duplicate a default_tags entry. The estate this was found
// against does exactly that: the caller's own `tags = var.tags` feeds BOTH
// the resource's tags argument AND, through the same variable, the
// provider block's default_tags map. A plain OpenTofu apply followed by a
// plain refresh never hits this: the state written at CREATE already
// carries the resource's own declared tags, so PriorState.tags is never
// empty going into a later ReadResource. choudoufu has no persisted state -
// every plan re-derives prior state through [importAndRead] - so it hits
// the ambiguity on every single plan, forever, for any stamped resource
// whose configuration redeclares a default_tags key. This is what re-seeds
// the missing signal.
//
// This function deliberately reads the SAME cfg [Build] was given, which is
// the configuration internal/command/live_plan.go's numbered pipeline
// resolves and projects at step 7, BEFORE step 8 (stamp.Stamp) rewrites a
// copy of it to add the marker entries - see that package's own doc
// comment, "The seam: configuration synthesis, before the plan runs":
// stamping mutates an in-memory copy, never a file, and never before a
// projection has already been built from the unstamped one. So the "tags"
// value this reads is exactly the author's own declaration, with no marker
// entries in it - and that is precisely the right value to seed with: the
// marker keys (tofu-estate, tofu-address, tofu-slot) never collide with a
// real default_tags entry, so the provider's own ambiguity never touches
// them either way. Only a key the configuration's OWN declaration happens
// to share with default_tags needs this signal, and that is exactly the
// key set this reads.
//
// Read from the schema - [markers.Taggable] plus a "tags_all" attribute,
// the AWS provider's transparent-tagging convention present on nearly
// every taggable AWS type - never from a type name list, so the fix
// reaches every type sharing that convention rather than the two this
// issue happened to name.
//
// A resource whose "tags" argument is not statically evaluable - it
// references another resource's computed attribute, or an each/count value
// this module-level evaluator has no repetition data for - is left alone:
// the second return is false and [importAndRead] falls back to
// ImportResourceState's own answer, exactly as it did before this existed.
// That is the same "refuse rather than guess" choice [PlanInstances] makes
// for the same reason.
//
// It decodes ONLY the "tags" attribute, deliberately never the resource's
// whole config the way [PlanInstances]'s planOne does: this estate's own
// aws_iam_role.this and aws_iam_openid_connect_provider.this resources have
// OTHER arguments referencing a data source
// (data.aws_iam_policy_document.this, data.aws_partition.current) that are
// never statically evaluable, and a whole-block decode against
// [configschema.Block.DecoderSpec] fails outright the moment any one
// attribute cannot resolve - hcl's Body.Content, which backs
// [hcl.Body.Content]-based decoding, errors on a body it was not given a
// schema entry for just as readily as it errors on an unresolvable
// reference. A single-attribute spec plus [hcldec.PartialDecode] asks only
// about "tags" and tolerates every sibling argument the schema does not
// mention, so a resource whose OTHER arguments are dynamic still gets its
// (fully static) tags seeded.
func configuredTagsSeed(ctx context.Context, eval *configs.StaticEvaluator, modPath addrs.Module, rc *configs.Resource, schema providers.Schema) (val cty.Value, ok bool) {
	if eval == nil || rc == nil || schema.Block == nil {
		return cty.NilVal, false
	}
	if !markers.Taggable(schema.Block) {
		return cty.NilVal, false
	}
	if attr, has := schema.Block.Attributes["tags_all"]; !has || attr == nil {
		// Not the default_tags-merging convention: nothing to disambiguate.
		return cty.NilVal, false
	}
	tagsAttr := schema.Block.Attributes["tags"]
	if tagsAttr == nil {
		return cty.NilVal, false
	}

	// A provider plugin is a subprocess doing arbitrary work on a value
	// this package built; [PlanInstances]'s planOne guards the same decode
	// call the same way, for the same reason.
	defer func() {
		if rec := recover(); rec != nil {
			val, ok = cty.NilVal, false
		}
	}()

	spec := hcldec.ObjectSpec{
		"tags": &hcldec.AttrSpec{Name: "tags", Type: tagsAttr.Type, Required: false},
	}

	ident := configs.StaticIdentifier{
		Module:    modPath,
		Subject:   rc.Addr().String(),
		DeclRange: rc.DeclRange,
	}
	refs, refDiags := lang.References(addrs.ParseRef, hcldec.Variables(rc.Config, spec))
	if refDiags.HasErrors() {
		return cty.NilVal, false
	}
	hclCtx, ctxDiags := eval.EvalContext(ctx, ident, refs)
	if ctxDiags.HasErrors() {
		return cty.NilVal, false
	}
	if hclCtx == nil {
		hclCtx = &hcl.EvalContext{}
	}

	configVal, _, valDiags := hcldec.PartialDecode(rc.Config, spec, hclCtx)
	if valDiags.HasErrors() || configVal == cty.NilVal || configVal.IsNull() {
		return cty.NilVal, false
	}
	if !configVal.Type().HasAttribute("tags") {
		return cty.NilVal, false
	}
	tagsVal := configVal.GetAttr("tags")
	if tagsVal.IsNull() || !tagsVal.IsWhollyKnown() {
		return cty.NilVal, false
	}
	return tagsVal, true
}

// withSeededTags returns a copy of v with its "tags" attribute replaced by
// seed, when v is an object with a "tags" attribute of the exact same type
// seed has. Any mismatch - v not an object, no "tags" attribute, a
// different type - returns v unchanged and false: this is a best-effort
// seed for [configuredTagsSeed]'s ambiguity, not a correctness requirement,
// and a provider whose ImportResourceState returns a differently-shaped
// stub than expected should be read exactly as it always was.
func withSeededTags(v cty.Value, seed cty.Value) (cty.Value, bool) {
	if v == cty.NilVal || v.IsNull() || !v.Type().IsObjectType() {
		return v, false
	}
	ty := v.Type()
	if !ty.HasAttribute("tags") || !seed.Type().Equals(ty.AttributeType("tags")) {
		return v, false
	}
	attrs := make(map[string]cty.Value, len(ty.AttributeTypes()))
	for name := range ty.AttributeTypes() {
		attrs[name] = v.GetAttr(name)
	}
	attrs["tags"] = seed
	return cty.ObjectVal(attrs), true
}

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
//
// tagsSeed and tagsSeedOK are [configuredTagsSeed]'s answer for this
// instance, applied to the stub object ImportResourceState returns, BEFORE
// ReadResource sees it - see the seeding step below for why this is the one
// place in the whole conversation it can go.
func importAndRead(ctx context.Context, provider providers.Interface, schema providers.Schema, typeName string, target providers.ImportTarget, importID string, tagsSeed cty.Value, tagsSeedOK bool) (*states.ResourceInstanceObject, materializeStatus, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	if !target.IsIdentityBased() && !target.IsIDBased() {
		diags = diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Empty import identity",
			fmt.Sprintf("Nothing was computed as the import identity for a %s: no identity object and no import ID. For a type identified by several attributes with no separator between them, the identity object is the only form there is (see internal/live/identity's IdentityObjectOnly), so an identity the provider's schema would not accept leaves nothing to import by - which is refused here rather than approximated with a string.", typeName),
		))
		return nil, statusFailed, diags
	}

	importResp := provider.ImportResourceState(ctx, providers.ImportResourceStateRequest{
		TypeName: typeName,
		Target:   target,
	})
	if importResp.Diagnostics.HasErrors() {
		if ok, detail := notFoundDiagnostics(importResp.Diagnostics); ok {
			// Some providers answer "there is no such object" as an error
			// diagnostic out of ImportResourceState rather than an empty
			// ImportedResources list - aws_lambda_permission is a confirmed
			// instance, whose import lookup calls GetPolicy on the
			// *function* and gets ResourceNotFoundException back when the
			// function itself does not exist yet either (issue #297). That
			// is still an ordinary absence, not a provider that could not
			// answer, so it takes the same path an empty list or a null
			// read result already takes below.
			diags = diags.Append(tfdiags.Sourceless(
				tfdiags.Warning,
				"Import reported absence as an error",
				fmt.Sprintf(
					"Looking up the %s with identity %q failed with what reads as a not-found response rather than a genuine error: %s. Treating it as an ordinary absence.",
					typeName, importID, detail,
				),
			))
			return nil, statusAbsent, diags
		}
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

	// GitHub issue #287 item 8. ImportResourceState commonly leaves "tags"
	// null or empty on the stub it returns - it has no configuration to
	// consult, only the identity it was given - and a provider whose
	// ReadResource treats PriorState.tags as the signal for "which raw tags
	// were explicitly declared, as opposed to arriving through the
	// provider's own default_tags" then has no signal at all: every raw tag
	// whose VALUE happens to match a default_tags entry reads as
	// default-derived, even one the configuration also declares explicitly.
	// A resource with real, persisted state never hits this - its state
	// already carries the tags the resource was created with, so an
	// ordinary refresh's PriorState.tags carries the same signal a live
	// create's response would. choudoufu has no such state: every plan
	// re-derives prior state through exactly this call, so a projection
	// that skipped this step would hit the ambiguity on every single plan,
	// permanently, for any stamped resource whose configuration happens to
	// redeclare a default_tags key - precisely GitHub issue #287's repro
	// shape. Seeding PriorState.tags with what the configuration actually
	// declares, when that is statically known, makes this call see what a
	// genuinely persisted state would have shown.
	if tagsSeedOK {
		if seeded, ok := withSeededTags(obj.Value, tagsSeed); ok {
			obj.Value = seeded
		}
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
// For an undeclared instance, [Options.UndeclaredProviders] is checked
// first, keyed by addr: an estate whose managed resources span more than
// one provider configuration (issue #69) attributes each undeclared
// instance to the specific provider configuration whose sweep found it,
// because that account and region is where the resource actually lives.
// [Options.UndeclaredProvider] is the fallback for whichever undeclared
// instances the map does not name (every one of them, for a single-provider
// caller, which is what keeps this byte-identical to before issue #69). The
// implied-provider rule below ("aws_vpc" means the module's "aws"
// provider), with no alias, is the last resort when neither says anything:
// an alias is a property of the resource block and the block is what is
// missing, so a deleted block whose provider this run never listed through
// at all is the one case nothing here can serve, and it reports rather than
// reads through the wrong account.
func (b *builder) providerFor(rc *configs.Resource, modPath addrs.Module, typeName string, addr addrs.AbsResourceInstance) (addrs.AbsProviderConfig, bool) {
	if rc != nil {
		if modCfg, ok := identity.ConfigForModule(b.cfg, addr.Module); ok && modCfg.Module != nil {
			return providerConfigAddr(modCfg, rc), true
		}
		// materialize's own identity.ConfigForModule(b.cfg, addr.Module)
		// lookup is what produced rc in the first place, so this branch is
		// unreachable in practice; it exists only so an internal
		// inconsistency degrades to the resource's own local address
		// anchored at root rather than failing the whole plan.
		return addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: rc.Provider, Alias: rc.ProviderConfigAddr().Alias}, true
	}
	if p, ok := b.opts.UndeclaredProviders[addr.String()]; ok && p.Provider.Type != "" {
		return p, true
	}
	if b.opts.UndeclaredProvider.Provider.Type != "" {
		return b.opts.UndeclaredProvider, true
	}
	modCfg, ok := identity.ConfigForModule(b.cfg, modPath.UnkeyedInstanceShim())
	if !ok || modCfg.Module == nil {
		return addrs.AbsProviderConfig{}, false
	}
	implied := modCfg.Module.ImpliedProviderForUnqualifiedType(impliedProviderName(typeName))
	if implied.Type == "" {
		return addrs.AbsProviderConfig{}, false
	}
	return addrs.AbsProviderConfig{Module: modPath, Provider: implied}, true
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
func (b *builder) dependencies(rc *configs.Resource, modPath addrs.Module, schema providers.Schema) []addrs.ConfigResource {
	key := modPath.String() + "\x00" + rc.Addr().String()
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
		cr := res.InModule(modPath)
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
// block uses: [providerscope.ResolveResource] walking every ancestor module
// call's `providers = {...}` mapping between modCfg (the static module the
// block itself is declared in) and the root, honouring an aliased mapping
// instead of ignoring it. GitHub issue #188; the resolution core is
// internal/live/providerscope, built and tested separately from this
// wiring.
func providerConfigAddr(modCfg *configs.Config, rc *configs.Resource) addrs.AbsProviderConfig {
	return providerscope.ResolveResource(modCfg, rc)
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
