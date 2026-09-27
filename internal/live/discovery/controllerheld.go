// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"fmt"
	"sort"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/markers"
)

// ControllerHeldResource is one live resource the sweep found carrying an
// in-cluster controller's tags ([markers.ControllerTagKeys]): made by ACK
// or Crossplane from an object on the cluster side. GitHub issue #1606,
// ruled on #1604: it is never an orphan and never unclaimed, so it is never
// proposed for destroy and never offered for adoption.
type ControllerHeldResource struct {
	// TypeName is the resource type it was listed as.
	TypeName string

	// ImportID is its live identity, empty if the provider sent none.
	ImportID string

	// DisplayName is the provider's label. Display only.
	DisplayName string

	// Hold is what the tags say about the controller and its object.
	Hold markers.ControllerHold

	// Marked is true when it also carries this estate's markers at an
	// address the configuration does not declare: the sweep's orphan
	// shape, withheld from the removal set. Addr is that address.
	Marked bool
	Addr   addrs.AbsResourceInstance
}

// String renders one controller-held resource on one line.
func (c ControllerHeldResource) String() string {
	id := c.ImportID
	if id == "" {
		id = "(no identity)"
	}
	return c.TypeName + " " + id + " CONTROLLER-HELD (" + c.Hold.Describe() + ")"
}

// controllerHeldWithheld is the sentence an orphan gets in place of a
// destroy.
func controllerHeldWithheld(h markers.ControllerHold) string {
	return fmt.Sprintf("controller-held: made by %s. Deleting it here would race the controller, which recreates what its object still asks for; remove or change the object on the cluster side instead.", h.Describe())
}

// applyControllerHeld takes every controller-held resource out of the two
// populations that would act on it or offer it: an orphan becomes a
// withheld one, with its prior-state resolution removed so the plan does
// not destroy it, and an unclaimed resource leaves [Report.Unclaimed], so
// neither the foreign report nor the adoption offer ever sees it. Both are
// recorded in [Report.ControllerHeld].
//
// It runs right after classifyOrphans and before the parent-read legs, so
// a withheld parent's resolution is gone before those legs look for
// children of a removed parent to remove with it.
func applyControllerHeld(res *Result) {
	withheld := map[string]bool{}
	for i := range res.Orphans {
		o := &res.Orphans[i]
		hold, ok := markers.ControllerHeld(o.Tags)
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
			Hold:        hold,
			Marked:      true,
			Addr:        o.Addr,
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
			hold, ok := markers.ControllerHeld(u.Tags)
			if !ok {
				kept = append(kept, u)
				continue
			}
			res.ControllerHeld = append(res.ControllerHeld, ControllerHeldResource{
				TypeName:    u.TypeName,
				ImportID:    u.ImportID,
				DisplayName: u.DisplayName,
				Hold:        hold,
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
