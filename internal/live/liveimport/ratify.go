// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package liveimport

import (
	"context"
	"fmt"
	"sort"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// Providers supplies a configured provider instance for a provider
// configuration address - the same seam
// [github.com/intentius/choudoufu/internal/live/projection.Providers] and the
// command package's statelessProviders both implement, narrowed to the one
// method this package calls. Nothing here lists a resource type and nothing
// here evaluates a provider block a second time; state already gives every
// resource its identity.
type Providers interface {
	ConfiguredProvider(ctx context.Context, addr addrs.AbsProviderConfig) (providers.Interface, error)
}

// Status is the ratification verdict for one resource instance. Every value
// is one of the five the issue names; nothing in this package produces a
// sixth.
type Status string

const (
	// StatusVerified means this run read the identity the state recorded off
	// the live system and every attribute matched. Eligible for stamping.
	StatusVerified Status = "VERIFIED"

	// StatusMissing means the live system could not confirm the identity the
	// state recorded - a real deletion, a provider or schema this run could
	// not use, or a read call that failed; Detail says which. Never
	// stamped: there is nothing live to carry a marker.
	StatusMissing Status = "MISSING"

	// StatusDrifted means the live system answered, but at least one
	// attribute besides tags_all (the provider's own computed rollup)
	// differs from what the state recorded. Reported, not fatal: the
	// resource is still eligible for stamping, the same way a plan still
	// proposes reconciling drift on a resource whose ownership is already
	// settled.
	StatusDrifted Status = "DRIFTED"

	// StatusUnadmittedType means the resource type has no row in the
	// identity package's admission table, so this package has no knowledge
	// of how to read or write it at all. Never stamped.
	StatusUnadmittedType Status = "UNADMITTED_TYPE"

	// StatusUntaggable means the provider's schema for the type has no tags
	// argument of the shape live/MARKERS.md describes, so there is nowhere
	// on the live object to write a marker. Never stamped, and not a
	// problem in itself - see the stamp package's doc comment on why an
	// untaggable type is not a gap in an estate's records.
	StatusUntaggable Status = "UNTAGGABLE"
)

// Entry is one resource instance's ratification verdict, safe to hand to a
// view: nothing on it needs a live provider connection to render.
type Entry struct {
	Addr     addrs.AbsResourceInstance
	TypeName string
	Status   Status
	Detail   string

	// LiveID is the identity attribute value this run read the object by,
	// when one is known. Empty when nothing was attempted (StatusUnadmittedType)
	// or no identity attribute could be read.
	LiveID string

	// Drifted names the top-level attributes whose live value differs from
	// the state's recorded value, each rendered "name (state -> live)". Set
	// only when Status is StatusDrifted.
	Drifted []string
}

// eligible is what Approve needs for one resource beyond what its Entry
// already reports: the live connection and the freshly read object, carried
// forward from Ratify so Approve never reads the tfstate or the live
// system's identity a second time - only the write itself is a new call.
type eligible struct {
	provider providers.Interface
	schema   providers.Schema
	typeName string
	liveVal  cty.Value
	identity cty.Value
	private  []byte
}

// Request is one ratification pass.
type Request struct {
	// Estate is the estate this run would stamp, matching the tofu-estate
	// marker grammar. Required: unlike live-plan and live-mv, there is no
	// configuration here to derive it from if it is left empty.
	Estate string

	// State is the state to read, already parsed by the caller - through
	// [github.com/intentius/choudoufu/internal/states/statefile] - and never
	// touched again by this package. Every module's managed resources are
	// considered, root and child alike; see the package doc, "Scope (v1)".
	State *states.State

	// Providers supplies a configured provider per provider configuration
	// address, keyed exactly as [states.Resource.ProviderConfig] names it.
	Providers Providers

	// ResidueStore is GitHub issue #275's argument-level residue store,
	// opened for this estate exactly the way live-plan and a stateless apply
	// open theirs - nil when the configuration declares no record_store.
	// See [projection.RecordResidueForInstance]'s doc comment (issue #327)
	// for why Approve, not Ratify, is where this gets used: recording
	// residue is a write, and this package's whole contract is that Ratify
	// never writes anything.
	ResidueStore *projection.ResidueStore
}

// Ratification is one pass's read-only findings, plus what a later Approve
// call needs to act on them. Entries is safe to render on its own - the rest
// is unexported, so that "the report comes first" is a property of this
// type's shape and not only of the order a caller happens to call things in.
type Ratification struct {
	Estate  string
	Entries []Entry

	eligible     map[string]*eligible
	residueStore *projection.ResidueStore
}

// Ratify reads every managed resource instance in req.State - root module
// and child module alike - and verifies it against the live system through
// req.Providers, producing one Entry per instance. It writes nothing: every
// call it makes is GetProviderSchema or ReadResource, both read-only on the
// provider protocol.
func Ratify(ctx context.Context, req Request) (*Ratification, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	switch {
	case !discovery.ValidEstateName(req.Estate):
		return nil, diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Invalid estate name",
			fmt.Sprintf("A migration needs the estate's name, matching the tofu-estate marker grammar in live/MARKERS.md (a lowercase letter followed by lowercase letters, digits or hyphens, at most 128 characters). Got %q.", req.Estate),
		))
	case req.State == nil:
		return nil, diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"No state to ratify",
			"live-import needs a parsed tfstate to read, and none was given.",
		))
	case req.Providers == nil:
		return nil, diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"No provider access",
			"Verifying a resource against the live system needs a configured provider, and none was given.",
		))
	}

	rat := &Ratification{
		Estate:       req.Estate,
		eligible:     make(map[string]*eligible),
		residueStore: req.ResidueStore,
	}

	for _, mod := range sortedModules(req.State) {
		for _, res := range sortedResources(mod) {
			if res.Addr.Resource.Mode != addrs.ManagedResourceMode {
				continue
			}
			for _, key := range sortedInstanceKeys(res) {
				addr := res.Addr.Instance(key)
				entry, elig := ratifyOne(ctx, req, res, addr, res.Instances[key])
				rat.Entries = append(rat.Entries, entry)
				if elig != nil {
					rat.eligible[addr.String()] = elig
				}
			}
		}
	}

	return rat, diags
}

// ratifyOne is the whole verdict for one resource instance: admission, the
// schema, the state's own recorded object, and - past every gate that would
// make a live call meaningless - one ReadResource call.
func ratifyOne(ctx context.Context, req Request, res *states.Resource, addr addrs.AbsResourceInstance, inst *states.ResourceInstance) (Entry, *eligible) {
	typeName := res.Addr.Resource.Type
	entry := Entry{Addr: addr, TypeName: typeName}

	if _, admitted := identity.LookupType(typeName); !admitted && !admittedByProviderSchema(ctx, req, res, typeName) {
		entry.Status = StatusUnadmittedType
		entry.Detail = fmt.Sprintf(
			"There is no identity knowledge for resource type %q, so this run cannot read or verify it. See live/LIMITATIONS.md, \"unadmitted-type\", for the admitted set.",
			typeName)
		return entry, nil
	}

	if inst == nil || inst.Current == nil {
		entry.Status = StatusMissing
		entry.Detail = "The state has no current object for this instance - only a deposed one, or none at all - so there is nothing recorded to verify against the live system."
		return entry, nil
	}

	providerAddr := impliedProviderAddr(res)
	provider, err := req.Providers.ConfiguredProvider(ctx, providerAddr)
	if err != nil {
		entry.Status = StatusMissing
		entry.Detail = fmt.Sprintf("Provider %s could not be used, so this instance could not be verified: %s.", providerAddr, err)
		return entry, nil
	}

	schema, schemaErr := resourceSchema(ctx, provider, typeName)
	if schemaErr != nil {
		entry.Status = StatusMissing
		entry.Detail = schemaErr.Error()
		return entry, nil
	}

	if !taggable(schema.Block) {
		entry.Status = StatusUntaggable
		entry.Detail = fmt.Sprintf("%s has no tags argument in the provider's schema, so there is nowhere on it to carry an ownership marker.", typeName)
		return entry, nil
	}

	prior, decErr := inst.Current.Decode(schema.Block.ImpliedType())
	if decErr != nil {
		entry.Status = StatusMissing
		entry.Detail = fmt.Sprintf("The recorded state does not fit the provider's current schema for %s: %s.", typeName, decErr)
		return entry, nil
	}
	priorVal, _ := prior.Value.UnmarkDeep()
	entry.LiveID = liveIDFrom(typeName, priorVal)

	readResp := provider.ReadResource(ctx, providers.ReadResourceRequest{
		TypeName:      typeName,
		PriorState:    priorVal,
		Private:       prior.Private,
		ProviderMeta:  cty.NullVal(cty.DynamicPseudoType),
		PriorIdentity: prior.Identity,
	})
	if readResp.Diagnostics.HasErrors() {
		entry.Status = StatusMissing
		entry.Detail = fmt.Sprintf("The provider failed while reading this resource from the live system: %s.", readResp.Diagnostics.Err())
		return entry, nil
	}
	if readResp.NewState == cty.NilVal || readResp.NewState.IsNull() {
		entry.Status = StatusMissing
		entry.Detail = "The live system reports that this identity no longer exists."
		return entry, nil
	}

	if drifted := driftedAttrs(schema.Block, priorVal, readResp.NewState); len(drifted) > 0 {
		entry.Status = StatusDrifted
		entry.Drifted = drifted
		entry.Detail = fmt.Sprintf("The live object differs from the state's recorded attributes: %s. Verification does not fix drift; a live-plan after stamping will show it.", joinList(drifted))
	} else {
		entry.Status = StatusVerified
		entry.Detail = "The live object matches the state's recorded attributes."
	}

	return entry, &eligible{
		provider: provider,
		schema:   schema,
		typeName: typeName,
		liveVal:  readResp.NewState,
		identity: readResp.NewIdentity,
		private:  readResp.Private,
	}
}

// impliedProviderAddr is the provider configuration this run reaches a
// resource through: the default provider implied by its type ("aws" for
// every "aws_*" type), in the root module, keeping whatever alias the state
// recorded. This is deliberately unconditional on which module res itself
// lives in - a child-module resource inheriting its caller's default
// provider (the common case, and the only case a module block with no
// explicit `providers = {...}` map can produce) resolves exactly the same
// way a root resource does.
//
// It is deliberately not res.ProviderConfig verbatim. A tfstate written by
// an older, unrelated tool - real Terraform, most realistically, which is
// exactly who this package exists to migrate from - records a provider
// address under registry.terraform.io; this run's own plugin cache, and the
// working directory's own "init", know only registry.opentofu.org. Every
// other live-* command resolves a resource's provider from its configuration
// for the same reason, never from a recorded address. A future alias- or
// namespace-aware config lookup could refine this further - a module given
// an explicit `providers = {...}` map that rebinds an alias resolves wrong
// here, same as a root resource on a non-default provider always has - but
// v1 does not need one: the default provider address is what "choudoufu
// init" in this directory already prepared.
func impliedProviderAddr(res *states.Resource) addrs.AbsProviderConfig {
	return addrs.AbsProviderConfig{
		Module:   addrs.RootModule,
		Provider: addrs.ImpliedProviderForUnqualifiedType(res.Addr.Resource.ImpliedProvider()),
		Alias:    res.ProviderConfig.Alias,
	}
}

// admittedByProviderSchema is the schema-based admission fallback
// internal/live/lint's admitted() already applies at plan/lint time
// (identity.SynthesizeTypeIdentity): a type identity.DefaultTable does not
// cover, but whose identity the provider's own schema settles completely
// from a single required argument, the same way row-gen itself proposes a
// [client-named] row. Without this, live-import refused to even read a
// type that a plain live-plan over the identical configuration admits fine
// - aws_s3_bucket_acl among six sibling aws_s3_bucket_* types, found
// crossing terraform-aws-modules/terraform-aws-s3-bucket's "complete"
// example, none of which have a static table row because the runtime
// schema fallback already covers them everywhere else in this fork.
//
// Fetching the provider or its schema failing here is not itself reported:
// it just means the fallback does not apply, and the caller's ordinary "no
// identity knowledge" refusal stands exactly as it did before this existed.
func admittedByProviderSchema(ctx context.Context, req Request, res *states.Resource, typeName string) bool {
	provider, err := req.Providers.ConfiguredProvider(ctx, impliedProviderAddr(res))
	if err != nil {
		return false
	}
	resp := provider.GetProviderSchema(ctx)
	if resp.Diagnostics.HasErrors() {
		return false
	}
	_, ok := identity.SynthesizeTypeIdentity(typeName, resp.ResourceTypes, nil)
	return ok
}

func resourceSchema(ctx context.Context, provider providers.Interface, typeName string) (providers.Schema, error) {
	resp := provider.GetProviderSchema(ctx)
	if resp.Diagnostics.HasErrors() {
		return providers.Schema{}, fmt.Errorf("the provider's schema could not be read: %w", resp.Diagnostics.Err())
	}
	schema, ok := resp.ResourceTypes[typeName]
	if !ok || schema.Block == nil {
		return providers.Schema{}, fmt.Errorf("the provider offers no schema for resource type %q", typeName)
	}
	return schema, nil
}

// liveIDFrom is the identity of a decoded state object, following the
// identity table's IdentityAttrs for the type and falling back to "id" -
// the same lookup [github.com/intentius/choudoufu/internal/live/mv] uses.
func liveIDFrom(typeName string, obj cty.Value) string {
	if obj == cty.NilVal || obj.IsNull() || !obj.Type().IsObjectType() {
		return ""
	}
	attrs := []string{"id"}
	if ti, ok := identity.LookupType(typeName); ok && len(ti.IdentityAttrs) > 0 {
		attrs = ti.IdentityAttrs
	}
	for _, name := range attrs {
		if !obj.Type().HasAttribute(name) {
			continue
		}
		v, _ := obj.GetAttr(name).Unmark()
		if v.IsNull() || !v.IsKnown() || v.Type() != cty.String {
			continue
		}
		if s := v.AsString(); s != "" {
			return s
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// Deterministic walk order
// ---------------------------------------------------------------------------

func sortedModules(state *states.State) []*states.Module {
	mods := make([]*states.Module, 0, len(state.Modules))
	for _, m := range state.Modules {
		mods = append(mods, m)
	}
	sort.Slice(mods, func(i, j int) bool { return mods[i].Addr.String() < mods[j].Addr.String() })
	return mods
}

func sortedResources(mod *states.Module) []*states.Resource {
	out := make([]*states.Resource, 0, len(mod.Resources))
	for _, r := range mod.Resources {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Addr.String() < out[j].Addr.String() })
	return out
}

func sortedInstanceKeys(res *states.Resource) []addrs.InstanceKey {
	keys := make([]addrs.InstanceKey, 0, len(res.Instances))
	for k := range res.Instances {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		return res.Addr.Instance(keys[i]).String() < res.Addr.Instance(keys[j]).String()
	})
	return keys
}

func joinList(items []string) string {
	out := ""
	for i, s := range items {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}
