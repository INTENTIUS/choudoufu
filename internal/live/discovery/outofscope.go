// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"fmt"
	"sort"
	"strings"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/providerscope"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// This file is GitHub issue #906.
//
// # What was wrong
//
// internal/command/live_plan.go runs one [Discover] pass per distinct
// provider configuration and [Merge] combines them. A pass lists only what
// its own configuration reaches, and [Request.ScopeProvider] keeps it from
// BINDING an address another configuration declares - while [declared.all],
// which answers "is this address declared at all", is deliberately built
// from every resolution with no scope filter at all. That asymmetry is not
// an oversight: an account-global list operation (aws_s3_bucket, IAM,
// Route53) hands every pass objects that belong, in configuration, to a
// different one, and a pass needs decl.all to tell "somebody else's declared
// resource" apart from "an orphan to remove".
//
// So all three sighting classifiers ended a declared-but-out-of-scope
// address at the same `continue`, and the live object went into no section
// of the result. Right for the case decl.all exists for, and wrong for one
// it cannot tell apart on its own. Move a resource block from
// `provider = aws.west` to `provider = aws.east` and the live object stays
// in us-west-2 wearing this estate's marker for an address that now belongs
// to a configuration looking at us-east-1. The west pass sights it and drops
// it, the east pass lists us-east-1 and finds nothing, and the plan proposes
// a create over the top - printing "Marker discovery will find it", which for
// that instance can never come true, because discovery for it now runs
// through aws.east and lists a region the object is not in.
//
// # What tells the two apart
//
// No single pass can: "declared elsewhere and sighted here" is the shape of
// an ordinary account-global sighting as much as it is the shape of a
// stranded object. [Merge] is the first point with every pass in view, so it
// is where they are separated, and the discriminator names no resource type
// and no region:
//
//	an address whose marked live object was sighted by a provider
//	configuration that does not declare it, and by no pass that does, is an
//	object its own configuration cannot reach.
//
// The account-global sighting fails that test, because the pass that
// declares the address sights the very same object in scope and binds or
// vouches it. The moved block passes it, because the region its address now
// points at holds nothing to sight.
//
// # Why this refuses rather than proposing the destroy
//
// A region change is a replace, and half a replace is what the plan already
// proposes. Planning the other half - a destroy of the old object - is not
// available at this address: a resource address has exactly one provider
// configuration in the plan graph, taken from its own block, and the destroy
// would have to run through the configuration the block no longer names.
// Nothing short of inventing a second address for one resource block makes
// that expressible, and a defect fix is not where an address is invented.
//
// What is left is to proceed or to refuse, and HANDOFF's safety rule decides
// it: proceeding creates a second live resource carrying this estate's marker
// for one address, which is exactly the state live/MARKERS.md's "Ownership
// semantics" forbids and exactly the state
// [crossProviderOrphanCollisions] already refuses a plan over once both
// objects exist. Refusing here is that same refusal one step earlier, before
// the run itself manufactures the collision, and it is loud and reversible
// where the collision it prevents is neither: after the apply, no pass can
// bind the old object and no plan proposes anything for it, so the estate is
// billed for it until a human finds it by hand.

// DeclaredSighting is one live object a pass listed that carries this
// estate's ownership marker for an address the configuration declares -
// recorded whether or not the resource block that declares that address
// belongs to this pass's own provider configuration.
//
// It is the per-pass evidence [strandedAcrossProviderConfigs] reads, and
// nothing else consumes it. A pass files a sighting for every declared
// address it sees, in scope and out, because the whole question is which
// passes saw an address's object and which did not - a list of only the
// out-of-scope half would say nothing about whether the address's own
// configuration also found it.
type DeclaredSighting struct {
	// Addr is the declared instance whose marker this object carries.
	Addr addrs.AbsResourceInstance

	// TypeName is the resource type the sighting was classified under -
	// [sweepBindType]'s corrected type where a companion pair applies, the
	// listed type otherwise.
	TypeName string

	// ImportID is the live identity the list served, empty where it served
	// none.
	ImportID string

	// Marker is the escaped tofu-address value read off the object, which
	// may be a legacy or moved-block spelling of Addr rather than Addr's
	// own current escaping.
	Marker string

	// Provider is the provider configuration Addr's own resource block
	// uses, resolved through every ancestor module call's
	// `providers = {...}` mapping exactly as [inScope] resolves it.
	Provider addrs.AbsProviderConfig

	// InScope is whether Provider is the configuration this pass listed
	// through. An unscoped pass - the single-provider path's zero
	// [Request.ScopeProvider] - owns every block, so it is always true
	// there, which is what keeps a single-provider estate unable to
	// produce a stranded finding at all.
	InScope bool
}

// noteDeclaredSighting files one sighting of a live object whose marker
// names an address the configuration declares.
//
// It is called by each of the three sighting classifiers immediately before
// the two branches that consume such an object - [declared.entryFor]'s
// claimant and [declared.declares]' displacement check - so that both are
// covered by one call rather than by a copy in each. Membership in
// [declared.all] is precisely the condition those two branches share, which
// is why this is the one gate here.
//
// Anything it cannot resolve is filed as nothing at all: an ambiguous marker
// value names no single address, and an address whose block cannot be found
// has no provider configuration to compare. Both leave today's behaviour
// exactly as it was, which is the safe direction, since a missing sighting
// can only ever make [strandedAcrossProviderConfigs] quieter.
func noteDeclaredSighting(req Request, decl *declared, res *Result, typeName, escaped, importID string) {
	if req.ScopeProvider.Provider.Type == "" {
		// An unscoped pass owns every block ([inScope]), so every sighting
		// it could file would be in scope and nothing across passes could
		// ever disagree with it. Returning here keeps the single-provider
		// path free of the resolution below, which is not merely a cost:
		// [providerscope.ResolveResource] reads the module's own provider
		// requirements, and the single-provider callers include tests and
		// tools whose synthesized configurations do not carry them.
		return
	}
	da := decl.all[typeName][escaped]
	if da == nil || da.ambiguous {
		return
	}
	addr := da.res.Addr
	block, modCfg, ok := declaringBlock(req, addr)
	if !ok {
		return
	}
	provider := providerscope.ResolveResource(modCfg, block)
	res.DeclaredSightings = append(res.DeclaredSightings, DeclaredSighting{
		Addr:     addr,
		TypeName: typeName,
		ImportID: importID,
		Marker:   escaped,
		Provider: provider,
		InScope:  req.ScopeProvider.Provider.Type == "" || provider.String() == req.ScopeProvider.String(),
	})
}

// declaringBlock finds the resource block that declares addr, together with
// the static module node it is declared in - the pair
// [providerscope.ResolveResource] needs to walk a module call's
// `providers = {...}` mapping up to the root (GitHub issue #188).
func declaringBlock(req Request, addr addrs.AbsResourceInstance) (*configs.Resource, *configs.Config, bool) {
	if req.Config == nil {
		return nil, nil, false
	}
	modCfg, ok := identity.ConfigForModule(req.Config, addr.Module)
	if !ok || modCfg.Module == nil {
		return nil, nil, false
	}
	block := modCfg.Module.ManagedResources[addr.Resource.Resource.String()]
	if block == nil {
		return nil, nil, false
	}
	return block, modCfg, true
}

// strandedSighting is one out-of-scope sighting of one address, flattened
// with the labels the message needs.
type strandedSighting struct {
	addr     addrs.AbsResourceInstance
	typeName string
	importID string
	marker   string
	seenBy   string
	owner    addrs.AbsProviderConfig
}

// strandedAcrossProviderConfigs refuses the run for every declared address
// whose marked live object was sighted only by provider configurations that
// do not declare it. See this file's doc comment for why that predicate is
// the honest reading of a multi-pass sweep, and why the answer is a refusal.
func strandedAcrossProviderConfigs(estate string, passes []Pass, res *Result, diags *tfdiags.Diagnostics) {
	// The label each provider configuration answers to in a message: its
	// own pass's region where a caller gave one, its configuration address
	// otherwise. Built over the passes because the owning configuration is
	// always one of them - every distinct provider configuration among the
	// estate's managed resources gets a pass - but falling back to the bare
	// address rather than assuming it.
	labelOf := make(map[string]string, len(passes))
	for _, p := range passes {
		labelOf[p.Provider.String()] = p.label()
	}

	sawInScope := make(map[string]bool)
	strandedOf := make(map[string][]strandedSighting)
	seen := make(map[string]bool)
	for _, p := range passes {
		for _, s := range p.Result.DeclaredSightings {
			key := s.Addr.String()
			if s.InScope {
				sawInScope[key] = true
				continue
			}
			// One pass can sight one object more than once - a
			// config-driven scan and the estate sweep both reach a declared
			// address - and that is one fact, not two.
			dedupe := key + "\x00" + p.Provider.String() + "\x00" + s.ImportID
			if seen[dedupe] {
				continue
			}
			seen[dedupe] = true
			strandedOf[key] = append(strandedOf[key], strandedSighting{
				addr:     s.Addr,
				typeName: s.TypeName,
				importID: s.ImportID,
				marker:   s.Marker,
				seenBy:   p.label(),
				owner:    s.Provider,
			})
		}
	}

	// Sorted so several stranded addresses produce their problems and
	// diagnostics in the same order on every run, whatever order Go's map
	// iteration happened to take.
	keys := make([]string, 0, len(strandedOf))
	for k := range strandedOf {
		if sawInScope[k] {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, key := range keys {
		found := strandedOf[key]
		first := found[0]
		if _, ran := labelOf[first.owner.String()]; !ran {
			// The address's own provider configuration contributed no pass
			// at all - statelessDiscover drops one whose configuration
			// depends on a managed resource this run has not created yet.
			// Silence from a pass that never ran is not evidence the
			// address's own configuration cannot find its object, and this
			// finding rests entirely on that pass having looked.
			continue
		}

		ids := make([]string, 0, len(found))
		for _, s := range found {
			id := s.importID
			if id == "" {
				id = "(no identity)"
			}
			ids = append(ids, fmt.Sprintf("%s in %s", id, s.seenBy))
		}
		sort.Strings(ids)

		ownerLabel := labelOf[first.owner.String()]
		if ownerLabel == "" {
			ownerLabel = first.owner.String()
		}
		places := make([]string, 0, len(found))
		for _, s := range found {
			places = append(places, s.seenBy)
		}
		sort.Strings(places)
		places = dedupeStrings(places)

		detail := fmt.Sprintf(
			"%s carries estate %q and the address %q, and the only pass that listed it was this estate's own pass over %s. The configuration declares %s under %s, and that pass found nothing for it. Marker discovery looks where a provider configuration points, so no run of this configuration reaches that resource at that address again, and creating %s in %s would leave two live resources carrying this estate's marker for one address - a tofu-address marker names one live resource per estate regardless of region or account (live/MARKERS.md, \"Ownership semantics\"). Destroy it in %s, remove its tofu-estate and tofu-address tags to disown it, or declare %s under a provider configuration that reaches %s again.",
			liveResourcePhrase(found), estate, first.marker, strings.Join(places, " and "),
			first.addr, ownerLabel,
			first.addr, ownerLabel,
			strings.Join(places, " and "),
			first.addr, strings.Join(places, " and "))

		res.Problems = append(res.Problems, Problem{
			Kind:     ProblemOutOfScopeMarker,
			TypeName: first.typeName,
			Addr:     first.addr,
			Marker:   first.marker,
			LiveIDs:  ids,
			Detail:   detail,
		})
		*diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, problemSummaries[ProblemOutOfScopeMarker], detail))
	}
}

// liveResourcePhrase names the stranded objects at the head of the detail:
// "A live aws_vpc (vpc-0abc)" for the ordinary one, a count for the several
// that one address can collect when more than one out-of-scope pass sights
// an object of it.
func liveResourcePhrase(found []strandedSighting) string {
	if len(found) == 1 {
		id := found[0].importID
		if id == "" {
			return fmt.Sprintf("A live %s", found[0].typeName)
		}
		return fmt.Sprintf("A live %s (%s)", found[0].typeName, id)
	}
	return fmt.Sprintf("%d live %s resources", len(found), found[0].typeName)
}

// dedupeStrings collapses runs of equal entries in a sorted slice, into a
// slice of its own rather than in place: the caller's input is a projection
// of another pass's Result and is not this function's to rewrite.
func dedupeStrings(sorted []string) []string {
	out := make([]string, 0, len(sorted))
	for _, s := range sorted {
		if len(out) > 0 && s == out[len(out)-1] {
			continue
		}
		out = append(out, s)
	}
	return out
}
