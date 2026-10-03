// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"fmt"
	"sort"
	"strings"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/substrate"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// SummaryIdentityUnresolvedNoAddress is the refusal GitHub issue #1539's
// ruling (2026-09-26, option 1) makes stand.
//
// The #388 plan-node seam downgrades a static identity refusal to a
// warning and lets the node try again: a record, the marker sweep's index,
// the identity table over the evaluated value. When all three come back
// empty the node plans a create. Where the marker carries tofu-address that
// is survivable, because the next sweep finds the object by its address
// and binds it to the block. Where the marker is tofu-estate alone (the
// Kubernetes label and manifest surfaces, #1016's ruling), the sweep finds
// the object by its estate label and has nothing tying it to this block,
// so it files it as an orphan: the plans alternate between a create over
// the existing object and an orphan destroy of it, each run exiting 0.
//
// So on such a surface the static refusal stands, as an error, at the
// node. An instance the node does resolve (record, marker index, table
// over value) never reaches this; an instance the static evaluator never
// refused (a greenfield parent-derived one) is not in
// [NodeResolver.StaticRefusals] and plans its create as before; a surface
// whose marker carries the address (the AWS tag map) is untouched.
//
// GitHub issue #1641 (#1605 step 3): a Kubernetes object now carries its
// block address in an annotation (#1639), the sweep binds on it (#1640),
// and substrate.Kubernetes.CarriesAddress is true. What stays true is that
// an object can lack the annotation - one an older build created, or one
// migrated from stock state before live-import stamped it - and such an
// object is exactly as indistinguishable as before. So on a surface that
// carries the address outside its marker map the refusal stands per
// object: only when the sweep found an object that could be this
// instance's and carries no annotation, or could not list a kind the
// instance's type can declare ([NodeResolver.UnaddressedObjects]).
// Otherwise the create is safe: the object it makes carries the
// annotation, and the next sweep binds it.
const SummaryIdentityUnresolvedNoAddress = "Identity not resolvable, and the marker carries no address"

// refuseAddresslessMarker is the #1539 check. It asks the substrate, from
// the type's own schema, whether its marker surface carries an address;
// a type with no marker surface at all is not this check's business
// (it has its own rung, the record).
func (n *NodeResolver) refuseAddresslessMarker(addr addrs.AbsResourceInstance, schema providers.Schema) tfdiags.Diagnostic {
	refused := n.StaticRefusals[addr.String()]
	if len(refused) == 0 {
		return nil
	}
	surface, ok := substrate.SurfaceOf(n.providerType(addr), schema.Block)
	if !ok || substrate.AddressInMarkers(surface) {
		return nil
	}
	// The object carries its address outside the marker map (the
	// Kubernetes annotation): ask the sweep about the objects themselves.
	var unaddressed []string
	if substrate.CarriesAddress(surface) {
		objects, listed := n.UnaddressedObjects[addr.String()]
		if listed && len(objects) == 0 {
			return nil
		}
		unaddressed = objects
		if !listed {
			unaddressed = nil
		}
	}

	seen := map[string]bool{}
	var why []string
	for _, d := range refused {
		desc := d.Description()
		line := desc.Summary
		if desc.Detail != "" {
			line += ": " + strings.Join(strings.Fields(desc.Detail), " ")
		}
		if !seen[line] {
			seen[line] = true
			why = append(why, line)
		}
	}
	sort.Strings(why)

	// The wording is the substrate's (GitHub issue #1705): where the
	// address is carried, and what one marker entry is called.
	addressKey, addressNoun := substrate.AddressCarrier(surface)
	markerNoun := substrate.MarkerNoun(surface)
	var marker string
	switch {
	case !substrate.CarriesAddress(surface):
		marker = fmt.Sprintf("%s's marker is the %s surface, which carries tofu-estate but no tofu-address, so a live object this block already created cannot be told apart from a new one", addr.Resource.Resource.Type, surface)
	case len(unaddressed) > 0:
		marker = fmt.Sprintf("A %s carries its block address in the %s %s, and this run's sweep found %s carrying this estate's %s %s and no %s that binds it to this block, so if this block created %s, it cannot be told apart from a new object", addr.Resource.Resource.Type, addressKey, addressNoun, quotedObjects(unaddressed), markers.TagEstate, markerNoun, addressNoun, oneOrAny(unaddressed))
	default:
		marker = fmt.Sprintf("A %s carries its block address in the %s %s, but this run's sweep could not list every kind %s can declare, so whether a live object this block already created lacks that %s cannot be told", addr.Resource.Resource.Type, addressKey, addressNoun, addr.Resource.Resource.Type, addressNoun)
	}

	return tfdiags.Sourceless(tfdiags.Error, SummaryIdentityUnresolvedNoAddress, fmt.Sprintf(
		"%s's identity could not be resolved from configuration, and this run found no record and no marker entry for it. %s, and planning a create would either collide with it or leave it behind as an orphan. Make the identity-bearing arguments resolvable from variables, locals, or another resource's identity attribute, or run \"choudoufu live-import\" from the stock state that holds the object.\n\nWhy the identity did not resolve:\n  - %s\n\nSee live/LIMITATIONS.md, %q.",
		addr, marker, strings.Join(why, "\n  - "), SummaryIdentityUnresolvedNoAddress,
	))
}

// quotedObjects names the sweep's objects as a sentence reads them.
func quotedObjects(objects []string) string {
	switch len(objects) {
	case 1:
		return objects[0]
	case 2:
		return objects[0] + " and " + objects[1]
	}
	return strings.Join(objects[:len(objects)-1], ", ") + " and " + objects[len(objects)-1]
}

func oneOrAny(objects []string) string {
	if len(objects) == 1 {
		return "it"
	}
	return "any of them"
}
