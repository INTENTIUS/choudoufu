// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"fmt"
	"sort"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/substrate"
)

// ControllerHeldResource is one live resource the sweep found held by a
// controller rather than a block, on either substrate (the 2026-09-26
// ruling on GitHub issue #1604, "controller-held by default on both"):
//
//   - on AWS, a resource carrying an in-cluster controller's tags
//     ([markers.ControllerTagKeys]): made by ACK or Crossplane from an
//     object on the cluster side (#1606);
//   - on Kubernetes, an object carrying this estate's label and Helm's
//     release annotation: installed by that release (#1607).
//
// It is never an orphan and never unclaimed, so it is never proposed for
// destroy and never offered for adoption.
type ControllerHeldResource struct {
	// TypeName is the resource type it was listed as.
	TypeName string

	// ImportID is its live identity, empty if the provider sent none. A
	// Kubernetes object's is its NAMESPACE/NAME natural key.
	ImportID string

	// DisplayName is the provider's label. Display only.
	DisplayName string

	// Kind is the Kubernetes kind for an object the Kubernetes leg found,
	// empty on AWS.
	Kind string

	// Controller is the controller that holds it: ACK, Crossplane, Helm.
	Controller string

	// HeldBy names the controller and the object of its that holds this
	// resource, in one line an operator can act on: "Helm release web/web",
	// "ACK s3 controller (s3-v1.0.14), custom resource in namespace team-a".
	HeldBy string

	// Marked is true when it also carries this estate's markers at an
	// address the configuration does not declare: the sweep's orphan
	// shape, withheld from the removal set. Addr is that address.
	Marked bool
	Addr   addrs.AbsResourceInstance

	// Resource is the full listed object, carried through from the
	// [OwnedResource] or [UnclaimedResource] this was built from, so a
	// consumer can match on content without listing again - GitHub issue
	// #1628, the lookalike guard's own content match against a create's
	// identity-bearing arguments. cty.NilVal when the provider sent none.
	Resource cty.Value
}

// String renders one controller-held resource on one line.
func (c ControllerHeldResource) String() string {
	id := c.ImportID
	if id == "" {
		id = "(no identity)"
	}
	return c.TypeName + " " + id + " CONTROLLER-HELD (" + c.HeldBy + ")"
}

// controllerHeldWithheld is the sentence an orphan gets in place of a
// destroy.
func controllerHeldWithheld(h substrate.Hold) string {
	return fmt.Sprintf("controller-held: made by %s. Deleting it here would race the controller, which recreates what its object still asks for; remove or change the object on the cluster side instead.", h.HeldBy)
}

// applyControllerHeld takes every controller-held resource out of the two
// populations that would act on it or offer it: an orphan becomes a
// withheld one, with its prior-state resolution removed so the plan does
// not destroy it, and an unclaimed resource leaves [Report.Unclaimed], so
// neither the foreign report nor the adoption offer ever sees it. Both are
// recorded in [Report.ControllerHeld].
//
// Whether a resource is held, and by what, is the families' answer
// ([substrate.ControllerHeld], GitHub issue #1706) asked of its tags: on
// AWS, the ACK and Crossplane tag keys.
//
// It runs right after classifyOrphans and before the parent-read legs, so
// a withheld parent's resolution is gone before those legs look for
// children of a removed parent to remove with it.
func applyControllerHeld(res *Result) {
	withheld := map[string]bool{}
	for i := range res.Orphans {
		o := &res.Orphans[i]
		hold, ok := substrate.ControllerHeld(substrate.HoldEvidence{Tags: o.Tags})
		if !ok {
			continue
		}
		if o.Removal && o.Addressable {
			withheld[o.Addr.String()] = true
		}
		o.Removal = false
		o.Withheld = controllerHeldWithheld(hold)
		res.ControllerHeld = append(res.ControllerHeld, ControllerHeldResource{
			TypeName:    o.TypeName,
			ImportID:    o.ImportID,
			DisplayName: o.DisplayName,
			Controller:  hold.Controller,
			HeldBy:      hold.HeldBy,
			Marked:      true,
			Addr:        o.Addr,
			Resource:    o.Resource,
		})
	}
	if len(withheld) > 0 {
		kept := res.Resolutions[:0]
		for _, r := range res.Resolutions {
			if r.Undeclared && withheld[r.Addr.String()] {
				continue
			}
			kept = append(kept, r)
		}
		res.Resolutions = kept
	}

	if len(res.Unclaimed) > 0 {
		kept := res.Unclaimed[:0]
		for _, u := range res.Unclaimed {
			hold, ok := substrate.ControllerHeld(substrate.HoldEvidence{Tags: u.Tags})
			if !ok {
				kept = append(kept, u)
				continue
			}
			res.ControllerHeld = append(res.ControllerHeld, ControllerHeldResource{
				TypeName:    u.TypeName,
				ImportID:    u.ImportID,
				DisplayName: u.DisplayName,
				Controller:  hold.Controller,
				HeldBy:      hold.HeldBy,
				Resource:    u.Resource,
			})
		}
		res.Unclaimed = kept
	}

	sortControllerHeld(res.ControllerHeld)
}

func sortControllerHeld(held []ControllerHeldResource) {
	sort.SliceStable(held, func(i, j int) bool {
		if held[i].TypeName != held[j].TypeName {
			return held[i].TypeName < held[j].TypeName
		}
		return held[i].ImportID < held[j].ImportID
	})
}
