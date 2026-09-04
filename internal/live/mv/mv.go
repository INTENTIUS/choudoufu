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
	"github.com/intentius/choudoufu/internal/live/cloudcontrol"
	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/listclient"
	"github.com/intentius/choudoufu/internal/live/moved"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/live/providerscope"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// Request is one rename.
type Request struct {
	// Estate is the estate that owns the resource after this runs, matching
	// the tofu-estate marker grammar. A rename never crosses an estate
	// boundary: the live resource must already carry this estate's tag, and
	// one that carries another estate's is refused rather than taken over.
	// The one exception is the cross-estate move [Request.FromEstate] names.
	Estate string

	// FromEstate, when set, turns the rename into a cross-estate move: the
	// live resource is found by FromEstate's tag and the old address, and
	// rewritten to carry Estate's tag and the new address, which may be the
	// same address. That is the transfer of ownership live/MARKERS.md
	// describes under "Splitting an estate" - a tag write - performed
	// through the same tags-only apply a rename makes, with the same
	// refusals: the destination configuration must declare the address,
	// nothing else in the destination estate may already claim it, and a
	// plan that would touch anything beyond tags is never applied. Empty
	// is an ordinary rename within Estate.
	//
	// What a cross-estate move does not carry is the record the source
	// estate's store holds for the resource: that store belongs to another
	// configuration this run cannot see. The destination's first apply
	// records the instance afresh, and the plan before it names any
	// attribute the record alone remembered.
	FromEstate string

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

	// Tagging is the Resource Groups Tagging API client a caller builds the
	// same way internal/command/live_plan.go does (nil when Cloud Control
	// fallback is off, or the run named no endpoint at all). sweep uses it
	// as issue #266's fallback, through [discovery.JoinMarkerFromTagging],
	// for a listed object whose own tags come back empty: some list
	// operations drop tags entirely (iam:ListRoles, iam:ListPolicies), and
	// without this a needs-discovery instance of such a type can never be
	// found by locateByList, no matter how correctly it is tagged. A nil
	// client degrades to the pre-#266 behavior, exactly as an ordinary
	// discovery pass degrades when it has none.
	Tagging *cloudcontrol.Client

	// RecordStore is the estate's record envelope store (GitHub issue #364),
	// opened the same way live-plan and live-import open theirs. It is what
	// [mover.find] consults, keyed by [Request.Old], before refusing a
	// provider-assigned type the provider cannot list: a migration writes
	// this same store a kind=identity record for EVERY stamped instance
	// (internal/live/liveimport/stamp.go's seedIdentityFor), which is
	// exactly the authoritative-identity-without-List-support case the
	// no-search-path refusal used to have no answer for. Nil is a
	// configuration with no record_store block, or a caller (today, only
	// this package's own unit tests) that has not wired one in; either way
	// the refusal behaves exactly as it did before this field existed.
	RecordStore *projection.RecordStore

	// ReadParallelism is how many of [mover.materialize]'s per-instance
	// provider round trips run at once - [projection.Options.ReadParallelism],
	// which live-plan and the live-block path already carry from
	// TOFU_LIVE_READ_PARALLELISM (internal/command/live_read_parallelism.go).
	// Zero, the value every caller that does not set it passes, is what
	// Options already reads as "unset" and answers with
	// [projection.DefaultReadParallelism]; so this package's own unit tests
	// and any other caller behave exactly as they did before the field
	// existed.
	//
	// GitHub issue #640. Issue #626 wired three of the tree's four
	// projection.Options and left this one, so a rename read at the engine's
	// ten however far down an operator had turned the variable. That matters
	// here and is not merely tidiness: materialize hands the WHOLE resolution
	// list to the projection builder for a parent-derived identity, so a
	// live-mv of such a resource runs the same estate-wide read pass a plan
	// runs, at the same width, against the same account - and an operator
	// reaches for live-mv during a migration, which is when that account is
	// least likely to have headroom. The single-instance case reads one
	// object and any bound is equally moot for it.
	//
	// Deliberately a plain field rather than an environment read inside this
	// package: internal/live is engine, and reading TOFU_LIVE_* here would
	// give one process two places to resolve the same setting. The command
	// resolves it once and passes it, exactly as [Request.Region] and
	// [Request.Tagging] arrive.
	ReadParallelism int
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
	// Estate is the estate the rename ran against - the destination, for a
	// cross-estate move.
	Estate string

	// FromEstate is the estate the resource was found under when this was a
	// cross-estate move, and empty for an ordinary rename.
	FromEstate string

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

	// Followers are the declared instances that move along with this one
	// without a marker write of their own - see [Follower]'s own doc
	// comment. Computed from the configuration's identity map alone, the
	// moment [Result.Anchor] is known, so it is populated even when the
	// rename that follows refuses: a follower is a fact about the
	// configuration, not an outcome of the write.
	Followers []Follower

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
		Estate:     req.Estate,
		FromEstate: req.FromEstate,
		Old:        req.Old,
		New:        req.New,
		DryRun:     req.DryRun,
	}

	switch {
	case !discovery.ValidEstateName(req.Estate):
		return res, diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Invalid estate name",
			fmt.Sprintf("A rename needs the estate's name, matching the tofu-estate marker grammar in live/MARKERS.md (a lowercase letter followed by letters, digits or hyphens, at most 128 characters). Got %q.", req.Estate),
		))
	case req.FromEstate != "" && !discovery.ValidEstateName(req.FromEstate):
		return res, diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Invalid source estate name",
			fmt.Sprintf("A cross-estate move names the estate the resource leaves, matching the tofu-estate marker grammar in live/MARKERS.md. Got %q.", req.FromEstate),
		))
	case req.FromEstate != "" && req.FromEstate == req.Estate:
		return res, diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Source and destination estates are the same",
			fmt.Sprintf("-from-estate names %q, which is also this configuration's own estate, so there is no boundary to cross. A rename within one estate takes no -from-estate.", req.FromEstate),
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
	res.Followers = followersOf(anchor, req.Resolutions)

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
	if diags.HasErrors() {
		return res, diags
	}

	return res, diags.Append(m.propagateModuleRename(ctx))
}

// mover carries one rename's inputs through the find and write halves.
type mover struct {
	req      Request
	res      *Result
	provider providers.Interface
	schema   providers.Schema
}

// sourceEstate is the estate the live resource is looked for under: the
// source of a cross-estate move, or the one estate an ordinary rename stays
// within. The write always carries req.Estate, the destination.
func (m *mover) sourceEstate() string {
	if m.req.FromEstate != "" {
		return m.req.FromEstate
	}
	return m.req.Estate
}

// ---------------------------------------------------------------------------
// Checks that need no provider
// ---------------------------------------------------------------------------

// checkAddresses rejects the pairs of addresses that describe no move: the
// same address twice (unless the move crosses an estate, where keeping the
// address is the ordinary case), two different resource types, and
// anything that is not a managed resource.
//
// A root address, a static-module address, a for_each-keyed module address
// (59c, issue #59 phase 3), and a count-keyed module address (issue #317)
// are all fine, and so is any mix of them: crossing a module boundary at
// all - flattening an estate into its root, moving a resource into a module
// another config tree declares, or renaming across two module instances -
// is an ordinary rename once both ends are legal addresses, because a
// marker records an address, not which side of a module call wrote it (see
// mv.go's package doc, "the migration path for estates that flattened to
// try v0"). A count-keyed module step used to stay refused here on the
// premise that count renumbers every address beneath it on scale-down;
// issue #195 retired that premise for a scalar module count (it never
// renumbers on scale-down; live/LIMITATIONS.md, "child-module"), and this
// function's own refusal was the one copy of it #195 left standing. Nothing
// here re-derives that a count-keyed step is safe on its own: by the time an
// address reaches this function, internal/command/live_mv.go has already
// run lint.CheckWith (RuleChildModule) against the configuration, which is
// the static/no-count.index-leak proof that admits the step in the first
// place.
func checkAddresses(req Request) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics

	oldRes := req.Old.Resource.Resource
	newRes := req.New.Resource.Resource

	switch {
	case req.Old.String() == req.New.String() && req.FromEstate == "":
		return diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Identical source and destination addresses",
			fmt.Sprintf("%s is both the old and the new address, so there is nothing to rewrite. A resource keeps its address only when it moves to another estate, which -from-estate names.", req.Old),
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
	}
	return diags
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
		return addrs.AbsResourceInstance{}, diags.Append(refuse(
			RefusalDestinationNotDeclared,
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

// Follower is a declared instance whose identity composes from the resource
// being renamed rather than carrying an ownership marker of its own - an
// aws_iam_role_policy or aws_iam_role_policy_attachment beside the role the
// carve-by-retag smoke moves (live/smoke/scenarios/carve-by-retag.sh, "The
// three children need no write at all; they follow their parent."). This
// rename never touches a follower: rewriting the parent's tag is the whole
// move, because the follower's import identity is rendered fresh from the
// parent's live ID on every read regardless of what address the parent
// carries. Follower exists so a caller drawing the move - a preview, or the
// receipt GitHub issue #791's -json flag prints - can show these instances
// moving alongside the parent without re-deriving the same
// [identity.ClassParentDerived] walk [declaredChildImportIDs] in
// internal/live/discovery already makes for a different purpose (skipping a
// declared child during a list read, rather than naming it for a reader).
type Follower struct {
	// Addr is the follower's own declared address.
	Addr addrs.AbsResourceInstance

	// TypeName is its resource type.
	TypeName string
}

// followersOf finds every instance in resolutions whose identity is a
// formula over anchor - [identity.ClassParentDerived] naming anchor among
// its [identity.Formula.Parents]. resolutions is the whole configuration's
// identity map ([Request.Resolutions]), not only the instance being
// renamed, which is what lets one pass find every child anywhere in the
// configuration rather than only the ones a caller already knew to look
// for.
//
// This is a fact about the configuration's identity graph, not about
// whether the live write anchor names succeeds - see [Result.Followers]'s
// own doc comment for why it is computed before find or rewrite ever run.
func followersOf(anchor addrs.AbsResourceInstance, resolutions []identity.Resolution) []Follower {
	want := anchor.String()
	var out []Follower
	for _, r := range resolutions {
		if r.Class != identity.ClassParentDerived || r.Formula == nil {
			continue
		}
		for _, p := range r.Formula.Parents {
			if p.String() == want {
				out = append(out, Follower{Addr: r.Addr, TypeName: r.Addr.Resource.Resource.Type})
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Addr.String() < out[j].Addr.String() })
	return out
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
			if obj, recDiags, tried := m.locateByRecord(ctx); tried {
				return obj, diags.Append(recDiags)
			}
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

// sweep enumerates every live resource of the type and keeps the ones the
// named estate owns, with their markers escaped for comparison. A rename
// sweeps its one estate; a cross-estate move sweeps the source to find the
// resource and the destination to check the address is free there.
//
// The list is unfiltered on purpose: see the package doc. Everything the
// provider can see of this type crosses the wire once, and the estate and
// address comparisons happen here, against escaped values, never by decoding
// a tag back into an address.
func (m *mover) sweep(ctx context.Context, ts listclient.TypeSchema, estate string) ([]listed, int, tfdiags.Diagnostics) {
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
		if tags[discovery.TagEstate] == "" {
			// Issue #266's exact gap, hit by a second code path. Some list
			// operations drop tags entirely - iam:ListRoles and
			// iam:ListPolicies among them, per
			// internal/live/discovery/bindtags.go's doc comment - so an
			// object that IS this estate's own still reads as untagged
			// here. Ask the same estate-filtered tag index an ordinary
			// discovery pass already consults before concluding this
			// object is not ours: one GetResources call, tags joined back
			// on by identifier, gated exactly as discovery.JoinMarkerFromTagging
			// documents (a type-matching marker, this estate's own
			// tofu-estate, and no more than one match). Only worth asking
			// when the object's own tags say nothing at all - one that
			// already answered honestly, even to say "not mine", needs no
			// second opinion.
			if joined, ok := discovery.JoinMarkerFromTagging(ctx, m.req.Tagging, estate, m.res.TypeName, importIdentity(m.res.TypeName, r)); ok {
				tags = joined
			}
		}
		if tags[discovery.TagEstate] != estate {
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
	mine, _, diags := m.sweep(ctx, ts, m.req.Estate)
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
	return diags.Append(refuse(
		RefusalNewAddressClaimed,
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
	mine, listed, diags := m.sweep(ctx, ts, m.sourceEstate())
	if diags.HasErrors() {
		return "", cty.NilVal, diags
	}

	claimOld := claimants(mine, m.res.Old.String())
	claimNew := claimants(mine, m.res.New.String())
	if m.req.FromEstate != "" {
		// The destination address has to be free in the DESTINATION
		// estate, which is not the estate just swept. One more sweep of the
		// same type, filtered to the estate the write will carry.
		dest, _, destDiags := m.sweep(ctx, ts, m.req.Estate)
		diags = diags.Append(destDiags)
		if destDiags.HasErrors() {
			return "", cty.NilVal, diags
		}
		claimNew = claimants(dest, m.res.New.String())
	}

	switch len(claimOld) {
	case 1:
		// The one answer this whole function exists for.
	case 0:
		return "", cty.NilVal, diags.Append(notFoundDiag(m.res, m.sourceEstate(), listed, len(mine), len(claimNew) > 0))
	default:
		return "", cty.NilVal, diags.Append(refuse(
			RefusalTwoAtOldAddress,
			tfdiags.Error,
			"Two live resources claiming one address",
			fmt.Sprintf(
				"%d live %s resources carry estate %q and address %q at once: %s. Retag or delete the wrong one before renaming either; see live/MARKERS.md, \"Ownership semantics\".",
				len(claimOld), m.res.TypeName, m.sourceEstate(), m.res.OldMarker, strings.Join(liveIDs(claimOld), ", ")),
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
func notFoundDiag(res *Result, estate string, listed, inEstate int, newClaimed bool) tfdiags.Diagnostic {
	if newClaimed {
		return refuse(
			RefusalNewAddressClaimed,
			tfdiags.Error,
			"No live resource at the old address",
			fmt.Sprintf(
				"No live %s carries estate %q and address %q, but one already carries %q. This rename appears to have already run: there is nothing left to rewrite, and the resource is bound to the new address.",
				res.TypeName, estate, res.OldMarker, res.NewMarker),
		)
	}
	return refuse(
		RefusalNothingAtOldAddress,
		tfdiags.Error,
		"No live resource at the old address",
		fmt.Sprintf(
			"The provider listed %d %s, %d of which carry estate %q, and none of those carries the tofu-address value %q. Nothing was written; the type was enumerated, so a resource with that marker does not exist.",
			listed, res.TypeName, inEstate, estate, res.OldMarker),
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
	case discovery.AddressMatches(marker, m.res.Old.String()) && estate == m.sourceEstate():
		// Found it.
		return obj, diags
	case m.req.FromEstate != "" && estate == m.req.Estate && discovery.AddressMatches(marker, m.res.New.String()):
		return nil, diags.Append(refuse(
			RefusalNewAddressClaimed,
			tfdiags.Error,
			"No live resource at the old address",
			fmt.Sprintf(
				"The live %s at %s already carries tofu-estate = %q and the address %q. This move appears to have already run: there is nothing left to rewrite.",
				m.res.TypeName, m.res.LiveID, estate, m.res.NewMarker),
		))
	case m.req.FromEstate != "" && estate != "" && estate != m.req.FromEstate:
		return nil, diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Live resource owned by another estate",
			fmt.Sprintf(
				"The live %s at %s carries tofu-estate = %q, and this move is from estate %q. Only the estate that owns a resource can move it out; name that estate in -from-estate, or adopt the resource instead. Nothing was written.",
				m.res.TypeName, m.res.LiveID, estate, m.req.FromEstate),
		))
	case estate != "" && estate != m.req.Estate:
		return nil, diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Live resource owned by another estate",
			fmt.Sprintf(
				"The live %s at %s carries tofu-estate = %q, and this rename is for estate %q. Moving a resource across estates is a transfer of ownership, not a rename; see live/MARKERS.md, \"Ownership semantics\".",
				m.res.TypeName, m.res.LiveID, estate, m.req.Estate),
		))
	case discovery.AddressMatches(marker, m.res.New.String()):
		return nil, diags.Append(refuse(
			RefusalNewAddressClaimed,
			tfdiags.Error,
			"No live resource at the old address",
			fmt.Sprintf(
				"The live %s at %s already carries the address %q rather than %q. This rename appears to have already run: there is nothing left to rewrite.",
				m.res.TypeName, m.res.LiveID, m.res.NewMarker, m.res.OldMarker),
		))
	case marker == "":
		return nil, diags.Append(refuse(
			RefusalNothingAtOldAddress,
			tfdiags.Error,
			"No live resource at the old address",
			fmt.Sprintf(
				"The live %s at %s carries no tofu-address tag at all, so it does not carry %q. An unmarked resource is adopted by stamping its markers, not renamed. Nothing was written.",
				m.res.TypeName, m.res.LiveID, m.res.OldMarker),
		))
	default:
		return nil, diags.Append(refuse(
			RefusalNothingAtOldAddress,
			tfdiags.Error,
			"No live resource at the old address",
			fmt.Sprintf(
				"The live %s at %s carries the address %q, not %q. That resource is the one this configuration's own identity names, so it is the one a rename of %s is about. Nothing was written.",
				m.res.TypeName, m.res.LiveID, marker, m.res.OldMarker, m.res.Old),
		))
	}
}

// locateByRecord is the record-primary fallback (the foundation-order
// ruling's "The order" item 1; GitHub issue #364) for a
// provider-assigned type this provider cannot list: before find refuses for
// lack of a marker search path, it asks the estate's record store for an
// identity recorded under the OLD address. A migration writes exactly this
// record for every stamped instance, taggable or not
// (internal/live/liveimport/stamp.go's seedIdentityFor), so a type in this
// position is not actually unfindable once an estate has migrated - only
// the marker sweep was.
//
// tried is false whenever nothing was consulted at all: no record store
// configured, or the store holds no identity for the old address. Both
// leave find's caller to raise its ordinary, unmodified "No marker search
// path" refusal - the boundary this fix must not blur, since a record that
// was never written is exactly the case the refusal still describes
// correctly.
//
// tried is true from the moment a record was found, and from there the
// record is a cache, never an authority on its own (HANDOFF.md, "a wrong
// marker outranks a missing one"): what it buys is an import identity to
// read the live object BY, through [mover.locateByIdentity] and, beneath
// it, [mover.materialize]'s ImportResourceState/ReadResource pair - never a
// List call. locateByIdentity is reused rather than duplicated so a
// record-verified identity is held to the exact verification a
// configuration-derived one already is: the object's own tofu-address must
// still name m.req.Old and its tofu-estate must still name this estate, or
// the rename refuses with locateByIdentity's own honest, distinct message
// (a stale record, a corrupt marker, a different owner) rather than
// authorizing a write on the record's say-so alone.
func (m *mover) locateByRecord(ctx context.Context) (*states.ResourceInstanceObject, tfdiags.Diagnostics, bool) {
	if m.req.FromEstate != "" {
		// The record store handed in is the destination estate's, keyed
		// under its own prefix. The source's record for the old address
		// lives in a store this run cannot see, so a cross-estate move has
		// no record fallback and find raises its ordinary refusal.
		return nil, nil, false
	}
	if m.req.RecordStore == nil {
		return nil, nil, false
	}

	rec, _, _, identityFound, err := m.req.RecordStore.GetIdentity(ctx, m.req.Old)
	if err != nil {
		var diags tfdiags.Diagnostics
		return nil, diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Recorded identity could not be read",
			fmt.Sprintf(
				"This estate's record store holds a key for %s, but it could not be read: %s. Nothing was searched further and nothing was written.",
				m.req.Old, err),
		)), true
	}
	if !identityFound {
		return nil, nil, false
	}

	m.res.Path = PathIdentity
	obj, diags := m.locateByIdentity(ctx, identity.Resolution{
		Addr:           m.res.Anchor,
		Class:          identity.ClassConcrete,
		ImportID:       rec.ImportID,
		IdentityValues: rec.Components,
	})
	return obj, diags, true
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

	// BuildWith, not the record-store-less BuildFrom: a parent-derived
	// resolution hands the WHOLE resolution list in above, and for any
	// estate whose configuration declares a record_store that list
	// ordinarily contains at least one identity.ClassRecordBacked sibling
	// (corpus-lambda-simple's random_pet.this and friends) that has
	// nothing to do with the instance actually being renamed. Without the
	// estate's RecordStore threaded through exactly as live-plan's own
	// build call does (internal/command/live_plan.go), the projection
	// builder hits that sibling and raises "Record-backed instance with no
	// record store" (build.go's materializeRecord) - a crash on the rename
	// of a plainly taggable resource, reachable on every estate that
	// combines a record_store with a live-mv rename on any resource.
	// m.req.RecordStore is nil for a configuration with no record_store
	// block (or a caller, today only this package's own unit tests, that
	// never wired one in), so this changes nothing for that boundary: a
	// nil field carried into Options.RecordStore is the exact input
	// BuildFrom already passed.
	//
	// ReadParallelism is issue #640's half of the same call. The list above
	// is one instance for most renames and the whole estate's resolutions for
	// a parent-derived one, so this read pass is the same read pass a plan
	// makes whenever it is wide enough for a bound to mean anything. See
	// [Request.ReadParallelism]: zero, from a caller that sets nothing, is
	// what Options already reads as the engine default, so nothing changes
	// for one.
	projRes, projDiags := projection.BuildWith(ctx, m.req.Config, list, m.req.Providers, projection.Options{
		RecordStore:     m.req.RecordStore,
		ReadParallelism: m.req.ReadParallelism,
	})
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
// Carrying the record store along with a module rename
// ---------------------------------------------------------------------------

// propagateModuleRename re-keys every record this estate's store holds
// under the module boundary req.Old -> req.New actually renamed, once
// [m.rewrite] has already made the live write for the one instance this
// call was asked to rename. It also, unconditionally and first, moves
// req.Old's own record to req.New regardless of whether a module boundary
// changed at all - GitHub issue #412: an ordinary same-module rename (no
// module step differs) gives [moduleRenameBoundary] nothing to find, so
// without that first, unconditional step, a bare resource rename never
// reached [RecordStore.MoveRecord] for its own key and left it stale
// forever. See the unconditional call at the top of this function's body
// for the detail; everything else below is the module-boundary sweep for
// everything else that lives under a renamed module.
//
// A live-mv rename is a single-resource operation: req.Old and req.New name
// one instance, and the caller may not even know what else lives under the
// module they share. But everything living under that module and findable
// only through this estate's record store - [identity.ClassRecordLocated]
// (GitHub issue #270) and an ordinary [identity.ClassNeedsDiscovery]
// type's own stamp-time identity record, [RecordStore.MoveRecord]'s doc
// comment names both - is invisible to the marker rewrite this package
// already does, and stays bound to the old address forever unless
// something else moves it. A `moved` block gets one: a plan-time alias
// consult (located.go's locatedIdentityWithAliases). live-mv writes no
// `moved` block, so this is the closest thing it has - a real re-key,
// right now, rather than teaching every future reader to walk an alias
// chain with no configuration behind it.
//
// Generic by construction: nothing here names a resource type. Any record
// under [RecordStore]'s namespace whose address falls under the renamed
// module prefix moves, including req.Old's own record if it has one -
// GitHub issue #357's day2_rename wall on corpus-rds-complete-postgres
// (aws_db_instance's own record-backed sibling, random_id.snapshot_identifier)
// is the estate that named this, but the rule reaches every type stock
// admits the same way. A sibling module's record, or anything outside the
// renamed prefix, is never a match and is never touched - the mutation
// check this package's own tests hold it to (a sibling module's record
// stays put).
//
// Deliberately conservative about what counts as "a module rename" at all:
// [moduleRenameBoundary] only recognizes exactly one module-instance step
// differing between req.Old.Module and req.New.Module, with every step
// before and after it identical. An ordinary resource rename within the
// same module (no step differs) or an address pair this function does not
// trust to generalize (more than one step differing at once - not
// something live-mv itself ever produces from a single Old/New pair, but
// nothing stops a caller from constructing one) does nothing here rather
// than guess: doing nothing is always safe, and moving the wrong record
// is exactly the wrong-marker hazard HANDOFF.md's safety rule exists to
// stop.
//
// One boundary can have more than one "old" prefix, though, when req.Old
// itself arrived at its current address through one or more `moved`-block
// hops before this live-mv call: a bare `moved` block is a plan-time alias
// only (this file's doc comment, and moved's own) and never physically
// rekeys the record store, so a record-located child renamed that way stays
// parked at whichever earlier address the block chain last named. GitHub
// issue #405's giantswarm/giantswarm-aws-account-prerequisites day2_rename
// wall (gauntlet:giantswarm-mv-children) is that shape: a plain `moved`
// block relocates module.crossplane -> .crossplane_renamed, then this
// package's own live-mv relocates .crossplane_renamed -> .crossplane_final
// with no second `moved` block at all, and a record-located sibling with no
// marker of its own (aws_iam_role_policy, aws_iam_role_policy_attachment)
// was still keyed at module.crossplane - one hop further back than the
// prefix this call's own req.Old/req.New pair names. [renameBoundaryOrigins]
// closes it: it walks req.Old's own `moved`-block alias chain
// ([moved.Origins], the exact primitive [gauntlet:sweep-moved-alias]'s
// recordOrphanReadSweep already consults on the read side) to find every
// earlier name this module boundary carried, and the sweep below matches a
// record against any of them, not just the immediate one.
//
// No cross-record transaction: each record moves through
// [RecordStore.MoveRecord]'s own single-record CAS, but there is nothing
// tying the whole set together. A crash partway through this loop leaves
// every record reached before the crash correctly at its new address and
// everything after it still at the old one - see MoveRecord's own doc
// comment for why that is safe rather than merely tolerable. day2_crash
// (live/GAUNTLET.md #10, planned) will need to recover the interrupted
// case; the story today is the same one MoveRecord names: re-run the same
// live-mv command, which finds nothing left to do for whatever already
// moved and finishes what did not.
func (m *mover) propagateModuleRename(ctx context.Context) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics

	if m.req.FromEstate != "" {
		// Every record this could move lives in the source estate's store,
		// which this run cannot reach; see [Request.FromEstate]. The
		// destination's first apply records the instance afresh.
		return diags
	}

	store := m.req.RecordStore
	if store == nil {
		return diags
	}

	// The renamed resource's OWN record, if it has one, has to follow to
	// req.New unconditionally - independent of whether this rename crossed
	// a module boundary at all. An ordinary same-module rename
	// (aws_sqs_queue.this -> .this_renamed, or an index change with no
	// module step differing) gives [moduleRenameBoundary] below nothing to
	// find, so the module-prefix sweep never runs for it; before this call,
	// req.Old's own kind=identity record (stamped by
	// internal/live/liveimport/stamp.go's seedIdentityFor for every stamped
	// instance, taggable or not - [projection.RecordStore.MoveRecord]'s own
	// doc comment names this exact case) was left stale at its pre-rename
	// key forever, even though the live marker and the returned Result both
	// correctly name req.New. GitHub issue #412 (found on corpus-
	// autoscaling-complete's, corpus-eks-basic's and corpus-ecs-fargate's
	// day2_rename/day2_replace stages): renaming the same instance a second
	// time would then look for its record under an address the store no
	// longer holds anything at.
	//
	// [projection.RecordStore.MoveRecord] is a no-op, with no error, when
	// req.Old has no record at all - the ordinary case for a markable
	// resource that carries no record-backed identity, so this call costs
	// nothing when there is nothing to move. When the module-boundary sweep
	// below also applies (a real module rename), it reaches req.Old's own
	// key again through the group it falls into; by then this call has
	// already moved it, so MoveRecord finds nothing left at req.Old and
	// no-ops on the second pass - the same idempotent shape a re-run after
	// a crash relies on (this function's own doc comment above).
	//
	// ownMoved records whether this call actually relocated something, for
	// the module-boundary sweep below: when it did, req.New's slot is
	// already correctly occupied by the freshest copy, and any further
	// stale duplicate the sweep finds mapping to that same destination
	// (GitHub issue #467, this call's own interaction with the moved-block-
	// origin chase [renameBoundaryOrigins] adds) is cleaned up rather than
	// moved into an address [staterecord.Store.PutIfVersion] will now,
	// correctly, refuse to overwrite.
	ownMoved, err := store.MoveRecord(ctx, m.req.Old, m.req.New)
	if err != nil {
		return diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"This resource's own record could not be moved",
			fmt.Sprintf(
				"Renaming %s to %s could not move its own record in this estate's record store: %s. The live write already completed; run the same live-mv command again to finish moving this estate's records once this is resolved - moving a record is safe to retry.",
				m.req.Old, m.req.New, err),
		))
	}

	oldPrefix, newPrefix, ok := moduleRenameBoundary(m.req.Old.Module, m.req.New.Module)
	if !ok {
		return diags
	}
	oldPrefixes := renameBoundaryOrigins(m.req.Config, m.req.Old, oldPrefix, newPrefix)

	keys, err := store.List(ctx)
	if err != nil {
		return diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Could not list this estate's records",
			fmt.Sprintf(
				"Renaming %s to %s moves module boundary %s to %s, and every record under it has to follow, but this estate's record store could not be listed: %s.",
				m.req.Old, m.req.New, oldPrefix, newPrefix, err),
		))
	}

	prefix := store.Prefix()

	// First pass: every stored key that falls under any of oldPrefixes,
	// with the destination it maps to and how many hops back its matching
	// prefix was (0 = oldPrefix itself, the closest; larger = a further
	// origin renameBoundaryOrigins chased). No writes yet.
	type match struct {
		addr    addrs.AbsResourceInstance
		newAddr addrs.AbsResourceInstance
		hop     int
	}
	groups := map[string][]match{}
	for _, key := range keys {
		addr, ok := projection.RecordAddr(prefix, key)
		if !ok {
			continue
		}
		rest, hop, under := moduleSuffixUnderAny(addr.Module, oldPrefixes)
		if !under {
			continue
		}
		newModule := make(addrs.ModuleInstance, 0, len(newPrefix)+len(rest))
		newModule = append(newModule, newPrefix...)
		newModule = append(newModule, rest...)
		newAddr := addrs.AbsResourceInstance{Module: newModule, Resource: addr.Resource}
		if newAddr.String() == addr.String() {
			continue
		}
		dest := newAddr.String()
		groups[dest] = append(groups[dest], match{addr: addr, newAddr: newAddr, hop: hop})
	}

	// Second pass, one destination at a time. More than one record in a
	// group only happens when reqOld arrived at its current address
	// through a `moved`-block hop this call also had to chase
	// (renameBoundaryOrigins' own doc comment): an ordinary apply along
	// the way refreshes every declared instance's own record at whichever
	// address was current when it ran, without deleting the copy an
	// earlier hop left behind - a bare `moved` block has no way to. The
	// closest copy (smallest hop) is the freshest, written by the most
	// recent apply along the chain, and is the one moved to the final
	// destination; any farther copy is a superseded duplicate of the exact
	// same instance and is deleted rather than left behind - see
	// [projection.RecordStore.DeleteRecord]'s own doc comment for why that
	// is safe. The move always runs before any delete for its own group,
	// so an interrupted run is still safe to retry: on a second pass,
	// MoveRecord finds nothing left at an already-moved winner's address
	// and no-ops, while a not-yet-deleted loser is still there to clean up.
	//
	// One destination is special: req.New's own. Whenever the unconditional
	// call above actually moved something (ownMoved), the freshest copy -
	// conceptually hop -1, closer than anything [renameBoundaryOrigins]
	// chases - already sits at req.New, written and CAS-confirmed before
	// [store.List] above ever ran; that is exactly why it can never appear
	// in this group itself (its stored key changed out from under it before
	// the sweep looked). So every entry the sweep still found for req.New's
	// own destination is, without exception, a stale duplicate the moved-
	// block-only hop (D1's own apply, in the day2_rename estate this was
	// found on) left behind - not a copy still waiting to move. Cleaning
	// each one up directly, with no MoveRecord attempt, is what GitHub
	// issue #467 needed: the ordinary winner/MoveRecord path below would
	// try to write into req.New's slot a second time and fail with exactly
	// the version conflict [projection.RecordStore.MoveRecord]'s own
	// PutIfVersion(..., "") call is supposed to raise for a genuinely
	// contested key.
	newAddrKey := m.req.New.String()
	for dest, group := range groups {
		if dest == newAddrKey && ownMoved {
			for _, mt := range group {
				_, version, keyExists, _, gErr := store.GetIdentity(ctx, mt.addr)
				if gErr != nil {
					return diags.Append(tfdiags.Sourceless(
						tfdiags.Error,
						"A superseded record could not be read before cleanup",
						fmt.Sprintf(
							"Renaming %s to %s moved module boundary %s to %s. %s is a stale duplicate of %s, already carried forward by this rename's own unconditional move, and could not be read to clean it up: %s.",
							m.req.Old, m.req.New, oldPrefix, newPrefix, mt.addr, m.req.New, gErr),
					))
				}
				if !keyExists {
					continue
				}
				if dErr := store.DeleteRecord(ctx, mt.addr, version); dErr != nil {
					return diags.Append(tfdiags.Sourceless(
						tfdiags.Error,
						"A superseded record could not be cleaned up",
						fmt.Sprintf(
							"Renaming %s to %s moved module boundary %s to %s. %s is a stale duplicate of %s, already carried forward by this rename's own unconditional move, and could not be removed: %s. Nothing is lost - %s already holds the correct record - but %s should be cleaned up by hand or by rerunning the same live-mv command.",
							m.req.Old, m.req.New, oldPrefix, newPrefix, mt.addr, m.req.New, dErr, m.req.New, mt.addr),
					))
				}
			}
			continue
		}

		winner := group[0]
		for _, mt := range group[1:] {
			if mt.hop < winner.hop {
				winner = mt
			}
		}

		if _, mErr := store.MoveRecord(ctx, winner.addr, winner.newAddr); mErr != nil {
			return diags.Append(tfdiags.Sourceless(
				tfdiags.Error,
				"A record could not be moved with its module",
				fmt.Sprintf(
					"Renaming %s to %s moved module boundary %s to %s. %s's own record has to move to %s to follow, and it could not: %s. The live write already completed; run the same live-mv command again to finish moving this estate's records once this is resolved - moving a record is safe to retry.",
					m.req.Old, m.req.New, oldPrefix, newPrefix, winner.addr, winner.newAddr, mErr),
			))
		}

		for _, mt := range group {
			if mt.addr.String() == winner.addr.String() {
				continue
			}
			_, version, keyExists, _, gErr := store.GetIdentity(ctx, mt.addr)
			if gErr != nil {
				return diags.Append(tfdiags.Sourceless(
					tfdiags.Error,
					"A superseded record could not be read before cleanup",
					fmt.Sprintf(
						"Renaming %s to %s moved module boundary %s to %s. %s is a stale duplicate of %s, already carried forward to %s, and could not be read to clean it up: %s.",
						m.req.Old, m.req.New, oldPrefix, newPrefix, mt.addr, winner.addr, winner.newAddr, gErr),
				))
			}
			if !keyExists {
				continue
			}
			if dErr := store.DeleteRecord(ctx, mt.addr, version); dErr != nil {
				return diags.Append(tfdiags.Sourceless(
					tfdiags.Error,
					"A superseded record could not be cleaned up",
					fmt.Sprintf(
						"Renaming %s to %s moved module boundary %s to %s. %s is a stale duplicate of %s, already carried forward to %s, and could not be removed: %s. The fresher copy's move already completed; nothing is lost, but %s should be cleaned up by hand or by rerunning the same live-mv command.",
						m.req.Old, m.req.New, oldPrefix, newPrefix, mt.addr, winner.addr, winner.newAddr, dErr, mt.addr),
				))
			}
		}
	}
	return diags
}

// renameBoundaryOrigins is [gauntlet:giantswarm-mv-children]'s own fix: a
// live-mv call only ever names ONE boundary - reqOld's module to the sweep's
// own newPrefix - but reqOld may itself have arrived there through one or
// more earlier `moved`-block-only hops on the exact same module boundary,
// and a bare `moved` block never physically moves anything in the record
// store (this package's own doc comment, and [projection.RecordStore.
// MoveRecord]'s: only a real rewrite - a prior live-mv call, or this one -
// does that). A record-located child with no marker of its own then stays
// parked at whichever pre-live-mv address the `moved` chain last left it,
// invisible to a single-prefix sweep of oldPrefix alone: the corpus-
// giantswarm-crossplane day2_rename wall (moved block module.crossplane ->
// .crossplane_renamed, THEN live-mv .crossplane_renamed -> .crossplane_final
// with no moved block for that second hop at all) is exactly this - the
// child's record was still keyed at module.crossplane, one hop further back
// than oldPrefix (module.crossplane_renamed).
//
// The bridge is reqOld's own alias chain: [moved.Origins] over the
// configuration's [moved.Honoured] statements, asked about reqOld itself
// (the one resource this live-mv call was explicitly given), names every
// earlier address reqOld could still be marked as. Since the `moved` block
// that produced each origin names a MODULE CALL, not one resource, it
// applies uniformly to every resource beneath it - a record-located
// sibling's own history is the same as the anchor's. Each origin's module,
// re-diffed against reqOld's module the same conservative way
// [moduleRenameBoundary] already diffs oldPrefix, contributes one more
// prefix candidate the sweep matches records against, all landing on the
// SAME newPrefix - so a child still sitting two hops back is carried the
// whole way to the live-mv destination in one call, the same as one hop
// back. An origin that does not re-diff cleanly (a different length, more
// than one differing step, or a differing step at a different position than
// the one this rename is actually about) is dropped rather than guessed at,
// the same discipline [moduleRenameBoundary] itself applies: doing nothing
// with an ambiguous shape is always safe, and moving the wrong record is the
// wrong-marker hazard HANDOFF.md's safety rule exists to stop.
//
// Generic by construction, same as [mover.propagateModuleRename] itself:
// nothing here names a resource type, only reqOld's own address and the
// configuration's `moved` blocks.
func renameBoundaryOrigins(cfg *configs.Config, reqOld addrs.AbsResourceInstance, oldPrefix, newPrefix addrs.ModuleInstance) []addrs.ModuleInstance {
	out := []addrs.ModuleInstance{oldPrefix}

	stmts := moved.Honoured(cfg)
	if len(stmts) == 0 {
		return out
	}

	for _, origin := range moved.Origins(stmts, reqOld) {
		furtherOld, matchOld, ok := moduleRenameBoundary(origin.Module, reqOld.Module)
		if !ok || !matchOld.Equal(oldPrefix) {
			// Not the same renamed step this call is about - conservative
			// skip, per this function's own doc comment.
			continue
		}
		dup := false
		for _, p := range out {
			if p.Equal(furtherOld) {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, furtherOld)
		}
	}
	return out
}

// moduleSuffixUnderAny is [moduleSuffixUnder] over a set of candidate
// prefixes, in order: prefixes is [renameBoundaryOrigins]'s own output,
// closest (oldPrefix itself) first and progressively further chased
// `moved`-block origins after it, so the first match found is also the
// closest one - hop is its index, which [mover.propagateModuleRename] uses
// to pick the freshest of several records mapping to the same destination.
// [renameBoundaryOrigins] is the only caller that ever supplies more than
// one prefix; every other caller of [moduleSuffixUnder] keeps using it
// directly for the single-prefix case.
func moduleSuffixUnderAny(module addrs.ModuleInstance, prefixes []addrs.ModuleInstance) (rest addrs.ModuleInstance, hop int, ok bool) {
	for i, prefix := range prefixes {
		if r, under := moduleSuffixUnder(module, prefix); under {
			return r, i, true
		}
	}
	return nil, 0, false
}

// moduleRenameBoundary reports the module-instance prefix a rename from old
// to new actually moved: the leading steps up to and including the one step
// that differs, on both sides, when old and new are the same length and
// differ at EXACTLY one step. ok is false whenever that shape does not
// hold - same length but no step differs (an ordinary rename within one
// module, nothing to propagate), different lengths, or more than one step
// differing - and [mover.propagateModuleRename] does nothing rather than
// guess which steps are "the" rename in any of those cases.
func moduleRenameBoundary(old, new addrs.ModuleInstance) (oldPrefix, newPrefix addrs.ModuleInstance, ok bool) {
	if len(old) != len(new) {
		return nil, nil, false
	}
	diffAt := -1
	for i := range old {
		if old[i] != new[i] {
			if diffAt != -1 {
				return nil, nil, false
			}
			diffAt = i
		}
	}
	if diffAt == -1 {
		return nil, nil, false
	}
	return old[:diffAt+1], new[:diffAt+1], true
}

// moduleSuffixUnder reports whether module falls under prefix - its leading
// steps equal prefix exactly - and the steps beyond prefix when it does.
func moduleSuffixUnder(module, prefix addrs.ModuleInstance) (rest addrs.ModuleInstance, ok bool) {
	if len(module) < len(prefix) {
		return nil, false
	}
	for i := range prefix {
		if module[i] != prefix[i] {
			return nil, false
		}
	}
	return module[len(prefix):], true
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
