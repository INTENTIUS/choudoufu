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
	surface, ok := substrate.SurfaceOf(schema.Block)
	if !ok || substrate.CarriesAddress(surface) {
		return nil
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

	return tfdiags.Sourceless(tfdiags.Error, SummaryIdentityUnresolvedNoAddress, fmt.Sprintf(
		"%s's identity could not be resolved from configuration, and this run found no record and no marker entry for it. %s's marker is the %s surface, which carries tofu-estate but no tofu-address, so a live object this block already created cannot be told apart from a new one, and planning a create would either collide with it or leave it behind as an orphan. Make the identity-bearing arguments resolvable from variables, locals, or another resource's identity attribute, or run \"choudoufu live-import\" from the stock state that holds the object.\n\nWhy the identity did not resolve:\n  - %s\n\nSee live/LIMITATIONS.md, %q.",
		addr, addr.Resource.Resource.Type, surface, strings.Join(why, "\n  - "), SummaryIdentityUnresolvedNoAddress,
	))
}
