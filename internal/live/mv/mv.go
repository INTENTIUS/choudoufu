// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package mv

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/listclient"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/live/providerscope"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// Request is one rename.
type Request struct {
	// Estate is the estate that owns the resource, matching the tofu-estate
	// marker grammar. A rename never crosses an estate boundary: the live
	// resource must already carry this estate's tag, and one that carries
	// another estate's is refused rather than taken over.
	Estate string

	// Old is the address the live resource carries today.
	Old addrs.AbsResourceInstance

	// New is the address it should carry after this runs.
	New addrs.AbsResourceInstance

	// Config is the configuration as it stands now - normally already
	// renamed, since renaming the block is the operator's edit and this
	// operation makes the live system agree with it.
	Config *configs.Config

	// Resolutions is identity.Resolve's output over Config. It says which
	// instances are declared at all, and it supplies the import identity for
	// the types the provider cannot list.
	Resolutions []identity.Resolution

	// Providers supplies configured provider instances, the same seam the
	// projection builder uses.
	Providers projection.Providers

	// Region is the region a list call should go to. Empty leaves the list
	// configuration's region unset and the provider's own resolution stands.
	Region string

	// DryRun stops after everything has been found and checked, before
	// PlanResourceChange. Every read still happens; nothing is written.
	DryRun bool

	// AllowMissingConfig permits the destination address to be absent from
	// the configuration, for the operator who rewrites the marker before
	// renaming the block. The default refusal is the ordering that leaves no
	// window in which a marker names an address nothing declares.
	AllowMissingConfig bool
}

// Path is how the live resource was found.
type Path string

const (
	// PathList means the instance's identity is assigned by the provider, so
	// the type was listed and the marker was read off the listed objects.
	PathList Path = "LIST"

	// PathIdentity means the instance's identity is in configuration, so the
	// resource was materialized from it and the marker was read off the
	// object that came back.
	PathIdentity Path = "IDENTITY"
)

// Result is what one rename found and did.
type Result struct {
	// Estate is the estate the rename ran against.
	Estate string

	// Old and New are the addresses, as given.
	Old, New addrs.AbsResourceInstance

	// OldMarker and NewMarker are those addresses escaped per
	// live/MARKERS.md: the tag value that was matched and the tag value
	// that was written.
	OldMarker, NewMarker string

	// TypeName is the resource type both addresses name.
	TypeName string

	// LiveID is the live resource's import identity.
	LiveID string

	// DisplayName is the provider's label for it, when the list path
	// supplied one. Display only.
	DisplayName string

	// Anchor is the configuration address the resource was materialized
	// through: the new address normally, the old one under
	// [Request.AllowMissingConfig].
	Anchor addrs.AbsResourceInstance

	// Path is how the resource was found.
	Path Path

	// Swept is true when the whole resource type was enumerated, which is
	// what makes "nothing else claims the destination address" a complete
	// answer rather than a best effort.
	Swept bool

	// DryRun echoes the request: nothing below the marker read happened.
	DryRun bool

	// Written is true when ApplyResourceChange completed.
	Written bool

	// Verified is true when the object the provider returned from the apply
	// carries the new marker. A write can succeed without this: some
	// provider/API combinations do not serve tags back on a read (the
	// aws_iam_role gap the e2e notes call #5), and that is reported as a
	// warning rather than treated as a failure.
	Verified bool
}

// Move rewrites the tofu-address marker on the one live resource that carries
// the old address, and returns what it did.
//
// Error diagnostics mean nothing was written, with one exception that says so
// in its own message: a provider error during ApplyResourceChange leaves the
// live resource in whatever state the provider left it, and the operator is
// told to re-read it rather than assured either way.
func Move(ctx context.Context, req Request) (*Result, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	res := &Result{
		Estate: req.Estate,
		Old:    req.Old,
		New:    req.New,
		DryRun: req.DryRun,
	}

	switch {
	case !discovery.ValidEstateName(req.Estate):
		return res, diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Invalid estate name",
			fmt.Sprintf("A rename needs the estate's name, matching the tofu-estate marker grammar in live/MARKERS.md (a lowercase letter followed by letters, digits or hyphens, at most 128 characters). Got %q.", req.Estate),
		))
	case req.Config == nil || req.Config.Module == nil:
		return res, diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"No configuration to rename against",
			"A rename reads the configuration to find which resource block owns the destination address and which provider to reach the live resource through, and none was given.",
		))
	case req.Providers == nil:
		return res, diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"No provider access",
			"A rename reads the live resource and writes one tag back through a configured provider, and none was given.",
		))
	}

	if addrDiags := checkAddresses(req); addrDiags.HasErrors() {
		return res, diags.Append(addrDiags)
	}

	res.TypeName = req.Old.Resource.Resource.Type
	res.OldMarker = discovery.EscapeAddress(req.Old.String())
	res.NewMarker = discovery.EscapeAddress(req.New.String())

	if markerDiags := checkMarkers(res); markerDiags.HasErrors() {
		return res, diags.Append(markerDiags)
	}

	if _, admitted := identity.LookupType(res.TypeName); !admitted {
		return res, diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Resource type outside the live-markers subset",
			fmt.Sprintf(
				"There is no identity knowledge for resource type %q, so a live resource of that type cannot be found, read or rewritten. See live/LIMITATIONS.md, \"unadmitted-type\", for the admitted set.",
				res.TypeName),
		))
	}

	anchor, anchorDiags := anchorAddr(req)
	diags = diags.Append(anchorDiags)
	if anchorDiags.HasErrors() {
		return res, diags
	}
	res.Anchor = anchor

	var rc *configs.Resource
	var rcCfg *configs.Config
	if modCfg, ok := identity.ConfigForModule(req.Config, anchor.Module); ok && modCfg.Module != nil {
		rc = modCfg.Module.ManagedResources[anchor.Resource.Resource.String()]
		rcCfg = modCfg
	}
	if rc == nil {
		// anchorAddr only returns declared addresses, so this is a bug.
		return res, diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"No resource block for the rename's anchor address",
			fmt.Sprintf("%s is declared per identity resolution, but no resource block was found for it in the configuration. This is a bug.", anchor),
		))
	}
	providerAddr := providerscope.ResolveResource(rcCfg, rc)
	provider, err := req.Providers.ConfiguredProvider(ctx, providerAddr)
	if err != nil {
		return res, diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Provider unavailable",
			fmt.Sprintf("Renaming %s needs provider %s, which could not be used: %s.", req.Old, providerAddr, err),
		))
	}

	schema, schemaDiags := resourceSchema(ctx, provider, providerAddr, res.TypeName)
	diags = diags.Append(schemaDiags)
	if schemaDiags.HasErrors() {
		return res, diags
	}

	m := &mover{req: req, res: res, provider: provider, schema: schema}

	prior, findDiags := m.find(ctx)
	diags = diags.Append(findDiags)
	if findDiags.HasErrors() {
		return res, diags
	}

	if req.DryRun {
		return res, diags
	}

	diags = diags.Append(m.rewrite(ctx, prior))
	return res, diags
}

// mover carries one rename's inputs through the find and write halves.
type mover struct {
	req      Request
	res      *Result
	provider providers.Interface
	schema   providers.Schema
}

// ---------------------------------------------------------------------------
// Checks that need no provider
// ---------------------------------------------------------------------------

// checkAddresses rejects the pairs of addresses that describe no move: the
// same address twice, two different resource types, anything that is not a
// managed resource, and either address passing through a count-keyed module
// instance.
//
// A root address, a static-module address, and a for_each-keyed module
// address (59c, issue #59 phase 3) are all fine, and so is any mix of them:
// crossing a module boundary at all - flattening an estate into its root,
// moving a resource into a module another config tree declares, or renaming
// across two module instances - is an ordinary rename once both ends are
// legal addresses, because a marker records an address, not which side of a
// module call wrote it (see mv.go's package doc, "the migration path for
// estates that flattened to try v0"). Only a count-keyed step stays
// refused, because count modules stay refused outright (RuleChildModule;
// live/LIMITATIONS.md, "child-module").
func checkAddresses(req Request) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics

	oldRes := req.Old.Resource.Resource
	newRes := req.New.Resource.Resource

	switch {
	case req.Old.String() == req.New.String():
		return diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Identical source and destination addresses",
			fmt.Sprintf("%s is both the old and the new address, so there is nothing to rewrite.", req.Old),
		))
	case oldRes.Mode != addrs.ManagedResourceMode || newRes.Mode != addrs.ManagedResourceMode:
		return diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Unsupported address for a rename",
			fmt.Sprintf("%s and %s must both be managed resource instances. Data sources are read at plan time and own nothing, so they have no marker to move.", req.Old, req.New),
		))
	case oldRes.Type != newRes.Type:
		return diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Mismatched resource types in a rename",
			fmt.Sprintf(
				"%s is a %s and %s is a %s. A rename rewrites the marker on one live resource; it does not turn one kind of cloud object into another. See live/MARKERS.md, \"The rename rule\".",
				req.Old, oldRes.Type, req.New, newRes.Type),
		))
	case hasCountKeyedModuleStep(req.Old.Module) || hasCountKeyedModuleStep(req.New.Module):
		// Unlike the guard at the top of Move, this one is about the two
		// addresses on the command line rather than about the configuration,
		// so lint cannot have caught it and it stays a full explanation.
		return diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Count-keyed module instances are not available under live resource markers",
			fmt.Sprintf("%s or %s passes through a module instance carrying a count key, and count-expanded module blocks are refused permanently. See live/LIMITATIONS.md, \"child-module\".", req.Old, req.New),
		))
	}
	return diags
}

// hasCountKeyedModuleStep reports whether a module instance path carries a
// count-style (integer) instance key anywhere in it: a module expanded with
// count, which live resource markers refuse permanently (RuleChildModule;
// live/LIMITATIONS.md, "child-module") because count renumbers every
// address beneath it on every insertion or removal above the changed
// index, moving addresses out from under their markers. A string-keyed step
// - a for_each-expanded module, issue #59's 59c - is fine at any depth,
// exactly like an unkeyed one: its key does not shift under insertion or
// removal, so a marker naming it stays valid the same way a root or
// static-module address does. See mv.go's package doc, "the migration path
// for estates that flattened to try v0", for why crossing a module
// boundary at all - static, keyed, or a mix - is an ordinary rename once
// both ends are legal addresses.
func hasCountKeyedModuleStep(modInst addrs.ModuleInstance) bool {
	for _, step := range modInst {
		if _, isCount := step.InstanceKey.(addrs.IntKey); isCount {
			return true
		}
	}
	return false
}

// checkMarkers rejects addresses that cannot be carried as a tag value at
// all. An address too long to store is a lint-time problem rather than
// something a rename can work around, and one that does not escape to a
// well-formed marker would be written and then never read back.
func checkMarkers(res *Result) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics

	for _, m := range []struct {
		addr   addrs.AbsResourceInstance
		marker string
	}{{res.Old, res.OldMarker}, {res.New, res.NewMarker}} {
		if len([]rune(m.marker)) > discovery.MaxAddressLen {
			diags = diags.Append(tfdiags.Sourceless(
				tfdiags.Error,
				"Address too long to carry an ownership marker",
				fmt.Sprintf("The address %s escapes to %d characters, over the %d-character ceiling tofu-address and its continuation tags allow (live/MARKERS.md, \"tofu-address continuation tags\"), so no live resource can carry it as a marker.", m.addr, len([]rune(m.marker)), discovery.MaxAddressLen),
			))
			continue
		}
		if !discovery.ValidMarkerAddress(m.marker) {
			diags = diags.Append(tfdiags.Sourceless(
				tfdiags.Error,
				"Address with no well-formed marker form",
				fmt.Sprintf("The address %s escapes to %q, which is not a well-formed tofu-address value under live/MARKERS.md. A marker written from it could never be matched back to this address.", m.addr, m.marker),
			))
		}
	}
	return diags
}

// anchorAddr picks the configuration address the live resource is
// materialized through, and enforces the config-ordering rule.
//
// The destination address being declared is the default requirement: a marker
// naming an address nothing declares is an orphan, and producing one on
// purpose is not something this command does without being told to. With
// -allow-missing-config the old address stands in, which is the "rewrite the
// marker first, rename the block second" ordering; either way one of the two
// has to be in the configuration, because the resource block is what says
// which provider configuration reaches this resource.
func anchorAddr(req Request) (addrs.AbsResourceInstance, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	newDeclared := declared(req, req.New)
	oldDeclared := declared(req, req.Old)

	switch {
	case newDeclared:
		return req.New, diags
	case !req.AllowMissingConfig:
		detail := fmt.Sprintf(
			"%s is not declared in this configuration, so rewriting a live resource's marker to it would produce a resource owned by an address that does not exist. Rename the resource block first, or pass -allow-missing-config to rewrite the marker now and rename the block in the same change.",
			req.New)
		if !oldDeclared {
			detail += fmt.Sprintf(" Note that %s is not declared either.", req.Old)
		}
		return addrs.AbsResourceInstance{}, diags.Append(tfdiags.Sourceless(
			tfdiags.Error, "Destination address missing from the configuration", detail,
		))
	case oldDeclared:
		return req.Old, diags.Append(tfdiags.Sourceless(
			tfdiags.Warning,
			"Configuration still naming the old address",
			fmt.Sprintf(
				"-allow-missing-config was given and %s is not declared, so the live resource was read through %s. Until the resource block is renamed, a plan will report this resource as an orphan of this estate.",
				req.New, req.Old),
		))
	default:
		return addrs.AbsResourceInstance{}, diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Neither address in the configuration",
			fmt.Sprintf(
				"Neither %s nor %s is declared here. One of them has to be, to name the provider configuration the live resource is reached through.",
				req.Old, req.New),
		))
	}
}

// declared reports whether an instance address is one this configuration
// expands to, per the identity resolutions.
func declared(req Request, addr addrs.AbsResourceInstance) bool {
	want := addr.String()
	for _, r := range req.Resolutions {
		if r.Addr.String() == want {
			return true
		}
	}
	return false
}

// resolutionFor returns the identity resolution for one declared instance.
func resolutionFor(req Request, addr addrs.AbsResourceInstance) (identity.Resolution, bool) {
	want := addr.String()
	for _, r := range req.Resolutions {
		if r.Addr.String() == want {
			return r, true
		}
	}
	return identity.Resolution{}, false
}

func resourceSchema(ctx context.Context, provider providers.Interface, providerAddr addrs.AbsProviderConfig, typeName string) (providers.Schema, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	resp := provider.GetProviderSchema(ctx)
	if resp.Diagnostics.HasErrors() {
		return providers.Schema{}, diags.Append(resp.Diagnostics)
	}
	schema, ok := resp.ResourceTypes[typeName]
	if !ok || schema.Block == nil {
		return providers.Schema{}, diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Unsupported resource type for the provider",
			fmt.Sprintf("Provider %s has no schema for managed resource type %q, so nothing of that type can be read or rewritten.", providerAddr, typeName),
		))
	}
	return schema, diags
}

// ---------------------------------------------------------------------------
// Finding the live resource
// ---------------------------------------------------------------------------

// SummaryLocatedRenameUnsupported is the summary [mover.find] raises for a
// record-located resource (GitHub issue #270): every path below rewrites a
// marker, and such a resource has none.
//
// Exported so a test can name one string rather than two, the same reason
// internal/live/projection exports its own summaries.
const SummaryLocatedRenameUnsupported = "Renaming a record-located resource"

// find locates the live resource carrying the old marker and materializes it,
// returning the object that becomes the prior state of the write.
//
// Which path finds it is decided by the admission rule the roadmap states,
// strongest first, and not by what the provider happens to be able to list. An
// instance whose identity is in configuration is found through that identity,
// because the identity is the stronger claim: it says which cloud object this
// address means without trusting any tag. The marker path is for the
// instances that have no other answer - the ones identity resolution left as
// needs-discovery - which is exactly the set the marker exists for.
//
// The list is still used on the identity path when the provider offers it,
// for one thing only: answering whether anything else already claims the
// destination address.
func (m *mover) find(ctx context.Context) (*states.ResourceInstanceObject, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	resolution, ok := resolutionFor(m.req, m.res.Anchor)
	if !ok {
		// anchorAddr only returns declared addresses, so this is a bug.
		return nil, diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"No identity resolution for the resource being renamed",
			fmt.Sprintf("%s is declared but identity resolution produced nothing for it. This is a bug.", m.res.Anchor),
		))
	}

	if resolution.Class == identity.ClassRecordLocated {
		// GitHub issue #270. Both of this function's paths end in a marker
		// rewrite, and a record-located resource has no marker: what says
		// which object it is is a key in the estate's record store, keyed
		// by the OLD address. Renaming it means moving that key, which is
		// a different operation from anything below, and it is not built
		// yet.
		//
		// Refused here, up front, rather than left to fail further in. The
		// projection this function would otherwise build carries no
		// located store, so it would stop with the "Record-located
		// instance with no record store" internal-inconsistency wording -
		// true of the projection and misleading to an operator whose
		// configuration does declare one. A rename that got past either
		// would be worse: the store would still name the old address, the
		// next plan would read the new address unbound, and it would
		// propose creating a second object.
		return nil, diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			SummaryLocatedRenameUnsupported,
			fmt.Sprintf(
				"A %s carries no ownership marker, so what says which live object %s owns is a record in this estate's record store rather than a tag. Renaming it means moving that record from %s to %s, which live-mv does not do yet. Nothing was searched and nothing was written. Until it does: destroy the resource under its old address and let the new one create it, or move the store key by hand.",
				m.res.TypeName, m.res.Old, m.res.Old, m.res.New),
		))
	}

	schemas, schemaDiags := listclient.ListSchemas(ctx, m.provider)
	// A provider that cannot list at all is not an error here; it is the
	// identity path's ordinary condition. Errors from the call itself travel.
	diags = diags.Append(schemaDiags)
	if schemaDiags.HasErrors() {
		return nil, diags
	}
	ts, listable := schemas.Get(m.res.TypeName)

	if resolution.Class == identity.ClassNeedsDiscovery {
		m.res.Path = PathList
		if !listable {
			return nil, diags.Append(tfdiags.Sourceless(
				tfdiags.Error,
				"No marker search path for this resource type",
				fmt.Sprintf(
					"The identity of a %s is provider-assigned (%s), so the resource carrying %q can only be found by listing the type, and this provider cannot list it. Nothing was searched and nothing was written; this is not a report that no such resource exists. The provider needs list support for %s before live-mv can rename it.",
					m.res.TypeName, discoveryReason(resolution), m.res.OldMarker, m.res.TypeName),
			))
		}
		liveID, liveIdentity, listDiags := m.locateByList(ctx, ts)
		diags = diags.Append(listDiags)
		if listDiags.HasErrors() {
			return nil, diags
		}
		obj, matDiags := m.materialize(ctx, identity.Resolution{
			Addr:     m.res.Anchor,
			Class:    identity.ClassConcrete,
			ImportID: liveID,
			Identity: liveIdentity,
		})
		return obj, diags.Append(matDiags)
	}

	m.res.Path = PathIdentity
	obj, idDiags := m.locateByIdentity(ctx, resolution)
	diags = diags.Append(idDiags)
	if idDiags.HasErrors() {
		return nil, diags
	}
	if listable {
		diags = diags.Append(m.checkDestinationFree(ctx, ts))
		if diags.HasErrors() {
			return nil, diags
		}
	}
	return obj, diags
}

// listed is one live resource of the type, as the list protocol served it,
// reduced to what a rename compares: an identity to write to, a label to show,
// and the escaped address it currently claims.
type listed struct {
	liveID      string
	displayName string
	marker      string

	// identity is the identity object the provider attached to the list
	// result, so that the import that follows can ask for the resource the
	// way the provider names it rather than by one attribute of that name
	// flattened into a string. Null when the provider sent none.
	identity cty.Value
}

// sweep enumerates every live resource of the type and keeps the ones this
// estate owns, with their markers escaped for comparison.
//
// The list is unfiltered on purpose: see the package doc. Everything the
// provider can see of this type crosses the wire once, and the estate and
// address comparisons happen here, against escaped values, never by decoding
// a tag back into an address.
func (m *mover) sweep(ctx context.Context, ts listclient.TypeSchema) ([]listed, int, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	vals := make(map[string]cty.Value)
	if ts.Config != nil && m.req.Region != "" {
		if _, ok := ts.Config.Attributes["region"]; ok {
			vals["region"] = cty.StringVal(m.req.Region)
		}
	}
	config, cfgDiags := ts.BuildConfig(vals)
	diags = diags.Append(cfgDiags)
	if cfgDiags.HasErrors() {
		return nil, 0, diags
	}

	results, listDiags := listclient.List(ctx, m.provider, m.res.TypeName, config, true)
	diags = diags.Append(listDiags)
	if listDiags.HasErrors() {
		return nil, 0, diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Failed to list a resource type",
			fmt.Sprintf("The provider failed while listing %s, so the live resources carrying this estate's markers could not be looked for.", m.res.TypeName),
		))
	}
	m.res.Swept = true

	var mine []listed
	for _, r := range results {
		tags, taggable := tagsFromListed(r.Resource)
		if !taggable {
			continue
		}
		if tags[discovery.TagEstate] != m.req.Estate {
			continue
		}
		raw, corrupt := discovery.GatherAddress(tags)
		if corrupt {
			// A malformed marker (a gap in its continuation tags) is not a
			// claim on any address; it neither matches the address being
			// renamed away from nor collides with the one being renamed
			// onto, the same way an unparseable tofu-address already did
			// before continuation tags existed.
			continue
		}
		mine = append(mine, listed{
			liveID:      importIdentity(m.res.TypeName, r),
			displayName: r.DisplayName,
			marker:      discovery.EscapeAddress(raw),
			identity:    r.Identity,
		})
	}
	return mine, len(results), diags
}

// claimants returns the swept resources carrying the address declared,
// unescaped (addrs.AbsResourceInstance.String()). The comparison is
// [discovery.AddressMatches] rather than a bare equality, so a live
// resource still carrying a marker a prior run wrote before issue #178
// widened the for_each key grammar is still found - see that function's
// doc comment for which keys this can matter for (only ones containing
// "@") and why.
func claimants(mine []listed, declared string) []listed {
	var out []listed
	for _, l := range mine {
		if discovery.AddressMatches(l.marker, declared) {
			out = append(out, l)
		}
	}
	return out
}

// checkDestinationFree refuses a rename onto an address another live resource
// already carries. It is the identity path's half of the collision rule: the
// resource being renamed was found by its identity rather than by a sweep, so
// the sweep happens here purely to answer "is anything else already claiming
// where this is going".
func (m *mover) checkDestinationFree(ctx context.Context, ts listclient.TypeSchema) tfdiags.Diagnostics {
	mine, _, diags := m.sweep(ctx, ts)
	if diags.HasErrors() {
		return diags
	}
	return diags.Append(m.destinationDiags(claimants(mine, m.res.New.String())))
}

// destinationDiags is the refusal itself, shared by both paths.
func (m *mover) destinationDiags(claimNew []listed) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics

	var others []listed
	for _, l := range claimNew {
		if l.liveID != m.res.LiveID {
			others = append(others, l)
		}
	}
	if len(others) == 0 {
		return diags
	}
	return diags.Append(tfdiags.Sourceless(
		tfdiags.Error,
		"Destination address already claimed",
		fmt.Sprintf(
			"%s (%s) already carries the address %q; rewriting %s to the same value would make two live resources claim one address. Nothing was written. See live/MARKERS.md, \"Ownership semantics\".",
			strings.Join(liveIDs(others), ", "), m.res.TypeName, m.res.NewMarker, m.res.LiveID),
	))
}

// locateByList enumerates the type and picks the one resource carrying this
// estate's tag and the old address. It is the marker admission path, for the
// instances whose identity is nowhere in configuration.
func (m *mover) locateByList(ctx context.Context, ts listclient.TypeSchema) (string, cty.Value, tfdiags.Diagnostics) {
	mine, listed, diags := m.sweep(ctx, ts)
	if diags.HasErrors() {
		return "", cty.NilVal, diags
	}

	claimOld := claimants(mine, m.res.Old.String())
	claimNew := claimants(mine, m.res.New.String())

	switch len(claimOld) {
	case 1:
		// The one answer this whole function exists for.
	case 0:
		return "", cty.NilVal, diags.Append(notFoundDiag(m.res, listed, len(mine), len(claimNew) > 0))
	default:
		return "", cty.NilVal, diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Two live resources claiming one address",
			fmt.Sprintf(
				"%d live %s resources carry estate %q and address %q at once: %s. Retag or delete the wrong one before renaming either; see live/MARKERS.md, \"Ownership semantics\".",
				len(claimOld), m.res.TypeName, m.req.Estate, m.res.OldMarker, strings.Join(liveIDs(claimOld), ", ")),
		))
	}

	found := claimOld[0]
	if found.liveID == "" {
		return "", cty.NilVal, diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Listed resource with no identity",
			fmt.Sprintf("The live %s carrying the marker %q came back from the list call with no usable identity, so there is no import ID to read it or write it back with.", m.res.TypeName, m.res.OldMarker),
		))
	}

	m.res.LiveID = found.liveID
	m.res.DisplayName = found.displayName

	if destDiags := m.destinationDiags(claimNew); destDiags.HasErrors() {
		return "", cty.NilVal, diags.Append(destDiags)
	}
	return found.liveID, found.identity, diags
}

// notFoundDiag distinguishes the two shapes of "not found" a swept type can
// produce: nothing of this estate carries the address, and something already
// carries the new one - the second being what a rename that already ran looks
// like from the outside.
func notFoundDiag(res *Result, listed, inEstate int, newClaimed bool) tfdiags.Diagnostic {
	if newClaimed {
		return tfdiags.Sourceless(
			tfdiags.Error,
			"No live resource at the old address",
			fmt.Sprintf(
				"No live %s carries estate %q and address %q, but one already carries %q. This rename appears to have already run: there is nothing left to rewrite, and the resource is bound to the new address.",
				res.TypeName, res.Estate, res.OldMarker, res.NewMarker),
		)
	}
	return tfdiags.Sourceless(
		tfdiags.Error,
		"No live resource at the old address",
		fmt.Sprintf(
			"The provider listed %d %s, %d of which carry estate %q, and none of those carries the tofu-address value %q. Nothing was written; the type was enumerated, so a resource with that marker does not exist.",
			listed, res.TypeName, inEstate, res.Estate, res.OldMarker),
	)
}

// locateByIdentity is the client-named path: the identity comes from the
// configuration, so the resource is materialized from it and its markers are
// read off the object that comes back. The marker still has to say what it is
// supposed to say - finding the object is not the same as being allowed to
// rewrite it - so every way it can disagree is its own named refusal.
func (m *mover) locateByIdentity(ctx context.Context, resolution identity.Resolution) (*states.ResourceInstanceObject, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	obj, matDiags := m.materialize(ctx, resolution)
	diags = diags.Append(matDiags)
	if matDiags.HasErrors() {
		return nil, diags
	}

	tags, taggable := tagsFromObject(m.schema, obj.Value)
	if !taggable {
		return nil, diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Resource type with no tags",
			fmt.Sprintf("A %s has no tags argument, so it carries no ownership marker and there is nothing to rewrite. Its identity comes from its configuration, which means renaming the block is the whole rename.", m.res.TypeName),
		))
	}

	m.res.LiveID = resolution.ImportID
	if m.res.LiveID == "" {
		m.res.LiveID = liveIDFromObject(m.res.TypeName, obj.Value)
	}

	estate := tags[discovery.TagEstate]
	raw, corrupt := discovery.GatherAddress(tags)
	marker := discovery.EscapeAddress(raw)

	switch {
	case corrupt:
		return nil, diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Malformed ownership marker",
			fmt.Sprintf(
				"The live %s at %s carries a tofu-address marker whose continuation tags (tofu-address-2, tofu-address-3, ...) have a gap in them, so this run cannot tell what address it names. See live/MARKERS.md, \"tofu-address continuation tags\"; a human has to resolve this before it can be renamed.",
				m.res.TypeName, m.res.LiveID),
		))
	case discovery.AddressMatches(marker, m.res.Old.String()) && estate == m.req.Estate:
		// Found it.
		return obj, diags
	case estate != "" && estate != m.req.Estate:
		return nil, diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Live resource owned by another estate",
			fmt.Sprintf(
				"The live %s at %s carries tofu-estate = %q, and this rename is for estate %q. Moving a resource across estates is a transfer of ownership, not a rename; see live/MARKERS.md, \"Ownership semantics\".",
				m.res.TypeName, m.res.LiveID, estate, m.req.Estate),
		))
	case discovery.AddressMatches(marker, m.res.New.String()):
		return nil, diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"No live resource at the old address",
			fmt.Sprintf(
				"The live %s at %s already carries the address %q rather than %q. This rename appears to have already run: there is nothing left to rewrite.",
				m.res.TypeName, m.res.LiveID, m.res.NewMarker, m.res.OldMarker),
		))
	case marker == "":
		return nil, diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"No live resource at the old address",
			fmt.Sprintf(
				"The live %s at %s carries no tofu-address tag at all, so it does not carry %q. An unmarked resource is adopted by stamping its markers, not renamed. Nothing was written.",
				m.res.TypeName, m.res.LiveID, m.res.OldMarker),
		))
	default:
		return nil, diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"No live resource at the old address",
			fmt.Sprintf(
				"The live %s at %s carries the address %q, not %q. That resource is the one this configuration's own identity names, so it is the one a rename of %s is about. Nothing was written.",
				m.res.TypeName, m.res.LiveID, marker, m.res.OldMarker, m.res.Old),
		))
	}
}

// materialize reads the live object exactly as a projection does, through the
// projection builder itself: ImportResourceState then ReadResource. A rename
// writes the object the provider served back with one tag changed, so the
// prior state it plans against has to be that same object.
//
// A parent-derived identity is rendered from its parents' live IDs, so the
// whole resolution list goes in for that case; everything else materializes
// exactly one instance.
func (m *mover) materialize(ctx context.Context, resolution identity.Resolution) (*states.ResourceInstanceObject, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	list := []identity.Resolution{resolution}
	if resolution.Class == identity.ClassParentDerived {
		list = m.req.Resolutions
	}

	projRes, projDiags := projection.BuildFrom(ctx, m.req.Config, list, m.req.Providers)
	diags = diags.Append(projDiags)
	if projDiags.HasErrors() {
		return nil, diags
	}

	ri := projRes.State.ResourceInstance(resolution.Addr)
	if ri == nil || ri.Current == nil {
		detail := fmt.Sprintf("The live %s behind %s could not be read, so there is no object to rewrite a marker on.", m.res.TypeName, resolution.Addr)
		for _, om := range projRes.Omitted {
			if om.Addr.String() == resolution.Addr.String() {
				detail = om.Detail
				break
			}
		}
		return nil, diags.Append(tfdiags.Sourceless(
			tfdiags.Error, "The live resource could not be read", detail,
		))
	}

	obj, err := ri.Current.Decode(m.schema.Block.ImpliedType())
	if err != nil {
		return nil, diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Failed to decode the live resource",
			fmt.Sprintf("The object read for %s does not fit the provider's own schema for %s: %s.", resolution.Addr, m.res.TypeName, err),
		))
	}
	return obj, diags
}

// ---------------------------------------------------------------------------
// Small helpers over list results
// ---------------------------------------------------------------------------

// importIdentity reads the live import ID out of a list result, following the
// identity table's IdentityAttrs for the type and falling back to "id".
func importIdentity(typeName string, r listclient.Result) string {
	attrs := []string{"id"}
	if ti, ok := identity.LookupType(typeName); ok && len(ti.IdentityAttrs) > 0 {
		attrs = ti.IdentityAttrs
	}
	for _, attr := range attrs {
		if v, ok := r.IdentityAttr(attr); ok && v != "" {
			return v
		}
	}
	return ""
}

func liveIDs(ls []listed) []string {
	out := make([]string, 0, len(ls))
	for _, l := range ls {
		id := l.liveID
		if id == "" {
			id = "(no identity)"
		}
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// liveIDFromObject is the identity of a materialized object, for the identity
// path, where the import ID may be a composite rather than the live ID.
func liveIDFromObject(typeName string, obj cty.Value) string {
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

func discoveryReason(r identity.Resolution) string {
	reason := strings.TrimSuffix(r.Reason, ".")
	if reason == "" {
		reason = "its identity appears nowhere in configuration"
	}
	return reason
}
